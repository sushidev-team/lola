package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
)

// stub is one canned response of the fake git script, matched by substring
// against the full argv line ("$*"). First match wins; unmatched invocations
// succeed silently with exit 0.
type stub struct {
	match  string
	stdout string
	stderr string
	exit   int
}

// fakeGit installs a shell script standing in for the git binary: it appends
// its argv (one line per invocation) to <dir>/args.log, then answers per the
// stubs. Pattern mirrors internal/tmux fake-bin helper and
// internal/tmux's fakeTmux; no real git is ever run.
func fakeGit(t *testing.T, stubs ...stub) (bin, argsLog string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "git")
	argsLog = filepath.Join(dir, "args.log")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("echo \"$@\" >> \"" + argsLog + "\"\n")
	b.WriteString("case \"$*\" in\n")
	for _, s := range stubs {
		b.WriteString("*\"" + s.match + "\"*)\n")
		if s.stdout != "" {
			b.WriteString("cat <<'LOLA_EOF'\n" + s.stdout + "\nLOLA_EOF\n")
		}
		if s.stderr != "" {
			b.WriteString("cat <<'LOLA_EOF' >&2\n" + s.stderr + "\nLOLA_EOF\n")
		}
		fmt.Fprintf(&b, "exit %d\n;;\n", s.exit)
	}
	b.WriteString("esac\nexit 0\n")
	if err := os.WriteFile(bin, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsLog
}

// loggedArgs returns the argv log, one invocation per line, or "" when git
// was never invoked.
func loggedArgs(t *testing.T, argsLog string) string {
	t.Helper()
	b, err := os.ReadFile(argsLog)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(string(b), "\n")
}

func testProject(repo string) config.Project {
	return config.Project{Name: "nori", Path: repo, DefaultBranch: "main"}
}

func TestDefaultRootHonorsLolaHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	got, err := DefaultRoot()
	if err != nil {
		t.Fatalf("DefaultRoot: %v", err)
	}
	if want := filepath.Join(home, "worktrees"); got != want {
		t.Errorf("DefaultRoot = %q, want %q", got, want)
	}
}

func TestCreateFreshFromOriginDefaultBranch(t *testing.T) {
	bin, argsLog := fakeGit(t) // everything succeeds, incl. the rev-parse probe
	root, repo := t.TempDir(), t.TempDir()
	m := &Manager{GitBin: bin, Root: root}

	dir, err := m.Create(context.Background(), testProject(repo), "s1", "lola/NORI-12-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if want := filepath.Join(root, "nori", "s1"); dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	want := strings.Join([]string{
		"-C " + repo + " rev-parse --verify --quiet refs/remotes/origin/main",
		"-C " + repo + " worktree add -b lola/NORI-12-1 " + dir + " origin/main",
	}, "\n")
	if got := loggedArgs(t, argsLog); got != want {
		t.Errorf("git calls:\n%s\nwant:\n%s", got, want)
	}
}

func TestCreateFallsBackToLocalDefaultBranch(t *testing.T) {
	// rev-parse says origin/main does not exist -> start point is plain main.
	bin, argsLog := fakeGit(t, stub{match: "rev-parse", exit: 1})
	root, repo := t.TempDir(), t.TempDir()
	m := &Manager{GitBin: bin, Root: root}

	dir, err := m.Create(context.Background(), testProject(repo), "s1", "lola/NORI-12-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wantAdd := "-C " + repo + " worktree add -b lola/NORI-12-1 " + dir + " main"
	lines := strings.Split(loggedArgs(t, argsLog), "\n")
	if got := lines[len(lines)-1]; got != wantAdd {
		t.Errorf("worktree add = %q, want fallback start point %q", got, wantAdd)
	}
}

func TestCreateExistingRegisteredWorktreeErrors(t *testing.T) {
	root, repo := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "nori", "s1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	porcelain := "worktree " + repo + "\nHEAD 1111\nbranch refs/heads/main\n\n" +
		"worktree " + dir + "\nHEAD 2222\nbranch refs/heads/lola/NORI-12-1"
	bin, argsLog := fakeGit(t, stub{match: "worktree list --porcelain", stdout: porcelain})
	m := &Manager{GitBin: bin, Root: root}

	if _, err := m.Create(context.Background(), testProject(repo), "s1", "lola/NORI-12-1"); err == nil {
		t.Fatal("Create: want error for existing registered worktree, got nil")
	}
	if got := loggedArgs(t, argsLog); strings.Contains(got, "worktree add") {
		t.Errorf("worktree add must not run for a registered dir; git calls:\n%s", got)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("registered dir must not be removed: %v", err)
	}
}

func TestCreateCleansEmptyStaleUnregisteredDir(t *testing.T) {
	root, repo := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "nori", "s1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// git only knows the main checkout: the empty dir is a stale leftover
	// (crash between mkdir and registration) and safe to clean.
	bin, argsLog := fakeGit(t, stub{match: "worktree list --porcelain", stdout: "worktree " + repo})
	m := &Manager{GitBin: bin, Root: root}

	got, err := m.Create(context.Background(), testProject(repo), "s1", "lola/NORI-12-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got != dir {
		t.Errorf("dir = %q, want %q", got, dir)
	}
	want := strings.Join([]string{
		"-C " + repo + " worktree list --porcelain",
		"-C " + repo + " rev-parse --verify --quiet refs/remotes/origin/main",
		"-C " + repo + " worktree add -b lola/NORI-12-1 " + dir + " origin/main",
	}, "\n")
	if got := loggedArgs(t, argsLog); got != want {
		t.Errorf("git calls:\n%s\nwant:\n%s", got, want)
	}
}

// A NON-EMPTY unregistered dir may still hold real uncommitted agent work
// (the registration vanishes when the project is re-cloned or its path
// re-pointed, or when the worktree's .git pointer is lost and pruned) —
// Create must fail closed, never RemoveAll it (destructive-op discipline:
// every removal path refuses dirty work, and here dirtiness cannot even be
// checked).
func TestCreateRefusesNonEmptyUnregisteredDir(t *testing.T) {
	root, repo := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "nori", "s1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "uncommitted-work.go")
	if err := os.WriteFile(marker, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin, argsLog := fakeGit(t, stub{match: "worktree list --porcelain", stdout: "worktree " + repo})
	m := &Manager{GitBin: bin, Root: root}

	_, err := m.Create(context.Background(), testProject(repo), "s1", "lola/NORI-12-1")
	if err == nil {
		t.Fatal("Create: want fail-closed error for non-empty unregistered dir, got nil")
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("error %q must name the refused dir %s", err, dir)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Errorf("contents of the unregistered dir must never be deleted: %v", statErr)
	}
	if got := loggedArgs(t, argsLog); strings.Contains(got, "worktree add") {
		t.Errorf("worktree add must not run after the refusal; git calls:\n%s", got)
	}
}

func TestBranchExists(t *testing.T) {
	repo := t.TempDir()
	// rev-parse succeeds -> branch exists.
	bin, argsLog := fakeGit(t)
	m := &Manager{GitBin: bin, Root: t.TempDir()}
	ok, err := m.BranchExists(context.Background(), testProject(repo), "lola/NORI-12-1")
	if err != nil || !ok {
		t.Fatalf("BranchExists = (%v, %v), want (true, nil)", ok, err)
	}
	if want := "-C " + repo + " rev-parse --verify --quiet refs/heads/lola/NORI-12-1"; loggedArgs(t, argsLog) != want {
		t.Errorf("git calls %q, want %q", loggedArgs(t, argsLog), want)
	}

	// rev-parse exit failure -> no such branch, not an error.
	bin, _ = fakeGit(t, stub{match: "rev-parse", exit: 1})
	m = &Manager{GitBin: bin, Root: t.TempDir()}
	ok, err = m.BranchExists(context.Background(), testProject(repo), "lola/NORI-12-1")
	if err != nil || ok {
		t.Fatalf("BranchExists on exit 1 = (%v, %v), want (false, nil)", ok, err)
	}

	// git binary missing -> the error propagates (fail closed).
	m = &Manager{GitBin: "/nonexistent/git", Root: t.TempDir()}
	if _, err := m.BranchExists(context.Background(), testProject(repo), "b"); err == nil {
		t.Fatal("BranchExists with missing git: want error, got nil")
	}
}

func TestCreateRejectsPathEscapingNames(t *testing.T) {
	bin, argsLog := fakeGit(t)
	m := &Manager{GitBin: bin, Root: t.TempDir()}
	p := testProject(t.TempDir())

	for _, bad := range []string{"", "..", "a/b", "../evil"} {
		if _, err := m.Create(context.Background(), p, bad, "b"); err == nil {
			t.Errorf("Create with session id %q: want error, got nil", bad)
		}
	}
	p.Name = "../oops"
	if _, err := m.Create(context.Background(), p, "s1", "b"); err == nil {
		t.Error("Create with escaping project name: want error, got nil")
	}
	if got := loggedArgs(t, argsLog); got != "" {
		t.Errorf("git must never run for invalid names; got:\n%s", got)
	}
}

func TestRemoveRefusesDirtyWorktree(t *testing.T) {
	bin, argsLog := fakeGit(t, stub{match: "status --porcelain", stdout: " M main.go"})
	root, repo := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "nori", "s1")
	m := &Manager{GitBin: bin, Root: root}

	err := m.Remove(context.Background(), testProject(repo), dir, "lola/NORI-12-1", false)
	if !errors.Is(err, ErrDirty) {
		t.Fatalf("Remove dirty: want ErrDirty, got %v", err)
	}
	if want := "-C " + dir + " status --porcelain"; loggedArgs(t, argsLog) != want {
		t.Errorf("git calls:\n%s\nwant only the dirty check:\n%s", loggedArgs(t, argsLog), want)
	}
}

func TestRemoveCleanWorktreeAndBranch(t *testing.T) {
	bin, argsLog := fakeGit(t) // status outputs nothing: clean
	root, repo := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "nori", "s1")
	m := &Manager{GitBin: bin, Root: root}

	if err := m.Remove(context.Background(), testProject(repo), dir, "lola/NORI-12-1", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	want := strings.Join([]string{
		"-C " + dir + " status --porcelain",
		"-C " + repo + " worktree remove " + dir,
		"-C " + repo + " branch -D lola/NORI-12-1",
	}, "\n")
	if got := loggedArgs(t, argsLog); got != want {
		t.Errorf("git calls:\n%s\nwant:\n%s", got, want)
	}
}

func TestRemoveForceSkipsDirtyCheck(t *testing.T) {
	bin, argsLog := fakeGit(t, stub{match: "status --porcelain", stdout: " M main.go"})
	root, repo := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "nori", "s1")
	m := &Manager{GitBin: bin, Root: root}

	if err := m.Remove(context.Background(), testProject(repo), dir, "lola/NORI-12-1", true); err != nil {
		t.Fatalf("Remove --force: %v", err)
	}
	want := strings.Join([]string{
		"-C " + repo + " worktree remove --force " + dir,
		"-C " + repo + " branch -D lola/NORI-12-1",
	}, "\n")
	if got := loggedArgs(t, argsLog); got != want {
		t.Errorf("git calls:\n%s\nwant force removal without dirty check:\n%s", got, want)
	}
}

func TestRemoveIgnoresMissingBranch(t *testing.T) {
	bin, _ := fakeGit(t, stub{
		match:  "branch -D",
		stderr: "error: branch 'lola/NORI-12-1' not found.",
		exit:   1,
	})
	root, repo := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "nori", "s1")
	m := &Manager{GitBin: bin, Root: root}

	if err := m.Remove(context.Background(), testProject(repo), dir, "lola/NORI-12-1", false); err != nil {
		t.Fatalf("Remove with already-deleted branch: want nil, got %v", err)
	}
}

// A finished session's directory can lose its `.git` link (a broken/deleted
// link, `git worktree prune`, or a project re-clone deregisters it). git then
// fails the dirty check with exit 128, so the leftover is handled directly. A
// leftover that still holds files is unverifiable, so it is kept as ErrDirty
// (never silently deleted) and git worktree remove is never attempted.
func TestRemoveOrphanedLeftoverNonEmptyKeptAsDirty(t *testing.T) {
	bin, argsLog := fakeGit(t, stub{
		match:  "status --porcelain",
		stderr: "fatal: not a git repository (or any of the parent directories): .git",
		exit:   128,
	})
	root, repo := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "nori", "s1")
	if err := os.MkdirAll(filepath.Join(dir, "storage", "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "storage", "logs", "app.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manager{GitBin: bin, Root: root}

	err := m.Remove(context.Background(), testProject(repo), dir, "lola/NORI-12-1", false)
	if !errors.Is(err, ErrDirty) {
		t.Fatalf("Remove orphaned non-empty leftover: want ErrDirty, got %v", err)
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Errorf("kept leftover must stay on disk: %v", statErr)
	}
	// Only the failed dirty check ran — never `worktree remove` (git cannot) nor
	// `branch -D` (the dir is kept for inspection).
	if got := loggedArgs(t, argsLog); got != "-C "+dir+" status --porcelain" {
		t.Errorf("git calls:\n%s\nwant only the failed dirty check", got)
	}
}

// An empty orphaned leftover has nothing to lose, so a non-force Remove deletes
// it and prunes the branch — closing out the session instead of retrying the
// impossible git removal forever.
func TestRemoveOrphanedLeftoverEmptyDeletedWithBranch(t *testing.T) {
	bin, argsLog := fakeGit(t, stub{
		match:  "status --porcelain",
		stderr: "fatal: not a git repository (or any of the parent directories): .git",
		exit:   128,
	})
	root, repo := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "nori", "s1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := &Manager{GitBin: bin, Root: root}

	if err := m.Remove(context.Background(), testProject(repo), dir, "lola/NORI-12-1", false); err != nil {
		t.Fatalf("Remove empty orphaned leftover: %v", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("empty leftover must be deleted; stat err = %v", statErr)
	}
	want := strings.Join([]string{
		"-C " + dir + " status --porcelain",
		"-C " + repo + " branch -D lola/NORI-12-1",
	}, "\n")
	if got := loggedArgs(t, argsLog); got != want {
		t.Errorf("git calls:\n%s\nwant:\n%s", got, want)
	}
}

// --force must be able to clear an orphaned leftover git can no longer remove
// (git worktree remove --force itself fails exit 128 on it): the directory is
// deleted directly and the branch pruned.
func TestRemoveOrphanedLeftoverForceDeletes(t *testing.T) {
	bin, argsLog := fakeGit(t, stub{
		match:  "worktree remove",
		stderr: "fatal: '/x/nori/s1' is not a working tree",
		exit:   128,
	})
	root, repo := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "nori", "s1")
	if err := os.MkdirAll(filepath.Join(dir, "storage"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "storage", "app.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manager{GitBin: bin, Root: root}

	if err := m.Remove(context.Background(), testProject(repo), dir, "lola/NORI-12-1", true); err != nil {
		t.Fatalf("Remove --force orphaned leftover: %v", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("--force must delete the leftover; stat err = %v", statErr)
	}
	// force skips the dirty check; git worktree remove --force was attempted
	// (and failed, since git no longer knows the dir), then branch -D ran.
	want := strings.Join([]string{
		"-C " + repo + " worktree remove --force " + dir,
		"-C " + repo + " branch -D lola/NORI-12-1",
	}, "\n")
	if got := loggedArgs(t, argsLog); got != want {
		t.Errorf("git calls:\n%s\nwant:\n%s", got, want)
	}
}

// A real (non-orphan) git failure whose `.git` link is intact must propagate
// unchanged — the fallback must not swallow it and delete a live worktree.
func TestRemoveRealErrorWithIntactGitLinkPropagates(t *testing.T) {
	bin, _ := fakeGit(t, stub{
		match:  "status --porcelain",
		stderr: "fatal: index file corrupt",
		exit:   128,
	})
	root, repo := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "nori", "s1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /somewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manager{GitBin: bin, Root: root}

	err := m.Remove(context.Background(), testProject(repo), dir, "lola/NORI-12-1", false)
	if err == nil || errors.Is(err, ErrDirty) {
		t.Fatalf("real git error with intact .git must propagate as-is, got %v", err)
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Errorf("worktree must not be deleted on a real git error: %v", statErr)
	}
}

func TestRemoveRefusesDirOutsideRoot(t *testing.T) {
	bin, argsLog := fakeGit(t)
	root := t.TempDir()
	m := &Manager{GitBin: bin, Root: root}
	p := testProject(t.TempDir())

	for _, dir := range []string{
		t.TempDir(),                        // entirely elsewhere
		root,                               // the root itself
		filepath.Join(root, ".."),          // parent of root
		filepath.Join(root, "..", "other"), // sibling via ..
	} {
		if err := m.Remove(context.Background(), p, dir, "b", true); err == nil {
			t.Errorf("Remove(%q): want path-guard error, got nil", dir)
		}
	}
	if got := loggedArgs(t, argsLog); got != "" {
		t.Errorf("git must never run for guarded paths; got:\n%s", got)
	}
}

func TestRemoveRefusesMainCheckout(t *testing.T) {
	bin, argsLog := fakeGit(t)
	root := t.TempDir()
	repo := filepath.Join(root, "repo") // main checkout living inside Root
	m := &Manager{GitBin: bin, Root: root}
	p := testProject(repo)

	if err := m.Remove(context.Background(), p, repo, "b", true); err == nil {
		t.Error("Remove(main checkout): want error, got nil")
	}
	// A dir that contains the main checkout is refused too.
	p.Path = filepath.Join(root, "nori", "s1", "nested-repo")
	if err := m.Remove(context.Background(), p, filepath.Join(root, "nori", "s1"), "b", true); err == nil {
		t.Error("Remove(dir containing main checkout): want error, got nil")
	}
	if got := loggedArgs(t, argsLog); got != "" {
		t.Errorf("git must never run for guarded paths; got:\n%s", got)
	}
}

func TestPrepareSymlinksThenPostCreate(t *testing.T) {
	repo, dir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "local.toml"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := testProject(repo)
	p.Symlinks = []string{".env", "config/local.toml"}
	p.PostCreate = []string{"cat .env > copied.txt"} // proves links exist before post_create, cwd = dir

	if err := (&Manager{Root: t.TempDir()}).Prepare(context.Background(), p, dir); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	target, err := os.Readlink(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("Readlink .env: %v", err)
	}
	if want := filepath.Join(repo, ".env"); target != want {
		t.Errorf(".env -> %q, want %q", target, want)
	}
	if _, err := os.Readlink(filepath.Join(dir, "config", "local.toml")); err != nil {
		t.Errorf("nested symlink: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "copied.txt"))
	if err != nil {
		t.Fatalf("post_create output: %v", err)
	}
	if string(got) != "SECRET=1\n" {
		t.Errorf("copied.txt = %q, want content read through the symlink", got)
	}
}

func TestPrepareRejectsEscapingSymlinks(t *testing.T) {
	for _, bad := range []string{"/etc/passwd", "../secrets", "a/../../x", ".."} {
		repo, dir := t.TempDir(), t.TempDir()
		p := testProject(repo)
		p.Symlinks = []string{bad}
		p.PostCreate = []string{"touch ran.txt"}

		if err := (&Manager{Root: t.TempDir()}).Prepare(context.Background(), p, dir); err == nil {
			t.Errorf("Prepare with symlink %q: want error, got nil", bad)
		}
		if _, err := os.Stat(filepath.Join(dir, "ran.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("symlink %q: post_create must not run after rejection", bad)
		}
	}
}

func TestPreparePostCreateEnvThreading(t *testing.T) {
	t.Setenv("LOLA_TEST_INHERITED", "from-parent")
	repo, dir := t.TempDir(), t.TempDir()
	p := testProject(repo)
	p.Env = map[string]string{"LOLA_TEST_PROJECT": "from-project"}
	p.PostCreate = []string{`printf '%s|%s' "$LOLA_TEST_INHERITED" "$LOLA_TEST_PROJECT" > env.txt`}

	if err := (&Manager{Root: t.TempDir()}).Prepare(context.Background(), p, dir); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "from-parent|from-project"; string(got) != want {
		t.Errorf("env.txt = %q, want %q (os.Environ + project env)", got, want)
	}
}

func TestPreparePostCreateAbortsOnFirstFailure(t *testing.T) {
	repo, dir := t.TempDir(), t.TempDir()
	p := testProject(repo)
	p.PostCreate = []string{
		"touch first.txt",
		"echo boom >&2; exit 3",
		"touch third.txt",
	}

	err := (&Manager{Root: t.TempDir()}).Prepare(context.Background(), p, dir)
	if err == nil {
		t.Fatal("Prepare: want error from failing post_create, got nil")
	}
	if !strings.Contains(err.Error(), "echo boom >&2; exit 3") {
		t.Errorf("error %q must name the failing command", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q must carry the command's stderr", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "first.txt")); err != nil {
		t.Errorf("first command should have run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "third.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Error("commands after the failure must not run")
	}
}

func TestListReturnsOnlyRegisteredDirs(t *testing.T) {
	root, repo := t.TempDir(), t.TempDir()
	base := filepath.Join(root, "nori")
	s1 := filepath.Join(base, "s1")
	s2 := filepath.Join(base, "s2") // stale: on disk but unregistered
	for _, d := range []string{s1, s2} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "notes.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	porcelain := "worktree " + repo + "\nHEAD 1111\nbranch refs/heads/main\n\n" +
		"worktree " + s1 + "\nHEAD 2222\nbranch refs/heads/lola/NORI-12-1"
	bin, argsLog := fakeGit(t, stub{match: "worktree list --porcelain", stdout: porcelain})
	m := &Manager{GitBin: bin, Root: root}

	got, err := m.List(context.Background(), testProject(repo))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if want := []string{s1}; !slices.Equal(got, want) {
		t.Errorf("List = %v, want %v", got, want)
	}
	if want := "-C " + repo + " worktree list --porcelain"; loggedArgs(t, argsLog) != want {
		t.Errorf("invoked %q, want %q", loggedArgs(t, argsLog), want)
	}
}

func TestListMissingBaseIsEmptyNotError(t *testing.T) {
	m := &Manager{GitBin: "/nonexistent/git", Root: t.TempDir()}
	got, err := m.List(context.Background(), testProject(t.TempDir()))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("want empty non-nil slice, got %#v", got)
	}
}
