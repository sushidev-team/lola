package daemon

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalwork"
)

func TestGoalVerificationEnforcesAttemptOwnershipBeforeChecks(t *testing.T) {
	for _, mode := range []string{"allowed", "out-of-scope"} {
		t.Run(mode, func(t *testing.T) {
			d, f, g, policy, _ := goalExecutionFixture(t)
			paths := []string{"app.txt"}
			if mode == "out-of-scope" {
				paths = []string{"docs"}
			}
			g.Tasks[0].Paths = map[string][]string{"p1": paths}
			var err error
			g, err = d.goals.Save(g)
			if err != nil {
				t.Fatal(err)
			}
			for i := range f.Details {
				if f.Details[i].ID == g.Tasks[0].Issue.ID {
					f.Details[i].Description = g.TaskDescription(g.Tasks[0])
				}
			}
			g, err = d.authorizeGoal(context.Background(), goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: policy})
			if err != nil {
				t.Fatal(err)
			}
			if err = d.runGoal(context.Background(), g.ID); err != nil {
				t.Fatal(err)
			}
			g = submitRunningGoalTask(t, d, g.ID)
			base := g.Execution.Manifest["p1"]
			checks := 0
			d.goalCheckRun = func(context.Context, string, []string) (string, error) { checks++; return "passed", nil }
			d.goalVerify = nil
			if err = d.queueGoalVerification(context.Background(), g, g.Execution.PendingIntegration); err != nil {
				t.Fatal(err)
			}
			d.connWg.Wait()
			g, _ = d.goals.Get(g.ID)
			m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
			tip, err := m.Tip(context.Background(), g.ID, "p1")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "allowed" {
				if checks == 0 || tip == base || g.Execution.Tasks[g.Tasks[0].ID].State != "integrated" {
					t.Fatal("scoped work failed to integrate", g.Execution.Reason)
				}
			} else {
				if checks != 0 || tip != base || g.Execution.Tasks[g.Tasks[0].ID].State != "pending" || g.Execution.PendingIntegration != "" {
					t.Fatal("scope violation was checked, integrated or not scheduled for bounded repair", g.Execution.Reason)
				}
			}
		})
	}
}

func TestGoalPolicyRejectsTaskScopeExpansionBeforePreparation(t *testing.T) {
	d, _, g, policy, _ := goalExecutionFixture(t)
	g.Tasks[0].Paths = map[string][]string{"p1": {"src"}}
	policy.Members[0].Paths = []string{"src/api"}
	if err := d.validateGoalPolicy(g, policy); err == nil {
		t.Fatal("task exceeded goal scope")
	}
	if stored, _ := d.goals.Get(g.ID); stored.Execution != nil {
		t.Fatal("invalid policy started preparation")
	}
}
