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
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"config", "commit.gpgsign", "false"},
		{"commit", "-q", "--allow-empty", "-m", "first"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v %s", err, out)
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
	d.projectGitMu.Lock()
	defer d.projectGitMu.Unlock()
	resp := d.handle(context.Background(), gitReq(t, protocol.CmdProjectGitAct, protocol.ProjectGitArgs{Op: "stage", Project: "p", Paths: []string{"a"}}))
	if resp.OK {
		t.Fatal("second write was accepted while one is running")
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
