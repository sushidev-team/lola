package projectgit

import (
	"context"
	"errors"
	"fmt"
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
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%v", force), func(t *testing.T) {
			testRemoveWorktreeRefusesUnsafeOrChangedTargets(t, force)
		})
	}
}

func testRemoveWorktreeRefusesUnsafeOrChangedTargets(t *testing.T, force bool) {
	dir := actionRepo(t)
	path := filepath.Join(t.TempDir(), "linked")
	testGit(t, dir, "worktree", "add", "-b", "topic", path)
	ctx, c := context.Background(), (Client{})
	r := BranchRequest{Action: "remove-worktree-branch", Target: path, Source: "refs/heads/topic", SourceSHA: tip(t, path, "HEAD"), ForceDeleteBranch: force, ForceRemoveWorktree: force}
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
	r.ForceRemoveWorktree = false
	write(t, path, "untracked", "keep this\n")
	refuse(r)
	testGit(t, path, "add", "untracked")
	testGit(t, path, "commit", "-m", "unmerged work")
	r.SourceSHA = tip(t, path, "HEAD")
	r.ForceDeleteBranch = false
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

func TestForceRemoveDirtyWorktreeKeepsOrDeletesBranch(t *testing.T) {
	for _, action := range []string{"remove-worktree", "remove-worktree-branch"} {
		t.Run(action, func(t *testing.T) {
			dir := actionRepo(t)
			path := filepath.Join(t.TempDir(), "linked")
			testGit(t, dir, "worktree", "add", "-b", "topic", path)
			testGit(t, path, "commit", "--allow-empty", "-m", "unmerged")
			write(t, path, "base", "dirty tracked content")
			write(t, path, "untracked", "untracked content")
			r := BranchRequest{Action: action, Target: path, Source: "refs/heads/topic", SourceSHA: tip(t, path, "HEAD"), ForceRemoveWorktree: true, ForceDeleteBranch: true}
			c := Client{}
			if err := c.RemoveWorktree(context.Background(), dir, r); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("dirty checkout remains: %v", err)
			}
			_, err := c.text(context.Background(), dir, "show-ref", "--verify", r.Source)
			if (err == nil) != (action == "remove-worktree") {
				t.Fatalf("branch preservation: %v", err)
			}
		})
	}
}

func TestRemoveWorktreeHandlesPartialGitRemoval(t *testing.T) {
	for _, leftover := range []string{"", ".DS_Store", "keep.txt"} {
		t.Run(leftover, func(t *testing.T) {
			dir := actionRepo(t)
			path := filepath.Join(t.TempDir(), "linked")
			testGit(t, dir, "worktree", "add", "-b", "topic", path)
			r := BranchRequest{Action: "remove-worktree-branch", Target: path, Source: "refs/heads/topic", SourceSHA: tip(t, path, "HEAD")}
			c := Client{run: func(ctx context.Context, dir, input string, args ...string) ([]byte, error) {
				out, err := runGit(ctx, dir, input, args...)
				if err == nil && len(args) > 1 && args[0] == "worktree" && args[1] == "remove" {
					if leftover != "" {
						if err := os.Mkdir(path, 0700); err != nil {
							t.Fatal(err)
						}
						write(t, path, leftover, "keep")
					}
					return nil, errors.New("failed to delete worktree: Directory not empty")
				}
				return out, err
			}}
			err := c.RemoveWorktree(context.Background(), dir, r)
			if leftover == "keep.txt" {
				if err == nil || !strings.Contains(err.Error(), "branch were kept") {
					t.Fatalf("partial error = %v", err)
				}
				if got, err := os.ReadFile(filepath.Join(path, leftover)); err != nil || string(got) != "keep" {
					t.Fatal("lost remaining file")
				}
				if tip(t, dir, r.Source) != r.SourceSHA {
					t.Fatal("lost remaining branch")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("metadata directory remains: %v", err)
			}
			if _, err := c.text(context.Background(), dir, "show-ref", "--verify", r.Source); err == nil {
				t.Fatal("branch remains after partial Git success")
			}
		})
	}
}

func TestPartialRemovalDoesNotCleanRegisteredWorktree(t *testing.T) {
	dir := actionRepo(t)
	path := filepath.Join(t.TempDir(), "linked")
	testGit(t, dir, "worktree", "add", "-b", "topic", path)
	write(t, path, ".DS_Store", "finder")
	if err := (Client{}).finishRemovedWorktree(context.Background(), dir, path); err == nil {
		t.Fatal("cleaned a registered checkout")
	}
	if _, err := os.Stat(filepath.Join(path, ".DS_Store")); err != nil {
		t.Fatal("removed metadata from registered checkout")
	}
}

func TestForceDeleteRemainingBranch(t *testing.T) {
	dir := actionRepo(t)
	testGit(t, dir, "switch", "-c", "topic")
	testGit(t, dir, "commit", "--allow-empty", "-m", "unmerged")
	r := BranchRequest{Action: "delete-branch", Source: "refs/heads/topic", SourceSHA: tip(t, dir, "HEAD"), ForceDeleteBranch: true}
	c, ctx := Client{}, context.Background()
	if err := c.BranchAction(ctx, dir, r); err == nil {
		t.Fatal("force deleted checked-out branch")
	}
	testGit(t, dir, "switch", "main")
	stale := r
	stale.SourceSHA = tip(t, dir, "HEAD")
	if err := c.BranchAction(ctx, dir, stale); err == nil {
		t.Fatal("force deleted changed branch")
	}
	r.ForceDeleteBranch = false
	if err := c.BranchAction(ctx, dir, r); err == nil {
		t.Fatal("deleted unmerged branch without force")
	}
	r.ForceDeleteBranch = true
	if err := c.BranchAction(ctx, dir, r); err != nil {
		t.Fatal(err)
	}
	if _, err := c.text(ctx, dir, "show-ref", "--verify", r.Source); err == nil {
		t.Fatal("branch remains")
	}
}

func TestRemoveWorktreeForceDeletesOnlyConfirmedUnmergedBranch(t *testing.T) {
	dir := actionRepo(t)
	path := filepath.Join(t.TempDir(), "linked")
	testGit(t, dir, "worktree", "add", "-b", "topic", path)
	testGit(t, path, "commit", "--allow-empty", "-m", "unmerged")
	r := BranchRequest{Action: "remove-worktree-branch", Target: path, Source: "refs/heads/topic", SourceSHA: tip(t, path, "HEAD")}
	c, ctx := Client{}, context.Background()
	if err := c.RemoveWorktree(ctx, dir, r); err == nil || !strings.Contains(err.Error(), "not fully merged") {
		t.Fatalf("ordinary removal = %v", err)
	}
	r.ForceDeleteBranch = true
	if err := c.RemoveWorktree(ctx, dir, r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("worktree remains: %v", err)
	}
	if _, err := c.text(ctx, dir, "show-ref", "--verify", r.Source); err == nil {
		t.Fatal("unmerged branch remains")
	}
}

func TestForceRemovalKeepsBranchThatMovesAfterWorktreeRemoval(t *testing.T) {
	dir := actionRepo(t)
	path := filepath.Join(t.TempDir(), "linked")
	testGit(t, dir, "worktree", "add", "-b", "topic", path)
	r := BranchRequest{Action: "remove-worktree-branch", Target: path, Source: "refs/heads/topic", SourceSHA: tip(t, path, "HEAD"), ForceDeleteBranch: true}
	testGit(t, dir, "commit", "--allow-empty", "-m", "new tip")
	newTip := tip(t, dir, "HEAD")
	c := Client{run: func(ctx context.Context, dir, input string, args ...string) ([]byte, error) {
		out, err := runGit(ctx, dir, input, args...)
		if err == nil && len(args) > 1 && args[0] == "worktree" && args[1] == "remove" {
			testGit(t, dir, "update-ref", r.Source, newTip)
		}
		return out, err
	}}
	if err := c.RemoveWorktree(context.Background(), dir, r); err == nil || !strings.Contains(err.Error(), "branch was kept") {
		t.Fatalf("moved branch removal = %v", err)
	}
	if tip(t, dir, r.Source) != newTip {
		t.Fatal("lost changed branch")
	}
}
