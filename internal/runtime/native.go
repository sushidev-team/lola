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

	"github.com/sushidev-team/lola/internal/agent"
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

// manualInfix tags a manually-opened (`lola open`) session's ID right after the
// project segment: "lola-<project>-open-<slug>". It keeps hand-opened shell
// sessions distinguishable from issue-driven ones (whose segment is a lowercased
// Linear identifier, which can never start with "open-" followed by a Linear
// key), so both SessionID-shape parsing (issueFromSessionID) and restart
// adoption can recognize a manual session from its ID alone.
const manualInfix = "open-"

// lolaDir is the runtime scratch directory inside each worktree, holding
// prompt.md, env, and the per-agent callback artifact(s) (Claude's
// settings.json, or Codex's codex/ home). It is excluded via the worktree's
// git info/exclude, never via the repository's .gitignore.
const lolaDir = ".lola"

// openCodeDir is the directory OpenCode auto-loads plugins from
// (.opencode/plugins/); the lola hook plugin lands there for opencode
// sessions. Like lolaDir it is kept out of git via the worktree's
// info/exclude, so a rollback that removes a clean worktree takes it too.
const openCodeDir = ".opencode"

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
	// ClaudeBin overrides the claude binary launched inside tmux; empty means
	// "claude" resolved via the pane's PATH. It applies ONLY when the resolved
	// agent kind is Claude — codex/opencode sessions always use their own
	// PATH-resolved binary (agent.Kind.Binary()).
	ClaudeBin string
	// LinearKey is an optional provider for the current Linear API key,
	// forwarded into the session via the 0600 <dir>/.lola/env file (never on
	// argv). It is called once per Spawn so a rotated key is picked up on the
	// next spawn; it may be nil or return "" when no key is available, in which
	// case the session simply has no LINEAR_API_KEY and the agent falls back to
	// whatever Linear tooling it can otherwise authenticate. It MUST never be a
	// key captured once as a plain string on this struct.
	LinearKey func() string
	// Logf, when non-nil, records best-effort advisories that must not fail an
	// operation — currently the tmux status-bar styling applied after a session
	// launches. A styling failure only logs; the spawn always succeeds. nil
	// silences these advisories (tests, or callers that don't care).
	Logf func(format string, args ...any)
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

// ManualSessionID returns the native session identifier for a manually-opened
// branch/PR: "lola-<project>-open-<slug>", where slug is label lowercased with
// every run of non [a-z0-9._-] characters collapsed to a single "-" (so a branch
// like "feat/foo bar" becomes a single valid path segment). It is both the tmux
// session name and the worktree directory basename, distinct from an
// issue-driven SessionID via the manualInfix.
func ManualSessionID(project, label string) string {
	return sessionPrefix + project + "-" + manualInfix + slugify(label)
}

// manualSlugRe matches runs of characters not allowed in a session-ID slug; each
// run collapses to one "-".
var manualSlugRe = regexp.MustCompile(`[^a-z0-9._-]+`)

// slugify lowercases label and reduces it to a single safe path segment. A label
// that slugs to empty yields "ref" so ManualSessionID always produces a valid
// segment.
func slugify(label string) string {
	s := manualSlugRe.ReplaceAllString(strings.ToLower(label), "-")
	s = strings.Trim(s, "-.")
	if s == "" || s == "." || s == ".." {
		return "ref"
	}
	return s
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
	// Resolve which coding agent drives this session's pane: the project's
	// override, else [defaults].agent, else "claude" (AgentForProject). Parse is
	// total — an unknown/empty value falls back to Claude — so the launch line,
	// the callback artifact(s), the launch env, and the recorded Session.Agent
	// are all derived from this one Kind.
	kind := agent.Parse(n.Cfg.AgentForProject(p.Name))
	baseID := SessionID(p.Name, issue.Identifier)
	baseBranch := issue.BranchName
	if baseBranch == "" {
		baseBranch = n.Cfg.BranchPrefixForProject(p.Name) + strings.ToLower(issue.Identifier)
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
	// OpenCode's plugin lives under .opencode/, not .lola/; exclude it the same
	// way so a rollback that removes a clean worktree isn't fooled into seeing
	// the plugin as a dirty untracked file (excluded paths never show in
	// `git status --porcelain`). Exclude BEFORE writing the plugin below.
	if kind == agent.OpenCode {
		if err := excludeGitPattern(dir, openCodeDir+"/"); err != nil {
			return fail("git info/exclude "+openCodeDir, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, lolaDir), 0o700); err != nil {
		return fail("create "+lolaDir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, lolaDir, "prompt.md"), promptMD(p, issue, branch), 0o600); err != nil {
		return fail("write prompt.md", err)
	}
	// Per-agent lifecycle-callback artifact(s): claude's .lola/settings.json,
	// codex's .lola/codex/config.toml (+ best-effort auth symlink), or
	// opencode's .opencode/plugins/lola-hook.js. All land under .lola/ or the
	// just-excluded .opencode/, so they share the other .lola files' rollback
	// disposition (a clean worktree is removed wholesale, a dirty one is kept).
	if err := n.writeAgentArtifacts(dir, kind); err != nil {
		return fail("write agent artifacts", err)
	}
	// The env file carries the Linear API key, project env, and (for codex) the
	// per-session CODEX_HOME; it is 0600 and must be in place BEFORE the launch
	// sources it. Written last of the .lola files so a rollback that keeps a
	// dirty worktree keeps it too (0600, in the kept dir — same disposition as
	// the other .lola artifacts).
	if err := os.WriteFile(filepath.Join(dir, lolaDir, "env"), n.envFile(p, id, dir, kind), 0o600); err != nil {
		return fail("write env", err)
	}

	if err := n.Tmux.NewSession(ctx, id, dir, n.launchCommand(id, kind, false)); err != nil {
		return session.Session{}, n.rollbackTmux(ctx, p, id, dir, branch, "start tmux session", err)
	}

	// Brand the agent pane's status bar (LOLA · issue · detach hint) and bind the
	// optional custom detach key, both PER-SESSION on the isolated lola server.
	// Strictly cosmetic and best-effort: a styling failure is logged and the
	// spawn still succeeds, so a session is never lost to a chrome hiccup.
	if err := n.Tmux.ConfigureSession(ctx, id, n.Cfg.SessionChrome(issue.Identifier)); err != nil && n.Logf != nil {
		n.Logf("session %s: status-bar styling failed (cosmetic, session is up): %v", id, err)
	}

	return session.Session{
		ID:        id,
		Source:    "native",
		Kind:      session.KindLinear,
		Project:   p.Name,
		Issue:     issue.Identifier,
		Title:     issue.Title,
		IssueUUID: issue.ID,
		Branch:    branch,
		Repo:      p.Repo,
		Worktree:  dir,
		TmuxName:  id,
		Status:    StatusWorking,
		Agent:     string(kind),
	}, nil
}

// Open checks out an EXISTING branch or PR of project p into a throwaway
// DETACHED worktree and drops a plain interactive shell into it — no coding
// agent — so a human can run and test it. fetchRef is what to fetch from origin
// ("pull/<n>/head" for a PR, a branch name otherwise, resolved by
// worktree.CheckoutRef); branch is the human-readable label recorded on the
// session (and shown in the TUI). sessionID must be a ManualSessionID.
//
// The pipeline mirrors Spawn's but is deliberately agent-free: CheckoutRef
// (detached) → Prepare (symlinks + post_create, so the checkout is runnable) →
// exclude .lola/ → write a minimal 0600 .lola/env (project env only, NO Linear
// key — the human drives this shell) → tmux new-session running the user's login
// shell. Detached HEAD means teardown never deletes the upstream branch. On any
// step failure the worktree is rolled back best-effort (force=false, so a dirty
// checkout is kept for inspection) and the error says what was left behind.
func (n *Native) Open(ctx context.Context, p config.Project, sessionID, fetchRef, branch string) (session.Session, error) {
	if sessionID == "" || fetchRef == "" {
		return session.Session{}, errors.New("runtime: open: session id and ref required")
	}
	dir, err := n.WT.CheckoutRef(ctx, p, sessionID, fetchRef)
	if err != nil {
		return session.Session{}, fmt.Errorf("runtime: open %s: %w", sessionID, err)
	}
	fail := func(step string, cause error) (session.Session, error) {
		// The worktree is DETACHED — there is no lola-owned branch to delete, so
		// pass "" (Remove's deleteBranch no-ops) and never risk the upstream branch.
		if rmErr := n.WT.Remove(ctx, p, dir, "", false); rmErr != nil {
			return session.Session{}, fmt.Errorf("runtime: open %s: %s: %w (rollback failed: %v; worktree kept at %s)", sessionID, step, cause, rmErr, dir)
		}
		return session.Session{}, fmt.Errorf("runtime: open %s: %s: %w (worktree rolled back)", sessionID, step, cause)
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
	if err := os.WriteFile(filepath.Join(dir, lolaDir, "env"), manualEnvFile(p), 0o600); err != nil {
		return fail("write env", err)
	}

	if err := n.Tmux.NewSession(ctx, sessionID, dir, n.shellCommand()); err != nil {
		if n.Tmux.Has(ctx, sessionID) {
			_ = n.Tmux.KillSession(ctx, sessionID)
		}
		return fail("start tmux session", err)
	}

	// Best-effort status-bar chrome, exactly like Spawn — a styling hiccup never
	// loses the session.
	if err := n.Tmux.ConfigureSession(ctx, sessionID, n.Cfg.SessionChrome(branch)); err != nil && n.Logf != nil {
		n.Logf("session %s: status-bar styling failed (cosmetic, shell is up): %v", sessionID, err)
	}

	return session.Session{
		ID:        sessionID,
		Source:    "native",
		Kind:      session.KindPR, // detached checkout of an existing upstream ref: non-owning
		Agentless: true,           // plain shell, no coding agent
		Manual:    true,           // back-compat alias for old readers
		Project:   p.Name,
		Title:     "manual: " + branch,
		Branch:    branch,
		Repo:      p.Repo,
		Worktree:  dir,
		TmuxName:  sessionID,
		Status:    "shell",
	}, nil
}

// OpenManual creates a NEW branch off base (empty → the project's default
// branch) in a fresh worktree and drops a plain interactive shell into it — no
// coding agent — so a human can start work by hand. Unlike Open (which detaches
// on an existing ref), the branch is lola-owned, so teardown deletes it
// (session Kind=manual). The pipeline mirrors Open's otherwise: CreateFrom →
// Prepare → exclude .lola/ → minimal 0600 .lola/env (project env only, no Linear
// key) → tmux shell. On any step failure the worktree AND its new branch are
// rolled back best-effort (force=false keeps a dirty checkout for inspection).
func (n *Native) OpenManual(ctx context.Context, p config.Project, sessionID, branch, base string) (session.Session, error) {
	if sessionID == "" || branch == "" {
		return session.Session{}, errors.New("runtime: open manual: session id and branch required")
	}
	dir, err := n.WT.CreateFrom(ctx, p, sessionID, branch, base)
	if err != nil {
		return session.Session{}, fmt.Errorf("runtime: open manual %s: %w", sessionID, err)
	}
	fail := func(step string, cause error) (session.Session, error) {
		// lola OWNS this new branch — pass it so a rollback removes it too.
		if rmErr := n.WT.Remove(ctx, p, dir, branch, false); rmErr != nil {
			return session.Session{}, fmt.Errorf("runtime: open manual %s: %s: %w (rollback failed: %v; worktree kept at %s)", sessionID, step, cause, rmErr, dir)
		}
		return session.Session{}, fmt.Errorf("runtime: open manual %s: %s: %w (worktree rolled back)", sessionID, step, cause)
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
	if err := os.WriteFile(filepath.Join(dir, lolaDir, "env"), manualEnvFile(p), 0o600); err != nil {
		return fail("write env", err)
	}

	if err := n.Tmux.NewSession(ctx, sessionID, dir, n.shellCommand()); err != nil {
		if n.Tmux.Has(ctx, sessionID) {
			_ = n.Tmux.KillSession(ctx, sessionID)
		}
		return fail("start tmux session", err)
	}
	if err := n.Tmux.ConfigureSession(ctx, sessionID, n.Cfg.SessionChrome(branch)); err != nil && n.Logf != nil {
		n.Logf("session %s: status-bar styling failed (cosmetic, shell is up): %v", sessionID, err)
	}

	return session.Session{
		ID:        sessionID,
		Source:    "native",
		Kind:      session.KindManual, // lola-owned new branch: owns + deletes it on teardown
		Agentless: true,               // plain shell, no coding agent
		Project:   p.Name,
		Title:     "manual: " + branch,
		Branch:    branch,
		Repo:      p.Repo,
		Worktree:  dir,
		TmuxName:  sessionID,
		Status:    "shell",
	}, nil
}

// finishAgentLaunch performs the shared post-worktree steps for an AGENT
// session (as opposed to a shell): prepare the worktree, write the .lola
// artifacts (the given prompt, the per-agent lifecycle callbacks, and the 0600
// env with the Linear key + project env), start the agent in tmux, and brand
// the pane. dir must be a freshly created worktree. On any step failure it rolls
// the worktree back (force=false, so a dirty checkout is kept for inspection) —
// deleting the branch only when ownsBranch — and returns the wrapped error.
func (n *Native) finishAgentLaunch(ctx context.Context, p config.Project, id, dir, branch string, kind agent.Kind, ownsBranch bool, prompt string) error {
	rb := func(step string, cause error) error {
		delBranch := ""
		if ownsBranch {
			delBranch = branch
		}
		if rmErr := n.WT.Remove(ctx, p, dir, delBranch, false); rmErr != nil {
			return fmt.Errorf("runtime: launch %s: %s: %w (rollback failed: %v; worktree kept at %s)", id, step, cause, rmErr, dir)
		}
		return fmt.Errorf("runtime: launch %s: %s: %w (worktree rolled back)", id, step, cause)
	}
	if err := n.WT.Prepare(ctx, p, dir); err != nil {
		return rb("prepare worktree", err)
	}
	if err := excludeLolaDir(dir); err != nil {
		return rb("git info/exclude", err)
	}
	if kind == agent.OpenCode {
		if err := excludeGitPattern(dir, openCodeDir+"/"); err != nil {
			return rb("git info/exclude "+openCodeDir, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, lolaDir), 0o700); err != nil {
		return rb("create "+lolaDir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, lolaDir, "prompt.md"), []byte(prompt), 0o600); err != nil {
		return rb("write prompt.md", err)
	}
	if err := n.writeAgentArtifacts(dir, kind); err != nil {
		return rb("write agent artifacts", err)
	}
	if err := os.WriteFile(filepath.Join(dir, lolaDir, "env"), n.envFile(p, id, dir, kind), 0o600); err != nil {
		return rb("write env", err)
	}
	if err := n.Tmux.NewSession(ctx, id, dir, n.launchCommand(id, kind, false)); err != nil {
		if n.Tmux.Has(ctx, id) {
			_ = n.Tmux.KillSession(ctx, id)
		}
		return rb("start tmux session", err)
	}
	if err := n.Tmux.ConfigureSession(ctx, id, n.Cfg.SessionChrome(branch)); err != nil && n.Logf != nil {
		n.Logf("session %s: status-bar styling failed (cosmetic, session is up): %v", id, err)
	}
	return nil
}

// OpenPRAgent opens a PR's branch as a TRACKING worktree and launches the coding
// agent on it (so it can address review feedback and PUSH BACK) — the "agent on
// PR" upgrade. The branch must be same-owner (the daemon refuses fork PRs before
// calling this). Session Kind=pr: the branch is upstream and NEVER deleted on
// teardown. prompt seeds .lola/prompt.md (the caller passes a PR briefing).
func (n *Native) OpenPRAgent(ctx context.Context, p config.Project, sessionID, branch, prompt string) (session.Session, error) {
	if sessionID == "" || branch == "" {
		return session.Session{}, errors.New("runtime: open pr agent: session id and branch required")
	}
	dir, err := n.WT.CheckoutTracking(ctx, p, sessionID, branch)
	if err != nil {
		return session.Session{}, fmt.Errorf("runtime: open pr agent %s: %w", sessionID, err)
	}
	kind := agent.Parse(n.Cfg.AgentForProject(p.Name))
	if err := n.finishAgentLaunch(ctx, p, sessionID, dir, branch, kind, false /* pr: upstream branch, not owned */, prompt); err != nil {
		return session.Session{}, err
	}
	return session.Session{
		ID:       sessionID,
		Source:   "native",
		Kind:     session.KindPR,
		Project:  p.Name,
		Title:    "PR: " + branch,
		Branch:   branch,
		Repo:     p.Repo,
		Worktree: dir,
		TmuxName: sessionID,
		Status:   StatusWorking,
		Agent:    string(kind),
	}, nil
}

// OpenManualAgent creates a NEW branch off base and launches the coding agent on
// it — the manual-worktree "with agent" variant. Session Kind=manual: lola owns
// the branch and deletes it on teardown. prompt seeds .lola/prompt.md.
func (n *Native) OpenManualAgent(ctx context.Context, p config.Project, sessionID, branch, base, prompt string) (session.Session, error) {
	if sessionID == "" || branch == "" {
		return session.Session{}, errors.New("runtime: open manual agent: session id and branch required")
	}
	dir, err := n.WT.CreateFrom(ctx, p, sessionID, branch, base)
	if err != nil {
		return session.Session{}, fmt.Errorf("runtime: open manual agent %s: %w", sessionID, err)
	}
	kind := agent.Parse(n.Cfg.AgentForProject(p.Name))
	if err := n.finishAgentLaunch(ctx, p, sessionID, dir, branch, kind, true /* manual: lola-owned branch */, prompt); err != nil {
		return session.Session{}, err
	}
	return session.Session{
		ID:       sessionID,
		Source:   "native",
		Kind:     session.KindManual,
		Project:  p.Name,
		Title:    "manual: " + branch,
		Branch:   branch,
		Repo:     p.Repo,
		Worktree: dir,
		TmuxName: sessionID,
		Status:   StatusWorking,
		Agent:    string(kind),
	}, nil
}

// shellCommand builds the tmux command for a manual (`lola open`) session: a
// plain interactive login shell, with the worktree's 0600 .lola/env sourced
// first so [[project]].env is available for running/testing (same POSIX wrapper
// as launchCommand — the login shell only has to exec `sh`; see that comment).
// The env file is sourced conditionally so a missing/empty one is not an error,
// and NO secret is ever placed on argv. "${SHELL:-/bin/sh}" is the human's login
// shell, exec'd so the pane's process IS the interactive shell.
func (n *Native) shellCommand() string {
	posix := `set -a; [ -f ./` + lolaDir + `/env ] && . ./` + lolaDir + `/env; set +a; exec "${SHELL:-/bin/sh}"`
	return "exec sh -c " + shQuote(posix)
}

// manualEnvFile renders a manual session's 0600 .lola/env: ONLY the project's
// [[project]].env pairs (sorted, each shQuote'd), so the interactive shell can
// run project commands. It deliberately carries NO Linear API key and NO
// LOLA_SESSION — a manual shell has no lifecycle hooks and the human drives it,
// so no secret is exported into a shell they may screenshot or share.
func manualEnvFile(p config.Project) []byte {
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(p.Env)) {
		if !envNameRe.MatchString(k) { // defense-in-depth, mirrors envFile
			continue
		}
		fmt.Fprintf(&b, "%s=%s\n", k, shQuote(p.Env[k]))
	}
	return []byte(b.String())
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
// API key, the project env, and codex's CODEX_HOME) is carried by the 0600
// <dir>/.lola/env file, which the line sources instead of putting it on argv
// (ps-visible / tmux-server-visible). The inner POSIX line is:
//
//	set -a; . ./.lola/env; set +a; exec <bin> <agent args...>
//
// `set -a` auto-exports everything the sourced file defines (so LOLA_SESSION
// is still exported for hooks, and LINEAR_API_KEY / project env / CODEX_HOME
// reach the agent); `. ./.lola/env` sources it relative to the session's -c dir
// (the worktree); `set +a` restores default behavior; `exec` replaces the shell
// with the agent binary. The binary and every launch arg are shQuote'd
// individually, then the whole POSIX line is shQuote'd again for the outer
// `sh -c`.
//
// The binary is the resolved agent's own (agent.Kind.Binary()), except that a
// Claude session honors a non-empty n.ClaudeBin override for back-compat. The
// args come from agent.LaunchArgs(kind, prompt): claude gets
// `--settings .lola/settings.json <prompt>`, codex gets its unattended
// approval/sandbox flags plus the positional prompt, opencode gets
// `--prompt <prompt> --auto`. The prompt argv string is identical for all three
// and deliberately short — it only points the agent at .lola/prompt.md, which
// carries the real briefing, so huge issue titles never bloat the command line
// or the tmux server's argv.
// resume=true builds the REVIVE launch (agent.LaunchArgsResume): Claude adds
// --continue to pick up its prior conversation instead of the fresh positional
// prompt. Spawn always passes false; only Revive passes true, and only after
// confirming there is a transcript to continue.
func (n *Native) launchCommand(id string, kind agent.Kind, resume bool) string {
	bin := kind.Binary()
	if kind == agent.Claude && n.ClaudeBin != "" {
		bin = n.ClaudeBin
	}
	prompt := "You are lola session " + id + ". Read " + lolaDir + "/prompt.md in the current directory first; it contains your task briefing."
	args := agent.LaunchArgs(kind, prompt)
	if resume {
		args = agent.LaunchArgsResume(kind, prompt)
	}
	posix := "set -a; . ./" + lolaDir + "/env; set +a; exec " + shQuote(bin)
	for _, arg := range args {
		posix += " " + shQuote(arg)
	}
	return "exec sh -c " + shQuote(posix)
}

// writeAgentArtifacts writes the per-agent lifecycle-callback artifact(s) into
// the worktree at dir, before launch, for the resolved kind:
//
//   - Claude:   .lola/settings.json = hook.SettingsJSON(LolaBin) — the hook
//     wiring Claude Code reads via `--settings`.
//   - Codex:    .lola/codex/config.toml = agent.CodexConfigTOML(LolaBin), a
//     per-session CODEX_HOME whose `notify` key routes codex events to `lola
//     hook codex-notify`, plus a best-effort auth.json symlink to the user's
//     real codex login so `codex login` survives (absent source is not an
//     error — API-key users authenticate via OPENAI_API_KEY from the pane env).
//   - OpenCode: .opencode/plugins/lola-hook.js = agent.OpenCodePluginJS(LolaBin),
//     the in-process plugin opencode auto-loads that shells `lola hook` on its
//     lifecycle events.
//
// The caller has already created <dir>/.lola (0700) and, for opencode, excluded
// .opencode/ from git. Files are 0600; directories 0700 — same discipline as
// the other .lola artifacts.
func (n *Native) writeAgentArtifacts(dir string, kind agent.Kind) error {
	switch kind {
	case agent.Codex:
		return n.writeCodexArtifacts(dir)
	case agent.OpenCode:
		return os.WriteFile(openCodePluginPath(dir), agent.OpenCodePluginJS(n.LolaBin), 0o600)
	default: // Claude
		return os.WriteFile(filepath.Join(dir, lolaDir, "settings.json"), hook.SettingsJSON(n.LolaBin), 0o600)
	}
}

// writeCodexArtifacts writes the codex per-session CODEX_HOME under
// <dir>/.lola/codex: config.toml (with the notify wiring) and a best-effort
// auth.json symlink to the user's existing codex login. The symlink is
// advisory — an absent source is skipped silently, so a codex run with no
// prior `codex login` still launches (it authenticates via OPENAI_API_KEY
// inherited from the pane env).
func (n *Native) writeCodexArtifacts(dir string) error {
	codexHome := filepath.Join(dir, lolaDir, "codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), agent.CodexConfigTOML(n.LolaBin), 0o600); err != nil {
		return err
	}
	linkCodexAuth(codexHome)
	return nil
}

// openCodePluginPath returns the plugin file path for an opencode session's
// worktree and ensures its .opencode/plugins parent exists (best-effort: a
// mkdir failure surfaces when the subsequent WriteFile fails).
func openCodePluginPath(dir string) string {
	pluginsDir := filepath.Join(dir, openCodeDir, "plugins")
	_ = os.MkdirAll(pluginsDir, 0o700)
	return filepath.Join(pluginsDir, "lola-hook.js")
}

// linkCodexAuth best-effort symlinks the user's real codex auth.json into the
// per-session CODEX_HOME so an existing `codex login` carries over. It never
// returns an error: a missing source (API-key users) or a symlink failure is
// silently skipped — a codex session must launch regardless.
func linkCodexAuth(codexHome string) {
	src := userCodexAuth()
	if src == "" {
		return
	}
	if _, err := os.Stat(src); err != nil {
		return // no existing login to carry over
	}
	_ = os.Symlink(src, filepath.Join(codexHome, "auth.json"))
}

// userCodexAuth resolves the path to the user's real codex auth.json: under
// $CODEX_HOME when set, else ~/.codex/auth.json. Returns "" when no home can be
// determined (the caller then skips the symlink).
func userCodexAuth() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return filepath.Join(h, "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex", "auth.json")
}

// envFile renders <dir>/.lola/env: shell-sourceable NAME=value assignments the
// launch command sources under `set -a`. Each value is single-quoted via
// shQuote so nothing needs a shell-safe shape, and the file is written 0600 and
// MUST never be logged — it may hold the Linear API key. It carries, in this
// order: LOLA_SESSION (not secret); for a codex session, CODEX_HOME pointing at
// the per-session <dir>/.lola/codex (so codex reads the lola-written config.toml
// and notify wiring, not the user's real home); LINEAR_API_KEY, only when a
// LinearKey provider is set and returns a non-empty key (a rotated key is picked
// up on the next spawn because the provider is called here, each spawn); and
// every [[project]].env pair in sorted order (the same variables Prepare gives
// post_create commands — the agent session sees them too). dir is the absolute
// worktree path, so CODEX_HOME is absolute and resolves regardless of cwd.
func (n *Native) envFile(p config.Project, id, dir string, kind agent.Kind) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "LOLA_SESSION=%s\n", shQuote(id))
	if kind == agent.Codex {
		fmt.Fprintf(&b, "CODEX_HOME=%s\n", shQuote(filepath.Join(dir, lolaDir, "codex")))
	}
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
			// A manually-opened shell has no coding agent: recover it as such from
			// its ID shape (backstop for a lost store record) so a live one comes
			// back as "shell" and the daemon keeps it out of the control loop.
			manual := isManualSessionID(id, p.Name)
			if manual && status == StatusWorking {
				status = "shell"
			}
			out = append(out, session.Session{
				ID:        id,
				Source:    "native",
				Manual:    manual,
				Agentless: manual, // a manual (`lola open`) shell has no coding agent
				Project:   p.Name,
				Issue:     issueFromSessionID(id, p.Name),
				Repo:      p.Repo,
				TmuxName:  id,
				Status:    status,
			})
		}
	}

	for name := range live {
		if paired[name] {
			continue
		}
		project := n.projectForSessionName(name)
		manual := isManualSessionID(name, project)
		out = append(out, session.Session{
			ID:        name,
			Source:    "native",
			Manual:    manual,
			Agentless: manual, // a manual (`lola open`) shell has no coding agent
			Project:   project,
			Issue:     issueFromSessionID(name, project),
			TmuxName:  name,
			Status:    StatusOrphaned,
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
	// Only lola-owned branches are deleted on teardown (see Session.OwnsBranch): a
	// linear dispatch's branch and a manual new-branch worktree's branch. A pr
	// session (`lola open` / the PR picker) is a DETACHED checkout whose recorded
	// Branch is the UPSTREAM branch/PR label — pass "" so Remove never deletes it
	// (deleteBranch no-ops on "").
	branch := s.Branch
	if !s.OwnsBranch() {
		branch = ""
	}
	if err := n.WT.Remove(ctx, *p, dir, branch, force); err != nil {
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

// Revive re-creates the tmux agent session for a session whose pane died but
// whose worktree survives (dead sessions keep their worktree for inspection).
// It reuses the existing worktree and its .lola artifacts (prompt, settings,
// env) exactly as the original spawn left them and relaunches the agent in
// place: when the agent is Claude AND it wrote a transcript before dying, the
// pane comes back with `claude --continue` and resumes the prior conversation
// (launchCommand resume=true); otherwise it launches fresh on the same worktree
// — the case for a session that died so fast it never recorded a transcript
// (an empty --continue would just error and re-kill the pane).
//
// The caller (daemon.handleRevive) is responsible for the checks Revive does
// not do: that the session is not already alive, and re-establishing the
// dispatch guards so the revived session is not duplicated. Revive itself only
// needs the worktree to still be there.
func (n *Native) Revive(ctx context.Context, s session.Session) (session.Session, error) {
	id := s.ID
	dir := filepath.Join(n.WT.Root, s.Project, id)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return session.Session{}, fmt.Errorf("runtime: revive %s: worktree %s is gone — let the issue re-dispatch instead", id, dir)
	}
	kind := agent.Parse(s.Agent)
	resume := kind == agent.Claude && claudeHasTranscript(dir)
	if err := n.Tmux.NewSession(ctx, id, dir, n.launchCommand(id, kind, resume)); err != nil {
		return session.Session{}, fmt.Errorf("runtime: revive %s: %w", id, err)
	}
	// Best-effort chrome, exactly as Spawn: a styling hiccup never loses the
	// revived session.
	if err := n.Tmux.ConfigureSession(ctx, id, n.Cfg.SessionChrome(s.Issue)); err != nil && n.Logf != nil {
		n.Logf("session %s: status-bar styling failed (cosmetic, session is up): %v", id, err)
	}
	revived := s
	revived.Status = StatusWorking
	revived.TmuxName = id
	return revived, nil
}

// claudeHasTranscript reports whether Claude Code has a saved conversation for
// the worktree at dir — i.e. whether `claude --continue` has anything to resume.
// Claude stores transcripts under ~/.claude/projects/<escaped-cwd>/<uuid>.jsonl,
// escaping the worktree path into the project-dir name; rather than reproduce
// that escaping exactly, we match on the worktree's basename (the session ID),
// which contains no path separator and so survives escaping verbatim as a
// stable, near-unique substring of the project-dir name. Best-effort: any error
// resolving home means "no transcript", so Revive launches fresh.
func claudeHasTranscript(dir string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*"+filepath.Base(dir)+"*", "*.jsonl"))
	return len(matches) > 0
}

// issueFromSessionID recovers the Linear identifier from a session ID built
// by SessionID for the given project (retry suffixes like "-r2" are
// stripped), or "" when the ID has another shape.
func issueFromSessionID(id, project string) string {
	rest, ok := strings.CutPrefix(id, sessionPrefix+project+"-")
	if !ok || project == "" || rest == "" {
		return ""
	}
	if strings.HasPrefix(rest, manualInfix) {
		return "" // a manual (`lola open`) session has no Linear issue
	}
	rest = attemptSuffixRe.ReplaceAllString(rest, "")
	if rest == "" {
		return ""
	}
	return strings.ToUpper(rest)
}

// isManualSessionID reports whether id is a manual (`lola open`) session ID for
// the given project — i.e. "lola-<project>-open-…". project may be "" (a
// tmux-only session whose project could not be resolved), in which case no ID
// matches and the session is treated as non-manual.
func isManualSessionID(id, project string) bool {
	if project == "" {
		return false
	}
	rest, ok := strings.CutPrefix(id, sessionPrefix+project+"-")
	return ok && strings.HasPrefix(rest, manualInfix)
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
// worktree at dir (see excludeGitPattern). Kept as a named helper because the
// .lola/ exclusion is unconditional for every session, whereas .opencode/ is
// added only for opencode sessions.
func excludeLolaDir(dir string) error {
	return excludeGitPattern(dir, lolaDir+"/")
}

// excludeGitPattern appends pattern to the git info/exclude that governs the
// worktree at dir, keeping runtime files out of git status without touching
// the repository's tracked .gitignore. In a linked worktree, <dir>/.git is a
// file ("gitdir: <path>") and info/ is shared, so the pattern lands in the
// common git dir's info/exclude (resolved via the gitdir's commondir file) —
// exactly the file git reads. Idempotent: an existing identical line is left
// alone.
func excludeGitPattern(dir, pattern string) error {
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
