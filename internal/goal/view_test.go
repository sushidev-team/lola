package goal

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

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
