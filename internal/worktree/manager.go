// Package worktree owns the lifecycle of per-session git worktrees under
// Root/<project>/<session> (Root defaults to ~/.lola/worktrees, see
// DefaultRoot). It creates worktrees off the project's default branch,
// prepares them for an agent (symlinks + post_create commands), and removes
// them again — but only for sessions whose PR merged or which were explicitly
// killed, and never when the worktree still has uncommitted changes unless
// forced. All git access shells out (exec seam: GitBin), mirroring
// internal/tmux.
package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitguard"
)

// ErrDirty is wrapped into Remove's error when the worktree has uncommitted
// changes (git status --porcelain non-empty) and force is false. Match with
// errors.Is.
var ErrDirty = errors.New("worktree has uncommitted changes")

// Manager creates, prepares, lists, and removes session worktrees. Zero value
// is unusable: Root must be set (DefaultRoot for production callers).
type Manager struct {
	GitBin string // absolute git path; "" resolves "git" via exec.LookPath
	Root   string // worktree root, e.g. ~/.lola/worktrees
}

// DefaultRoot returns config.Home()/worktrees, honoring $LOLA_HOME.
func DefaultRoot() (string, error) {
	home, err := config.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "worktrees"), nil
}

// git runs the git binary with args, returning stdout and stderr separately;
// a failure wraps the trimmed stderr text into the error.
func (m *Manager) git(ctx context.Context, args ...string) (stdout, stderr string, err error) {
	bin := m.GitBin
	if bin == "" {
		bin, err = exec.LookPath("git")
		if err != nil {
			return "", "", err
		}
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	// `-C <worktree>` on a SANDBOXED worktree must not follow the .git pointer
	// the agent can rewrite (internal/gitguard).
	gitguard.Apply(cmd)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	if err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			err = fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
		} else {
			err = fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}
	return out.String(), errb.String(), err
}

// Create adds a git worktree for the session at Root/<p.Name>/<sessionID> on
// a new branch off origin/<p.DefaultBranch> (falling back to the local
// <p.DefaultBranch> when the origin ref does not exist, e.g. offline clones).
// Idempotence: a directory that git already knows as a worktree is an error
// (the session exists); an EMPTY stale leftover directory without a
// registration (crash between mkdir and registration) is cleaned and
// recreated. A NON-EMPTY unregistered directory fails closed: losing the
// registration does not mean the contents are worthless — re-cloning the
// project or re-pointing [[project]].path deregisters every session worktree
// while uncommitted agent work is still on disk, and without a registration
// git cannot even answer a dirty check — so deleting it here would bypass
// Remove's ErrDirty discipline.
func (m *Manager) Create(ctx context.Context, p config.Project, sessionID, branch string) (string, error) {
	return m.CreateFrom(ctx, p, sessionID, branch, "")
}

// CreateFrom is Create with an explicit base: the new branch is cut from
// origin/<base> (falling back to the local <base> when the origin ref does not
// exist). An empty base defaults to p.DefaultBranch, so Create delegates here
// unchanged. Used by the manual-worktree flow to branch off a chosen base.
func (m *Manager) CreateFrom(ctx context.Context, p config.Project, sessionID, branch, base string) (string, error) {
	if err := m.validCreate(p, sessionID, branch); err != nil {
		return "", err
	}
	if base == "" {
		base = p.DefaultBranch
	}
	return m.createAt(ctx, p, sessionID, branch, func() string {
		if _, _, err := m.git(ctx, "-C", p.Path, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+base); err != nil {
			return base
		}
		return "origin/" + base
	})
}

// CreateAt is Create with the new branch cut at an exact COMMIT (a full sha),
// taken verbatim — no origin/ lookup. Used to fork a session from a turn
// checkpoint, whose HEAD is a commit, not a branch.
func (m *Manager) CreateAt(ctx context.Context, p config.Project, sessionID, branch, commit string) (string, error) {
	if commit == "" || strings.HasPrefix(commit, "-") {
		return "", fmt.Errorf("worktree: invalid start commit %q", commit)
	}
	if err := m.validCreate(p, sessionID, branch); err != nil {
		return "", err
	}
	return m.createAt(ctx, p, sessionID, branch, func() string { return commit })
}

// CreateApp adds a STACK APP's worktree for a session: a worktree of the
// repository at repoPath, at dir (inside the stack session's folder, see
// runtime.StackAppDir), on a new branch cut from origin/<base> (falling back
// to the local <base>). dir must lie inside Root — the same guard Remove
// applies, so an app worktree can always be torn down through it — and must
// not exist yet: a leftover there is somebody's work.
func (m *Manager) CreateApp(ctx context.Context, repoPath, dir, branch, base string) error {
	if m.Root == "" {
		return errors.New("worktree: Root not set")
	}
	if repoPath == "" || branch == "" || base == "" {
		return errors.New("worktree: app repo path, branch and base are required")
	}
	if err := m.guardRemovable(config.Project{Path: repoPath}, dir); err != nil {
		return err
	}
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("worktree: app worktree %s already exists", dir)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	start := base
	if _, _, err := m.git(ctx, "-C", repoPath, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+base); err == nil {
		start = "origin/" + base
	}
	_, _, err := m.git(ctx, "-C", repoPath, "worktree", "add", "-b", branch, dir, start)
	return err
}

func (m *Manager) validCreate(p config.Project, sessionID, branch string) error {
	if m.Root == "" {
		return errors.New("worktree: Root not set")
	}
	if err := validSegment(p.Name); err != nil {
		return fmt.Errorf("worktree: project name: %w", err)
	}
	if err := validSegment(sessionID); err != nil {
		return fmt.Errorf("worktree: session id: %w", err)
	}
	if branch == "" {
		return errors.New("worktree: branch must not be empty")
	}
	return nil
}

// createAt runs `worktree add -b branch` at the commit-ish start resolves to.
// start is called only after the directory checks pass, so a refused create
// never execs the lookup.
func (m *Manager) createAt(ctx context.Context, p config.Project, sessionID, branch string, start func() string) (string, error) {
	dir := filepath.Join(m.Root, p.Name, sessionID)
	if err := m.ensureCleanDir(ctx, p, dir); err != nil {
		return "", err
	}
	if _, _, err := m.git(ctx, "-C", p.Path, "worktree", "add", "-b", branch, dir, start()); err != nil {
		return "", err
	}
	return dir, nil
}

// ensureCleanDir readies Root/<project>/<session> for a fresh `git worktree
// add`: it applies the same idempotence/fail-closed discipline Create documents
// (a registered worktree is an error; an EMPTY unregistered leftover is cleaned;
// a NON-EMPTY unregistered one is kept and named) and then creates the parent
// directory. Shared by Create and CheckoutRef so both honor the discipline.
func (m *Manager) ensureCleanDir(ctx context.Context, p config.Project, dir string) error {
	if _, err := os.Stat(dir); err == nil {
		registered, err := m.registered(ctx, p.Path)
		if err != nil {
			return err
		}
		if registered[filepath.Clean(dir)] {
			return fmt.Errorf("worktree: %s already exists and is a registered worktree of %s", dir, p.Path)
		}
		// Unregistered leftover. Only a trivially empty one is cleaned (see
		// Create's doc comment): anything with contents may hold real uncommitted
		// work whose dirtiness cannot be verified anymore — fail closed and name
		// the dir instead of deleting it.
		if err := m.guardRemovable(p, dir); err != nil {
			return err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("worktree: inspect stale dir: %w", err)
		}
		if len(entries) > 0 {
			return fmt.Errorf("worktree: %s exists but is not a registered worktree of %s and is not empty; refusing to delete it (inspect its contents and remove it manually)", dir, p.Path)
		}
		if err := os.Remove(dir); err != nil {
			return fmt.Errorf("worktree: clean stale dir: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.MkdirAll(filepath.Dir(dir), 0o755)
}

// CheckoutRef adds a DETACHED-HEAD worktree at Root/<p.Name>/<sessionID>
// pointing at an EXISTING ref (a PR head or a branch), for manually opening a
// PR/branch to run and test — unlike Create it never creates OR deletes a
// branch. fetchRef is fetched from origin first (so a PR head passed as
// "pull/<n>/head" or a branch that only lives on the remote is available), then
// pinned to a concrete commit via FETCH_HEAD; on fetch failure it falls back to
// resolving fetchRef locally (origin/<ref>, then refs/heads/<ref>, then the raw
// ref) so an already-present branch still opens offline. Detaching HEAD is what
// makes teardown safe: there is no lola-owned branch to delete, so removing the
// worktree can never touch the upstream branch, and the checkout never conflicts
// with the same branch being live in another worktree. Returns the worktree dir.
func (m *Manager) CheckoutRef(ctx context.Context, p config.Project, sessionID, fetchRef string) (string, error) {
	if m.Root == "" {
		return "", errors.New("worktree: Root not set")
	}
	if err := validSegment(p.Name); err != nil {
		return "", fmt.Errorf("worktree: project name: %w", err)
	}
	if err := validSegment(sessionID); err != nil {
		return "", fmt.Errorf("worktree: session id: %w", err)
	}
	if fetchRef == "" {
		return "", errors.New("worktree: ref must not be empty")
	}
	dir := filepath.Join(m.Root, p.Name, sessionID)
	if err := m.ensureCleanDir(ctx, p, dir); err != nil {
		return "", err
	}

	// Resolve the ref to a concrete commit. Fetch from origin, then pin
	// FETCH_HEAD to a sha (FETCH_HEAD is a mutable global — resolve it now, not
	// at `worktree add` time). Fall back to a local ref when the fetch fails.
	commit := ""
	if _, _, err := m.git(ctx, "-C", p.Path, "fetch", "--no-tags", "origin", fetchRef); err == nil {
		if sha, _, err := m.git(ctx, "-C", p.Path, "rev-parse", "--verify", "--quiet", "FETCH_HEAD"); err == nil {
			commit = strings.TrimSpace(sha)
		}
	}
	if commit == "" {
		for _, cand := range []string{"refs/remotes/origin/" + fetchRef, "refs/heads/" + fetchRef, fetchRef} {
			if sha, _, err := m.git(ctx, "-C", p.Path, "rev-parse", "--verify", "--quiet", cand); err == nil {
				commit = strings.TrimSpace(sha)
				break
			}
		}
	}
	if commit == "" {
		return "", fmt.Errorf("worktree: cannot resolve ref %q in %s (not a PR head, a remote branch, or a local ref)", fetchRef, p.Path)
	}
	if _, _, err := m.git(ctx, "-C", p.Path, "worktree", "add", "--detach", dir, commit); err != nil {
		return "", err
	}
	return dir, nil
}

// ErrBranchCheckedOut is returned by CheckoutTracking when the branch is
// already live in another worktree (git refuses a second checkout). Match with
// errors.Is.
var ErrBranchCheckedOut = errors.New("branch is already checked out in another worktree")

// CheckoutTracking adds a worktree at Root/<p.Name>/<sessionID> on a LOCAL
// branch that tracks origin/<branch> — for opening a PR with an AGENT that will
// push commits back to it. Unlike CheckoutRef (detached, non-owning) it creates
// a real local branch so the agent has somewhere to commit; unlike Create it
// tracks the existing remote branch rather than cutting a fresh one. The remote
// branch is fetched first. A branch already checked out elsewhere yields
// ErrBranchCheckedOut; a local branch that merely already exists is reused.
// Returns the worktree dir.
func (m *Manager) CheckoutTracking(ctx context.Context, p config.Project, sessionID, branch string) (string, error) {
	if m.Root == "" {
		return "", errors.New("worktree: Root not set")
	}
	if err := validSegment(p.Name); err != nil {
		return "", fmt.Errorf("worktree: project name: %w", err)
	}
	if err := validSegment(sessionID); err != nil {
		return "", fmt.Errorf("worktree: session id: %w", err)
	}
	if branch == "" {
		return "", errors.New("worktree: branch must not be empty")
	}
	dir := filepath.Join(m.Root, p.Name, sessionID)
	if err := m.ensureCleanDir(ctx, p, dir); err != nil {
		return "", err
	}

	// Fetch so origin/<branch> exists locally (best-effort: an offline clone may
	// already have it).
	_, _, _ = m.git(ctx, "-C", p.Path, "fetch", "--no-tags", "origin", branch)

	// Create a worktree on a new local branch tracking origin/<branch>.
	_, stderr, err := m.git(ctx, "-C", p.Path, "worktree", "add", "--track", "-b", branch, dir, "origin/"+branch)
	if err == nil {
		return dir, nil
	}
	if isBranchCheckedOut(stderr) {
		return "", fmt.Errorf("%w: %s", ErrBranchCheckedOut, branch)
	}
	// The local branch may already exist (a prior open of this PR) — retry
	// checking it out into the worktree without re-creating it.
	if _, stderr2, err2 := m.git(ctx, "-C", p.Path, "worktree", "add", dir, branch); err2 != nil {
		if isBranchCheckedOut(stderr2) {
			return "", fmt.Errorf("%w: %s", ErrBranchCheckedOut, branch)
		}
		return "", fmt.Errorf("worktree: checkout tracking %s in %s: %w", branch, p.Path, err)
	}
	return dir, nil
}

// isBranchCheckedOut reports whether a `git worktree add` stderr indicates the
// branch is already live in another worktree.
func isBranchCheckedOut(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "already checked out") || strings.Contains(s, "already used by worktree")
}

// Prepare readies a freshly created worktree for an agent: first the
// project's symlinks (each a relative path inside the repo, linked from
// p.Path/<rel> to dir/<rel>), then the post_create commands sequentially via
// `sh -c` in dir with p.Env layered over the daemon environment. The first
// failure aborts with the command and its stderr in the error — a session
// must never start on a partially prepared worktree.
func (m *Manager) Prepare(ctx context.Context, p config.Project, dir string) error {
	for _, rel := range p.Symlinks {
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("worktree: symlink %q: must be a relative path inside the repository", rel)
		}
		link := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return fmt.Errorf("worktree: symlink %q: %w", rel, err)
		}
		if err := os.Symlink(filepath.Join(p.Path, rel), link); err != nil {
			return fmt.Errorf("worktree: symlink %q: %w", rel, err)
		}
	}

	env := os.Environ()
	for _, k := range slices.Sorted(maps.Keys(p.Env)) { // deterministic order
		env = append(env, k+"="+p.Env[k])
	}
	for _, command := range p.PostCreate {
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Dir = dir
		cmd.Env = env
		var errb bytes.Buffer
		cmd.Stderr = &errb
		if err := cmd.Run(); err != nil {
			if msg := strings.TrimSpace(errb.String()); msg != "" {
				return fmt.Errorf("worktree: post_create %q: %w: %s", command, err, msg)
			}
			return fmt.Errorf("worktree: post_create %q: %w", command, err)
		}
	}
	return nil
}

// Dirty reports whether dir has uncommitted changes (`git status
// --porcelain` non-empty) — the same test Remove applies before it refuses
// with ErrDirty. An error means git could not tell.
func (m *Manager) Dirty(ctx context.Context, dir string) (bool, error) {
	out, _, err := m.git(ctx, "-C", dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// teardownWaitDelay is how long Teardown waits for a hook's output pipes after
// its process group was killed: a grandchild that escaped the group must not
// hold Wait open past the deadline.
const teardownWaitDelay = 2 * time.Second

// Teardown runs the project's pre_remove commands in dir — the mirror of
// Prepare's post_create: sequential `sh -c`, cwd dir, p.Env layered over the
// daemon environment. Unlike Prepare it does NOT stop at the first failure: a
// teardown hook is a list of independent cleanups (drop the database, remove a
// docker volume), and one that fails must not skip the next. Every failure is
// returned, in order, for the caller to log; none is fatal.
//
// ctx bounds the WHOLE hook. Each command runs in its own process group, and
// the deadline kills that group rather than just `sh`, so a hung `docker
// compose down` cannot outlive the teardown it was part of. The group is also
// killed once the command returns, so a job it backgrounded does not keep using
// the worktree. A process that leaves the group on purpose (setsid, a daemon
// that double-forks) is outside lola's reach, as it is for post_create. Once ctx is done
// the remaining commands are reported as skipped, not run.
func (m *Manager) Teardown(ctx context.Context, p config.Project, dir string) []error {
	env := os.Environ()
	for _, k := range slices.Sorted(maps.Keys(p.Env)) { // deterministic order
		env = append(env, k+"="+p.Env[k])
	}
	var errs []error
	for _, command := range p.PreRemove {
		if ctx.Err() != nil {
			errs = append(errs, fmt.Errorf("pre_remove %q: skipped: %w", command, ctx.Err()))
			continue
		}
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Dir = dir
		cmd.Env = env
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			// Negative pid = the whole group: sh and everything it started.
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		cmd.WaitDelay = teardownWaitDelay
		errb := &tailBuffer{max: teardownStderrMax}
		cmd.Stderr = errb
		err := cmd.Run()
		if cmd.Process != nil {
			// Whatever the command left running in its group (`cmd &`) goes down
			// with it: Run returning (or WaitDelay closing the pipes) is not proof
			// that nothing still uses the worktree about to be removed.
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		if err != nil {
			if msg := strings.TrimSpace(errb.String()); msg != "" {
				errs = append(errs, fmt.Errorf("pre_remove %q: %w: %s", command, err, lastLine(msg)))
			} else {
				errs = append(errs, fmt.Errorf("pre_remove %q: %w", command, err))
			}
		}
	}
	return errs
}

// teardownStderrMax caps the stderr a pre_remove command keeps while it runs.
// Only the last line is ever logged, so the TAIL is what is retained.
const teardownStderrMax = 4 << 10

// tailBuffer is an io.Writer that keeps only the last max bytes written, so a
// hook streaming stderr until its deadline cannot grow the daemon's memory.
type tailBuffer struct {
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) >= t.max {
		t.buf = append(t.buf[:0], p[len(p)-t.max:]...)
		return n, nil
	}
	if over := len(t.buf) + len(p) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	t.buf = append(t.buf, p...)
	return n, nil
}

func (t *tailBuffer) String() string { return string(t.buf) }

// lastLine is the final line of a command's stderr — where a CLI puts the
// error that ended it — so a chatty hook does not flood the daemon log.
func lastLine(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// Remove deletes the session's worktree and branch. Unless force is set it
// refuses a dirty worktree (uncommitted changes) with an error matching
// ErrDirty. dir must lie strictly inside Root and must not be the project's
// main checkout; a missing branch is not an error (already deleted). Callers
// invoke Remove only for merged or explicitly killed sessions.
//
// A finished session's directory can outlive git's worktree registration: a
// deleted or broken `.git` link, a `git worktree prune`, or a project
// re-clone/gc leave a directory git no longer recognizes. On such a directory
// both the dirty check (`git -C dir status`) and `git worktree remove` fail
// with exit 128 — which wedged the merged/kill cleanup into retrying forever
// and made --force useless too. When a git step fails AND the dir's `.git`
// link is gone, the leftover is removed directly (removeUnregistered).
func (m *Manager) Remove(ctx context.Context, p config.Project, dir, branch string, force bool) error {
	if err := m.guardRemovable(p, dir); err != nil {
		return err
	}
	if !force {
		out, _, err := m.git(ctx, "-C", dir, "status", "--porcelain")
		if err != nil {
			// git cannot inspect the dir. If its `.git` link is gone it is an
			// orphaned leftover git can no longer manage; remove it directly.
			// Otherwise the error is real (transient/permissions) — propagate.
			if m.orphanedLeftover(dir) {
				return m.removeUnregistered(ctx, p, dir, branch, false)
			}
			return fmt.Errorf("worktree: dirty check: %w", err)
		}
		if strings.TrimSpace(out) != "" {
			return fmt.Errorf("worktree %s: %w", dir, ErrDirty)
		}
	}

	args := []string{"-C", p.Path, "worktree", "remove"}
	// A PINNED (sandboxed) worktree was dirty-checked above through its pin.
	// `worktree remove` would repeat that check in a child that finds the
	// repository through the admin dir's `commondir` — a file the agent could
	// rewrite — so the already-made decision is passed as --force instead.
	if force || gitguard.Pinned(dir) {
		args = append(args, "--force")
	}
	args = append(args, dir)
	if _, _, err := m.git(ctx, args...); err != nil {
		// The force path skips the dirty check, so a deregistered leftover first
		// surfaces here: `git worktree remove` also fails exit 128 on it. Same
		// fallback — a real git failure (locked worktree, permissions) keeps its
		// `.git` link and propagates.
		if m.orphanedLeftover(dir) {
			return m.removeUnregistered(ctx, p, dir, branch, force)
		}
		return err
	}
	return m.deleteBranch(ctx, p, branch)
}

// orphanedLeftover reports whether dir is a directory git no longer manages as
// a worktree — its `.git` link (a file for a linked worktree) is absent. Such
// a leftover can be neither dirty-checked nor removed through git. A missing
// dir counts as orphaned too (nothing for git to manage); any other stat error
// (e.g. permissions) counts as not-orphaned so the original git error wins.
func (m *Manager) orphanedLeftover(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return errors.Is(err, fs.ErrNotExist)
}

// removeUnregistered deletes a session directory that git no longer tracks as a
// worktree (see Remove). git cannot verify its cleanliness, so Remove's
// fail-closed discipline holds: without force only a trivially empty leftover
// is deleted; one that still holds entries is kept and reported as ErrDirty
// (matching a dirty registered worktree — the caller keeps it, notifies, and
// can rerun with --force). With force the whole tree is removed. The session's
// branch is deleted only when the directory is actually removed.
func (m *Manager) removeUnregistered(ctx context.Context, p config.Project, dir, branch string, force bool) error {
	if !force {
		entries, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("worktree: inspect orphaned dir: %w", err)
		}
		if len(entries) > 0 {
			return fmt.Errorf("worktree %s: %w", dir, ErrDirty)
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("worktree: remove orphaned dir: %w", err)
	}
	return m.deleteBranch(ctx, p, branch)
}

// DeleteBranch removes a session's local branch WITHOUT removing a worktree —
// the teardown path for a session whose checkout is already gone, where there
// is nothing left for `git worktree remove` to do but the branch still exists.
// It first prunes git's stale worktree registrations, because a directory that
// vanished from disk leaves an administrative entry behind and git then refuses
// to delete "a branch checked out in a worktree". "" is a no-op, so callers can
// pass a non-owned branch unconditionally.
//
// Like the worktree-removing path it force-deletes (`branch -D`): a squash- or
// rebase-merged PR leaves a local branch git does not consider merged, and the
// non-force `-d` would refuse exactly the case this exists for.
func (m *Manager) DeleteBranch(ctx context.Context, p config.Project, branch string) error {
	if branch == "" {
		return nil
	}
	// Best-effort: a prune failure only means the -D below may refuse, which the
	// caller surfaces as a normal error.
	_, _, _ = m.git(ctx, "-C", p.Path, "worktree", "prune")
	return m.deleteBranch(ctx, p, branch)
}

// deleteBranch removes the session's local branch; "" and an already-missing
// branch are both no-ops.
func (m *Manager) deleteBranch(ctx context.Context, p config.Project, branch string) error {
	if branch == "" {
		return nil
	}
	if _, stderr, err := m.git(ctx, "-C", p.Path, "branch", "-D", branch); err != nil {
		if strings.Contains(stderr, "not found") { // already gone
			return nil
		}
		return err
	}
	return nil
}

// BranchExists reports whether refs/heads/<branch> exists in the project's
// repository. A rev-parse exit failure means "no such ref" (which includes a
// broken repo — the subsequent `worktree add` fails loudly there anyway);
// only errors before git could run (e.g. binary missing) propagate.
func (m *Manager) BranchExists(ctx context.Context, p config.Project, branch string) (bool, error) {
	_, _, err := m.git(ctx, "-C", p.Path, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return false, nil
	}
	return false, err
}

// RemoveDir deletes a STACK session folder (internal/runtime/stack.go) once
// its app worktrees are gone. It is not a worktree, so git has nothing to say
// about it; the guard is the same as for one — strictly inside Root, never
// the root itself.
func (m *Manager) RemoveDir(dir string) error {
	if err := m.guardRemovable(config.Project{}, dir); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// Ahead counts the commits on branch that its base does not have
// (origin/<base> when that remote ref exists, else <base>) in the repository at
// repoPath. A stack session's teardown asks it for an app that has no PR: a
// branch carrying commits nobody pushed into a PR is work, and deleting it
// would lose that work although the worktree itself is clean.
func (m *Manager) Ahead(ctx context.Context, repoPath, base, branch string) (int, error) {
	if repoPath == "" || base == "" || branch == "" {
		return 0, errors.New("worktree: ahead: repo, base and branch required")
	}
	start := base
	if _, _, err := m.git(ctx, "-C", repoPath, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+base); err == nil {
		start = "origin/" + base
	}
	out, _, err := m.git(ctx, "-C", repoPath, "rev-list", "--count", start+".."+"refs/heads/"+branch)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("worktree: ahead: %q is not a count", strings.TrimSpace(out))
	}
	return n, nil
}

// List returns the session worktree directories under Root/<p.Name> that git
// (worktree list --porcelain, run in p.Path) confirms as registered
// worktrees. Directories git does not know about are stale and omitted; a
// missing Root/<p.Name> means no sessions, not an error.
func (m *Manager) List(ctx context.Context, p config.Project) ([]string, error) {
	base := filepath.Join(m.Root, p.Name)
	entries, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	registered, err := m.registered(ctx, p.Path)
	if err != nil {
		return nil, err
	}
	dirs := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if dir := filepath.Join(base, e.Name()); registered[filepath.Clean(dir)] {
			dirs = append(dirs, dir)
		}
	}
	return dirs, nil
}

// registered returns the set of worktree paths (cleaned) that the repository
// at repoPath knows about, keyed for O(1) membership checks. The main
// checkout is included — which is exactly why guardRemovable exists.
func (m *Manager) registered(ctx context.Context, repoPath string) (map[string]bool, error) {
	out, _, err := m.git(ctx, "-C", repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			set[filepath.Clean(path)] = true
		}
	}
	return set, nil
}

// guardRemovable enforces the destructive-op discipline: dir must lie
// strictly inside Root, must not be the project's main checkout, and must not
// contain it. Mixed relative/absolute paths fail closed (filepath.Rel
// errors).
func (m *Manager) guardRemovable(p config.Project, dir string) error {
	if m.Root == "" {
		return errors.New("worktree: Root not set")
	}
	root := filepath.Clean(m.Root)
	d := filepath.Clean(dir)

	rel, err := filepath.Rel(root, d)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("worktree: refusing to remove %s: outside worktree root %s", dir, m.Root)
	}

	main := filepath.Clean(p.Path)
	if d == main {
		return fmt.Errorf("worktree: refusing to remove %s: it is the project's main checkout", dir)
	}
	if rel, err := filepath.Rel(d, main); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("worktree: refusing to remove %s: it contains the project's main checkout %s", dir, p.Path)
	}
	return nil
}

// validSegment rejects names that would escape their intended single path
// level under Root (separators, "..", empty).
func validSegment(s string) error {
	if s == "" {
		return errors.New("must not be empty")
	}
	if s == "." || s == ".." || s != filepath.Base(filepath.Clean(s)) {
		return fmt.Errorf("invalid path segment %q", s)
	}
	return nil
}
