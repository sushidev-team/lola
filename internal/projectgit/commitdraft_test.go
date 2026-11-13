package projectgit

import (
	"context"
	"strings"
	"testing"
)

func TestCommitDraftUsesOnlyStagedContents(t *testing.T) {
	dir := repo(t)
	ctx := context.Background()
	c := Client{}
	write(t, dir, "a.ts", "base\n")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "commit", "-m", "feat: establish project")
	write(t, dir, "a.ts", "STAGED_BEHAVIOR\n")
	testGit(t, dir, "add", "a.ts")
	write(t, dir, "a.ts", "UNSTAGED_PRIVATE_TEXT\n")
	write(t, dir, "untracked.txt", "UNTRACKED_PRIVATE_TEXT\n")
	index := testGit(t, dir, "diff", "--cached", "--binary")
	working := testGit(t, dir, "diff", "--binary")
	input, err := c.PrepareCommitDraft(ctx, dir, "feat:")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(input.Text, "STAGED_BEHAVIOR") || !strings.Contains(input.Text, "feat: establish project") {
		t.Fatal(input.Text)
	}
	if strings.Contains(input.Text, "UNSTAGED_PRIVATE_TEXT") || strings.Contains(input.Text, "UNTRACKED_PRIVATE_TEXT") || strings.Contains(input.Text, "untracked.txt") {
		t.Fatal("included unstaged content")
	}
	if len(input.Text) > 12000 {
		t.Fatal("input exceeds helper cap")
	}
	if index != testGit(t, dir, "diff", "--cached", "--binary") || working != testGit(t, dir, "diff", "--binary") {
		t.Fatal("draft preparation mutated repository")
	}
	testGit(t, dir, "add", "a.ts")
	fingerprint, err := c.IndexFingerprint(ctx, dir)
	if err != nil || fingerprint == input.Fingerprint {
		t.Fatal("missed changed index")
	}
}
func TestCommitDraftUnbornAndEmptyIndex(t *testing.T) {
	dir := repo(t)
	c := Client{}
	ctx := context.Background()
	if _, err := c.PrepareCommitDraft(ctx, dir, ""); err == nil {
		t.Fatal("empty index accepted")
	}
	write(t, dir, "first", "first change\n")
	testGit(t, dir, "add", "first")
	if input, err := c.PrepareCommitDraft(ctx, dir, ""); err != nil || !strings.Contains(input.Text, "first change") {
		t.Fatalf("input=%+v err=%v", input, err)
	}
}
func TestParseCommitDraft(t *testing.T) {
	got, err := ParseCommitDraft("```json\n{\"subject\":\"feat(git): draft staged commit messages\",\"body\":\"Use the index to describe the next commit.\"}\n```")
	if err != nil || got != "feat(git): draft staged commit messages\n\nUse the index to describe the next commit." {
		t.Fatalf("%q %v", got, err)
	}
	for _, bad := range []string{"not json", `{"subject":""}`, `{"subject":"oops\nsecond line"}`, `{"subject":"hello","body":"bad\u0000text"}`} {
		if _, err := ParseCommitDraft(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
