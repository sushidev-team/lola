package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/projectgit"
	"github.com/sushidev-team/lola/internal/session"
)

func projectGitRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.name", "Tester"}, {"config", "user.email", "test@example.test"}, {"commit", "--allow-empty", "-m", "Initial"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}
func TestProjectGitOnlyAllowsConfiguredRepositoryAndListedWorktrees(t *testing.T) {
	root := projectGitRepo(t)
	other := projectGitRepo(t)
	writeTestConfig(t, minimalConfig+fmt.Sprintf("\n[[project]]\nname = \"demo\"\npath = %q\n", root))
	svc := &ProjectGitService{}
	snap, err := svc.Snapshot("demo", "", "", 200)
	if err != nil || len(snap.Commits) != 1 {
		t.Fatalf("snapshot=%+v err=%v", snap, err)
	}
	if _, err = svc.Snapshot("demo", other, "", 200); err == nil {
		t.Fatal("accepted unrelated repository")
	}
	wt := filepath.Join(t.TempDir(), "checkout")
	cmd := exec.Command("git", "-C", root, "worktree", "add", "-b", "feature", wt)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worktree: %v %s", err, out)
	}
	if _, err = svc.Snapshot("demo", wt, "", 200); err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("LOLA_HOME")
	if err = os.MkdirAll(filepath.Join(home, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal([]session.Session{{ID: "sandbox", Worktree: wt, Sandbox: "docker"}})
	if err = os.WriteFile(filepath.Join(home, "state", "sessions.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Snapshot("demo", wt, "", 200); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("sandbox request error=%v", err)
	}
	if err = svc.Commit("demo", wt, "blocked"); err == nil {
		t.Fatal("sandbox commit accepted")
	}
	if err = svc.Amend("demo", wt, "blocked", snap.Head); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("sandbox amendment error=%v", err)
	}
	if _, err = svc.LastCommitMessage("demo", wt, snap.Head); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("sandbox message error=%v", err)
	}
	if _, err = svc.PrepareDiscard("demo", wt); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("sandbox discard preview error=%v", err)
	}
	if err = svc.DiscardUnstaged("demo", wt, "token"); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("sandbox discard error=%v", err)
	}
	if _, err = svc.DraftCommitMessage("demo", wt, ""); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("sandbox AI draft error=%v", err)
	}
	snap, err = svc.Snapshot("demo", "", "", 200)
	if err != nil {
		t.Fatal(err)
	}
	blocked := false
	for _, tree := range snap.Worktrees {
		if canonicalPath(tree.Path) == canonicalPath(wt) {
			blocked = tree.Blocked
		}
	}
	if !blocked {
		t.Fatal("sandbox worktree not marked blocked")
	}
	if err = os.WriteFile(filepath.Join(home, "state", "sessions.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Snapshot("demo", "", "", 200); err == nil {
		t.Fatal("accepted unreadable isolation state")
	}
}

func TestProjectGitWorktreeCreationRespectsBoundaries(t *testing.T) {
	root := projectGitRepo(t)
	writeTestConfig(t, minimalConfig+fmt.Sprintf("\n[[project]]\nname = \"demo\"\npath = %q\n", root))
	svc := &ProjectGitService{}
	snap, err := svc.Snapshot("demo", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("LOLA_HOME")
	alias := filepath.Join(t.TempDir(), "managed-link")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(root, "nested"), filepath.Join(home, "human"), filepath.Join(alias, "human"), root, "relative"} {
		if _, err := svc.CreateWorktree("demo", "", "refs/heads/main", snap.Head, path, "parallel"); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	destination := filepath.Join(t.TempDir(), "human")
	if _, err := svc.CreateWorktree("missing", "", "refs/heads/main", snap.Head, destination, "parallel"); err == nil {
		t.Fatal("accepted unknown project")
	}
	created, err := svc.CreateWorktree("demo", "", "refs/heads/main", snap.Head, destination, "parallel")
	if err != nil {
		t.Fatal(err)
	}
	if canonicalPath(created.Path) != canonicalPath(destination) {
		t.Fatal(created)
	}
	if _, err := svc.Snapshot("demo", created.Path, "", 100); err != nil {
		t.Fatal("created checkout not accessible", err)
	}
	if err := os.MkdirAll(filepath.Join(home, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]session.Session{{ID: "sandbox", Worktree: created.Path, Sandbox: "docker"}})
	if err := os.WriteFile(filepath.Join(home, "state", "sessions.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateWorktree("demo", created.Path, "refs/heads/parallel", snap.Head, filepath.Join(t.TempDir(), "forbidden"), "forbidden"); err == nil {
		t.Fatal("accepted sandbox source checkout")
	}
	if _, err := svc.BranchAction("demo", "", projectgit.BranchRequest{Action: "publish", Source: "refs/heads/parallel", SourceSHA: snap.Head, Remote: "origin", RemoteBranch: "parallel"}); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatal("publish bypassed owner boundary", err)
	}
	selection := projectgit.PRSelection{Remote: "origin", SourceRef: "refs/heads/parallel", SourceSHA: snap.Head, BaseRepo: "acme/repo", Base: "main"}
	if _, err := svc.PreviewPullRequest("demo", "", selection); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatal("PR preview bypassed source owner boundary", err)
	}
	if _, err := svc.CreatePullRequest("demo", "", projectgit.PRCreateRequest{Selection: selection, Title: "test"}); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatal("PR creation bypassed source owner boundary", err)
	}
	if _, err := svc.DraftPullRequest("demo", "", selection, "", ""); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatal("PR draft bypassed source owner boundary", err)
	}
}

func TestProjectGitWorktreeRemovalRespectsBoundaries(t *testing.T) {
	root := projectGitRepo(t)
	writeTestConfig(t, minimalConfig+fmt.Sprintf("\n[[project]]\nname = \"demo\"\npath = %q\n", root))
	svc := &ProjectGitService{}
	snap, err := svc.Snapshot("demo", "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "linked")
	if _, err = svc.CreateWorktree("demo", "", "refs/heads/main", snap.Head, path, "topic"); err != nil {
		t.Fatal(err)
	}
	r := projectgit.BranchRequest{Action: "remove-worktree-branch", Source: "refs/heads/topic", SourceSHA: snap.Head, Target: path}
	for _, target := range []string{root, projectGitRepo(t)} {
		bad := r
		bad.Target = target
		if _, err = svc.BranchAction("demo", "", bad); err == nil {
			t.Fatalf("accepted target %s", target)
		}
	}
	home := os.Getenv("LOLA_HOME")
	stateDir := filepath.Join(home, "state")
	if err = os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, sandbox := range []string{"docker", "none"} {
		raw, _ := json.Marshal([]session.Session{{ID: "managed", Worktree: path, Sandbox: sandbox}})
		if err = os.WriteFile(filepath.Join(stateDir, "sessions.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = svc.BranchAction("demo", "", r); err == nil {
			t.Fatalf("removed %s session worktree", sandbox)
		}
		if _, err = os.Stat(path); err != nil {
			t.Fatal("protected checkout disappeared")
		}
	}
	if err = os.WriteFile(filepath.Join(stateDir, "sessions.json"), []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.BranchAction("demo", "", r); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("checkout remains: %v", err)
	}
	if out, err := exec.Command("git", "-C", root, "show-ref", "--verify", r.Source).CombinedOutput(); err == nil {
		t.Fatalf("branch remains: %s", out)
	}
}
func TestProjectGitRejectsSandboxRootBeforeExecutingGit(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, minimalConfig+fmt.Sprintf("\n[[project]]\nname = \"demo\"\npath = %q\n", root))
	home := os.Getenv("LOLA_HOME")
	if err := os.MkdirAll(filepath.Join(home, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal([]session.Session{{ID: "sandbox", Worktree: root, Sandbox: "docker"}})
	if err := os.WriteFile(filepath.Join(home, "state", "sessions.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	// This is not a repository. An ordinary Git error would prove execution got
	// past the isolation boundary; the sandbox error must happen first.
	if _, err := (&ProjectGitService{}).Snapshot("demo", "", "", 200); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("error=%v", err)
	}
}

func TestProjectGitIdentitiesRequireKnownHostRepositoryAndBoundedHashes(t *testing.T) {
	root := projectGitRepo(t)
	writeTestConfig(t, minimalConfig+fmt.Sprintf("\n[[project]]\nname = \"demo\"\npath = %q\n", root))
	svc := &ProjectGitService{}
	if _, err := svc.Identities("demo", []string{"main"}); err == nil {
		t.Fatal("accepted non-hash revision")
	}
	if _, err := svc.Identities("missing", []string{strings.Repeat("a", 40)}); err == nil {
		t.Fatal("accepted unconfigured project")
	}
	// This repository has no GitHub remote: GitHub lookup is unnecessary and
	// must succeed empty even when gh is not installed or authenticated.
	result, err := svc.Identities("demo", []string{strings.Repeat("a", 40)})
	if err != nil || len(result.Authors) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	home := os.Getenv("LOLA_HOME")
	if err := os.MkdirAll(filepath.Join(home, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal([]session.Session{{ID: "sandbox", Worktree: root, Sandbox: "docker"}})
	if err := os.WriteFile(filepath.Join(home, "state", "sessions.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Identities("demo", []string{strings.Repeat("a", 40)}); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("sandbox error=%v", err)
	}
}

func TestProjectGitBranchActionsResolveOwnerAndBlockSandbox(t *testing.T) {
	root := projectGitRepo(t)
	writeTestConfig(t, minimalConfig+fmt.Sprintf("\n[[project]]\nname = \"demo\"\npath = %q\n", root))
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	wt := filepath.Join(t.TempDir(), "linked")
	run("worktree", "add", "-b", "feature", wt)
	sha := run("rev-parse", "feature")
	svc := &ProjectGitService{}
	request := projectgit.BranchRequest{Action: "checkout", Source: "refs/heads/feature", SourceSHA: sha}
	result, err := svc.BranchAction("demo", "", request)
	if err != nil || canonicalPath(result.Path) != canonicalPath(wt) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if current := run("branch", "--show-current"); current != "main" {
		t.Fatal("changed main checkout")
	}
	run("commit", "--allow-empty", "-m", "main advances")
	preview := projectgit.BranchRequest{Source: "refs/heads/main", SourceSHA: run("rev-parse", "HEAD"), Target: "refs/heads/feature", TargetSHA: sha}
	if ok, err := svc.CanFastForward("demo", "", preview); err != nil || !ok {
		t.Fatalf("preview=%v err=%v", ok, err)
	}
	// The target's worktree-specific settings determine the merge, even when
	// the source checkout is the one currently displayed.
	run("config", "extensions.worktreeConfig", "true")
	run("-C", wt, "config", "--worktree", "merge.ff", "false")
	if ok, err := svc.CanFastForward("demo", "", preview); err != nil || ok {
		t.Fatalf("target settings ignored: preview=%v err=%v", ok, err)
	}
	home := os.Getenv("LOLA_HOME")
	if err = os.MkdirAll(filepath.Join(home, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal([]session.Session{{ID: "sandbox", Worktree: wt, Sandbox: "docker"}})
	if err = os.WriteFile(filepath.Join(home, "state", "sessions.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"checkout", "pull", "push"} {
		request.Action = action
		if _, err = svc.BranchAction("demo", "", request); err == nil || !strings.Contains(err.Error(), "sandbox") {
			t.Fatalf("%s: %v", action, err)
		}
	}
	if _, err = svc.CanFastForward("demo", "", preview); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("preview read sandbox target: %v", err)
	}
	if _, err = svc.Stashes("demo", wt); err == nil {
		t.Fatal("listed sandbox stashes")
	}
	if err = svc.StashPush("demo", wt, "blocked", sha); err == nil {
		t.Fatal("stashed sandbox")
	}
	if err = svc.StashApply("demo", wt, sha, sha, true); err == nil {
		t.Fatal("popped sandbox")
	}
	if err = svc.StashDrop("demo", wt, sha); err == nil {
		t.Fatal("dropped sandbox stash")
	}
}

func TestProjectGitCachedIdentitiesStayLocalAndHideRemoteImageURLs(t *testing.T) {
	root := projectGitRepo(t)
	writeTestConfig(t, minimalConfig+fmt.Sprintf("\n[[project]]\nname = \"demo\"\npath = %q\n", root))
	if out, err := exec.Command("git", "-C", root, "remote", "add", "origin", "https://github.com/team/repo.git").CombinedOutput(); err != nil {
		t.Fatalf("remote: %v %s", err, out)
	}
	bin := t.TempDir()
	marker := filepath.Join(bin, "called-gh")
	script := "#!/bin/sh\ntouch \"" + marker + "\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	svc := &ProjectGitService{}
	result, err := svc.CachedIdentities("demo", nil)
	if err != nil || len(result.Authors) != 0 || result.RemoteAvatars["origin"] != "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("cache read invoked gh")
	}
	if _, err = svc.CachedIdentities("demo", []string{"main"}); err == nil {
		t.Fatal("cache accepted non-hash revision")
	}
	if _, err = svc.CachedIdentities("missing", nil); err == nil {
		t.Fatal("cache accepted unconfigured project")
	}
	home := os.Getenv("LOLA_HOME")
	if err = os.MkdirAll(filepath.Join(home, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal([]session.Session{{ID: "sandbox", Worktree: root, Sandbox: "docker"}})
	if err = os.WriteFile(filepath.Join(home, "state", "sessions.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.CachedIdentities("demo", nil); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("cache sandbox error=%v", err)
	}
}

func TestProjectGitCreateBranchAuthorizesRequestedWorktree(t *testing.T) {
	root := projectGitRepo(t)
	other := projectGitRepo(t)
	writeTestConfig(t, minimalConfig+fmt.Sprintf("\n[[project]]\nname = \"demo\"\npath = %q\n", root))
	svc := &ProjectGitService{}
	output, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(output))
	if _, err = svc.CreateBranch("demo", other, "feature/foreign", sha, false); err == nil {
		t.Fatal("created in unrelated repository")
	}
	if _, err = svc.CreateBranch("missing", "", "feature/unknown", sha, false); err == nil {
		t.Fatal("created for unconfigured project")
	}
	result, err := svc.CreateBranch("demo", "", "feature/created", sha, false)
	if err != nil || canonicalPath(result.Path) != canonicalPath(root) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	wt := filepath.Join(t.TempDir(), "linked")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "-b", "linked", wt).CombinedOutput(); err != nil {
		t.Fatalf("worktree: %v %s", err, out)
	}
	result, err = svc.CreateBranch("demo", wt, "feature/in-linked", sha, true)
	if err != nil || canonicalPath(result.Path) != canonicalPath(wt) {
		t.Fatalf("linked result=%+v err=%v", result, err)
	}
	if out, err := exec.Command("git", "-C", root, "branch", "--show-current").Output(); err != nil || strings.TrimSpace(string(out)) != "main" {
		t.Fatal("creation changed main checkout")
	}
	home := os.Getenv("LOLA_HOME")
	if err = os.MkdirAll(filepath.Join(home, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal([]session.Session{{ID: "sandbox", Worktree: wt, Sandbox: "docker"}})
	if err = os.WriteFile(filepath.Join(home, "state", "sessions.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.CreateBranch("demo", wt, "feature/sandbox", sha, false); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("sandbox error=%v", err)
	}
}

func TestProjectGitDiscardUsesConfirmedAuthorizedCheckout(t *testing.T) {
	root := projectGitRepo(t)
	writeTestConfig(t, minimalConfig+fmt.Sprintf("\n[[project]]\nname = \"demo\"\npath = %q\n", root))
	path := filepath.Join(root, "untracked")
	if err := os.WriteFile(path, []byte("remove after confirmation"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := &ProjectGitService{}
	plan, err := svc.PrepareDiscard("demo", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.DiscardUnstaged("missing", "", plan.Token); err == nil {
		t.Fatal("accepted unknown project")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("preview or refused request removed file")
	}
	if err = svc.DiscardUnstaged("demo", "", plan.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file remains")
	}
}

func TestProjectGitIndexActionsReturnFreshFiles(t *testing.T) {
	root := projectGitRepo(t)
	writeTestConfig(t, minimalConfig+fmt.Sprintf("\n[[project]]\nname = \"demo\"\npath = %q\n", root))
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := &ProjectGitService{}
	files, err := svc.Stage("demo", "", []string{"new.txt"})
	if err != nil || len(files) != 1 || files[0].Index != "A" || files[0].Untracked {
		t.Fatalf("staged=%+v err=%v", files, err)
	}
	files, err = svc.Unstage("demo", "", []string{"new.txt"})
	if err != nil || len(files) != 1 || !files[0].Untracked {
		t.Fatalf("unstaged=%+v err=%v", files, err)
	}
	if _, err = svc.Stage("missing", "", []string{"new.txt"}); err == nil {
		t.Fatal("unknown project accepted")
	}
}

// A snapshot counts the uncommitted files of the OTHER checkouts, so the
// history can show work in progress on a branch checked out elsewhere. The
// checkout the snapshot was taken in lists its files instead, and a sandboxed
// checkout is never asked.
func TestProjectGitSnapshotCountsOtherWorktreeChanges(t *testing.T) {
	root := projectGitRepo(t)
	writeTestConfig(t, minimalConfig+fmt.Sprintf("\n[[project]]\nname = \"demo\"\npath = %q\n", root))
	wt := filepath.Join(t.TempDir(), "checkout")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "-b", "feature", wt).CombinedOutput(); err != nil {
		t.Fatalf("worktree: %v %s", err, out)
	}
	// One tracked file to modify and one to delete, committed on the feature
	// checkout, then two new files on top.
	for _, name := range []string{"keep.txt", "gone.txt"} {
		if err := os.WriteFile(filepath.Join(wt, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "Tracked"}} {
		if out, err := exec.Command("git", append([]string{"-C", wt}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(wt, "keep.txt"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(wt, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(wt, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	changes := func(snap projectgit.Snapshot, path string) int {
		t.Helper()
		for _, tree := range snap.Worktrees {
			if canonicalPath(tree.Path) == canonicalPath(path) {
				return tree.Changes
			}
		}
		t.Fatalf("worktree %s missing from %+v", path, snap.Worktrees)
		return 0
	}
	svc := &ProjectGitService{}
	snap, err := svc.Snapshot("demo", "", "", 200)
	if err != nil {
		t.Fatal(err)
	}
	if got := changes(snap, wt); got != 4 {
		t.Fatalf("other checkout changes = %d, want 4", got)
	}
	for _, tree := range snap.Worktrees {
		if canonicalPath(tree.Path) == canonicalPath(wt) && (tree.Added != 2 || tree.Modified != 1 || tree.Deleted != 1 || tree.Conflicts != 0) {
			t.Fatalf("split = +%d ~%d -%d !%d, want +2 ~1 -1 !0", tree.Added, tree.Modified, tree.Deleted, tree.Conflicts)
		}
	}
	if got := changes(snap, root); got != 0 {
		t.Fatalf("active checkout changes = %d, want 0 (its files are in Files)", got)
	}
	// Seen from the feature checkout, that checkout is the active one.
	if snap, err = svc.Snapshot("demo", wt, "", 200); err != nil {
		t.Fatal(err)
	}
	if got := changes(snap, wt); got != 0 || len(snap.Files) != 4 {
		t.Fatalf("active feature checkout: changes=%d files=%d", got, len(snap.Files))
	}
	home := os.Getenv("LOLA_HOME")
	if err = os.MkdirAll(filepath.Join(home, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal([]session.Session{{ID: "sandbox", Worktree: wt, Sandbox: "docker"}})
	if err = os.WriteFile(filepath.Join(home, "state", "sessions.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if snap, err = svc.Snapshot("demo", "", "", 200); err != nil {
		t.Fatal(err)
	}
	if got := changes(snap, wt); got != 0 {
		t.Fatalf("sandboxed checkout was asked: changes = %d", got)
	}
}
