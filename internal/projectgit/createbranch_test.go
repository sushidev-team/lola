package projectgit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateBranchPinsSourceWithoutChangingCheckout(t *testing.T) {
	dir := actionRepo(t)
	c := Client{}
	ctx := context.Background()
	source := tip(t, dir, "HEAD")
	write(t, dir, "next", "next\n")
	testGit(t, dir, "add", "next")
	testGit(t, dir, "commit", "-m", "advance main")
	current := tip(t, dir, "HEAD")
	write(t, dir, "base", "working edits\n")
	if err := c.CreateBranch(ctx, dir, "feature/from-history", source, false); err != nil {
		t.Fatal(err)
	}
	if got := tip(t, dir, "feature/from-history"); got != source {
		t.Fatalf("branch points at %s, expected %s", got, source)
	}
	if tip(t, dir, "HEAD") != current || testGit(t, dir, "branch", "--show-current") != "main" {
		t.Fatal("creation changed checkout")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "base")); string(b) != "working edits\n" {
		t.Fatal("creation changed working files")
	}
	if err := c.CreateBranch(ctx, dir, "feature/from-history", current, false); err == nil {
		t.Fatal("overwrote existing branch")
	}
	if tip(t, dir, "feature/from-history") != source {
		t.Fatal("existing branch moved")
	}
}
func TestCreateAndCheckoutBranchKeepsSafeWorkingChanges(t *testing.T) {
	dir := actionRepo(t)
	c := Client{}
	source := tip(t, dir, "HEAD")
	write(t, dir, "base", "working edits\n")
	write(t, dir, "untracked", "untracked edits\n")
	if err := c.CreateBranch(context.Background(), dir, "feature/new", source, true); err != nil {
		t.Fatal(err)
	}
	if testGit(t, dir, "branch", "--show-current") != "feature/new" || tip(t, dir, "HEAD") != source {
		t.Fatal("new branch not checked out")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "base")); string(b) != "working edits\n" {
		t.Fatal("working changes lost")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "untracked")); string(b) != "untracked edits\n" {
		t.Fatal("untracked changes lost")
	}
	if got := testGit(t, dir, "stash", "list"); got != "" {
		t.Fatal("unexpected automatic stash")
	}
	if err := c.CreateBranch(context.Background(), dir, "main", source, true); err == nil {
		t.Fatal("checked out existing branch via create")
	}
	if testGit(t, dir, "branch", "--show-current") != "feature/new" {
		t.Fatal("existing-name failure switched branches")
	}
}
func TestCreateCheckoutConflictDoesNotLeaveBranch(t *testing.T) {
	dir := actionRepo(t)
	c := Client{}
	older := tip(t, dir, "HEAD")
	write(t, dir, "base", "committed change\n")
	testGit(t, dir, "add", "base")
	testGit(t, dir, "commit", "-m", "change base")
	head := tip(t, dir, "HEAD")
	write(t, dir, "base", "my newer working edits\n")
	if err := c.CreateBranch(context.Background(), dir, "feature/would-clobber", older, true); err == nil {
		t.Fatal("accepted conflicting checkout")
	}
	refs := testGit(t, dir, "for-each-ref", "--format=%(refname)", "refs/heads/feature/would-clobber")
	if refs != "" {
		t.Fatalf("left branch behind: %s", refs)
	}
	if tip(t, dir, "HEAD") != head || testGit(t, dir, "branch", "--show-current") != "main" {
		t.Fatal("failed creation moved HEAD")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "base")); string(b) != "my newer working edits\n" {
		t.Fatal("failed creation changed working files")
	}
	if testGit(t, dir, "stash", "list") != "" {
		t.Fatal("failed creation stashed changes")
	}
}
func TestCreateBranchRejectsInvalidNamesAndCommits(t *testing.T) {
	dir := actionRepo(t)
	head := tip(t, dir, "HEAD")
	c := Client{}
	for _, name := range []string{"", "--force", "-", "HEAD", "refs/heads/nested", "@{-1}", " space", "with space", "../escape", "feature..bad", "name.lock", "bad\x00name"} {
		if err := c.CreateBranch(context.Background(), dir, name, head, false); err == nil {
			t.Fatalf("accepted name %q", name)
		}
	}
	for _, sha := range []string{"main", "--all", strings.Repeat("f", 40)} {
		if err := c.CreateBranch(context.Background(), dir, "valid-name", sha, true); err == nil {
			t.Fatalf("accepted commit %q", sha)
		}
	}
	if got := testGit(t, dir, "for-each-ref", "--format=%(refname)", "refs/heads/"); got != "refs/heads/main" {
		t.Fatalf("unexpected branches=%s", got)
	}
}
func TestCreateCheckoutRefusesOperationInProgress(t *testing.T) {
	dir := actionRepo(t)
	head := tip(t, dir, "HEAD")
	if err := os.WriteFile(filepath.Join(dir, ".git", "MERGE_HEAD"), []byte(head+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := (Client{}).CreateBranch(context.Background(), dir, "feature/during-merge", head, true); err == nil {
		t.Fatal("switched during merge")
	}
	if got := testGit(t, dir, "for-each-ref", "--format=%(refname)", "refs/heads/feature/during-merge"); got != "" {
		t.Fatal("created branch despite active operation")
	}
}
