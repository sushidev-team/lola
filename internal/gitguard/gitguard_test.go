package gitguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These run REAL git: the attack under test is git honouring a config it
// found by following a rewritten `.git` pointer, which no fake can model.

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// setup returns a repo, a linked worktree of it, and the worktree's pin.
func setup(t *testing.T) (repo, wt string, pin Pin) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@x")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@x")
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	repo = filepath.Join(base, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "a.txt")
	git(t, repo, "commit", "-qm", "init")
	wt = filepath.Join(base, "wt")
	git(t, repo, "worktree", "add", "-q", "-b", "s", wt)
	pin, err := Capture(wt)
	if err != nil {
		t.Fatal(err)
	}
	if pin.CommonDir != filepath.Join(repo, ".git") || pin.GitDir != filepath.Join(repo, ".git", "worktrees", "wt") {
		t.Fatalf("captured pin = %+v", pin)
	}
	t.Cleanup(func() { Clear(wt) })
	return repo, wt, pin
}

// hijack does what a sandboxed agent can: point the worktree's `.git` at a
// git dir of its own whose config runs a command on the next host git call —
// both through core.fsmonitor and through a clean filter (the attacker picks
// the filter's NAME, so no fixed override list can cover it).
func hijack(t *testing.T, wt, marker string) {
	t.Helper()
	fake := filepath.Join(wt, ".evil")
	git(t, wt, "init", "-q", "--bare", fake) // a valid git dir to point at
	cfg := "[core]\n\tbare = false\n\tfsmonitor = \"touch " + marker + ".fsmonitor; false\"\n" +
		"[filter \"x\"]\n\tclean = \"touch " + marker + ".filter; cat\"\n"
	if err := os.WriteFile(filepath.Join(fake, "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".gitattributes"), []byte("* filter=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+fake+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fired(marker string) []string {
	var out []string
	for _, s := range []string{".fsmonitor", ".filter"} {
		if _, err := os.Stat(marker + s); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// hostAdd is the host-side call a checkpoint makes (`git add -A` into an
// index), run the way every lola git exec is.
func hostAdd(wt string) ([]byte, error) {
	cmd := exec.Command("git", "-C", wt, "add", "-A")
	cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(filepath.Dir(wt), "tmp-index"))
	Apply(cmd)
	return cmd.CombinedOutput()
}

func TestUnpinnedHostGitRunsTheAgentsConfig(t *testing.T) {
	// The control: proves the attack is real, so the pinned test below is
	// not passing for an unrelated reason.
	_, wt, _ := setup(t)
	marker := filepath.Join(t.TempDir(), "pwned")
	hijack(t, wt, marker)
	_, _ = hostAdd(wt)
	if len(fired(marker)) == 0 {
		t.Fatal("control: the hijacked config did not run unpinned — the attack model is off")
	}
}

func TestPinnedHostGitIgnoresARewrittenPointer(t *testing.T) {
	_, wt, pin := setup(t)
	Set(wt, pin)
	marker := filepath.Join(t.TempDir(), "pwned")
	hijack(t, wt, marker)
	if out, err := hostAdd(wt); err != nil {
		t.Fatalf("pinned git add failed: %v\n%s", err, out)
	}
	if f := fired(marker); len(f) != 0 {
		t.Fatalf("host git ran the agent's config through %v despite the pin", f)
	}
	// And it worked against the REAL repository.
	cmd := exec.Command("git", "-C", wt, "status", "--porcelain")
	Apply(cmd)
	out, err := cmd.Output()
	if err != nil || !strings.Contains(string(out), "new.txt") {
		t.Fatalf("pinned status = %q, %v", out, err)
	}
	// A subdirectory and a nested pinned worktree resolve to the right pin.
	if at, _, ok := Lookup(filepath.Join(wt, "sub", "dir")); !ok || at != wt {
		t.Fatalf("subdir lookup = %q %v", at, ok)
	}
	nested := filepath.Join(wt, ".lola", "repos", "api")
	Set(nested, Pin{GitDir: "/x/g", CommonDir: "/x"})
	defer Clear(nested)
	if _, p, _ := Lookup(filepath.Join(nested, "f")); p.GitDir != "/x/g" {
		t.Fatalf("nested worktree resolved to the outer pin: %+v", p)
	}
}

func TestDeniedWorktreeRefusesHostGit(t *testing.T) {
	_, wt, _ := setup(t)
	Deny(wt)
	marker := filepath.Join(t.TempDir(), "pwned")
	hijack(t, wt, marker)
	if _, err := hostAdd(wt); err == nil {
		t.Fatal("host git on a denied worktree succeeded")
	}
	if f := fired(marker); len(f) != 0 {
		t.Fatalf("denied worktree still ran the agent's config: %v", f)
	}
}

func TestApplyFindsTheDirectory(t *testing.T) {
	Set("/w/s1", Pin{GitDir: "/r/.git/worktrees/s1", CommonDir: "/r/.git"})
	defer Clear("/w/s1")
	cmd := exec.Command("git", "-C", "/w/s1", "status")
	Apply(cmd)
	if !strings.Contains(strings.Join(cmd.Env, "\n"), "GIT_DIR=/r/.git/worktrees/s1") {
		t.Fatalf("-C not honoured: %v", cmd.Env)
	}
	cmd = exec.Command("git", "diff")
	cmd.Dir = "/w/s1"
	Apply(cmd)
	if !strings.Contains(strings.Join(cmd.Env, "\n"), "GIT_WORK_TREE=/w/s1") {
		t.Fatal("cmd.Dir not honoured")
	}
	cmd = exec.Command("git", "-C", "/elsewhere", "status")
	Apply(cmd)
	if cmd.Env != nil {
		t.Fatal("unpinned directory got an env")
	}
}
