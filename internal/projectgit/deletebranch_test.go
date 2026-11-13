package projectgit

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDeleteBranchProtectsCheckedOutUnmergedAndStaleBranches(t *testing.T) {
	dir := actionRepo(t)
	ctx := context.Background()
	c := Client{}
	head := tip(t, dir, "HEAD")
	testGit(t, dir, "branch", "merged")
	r := BranchRequest{Action: "delete-branch", Source: "refs/heads/merged", SourceSHA: strings.Repeat("f", 40)}
	if err := c.BranchAction(ctx, dir, r); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale branch: %v", err)
	}
	if got := tip(t, dir, r.Source); got != head {
		t.Fatal("stale request changed branch")
	}
	r.SourceSHA = head
	if err := c.BranchAction(ctx, dir, r); err != nil {
		t.Fatal(err)
	}
	if _, err := c.text(ctx, dir, "show-ref", "--verify", r.Source); err == nil {
		t.Fatal("merged branch still exists")
	}
	for _, name := range []string{"main", "linked"} {
		if name == "linked" {
			testGit(t, dir, "worktree", "add", "-b", name, filepath.Join(t.TempDir(), "linked"))
		}
		r.Source = "refs/heads/" + name
		if err := c.BranchAction(ctx, dir, r); err == nil || !strings.Contains(err.Error(), "checked out") {
			t.Fatalf("%s: %v", name, err)
		}
		if got := tip(t, dir, r.Source); got != head {
			t.Fatalf("checked-out %s changed", name)
		}
	}
	testGit(t, dir, "switch", "-c", "unmerged")
	write(t, dir, "feature", "unique work\n")
	testGit(t, dir, "add", "feature")
	testGit(t, dir, "commit", "-m", "unmerged work")
	r.Source, r.SourceSHA = "refs/heads/unmerged", tip(t, dir, "HEAD")
	testGit(t, dir, "switch", "main")
	if err := c.BranchAction(ctx, dir, r); err == nil {
		t.Fatal("deleted unmerged branch")
	}
	if got := tip(t, dir, r.Source); got != r.SourceSHA {
		t.Fatal("unmerged work lost")
	}
}

func TestDeleteRemoteBranchPinsDeletionAndPropagatesRejection(t *testing.T) {
	dir := actionRepo(t)
	head := tip(t, dir, "HEAD")
	testGit(t, dir, "remote", "add", "origin", "https://example.invalid/repo.git")
	testGit(t, dir, "update-ref", "refs/remotes/origin/topic", head)
	rejected := errors.New("remote rejected stale tip")
	var pushed []string
	c := Client{run: func(ctx context.Context, dir, input string, args ...string) ([]byte, error) {
		if args[0] == "push" {
			pushed = args
			return nil, rejected
		}
		return runGit(ctx, dir, input, args...)
	}}
	r := BranchRequest{Action: "delete-branch", Source: "refs/remotes/origin/topic", SourceSHA: head}
	if err := c.BranchAction(context.Background(), dir, r); err == nil || !strings.Contains(err.Error(), rejected.Error()) {
		t.Fatalf("rejection lost: %v", err)
	}
	want := []string{"push", "--force-with-lease=refs/heads/topic:" + head, "--", "origin", ":refs/heads/topic"}
	if !reflect.DeepEqual(pushed, want) {
		t.Fatalf("push = %q, want %q", pushed, want)
	}
	if got := tip(t, dir, r.Source); got != head {
		t.Fatal("rejected deletion removed tracking ref")
	}
	pushed = nil
	testGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", r.Source)
	r.Source = "refs/remotes/origin/HEAD"
	if err := c.BranchAction(context.Background(), dir, r); err == nil || len(pushed) != 0 {
		t.Fatalf("remote HEAD accepted: %v, %q", err, pushed)
	}
}
