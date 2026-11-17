package daemon

import (
	"context"
	"testing"

	"github.com/sushidev-team/lola/internal/reviewprofile"
)

func TestProfileModelChangeRequiresFreshEvidence(t *testing.T) {
	d, s := profileFixture(t)
	a := profileArgs{Session: s.ID, Profile: "backend", Contract: "No lost writes"}
	d.cfg.ReviewProviders[0].Model = "model-a"
	r, err := d.runProfile(context.Background(), a)
	if err != nil || !d.profileGatesOpen(s) {
		t.Fatal("initial review", err)
	}
	if r.Inputs.Invocation == nil || r.Inputs.Invocation.RequestedModel != "model-a" {
		t.Fatal("requested model not preserved")
	}
	d.cfg.ReviewProviders[0].Model = "model-b"
	if d.profileGatesOpen(s) {
		t.Fatal("model change reused old verdict")
	}
	next, err := d.runProfile(context.Background(), a)
	if err != nil || next.Key == r.Key || !d.profileGatesOpen(s) {
		t.Fatal("new model did not receive fresh review", err)
	}
	if len(d.profileReviews.List()) != 2 {
		t.Fatal("model change erased original evidence")
	}
	// A reload while the paid pass is in flight must not certify the old request
	// under the newly selected model.
	d.cfg.ReviewProviders[0].Model = "model-c"
	d.profileRun = func(context.Context, string, reviewprofile.Profile, reviewprofile.Inputs) (reviewprofile.Result, error) {
		d.cfg.ReviewProviders[0].Model = "model-d"
		return r.Result, nil
	}
	changed, err := d.runProfile(context.Background(), a)
	if err != nil || changed.State != "error" || d.profileGatesOpen(s) {
		t.Fatalf("changed invocation passed: %+v %v", changed, err)
	}
}

func TestProfileLegacyVerdictDoesNotInventCurrentProvenance(t *testing.T) {
	d, s := profileFixture(t)
	p := d.cfg.ReviewProfiles[0]
	in := reviewprofile.Inputs{Repo: s.Repo, Head: s.PR.HeadSHA, Base: s.PR.BaseSHA, Contract: "No lost writes"}
	r, err := d.profileReviews.Begin(p, in, false)
	if err != nil {
		t.Fatal(err)
	}
	r.State, r.Result = "pass", reviewprofile.Result{Complete: true, Evidence: []reviewprofile.Evidence{{Criterion: "Atomicity", Observation: "All writes protected"}}}
	if err = d.profileReviews.Finish(r); err != nil {
		t.Fatal(err)
	}
	if d.profileGatesOpen(s) {
		t.Fatal("legacy verdict gained current model/prompt attribution")
	}
	if _, ok := d.profileReviews.Get(r.Key); !ok {
		t.Fatal("legacy result was deleted")
	}
}
