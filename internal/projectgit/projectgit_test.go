package projectgit

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	b, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v: %s", args, e, b)
	}
	return strings.TrimSpace(string(b))
}
func repo(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	testGit(t, dir, "init", "-b", "main")
	testGit(t, dir, "config", "user.name", "Workspace tester")
	testGit(t, dir, "config", "user.email", "workspace@example.test")
	return dir
}
func write(t *testing.T, dir, path, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestSnapshotIncludesLightweightAndAnnotatedTags(t *testing.T) {
	dir := repo(t)
	write(t, dir, "base", "base\n")
	testGit(t, dir, "add", "base")
	testGit(t, dir, "commit", "-m", "base")
	testGit(t, dir, "tag", "v0.5.0")
	testGit(t, dir, "tag", "-a", "release", "-m", "Release")
	snapshot, err := (Client{}).Snapshot(context.Background(), dir, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Commits) != 1 {
		t.Fatalf("commits: %+v", snapshot.Commits)
	}
	refs := strings.Join(snapshot.Commits[0].Refs, "\n")
	if !strings.Contains(refs, "tag: v0.5.0") || !strings.Contains(refs, "tag: release") {
		t.Fatalf("missing tag decorations: %s", refs)
	}
	if len(snapshot.Refs) != 1 || snapshot.Refs[0].Name != "main" {
		t.Fatal("tags leaked into branch actions")
	}
}

func TestUnbornStageUnstageCommitAndDiff(t *testing.T) {
	dir := repo(t)
	ctx := context.Background()
	c := Client{}
	write(t, dir, "first.txt", "hello\n")
	s, err := c.Snapshot(ctx, dir, "", 200)
	if err != nil || s.Head != "" || len(s.Commits) != 0 || len(s.Files) != 1 || !s.Files[0].Untracked {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
	d, err := c.Diff(ctx, dir, "", "", "untracked", "first.txt")
	if err != nil || len(d.Files) != 1 || d.Files[0].Additions != 1 {
		t.Fatalf("diff=%+v err=%v", d, err)
	}
	if err = c.ChangeIndex(ctx, dir, []string{"first.txt"}, true); err != nil {
		t.Fatal(err)
	}
	if err = c.ChangeIndex(ctx, dir, []string{"first.txt"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "first.txt")); err != nil {
		t.Fatal("unstage deleted the working file")
	}
	if err = c.ChangeIndex(ctx, dir, []string{"first.txt"}, true); err != nil {
		t.Fatal(err)
	}
	if err = c.Commit(ctx, dir, "initial\n\nBody text"); err != nil {
		t.Fatal(err)
	}
	s, err = c.Snapshot(ctx, dir, "", 200)
	if err != nil || len(s.Commits) != 1 || s.Commits[0].Subject != "initial" || s.Commits[0].Author != "Workspace tester" || s.Branch != "main" {
		t.Fatalf("snapshot=%+v err=%v", s, err)
	}
	d, err = c.Diff(ctx, dir, "", s.Head, "commit", "")
	if err != nil || len(d.Files) != 1 || d.Files[0].Additions != 1 {
		t.Fatalf("root diff=%+v err=%v", d, err)
	}
}
func TestHistoryRenameComparisonAndWorkingAreas(t *testing.T) {
	dir := repo(t)
	ctx := context.Background()
	c := Client{}
	write(t, dir, "old name.txt", "one\ntwo\nthree\n")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "commit", "-m", "first")
	first := testGit(t, dir, "rev-parse", "HEAD")
	testGit(t, dir, "mv", "old name.txt", "new name.txt")
	s, err := c.Snapshot(ctx, dir, "", 200)
	if err != nil || len(s.Files) != 1 || s.Files[0].OldPath != "old name.txt" {
		t.Fatalf("rename=%+v err=%v", s.Files, err)
	}
	if err = c.ChangeIndex(ctx, dir, []string{"new name.txt"}, false); err != nil {
		t.Fatal(err)
	}
	if testGit(t, dir, "diff", "--cached", "--name-only") != "" {
		t.Fatal("rename remains staged")
	}
	testGit(t, dir, "add", "-A")
	if err = c.Commit(ctx, dir, "rename"); err != nil {
		t.Fatal(err)
	}
	second := testGit(t, dir, "rev-parse", "HEAD")
	d, err := c.Diff(ctx, dir, first, second, "commit", "")
	if err != nil || len(d.Files) != 1 || d.Files[0].Status != "renamed" || d.Files[0].OldPath != "old name.txt" {
		t.Fatalf("comparison=%+v err=%v", d, err)
	}
	write(t, dir, "new name.txt", "one\ntwo\nchanged\n")
	if err = c.ChangeIndex(ctx, dir, []string{"new name.txt"}, true); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "new name.txt", "one\ntwo\nchanged again\n")
	staged, err := c.Diff(ctx, dir, "", "", "staged", "")
	if err != nil {
		t.Fatal(err)
	}
	unstaged, err := c.Diff(ctx, dir, "", "", "unstaged", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(staged.Files[0].Patch, "+changed\n") || !strings.Contains(unstaged.Files[0].Patch, "+changed again") {
		t.Fatalf("staged=%+v unstaged=%+v", staged, unstaged)
	}
	s, err = c.Snapshot(ctx, dir, "refs/heads/main", 1)
	if err != nil || !s.HasMore || len(s.Commits) != 1 || len(s.Commits[0].Parents) != 1 || s.Commits[0].Parents[0] != first {
		t.Fatalf("history=%+v err=%v", s, err)
	}
	if _, err = c.Snapshot(ctx, dir, "--all", 200); err == nil {
		t.Fatal("accepted injected ref")
	}
	if _, err = c.Diff(ctx, dir, "", "--help", "commit", ""); err == nil {
		t.Fatal("accepted injected SHA")
	}
}
func TestLiteralPathspecAndOutsideSymlink(t *testing.T) {
	dir := repo(t)
	c := Client{}
	ctx := context.Background()
	write(t, dir, "*.txt", "literal")
	write(t, dir, "other.txt", "other")
	if err := c.ChangeIndex(ctx, dir, []string{"*.txt"}, true); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, dir, "diff", "--cached", "--name-only"); got != "*.txt" {
		t.Fatalf("staged %q", got)
	}
	if err := c.ChangeIndex(ctx, dir, []string{"../outside"}, true); err == nil {
		t.Fatal("accepted traversal")
	}
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Diff(ctx, dir, "", "", "untracked", "link"); err == nil {
		t.Fatal("read outside symlink")
	}
}
func TestCommitPreservesSigningAndHooks(t *testing.T) {
	dir := repo(t)
	ctx := context.Background()
	c := Client{}
	write(t, dir, "a", "one")
	testGit(t, dir, "add", "a")
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 17\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := c.Commit(ctx, dir, "blocked by hook"); err == nil {
		t.Fatal("bypassed hook")
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "config", "commit.gpgsign", "true")
	testGit(t, dir, "config", "gpg.program", "/usr/bin/false")
	testGit(t, dir, "config", "user.signingkey", "test-key")
	s, err := c.Snapshot(ctx, dir, "", 200)
	if err != nil || !s.Signing.Enabled || s.Signing.Key != "test-key" {
		t.Fatalf("signing=%+v err=%v", s.Signing, err)
	}
	if err = c.Commit(ctx, dir, "signed"); err == nil {
		t.Fatal("silently bypassed signing failure")
	}
	if testGit(t, dir, "diff", "--cached", "--name-only") != "a" {
		t.Fatal("failed signing consumed index")
	}
}
func TestSyncHasNoForceOrRewrite(t *testing.T) {
	var got [][]string
	c := Client{run: func(_ context.Context, _, _ string, args ...string) ([]byte, error) {
		got = append(got, args)
		return nil, nil
	}}
	for _, a := range []string{"fetch", "push"} {
		if err := c.Sync(context.Background(), "repo", a); err != nil {
			t.Fatal(err)
		}
	}
	want := [][]string{{"fetch", "--all", "--prune"}, {"push"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestInstalledGitCreatesSSHSignature(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen unavailable")
	}
	dir := repo(t)
	key := filepath.Join(t.TempDir(), "signing-key")
	if b, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("key generation: %v %s", err, b)
	}
	testGit(t, dir, "config", "gpg.format", "ssh")
	testGit(t, dir, "config", "commit.gpgsign", "true")
	testGit(t, dir, "config", "user.signingkey", key)
	write(t, dir, "signed.txt", "Signed with the user's configured Git flow.\n")
	c := Client{}
	ctx := context.Background()
	if err := c.ChangeIndex(ctx, dir, []string{"signed.txt"}, true); err != nil {
		t.Fatal(err)
	}
	if err := c.Commit(ctx, dir, "signed through workspace"); err != nil {
		t.Fatal(err)
	}
	raw := testGit(t, dir, "cat-file", "commit", "HEAD")
	if !strings.Contains(raw, "gpgsig -----BEGIN SSH SIGNATURE-----") {
		t.Fatalf("missing SSH signature: %s", raw)
	}
}

func TestHistoryOmitsPrivateCheckpointRefs(t *testing.T) {
	dir := repo(t)
	write(t, dir, "a", "main")
	testGit(t, dir, "add", "a")
	testGit(t, dir, "commit", "-m", "visible")
	tree := testGit(t, dir, "rev-parse", "HEAD^{tree}")
	hidden := testGit(t, dir, "commit-tree", tree, "-m", "private checkpoint")
	testGit(t, dir, "update-ref", "refs/lola/checkpoints/session/1", hidden)
	s, err := (Client{}).Snapshot(context.Background(), dir, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Commits) != 1 || s.Commits[0].Subject != "visible" {
		t.Fatalf("private checkpoint in graph: %+v", s.Commits)
	}
}

func TestStageEditsAfterIndexRename(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(fmt.Sprintf("deleted=%v", deleted), func(t *testing.T) {
			dir := repo(t)
			ctx := context.Background()
			c := Client{}
			write(t, dir, "old.txt", "one\ntwo\nthree\n")
			testGit(t, dir, "add", "old.txt")
			testGit(t, dir, "commit", "-m", "initial")
			testGit(t, dir, "mv", "old.txt", "new.txt")
			if deleted {
				if err := os.Remove(filepath.Join(dir, "new.txt")); err != nil {
					t.Fatal(err)
				}
			} else {
				write(t, dir, "new.txt", "one\ntwo\nchanged\n")
			}
			files, err := c.status(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			wantWorktree := "M"
			if deleted {
				wantWorktree = "D"
			}
			if len(files) != 1 || files[0].Index != "R" || files[0].Worktree != wantWorktree {
				t.Fatalf("status before staging=%+v", files)
			}
			if err = c.ChangeIndex(ctx, dir, []string{"new.txt"}, true); err != nil {
				t.Fatal(err)
			}
			if got := testGit(t, dir, "diff", "--name-only"); got != "" {
				t.Fatalf("edits remain unstaged: %q", got)
			}
			if !deleted && testGit(t, dir, "show", ":new.txt") != "one\ntwo\nchanged" {
				t.Fatal("renamed file edits not staged")
			}
		})
	}
}

func TestStageRenamedFileDoesNotStageRecreatedSource(t *testing.T) {
	dir := repo(t)
	ctx := context.Background()
	c := Client{}
	write(t, dir, "old.txt", "one\ntwo\nthree\n")
	testGit(t, dir, "add", "old.txt")
	testGit(t, dir, "commit", "-m", "initial")
	testGit(t, dir, "mv", "old.txt", "new.txt")
	write(t, dir, "new.txt", "one\ntwo\nchanged\n")
	write(t, dir, "old.txt", "unrelated new work\n")
	if err := c.ChangeIndex(ctx, dir, []string{"new.txt"}, true); err != nil {
		t.Fatal(err)
	}
	files, err := c.status(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range files {
		if f.Path == "old.txt" && f.Untracked {
			found = true
		}
	}
	if !found {
		t.Fatalf("recreated source was staged: %+v", files)
	}
}

func TestUnstageUnbornFilePreservesLaterWorkingEdits(t *testing.T) {
	dir := repo(t)
	ctx := context.Background()
	c := Client{}
	write(t, dir, "first.txt", "staged version\n")
	if err := c.ChangeIndex(ctx, dir, []string{"first.txt"}, true); err != nil {
		t.Fatal(err)
	}
	working := "later working edits\nwith another line\n"
	write(t, dir, "first.txt", working)
	files, err := c.status(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Index != "A" || files[0].Worktree != "M" {
		t.Fatalf("expected AM, got %+v", files)
	}
	if err = c.ChangeIndex(ctx, dir, []string{"first.txt"}, false); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "first.txt"))
	if err != nil || string(content) != working {
		t.Fatalf("working content changed: %q, %v", content, err)
	}
	if staged := testGit(t, dir, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("still staged: %q", staged)
	}
	files, err = c.status(ctx, dir)
	if err != nil || len(files) != 1 || !files[0].Untracked {
		t.Fatalf("status after unstage=%+v, %v", files, err)
	}
}

func TestAmendReplacesOnlyExpectedHeadWithStagedChanges(t *testing.T) {
	dir := repo(t)
	ctx := context.Background()
	c := Client{}
	write(t, dir, "a", "original\n")
	testGit(t, dir, "add", "a")
	testGit(t, dir, "commit", "-m", "original")
	head := testGit(t, dir, "rev-parse", "HEAD")
	if err := c.Amend(ctx, dir, "empty", head); err == nil {
		t.Fatal("amended with no staged changes")
	}
	write(t, dir, "a", "staged\n")
	testGit(t, dir, "add", "a")
	write(t, dir, "a", "unstaged\n")
	for _, stale := range []string{"", "--help", strings.Repeat("f", 40)} {
		if err := c.Amend(ctx, dir, "refused", stale); err == nil {
			t.Fatalf("accepted expected HEAD %q", stale)
		}
	}
	if got := testGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatal("failed amendment moved HEAD")
	}
	if err := c.Amend(ctx, dir, "Updated subject\n\nDetailed body", head); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, dir, "rev-list", "--count", "HEAD"); got != "1" {
		t.Fatalf("amend added a commit: %s", got)
	}
	if got := testGit(t, dir, "log", "-1", "--format=%B"); got != "Updated subject\n\nDetailed body" {
		t.Fatalf("message=%q", got)
	}
	if got := testGit(t, dir, "show", "HEAD:a"); got != "staged" {
		t.Fatalf("committed unstaged work: %q", got)
	}
	if got := testGit(t, dir, "diff", "--name-only"); got != "a" {
		t.Fatalf("lost unstaged work: %q", got)
	}
}

func TestAmendPreservesHooksAndSigning(t *testing.T) {
	dir := repo(t)
	ctx := context.Background()
	c := Client{}
	write(t, dir, "a", "initial")
	testGit(t, dir, "add", "a")
	testGit(t, dir, "commit", "-m", "initial")
	head := testGit(t, dir, "rev-parse", "HEAD")
	write(t, dir, "a", "updated")
	testGit(t, dir, "add", "a")
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 17\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := c.Amend(ctx, dir, "blocked by hook", head); err == nil {
		t.Fatal("bypassed hook")
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "config", "commit.gpgsign", "true")
	testGit(t, dir, "config", "gpg.program", "/usr/bin/false")
	testGit(t, dir, "config", "user.signingkey", "test-key")
	if err := c.Amend(ctx, dir, "blocked by signing", head); err == nil {
		t.Fatal("bypassed signing")
	}
	if got := testGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatal("failed amend moved HEAD")
	}
	if got := testGit(t, dir, "diff", "--cached", "--name-only"); got != "a" {
		t.Fatal("failed amend consumed index")
	}
}

func TestLastCommitMessagePinsExpectedHeadAndPreservesBody(t *testing.T) {
	dir := repo(t)
	testGit(t, dir, "commit", "--allow-empty", "-m", "Subject\n\nBody paragraph\n\nAnother paragraph")
	head := testGit(t, dir, "rev-parse", "HEAD")
	ctx := context.Background()
	c := Client{}
	message, err := c.LastCommitMessage(ctx, dir, head)
	if err != nil || message != "Subject\n\nBody paragraph\n\nAnother paragraph" {
		t.Fatalf("message=%q err=%v", message, err)
	}
	for _, invalid := range []string{"", "--help", strings.Repeat("f", 40)} {
		if _, err = c.LastCommitMessage(ctx, dir, invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	testGit(t, dir, "commit", "--allow-empty", "-m", "Newer")
	if _, err = c.LastCommitMessage(ctx, dir, head); err == nil {
		t.Fatal("read stale HEAD message")
	}
}

func TestIndexStatusRefreshReadsOnlyFiles(t *testing.T) {
	dir := repo(t)
	ctx := context.Background()
	write(t, dir, "a", "base")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "commit", "-m", "base")
	write(t, dir, "a", "changed")
	calls := []string{}
	c := Client{run: func(ctx context.Context, dir, input string, args ...string) ([]byte, error) {
		calls = append(calls, args[0])
		return runGit(ctx, dir, input, args...)
	}}
	files, err := c.ChangeIndexAndStatus(ctx, dir, []string{"a"}, true)
	if err != nil || len(files) != 1 || files[0].Index != "M" || files[0].Worktree != " " {
		t.Fatalf("files=%+v err=%v", files, err)
	}
	if !reflect.DeepEqual(calls, []string{"status", "add", "status"}) {
		t.Fatalf("unexpected reads: %v", calls)
	}
	calls = nil
	files, err = c.ChangeIndexAndStatus(ctx, dir, []string{"a"}, false)
	if err != nil || len(files) != 1 || files[0].Worktree != "M" || files[0].Index != " " {
		t.Fatalf("files=%+v err=%v", files, err)
	}
	if !reflect.DeepEqual(calls, []string{"status", "rev-parse", "restore", "status"}) {
		t.Fatalf("unexpected reads: %v", calls)
	}
}

func TestAmendConfirmationChecksAllRemoteHistories(t *testing.T) {
	dir := repo(t)
	ctx := context.Background()
	c := Client{}
	write(t, dir, "file", "first")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "commit", "-m", "first")
	first := testGit(t, dir, "rev-parse", "HEAD")
	check := func(want bool) {
		t.Helper()
		got, err := c.AmendNeedsConfirmation(ctx, dir, testGit(t, dir, "rev-parse", "HEAD"))
		if err != nil || got != want {
			t.Fatalf("confirmation=%v want=%v err=%v", got, want, err)
		}
	}
	check(false)
	testGit(t, dir, "update-ref", "refs/remotes/other/topic", first)
	check(true)
	write(t, dir, "file", "local change")
	testGit(t, dir, "commit", "-am", "local")
	second := testGit(t, dir, "rev-parse", "HEAD")
	check(false)
	if _, err := c.AmendNeedsConfirmation(ctx, dir, first); err == nil {
		t.Fatal("stale HEAD accepted")
	}
	testGit(t, dir, "update-ref", "refs/remotes/other/topic", second)
	// HEAD is an ancestor of a remote tip, not equal to one: still published.
	testGit(t, dir, "checkout", "--detach", first)
	check(true)
	if _, err := c.AmendNeedsConfirmation(ctx, dir, "--all"); err == nil {
		t.Fatal("invalid hash accepted")
	}
}

func TestSnapshotReportsUpstreamDivergence(t *testing.T) {
	dir := repo(t)
	testGit(t, dir, "commit", "--allow-empty", "-m", "base")
	testGit(t, dir, "branch", "base")
	testGit(t, dir, "branch", "upstream")
	testGit(t, dir, "commit", "--allow-empty", "-m", "local")
	testGit(t, dir, "checkout", "upstream")
	testGit(t, dir, "commit", "--allow-empty", "-m", "upstream")
	testGit(t, dir, "checkout", "main")
	testGit(t, dir, "branch", "--set-upstream-to=upstream", "main")
	testGit(t, dir, "branch", "behind", "base")
	testGit(t, dir, "branch", "--set-upstream-to=upstream", "behind")
	testGit(t, dir, "branch", "ahead", "main")
	testGit(t, dir, "branch", "--set-upstream-to=base", "ahead")
	testGit(t, dir, "branch", "untracked", "main")
	got, err := (Client{}).Snapshot(context.Background(), dir, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string][2]int{"main": {1, 1}, "behind": {0, 1}, "ahead": {1, 0}, "untracked": {0, 0}}
	for _, ref := range got.Refs {
		if want, ok := expected[ref.Name]; ok {
			if ref.Ahead != want[0] || ref.Behind != want[1] {
				t.Errorf("%s: got +%d -%d, want %v", ref.Name, ref.Ahead, ref.Behind, want)
			}
			delete(expected, ref.Name)
		}
	}
	if len(expected) > 0 {
		t.Fatalf("missing branches: %v", expected)
	}
}
