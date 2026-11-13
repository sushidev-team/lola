package projectgit

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPublishNamedDestinationAndTrackWithoutSwitching(t *testing.T) {
	dir := actionRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	testGit(t, dir, "init", "--bare", remote)
	testGit(t, dir, "remote", "add", "origin", remote)
	testGit(t, dir, "branch", "feature/local")
	write(t, dir, "base", "dirty\n")
	c := Client{}
	r := BranchRequest{Action: "publish", Source: "refs/heads/feature/local", SourceSHA: tip(t, dir, "HEAD"), Remote: "origin", RemoteBranch: "team/published"}
	if err := c.BranchAction(context.Background(), dir, r); err != nil {
		t.Fatal(err)
	}
	if tip(t, remote, "refs/heads/team/published") != r.SourceSHA {
		t.Fatal("wrong destination")
	}
	if got := testGit(t, dir, "config", "--get", "branch.feature/local.merge"); got != "refs/heads/team/published" {
		t.Fatal(got)
	}
	if testGit(t, dir, "branch", "--show-current") != "main" || testGit(t, dir, "diff") == "" {
		t.Fatal("publish changed working copy")
	}
	s, err := c.Snapshot(context.Background(), dir, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ref := range s.Refs {
		if ref.Name == "feature/local" {
			found = ref.Upstream == "refs/remotes/origin/team/published"
		}
	}
	if !found {
		t.Fatal("snapshot did not expose branch upstream")
	}
}

func TestPublishRejectsBadDestinationStaleAndDivergent(t *testing.T) {
	dir := actionRepo(t)
	remote := actionRepo(t)
	testGit(t, dir, "remote", "add", "origin", remote)
	c := Client{}
	ctx := context.Background()
	r := BranchRequest{Action: "publish", Source: "refs/heads/main", SourceSHA: tip(t, dir, "HEAD"), Remote: "origin", RemoteBranch: "new"}
	for _, name := range []string{"", "-bad", "refs/heads/bad", "HEAD", "bad:other", "a..b", "@{-1}"} {
		bad := r
		bad.RemoteBranch = name
		if err := c.BranchAction(ctx, dir, bad); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	bad := r
	bad.Remote = remote
	if err := c.BranchAction(ctx, dir, bad); err == nil {
		t.Fatal("accepted arbitrary remote URL")
	}
	bad = r
	bad.SourceSHA = "ffffffffffffffffffffffffffffffffffffffff"
	if err := c.BranchAction(ctx, dir, bad); err == nil {
		t.Fatal("accepted stale tip")
	}
	write(t, remote, "base", "divergent\n")
	testGit(t, remote, "add", "base")
	testGit(t, remote, "commit", "-m", "diverge")
	remoteHead := tip(t, remote, "HEAD")
	testGit(t, remote, "branch", "destination")
	r.RemoteBranch = "destination"
	if err := c.BranchAction(ctx, dir, r); err == nil {
		t.Fatal("overwrote divergent branch")
	}
	if tip(t, remote, "destination") != remoteHead {
		t.Fatal("remote moved")
	}
	if got, _ := c.text(ctx, dir, "config", "--get", "branch.main.remote"); got != "" {
		t.Fatal("failed publish set upstream", got)
	}
}

func TestSetUpstreamDoesNotPublishOrSwitch(t *testing.T) {
	dir := actionRepo(t)
	remote := actionRepo(t)
	c := Client{}
	ctx := context.Background()
	testGit(t, remote, "branch", "review/topic")
	testGit(t, dir, "remote", "add", "origin", remote)
	testGit(t, dir, "fetch", "origin")
	testGit(t, dir, "branch", "local-topic")
	before := tip(t, remote, "review/topic")
	r := BranchRequest{Action: "set-upstream", Source: "refs/heads/local-topic", SourceSHA: tip(t, dir, "local-topic"), Target: "refs/remotes/origin/review/topic", TargetSHA: tip(t, dir, "origin/review/topic")}
	write(t, dir, "base", "keep WIP\n")
	if err := c.BranchAction(ctx, dir, r); err != nil {
		t.Fatal(err)
	}
	if testGit(t, dir, "config", "--get", "branch.local-topic.merge") != "refs/heads/review/topic" {
		t.Fatal("wrong upstream")
	}
	if tip(t, remote, "review/topic") != before || testGit(t, dir, "branch", "--show-current") != "main" || testGit(t, dir, "diff") == "" {
		t.Fatal("tracking changed remote or checkout")
	}
	r.TargetSHA = "ffffffffffffffffffffffffffffffffffffffff"
	if err := c.BranchAction(ctx, dir, r); err == nil {
		t.Fatal("accepted changed tracking target")
	}
}
