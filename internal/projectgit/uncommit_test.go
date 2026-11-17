package projectgit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUncommitReturnsTheCommitToStagedChanges(t *testing.T) {
	dir, base := stashRepo(t)
	ctx, c := context.Background(), Client{}
	write(t, dir, "tracked", "committed\n")
	write(t, dir, "added", "new file\n")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "commit", "-m", "should not have happened")
	head := testGit(t, dir, "rev-parse", "HEAD")
	// Work in progress on top of the commit must survive untouched.
	write(t, dir, "tracked", "working\n")
	write(t, dir, "loose", "untracked\n")

	if err := c.Uncommit(ctx, dir, base); err == nil {
		t.Fatal("stale HEAD accepted")
	}
	if err := c.Uncommit(ctx, dir, "HEAD"); err == nil {
		t.Fatal("symbolic revision accepted")
	}
	if err := c.Uncommit(ctx, dir, head); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, dir, "rev-parse", "HEAD"); got != base {
		t.Fatalf("HEAD=%s, want %s", got, base)
	}
	if got := testGit(t, dir, "rev-parse", "ORIG_HEAD"); got != head {
		t.Fatalf("ORIG_HEAD=%s, want the undone commit %s", got, head)
	}
	if got := testGit(t, dir, "show", ":tracked"); got != "committed" {
		t.Fatalf("index=%q", got)
	}
	if got := testGit(t, dir, "diff", "--cached", "--name-only"); got != "added\ntracked" {
		t.Fatalf("staged=%q", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "tracked")); string(b) != "working\n" {
		t.Fatalf("working=%q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "loose")); string(b) != "untracked\n" {
		t.Fatalf("untracked=%q", b)
	}
}

func TestUncommitRefusesWhatItCannotUndoSafely(t *testing.T) {
	ctx, c := context.Background(), Client{}
	t.Run("first commit", func(t *testing.T) {
		dir, head := stashRepo(t)
		if err := c.Uncommit(ctx, dir, head); err == nil || !strings.Contains(err.Error(), "first commit") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("commit a remote has", func(t *testing.T) {
		dir, _ := stashRepo(t)
		write(t, dir, "tracked", "pushed\n")
		testGit(t, dir, "commit", "-am", "pushed")
		head := testGit(t, dir, "rev-parse", "HEAD")
		testGit(t, dir, "update-ref", "refs/remotes/origin/elsewhere", head)
		if err := c.Uncommit(ctx, dir, head); err == nil || !strings.Contains(err.Error(), "already on a remote") {
			t.Fatalf("err=%v", err)
		}
		if got := testGit(t, dir, "rev-parse", "HEAD"); got != head {
			t.Fatal("pushed commit was removed")
		}
	})
	t.Run("merge commit", func(t *testing.T) {
		dir, _ := stashRepo(t)
		branch := testGit(t, dir, "symbolic-ref", "--short", "HEAD")
		testGit(t, dir, "switch", "-c", "side")
		write(t, dir, "side", "side\n")
		testGit(t, dir, "add", ".")
		testGit(t, dir, "commit", "-m", "side")
		testGit(t, dir, "switch", branch)
		testGit(t, dir, "merge", "--no-ff", "-m", "merge side", "side")
		head := testGit(t, dir, "rev-parse", "HEAD")
		if err := c.Uncommit(ctx, dir, head); err == nil || !strings.Contains(err.Error(), "merge commit") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("operation in progress", func(t *testing.T) {
		dir, _ := stashRepo(t)
		write(t, dir, "tracked", "second\n")
		testGit(t, dir, "commit", "-am", "second")
		head := testGit(t, dir, "rev-parse", "HEAD")
		gitDir := testGit(t, dir, "rev-parse", "--absolute-git-dir")
		if err := os.WriteFile(filepath.Join(gitDir, "MERGE_HEAD"), []byte(head+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := c.Uncommit(ctx, dir, head); err == nil || !strings.Contains(err.Error(), "merge in progress") {
			t.Fatalf("err=%v", err)
		}
	})
}
