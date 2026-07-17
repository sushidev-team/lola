package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
)

func findProject(t *testing.T, data protocol.ProjectsData, name string) protocol.ProjectInfo {
	t.Helper()
	for _, p := range data.Projects {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no project %q in %+v", name, data.Projects)
	return protocol.ProjectInfo{}
}

// TestProjectsDataRollup pins the cmd=projects join: poll counts per project,
// per-project session rollups (total / live-counted / needs-you / ci-red /
// open-PRs), and per-project agent health — all from in-memory snapshots.
func TestProjectsDataRollup(t *testing.T) {
	// alpha polls (enabled); beta has no polling and no repo. A project has at
	// most one polling config now.
	alphaP := labelPoll("alpha")
	alphaP.Repo = "acme/alpha"
	alphaP.Path = "/tmp/alpha"
	alphaP.Enabled = true

	cfg := &config.Config{
		Defaults: config.Defaults{ConcurrencyCap: 10, GlobalCap: 10},
		Projects: []config.Project{
			alphaP,
			{Name: "beta", Path: "/tmp/beta", DefaultBranch: "main"}, // no repo, no poll
		},
	}
	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})

	lin := func(id, project, status string) session.Session {
		return session.Session{ID: id, Source: "native", Kind: session.KindLinear, Project: project, IssueUUID: "u-" + id, Status: status}
	}
	d.sessions.Upsert(lin("s1", "alpha", "working"))     // live
	d.sessions.Upsert(lin("s2", "alpha", "needs_input")) // live + needsYou
	d.sessions.Upsert(lin("s3", "alpha", "ci_failed"))   // live + ciRed
	d.sessions.Upsert(lin("s4", "alpha", "merged"))      // not counted
	withPR := lin("s6", "alpha", "working")
	withPR.PR = &scm.PR{State: "OPEN", Number: 5}
	d.sessions.Upsert(withPR) // live + openPR
	d.sessions.Upsert(session.Session{ID: "s5", Source: "native", Kind: session.KindPR, Agentless: true, Project: "beta", Status: "shell"})

	data := d.projectsData(context.Background())

	alpha := findProject(t, data, "alpha")
	if alpha.PollCount != 1 || alpha.PollsEnabled != 1 {
		t.Errorf("alpha polls: count=%d enabled=%d, want 1/1", alpha.PollCount, alpha.PollsEnabled)
	}
	if alpha.Sessions != 5 {
		t.Errorf("alpha sessions = %d, want 5", alpha.Sessions)
	}
	if alpha.LiveCounted != 4 {
		t.Errorf("alpha liveCounted = %d, want 4 (working, needs_input, ci_failed, working+PR; merged excluded)", alpha.LiveCounted)
	}
	if alpha.NeedsYou != 1 || alpha.CIRed != 1 || alpha.OpenPRs != 1 {
		t.Errorf("alpha attention: needsYou=%d ciRed=%d openPrs=%d, want 1/1/1", alpha.NeedsYou, alpha.CIRed, alpha.OpenPRs)
	}
	if !alpha.RepoConfigured || !alpha.AgentOK {
		t.Errorf("alpha RepoConfigured=%v AgentOK=%v, want true/true", alpha.RepoConfigured, alpha.AgentOK)
	}
	if alpha.Agent != "claude" || alpha.AgentBin == "" {
		t.Errorf("alpha agent=%q bin=%q, want claude + a resolved bin", alpha.Agent, alpha.AgentBin)
	}

	beta := findProject(t, data, "beta")
	if beta.PollCount != 0 || beta.Sessions != 1 || beta.LiveCounted != 0 {
		t.Errorf("beta polls=%d sessions=%d live=%d, want 0/1/0 (shell not counted)", beta.PollCount, beta.Sessions, beta.LiveCounted)
	}
	if beta.RepoConfigured {
		t.Errorf("beta has no repo; RepoConfigured must be false")
	}
}

func TestProjectPathOK(t *testing.T) {
	if projectPathOK("") {
		t.Error("empty path must not be OK")
	}
	if projectPathOK("/no/such/path/xyz") {
		t.Error("missing path must not be OK")
	}

	// A dir without .git is not a checkout.
	bare := t.TempDir()
	if projectPathOK(bare) {
		t.Error("a dir without .git must not be OK")
	}

	// A dir with a .git entry is a checkout.
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !projectPathOK(repo) {
		t.Error("a dir with .git must be OK")
	}
}
