package runtime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
	"github.com/sushidev-team/lola/internal/worktree"
)

// STACK SESSIONS (config kind = "stack", see internal/config/stack.go). A
// stack session is a plain FOLDER,
//
//	~/.lola/worktrees/<stack>/<session id>/
//	  .lola/           prompt.md, env, settings, the context link — the agent's
//	  <member name>/   one worktree per app the issue's labels selected
//
// and the agent and every shell tab start in it, so all of the session's
// repositories are one `cd` away. Nothing in the folder itself is versioned:
// the apps are the repositories. Three rules hold it together:
//
//   - Each app is set up as its OWN project: its symlinks, its post_create
//     with its env and its own port lease (EnvVars.App), and its own
//     .lola/env inside its worktree, which is what its dev tabs source.
//   - Teardown removes every app dirty-safe BEFORE the folder, and keeps the
//     folder (and the apps still in it) on the first refusal — a removed
//     folder takes everything under it along with no dirty check. An app
//     without a PR whose branch carries commits is kept as well: clean, but
//     unshipped.
//   - WHICH apps a session has is decided once, at spawn; afterwards the
//     folders on disk are the truth (adoption rebuilds the record from them,
//     the sandbox mounts only those).

// ErrNoStackApp is returned by a stack spawn for an issue none of the stack's
// members selects (their labels did not match, and none takes every issue).
// Dispatch checks first and skips such an issue with a reason; this is the
// runtime's own guard.
var ErrNoStackApp = errors.New("no app of the stack is selected by the issue's labels")

// StackAppDir is where app lives inside the stack session folder dir.
func StackAppDir(dir, app string) string { return filepath.Join(dir, app) }

// StackAppsFor returns the members of stack an issue needs, in config order:
// each member whose labels the issue carries, plus every member with no
// labels (always included).
func StackAppsFor(cfg *config.Config, stack string, issue linear.Issue) []config.Project {
	return config.SelectApps(cfg.StackMembers(stack), issue.LabelIDs, issue.LabelNames, issue.LabelGroups)
}

// appBase is the branch an app's worktree is cut from and its PR targets.
func appBase(m config.Project) string {
	if m.DefaultBranch != "" {
		return m.DefaultBranch
	}
	return "main"
}

// needsCheckout refuses a launch path that needs the project's own checkout
// (open a branch or PR, fork a checkpoint) for a stack, which has none.
func needsCheckout(p config.Project, op string) error {
	if p.IsStack() {
		return fmt.Errorf("runtime: %s: %s is a stack and has no checkout of its own — use one of its member projects", op, p.Name)
	}
	return nil
}

// stackBranchTaken reports whether branch exists in any member repository of
// the stack — freeSessionSlot's collision check, so a retry suffix is chosen
// when an earlier attempt's branch was kept in one of them.
func (n *Native) stackBranchTaken(ctx context.Context, p config.Project, branch string) (bool, error) {
	for _, m := range n.Cfg.StackMembers(p.Name) {
		if m.Path == "" {
			continue
		}
		taken, err := n.WT.BranchExists(ctx, config.Project{Name: m.Name, Path: m.Path}, branch)
		if err != nil || taken {
			return taken, err
		}
	}
	return false, nil
}

// spawnStack is SpawnWith for a stack: the session folder, one worktree per
// selected app, the agent in the folder.
func (n *Native) spawnStack(ctx context.Context, p config.Project, issue linear.Issue, o SpawnOpts) (session.Session, error) {
	if o.Group != "" {
		return session.Session{}, fmt.Errorf("runtime: spawn %s: best-of is not supported for a stack", issue.Identifier)
	}
	apps := StackAppsFor(n.Cfg, p.Name, issue)
	if len(apps) == 0 {
		return session.Session{}, fmt.Errorf("runtime: spawn %s in stack %s: %w", issue.Identifier, p.Name, ErrNoStackApp)
	}
	kind := n.resolveKind(p.Name, o.Agent)
	baseBranch := issue.BranchName
	if baseBranch == "" {
		baseBranch = n.Cfg.BranchPrefixForProject(p.Name) + strings.ToLower(issue.Identifier)
	}
	id, branch, err := n.freeSessionSlot(ctx, p, SessionID(p.Name, issue.Identifier), baseBranch)
	if err != nil {
		return session.Session{}, fmt.Errorf("runtime: spawn %s: %w", issue.Identifier, err)
	}
	dir := filepath.Join(n.WT.Root, p.Name, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return session.Session{}, fmt.Errorf("runtime: spawn %s: create session folder: %w", id, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, lolaDir), 0o700); err != nil {
		return session.Session{}, fmt.Errorf("runtime: spawn %s: create %s: %w", id, lolaDir, err)
	}

	rec := session.Session{ID: id, Project: p.Name, Issue: issue.Identifier, Branch: branch, Stack: true, Kind: session.KindLinear}
	fail := func(step string, cause error) (session.Session, error) {
		if rbErr := n.removeStack(ctx, rec, false); rbErr != nil {
			return session.Session{}, fmt.Errorf("runtime: spawn %s: %s: %w (rollback failed: %v; session folder kept at %s)", id, step, cause, rbErr, dir)
		}
		return session.Session{}, fmt.Errorf("runtime: spawn %s: %s: %w (rolled back)", id, step, cause)
	}

	created, appErr := n.createApps(ctx, p, apps, dir, id, issue.Identifier, branch)
	rec.Apps = created
	if appErr != nil {
		return fail("create app worktrees", appErr)
	}
	ctxKey := contextKeyFor(issue.Identifier, id)
	ctxDir := n.setupContext(dir, p.Name, ctxKey)
	brief := withStackBriefing(promptMD(p, issue, branch), n.Cfg.StackMembers(p.Name), created)
	prompt := n.withReportBriefing(withContextBriefing(brief, ctxDir))
	gate := session.PlanNone
	if p.RequirePlan {
		prompt = n.withPlanBriefing(prompt)
		gate = session.PlanPlanning
	}
	if err := os.WriteFile(filepath.Join(dir, lolaDir, "prompt.md"), prompt, 0o600); err != nil {
		return fail("write prompt.md", err)
	}
	if err := n.writeAgentArtifacts(dir, kind, ctxDir); err != nil {
		return fail("write agent artifacts", err)
	}
	sp := n.sessionProject(p, EnvVars{Session: id, Issue: issue.Identifier, Branch: branch, Project: p.Name, Worktree: dir})
	if err := os.WriteFile(filepath.Join(dir, lolaDir, "env"), n.envFile(sp, id, dir, kind), 0o600); err != nil {
		return fail("write env", err)
	}
	model := ""
	if agent.ValidModel(o.Model) {
		model = o.Model
	}
	for _, a := range created {
		n.recordBaseline(ctx, a.Worktree, id)
	}
	if err := n.Tmux.NewSession(ctx, id, dir, n.launchLine(p.Name, id, kind, defaultLaunchPrompt(id), model, false)); err != nil {
		if n.Tmux.Has(ctx, id) {
			if killErr := n.Tmux.KillSession(ctx, id); killErr != nil {
				return session.Session{}, fmt.Errorf("runtime: spawn %s: start tmux session: %w (rollback failed: %v; tmux session and folder %s kept)", id, err, killErr, dir)
			}
		}
		return fail("start tmux session", err)
	}
	if err := n.Tmux.ConfigureSession(ctx, id, n.Cfg.SessionChrome(issue.Identifier)); err != nil && n.Logf != nil {
		n.Logf("session %s: status-bar styling failed (cosmetic, session is up): %v", id, err)
	}
	return withAgentState(session.Session{
		PlanGate:   gate,
		ID:         id,
		Source:     "native",
		Kind:       session.KindLinear,
		Project:    p.Name,
		Issue:      issue.Identifier,
		Title:      issue.Title,
		IssueUUID:  issue.ID,
		Branch:     branch,
		Worktree:   dir,
		TmuxName:   id,
		Agent:      string(kind),
		ContextKey: ctxKey,
		Model:      model,
		Sandbox:    n.SandboxFor(p.Name),
		Stack:      true,
		Apps:       created,
	}, state.AgentStarting), nil
}

// createApps checks each selected member out into the session folder on
// branch and prepares it as its own project: pinned when the stack is
// sandboxed, its own env (lease included), symlinks, post_create, and its
// own 0600 .lola/env for its dev tabs. On a failure it returns the apps it
// DID create alongside the error: the caller's rollback removes them.
func (n *Native) createApps(ctx context.Context, stack config.Project, members []config.Project, dir, id, issue, branch string) ([]session.App, error) {
	var out []session.App
	for _, m := range members {
		wt := StackAppDir(dir, m.Name)
		base := appBase(m)
		if err := n.WT.CreateApp(ctx, m.Path, wt, branch, base); err != nil {
			return out, fmt.Errorf("app %s: %w", m.Name, err)
		}
		out = append(out, session.App{Name: m.Name, Repo: m.Repo, Path: m.Path, Worktree: wt, Branch: branch, Base: base, Delivery: state.DeliveryNone})
		pin, err := n.pinSandbox(stack.Name, wt)
		if err != nil {
			return out, fmt.Errorf("app %s: pin sandbox git layout: %w", m.Name, err)
		}
		out[len(out)-1].GitPin = pin
		n.pruneCheckpoints(ctx, wt, id) // a fresh session starts with no history
		mp := n.sessionProject(m, EnvVars{Session: id, App: m.Name, Issue: issue, Branch: branch, Project: m.Name, Worktree: wt})
		if err := n.WT.Prepare(ctx, mp, wt); err != nil {
			return out, fmt.Errorf("app %s: %w", m.Name, err)
		}
		if err := excludeLolaDir(wt); err != nil {
			return out, fmt.Errorf("app %s: git info/exclude: %w", m.Name, err)
		}
		if err := os.MkdirAll(filepath.Join(wt, lolaDir), 0o700); err != nil {
			return out, fmt.Errorf("app %s: create %s: %w", m.Name, lolaDir, err)
		}
		if err := os.WriteFile(filepath.Join(wt, lolaDir, "env"), manualEnvFile(mp), 0o600); err != nil {
			return out, fmt.Errorf("app %s: write env: %w", m.Name, err)
		}
	}
	return out, nil
}

// AppProject is the member project an app of a stack session belongs to, at
// the path the app was cut from — the member's current config when it still
// exists (its pre_remove, its env), else a bare project carrying the path.
func (n *Native) AppProject(a session.App) config.Project {
	if n.Cfg != nil {
		if m := n.Cfg.ProjectByName(a.Name); m != nil {
			mp := *m
			if a.Path != "" {
				mp.Path = a.Path
			}
			return mp
		}
	}
	return config.Project{Name: a.Name, Path: a.Path}
}

// AppEnv renders an app's member env for an existing session — what its
// post_create saw, from its recorded lease (never a new one).
func (n *Native) AppEnv(s session.Session, a session.App) config.Project {
	v := EnvVars{Session: s.ID, App: a.Name, Issue: s.Issue, Branch: a.Branch, Project: a.Name, Worktree: a.Worktree}
	if n.Ports != nil {
		if l, ok := n.Ports.Get(v.LeaseKey()); ok {
			v.Ports = l.Ports
		}
	}
	return withSessionEnv(n.AppProject(a), v)
}

// killStack is Kill's worktree half for a stack session (the tmux sessions
// and the container are already down): every app first, then the folder.
func (n *Native) killStack(ctx context.Context, s session.Session, force bool) error {
	if err := n.removeStack(ctx, s, force); err != nil {
		return fmt.Errorf("runtime: kill %s: %w", s.ID, err)
	}
	return nil
}

// removeStack tears a stack session's folder down: each app dirty-safe (its
// pre_remove first, unless the session is sandboxed), then the folder itself
// — but only when every app is gone and nothing but lola's own files is left
// in it. Every app is attempted; the errors are joined, and any of them
// keeps the folder.
func (n *Native) removeStack(ctx context.Context, s session.Session, force bool) error {
	dir := filepath.Join(n.WT.Root, s.Project, s.ID)
	sandboxed := s.Sandbox != "" || n.sandboxProject(s.Project) != nil
	var errs []error
	for _, a := range s.Apps {
		if err := n.removeApp(ctx, s, a, sandboxed, force); err != nil {
			errs = append(errs, fmt.Errorf("app %s: %w", a.Name, err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return n.removeStackFolder(s, dir, force)
}

// removeApp removes one app's worktree and (when the session owns it) its
// branch, releasing its lease and its checkpoints once it is gone.
func (n *Native) removeApp(ctx context.Context, s session.Session, a session.App, sandboxed, force bool) error {
	if a.Path == "" || a.Worktree == "" {
		return nil
	}
	mp := n.AppProject(a)
	branch := ""
	if s.OwnsBranch() {
		branch = a.Branch
	}
	key := session.LaneID(s.ID, a.Name)
	// Committed but unshipped: the worktree may be clean while its branch
	// holds the only copy of the work. Only an app with no PR is asked — a
	// PR means the commits are on the remote.
	if !force && branch != "" && a.PR == nil {
		if ahead, err := n.WT.Ahead(ctx, a.Path, appBaseOf(a), branch); err == nil && ahead > 0 {
			return fmt.Errorf("branch %s has %d commit(s) in no pull request: %w", branch, ahead, worktree.ErrDirty)
		}
	}
	if _, err := os.Stat(a.Worktree); errors.Is(err, fs.ErrNotExist) {
		n.releasePorts(key)
		if err := n.WT.DeleteBranch(ctx, mp, branch); err != nil {
			return err
		}
		n.pruneCheckpoints(ctx, a.Path, s.ID)
		return nil
	}
	removeForce := force
	switch {
	case len(mp.PreRemove) == 0:
	case sandboxed:
		n.logf("session %s: app %s: pre_remove skipped: sandboxed worktree (host commands there would run agent-writable code)", s.ID, a.Name)
	case force:
		n.runAppPreRemove(ctx, s, a)
	default:
		if dirty, derr := n.WT.Dirty(ctx, a.Worktree); derr == nil && !dirty {
			n.runAppPreRemove(ctx, s, a)
			removeForce = true
		}
	}
	if err := n.WT.Remove(ctx, mp, a.Worktree, branch, removeForce); err != nil {
		return err
	}
	n.releasePorts(key)
	gitguard.Clear(a.Worktree)
	n.pruneCheckpoints(ctx, a.Path, s.ID)
	return nil
}

func appBaseOf(a session.App) string {
	if a.Base != "" {
		return a.Base
	}
	return "main"
}

// runAppPreRemove runs an app's pre_remove in its worktree with its own env
// (runPreRemove's contract: bounded, best-effort, every failure logged).
func (n *Native) runAppPreRemove(ctx context.Context, s session.Session, a session.App) {
	hctx, cancel := context.WithTimeout(ctx, preRemoveTimeout)
	defer cancel()
	for _, err := range n.WT.Teardown(hctx, n.AppEnv(s, a), a.Worktree) {
		n.logf("session %s: app %s: pre_remove: %v (teardown continues)", s.ID, a.Name, err)
	}
}

// stackOwnFiles are the folder entries lola (or the agent's own tooling)
// writes into a stack session folder: removing them loses no work.
var stackOwnFiles = []string{lolaDir, openCodeDir, ".claude", ".codex"}

// removeStackFolder deletes the session folder once its apps are gone. Any
// entry that is not lola's own — files the agent wrote beside the apps, an
// app folder the record does not account for — is unversioned work no dirty
// check has seen, so the folder is kept (ErrDirty) unless force.
func (n *Native) removeStackFolder(s session.Session, dir string, force bool) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var left []string
	for _, e := range entries {
		if !slices.Contains(stackOwnFiles, e.Name()) {
			left = append(left, e.Name())
		}
	}
	if len(left) > 0 && !force {
		return fmt.Errorf("session folder %s holds files outside its apps (%s): %w", dir, strings.Join(left, ", "), worktree.ErrDirty)
	}
	if err := n.WT.RemoveDir(dir); err != nil {
		return err
	}
	n.releasePorts(s.ID)
	return nil
}

// adoptStack rebuilds the records of a stack's session folders for Adopt: an
// app per folder named after one of the stack's members. Branch is left
// empty (it is not observable without running git in a worktree the agent
// may have tampered with), so a teardown of such a record keeps the branch;
// GitPin is nil, so a sandboxed one's host git is DENIED until a human looks
// (the daemon's pin sync). The daemon merges a surviving store record over
// these, which restores both.
func (n *Native) adoptStack(p config.Project, live map[string]bool) []session.Session {
	root := filepath.Join(n.WT.Root, p.Name)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	members := n.Cfg.StackMembers(p.Name)
	var out []session.Session
	for _, e := range entries {
		id := e.Name()
		if !e.IsDir() || !strings.HasPrefix(id, sessionPrefix) {
			continue
		}
		dir := filepath.Join(root, id)
		a := state.AgentDead
		if live[id] {
			a = state.AgentWorking
		}
		out = append(out, withAgentState(session.Session{
			ID:       id,
			Source:   "native",
			Project:  p.Name,
			Issue:    issueFromSessionID(id, p.Name),
			Worktree: dir,
			TmuxName: id,
			Stack:    true,
			Apps:     discoverApps(members, dir),
		}, a))
	}
	return out
}

// discoverApps lists the apps checked out in a stack session folder: every
// <dir>/<member name> that exists, in member order.
func discoverApps(members []config.Project, dir string) []session.App {
	var out []session.App
	for _, m := range members {
		wt := StackAppDir(dir, m.Name)
		if fi, err := os.Stat(wt); err == nil && fi.IsDir() {
			out = append(out, session.App{Name: m.Name, Repo: m.Repo, Path: m.Path, Worktree: wt, Base: appBase(m), Delivery: state.DeliveryNone})
		}
	}
	return out
}

// stackWorkSummary is WorkSummary for a stack session: each app's git facts
// under its folder name, so the agent taking over sees which repository each
// commit and change is in.
func (n *Native) stackWorkSummary(ctx context.Context, s session.Session) (logOut, statusOut, diffStat string) {
	var lb, sb, db strings.Builder
	add := func(b *strings.Builder, app, text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		fmt.Fprintf(b, "[%s/]\n%s\n", app, strings.TrimRight(text, "\n"))
	}
	for _, a := range s.Apps {
		if a.Worktree == "" {
			continue
		}
		if _, err := os.Stat(a.Worktree); err != nil {
			continue
		}
		l, st, ds := n.WT.WorkSummary(ctx, a.Worktree, appBaseOf(a))
		add(&lb, a.Name, l)
		add(&sb, a.Name, st)
		add(&db, a.Name, ds)
	}
	return lb.String(), sb.String(), db.String()
}

// stackPresent is the subset of a stack's members actually checked out in
// the session folder dir — what a sandbox launch mounts. Read from disk
// because every launch (spawn, revive, switch-agent) must agree with the
// spawn's selection, and the record is not at hand on all of those paths. A
// folder the agent creates there names only a repo the stack already has.
func stackPresent(members []config.Project, dir string) []config.Project {
	var out []config.Project
	for _, m := range members {
		if fi, err := os.Stat(StackAppDir(dir, m.Name)); err == nil && fi.IsDir() {
			out = append(out, m)
		}
	}
	return out
}

// withStackBriefing appends the stack section to a prompt.md body: the
// session folder holds one checkout per app, each already on the session's
// branch, and each app the agent changes needs its own PR — cross-linked, so
// a reviewer of one finds the others. The stack's other apps (not selected
// for this issue) are named too, so an agent that finds it needs one says so
// instead of cloning it somewhere.
func withStackBriefing(prompt []byte, members []config.Project, apps []session.App) []byte {
	have := map[string]bool{}
	for _, a := range apps {
		have[a.Name] = true
	}
	var skipped []string
	for _, m := range members {
		if !have[m.Name] {
			skipped = append(skipped, "`"+m.Name+"`")
		}
	}
	var b strings.Builder
	b.Write(prompt)
	b.WriteString("\n## Repositories\n\n")
	b.WriteString("This directory is NOT a git repository. It holds one checkout per repository this issue needs, each already on the session's branch:\n\n")
	for _, a := range apps {
		repo := a.Repo
		if repo == "" {
			repo = "(no GitHub repo configured)"
		}
		fmt.Fprintf(&b, "- `%s/` — %s, branch `%s`, base `%s`\n", a.Name, repo, a.Branch, appBaseOf(a))
	}
	b.WriteString("\nRun git, builds and tests INSIDE the checkout they belong to (`cd <name>` first). Make each change in the repository it belongs to; for every repository you changed, commit there, push the branch and open a pull request against its base branch. ")
	if len(apps) > 1 {
		b.WriteString("Link the pull requests to each other in their descriptions (\"Part of …, see also <other PR URL>\"). ")
	}
	b.WriteString("Leave a repository you did not need untouched — no empty pull requests. Files you create directly in this directory are not part of any repository and are lost when the session ends.\n")
	if len(skipped) > 0 {
		fmt.Fprintf(&b, "\nThis stack also has %s, NOT checked out for this issue (its labels did not select them). If the issue needs one of them, do not clone or edit it elsewhere: report it with `lola report blocked \"needs <name>\"` and explain in a note.\n", strings.Join(skipped, ", "))
	}
	return []byte(b.String())
}

// repoMount is what a sandboxed session's container gets per repository:
// the git store (resolved from the MAIN checkout at path, never from the
// worktree), the worktree's pinned overlays, and the symlink targets.
type repoMount struct {
	path     string
	worktree string
	symlinks []string
}

// sessionRepos lists the repositories a sandboxed session at dir works in:
// the project's own for an ordinary session, each present app for a stack.
// required reports whether the first store must resolve (an ordinary
// project's checkout must be a repository; a stack has no checkout).
func (n *Native) sessionRepos(p *config.Project, dir string) (repos []repoMount, required bool) {
	if !p.IsStack() {
		return []repoMount{{path: p.Path, worktree: dir, symlinks: p.Symlinks}}, true
	}
	for _, m := range stackPresent(n.Cfg.StackMembers(p.Name), dir) {
		repos = append(repos, repoMount{path: m.Path, worktree: StackAppDir(dir, m.Name), symlinks: m.Symlinks})
	}
	return repos, false
}
