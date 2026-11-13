package gitdiff

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseNumstatZ(t *testing.T) {
	z := "3\t1\ta.go\x00-\t-\tlogo.png\x002\t0\t\x00old.go\x00new.go\x00"
	got := parseNumstatZ(z)
	if want := (Stat{Files: 3, Additions: 5, Deletions: 1}); got != want {
		t.Fatalf("parseNumstatZ = %+v, want %+v", got, want)
	}
}

func TestStatRealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write("a.txt", "one\ntwo\n")
	git("add", ".")
	git("commit", "-qm", "base")
	git("checkout", "-qb", "feature")
	write("a.txt", "one\nTWO\nthree\n") // committed: +2 -1
	git("commit", "-qam", "work")
	write("c.txt", "x\n") // untracked
	got, err := Differ{}.Stat(context.Background(), dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Stat{Files: 2, Additions: 2, Deletions: 1, Untracked: 1}); got != want {
		t.Fatalf("Stat = %+v, want %+v", got, want)
	}
}
