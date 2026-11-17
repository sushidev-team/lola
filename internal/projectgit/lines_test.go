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

// Skipping an earlier hunk must not leave later hunks at positions that only
// made sense with it applied: repeated blocks would match the wrong place.
func TestChangeLinesRenumbersAfterSkippedHunks(t *testing.T) {
	block := strings.Repeat("x\ny\nz\nw\nv\nu\n", 1)
	var base strings.Builder
	for i := 0; i < 8; i++ {
		base.WriteString(block)
	}
	for _, stage := range []bool{true, false} {
		dir, _ := stashRepo(t)
		ctx, c := context.Background(), Client{}
		write(t, dir, "rep", base.String())
		testGit(t, dir, "add", "rep")
		testGit(t, dir, "commit", "-m", "rep")
		lines := strings.Split(strings.TrimSuffix(base.String(), "\n"), "\n")
		changed := append([]string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}, lines...)
		changed[10+16] = "CHANGED"
		write(t, dir, "rep", strings.Join(changed, "\n")+"\n")
		if !stage {
			testGit(t, dir, "add", "rep")
		}
		area := "unstaged"
		if !stage {
			area = "staged"
		}
		d, err := c.Diff(ctx, dir, "", "", area, "rep")
		if err != nil || len(d.Files) != 1 {
			t.Fatalf("diff %v %v", d, err)
		}
		patch := d.Files[0].Patch
		if err := c.ChangeLines(ctx, dir, area, "rep", patch, linesOf(patch, "+CHANGED", "-"+lines[16]), stage); err != nil {
			t.Fatal(err)
		}
		got := testGit(t, dir, "diff", "--cached", "-U0", "rep")
		if stage && (!strings.Contains(got, "@@ -17 +17 @@") || !strings.Contains(got, "+CHANGED")) {
			t.Fatalf("staged the wrong place:\n%s", got)
		}
		if !stage && strings.Contains(got, "CHANGED") {
			t.Fatalf("unstage left the change:\n%s", got)
		}
		if !stage && !strings.Contains(got, "+10") {
			t.Fatalf("unstage touched the other hunk:\n%s", got)
		}
	}
}
