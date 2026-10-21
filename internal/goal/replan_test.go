package goal

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func replanFixture(t *testing.T) Goal {
	t.Helper()
	g := executable(t)
	g.Execution.Policy.Replan = true
	g.Execution.Policy.Limits.AttemptsPerTask = 1
	g.Execution.PolicyDigest, _ = g.Execution.Policy.Digest()
	g.Tasks[0].Apps = []string{"api"}
	g.Tasks[0].Paths = map[string][]string{"api": {"src"}}
	a, err := g.ReserveAttempt(g.Tasks[0].ID, "failed-worker", "failed-session", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	a.State = "submitted"
	g.Execution.Attempts[a.ID] = a
	if _, err = g.ReserveIntegration("rejected", a.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = g.RejectIntegration("rejected", "Independent check reproduced expired invitation acceptance", time.Now()); err != nil {
		t.Fatal(err)
	}
	if g.State != "running" || g.ReplanTask() != g.Tasks[0].ID {
		t.Fatal("conclusive failure did not preserve bounded decomposition")
	}
	return g
}
func replacementFixture() []Replacement {
	return []Replacement{
		{TaskDraft: TaskDraft{Title: "Expiry contract", Description: "Implement expiry guard", Acceptance: []string{"Guard rejects expired tokens"}, Apps: []string{"api"}, Paths: map[string][]string{"api": {"src/api"}}}, Covers: []int{1}},
		{TaskDraft: TaskDraft{Title: "Expiry integration", Description: "Exercise end-to-end invitation acceptance", Acceptance: []string{"Regression integration test passes"}, Apps: []string{"api"}, Paths: map[string][]string{"api": {"src/tests"}}, BlockedBy: []int{1}}, Covers: []int{1}},
	}
}
func integrateTask(t *testing.T, g *Goal, index int, tag string) {
	t.Helper()
	a, err := g.ReserveAttempt(g.Tasks[index].ID, tag, "session-"+tag, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	a.State = "submitted"
	g.Execution.Attempts[a.ID] = a
	if _, err = g.ReserveIntegration("integration-"+tag, a.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = g.CompleteIntegration("integration-"+tag, "verified-evidence", g.Execution.Manifest, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestReplanRetainsOriginalAndGatesDependentsOnAllChildren(t *testing.T) {
	g := replanFixture(t)
	original := g.Tasks[0]
	count := len(g.Execution.Runs)
	if err := g.ReserveReplan(original.ID, "plan", time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(g.Execution.Runs) != count+1 {
		t.Fatal("coordinator not charged")
	}
	if _, err := g.ReserveAttempt(g.Tasks[1].ID, "early", "early", time.Now()); err == nil {
		t.Fatal("replan failed to hold dispatch")
	}
	if err := g.Decompose("plan", replacementFixture(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(g.Tasks) != 4 || g.Execution.Tasks[original.ID].State != "superseded" || g.TaskSatisfied(original.ID) {
		t.Fatal("decomposition fabricated completion")
	}
	if g.Tasks[0].ID != original.ID || g.Tasks[0].Issue.ID != original.Issue.ID || g.Tasks[0].Description != original.Description {
		t.Fatal("original contract overwritten")
	}
	for _, child := range g.Tasks[2:] {
		if !strings.Contains(g.TaskContract(child), original.Acceptance[0]) {
			t.Fatal("original criterion not inherited")
		}
	}
	store, _ := Open(filepath.Join(t.TempDir(), "goals.json"))
	var err error
	g, err = store.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	store, err = Open(store.path)
	if err != nil {
		t.Fatal(err)
	}
	g, _ = store.Get(g.ID)
	if err = g.CompleteReplan("plan", time.Now()); err == nil {
		t.Fatal("unpublished replacements released")
	}
	for i := 2; i < len(g.Tasks); i++ {
		g.Tasks[i].Issue.Confirmed = true
		for j := range g.Tasks[i].Relations {
			g.Tasks[i].Relations[j].Confirmed = true
		}
	}
	if err = g.CompleteReplan("plan", time.Now()); err != nil {
		t.Fatal(err)
	}
	integrateTask(t, &g, 2, "first")
	if _, err := g.ReserveAttempt(g.Tasks[1].ID, "dependent", "dependent", time.Now()); err == nil {
		t.Fatal("one replacement unlocked original dependent")
	}
	integrateTask(t, &g, 3, "second")
	if !g.TaskSatisfied(original.ID) || g.Execution.Tasks[original.ID].State != "superseded" {
		t.Fatal("completed replacement group was mislabeled")
	}
	integrateTask(t, &g, 1, "dependent")
	if g.State != "verifying" || !g.AllTasksSatisfied() {
		t.Fatal("goal did not advance after all verified replacement output")
	}
	if err := g.ReserveFinal("final", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := g.CompleteFinal("final", "whole-goal-proof", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Save(g); err != nil {
		t.Fatal(err)
	}
}

func TestReplanCannotExpandOrWeakenTheAcceptedContract(t *testing.T) {
	for _, mode := range []string{"coverage", "scope", "routing", "labels", "dependency", "budget", "pause", "manifest", "adoption"} {
		t.Run(mode, func(t *testing.T) {
			g := replanFixture(t)
			if err := g.ReserveReplan(g.Tasks[0].ID, "plan", time.Now()); err != nil {
				t.Fatal(err)
			}
			children := replacementFixture()
			switch mode {
			case "adoption":
				children[0].ExistingIssueID = "12ab5678-1234-4234-9234-123456789abc"
			case "coverage":
				children[0].Covers = nil
			case "scope":
				children[0].Paths["api"] = []string{"outside"}
			case "routing":
				children[0].Apps = []string{"unknown"}
			case "labels":
				children[0].LabelIDs = []string{"invented"}
			case "dependency":
				children[0].BlockedBy = []int{2}
			case "budget":
				children = append(children, children...)
				g.Execution.Policy.Limits.MaxTasks = 3
				g.Execution.PolicyDigest, _ = g.Execution.Policy.Digest()
			case "pause":
				g.State = "paused"
			case "manifest":
				g.Execution.Manifest["api"] = strings.Repeat("b", 40)
			}
			before := len(g.Tasks)
			if err := g.Decompose("plan", children, time.Now()); err == nil {
				t.Fatal("unsafe decomposition accepted")
			}
			if len(g.Tasks) != before || g.Execution.Tasks[g.Tasks[0].ID].State != "pending" {
				t.Fatal("rejected plan partially mutated graph")
			}
		})
	}
}

func TestReplanRetriesAreExplicitChargedAndDoNotAbandonPublication(t *testing.T) {
	g := replanFixture(t)
	if err := g.ReserveReplan(g.Tasks[0].ID, "plan1", time.Now()); err != nil {
		t.Fatal(err)
	}
	g.State = "blocked"
	g.SyncError = "interrupted model call"
	before := len(g.Execution.Runs)
	if err := g.RetryReplan("plan2", time.Now()); err != nil {
		t.Fatal(err)
	}
	if g.Execution.Replans["plan1"].State != "failed" || g.Execution.PendingReplan != "plan2" || len(g.Execution.Runs) != before+1 {
		t.Fatal("retry reused or erased run budget")
	}
	if err := g.Decompose("plan2", replacementFixture(), time.Now()); err != nil {
		t.Fatal(err)
	}
	g.State = "blocked"
	if err := g.RetryReplan("plan3", time.Now()); err == nil {
		t.Fatal("partial issue publication abandoned")
	}
}

func TestReplanRequiresConclusiveFailureAndPolicyOptIn(t *testing.T) {
	g := replanFixture(t)
	g.Execution.Policy.Replan = false
	g.Execution.PolicyDigest, _ = g.Execution.Policy.Digest()
	if g.ReplanTask() != "" {
		t.Fatal("replanned without accepted permission")
	}
	g.Execution.Policy.Replan = true
	g.Execution.PolicyDigest, _ = g.Execution.Policy.Digest()
	delete(g.Execution.Integrations, "rejected")
	if g.ReplanTask() != "" {
		t.Fatal("ordinary failed process interpreted as verified failure")
	}
}
