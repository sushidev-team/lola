package projectgit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBranchMergeWithLocalChanges(t *testing.T) {
	for _, scenario := range []string{"fast-forward", "diverged", "merge conflict", "restore conflict", "refused", "other checkout"} {
		t.Run(scenario, func(t *testing.T) {
			dir := actionRepo(t)
			c, ctx := Client{}, context.Background()
			testGit(t, dir, "switch", "-c", "live")
			write(t, dir, "incoming", "live change\n")
			testGit(t, dir, "add", "incoming")
			testGit(t, dir, "commit", "-m", "live change")
			source := tip(t, dir, "HEAD")
			testGit(t, dir, "switch", "main")
			switch scenario {
			case "diverged":
				write(t, dir, "base", "committed on main\n")
				testGit(t, dir, "commit", "-am", "main change")
			case "merge conflict":
				write(t, dir, "incoming", "main change\n")
				testGit(t, dir, "add", "incoming")
				testGit(t, dir, "commit", "-m", "main change")
			case "restore conflict":
				write(t, dir, "incoming", "untracked local work\n")
			case "refused":
				write(t, dir, ".git/info/exclude", "incoming\n")
				write(t, dir, "incoming", "ignored local work\n")
			case "other checkout":
				testGit(t, dir, "switch", "-c", "other")
			}
			target := tip(t, dir, "main")
			write(t, dir, "base", "staged\n")
			testGit(t, dir, "add", "base")
			write(t, dir, "base", "unstaged\n")
			write(t, dir, "untracked", "new work\n")
			index, working := testGit(t, dir, "diff", "--cached"), testGit(t, dir, "diff")
			err := c.BranchAction(ctx, dir, BranchRequest{Action: "merge", Source: "refs/heads/live", SourceSHA: source, Target: "refs/heads/main", TargetSHA: target})
			switch scenario {
			case "merge conflict", "restore conflict":
				stashes, listErr := c.Stashes(ctx, dir)
				if listErr != nil || len(stashes) != 1 || err == nil || !strings.Contains(err.Error(), stashes[0].SHA) || !strings.Contains(stashes[0].Subject, "before merge of live into main") {
					t.Fatalf("expected recoverable stash, got %v, %v, %v", stashes, listErr, err)
				}
				if testGit(t, dir, "show", "stash@{0}^2:base") != "staged" || testGit(t, dir, "show", "stash@{0}:base") != "unstaged" || testGit(t, dir, "show", "stash@{0}^3:untracked") != "new work" {
					t.Fatal("saved stash is missing local changes")
				}
				if scenario == "restore conflict" {
					if tip(t, dir, "HEAD") != source || testGit(t, dir, "show", "stash@{0}^3:incoming") != "untracked local work" {
						t.Fatal("merge or untracked file lost after restore conflict")
					}
				} else if tip(t, dir, "HEAD") != target || tip(t, dir, "MERGE_HEAD") != source {
					t.Fatal("merge conflict is no longer available to resolve")
				}
				return
			case "other checkout", "refused":
				if err == nil || tip(t, dir, "main") != target {
					t.Fatalf("expected merge refusal, got %v", err)
				}
				if scenario == "other checkout" && testGit(t, dir, "branch", "--show-current") != "other" {
					t.Fatal("moved edits off the original branch")
				}
				if scenario == "refused" {
					if got, _ := os.ReadFile(filepath.Join(dir, "incoming")); string(got) != "ignored local work\n" {
						t.Fatal("overwrote ignored file")
					}
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "fast-forward" && tip(t, dir, "main") != source {
					t.Fatal("main did not fast-forward")
				}
				if scenario == "diverged" && testGit(t, dir, "show", "-s", "--format=%P", "main") != target+" "+source {
					t.Fatal("main did not merge live")
				}
			}
			if testGit(t, dir, "diff", "--cached") != index || testGit(t, dir, "diff") != working || testGit(t, dir, "stash", "list") != "" {
				t.Fatal("local changes or staging were not restored")
			}
			if got, _ := os.ReadFile(filepath.Join(dir, "untracked")); string(got) != "new work\n" {
				t.Fatal("lost untracked work")
			}
		})
	}
}
