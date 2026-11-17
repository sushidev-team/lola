package goal

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goalpolicy"
)

func TestOverviewRetainsRolloutWithoutAliasingAcceptedPolicy(t *testing.T) {
	g := executable(t)
	g.Execution.Policy.Rollout = &goalpolicy.Rollout{Compatibility: []string{"Keep old clients working."}, MergeOrder: []string{"api"}}
	before, _ := json.Marshal(g)
	v := g.Overview(time.Now())
	if !reflect.DeepEqual(v.Rollout, g.Execution.Policy.Rollout) {
		t.Fatal("overview lost accepted rollout")
	}
	v.Rollout.Compatibility[0], v.Rollout.MergeOrder[0] = "changed", "changed"
	after, _ := json.Marshal(g)
	if string(before) != string(after) {
		t.Fatal("overview aliases accepted policy")
	}
}

func TestOverviewIsReadOnlyAndDoesNotExposePrivatePolicy(t *testing.T) {
	g := executable(t)
	g.State = "paused"
	taskID := g.Tasks[0].ID
	g.Execution.Tasks[taskID] = TaskProgress{State: "blocked"}
	before, _ := json.Marshal(g)
	view := g.Overview(time.Now())
	after, _ := json.Marshal(g)
	if string(before) != string(after) {
		t.Fatal("rendering mutated execution")
	}
	if !slices.Contains(view.Actions, "resume") {
		t.Fatal("expected resumable paused goal")
	}
	b, _ := json.Marshal(view)
	if strings.Contains(string(b), "/private/source") || strings.Contains(string(b), "make test") {
		t.Fatal("overview leaks private policy")
	}
	if !view.Authorized || view.Coverage.Runs != 0 || view.TokenLimit == 0 {
		t.Fatal("missing budget context")
	}
	g.State = "canceled"
	if len(g.Overview(time.Now()).Actions) != 0 {
		t.Fatal("canceled goal offers steering")
	}
	g.State = "blocked"
	g.Execution.ObservedTokens = g.Execution.Policy.Limits.Tokens
	if slices.Contains(g.Overview(time.Now()).Actions, "resume") {
		t.Fatal("exhausted goal offers resume")
	}
}
func TestOverviewIncludesAttemptAndVerifiedDelivery(t *testing.T) {
	g := executable(t)
	task := g.Tasks[0].ID
	g.Execution.Tasks[task] = TaskProgress{State: "integrated", AttemptIDs: []string{"a"}}
	g.Execution.Attempts["a"] = Attempt{ID: "a", SessionID: "session", State: "integrated", Summary: "Verified flow"}
	g.Execution.Final = &FinalVerification{Receipt: "evidence.json"}
	g.Execution.Delivery = &Delivery{Members: map[string]DeliveryMember{"api": {URL: "https://github.com/a/b/pull/2", Number: 2, State: "published"}}}
	v := g.Overview(time.Now())
	if !v.Verified || v.Members[0].PRNumber != 2 || v.Tasks[0].Attempts[0].SessionID != "session" {
		t.Fatal("deliverable lost")
	}
	if !reflect.DeepEqual(v.Acceptance, g.Acceptance) {
		t.Fatal("criteria missing")
	}
	v.Acceptance[0] = "changed"
	if v.Acceptance[0] == g.Acceptance[0] {
		t.Fatal("view aliases criteria")
	}
}

func TestOverviewOffersHumanAcceptanceOnlyForDeliveredRevision(t *testing.T) {
	g := humanAcceptanceGoal(t)
	before, _ := json.Marshal(g)
	v := g.Overview(time.Now())
	after, _ := json.Marshal(g)
	if !v.RequireHumanAcceptance || !v.Delivered || v.HumanAcceptance != nil || !slices.Contains(v.Actions, "accept-delivery") || string(before) != string(after) {
		t.Fatal("pending acceptance was hidden or overview mutated the goal")
	}
	for _, mode := range []string{"paused", "sync", "not-delivered", "not-required"} {
		trial := copyGoal(g)
		switch mode {
		case "paused":
			trial.State = "paused"
		case "sync":
			trial.SyncError = "changed"
		case "not-delivered":
			trial.Execution.Delivery.State = "published"
		case "not-required":
			trial.Execution.Policy.RequireHumanAcceptance = false
		}
		if slices.Contains(trial.Overview(time.Now()).Actions, "accept-delivery") {
			t.Fatal("unsafe action offered", mode)
		}
	}
	if err := g.Control("accept-delivery", "Checked recipient access in the delivered app", time.Now()); err != nil {
		t.Fatal(err)
	}
	v = g.Overview(time.Now())
	if v.HumanAcceptance == nil || v.HumanAcceptance.Revision != g.Revision || len(v.Actions) != 0 {
		t.Fatal("accepted decision missing or mutable")
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), g.Execution.Final.Receipt) || strings.Contains(string(b), "/private/source") {
		t.Fatal("view exposes private artifact paths")
	}
	v.HumanAcceptance.Evidence = "changed view"
	if g.Execution.HumanAcceptance.Evidence == v.HumanAcceptance.Evidence {
		t.Fatal("view aliases decision")
	}
}
