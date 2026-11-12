package projectgit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func actionRepo(t *testing.T) string {
	dir := repo(t)
	write(t, dir, "base", "base\n")
	testGit(t, dir, "add", "base")
	testGit(t, dir, "commit", "-m", "base")
	return dir
}
func tip(t *testing.T, dir, ref string) string { return testGit(t, dir, "rev-parse", ref) }
func TestBranchCheckoutRejectsStaleAndDirty(t *testing.T) {
	dir := actionRepo(t)
	c := Client{}
	ctx := context.Background()
	head := tip(t, dir, "HEAD")
	testGit(t, dir, "branch", "feature")
	request := BranchRequest{Action: "checkout", Source: "refs/heads/feature", SourceSHA: head}
	write(t, dir, "base", "dirty\n")
	if err := c.BranchAction(ctx, dir, request); err == nil {
		t.Fatal("accepted dirty checkout")
	}
	if got := testGit(t, dir, "branch", "--show-current"); got != "main" {
		t.Fatal(got)
	}
	write(t, dir, "base", "base\n")
	request.SourceSHA = strings.Repeat("f", 40)
	if err := c.BranchAction(ctx, dir, request); err == nil {
		t.Fatal("accepted stale ref")
	}
	request.SourceSHA = head
	if err := c.BranchAction(ctx, dir, request); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, dir, "branch", "--show-current"); got != "feature" {
		t.Fatal(got)
	}
}
func TestRemoteCheckoutTracksNamedBranch(t *testing.T) {
	remote := actionRepo(t)
	testGit(t, remote, "branch", "feature/topic")
	dir := actionRepo(t)
	testGit(t, dir, "remote", "add", "origin", remote)
	testGit(t, dir, "fetch", "origin")
	c := Client{}
	request := BranchRequest{Action: "checkout", Source: "refs/remotes/origin/feature/topic", SourceSHA: tip(t, dir, "origin/feature/topic")}
	if err := c.BranchAction(context.Background(), dir, request); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, dir, "rev-parse", "--abbrev-ref", "@{upstream}"); got != "origin/feature/topic" {
		t.Fatal(got)
	}
	if got := testGit(t, dir, "branch", "--show-current"); got != "feature/topic" {
		t.Fatal(got)
	}
}
func TestPullUncheckedBranchFastForwardsWithoutTouchingCheckout(t *testing.T) {
	remote := actionRepo(t)
	dir := actionRepo(t)
	testGit(t, dir, "remote", "add", "origin", remote)
	testGit(t, dir, "fetch", "origin")
	testGit(t, dir, "branch", "--track", "tracked", "origin/main")
	old := tip(t, dir, "tracked")
	head := tip(t, dir, "HEAD")
	write(t, dir, "base", "my uncommitted edits\n")
	write(t, remote, "next", "next\n")
	testGit(t, remote, "add", "next")
	testGit(t, remote, "commit", "-m", "next")
	c := Client{}
	request := BranchRequest{Action: "pull", Source: "refs/heads/tracked", SourceSHA: old}
	if err := c.BranchAction(context.Background(), dir, request); err != nil {
		t.Fatal(err)
	}
	if got := tip(t, dir, "tracked"); got != tip(t, remote, "HEAD") {
		t.Fatal("unchecked branch not advanced")
	}
	if got := tip(t, dir, "HEAD"); got != head {
		t.Fatal("active checkout moved")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "base")); string(got) != "my uncommitted edits\n" {
		t.Fatal("dirty file touched")
	}
	if _, err := os.Stat(filepath.Join(dir, "next")); !os.IsNotExist(err) {
		t.Fatal("unchecked branch files were written")
	}
	// Diverged branches must not be reset onto upstream.
	testGit(t, dir, "update-ref", "refs/heads/tracked", head)
	request.SourceSHA = head
	// The initial repositories can share identical commits; make local main diverge.
	write(t, dir, "base", "my committed edits\n")
	testGit(t, dir, "add", "base")
	testGit(t, dir, "commit", "-m", "diverge")
	local := tip(t, dir, "HEAD")
	testGit(t, dir, "update-ref", "refs/heads/tracked", local)
	request.SourceSHA = local
	if err := c.BranchAction(context.Background(), dir, request); err == nil || !strings.Contains(err.Error(), "diverged") {
		t.Fatalf("%v", err)
	}
	if got := tip(t, dir, "tracked"); got != local {
		t.Fatal("diverged branch moved")
	}
}
func TestBranchMergeConflictRecoveryAndStaleAbort(t *testing.T) {
	dir := actionRepo(t)
	testGit(t, dir, "switch", "-c", "feature")
	write(t, dir, "base", "feature\n")
	testGit(t, dir, "add", "base")
	testGit(t, dir, "commit", "-m", "feature")
	source := tip(t, dir, "HEAD")
	testGit(t, dir, "switch", "main")
	write(t, dir, "base", "main\n")
	testGit(t, dir, "add", "base")
	testGit(t, dir, "commit", "-m", "main")
	target := tip(t, dir, "HEAD")
	c := Client{}
	ctx := context.Background()
	request := BranchRequest{Action: "merge", Source: "refs/heads/feature", SourceSHA: source, Target: "refs/heads/main", TargetSHA: target}
	if err := c.BranchAction(ctx, dir, request); err == nil {
		t.Fatal("expected merge conflict")
	}
	if op, err := c.Operation(ctx, dir); err != nil || op != "merge" {
		t.Fatalf("%s %v", op, err)
	}
	if err := c.BranchAction(ctx, dir, BranchRequest{Action: "abort", Source: "rebase", SourceSHA: target}); err == nil {
		t.Fatal("aborted unexpected operation")
	}
	if err := c.BranchAction(ctx, dir, BranchRequest{Action: "abort", Source: "merge", SourceSHA: source}); err == nil {
		t.Fatal("aborted stale HEAD")
	}
	if err := c.BranchAction(ctx, dir, BranchRequest{Action: "abort", Source: "merge", SourceSHA: target}); err != nil {
		t.Fatal(err)
	}
	if op, err := c.Operation(ctx, dir); err != nil || op != "" {
		t.Fatalf("%s %v", op, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "base")); string(got) != "main\n" {
		t.Fatal("abort did not restore main")
	}
}
func TestRebaseSourceOntoTargetAndLinkedCheckoutRefusal(t *testing.T) {
	dir := actionRepo(t)
	testGit(t, dir, "switch", "-c", "feature")
	write(t, dir, "feature", "feature\n")
	testGit(t, dir, "add", "feature")
	testGit(t, dir, "commit", "-m", "feature")
	source := tip(t, dir, "HEAD")
	testGit(t, dir, "switch", "main")
	write(t, dir, "main", "main\n")
	testGit(t, dir, "add", "main")
	testGit(t, dir, "commit", "-m", "main")
	target := tip(t, dir, "HEAD")
	c := Client{}
	ctx := context.Background()
	r := BranchRequest{Action: "rebase", Source: "refs/heads/feature", SourceSHA: source, Target: "refs/heads/main", TargetSHA: target}
	if err := c.BranchAction(ctx, dir, r); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, dir, "branch", "--show-current"); got != "feature" {
		t.Fatal(got)
	}
	if got := tip(t, dir, "feature^"); got != target {
		t.Fatal("source not rebased onto target")
	}
	if got := tip(t, dir, "main"); got != target {
		t.Fatal("target branch changed")
	}
	wt := filepath.Join(t.TempDir(), "linked")
	testGit(t, dir, "worktree", "add", wt, "main")
	if err := c.BranchAction(ctx, dir, BranchRequest{Action: "checkout", Source: "refs/heads/main", SourceSHA: target}); err == nil {
		t.Fatal("checked out linked branch twice")
	}
}

func TestMergeContinueUsesResolvedIndexAndPreparedMessage(t *testing.T) {
	dir := actionRepo(t)
	testGit(t, dir, "switch", "-c", "feature")
	write(t, dir, "base", "feature\n")
	testGit(t, dir, "add", "base")
	testGit(t, dir, "commit", "-m", "feature")
	source := tip(t, dir, "HEAD")
	testGit(t, dir, "switch", "main")
	write(t, dir, "base", "main\n")
	testGit(t, dir, "add", "base")
	testGit(t, dir, "commit", "-m", "main")
	target := tip(t, dir, "HEAD")
	c := Client{}
	ctx := context.Background()
	if err := c.BranchAction(ctx, dir, BranchRequest{Action: "merge", Source: "refs/heads/feature", Target: "refs/heads/main", SourceSHA: source, TargetSHA: target}); err == nil {
		t.Fatal("expected conflict")
	}
	write(t, dir, "base", "resolved\n")
	testGit(t, dir, "add", "base")
	if err := c.BranchAction(ctx, dir, BranchRequest{Action: "continue", Source: "merge", SourceSHA: target}); err != nil {
		t.Fatal(err)
	}
	if parents := strings.Fields(testGit(t, dir, "show", "-s", "--format=%P", "HEAD")); len(parents) != 2 || parents[0] != target || parents[1] != source {
		t.Fatalf("parents=%v", parents)
	}
	if op, err := c.Operation(ctx, dir); err != nil || op != "" {
		t.Fatalf("operation=%s err=%v", op, err)
	}
}

func TestCheckoutRefCannotBecomeAnOptionOrPreviousBranch(t *testing.T) {
	dir := actionRepo(t)
	head := tip(t, dir, "HEAD")
	c := Client{}
	for _, name := range []string{"--detach", "-"} {
		ref := "refs/heads/" + name
		testGit(t, dir, "update-ref", ref, head)
		if err := c.BranchAction(context.Background(), dir, BranchRequest{Action: "checkout", Source: ref, SourceSHA: head}); err == nil {
			t.Fatal("accepted option-like branch", name)
		}
		if got := testGit(t, dir, "branch", "--show-current"); got != "main" {
			t.Fatalf("checkout changed to %s", got)
		}
	}
}
