package goal

import (
	"maps"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goalpolicy"
)

func executable(t *testing.T) Goal {
	t.Helper()
	g, err := New(testDraft(), "team", "linear-project")
	if err != nil {
		t.Fatal(err)
	}
	g.Parent.Confirmed = true
	g.State = "planned"
	for i := range g.Tasks {
		g.Tasks[i].Issue.Confirmed = true
		for j := range g.Tasks[i].Relations {
			g.Tasks[i].Relations[j].Confirmed = true
		}
	}
	p := goalpolicy.Policy{Members: []goalpolicy.Member{{ID: "api", Project: "nori", Source: "/private/source", Target: "main"}}, Checks: []goalpolicy.Check{{ID: "tests", Command: "make test", Image: "sha256:" + strings.Repeat("a", 64), TimeoutSeconds: 60}}, Limits: goalpolicy.Limits{Concurrency: 2, MaxTasks: 10, AttemptsPerTask: 2, ReviewRounds: 2, AgentRuns: 20, RuntimeMinutes: 60, AttemptMinutes: 15, Tokens: 100000}}
	if err := g.BeginExecution(p, map[string]string{"api": strings.Repeat("a", 40)}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestExecutionReservationsAndDependenciesSurviveRestart(t *testing.T) {
	g := executable(t)
	now := time.Now().UTC()
	if _, err := g.ReserveAttempt(g.Tasks[1].ID, "early", "session-early", now); err == nil {
		t.Fatal("dependency dispatched before integration")
	}
	a, err := g.ReserveAttempt(g.Tasks[0].ID, "attempt1", "session1", now)
	if err != nil {
		t.Fatal(err)
	}
	if a.Base["api"] != g.Execution.Manifest["api"] || len(g.Execution.Runs) != 1 {
		t.Fatal("base or run budget not reserved")
	}
	path := filepath.Join(t.TempDir(), "goals.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	g, err = store.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	g, _ = store.Get(g.ID)
	if _, err := g.ReserveAttempt(g.Tasks[0].ID, "duplicate", "session2", now); err == nil {
		t.Fatal("restart launched another active attempt")
	}
	a = g.Execution.Attempts[a.ID]
	a.State = "submitted"
	g.Execution.Attempts[a.ID] = a
	i, err := g.ReserveIntegration("integration1", a.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.ReserveAttempt(g.Tasks[1].ID, "early2", "session3", now); err == nil {
		t.Fatal("partial integration unlocked dependency")
	}
	g.State = "paused"
	if err := g.CompleteIntegration(i.ID, "receipt.json", map[string]string{"api": strings.Repeat("b", 40)}, now); err == nil {
		t.Fatal("paused goal integrated")
	}
	g.State = "running"
	if err := g.CompleteIntegration(i.ID, "receipt.json", map[string]string{"api": strings.Repeat("b", 40)}, now); err != nil {
		t.Fatal(err)
	}
	b, err := g.ReserveAttempt(g.Tasks[1].ID, "attempt2", "session2", now)
	if err != nil {
		t.Fatal(err)
	}
	if b.Base["api"] != strings.Repeat("b", 40) {
		t.Fatal("dependent task did not receive verified revision")
	}
	b.State = "submitted"
	g.Execution.Attempts[b.ID] = b
	final, err := g.ReserveIntegration("integration2", b.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.CompleteIntegration(final.ID, "receipt2.json", map[string]string{"api": strings.Repeat("c", 40)}, now); err != nil {
		t.Fatal(err)
	}
	if g.State != "verifying" {
		t.Fatal("all integrated tasks were mistaken for final acceptance")
	}
	if err := g.ReserveRun("final-review", "reviewer", g.ID, now); err != nil {
		t.Fatalf("final verification cannot reserve a reviewer: %v", err)
	}
	if err := g.ReserveFinal("final1", now); err != nil {
		t.Fatal(err)
	}
	g.State = "blocked"
	if err := g.RetryVerification("final2", now); err != nil {
		t.Fatal(err)
	}
	if len(g.Execution.FinalHistory) != 1 || g.Execution.FinalHistory[0].ID != "final1" {
		t.Fatal("final retry erased previous evidence identity")
	}
	g.State = "blocked"
	if err := g.RetryVerification("final3", now); err == nil {
		t.Fatal("final retries exceeded accepted round budget")
	}
	g.State = "paused"
	if err := g.CompleteFinal("final2", "final-evidence.json", now); err == nil {
		t.Fatal("paused goal became ready")
	}
	g.State = "verifying"
	if err := g.CompleteFinal("wrong", "final-evidence.json", now); err == nil {
		t.Fatal("wrong final evidence accepted")
	}
	if err := g.CompleteFinal("final2", "final-evidence.json", now); err != nil {
		t.Fatal(err)
	}
	if g.State != "ready" {
		t.Fatal("verified artifact not ready for human delivery")
	}
	if _, err := store.Save(g); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded, _ := reopened.Get(g.ID); loaded.State != "ready" || loaded.Execution.Final.Receipt != "final-evidence.json" {
		t.Fatal("restart lost final verification")
	}
}

func TestExecutionBudgetAndPauseHolds(t *testing.T) {
	for _, tc := range []string{"paused", "sync", "runtime", "tokens", "spend", "runs", "stop", "attempts", "concurrency"} {
		t.Run(tc, func(t *testing.T) {
			g := executable(t)
			e := g.Execution
			now := time.Now().UTC()
			switch tc {
			case "paused":
				g.State = "paused"
			case "sync":
				g.SyncError = "Linear unavailable"
			case "runtime":
				now = e.Deadline
			case "tokens":
				e.ObservedTokens = e.Policy.Limits.Tokens
			case "spend":
				e.Policy.Limits.SpendUSD = 1
				e.ObservedSpendUSD = 1
			case "runs":
				e.Policy.Limits.AgentRuns = 1
				e.PolicyDigest, _ = e.Policy.Digest()
				if err := g.ReserveRun("planner", "coordinator", g.ID, now); err != nil {
					t.Fatal(err)
				}
			case "stop":
				e.StopWorkersAt = now
			case "attempts":
				p := e.Tasks[g.Tasks[0].ID]
				p.AttemptIDs = []string{"old1", "old2"}
				e.Tasks[g.Tasks[0].ID] = p
			case "concurrency":
				e.Attempts["a"] = Attempt{State: "running"}
				e.Attempts["b"] = Attempt{State: "reserved"}
			}
			e.PolicyDigest, _ = e.Policy.Digest()
			before := len(e.Runs)
			if _, err := g.ReserveAttempt(g.Tasks[0].ID, "attempt", "session", now); err == nil {
				t.Fatal("held goal dispatched")
			}
			if len(e.Runs) != before {
				t.Fatal("rejected attempt consumed agent budget")
			}
		})
	}
}

func TestExecutionStoreRejectsPolicyMutationAndFalseIntegration(t *testing.T) {
	g := executable(t)
	s, err := Open(filepath.Join(t.TempDir(), "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	g, err = s.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	changed := copyGoal(g)
	changed.Execution.Policy.Limits.AgentRuns++
	changed.Execution.PolicyDigest, _ = changed.Execution.Policy.Digest()
	if _, err := s.Save(changed); err == nil {
		t.Fatal("accepted policy silently enlarged")
	}
	changed = copyGoal(g)
	changed.Execution = nil
	changed.State = "planned"
	if _, err := s.Save(changed); err == nil {
		t.Fatal("authorization and attempts erased")
	}
	changed = copyGoal(g)
	p := changed.Execution.Tasks[g.Tasks[0].ID]
	p.State = "integrated"
	changed.Execution.Tasks[g.Tasks[0].ID] = p
	if _, err := s.Save(changed); err == nil {
		t.Fatal("task completed without integration evidence")
	}
	changed = copyGoal(g)
	changed.Execution.Manifest = maps.Clone(g.Execution.Manifest)
	changed.Execution.Manifest["unexpected"] = strings.Repeat("b", 40)
	if _, err := s.Save(changed); err == nil {
		t.Fatal("unauthorized repository in manifest")
	}
}

func TestExecutionControlsPreserveWorkAndMembership(t *testing.T) {
	g := executable(t)
	now := time.Now().UTC()
	a, err := g.ReserveAttempt(g.Tasks[0].ID, "a", "session1", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Control("pause", "inspect progress", now); err != nil {
		t.Fatal(err)
	}
	if g.Execution.Attempts[a.ID].State != "reserved" {
		t.Fatal("pause destroyed running work")
	}
	if err := g.Control("resume", "continue", now); err != nil || g.State != "running" {
		t.Fatal(err)
	}
	if err := g.Control("stop-workers", "checkpoint", now); err != nil || g.Execution.StopWorkersAt.IsZero() {
		t.Fatal(err)
	}
	if err := g.Control("cancel", "retain output", now); err != nil || g.State != "canceled" {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(t.TempDir(), "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(g); err != nil {
		t.Fatal(err)
	}
	if !s.OwnsIssue(g.Tasks[0].Issue.ID) || len(g.Execution.Attempts) != 1 {
		t.Fatal("cancel released issue or lost attempt")
	}
	if err := g.Control("resume", "", now); err == nil {
		t.Fatal("cancellation silently resumed")
	}
}
