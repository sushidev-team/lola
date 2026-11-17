package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/reviewprofile"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

func profileFixture(t *testing.T) (*Daemon, session.Session) {
	d, _, _ := goalFixture(t)
	d.cfg.ReviewProviders = []config.ReviewProvider{{Provider: "codex-session", Enabled: true}}
	d.cfg.ReviewProfiles = []reviewprofile.Profile{{ID: "backend", Kind: "backend", Provider: "codex-session", Version: "1", Enabled: true, Required: true, OnPROpen: true}}
	s := session.Session{ID: "session-1", Source: "native", Project: "p1", Worktree: "/fake", Repo: "owner/repo", Branch: "feature", AgentState: state.AgentIdle,
		PR: &scm.PR{Number: 1, State: "OPEN", HeadSHA: headA, BaseSHA: headB, BaseRef: "main"}}
	d.sessions.Upsert(s)
	d.profilePaths = func(context.Context, string, string, string) ([]string, error) {
		return []string{"internal/daemon/dispatch.go"}, nil
	}
	d.verifyReviewHead = func(context.Context, string, string) error { return nil }
	d.prForBranch = func(context.Context, string, string) (*scm.PR, error) { p := *s.PR; return &p, nil }
	d.profileRun = func(context.Context, string, reviewprofile.Profile, reviewprofile.Inputs) (reviewprofile.Result, error) {
		return reviewprofile.Result{Complete: true, Evidence: []reviewprofile.Evidence{{Criterion: "Atomic writes", Observation: "Persist happens before publishing"}}}, nil
	}
	return d, s
}

func TestProfilesPinHeadBaseAndIndependentIDs(t *testing.T) {
	d, s := profileFixture(t)
	if d.profileGatesOpen(s) {
		t.Fatal("missing required profile passed")
	}
	a := profileArgs{Session: s.ID, Profile: "backend", Contract: "No duplicate dispatch after restart"}
	r, err := d.runProfile(context.Background(), a)
	if err != nil || r.State != "pass" {
		t.Fatalf("review: %+v %v", r, err)
	}
	if !d.profileGatesOpen(s) {
		t.Fatal("completed review missing from gate")
	}
	p := d.cfg.ReviewProfiles[0]
	p.ID = "integration-boundary"
	d.cfg.ReviewProfiles = append(d.cfg.ReviewProfiles, p)
	if d.profileGatesOpen(s) {
		t.Fatal("same-provider profile reused another profile's verdict")
	}
	a.Profile = p.ID
	if _, err := d.runProfile(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if !d.profileGatesOpen(s) {
		t.Fatal("independent profiles failed to satisfy gate")
	}
	s.PR.BaseSHA = headC
	if d.profileGatesOpen(s) {
		t.Fatal("base drift reused stale evidence")
	}
}

func TestProfilesUnknownEvidenceAndStaleHeadHold(t *testing.T) {
	for _, mode := range []string{"missing-artifacts", "empty-result", "failed-provider", "changed-head"} {
		t.Run(mode, func(t *testing.T) {
			d, s := profileFixture(t)
			calls := 0
			d.profileRun = func(context.Context, string, reviewprofile.Profile, reviewprofile.Inputs) (reviewprofile.Result, error) {
				calls++
				if mode == "failed-provider" {
					return reviewprofile.Result{}, errors.New("timeout")
				}
				if mode == "changed-head" {
					d.prForBranch = func(context.Context, string, string) (*scm.PR, error) { p := *s.PR; p.HeadSHA = headD; return &p, nil }
				}
				if mode == "empty-result" {
					return reviewprofile.Result{}, nil
				}
				return reviewprofile.Result{Complete: true, Evidence: []reviewprofile.Evidence{{Criterion: "C", Observation: "O"}}}, nil
			}
			if mode == "missing-artifacts" {
				d.cfg.ReviewProfiles[0].Kind = "uiux"
			}
			r, err := d.runProfile(context.Background(), profileArgs{Session: s.ID, Profile: "backend", Contract: "explicit acceptance"})
			if err != nil {
				t.Fatal(err)
			}
			if r.State == "pass" || d.profileGatesOpen(s) {
				t.Fatalf("unverified result passed: %+v", r)
			}
			if mode == "missing-artifacts" && (calls != 0 || !strings.Contains(r.Error, "missing")) {
				t.Fatal("paid call made without required evidence")
			}
		})
	}
}

func TestProfileQueueUsesSeparateBusyKeys(t *testing.T) {
	d, s := profileFixture(t)
	p := d.cfg.ReviewProfiles[0]
	p.ID = "second"
	d.cfg.ReviewProfiles = append(d.cfg.ReviewProfiles, p)
	d.queueProfileReviews(s)
	d.queueProfileReviews(s)
	if len(d.reviewCh) != 2 {
		t.Fatalf("want one job per profile, got %d", len(d.reviewCh))
	}
	if !d.reviewBusy[profileBusyKey(s.ID, "backend")] || !d.reviewBusy[profileBusyKey(s.ID, "second")] {
		t.Fatal("profile guards collided")
	}
}

func TestProfileHumanContractEditInvalidatesAndRequeues(t *testing.T) {
	d, s := profileFixture(t)
	s.IssueUUID = "issue-1"
	d.sessions.Upsert(s)
	f := d.lin.(*linear.Fake)
	f.Details = []linear.IssueDetail{{ID: s.IssueUUID, Title: "Atomic save", Description: "No duplicate dispatch after restart"}}
	r, err := d.runProfile(context.Background(), profileArgs{Session: s.ID, Profile: "backend"})
	if err != nil || r.State != "pass" {
		t.Fatalf("initial result: %+v %v", r, err)
	}
	if !d.profileContractsCurrent(context.Background(), s) {
		t.Fatal("unchanged contract rejected")
	}
	f.Details[0].Description = "Also preserve ownership during cancellation"
	if d.profileContractsCurrent(context.Background(), s) {
		t.Fatal("changed contract reused old pass")
	}
	if d.profileGatesOpen(s) {
		t.Fatal("invalidated result still opened gate")
	}
	d.queueProfileReviews(s)
	if len(d.reviewCh) != 1 {
		t.Fatal("invalidated profile not requeued")
	}
	d.runQueuedReview(context.Background(), <-d.reviewCh)
	if !d.profileGatesOpen(s) {
		t.Fatal("new contract review failed to satisfy gate")
	}
}
