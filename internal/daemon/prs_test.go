package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
)

func prsArgs(t *testing.T, a protocol.PrsArgs) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func prsDaemon(t *testing.T) *Daemon {
	cfg := &config.Config{
		Defaults: config.Defaults{ConcurrencyCap: 10, GlobalCap: 10},
		Projects: []config.Project{
			{Name: "alpha", Path: "/tmp/alpha", Repo: "acme/alpha", DefaultBranch: "main"},
			{Name: "norepo", Path: "/tmp/norepo", DefaultBranch: "main"},
		},
	}
	return newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})
}

func TestHandlePrsMapsAndDecorates(t *testing.T) {
	d := prsDaemon(t)
	d.listOpenPRs = func(_ context.Context, repo string) ([]scm.OpenPR, error) {
		if repo != "acme/alpha" {
			t.Errorf("listed wrong repo %q", repo)
		}
		return []scm.OpenPR{
			{Number: 229, Title: "fix oauth", Author: "mreit", Branch: "fix/oauth", Checks: "pass", Review: "APPROVED", Status: "approved"},
			{Number: 240, Title: "fork fix", Author: "ext", Branch: "patch-1", IsFork: true, Checks: "pass", Status: "review_pending"},
		}, nil
	}
	// A live session already holds fix/oauth.
	d.sessions.Upsert(session.Session{ID: "s1", Source: "native", Project: "alpha", Branch: "fix/oauth", Status: "working"})

	data, err := d.handlePrs(context.Background(), prsArgs(t, protocol.PrsArgs{Project: "alpha"}))
	if err != nil {
		t.Fatalf("handlePrs: %v", err)
	}
	if data.Repo != "acme/alpha" || len(data.PRs) != 2 {
		t.Fatalf("got repo=%q prs=%d", data.Repo, len(data.PRs))
	}
	if !data.PRs[0].AlreadyOpen {
		t.Error("PR on a held branch must be flagged AlreadyOpen")
	}
	if !data.PRs[1].IsFork {
		t.Error("fork PR must be flagged")
	}
	if data.PRs[0].Status != "approved" {
		t.Errorf("status not carried: %q", data.PRs[0].Status)
	}
}

func TestHandlePrsNoRepoFailsClosed(t *testing.T) {
	d := prsDaemon(t)
	d.listOpenPRs = func(context.Context, string) ([]scm.OpenPR, error) {
		t.Fatal("must not exec gh when the project has no repo")
		return nil, nil
	}
	_, err := d.handlePrs(context.Background(), prsArgs(t, protocol.PrsArgs{Project: "norepo"}))
	if err == nil {
		t.Fatal("a project with no repo must error, not return an empty list")
	}
}

func TestHandlePrsUnknownProject(t *testing.T) {
	d := prsDaemon(t)
	if _, err := d.handlePrs(context.Background(), prsArgs(t, protocol.PrsArgs{Project: "ghost"})); err == nil {
		t.Fatal("unknown project must error")
	}
}

func TestHandlePrsCachesAndRefresh(t *testing.T) {
	d := prsDaemon(t)
	var calls atomic.Int32
	d.listOpenPRs = func(context.Context, string) ([]scm.OpenPR, error) {
		calls.Add(1)
		return []scm.OpenPR{{Number: 1, Branch: "b"}}, nil
	}
	args := prsArgs(t, protocol.PrsArgs{Project: "alpha"})

	if _, err := d.handlePrs(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if _, err := d.handlePrs(context.Background(), args); err != nil { // served from cache
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected 1 gh exec (2nd served from cache), got %d", got)
	}

	// Explicit refresh bypasses the TTL.
	if _, err := d.handlePrs(context.Background(), prsArgs(t, protocol.PrsArgs{Project: "alpha", Refresh: true})); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("refresh should re-exec gh, got %d calls", got)
	}
}

func TestHandlePrsGhErrorSurfacedWithoutCache(t *testing.T) {
	d := prsDaemon(t)
	d.listOpenPRs = func(context.Context, string) ([]scm.OpenPR, error) {
		return nil, errors.New("gh auth required")
	}
	if _, err := d.handlePrs(context.Background(), prsArgs(t, protocol.PrsArgs{Project: "alpha"})); err == nil {
		t.Fatal("a gh error with no prior cache must surface, not read as no-PRs")
	}
}
