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
		if args[0] == "ls-remote" {
			return []byte(head + "\trefs/heads/topic\n"), nil
		}
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

func TestDeleteAlreadyMissingRemoteBranchPrunesOnlyItsTrackingRef(t *testing.T) {
	dir := actionRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	testGit(t, dir, "init", "--bare", remote)
	testGit(t, dir, "remote", "add", "origin", remote)
	head := tip(t, dir, "HEAD")
	testGit(t, dir, "branch", "topic")
	testGit(t, dir, "update-ref", "refs/remotes/origin/topic", head)
	c := Client{run: func(ctx context.Context, dir, input string, args ...string) ([]byte, error) {
		if args[0] == "push" {
			t.Fatal("missing remote branch must not be pushed")
		}
		return runGit(ctx, dir, input, args...)
	}}
	r := BranchRequest{Action: "delete-branch", Source: "refs/remotes/origin/topic", SourceSHA: head}
	if err := c.BranchAction(context.Background(), dir, r); err != nil {
		t.Fatal(err)
	}
	if _, err := c.text(context.Background(), dir, "show-ref", "--verify", r.Source); err == nil {
		t.Fatal("stale tracking ref remains")
	}
	if tip(t, dir, "refs/heads/topic") != head {
		t.Fatal("local branch changed")
	}
	// A symbolic tracking ref must not turn cache pruning into local deletion.
	testGit(t, dir, "symbolic-ref", r.Source, "refs/heads/topic")
	if err := c.BranchAction(context.Background(), dir, r); err != nil {
		t.Fatal(err)
	}
	if tip(t, dir, "refs/heads/topic") != head {
		t.Fatal("symbolic tracking ref deleted its local target")
	}
	// Fetching also removes stale remote-tracking refs without deleting locals.
	testGit(t, dir, "update-ref", r.Source, head)
	if err := c.BranchAction(context.Background(), dir, BranchRequest{Action: "fetch", Source: "origin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.text(context.Background(), dir, "show-ref", "--verify", r.Source); err == nil {
		t.Fatal("fetch retained stale tracking ref")
	}
	if tip(t, dir, "refs/heads/topic") != head {
		t.Fatal("fetch changed local branch")
	}
}

func TestRemoteDeletionPreservesUnknownOrChangedRefs(t *testing.T) {
	for _, mode := range []string{"unreachable", "remote moved", "concurrent fetch", "malformed reply"} {
		t.Run(mode, func(t *testing.T) {
			dir := actionRepo(t)
			head := tip(t, dir, "HEAD")
			testGit(t, dir, "commit", "--allow-empty", "-m", "new tip")
			newHead := tip(t, dir, "HEAD")
			testGit(t, dir, "remote", "add", "origin", "https://example.invalid/repo.git")
			ref := "refs/remotes/origin/topic"
			testGit(t, dir, "update-ref", ref, head)
			want := head
			c := Client{run: func(ctx context.Context, dir, input string, args ...string) ([]byte, error) {
				if args[0] == "push" {
					t.Fatal("unsafe deletion attempted")
				}
				if args[0] == "ls-remote" {
					switch mode {
					case "unreachable":
						return nil, errors.New("remote unavailable")
					case "remote moved":
						return []byte(newHead + "\trefs/heads/topic\n"), nil
					case "malformed reply":
						return []byte("unexpected reply"), nil
					case "concurrent fetch":
						testGit(t, dir, "update-ref", ref, newHead)
						want = newHead
						return nil, nil
					}
				}
				return runGit(ctx, dir, input, args...)
			}}
			if err := c.BranchAction(context.Background(), dir, BranchRequest{Action: "delete-branch", Source: ref, SourceSHA: head}); err == nil {
				t.Fatal("unknown or changed branch accepted")
			}
			if got := tip(t, dir, ref); got != want {
				t.Fatalf("tracking ref = %s, want %s", got, want)
			}
		})
	}
}
