package projectgit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscardUnstagedPreservesIndexAndIgnoredFiles(t *testing.T) {
	dir := repo(t)
	c := Client{}
	ctx := context.Background()
	write(t, dir, "partial", "original\n")
	write(t, dir, "deleted", "restore me\n")
	write(t, dir, "rename", "rename me\n")
	write(t, dir, ".gitignore", "ignored\n")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "commit", "-m", "base")
	write(t, dir, "partial", "staged\n")
	testGit(t, dir, "add", "partial")
	write(t, dir, "partial", "unstaged\n")
	testGit(t, dir, "mv", "rename", "renamed")
	write(t, dir, "renamed", "later edit\n")
	if err := os.Remove(filepath.Join(dir, "deleted")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "ignored", "keep ignored\n")
	write(t, dir, ":(glob)*\nnew", "remove only this\n")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("keep outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	before := testGit(t, dir, "diff", "--cached", "--binary")
	plan, err := c.PrepareDiscard(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tracked) != 3 || len(plan.Untracked) != 2 {
		t.Fatalf("plan=%+v", plan)
	}
	if err = c.DiscardUnstaged(ctx, dir, plan.Token); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, dir, "diff", "--cached", "--binary"); got != before {
		t.Fatal("staged changes altered")
	}
	for path, want := range map[string]string{"partial": "staged\n", "deleted": "restore me\n", "renamed": "rename me\n", "ignored": "keep ignored\n"} {
		b, e := os.ReadFile(filepath.Join(dir, path))
		if e != nil || string(b) != want {
			t.Fatalf("%s=%q err=%v", path, b, e)
		}
	}
	if b, e := os.ReadFile(outside); e != nil || string(b) != "keep outside" {
		t.Fatal("symlink target changed")
	}
	files, err := c.status(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Untracked || f.Worktree != " " {
			t.Fatalf("unstaged remains: %+v", f)
		}
	}
}

func TestDiscardUnstagedRejectsStaleConfirmation(t *testing.T) {
	for _, change := range []string{"contents", "new file", "index", "branch"} {
		t.Run(change, func(t *testing.T) {
			dir := repo(t)
			c := Client{}
			ctx := context.Background()
			write(t, dir, "file", "base")
			testGit(t, dir, "add", ".")
			testGit(t, dir, "commit", "-m", "base")
			write(t, dir, "file", "first edit")
			plan, err := c.PrepareDiscard(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "branch":
				testGit(t, dir, "switch", "-c", "other")
			case "contents":
				write(t, dir, "file", "second edit")
			case "new file":
				write(t, dir, "new", "keep")
			case "index":
				testGit(t, dir, "add", "file")
				write(t, dir, "file", "second edit")
			}
			before := testGit(t, dir, "diff", "--binary")
			if err = c.DiscardUnstaged(ctx, dir, plan.Token); err == nil || !strings.Contains(err.Error(), "changed") {
				t.Fatalf("error=%v", err)
			}
			if got := testGit(t, dir, "diff", "--binary"); got != before {
				t.Fatal("mutated before rejecting stale confirmation")
			}
		})
	}
}

func TestDiscardUnstagedRefusesDirectoriesAndActiveOperations(t *testing.T) {
	dir := repo(t)
	c := Client{}
	ctx := context.Background()
	write(t, dir, "file", "base")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "commit", "-m", "base")
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	testGit(t, nested, "init")
	write(t, nested, "precious", "keep")
	if _, err := c.PrepareDiscard(ctx, dir); err == nil {
		t.Fatal("accepted nested repository")
	}
	if b, err := os.ReadFile(filepath.Join(nested, "precious")); err != nil || string(b) != "keep" {
		t.Fatal("nested contents changed")
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "MERGE_HEAD"), []byte(testGit(t, dir, "rev-parse", "HEAD")), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PrepareDiscard(ctx, dir); err == nil {
		t.Fatal("accepted active merge")
	}
}

func TestDiscardUnstagedWorksBeforeFirstCommit(t *testing.T) {
	dir := repo(t)
	c := Client{}
	ctx := context.Background()
	write(t, dir, "staged", "staged")
	testGit(t, dir, "add", "staged")
	write(t, dir, "staged", "later edit")
	write(t, dir, "new", "remove")
	plan, err := c.PrepareDiscard(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.DiscardUnstaged(ctx, dir, plan.Token); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, dir, "show", ":staged"); got != "staged" {
		t.Fatal(got)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "staged")); err != nil || string(b) != "staged" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err = os.Stat(filepath.Join(dir, "new")); !os.IsNotExist(err) {
		t.Fatal("untracked remains")
	}
}

func TestDiscardUnstagedRefusesIntentToAddWithoutErasingOtherChanges(t *testing.T) {
	dir := repo(t)
	c := Client{}
	ctx := context.Background()
	write(t, dir, "base", "base")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "commit", "-m", "base")
	write(t, dir, "base", "keep edit")
	write(t, dir, "intent", "keep new file")
	testGit(t, dir, "add", "-N", "intent")
	if _, err := c.PrepareDiscard(ctx, dir); err == nil || !strings.Contains(err.Error(), "intent-to-add") {
		t.Fatalf("error=%v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "base")); err != nil || string(b) != "keep edit" {
		t.Fatal("changed another file")
	}
}
