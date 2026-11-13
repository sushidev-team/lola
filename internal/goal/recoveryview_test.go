package goal

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRecoverySnapshotRetainsDecisionInputsWithoutMutatingAuthority(t *testing.T) {
	g := executable(t)
	g.Revision = 9
	g.State = "paused"
	a := proposedBrief(t, &g)
	now := time.Now().UTC()
	g.ProgressUpdates = []ProgressUpdate{{ID: NewID(), Summary: "Previous progress awaiting confirmation", CreatedAt: now.Add(-time.Minute), AttemptedAt: now.Add(-time.Second)}}
	g.ProgressError = "Response was lost"
	g.Execution.DeliveryEvidenceURL = "https://artifacts.example/goal"
	g.BriefRevisions = []BriefRevision{{SourceID: "previous", Before: g.BriefContract(), After: g.BriefContract(), Reason: "Clarification", ArchivedFinals: []FinalVerification{{Receipt: "/private/old-verification.json"}}, AcceptedAt: now.Add(-time.Hour)}}
	before, _ := json.Marshal(g)
	v := g.Recovery(now)
	if v.Goal.Revision != 9 || v.PendingBrief == nil || v.PendingBrief.ID != a.SourceID || v.LatestProgress == nil || v.LatestProgress.ID != g.ProgressUpdates[0].ID || v.ProgressError != g.ProgressError {
		t.Fatal("missing exact review identities", v)
	}
	if !slices.Contains(v.Actions, "sync-brief") || !slices.Contains(v.Actions, "dismiss-progress") || !slices.Contains(v.Actions, "accept-brief") || slices.Contains(v.Actions, "retry-verification") {
		t.Fatal("incorrect recovery choices", v.Actions)
	}
	b, _ := json.Marshal(v)
	for _, secret := range []string{"/private/source", "/private/old-verification.json", "make test"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("private policy or receipt leaked", secret)
		}
	}
	v.Brief.Acceptance[0] = "changed"
	v.Goal.Tasks[0].Acceptance[0] = "changed"
	v.PendingBrief.Description = "changed"
	v.LatestProgress.Summary = "changed"
	v.LatestBrief.After.Acceptance[0] = "changed"
	after, _ := json.Marshal(g)
	if string(before) != string(after) {
		t.Fatal("recovery view or action probe mutated the accepted goal")
	}
}

func TestRecoveryActionEligibilityPreservesRetryBudgetsAndEvidence(t *testing.T) {
	g := replanFixture(t)
	now := time.Now().UTC()
	if err := g.ReserveReplan(g.Tasks[0].ID, "original-plan", now); err != nil {
		t.Fatal(err)
	}
	g.State = "blocked"
	before, _ := json.Marshal(g)
	if !slices.Contains(g.Recovery(now).Actions, "retry-replan") {
		t.Fatal("held coordinator retry hidden")
	}
	after, _ := json.Marshal(g)
	if string(before) != string(after) {
		t.Fatal("availability reserved a paid call")
	}
	for _, mode := range []string{"expired", "calls", "linear", "running", "canceled"} {
		trial := copyGoal(g)
		switch mode {
		case "expired":
			trial.Execution.Deadline = now
		case "calls":
			trial.Execution.Policy.Limits.AgentRuns = len(trial.Execution.Runs)
		case "linear":
			trial.SyncError = "changed placement"
		case "running", "canceled":
			trial.State = mode
		}
		if slices.Contains(trial.Recovery(now).Actions, "retry-replan") {
			t.Fatal("unsafe retry offered", mode)
		}
	}

	g = executable(t)
	for i := range g.Tasks {
		integrateTask(t, &g, i, "task-"+g.Tasks[i].ID)
	}
	now = time.Now().UTC()
	if err := g.ReserveFinal("original-verification", now); err != nil {
		t.Fatal(err)
	}
	g.State = "blocked"
	before, _ = json.Marshal(g)
	if !slices.Contains(g.Recovery(now).Actions, "retry-verification") {
		t.Fatal("held final verification retry hidden")
	}
	after, _ = json.Marshal(g)
	if string(before) != string(after) {
		t.Fatal("availability replaced verification evidence")
	}
	g.Execution.Policy.Limits.ReviewRounds = 1
	if slices.Contains(g.Recovery(now).Actions, "retry-verification") {
		t.Fatal("retry exceeds reviewed rounds")
	}
	g.Execution.Final.Receipt = "/private/final.json"
	g.State = "ready"
	if !slices.Contains(g.Recovery(now).Actions, "delivery-evidence") {
		t.Fatal("ready unpublished goal cannot attach shared evidence")
	}
	g.Execution.Delivery = &Delivery{}
	if slices.Contains(g.Recovery(now).Actions, "delivery-evidence") {
		t.Fatal("published description offered for replacement")
	}
}

func TestRecoveryResolvedProgressAndSettlingBrief(t *testing.T) {
	g := executable(t)
	g.Revision = 3
	g.State = "paused"
	proposedBrief(t, &g)
	g.Execution.PendingIntegration = "settling"
	now := time.Now().UTC()
	g.ProgressUpdates = []ProgressUpdate{{ID: NewID(), Summary: "Retained update", CreatedAt: now.Add(-time.Minute), DismissedAt: now, Resolution: "Checked Linear; preserve unknown outcome"}}
	v := g.Recovery(now)
	if slices.Contains(v.Actions, "accept-brief") || slices.Contains(v.Actions, "dismiss-progress") || v.LatestProgress.Resolution == "" {
		t.Fatal("settling brief or resolved progress can be resubmitted")
	}
	g.State = "accepted"
	if len(g.Recovery(now).Actions) != 0 {
		t.Fatal("terminal goal offers recovery effects")
	}
}
