package projectgit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveWorktreeKeepsOrDeletesMergedBranch(t *testing.T) {
	for _, action := range []string{"remove-worktree", "remove-worktree-branch"} {
		t.Run(action, func(t *testing.T) {
			dir := actionRepo(t)
			path := filepath.Join(t.TempDir(), "linked")
			testGit(t, dir, "worktree", "add", "-b", "topic", path)
			c := Client{}
			r := BranchRequest{Action: action, Target: path, Source: "refs/heads/topic", SourceSHA: tip(t, path, "HEAD")}
			if err := c.RemoveWorktree(context.Background(), dir, r); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("folder remains: %v", err)
			}
			_, err := c.text(context.Background(), dir, "show-ref", "--verify", r.Source)
			if (err == nil) != (action == "remove-worktree") {
				t.Fatalf("branch preservation for %s: %v", action, err)
			}
		})
	}
}

func TestRemoveWorktreeRefusesUnsafeOrChangedTargets(t *testing.T) {
	dir := actionRepo(t)
	path := filepath.Join(t.TempDir(), "linked")
	testGit(t, dir, "worktree", "add", "-b", "topic", path)
	ctx, c := context.Background(), (Client{})
	r := BranchRequest{Action: "remove-worktree-branch", Target: path, Source: "refs/heads/topic", SourceSHA: tip(t, path, "HEAD")}
	refuse := func(request BranchRequest) {
		t.Helper()
		if err := c.RemoveWorktree(ctx, dir, request); err == nil {
			t.Fatalf("accepted %+v", request)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("removed protected worktree: %v", err)
		}
		if tip(t, dir, r.Source) != tip(t, path, "HEAD") {
			t.Fatal("removed branch")
		}
	}
	bad := r
	bad.Target = dir
	refuse(bad)
	bad.Target = t.TempDir()
	refuse(bad)
	bad = r
	bad.SourceSHA = strings.Repeat("f", 40)
	refuse(bad)
	bad = r
	bad.Source = "refs/heads/renamed"
	refuse(bad)
	testGit(t, dir, "worktree", "lock", path)
	refuse(r)
	testGit(t, dir, "worktree", "unlock", path)
	write(t, path, "untracked", "keep this\n")
	refuse(r)
	testGit(t, path, "add", "untracked")
	testGit(t, path, "commit", "-m", "unmerged work")
	r.SourceSHA = tip(t, path, "HEAD")
	refuse(r)
	// Keeping the branch allows removing an otherwise clean, unmerged checkout.
	r.Action = "remove-worktree"
	if err := c.RemoveWorktree(ctx, dir, r); err != nil {
		t.Fatal(err)
	}
	if got := tip(t, dir, r.Source); got != r.SourceSHA {
		t.Fatal("lost unmerged branch")
	}
}
