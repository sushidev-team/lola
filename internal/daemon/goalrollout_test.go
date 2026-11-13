package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/reviewprofile"
)

func TestGoalPRIncludesReviewedRolloutAndUsesItsOrder(t *testing.T) {
	p := goalpolicy.Policy{Members: []goalpolicy.Member{
		{ID: "web", Repo: "owner/web", Target: "main"},
		{ID: "api", Repo: "owner/api", Target: "main"},
	}, Rollout: &goalpolicy.Rollout{Compatibility: []string{"Keep the previous web client compatible until its merge."}, MergeOrder: []string{"api", "web"}}}
	g := goal.Goal{ID: "goal", Execution: &goal.Execution{Policy: p, Manifest: map[string]string{"api": strings.Repeat("a", 40), "web": strings.Repeat("b", 40)}}}
	v := goalwork.Verification{Targets: map[string]string{"api": strings.Repeat("c", 40)}}
	body := goalPRBody(g, v)
	for _, want := range []string{p.Rollout.Compatibility[0], "1. [api]", "2. [web]", "skip unchanged members", "No automatic merge", strings.Repeat("c", 40)} {
		if !strings.Contains(body, want) {
			t.Fatalf("PR omitted %q", want)
		}
	}
	if strings.Index(body, "1. [api]") > strings.Index(body, "2. [web]") {
		t.Fatal("configured order replaced reviewed order")
	}
	g.Execution.Policy.Rollout = nil
	legacy := goalPRBody(g, v)
	if !strings.Contains(legacy, "No reviewed rollout contract") || !strings.Contains(legacy, "not a merge recommendation") || strings.Contains(legacy, "proposed human merge order") {
		t.Fatal("legacy contract invented review authority")
	}
}

func TestGoalSpecialistReceivesAcceptedRolloutAndBindsItToEvidence(t *testing.T) {
	d, g, _, _ := submittedGoalFixture(t, func(p *goalpolicy.Policy) {
		p.PublishPR = true
		p.Rollout = &goalpolicy.Rollout{Compatibility: []string{"Keep the previous client's API requests working."}, MergeOrder: []string{"p1"}}
	})
	original := d.profileRun
	var inspected bool
	d.profileRun = func(ctx context.Context, dir string, p reviewprofile.Profile, in reviewprofile.Inputs) (reviewprofile.Result, error) {
		inspected = true
		if !strings.Contains(in.Contract, g.Execution.Policy.RolloutContract()) {
			t.Fatal("reviewer did not see accepted rollout")
		}
		changed := in
		changed.Contract = strings.ReplaceAll(in.Contract, "Keep the previous client's API requests working.", "Permit breaking previous clients.")
		if p.Key(changed) == p.Key(in) {
			t.Fatal("rollout change reused specialist evidence")
		}
		return original(ctx, dir, p, in)
	}
	if err := d.verifyGoalIntegration(context.Background(), g.ID, g.Execution.PendingIntegration); err != nil {
		t.Fatal(err)
	}
	if !inspected {
		t.Fatal("test did not reach independent reviewer")
	}
}

func TestGoalRepairPlannerReceivesAcceptedRollout(t *testing.T) {
	d, g, _, _ := submittedGoalFixture(t, func(p *goalpolicy.Policy) {
		p.PublishPR, p.Replan = true, true
		p.Limits.AttemptsPerTask = 1
		p.Rollout = &goalpolicy.Rollout{Compatibility: []string{"Preserve previous API clients through deployment."}, MergeOrder: []string{"p1"}}
	})
	if err := d.repairGoal(context.Background(), g.ID, g.Execution.PendingIntegration, "Independent test reproduced a missing compatibility case"); err != nil {
		t.Fatal(err)
	}
	g, _ = d.goals.Get(g.ID)
	if err := g.ReserveReplan(g.ReplanTask(), goal.NewID(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	r := g.Execution.Replans[g.Execution.PendingReplan]
	in, err := d.goalRepairInput(context.Background(), g, r)
	if err != nil {
		t.Fatal(err)
	}
	if in.Repair == nil || !strings.Contains(in.Repair.Original.Contract, g.Execution.Policy.RolloutContract()) || in.Repair.PolicyDigest != g.Execution.PolicyDigest {
		t.Fatal("repair planning lost accepted rollout contract")
	}
}

func TestGoalExtendedPolicyRequiresVersionedAuthorization(t *testing.T) {
	for _, mode := range []string{"rollout", "human-acceptance"} {
		t.Run(mode, func(t *testing.T) {
			d, _, g, p, launched := goalExecutionFixture(t)
			p.PublishPR = true
			if mode == "rollout" {
				p.Rollout = &goalpolicy.Rollout{Compatibility: []string{"Keep old clients working."}, MergeOrder: []string{"p1"}}
			} else {
				p.RequireHumanAcceptance = true
			}
			args, _ := json.Marshal(goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: p})
			old := d.handle(context.Background(), protocol.Request{Cmd: "goalStart", Args: args})
			if old.OK || !strings.Contains(old.Error, "goalStartV2") {
				t.Fatal("extended policy accepted through legacy command", old)
			}
			unchanged, _ := d.goals.Get(g.ID)
			if unchanged.Revision != g.Revision || unchanged.Execution != nil || *launched != 0 {
				t.Fatal("rejected legacy command authorized work")
			}
			current := d.handle(context.Background(), protocol.Request{Cmd: protocol.GoalStartV2, Args: args})
			if !current.OK {
				t.Fatal(current.Error)
			}
			accepted, _ := d.goals.Get(g.ID)
			wantDigest, _ := p.Digest()
			if accepted.Execution == nil || accepted.Execution.PolicyDigest != wantDigest || *launched != 0 {
				t.Fatal("versioned authorization lost reviewed policy or launched before preparation")
			}
		})
	}
}
