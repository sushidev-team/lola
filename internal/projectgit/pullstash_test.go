package projectgit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pullRepos(t *testing.T) (string, string) {
	t.Helper()
	remote, dir := actionRepo(t), repo(t)
	testGit(t, dir, "remote", "add", "origin", remote)
	testGit(t, dir, "fetch", "origin")
	testGit(t, dir, "merge", "--ff-only", "origin/main")
	testGit(t, dir, "branch", "--set-upstream-to=origin/main", "main")
	return remote, dir
}

func TestPullDirtyFastForward(t *testing.T) {
	for _, scenario := range []string{"unrelated", "overlap", "untracked collision", "ignored collision", "up to date"} {
		t.Run(scenario, func(t *testing.T) {
			remote, dir := pullRepos(t)
			incomingFile := "remote"
			if scenario == "overlap" {
				incomingFile = "base"
			}
			if scenario != "up to date" {
				write(t, remote, incomingFile, "remote change\n")
				testGit(t, remote, "add", incomingFile)
				testGit(t, remote, "commit", "-m", "incoming")
			}
			write(t, dir, "base", "staged\n")
			testGit(t, dir, "add", "base")
			write(t, dir, "base", "staged and unstaged\n")
			write(t, dir, "untracked", "untracked\n")
			if scenario == "untracked collision" || scenario == "ignored collision" {
				write(t, dir, "remote", "my untracked work\n")
			}
			if scenario == "ignored collision" {
				write(t, dir, ".git/info/exclude", "remote\n")
			}
			index, working := testGit(t, dir, "diff", "--cached"), testGit(t, dir, "diff")
			head := tip(t, dir, "HEAD")
			err := (Client{}).Sync(context.Background(), dir, "pull")
			blocked := scenario == "overlap" || scenario == "untracked collision" || scenario == "ignored collision"
			if (err != nil) != blocked {
				t.Fatalf("pull error = %v", err)
			}
			wantHead := tip(t, remote, "HEAD")
			if blocked {
				wantHead = head
			}
			if tip(t, dir, "HEAD") != wantHead || testGit(t, dir, "diff", "--cached") != index || testGit(t, dir, "diff") != working {
				t.Fatal("pull changed local edits, staging, or the wrong HEAD")
			}
			if got, _ := os.ReadFile(filepath.Join(dir, "untracked")); string(got) != "untracked\n" {
				t.Fatal("untracked file lost")
			}
			if testGit(t, dir, "stash", "list") != "" {
				t.Fatal("ordinary pull stashed changes")
			}
		})
	}
}

func TestStashPullRefusedMergeRestoresEdits(t *testing.T) {
	remote, dir := pullRepos(t)
	write(t, remote, "ignored", "incoming file\n")
	testGit(t, remote, "add", "ignored")
	testGit(t, remote, "commit", "-m", "incoming")
	write(t, dir, ".git/info/exclude", "ignored\n")
	write(t, dir, "ignored", "private local file\n")
	write(t, dir, "base", "my work\n")
	head := tip(t, dir, "HEAD")
	err := (Client{}).BranchAction(context.Background(), dir, BranchRequest{Action: "pull-stash", Source: "refs/heads/main", SourceSHA: head})
	if err == nil || !strings.Contains(err.Error(), "local changes were restored") {
		t.Fatalf("refused merge result: %v", err)
	}
	if tip(t, dir, "HEAD") != head || testGit(t, dir, "stash", "list") != "" {
		t.Fatal("refused merge changed HEAD or failed to restore its stash")
	}
	for path, want := range map[string]string{"base": "my work\n", "ignored": "private local file\n"} {
		if got, _ := os.ReadFile(filepath.Join(dir, path)); string(got) != want {
			t.Fatalf("local file %s not preserved", path)
		}
	}
}

func TestStashPullRestoresStagingAndKeepsOtherStashes(t *testing.T) {
	remote, dir := pullRepos(t)
	c, ctx := Client{}, context.Background()
	write(t, dir, "earlier", "earlier stash\n")
	if err := c.StashPush(ctx, dir, "earlier", tip(t, dir, "HEAD")); err != nil {
		t.Fatal(err)
	}
	prior := testGit(t, dir, "stash", "list", "--format=%H")
	write(t, dir, "local", "local commit\n")
	testGit(t, dir, "add", "local")
	testGit(t, dir, "commit", "-m", "local")
	write(t, remote, "remote", "remote commit\n")
	testGit(t, remote, "add", "remote")
	testGit(t, remote, "commit", "-m", "remote")
	write(t, dir, "base", "staged\n")
	testGit(t, dir, "add", "base")
	write(t, dir, "base", "staged and unstaged\n")
	write(t, dir, "untracked", "new work\n")
	index, working := testGit(t, dir, "diff", "--cached"), testGit(t, dir, "diff")
	head := tip(t, dir, "HEAD")
	if err := c.BranchAction(ctx, dir, BranchRequest{Action: "pull-stash", Source: "refs/heads/main", SourceSHA: head}); err != nil {
		t.Fatal(err)
	}
	if testGit(t, dir, "show", "-s", "--format=%P", "HEAD") != head+" "+tip(t, remote, "HEAD") {
		t.Fatal("pull did not merge both histories")
	}
	if testGit(t, dir, "diff", "--cached") != index || testGit(t, dir, "diff") != working {
		t.Fatal("staged/unstaged split changed")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "untracked")); string(got) != "new work\n" {
		t.Fatal("untracked work not restored")
	}
	if testGit(t, dir, "stash", "list", "--format=%H") != prior {
		t.Fatal("successful pull changed an unrelated stash or left its own stash behind")
	}
}

func TestStashPullConflictRecovery(t *testing.T) {
	for _, scenario := range []string{"merge", "restore", "fetch"} {
		t.Run(scenario, func(t *testing.T) {
			remote, dir := pullRepos(t)
			c, ctx := Client{}, context.Background()
			if scenario == "merge" {
				write(t, dir, "base", "local commit\n")
				testGit(t, dir, "add", "base")
				testGit(t, dir, "commit", "-m", "local")
			}
			write(t, remote, "base", "remote commit\n")
			testGit(t, remote, "add", "base")
			testGit(t, remote, "commit", "-m", "remote")
			write(t, dir, "wip", "untracked work\n")
			if scenario == "restore" {
				write(t, dir, "base", "local uncommitted work\n")
			}
			if scenario == "fetch" {
				testGit(t, dir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing"))
			}
			head := tip(t, dir, "HEAD")
			err := c.BranchAction(ctx, dir, BranchRequest{Action: "pull-stash", Source: "refs/heads/main", SourceSHA: head})
			if err == nil {
				t.Fatal("expected pull/restore failure")
			}
			stashes, listErr := c.Stashes(ctx, dir)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if scenario == "fetch" {
				if len(stashes) != 0 || tip(t, dir, "HEAD") != head {
					t.Fatal("fetch failure changed checkout or created stash")
				}
			} else {
				if len(stashes) != 1 || !strings.Contains(stashes[0].Subject, "before pull of main") || !strings.Contains(err.Error(), stashes[0].SHA) {
					t.Fatalf("saved changes not recoverable: %v; %v", stashes, err)
				}
				if scenario == "merge" {
					if op, _ := c.Operation(ctx, dir); op != "merge" {
						t.Fatal("merge conflict not available to resolve")
					}
					if err := c.BranchAction(ctx, dir, BranchRequest{Action: "abort", Source: "merge", SourceSHA: head}); err != nil {
						t.Fatal(err)
					}
					if err := c.StashApply(ctx, dir, stashes[0].SHA, head, true); err != nil {
						t.Fatal(err)
					}
				} else if tip(t, dir, "HEAD") != tip(t, remote, "HEAD") {
					t.Fatal("restore failure undid pull")
				}
			}
			if got, _ := os.ReadFile(filepath.Join(dir, "wip")); string(got) != "untracked work\n" {
				t.Fatal("local work lost after recovery")
			}
		})
	}
}

func TestBackgroundFetchDoesNotReplacePullTarget(t *testing.T) {
	remote, dir := pullRepos(t)
	pinned := tip(t, dir, "FETCH_HEAD")
	write(t, remote, "new", "new\n")
	testGit(t, remote, "add", "new")
	testGit(t, remote, "commit", "-m", "new")
	c, ctx := Client{}, context.Background()
	for _, named := range []bool{false, true} {
		var err error
		if named {
			err = c.BranchAction(ctx, dir, BranchRequest{Action: "fetch", Source: "origin"})
		} else {
			err = c.Sync(ctx, dir, "fetch")
		}
		if err != nil {
			t.Fatal(err)
		}
		if tip(t, dir, "FETCH_HEAD") != pinned || tip(t, dir, "origin/main") != tip(t, remote, "HEAD") {
			t.Fatal("background fetch replaced FETCH_HEAD or failed to update refs")
		}
	}
}
