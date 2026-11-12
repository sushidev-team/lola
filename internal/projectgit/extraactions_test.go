package projectgit

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateWorktreeKeepsCheckoutAndRequiresNewFolder(t *testing.T) {
	dir := actionRepo(t)
	ctx := context.Background()
	c := Client{}
	head := tip(t, dir, "HEAD")
	write(t, dir, "base", "work in progress\n")
	path := filepath.Join(t.TempDir(), "parallel")
	if err := c.CreateWorktree(ctx, dir, "refs/heads/main", head, path, "feature/parallel"); err != nil {
		t.Fatal(err)
	}
	if testGit(t, path, "branch", "--show-current") != "feature/parallel" || tip(t, path, "HEAD") != head {
		t.Fatal("wrong checkout")
	}
	if testGit(t, dir, "branch", "--show-current") != "main" || testGit(t, dir, "diff") == "" {
		t.Fatal("modified original checkout")
	}
	if err := c.CreateWorktree(ctx, dir, "refs/heads/main", head, path, "another"); err == nil {
		t.Fatal("reused folder")
	}
	if err := c.CreateWorktree(ctx, dir, "refs/heads/main", head, filepath.Join(t.TempDir(), "occupied"), ""); err == nil {
		t.Fatal("checked out main twice")
	}
	testGit(t, dir, "branch", "unused")
	unused := filepath.Join(t.TempDir(), "unused")
	if err := c.CreateWorktree(ctx, dir, "refs/heads/unused", head, unused, ""); err != nil {
		t.Fatal(err)
	}
	if testGit(t, unused, "branch", "--show-current") != "unused" {
		t.Fatal("detached worktree")
	}
	if err := c.CreateWorktree(ctx, dir, "refs/heads/main", strings.Repeat("f", 40), filepath.Join(t.TempDir(), "stale"), "stale"); err == nil {
		t.Fatal("accepted stale source")
	}
}

func TestCherryPickRevertAndConflictRecovery(t *testing.T) {
	ctx := context.Background()
	c := Client{}
	dir := actionRepo(t)
	base := tip(t, dir, "HEAD")
	testGit(t, dir, "switch", "-c", "topic")
	write(t, dir, "base", "topic\n")
	testGit(t, dir, "add", "base")
	testGit(t, dir, "commit", "-m", "topic")
	topic := tip(t, dir, "HEAD")
	testGit(t, dir, "switch", "main")
	r := BranchRequest{Action: "cherry-pick", SourceSHA: topic, Target: "refs/heads/main", TargetSHA: base}
	if err := c.BranchAction(ctx, dir, r); err != nil {
		t.Fatal(err)
	}
	picked := tip(t, dir, "HEAD")
	r = BranchRequest{Action: "revert", SourceSHA: picked, Target: "refs/heads/main", TargetSHA: picked}
	if err := c.BranchAction(ctx, dir, r); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "base")); string(got) != "base\n" {
		t.Fatal("revert did not restore content")
	}
	for _, recovery := range []string{"abort", "continue"} {
		t.Run(recovery, func(t *testing.T) {
			d := actionRepo(t)
			testGit(t, d, "switch", "-c", "topic")
			write(t, d, "base", "incoming\n")
			testGit(t, d, "add", "base")
			testGit(t, d, "commit", "-m", "incoming")
			incoming := tip(t, d, "HEAD")
			testGit(t, d, "switch", "main")
			write(t, d, "base", "local\n")
			testGit(t, d, "add", "base")
			testGit(t, d, "commit", "-m", "local")
			head := tip(t, d, "HEAD")
			if err := c.BranchAction(ctx, d, BranchRequest{Action: "cherry-pick", SourceSHA: incoming, Target: "refs/heads/main", TargetSHA: head}); err == nil {
				t.Fatal("expected conflict")
			}
			if op, _ := c.Operation(ctx, d); op != "cherry-pick" {
				t.Fatal(op)
			}
			if recovery == "continue" {
				write(t, d, "base", "resolved\n")
				testGit(t, d, "add", "base")
			}
			if err := c.BranchAction(ctx, d, BranchRequest{Action: recovery, Source: "cherry-pick", SourceSHA: head}); err != nil {
				t.Fatal(err)
			}
			if op, _ := c.Operation(ctx, d); op != "" {
				t.Fatal(op)
			}
			if recovery == "abort" && tip(t, d, "HEAD") != head {
				t.Fatal("abort moved HEAD")
			}
		})
	}
	write(t, dir, "untracked", "keep")
	if err := c.BranchAction(ctx, dir, BranchRequest{Action: "cherry-pick", SourceSHA: topic, Target: "refs/heads/main", TargetSHA: tip(t, dir, "HEAD")}); err == nil {
		t.Fatal("accepted dirty checkout")
	}
}

func TestWorkingComparisonPreservesIndexAndIncludesAllFiles(t *testing.T) {
	dir := actionRepo(t)
	c := Client{}
	ctx := context.Background()
	base := tip(t, dir, "HEAD")
	write(t, dir, "base", "staged\n")
	write(t, dir, "added", "added\n")
	testGit(t, dir, "add", "base", "added")
	write(t, dir, "base", "unstaged\n")
	write(t, dir, "untracked", "new\n")
	write(t, dir, ".gitignore", "ignored\n")
	write(t, dir, "ignored", "secret\n")
	// Exercise a split index too; the comparison must not modify its bytes.
	testGit(t, dir, "update-index", "--split-index")
	index := filepath.Join(dir, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.Diff(ctx, dir, base, "", "working", "")
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(index)
	if !bytes.Equal(before, after) {
		t.Fatal("real index changed")
	}
	names := map[string]string{}
	for _, f := range result.Files {
		names[f.Path] = f.Patch
	}
	for _, name := range []string{"base", "added", "untracked", ".gitignore"} {
		if _, ok := names[name]; !ok {
			t.Fatal("missing", name)
		}
	}
	if _, ok := names["ignored"]; ok {
		t.Fatal("included ignored file")
	}
	if !strings.Contains(names["base"], "+unstaged") {
		t.Fatal("did not compare current working content")
	}
	// A historical tracked file may now be untracked: show its actual content,
	// not a duplicate delete/add pair.
	testGit(t, dir, "rm", "--cached", "-f", "base")
	result, err = c.Diff(ctx, dir, base, "", "working", "base")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || !strings.Contains(result.Files[0].Patch, "+unstaged") {
		t.Fatalf("historical path: %+v", result)
	}
}

func TestRevertConflictContinueAndAbort(t *testing.T) {
	for _, recovery := range []string{"continue", "abort"} {
		t.Run(recovery, func(t *testing.T) {
			dir := actionRepo(t)
			c := Client{}
			ctx := context.Background()
			write(t, dir, "base", "first\n")
			testGit(t, dir, "add", "base")
			testGit(t, dir, "commit", "-m", "first")
			first := tip(t, dir, "HEAD")
			write(t, dir, "base", "later\n")
			testGit(t, dir, "add", "base")
			testGit(t, dir, "commit", "-m", "later")
			head := tip(t, dir, "HEAD")
			r := BranchRequest{Action: "revert", SourceSHA: first, Target: "refs/heads/main", TargetSHA: head}
			if err := c.BranchAction(ctx, dir, r); err == nil {
				t.Fatal("expected conflict")
			}
			if op, _ := c.Operation(ctx, dir); op != "revert" {
				t.Fatal(op)
			}
			if recovery == "continue" {
				write(t, dir, "base", "resolved revert\n")
				testGit(t, dir, "add", "base")
			}
			if err := c.BranchAction(ctx, dir, BranchRequest{Action: recovery, Source: "revert", SourceSHA: head}); err != nil {
				t.Fatal(err)
			}
			if op, _ := c.Operation(ctx, dir); op != "" {
				t.Fatal(op)
			}
			if recovery == "abort" && tip(t, dir, "HEAD") != head {
				t.Fatal("abort moved HEAD")
			}
		})
	}
}

func TestCommitActionsRejectStaleCheckoutAndRequireMergeMainline(t *testing.T) {
	dir := actionRepo(t)
	c := Client{}
	ctx := context.Background()
	base := tip(t, dir, "HEAD")
	testGit(t, dir, "switch", "-c", "topic")
	write(t, dir, "topic", "topic\n")
	testGit(t, dir, "add", "topic")
	testGit(t, dir, "commit", "-m", "topic")
	testGit(t, dir, "switch", "main")
	testGit(t, dir, "merge", "--no-ff", "--no-edit", "topic")
	merge := tip(t, dir, "HEAD")
	r := BranchRequest{Action: "revert", SourceSHA: merge, Target: "refs/heads/main", TargetSHA: merge}
	if err := c.BranchAction(ctx, dir, r); err == nil {
		t.Fatal("accepted implicit merge parent")
	}
	r.Mainline = 1
	r.TargetSHA = base
	if err := c.BranchAction(ctx, dir, r); err == nil {
		t.Fatal("accepted stale target")
	}
	r.TargetSHA = merge
	r.Target = "refs/heads/topic"
	if err := c.BranchAction(ctx, dir, r); err == nil {
		t.Fatal("accepted another checkout ref")
	}
	r.Target = "refs/heads/main"
	if err := c.BranchAction(ctx, dir, r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "topic")); !os.IsNotExist(err) {
		t.Fatal("first-parent revert kept topic file")
	}
}

func TestSkipEmptyCherryPickKeepsExistingCommit(t *testing.T) {
	dir := actionRepo(t)
	c := Client{}
	ctx := context.Background()
	head := tip(t, dir, "HEAD")
	r := BranchRequest{Action: "cherry-pick", SourceSHA: head, Target: "refs/heads/main", TargetSHA: head}
	if err := c.BranchAction(ctx, dir, r); err == nil {
		t.Fatal("expected empty cherry-pick")
	}
	if op, _ := c.Operation(ctx, dir); op != "cherry-pick" {
		t.Fatal(op)
	}
	if err := c.BranchAction(ctx, dir, BranchRequest{Action: "skip", Source: "cherry-pick", SourceSHA: head}); err != nil {
		t.Fatal(err)
	}
	if tip(t, dir, "HEAD") != head {
		t.Fatal("skip changed existing history")
	}
	if op, _ := c.Operation(ctx, dir); op != "" {
		t.Fatal(op)
	}
}
