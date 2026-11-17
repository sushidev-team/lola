package projectgit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func aiWrite(t *testing.T, dir, path, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, path), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func aiInput(t *testing.T, dir string) AICommitInput {
	t.Helper()
	in, err := (Client{}).PrepareAICommit(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return in
}
func TestAICommitSeparatesStagedAndUntrackedFiles(t *testing.T) {
	dir := actionRepo(t)
	aiWrite(t, dir, "staged.txt", "staged\n")
	testGit(t, dir, "add", "staged.txt")
	aiWrite(t, dir, "new.txt", "new\n")
	in := aiInput(t, dir)
	if in.Preparation.Staged != 1 {
		t.Fatal(in.Preparation)
	}
	groups, err := ParseAICommit(`{"groups":[{"subject":"Add new file","paths":["new.txt"]},{"subject":"Add staged file","paths":["staged.txt"]}]}`, in)
	if err != nil {
		t.Fatal(err)
	}
	err = (Client{}).CommitAIGroups(context.Background(), dir, in, groups, func(int, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(testGit(t, dir, "show", "--format=", "--name-only", "HEAD~1")); got != "new.txt" {
		t.Fatalf("unrelated staged file leaked: %q", got)
	}
	if got := strings.TrimSpace(testGit(t, dir, "status", "--porcelain")); got != "" {
		t.Fatal(got)
	}
}
func TestAICommitCancelKeepsCompletedCommitAndRemainingIndex(t *testing.T) {
	dir := actionRepo(t)
	aiWrite(t, dir, "first", "first")
	aiWrite(t, dir, "second", "second")
	testGit(t, dir, "add", "second")
	in := aiInput(t, dir)
	head := tip(t, dir, "HEAD")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	groups := []AICommitGroup{{Subject: "First", Paths: []string{"first"}}, {Subject: "Second", Paths: []string{"second"}}}
	err := (Client{}).CommitAIGroups(ctx, dir, in, groups, func(n int, _ string) {
		if n == 1 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if tip(t, dir, "HEAD") == head {
		t.Fatal("completed commit lost")
	}
	if got := strings.TrimSpace(testGit(t, dir, "diff", "--cached", "--name-only")); got != "second" {
		t.Fatal(got)
	}
}
func TestAICommitStopsOnChangedContentsOrIndex(t *testing.T) {
	for _, change := range []string{"file", "index", "branch", "new-file"} {
		t.Run(change, func(t *testing.T) {
			dir := actionRepo(t)
			aiWrite(t, dir, "first", "first")
			aiWrite(t, dir, "second", "second")
			in := aiInput(t, dir)
			head := tip(t, dir, "HEAD")
			switch change {
			case "file":
				aiWrite(t, dir, "second", "edited")
			case "index":
				testGit(t, dir, "add", "second")
			case "branch":
				testGit(t, dir, "switch", "-c", "other")
			case "new-file":
				aiWrite(t, dir, "third", "new")
			}
			err := (Client{}).CommitAIGroups(context.Background(), dir, in, []AICommitGroup{{Subject: "All", Paths: []string{"first", "second"}}}, func(int, string) {})
			if err == nil || tip(t, dir, "HEAD") != head {
				t.Fatalf("unexpected mutation: %v", err)
			}
		})
	}
}
func TestAICommitRejectsInvalidPartitions(t *testing.T) {
	dir := actionRepo(t)
	aiWrite(t, dir, "a", "a")
	aiWrite(t, dir, "b", "b")
	in := aiInput(t, dir)
	for _, output := range []string{
		`{"groups":[{"subject":"One","paths":["a"]}]}`,
		`{"groups":[{"subject":"One","paths":["a","a","b"]}]}`,
		`{"groups":[{"subject":"One","paths":["a","b","../outside"]}]}`,
		`{"groups":[{"subject":"","paths":["a","b"]}]}`,
		`{"groups":[{"subject":"One","paths":["a","b"],"command":"rm"}]}`,
	} {
		if _, err := ParseAICommit(output, in); err == nil {
			t.Fatalf("accepted %s", output)
		}
	}
}
func TestAICommitRenameAndDeletion(t *testing.T) {
	dir := actionRepo(t)
	aiWrite(t, dir, "old", "original")
	aiWrite(t, dir, "gone", "remove")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "commit", "-m", "Setup")
	testGit(t, dir, "mv", "old", "renamed")
	if err := os.Remove(filepath.Join(dir, "gone")); err != nil {
		t.Fatal(err)
	}
	in := aiInput(t, dir)
	err := (Client{}).CommitAIGroups(context.Background(), dir, in, []AICommitGroup{{Subject: "Rename", Paths: []string{"renamed"}}, {Subject: "Delete", Paths: []string{"gone"}}}, func(int, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(testGit(t, dir, "status", "--porcelain")); got != "" {
		t.Fatal(got)
	}
}
func TestAICommitFailedHookKeepsFilesAndStops(t *testing.T) {
	dir := actionRepo(t)
	aiWrite(t, dir, "first", "first")
	aiWrite(t, dir, "second", "second")
	in := aiInput(t, dir)
	head := tip(t, dir, "HEAD")
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	err := (Client{}).CommitAIGroups(context.Background(), dir, in, []AICommitGroup{{Subject: "First", Paths: []string{"first"}}, {Subject: "Second", Paths: []string{"second"}}}, func(int, string) {})
	if err == nil || tip(t, dir, "HEAD") != head {
		t.Fatal("failed hook did not stop commits")
	}
	if got := strings.TrimSpace(testGit(t, dir, "diff", "--cached", "--name-only")); got != "first" {
		t.Fatal(got)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "second")); err != nil || string(data) != "second" {
		t.Fatal("remaining file lost")
	}
}

func TestAICommitCancelDuringGitFinishesOnlyCurrentCommit(t *testing.T) {
	dir := actionRepo(t)
	aiWrite(t, dir, "first", "first")
	aiWrite(t, dir, "second", "second")
	in := aiInput(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := Client{run: func(gitCtx context.Context, dir, input string, args ...string) ([]byte, error) {
		if args[0] == "commit" {
			cancel()
			if gitCtx.Err() != nil {
				t.Fatal("cancellation interrupted in-flight Git")
			}
		}
		return runGit(gitCtx, dir, input, args...)
	}}
	err := c.CommitAIGroups(ctx, dir, in, []AICommitGroup{{Subject: "First", Paths: []string{"first"}}, {Subject: "Second", Paths: []string{"second"}}}, func(int, string) {})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(testGit(t, dir, "log", "-1", "--format=%s")); got != "First" {
		t.Fatal(got)
	}
	if got := strings.TrimSpace(testGit(t, dir, "status", "--porcelain")); got != "?? second" {
		t.Fatal(got)
	}
}
func TestAICommitStopsWhenRemainingFileChangesBetweenCommits(t *testing.T) {
	dir := actionRepo(t)
	aiWrite(t, dir, "first", "first")
	aiWrite(t, dir, "second", "second")
	in := aiInput(t, dir)
	err := (Client{}).CommitAIGroups(context.Background(), dir, in, []AICommitGroup{{Subject: "First", Paths: []string{"first"}}, {Subject: "Second", Paths: []string{"second"}}}, func(n int, subject string) {
		if n == 1 && subject == "" {
			aiWrite(t, dir, "second", "external edit")
		}
	})
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(testGit(t, dir, "log", "-1", "--format=%s")); got != "First" {
		t.Fatal(got)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "second")); err != nil || string(got) != "external edit" {
		t.Fatal("external edit lost")
	}
}
func TestAICommitCombinesPartiallyStagedFile(t *testing.T) {
	dir := actionRepo(t)
	aiWrite(t, dir, "partial", "staged version")
	testGit(t, dir, "add", "partial")
	aiWrite(t, dir, "partial", "final working version")
	in := aiInput(t, dir)
	err := (Client{}).CommitAIGroups(context.Background(), dir, in, []AICommitGroup{{Subject: "Complete file", Paths: []string{"partial"}}}, func(int, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(testGit(t, dir, "show", "HEAD:partial")); got != "final working version" {
		t.Fatal(got)
	}
}
