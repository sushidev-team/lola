package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitdiff"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/projectgit"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"config", "commit.gpgsign", "false"},
		{"commit", "-q", "--allow-empty", "-m", "first"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func gitReq(t *testing.T, cmd string, a protocol.ProjectGitArgs) protocol.Request {
	t.Helper()
	raw, _ := json.Marshal(a)
	return protocol.Request{Cmd: cmd, Args: raw}
}

func TestProjectGitStageAndCommitFromAPhone(t *testing.T) {
	repo := gitRepo(t)
	d := newTestDaemon(t, &config.Config{Projects: []config.Project{{Name: "p", Path: repo}}}, &linear.Fake{}, nil)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := d.handle(ctx, gitReq(t, protocol.CmdProjectGit, protocol.ProjectGitArgs{Op: "snapshot", Project: "p", Limit: 10}))
	if !resp.OK {
		t.Fatalf("snapshot: %s", resp.Error)
	}
	var snap projectgit.Snapshot
	_ = json.Unmarshal(resp.Data, &snap)
	if len(snap.Files) != 1 || snap.Branch != "main" {
		t.Fatalf("snapshot = %+v", snap)
	}

	if resp = d.handle(ctx, gitReq(t, protocol.CmdProjectGitAct, protocol.ProjectGitArgs{Op: "stage", Project: "p", Paths: []string{"a.txt"}})); !resp.OK {
		t.Fatalf("stage: %s", resp.Error)
	}
	if resp = d.handle(ctx, gitReq(t, protocol.CmdProjectGitAct, protocol.ProjectGitArgs{Op: "commit", Project: "p", Message: "add a"})); !resp.OK {
		t.Fatalf("commit: %s", resp.Error)
	}
	resp = d.handle(ctx, gitReq(t, protocol.CmdProjectGit, protocol.ProjectGitArgs{Op: "snapshot", Project: "p", Limit: 10}))
	_ = json.Unmarshal(resp.Data, &snap)
	if len(snap.Files) != 0 || len(snap.Commits) != 2 {
		t.Fatalf("after commit: files=%d commits=%d", len(snap.Files), len(snap.Commits))
	}
}

func TestProjectGitRefusesWhatItShouldNot(t *testing.T) {
	repo := gitRepo(t)
	d := newTestDaemon(t, &config.Config{Projects: []config.Project{{Name: "p", Path: repo}}}, &linear.Fake{}, nil)
	ctx := context.Background()
	cases := []struct {
		name string
		req  protocol.Request
	}{
		{"write op through the read command", gitReq(t, protocol.CmdProjectGit, protocol.ProjectGitArgs{Op: "commit", Project: "p"})},
		{"discard through the control command", gitReq(t, protocol.CmdProjectGitAct, protocol.ProjectGitArgs{Op: "discard", Project: "p"})},
		{"unknown project", gitReq(t, protocol.CmdProjectGit, protocol.ProjectGitArgs{Op: "snapshot", Project: "nope"})},
		{"relative worktree", gitReq(t, protocol.CmdProjectGit, protocol.ProjectGitArgs{Op: "snapshot", Project: "p", Worktree: "../x"})},
		{"pop through the control command", gitReq(t, protocol.CmdProjectGitAct, protocol.ProjectGitArgs{Op: "stashApply", Project: "p", Pop: true})},
		{"plain apply through the discard command", gitReq(t, protocol.CmdProjectGitDiscard, protocol.ProjectGitArgs{Op: "stashApply", Project: "p"})},
		{"foreign worktree", gitReq(t, protocol.CmdProjectGit, protocol.ProjectGitArgs{Op: "snapshot", Project: "p", Worktree: t.TempDir()})},
	}
	for _, c := range cases {
		if resp := d.handle(ctx, c.req); resp.OK {
			t.Errorf("%s: accepted", c.name)
		}
	}
}

func TestProjectGitRefusesASecondConcurrentWrite(t *testing.T) {
	repo := gitRepo(t)
	d := newTestDaemon(t, &config.Config{Projects: []config.Project{{Name: "p", Path: repo}}}, &linear.Fake{}, nil)
	if err := os.WriteFile(filepath.Join(repo, "a"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stage := gitReq(t, protocol.CmdProjectGitAct, protocol.ProjectGitArgs{Op: "stage", Project: "p", Paths: []string{"a"}})
	d.projectGitMu.Lock()
	resp := d.handle(context.Background(), stage)
	d.projectGitMu.Unlock()
	if resp.OK || !strings.Contains(resp.Error, "another") {
		t.Fatalf("second write while one runs: ok=%v err=%q", resp.OK, resp.Error)
	}
	if resp := d.handle(context.Background(), stage); !resp.OK {
		t.Fatalf("stage after unlock: %s", resp.Error)
	}
}

func TestProjectGitDiffReplyFitsOneFrame(t *testing.T) {
	big := strings.Repeat("x", 300<<10)
	res := gitdiff.Result{Files: []gitdiff.File{{Path: "a", Patch: big}, {Path: "b", Patch: big}, {Path: "c", Patch: big}}}
	out := fitDiffToFrame(res)
	raw, _ := json.Marshal(out)
	if len(raw) >= protocol.MaxFrameBytes {
		t.Fatalf("reply is %d bytes, frame cap %d", len(raw), protocol.MaxFrameBytes)
	}
	if !out.Truncated || !out.Files[2].TooLarge || out.Files[0].Patch == "" {
		t.Fatalf("expected only the last patch dropped: %+v", out.Truncated)
	}
}

func TestProjectGitBlockedResolvesNativeSessionWorktrees(t *testing.T) {
	d := newTestDaemon(t, &config.Config{}, &linear.Fake{}, nil)
	home := gitCanonical(d.home)
	host := filepath.Join(home, "worktrees", "p", "p-1")
	boxed := filepath.Join(home, "worktrees", "p", "p-2")
	for _, p := range []string{host, boxed} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	d.sessions.Upsert(session.Session{ID: "p-1", Project: "p", Source: "native"})
	d.sessions.Upsert(session.Session{ID: "p-2", Project: "p", Source: "native", Sandbox: "docker"})
	if d.projectGitBlocked(host) {
		t.Error("a live host worktree with no stored path was refused")
	}
	if !d.projectGitBlocked(boxed) {
		t.Error("a sandboxed worktree with no stored path was allowed")
	}
	if !d.projectGitBlocked(filepath.Join(home, "worktrees", "p", "gone")) {
		t.Error("an unknown managed checkout was allowed")
	}
}

func TestProjectGitRefusesBranchCheckout(t *testing.T) {
	repo := gitRepo(t)
	d := newTestDaemon(t, &config.Config{Projects: []config.Project{{Name: "p", Path: repo}}}, &linear.Fake{}, nil)
	resp := d.handle(context.Background(), gitReq(t, protocol.CmdProjectGitAct, protocol.ProjectGitArgs{Op: "createBranch", Project: "p", Name: "x", Checkout: true}))
	if resp.OK || !strings.Contains(resp.Error, "only available on the Mac") {
		t.Fatalf("checkout accepted: ok=%v err=%q", resp.OK, resp.Error)
	}
	if out, err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "-q", "x").CombinedOutput(); err == nil {
		t.Fatalf("branch was created anyway: %s", out)
	}
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	resp = d.handle(context.Background(), gitReq(t, protocol.CmdProjectGitAct, protocol.ProjectGitArgs{Op: "createBranch", Project: "p", Name: "x", SHA: strings.TrimSpace(string(head))}))
	if !resp.OK {
		t.Fatalf("plain createBranch refused: %s", resp.Error)
	}
}

func TestProjectGitPopNeedsTheDiscardTier(t *testing.T) {
	repo := gitRepo(t)
	d := newTestDaemon(t, &config.Config{Projects: []config.Project{{Name: "p", Path: repo}}}, &linear.Fake{}, nil)
	for _, c := range []struct {
		cmd string
		pop bool
	}{{protocol.CmdProjectGitAct, true}, {protocol.CmdProjectGitDiscard, false}} {
		resp := d.handle(context.Background(), gitReq(t, c.cmd, protocol.ProjectGitArgs{Op: "stashApply", Project: "p", Pop: c.pop}))
		if resp.OK || !strings.Contains(resp.Error, "discard tier") {
			t.Errorf("%s pop=%v: ok=%v err=%q", c.cmd, c.pop, resp.OK, resp.Error)
		}
	}
}
