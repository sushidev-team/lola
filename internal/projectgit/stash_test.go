package projectgit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stashRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := repo(t)
	write(t, dir, "tracked", "base\n")
	write(t, dir, ".gitignore", "ignored\n")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "commit", "-m", "initial")
	return dir, testGit(t, dir, "rev-parse", "HEAD")
}

func TestStashPushPopPreservesIndexAndUntrackedButNotIgnored(t *testing.T) {
	dir, head := stashRepo(t)
	ctx, c := context.Background(), Client{}
	write(t, dir, "tracked", "staged\n")
	testGit(t, dir, "add", "tracked")
	write(t, dir, "tracked", "working\n")
	write(t, dir, "new", "untracked\n")
	write(t, dir, ":(glob)*", "literal filename\n")
	write(t, dir, "ignored", "ignored\n")
	if err := c.StashPush(ctx, dir, "before changing branch", head); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, dir, "status", "--porcelain"); got != "" {
		t.Fatalf("checkout not clean: %s; stashed untracked: %s", got, testGit(t, dir, "ls-tree", "--name-only", "stash@{0}^3"))
	}
	if _, err := os.Stat(filepath.Join(dir, "ignored")); err != nil {
		t.Fatal("ignored file was stashed")
	}
	entries, err := c.Stashes(ctx, dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("stashes=%+v err=%v", entries, err)
	}
	entry := entries[0]
	if entry.Selector != "stash@{0}" || !strings.Contains(entry.Subject, "before changing branch") || entry.Date == "" {
		t.Fatalf("stash=%+v", entry)
	}
	if err = c.StashApply(ctx, dir, entry.SHA, head, true); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, dir, "show", ":tracked"); got != "staged" {
		t.Fatalf("index=%q", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "tracked")); string(b) != "working\n" {
		t.Fatalf("working=%q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "new")); string(b) != "untracked\n" {
		t.Fatalf("untracked=%q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ":(glob)*")); string(b) != "literal filename\n" {
		t.Fatalf("literal filename=%q", b)
	}
	entries, err = c.Stashes(ctx, dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("stashes=%+v err=%v", entries, err)
	}
}

func TestStashApplyKeepsStashAndDropUsesIdentity(t *testing.T) {
	dir, head := stashRepo(t)
	ctx, c := context.Background(), Client{}
	write(t, dir, "tracked", "changed\n")
	if err := c.StashPush(ctx, dir, "first", head); err != nil {
		t.Fatal(err)
	}
	entries, _ := c.Stashes(ctx, dir)
	sha := entries[0].SHA
	if err := c.StashApply(ctx, dir, sha, head, false); err != nil {
		t.Fatal(err)
	}
	entries, _ = c.Stashes(ctx, dir)
	if len(entries) != 1 {
		t.Fatal("apply dropped stash")
	}
	if err := c.StashDrop(ctx, dir, strings.Repeat("f", 40)); err == nil {
		t.Fatal("accepted stale stash")
	}
	if err := c.StashDrop(ctx, dir, sha); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "tracked")); string(b) != "changed\n" {
		t.Fatal("drop changed checkout")
	}
}

func TestStashRefusesDirtyApplyStaleHeadAndEmptyPush(t *testing.T) {
	dir, head := stashRepo(t)
	ctx, c := context.Background(), Client{}
	if err := c.StashPush(ctx, dir, "", head); err == nil {
		t.Fatal("stashed a clean checkout")
	}
	write(t, dir, "tracked", "changed\n")
	if err := c.StashPush(ctx, dir, "", strings.Repeat("f", 40)); err == nil {
		t.Fatal("accepted stale HEAD")
	}
	if err := c.StashPush(ctx, dir, "label\nbad", head); err == nil {
		t.Fatal("accepted multiline label")
	}
	if err := c.StashPush(ctx, dir, "", head); err != nil {
		t.Fatal(err)
	}
	entries, _ := c.Stashes(ctx, dir)
	write(t, dir, "untracked", "keep\n")
	if err := c.StashApply(ctx, dir, entries[0].SHA, head, true); err == nil {
		t.Fatal("applied into dirty checkout")
	}
	if len(testGit(t, dir, "stash", "list")) == 0 {
		t.Fatal("removed stash after refusal")
	}
	if err := c.StashApply(ctx, dir, "stash@{0}", head, false); err == nil {
		t.Fatal("accepted moving selector")
	}
}

func TestStashPopConflictKeepsStash(t *testing.T) {
	dir, head := stashRepo(t)
	ctx, c := context.Background(), Client{}
	write(t, dir, "tracked", "stashed edit\n")
	if err := c.StashPush(ctx, dir, "conflict", head); err != nil {
		t.Fatal(err)
	}
	entries, _ := c.Stashes(ctx, dir)
	write(t, dir, "tracked", "new committed edit\n")
	testGit(t, dir, "add", "tracked")
	testGit(t, dir, "commit", "-m", "new head")
	head = testGit(t, dir, "rev-parse", "HEAD")
	if err := c.StashApply(ctx, dir, entries[0].SHA, head, true); err == nil || !strings.Contains(err.Error(), "stash was kept") {
		t.Fatalf("error=%v", err)
	}
	after, _ := c.Stashes(ctx, dir)
	if len(after) != 1 || after[0].SHA != entries[0].SHA {
		t.Fatal("conflicted stash was dropped")
	}
	if err := c.StashPush(ctx, dir, "conflict", head); err == nil {
		t.Fatal("stashed conflicted files")
	}
}

func TestStashPopKeepsEntryWhenListChangesDuringApply(t *testing.T) {
	dir, head := stashRepo(t)
	ctx, c := context.Background(), Client{}
	write(t, dir, "tracked", "changed\n")
	if err := c.StashPush(ctx, dir, "selected", head); err != nil {
		t.Fatal(err)
	}
	entries, _ := c.Stashes(ctx, dir)
	// Simulate another client creating a stash between apply and drop. The
	// selected entry moves; neither it nor the intervening entry may be dropped.
	c.run = func(ctx context.Context, dir, input string, args ...string) ([]byte, error) {
		out, err := runGit(ctx, dir, input, args...)
		if err == nil && len(args) > 1 && args[0] == "stash" && args[1] == "apply" {
			testGit(t, dir, "stash", "push", "-m", "intervening")
		}
		return out, err
	}
	if err := c.StashApply(ctx, dir, entries[0].SHA, head, true); err == nil || !strings.Contains(err.Error(), "stash was kept") {
		t.Fatalf("error=%v", err)
	}
	after, _ := c.Stashes(ctx, dir)
	if len(after) != 2 {
		t.Fatalf("stashes=%+v", after)
	}
}

func TestStashRefusesInProgressOperation(t *testing.T) {
	dir, head := stashRepo(t)
	write(t, dir, "tracked", "changed\n")
	write(t, dir, ".git/MERGE_HEAD", head+"\n")
	if err := (Client{}).StashPush(context.Background(), dir, "", head); err == nil || !strings.Contains(err.Error(), "merge") {
		t.Fatalf("error=%v", err)
	}
	if got := testGit(t, dir, "stash", "list"); got != "" {
		t.Fatal("created stash during merge")
	}
}

func TestStashesFailsClosedOnMalformedOutput(t *testing.T) {
	c := Client{run: func(context.Context, string, string, ...string) ([]byte, error) {
		return []byte("unexpected output"), nil
	}}
	if _, err := c.Stashes(context.Background(), "unused"); err == nil {
		t.Fatal("accepted malformed stash facts")
	}
}
