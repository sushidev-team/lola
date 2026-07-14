// Package runtime is Lola's native session launcher and lifecycle (PLAN
// P2.12/P2.15): it spawns Claude Code in a fresh tmux session inside a
// per-issue git worktree, adopts surviving sessions after a daemon restart,
// and kills sessions on request. It composes the P2 foundation packages —
// worktree (create/prepare/remove), tmux (session control), hook (per-session
// Claude Code settings) — and never talks to git, tmux, or claude except
// through those exec seams.
//
// Destructive-op discipline: worktrees are only ever removed via
// worktree.Manager.Remove with force=false, so a dirty worktree (uncommitted
// changes) always refuses with worktree.ErrDirty and is left in place for
// inspection; the project's main checkout is guarded inside the manager.
// Adopt is pure observation: it reports zombie candidates but never kills or
// removes anything itself — the daemon decides.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hook"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/tmux"
	"github.com/sushidev-team/lola/internal/worktree"
)

// Session status values reported by Spawn and Adopt. They deliberately reuse
// the observer's vocabulary where one exists ("working").
const (
	// StatusWorking: tmux session alive and worktree present.
	StatusWorking = "working"
	// StatusDead: worktree exists but its tmux session is gone — a cleanup
	// candidate for the daemon (after checking the PR merged).
	StatusDead = "dead"
	// StatusOrphaned: a lola-* tmux session without a matching worktree — a
	// kill candidate. Adopt only reports it; it never auto-kills.
	StatusOrphaned = "orphaned"
)

// sessionPrefix namespaces everything the native runtime owns: tmux session
// names and worktree directory basenames. Adopt scans by this prefix.
const sessionPrefix = "lola-"

// lolaDir is the runtime scratch directory inside each worktree, holding
// prompt.md and settings.json. It is excluded via the worktree's git
// info/exclude, never via the repository's .gitignore.
const lolaDir = ".lola"

// Native spawns and manages Lola's own runner sessions (runtime = "native"):
// one git worktree + one tmux session running Claude Code per Linear issue.
type Native struct {
	Cfg  *config.Config
	WT   *worktree.Manager
	Tmux *tmux.Client
	// LolaBin is the absolute path to the lola binary; the generated Claude
	// Code settings wire lifecycle hooks to `<LolaBin> hook <event>`.
	LolaBin string
	// Home is the lola runtime directory (config.Home()), recorded for
	// diagnostics/state paths; session state itself lives in the caller's
	// session.Store.
	Home string
	// ClaudeBin is the claude binary launched inside tmux; empty means
	// "claude" resolved via the pane's PATH.
	ClaudeBin string
	// LinearKey is an optional provider for the current Linear API key,
	// forwarded into the session via the 0600 <dir>/.lola/env file (never on
	// argv). It is called once per Spawn so a rotated key is picked up on the
	// next spawn; it may be nil or return "" when no key is available, in which
	// case the session simply has no LINEAR_API_KEY and the agent falls back to
	// whatever Linear tooling it can otherwise authenticate. It MUST never be a
	// key captured once as a plain string on this struct.
	LinearKey func() string
}

// SessionID returns the BASE native session identifier for an issue:
// "lola-<project>-<identifier-lowercased>", e.g. "lola-nori-eng-42". It is
// both the tmux session name and the worktree directory basename. When a
// previous attempt's worktree or branch still exists (kept for inspection
// after a dead session — PLAN P2.9's `lola/<issue>-<n>`), Spawn appends a
// "-r<attempt>" suffix so the re-queued issue never collides with it.
func SessionID(project, identifier string) string {
	return sessionPrefix + project + "-" + strings.ToLower(identifier)
}

// maxSpawnAttempts bounds the retry-suffix search in freeSessionSlot; hitting
// it means many kept-for-inspection leftovers piled up for one issue and a
// human should clean them up.
const maxSpawnAttempts = 20

// attemptSuffixRe matches the retry suffix Spawn appends to session IDs
// ("-r2", "-r3", …). Linear identifiers are <TEAMKEY>-<number>, so a
// lowercased identifier can never end in "-r<digits>" itself.
var attemptSuffixRe = regexp.MustCompile(`-r\d+$`)

// freeSessionSlot returns the first (session ID, branch) pair that does not
// collide with leftovers of earlier attempts for the same issue: attempt 1 is
// the deterministic (baseID, baseBranch) pair, retries append "-r<n>" to
// both. Collisions checked: the worktree dir on disk (a dead session's
// worktree is deliberately kept for inspection, and the reconcile pass
// re-queues its issue — without the suffix that re-queue could never spawn)
// and the branch in the project repo (it survives a manual `git worktree
// remove`). A live tmux pane under the base name implies a session record the
// reconciler still counts, so it never reaches Spawn and needs no check here.
func (n *Native) freeSessionSlot(ctx context.Context, p config.Project, baseID, baseBranch string) (id, branch string, err error) {
	for attempt := 1; attempt <= maxSpawnAttempts; attempt++ {
		id, branch = baseID, baseBranch
		if attempt > 1 {
			suffix := "-r" + strconv.Itoa(attempt)
			id, branch = baseID+suffix, baseBranch+suffix
		}
		if _, err := os.Stat(filepath.Join(n.WT.Root, p.Name, id)); err == nil {
			continue // a previous attempt's worktree is still on disk
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", "", err
		}
		exists, err := n.WT.BranchExists(ctx, p, branch)
		if err != nil {
			return "", "", err
		}
		if exists {
			continue // the branch survived (e.g. a manual worktree remove)
		}
		return id, branch, nil
	}
	return "", "", fmt.Errorf("no free session slot after %d attempts — clean up old %s* worktrees and branches", maxSpawnAttempts, baseID)
}

// Spawn creates the full native session for issue in project p:
//
//	worktree Create → Prepare → write <dir>/.lola/{prompt.md,settings.json}
//	(+ ignore .lola/ via the worktree's git info/exclude) → tmux new-session
//	running claude.
//
// The branch is issue.BranchName when Linear provides one, else
// "lola/<identifier-lowercased>"; when a previous attempt's worktree or
// branch still exists (kept for inspection), both the session ID and the
// branch get a "-r<attempt>" suffix (see freeSessionSlot) so a re-queued
// issue can always spawn again. On any step failure the already-created
// pieces are rolled back best-effort — the tmux session is killed if it came
// up, the worktree is removed only when clean (force=false; a dirty worktree
// is kept for inspection) — and the returned error says what was left behind.
func (n *Native) Spawn(ctx context.Context, p config.Project, issue linear.Issue) (session.Session, error) {
	if issue.Identifier == "" {
		return session.Session{}, errors.New("runtime: spawn: issue has no identifier")
	}
	baseID := SessionID(p.Name, issue.Identifier)
	baseBranch := issue.BranchName
	if baseBranch == "" {
		baseBranch = "lola/" + strings.ToLower(issue.Identifier)
	}
	id, branch, err := n.freeSessionSlot(ctx, p, baseID, baseBranch)
	if err != nil {
		return session.Session{}, fmt.Errorf("runtime: spawn %s: %w", baseID, err)
	}

	dir, err := n.WT.Create(ctx, p, id, branch)
	if err != nil {
		return session.Session{}, fmt.Errorf("runtime: spawn %s: %w", id, err)
	}
	fail := func(step string, cause error) (session.Session, error) {
		return session.Session{}, n.rollback(ctx, p, id, dir, branch, step, cause)
	}

	if err := n.WT.Prepare(ctx, p, dir); err != nil {
		return fail("prepare worktree", err)
	}
	if err := excludeLolaDir(dir); err != nil {
		return fail("git info/exclude", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, lolaDir), 0o700); err != nil {
		return fail("create "+lolaDir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, lolaDir, "prompt.md"), promptMD(p, issue, branch), 0o600); err != nil {
		return fail("write prompt.md", err)
	}
	if err := os.WriteFile(filepath.Join(dir, lolaDir, "settings.json"), hook.SettingsJSON(n.LolaBin), 0o600); err != nil {
		return fail("write settings.json", err)
	}
	// The env file carries the Linear API key and project env; it is 0600 and
	// must be in place BEFORE the launch sources it. Written last of the .lola
	// files so a rollback that keeps a dirty worktree keeps it too (0600, in
	// the kept dir — same disposition as the other .lola artifacts).
	if err := os.WriteFile(filepath.Join(dir, lolaDir, "env"), n.envFile(p, id), 0o600); err != nil {
		return fail("write env", err)
	}

	if err := n.Tmux.NewSession(ctx, id, dir, n.launchCommand(id)); err != nil {
		return session.Session{}, n.rollbackTmux(ctx, p, id, dir, branch, "start tmux session", err)
	}

	return session.Session{
		ID:        id,
		Source:    "native",
		Project:   p.Name,
		Issue:     issue.Identifier,
		IssueUUID: issue.ID,
		Branch:    branch,
		Repo:      p.Repo,
		TmuxName:  id,
		Status:    StatusWorking,
	}, nil
}

// launchCommand builds the single shell-command argument for `tmux
// new-session`. tmux passes one trailing argument through the user's LOGIN
// shell (`$SHELL -c`), which may be fish/csh/tcsh — not necessarily a POSIX
// sh. The actual launch logic is POSIX-only (`set -a`, `.`/source, `set +a`),
// so it is wrapped in an explicit `sh -c '<posix line>'`: the login shell only
// has to exec `sh` (a plain external command, portable across every shell),
// and `sh` alone interprets the POSIX builtins. Without this wrapper a fish or
// csh login shell errors on `set -a`/`.` and no session ever starts.
//
// Nothing secret ever appears here: the environment (LOLA_SESSION, the Linear
// API key, and the project env) is carried by the 0600 <dir>/.lola/env file,
// which the line sources instead of putting it on argv (ps-visible /
// tmux-server-visible). The inner POSIX line is:
//
//	set -a; . ./.lola/env; set +a; exec <claude> --settings .lola/settings.json '<prompt>'
//
// `set -a` auto-exports everything the sourced file defines (so LOLA_SESSION
// is still exported for hooks, and LINEAR_API_KEY / project env reach the
// agent); `. ./.lola/env` sources it relative to the session's -c dir (the
// worktree); `set +a` restores default behavior; `exec` replaces the shell
// with claude. The claude binary and prompt are shQuote'd, then the whole
// POSIX line is shQuote'd again for the outer `sh -c`. The argv prompt is
// deliberately short — it only points the agent at .lola/prompt.md, which
// carries the real briefing, so huge issue titles never bloat the command
// line or the tmux server's argv.
func (n *Native) launchCommand(id string) string {
	claude := n.ClaudeBin
	if claude == "" {
		claude = "claude"
	}
	prompt := "You are lola session " + id + ". Read " + lolaDir + "/prompt.md in the current directory first; it contains your task briefing."
	posix := "set -a; . ./" + lolaDir + "/env; set +a; exec " + shQuote(claude) +
		" --settings " + lolaDir + "/settings.json " + shQuote(prompt)
	return "exec sh -c " + shQuote(posix)
}

// envFile renders <dir>/.lola/env: shell-sourceable NAME=value assignments the
// launch command sources under `set -a`. Each value is single-quoted via
// shQuote so nothing needs a shell-safe shape, and the file is written 0600 and
// MUST never be logged — it may hold the Linear API key. It carries, in this
// order: LOLA_SESSION (not secret); LINEAR_API_KEY, only when a LinearKey
// provider is set and returns a non-empty key (a rotated key is picked up on
// the next spawn because the provider is called here, each spawn); and every
// [[project]].env pair in sorted order (the same variables Prepare gives
// post_create commands — the agent session sees them too).
func (n *Native) envFile(p config.Project, id string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "LOLA_SESSION=%s\n", shQuote(id))
	if n.LinearKey != nil {
		if key := n.LinearKey(); key != "" {
			fmt.Fprintf(&b, "LINEAR_API_KEY=%s\n", shQuote(key))
		}
	}
	for _, k := range slices.Sorted(maps.Keys(p.Env)) { // deterministic order
		// The NAME is the left-hand side of a shell assignment in a sourced
		// file, so it is shell-parsed: a name with metacharacters could run a
		// command with LINEAR_API_KEY already exported. config.Validate rejects
		// such names, but skip them here too as defense-in-depth so a leaking
		// name can never reach the launcher even if validation is bypassed.
		if !envNameRe.MatchString(k) {
			continue
		}
		fmt.Fprintf(&b, "%s=%s\n", k, shQuote(p.Env[k]))
	}
	return []byte(b.String())
}

// promptMD renders <dir>/.lola/prompt.md: the full standing briefing for the
// agent. The issue description and comments are deliberately not inlined —
// the agent fetches them live from Linear so it always sees current state.
func promptMD(p config.Project, issue linear.Issue, branch string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s: %s\n\n", issue.Identifier, issue.Title)
	fmt.Fprintf(&b, "You are working on Linear issue **%s** (%q) in project %s.\n\n", issue.Identifier, issue.Title, p.Name)
	b.WriteString("## First step\n\n")
	fmt.Fprintf(&b, "Fetch the full issue — description and all comments — from Linear via the tooling available to you (Linear MCP tools or CLI), using the identifier %s. This file intentionally contains only the summary above.\n\n", issue.Identifier)
	b.WriteString("Your Linear API key is available in the environment as `LINEAR_API_KEY` (if present) — use it to authenticate Linear tooling (linearis, a Linear MCP, or `curl` against the Linear GraphQL API). Never print the key or copy it into files, logs, or commits.\n\n")
	b.WriteString("## Git and PR expectations\n\n")
	fmt.Fprintf(&b, "- You are in a dedicated git worktree on branch `%s`; commit your work here and never switch branches.\n", branch)
	fmt.Fprintf(&b, "- When the work is done, push the branch and open a pull request against `%s`.\n", p.DefaultBranch)
	b.WriteString("- Never merge the pull request yourself; a human reviews and merges.\n")
	return []byte(b.String())
}

// rollback undoes a partially spawned session after step failed with cause:
// remove the worktree and its freshly created branch with force=false, so
// uncommitted artifacts refuse with worktree.ErrDirty and stay on disk for
// inspection. The returned error always wraps cause and states what, if
// anything, was left behind.
func (n *Native) rollback(ctx context.Context, p config.Project, id, dir, branch, step string, cause error) error {
	if rmErr := n.WT.Remove(ctx, p, dir, branch, false); rmErr != nil {
		return fmt.Errorf("runtime: spawn %s: %s: %w (rollback failed: %v; worktree kept at %s)", id, step, cause, rmErr, dir)
	}
	return fmt.Errorf("runtime: spawn %s: %s: %w (worktree rolled back)", id, step, cause)
}

// rollbackTmux is rollback for the final step: tmux new-session may have
// created the session even though the call failed, so it is killed
// best-effort first (only when tmux actually reports it).
func (n *Native) rollbackTmux(ctx context.Context, p config.Project, id, dir, branch, step string, cause error) error {
	if n.Tmux.Has(ctx, id) {
		if killErr := n.Tmux.KillSession(ctx, id); killErr != nil {
			return fmt.Errorf("runtime: spawn %s: %s: %w (rollback failed: %v; tmux session and worktree %s kept)", id, step, cause, killErr, dir)
		}
	}
	return n.rollback(ctx, p, id, dir, branch, step, cause)
}

// Adopt is the restart-recovery scan (PLAN P2.15): it pairs live "lola-*"
// tmux sessions with worktree directories under WT.Root across all configured
// projects and reports one session.Session per finding:
//
//	tmux alive + worktree present  → StatusWorking (re-adopt)
//	worktree without tmux          → StatusDead (cleanup candidate)
//	tmux without worktree          → StatusOrphaned (kill candidate)
//
// Adopt observes and reports only — it never kills sessions or removes
// worktrees; acting on dead/orphaned candidates is the daemon's decision.
// Branch is not recoverable from observation and is left empty; the caller's
// store merge preserves any previously persisted value. Issue identifiers are
// recovered from the session name (upper-cased, since SessionID lower-cases
// them and Linear identifiers are upper-case).
func (n *Native) Adopt(ctx context.Context) ([]session.Session, error) {
	tmuxSessions, err := n.Tmux.ListSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("runtime: adopt: %w", err)
	}
	live := map[string]bool{}
	for _, ts := range tmuxSessions {
		if strings.HasPrefix(ts.Name, sessionPrefix) {
			live[ts.Name] = true
		}
	}

	out := []session.Session{}
	paired := map[string]bool{}
	for _, p := range n.Cfg.Projects {
		dirs, err := n.WT.List(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("runtime: adopt: project %s: %w", p.Name, err)
		}
		for _, dir := range dirs {
			id := filepath.Base(dir)
			if !strings.HasPrefix(id, sessionPrefix) {
				continue // not ours; leave foreign dirs alone
			}
			status := StatusDead
			if live[id] {
				status = StatusWorking
				paired[id] = true
			}
			out = append(out, session.Session{
				ID:       id,
				Source:   "native",
				Project:  p.Name,
				Issue:    issueFromSessionID(id, p.Name),
				Repo:     p.Repo,
				TmuxName: id,
				Status:   status,
			})
		}
	}

	for name := range live {
		if paired[name] {
			continue
		}
		project := n.projectForSessionName(name)
		out = append(out, session.Session{
			ID:       name,
			Source:   "native",
			Project:  project,
			Issue:    issueFromSessionID(name, project),
			TmuxName: name,
			Status:   StatusOrphaned,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Kill terminates the session. Ordering is deliberate: the tmux session is
// killed FIRST (a session that is already gone is not an error), so the agent
// is always stopped even if the subsequent worktree removal refuses — a dirty
// worktree then survives with its agent already down. Only when removeWorktree
// is set does worktree removal follow, with the given force: force=false
// refuses a dirty worktree with worktree.ErrDirty (which propagates for the
// caller to surface as "worktree dirty, kept at <dir>"), force=true removes it
// regardless; a missing worktree directory is a no-op either way. Callers
// invoke Kill for merged or explicitly killed sessions.
func (n *Native) Kill(ctx context.Context, s session.Session, removeWorktree, force bool) error {
	name := s.TmuxName
	if name == "" {
		name = s.ID
	}
	if n.Tmux.Has(ctx, name) {
		if err := n.Tmux.KillSession(ctx, name); err != nil {
			return fmt.Errorf("runtime: kill %s: %w", s.ID, err)
		}
	}
	if !removeWorktree {
		return nil
	}
	p := n.Cfg.ProjectByName(s.Project)
	if p == nil {
		return fmt.Errorf("runtime: kill %s: unknown project %q, cannot remove worktree", s.ID, s.Project)
	}
	dir := filepath.Join(n.WT.Root, p.Name, s.ID)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil // already gone
	}
	if err := n.WT.Remove(ctx, *p, dir, s.Branch, force); err != nil {
		return fmt.Errorf("runtime: kill %s: %w", s.ID, err)
	}
	return nil
}

// Alive reports whether the session's tmux session still exists.
func (n *Native) Alive(ctx context.Context, s session.Session) bool {
	name := s.TmuxName
	if name == "" {
		name = s.ID
	}
	return n.Tmux.Has(ctx, name)
}

// issueFromSessionID recovers the Linear identifier from a session ID built
// by SessionID for the given project (retry suffixes like "-r2" are
// stripped), or "" when the ID has another shape.
func issueFromSessionID(id, project string) string {
	rest, ok := strings.CutPrefix(id, sessionPrefix+project+"-")
	if !ok || project == "" || rest == "" {
		return ""
	}
	rest = attemptSuffixRe.ReplaceAllString(rest, "")
	if rest == "" {
		return ""
	}
	return strings.ToUpper(rest)
}

// projectForSessionName finds the configured project a tmux-only session name
// belongs to by prefix match; the longest matching project name wins (project
// names may themselves contain '-'). Returns "" when nothing matches.
func (n *Native) projectForSessionName(name string) string {
	best := ""
	for _, p := range n.Cfg.Projects {
		if strings.HasPrefix(name, sessionPrefix+p.Name+"-") && len(p.Name) > len(best) {
			best = p.Name
		}
	}
	return best
}

// excludeLolaDir appends ".lola/" to the git info/exclude that governs the
// worktree at dir, keeping runtime files out of git status without touching
// the repository's tracked .gitignore. In a linked worktree, <dir>/.git is a
// file ("gitdir: <path>") and info/ is shared, so the pattern lands in the
// common git dir's info/exclude (resolved via the gitdir's commondir file) —
// exactly the file git reads. Idempotent: an existing ".lola/" line is left
// alone.
func excludeLolaDir(dir string) error {
	gitPath := filepath.Join(dir, ".git")
	fi, err := os.Stat(gitPath)
	if err != nil {
		return fmt.Errorf("locate git dir for %s: %w", dir, err)
	}

	gitDir := gitPath
	if !fi.IsDir() {
		data, err := os.ReadFile(gitPath)
		if err != nil {
			return err
		}
		target := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
		if target == "" {
			return fmt.Errorf("%s: not a gitdir pointer", gitPath)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(dir, target)
		}
		gitDir = filepath.Clean(target)
		// info/ is shared between worktrees: resolve the common dir.
		if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
			common := strings.TrimSpace(string(b))
			if !filepath.IsAbs(common) {
				common = filepath.Join(gitDir, common)
			}
			gitDir = filepath.Clean(common)
		}
	}

	infoDir := filepath.Join(gitDir, "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return err
	}
	exclude := filepath.Join(infoDir, "exclude")
	existing, err := os.ReadFile(exclude)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	pattern := lolaDir + "/"
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil // already excluded
		}
	}
	out := string(existing)
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	out += pattern + "\n"
	return os.WriteFile(exclude, []byte(out), 0o644)
}

// safeWord matches strings that need no shell quoting.
var safeWord = regexp.MustCompile(`^[A-Za-z0-9_%+=:,./@-]+$`)

// envNameRe matches a POSIX shell identifier — the only shape a project env
// key may take, since envFile emits each as the NAME on the left of a
// shell-sourced NAME=value assignment. config.Validate enforces this at load
// time; envFile re-checks as defense-in-depth (see envFile).
var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// shQuote single-quotes s for the shell that runs the tmux command line,
// unless it is already shell-safe (mirrors internal/hook's quoting so the
// generated command stays readable in `tmux ls`/ps output).
func shQuote(s string) string {
	if s != "" && safeWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
