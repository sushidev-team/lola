package projectgit

import (
	"context"
	"strings"
	"testing"
)

func linesOf(patch string, wants ...string) []int {
	var out []int
	for i, row := range strings.Split(strings.TrimSuffix(patch, "\n"), "\n") {
		for _, w := range wants {
			if row == w {
				out = append(out, i)
			}
		}
	}
	return out
}

func TestChangeLinesStagesOnlyTheChosenLines(t *testing.T) {
	dir, _ := stashRepo(t)
	ctx, c := context.Background(), Client{}
	write(t, dir, "doc", "a\nb\nc\nd\ne\n")
	testGit(t, dir, "add", "doc")
	testGit(t, dir, "commit", "-m", "doc")
	write(t, dir, "doc", "a\nB\nc\nD\ne\nf\n")

	d, err := c.Diff(ctx, dir, "", "", "unstaged", "doc")
	if err != nil || len(d.Files) != 1 {
		t.Fatalf("diff %v %v", d, err)
	}
	patch := d.Files[0].Patch
	// Stage "b -> B" only.
	if err := c.ChangeLines(ctx, dir, "unstaged", "doc", patch, linesOf(patch, "-b", "+B"), true); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, dir, "show", ":doc"); got != "a\nB\nc\nd\ne" {
		t.Fatalf("index=%q", got)
	}
	if got := testGit(t, dir, "diff", "--name-only"); got != "doc" {
		t.Fatalf("worktree change lost: %q", got)
	}
	// The same, now stale, patch is refused.
	if err := c.ChangeLines(ctx, dir, "unstaged", "doc", patch, linesOf(patch, "-d"), true); err == nil {
		t.Fatal("stale patch accepted")
	}
	// Unstage just that change again.
	staged, _ := c.Diff(ctx, dir, "", "", "staged", "doc")
	sp := staged.Files[0].Patch
	if err := c.ChangeLines(ctx, dir, "staged", "doc", sp, linesOf(sp, "-b", "+B"), false); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, dir, "show", ":doc"); got != "a\nb\nc\nd\ne" {
		t.Fatalf("index after unstage=%q", got)
	}
	if b := testGit(t, dir, "show", ":doc"); strings.Contains(b, "B") {
		t.Fatal("B still staged")
	}
	if got := testGit(t, dir, "diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("cached=%q", got)
	}
	// Staging a context row or a bad index is refused.
	if err := c.ChangeLines(ctx, dir, "unstaged", "doc", patch, []int{0}, true); err == nil {
		t.Fatal("hunk header accepted")
	}
}

func TestChangeLinesKeepsUnchosenChangesOutOfTheIndex(t *testing.T) {
	dir, _ := stashRepo(t)
	ctx, c := context.Background(), Client{}
	write(t, dir, "doc", "1\n2\n3\n")
	testGit(t, dir, "add", "doc")
	testGit(t, dir, "commit", "-m", "doc")
	write(t, dir, "doc", "1\nx\ny\n3\n")
	d, _ := c.Diff(ctx, dir, "", "", "unstaged", "doc")
	patch := d.Files[0].Patch
	if err := c.ChangeLines(ctx, dir, "unstaged", "doc", patch, linesOf(patch, "+x"), true); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, dir, "show", ":doc"); got != "1\n2\nx\n3" {
		t.Fatalf("index=%q", got)
	}
}
