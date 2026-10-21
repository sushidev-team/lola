package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCodeIdentityUsesCommittedPathAndMode(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Test", "-c", "user.email=test@localhost"}, args...)...)
		if body, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v %s", err, body)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-b", "main")
	if err := os.Mkdir(filepath.Join(dir, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	write("src/a.txt", "one")
	write("src/[literal].txt", "literal")
	git("add", ".")
	git("commit", "-m", "initial")
	read := func(path string) CodeIdentity {
		t.Helper()
		got, err := (CodeReader{}).Read(context.Background(), dir, "main", path)
		if err != nil || !got.Exists {
			t.Fatalf("%s: %+v %v", path, got, err)
		}
		return got
	}
	first := read("src/a.txt")
	tree := read("src")
	root := read(".")
	read("src/[literal].txt")
	// Working-tree edits and inherited Git repository overrides are irrelevant.
	write("src/a.txt", "dirty")
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "wrong"))
	if got := read("src/a.txt"); got != first {
		t.Fatal("read dirty tree or inherited GIT_DIR")
	}
	t.Setenv("GIT_DIR", "") // fixture commands need the variable absent
	os.Unsetenv("GIT_DIR")
	write("other.txt", "unrelated")
	git("add", "other.txt")
	git("commit", "-m", "unrelated")
	if got := read("src/a.txt"); got.Commit == first.Commit || got.Digest != first.Digest {
		t.Fatal("unrelated commit invalidated path")
	}
	if read("src").Digest != tree.Digest || read(".").Digest == root.Digest {
		t.Fatal("directory scope incorrect")
	}
	git("add", "src/a.txt")
	git("commit", "-m", "changed")
	changed := read("src/a.txt")
	if changed.Digest == first.Digest {
		t.Fatal("content change missed")
	}
	if old, err := (CodeReader{}).ReadCommit(context.Background(), dir, first.Commit, "src/a.txt"); err != nil || old != first {
		t.Fatal("exact verification read followed the moving branch", err)
	}
	git("update-index", "--chmod=+x", "src/a.txt")
	git("commit", "-m", "mode")
	if read("src/a.txt").Digest == changed.Digest {
		t.Fatal("mode change missed")
	}
	git("rm", "-f", "src/a.txt")
	git("commit", "-m", "removed")
	if got, err := (CodeReader{}).Read(context.Background(), dir, "main", "src/a.txt"); err != nil || got.Exists {
		t.Fatalf("deletion: %+v %v", got, err)
	}
	if _, err := (CodeReader{}).Read(context.Background(), dir, "missing", "."); err == nil {
		t.Fatal("missing branch accepted")
	}
}

func TestCodeReaderRejectsUntrustedReferencesBeforeExec(t *testing.T) {
	r := CodeReader{Run: func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("executed invalid reference")
		return nil, nil
	}}
	for _, tt := range []struct{ branch, path string }{{"main", "../outside"}, {"main", "/outside"}, {"main~1", "."}, {"--help", "."}, {"refs/heads/main", "."}, {"main", "a/../b"}} {
		if _, err := r.Read(context.Background(), "/repo", tt.branch, tt.path); err == nil {
			t.Fatalf("accepted %+v", tt)
		}
	}
}
