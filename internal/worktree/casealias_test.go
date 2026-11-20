package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveWorktreeWithMixedCaseFixtureDirectories(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	git("config", "core.ignoreCase", "false")
	blob := git("hash-object", "-w", "--stdin")
	// Build the repo's two tracked spellings even on a case-insensitive host.
	for _, path := range []string{"Fixtures/invoices/one", "fixtures/templates/two"} {
		git("update-index", "--add", "--cacheinfo", "100644,"+blob+","+path)
	}
	git("-c", "user.name=Test", "-c", "user.email=test@example.org", "-c", "commit.gpgSign=false", "commit", "-m", "mixed-case fixture directories")
	ctx := context.Background()
	manager := &Manager{Root: t.TempDir()}
	p := testProject(repo)
	const branch = "lola/advisories"
	dir, err := manager.Create(ctx, p, "session", branch)
	if err != nil {
		t.Fatal(err)
	}
	a, errA := os.Stat(filepath.Join(dir, "Fixtures"))
	b, errB := os.Stat(filepath.Join(dir, "fixtures"))
	if errA != nil || errB != nil {
		t.Fatalf("fixture directories: %v, %v", errA, errB)
	}
	if os.SameFile(a, b) {
		raw, _, err := manager.git(ctx, "-C", dir, "status", "--porcelain")
		if err != nil || !strings.HasPrefix(raw, "?? ") {
			t.Fatalf("expected to reproduce false untracked files: %q, %v", raw, err)
		}
	}

	file := filepath.Join(dir, "fixtures/templates/two")
	for _, path := range []string{file, filepath.Join(dir, "fixtures/templates/notes")} {
		if err := os.WriteFile(path, []byte("local edit"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := manager.Remove(ctx, p, dir, branch, false); !errors.Is(err, ErrDirty) {
			t.Fatalf("cleanup with local edit %s: %v", path, err)
		}
		if path == file {
			err = os.WriteFile(path, nil, 0o600)
		} else {
			err = os.Remove(path)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.Remove(ctx, p, dir, branch, false); err != nil {
		t.Fatalf("clean checkout: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("worktree survived cleanup: %v", err)
	}
	if exists, err := manager.BranchExists(ctx, p, branch); err != nil || exists {
		t.Fatalf("branch exists after cleanup: %v, %v", exists, err)
	}
}

func TestUntrackedPathsRequireAnUnambiguousTrackedDirectoryAlias(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Fixtures", "fixtures"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	upper, err := os.Stat(filepath.Join(dir, "Fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	lower, err := os.Stat(filepath.Join(dir, "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	const status = "?? Fixtures/file\x00"
	const tracked = "H Fixtures/other\x00H fixtures/file\x00"
	if got, want := onlyTrackedAliases(dir, status, tracked), os.SameFile(upper, lower); got != want {
		t.Fatalf("directory alias: got %v, want %v", got, want)
	}
	for _, index := range []string{
		"H fixtures/file\x00",                     // directory spelling changed locally
		"H Fixtures/other\x00h fixtures/file\x00", // assume-unchanged
		"H Fixtures/other\x00S fixtures/file\x00", // skip-worktree
		tracked + "H FIXTURES/file\x00",           // two candidate index entries
	} {
		if onlyTrackedAliases(dir, status, index) {
			t.Fatalf("unexpected alias for index %q", index)
		}
	}
}
