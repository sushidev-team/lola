package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalplan"
	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/linear"
)

func goalReplanFixture(t *testing.T) (*Daemon, *linear.Fake, goal.Goal, *int, *int) {
	t.Helper()
	d, g, _, workers := submittedGoalFixture(t, func(p *goalpolicy.Policy) { p.Replan = true; p.Limits.AttemptsPerTask = 1 })
	d.cfg.Sandbox.Image = "planner-image"
	d.cfg.Defaults.Agent = "codex"
	if err := d.repairGoal(context.Background(), g.ID, g.Execution.PendingIntegration, "Independent acceptance check reproduced missing expiry validation"); err != nil {
		t.Fatal(err)
	}
	g, _ = d.goals.Get(g.ID)
	f := d.lin.(*linear.Fake)
	calls := new(int)
	d.goalPlanRun = func(_ context.Context, _ string, args []string, input []byte, _ []string) ([]byte, error) {
		if args[0] != "run" {
			return nil, nil
		}
		*calls++
		var in goalplan.Input
		if err := json.Unmarshal(input, &in); err != nil {
			t.Fatal(err)
		}
		current, _ := d.goals.Get(g.ID)
		if in.Repair == nil || current.Execution.PendingReplan != in.Request.ID || current.Execution.Runs[in.Request.ID].Kind != "coordinator" {
			t.Fatal("model launched before durable run reservation")
		}
		if in.Members[0].Snapshot.Commit != in.Repair.Manifest["p1"] || in.Repair.Failure == "" {
			t.Fatal("coordinator missing verified source/failure context")
		}
		sourceSeen := false
		for _, file := range in.Members[0].Snapshot.Files {
			sourceSeen = sourceSeen || file.Path == "app.txt" && file.Content == "initial"
		}
		if !sourceSeen {
			t.Fatal("coordinator did not receive committed source from its owned area")
		}
		out := goalplan.Output{Tasks: []goalplan.ProposedTask{
			{TaskDraft: goal.TaskDraft{Title: "Expiry guard", Description: "Implement expiry guard", Acceptance: []string{"Expired tokens rejected"}, Apps: in.Repair.Original.Apps, LabelIDs: in.Repair.Original.LabelIDs, Paths: map[string][]string{"p1": {"app.txt"}}}, Covers: []int{1}},
			{TaskDraft: goal.TaskDraft{Title: "Expiry regression", Description: "Add regression coverage", Acceptance: []string{"Regression passes"}, Apps: in.Repair.Original.Apps, LabelIDs: in.Repair.Original.LabelIDs, Paths: map[string][]string{"p1": {"app.txt"}}, BlockedBy: []int{1}}, Covers: []int{1}},
		}}
		body, _ := json.Marshal(out)
		return fixtureAgentEvents(args, body), nil
	}
	return d, f, g, calls, workers
}

func TestAutonomousReplanPublishesOnceAndCompletesVerifiedReplacementGraph(t *testing.T) {
	d, f, g, calls, workers := goalReplanFixture(t)
	ctx := context.Background()
	original := g.Tasks[0]
	initialIssues := len(f.Created)
	// Simulate committed remote creates whose responses are lost. Stable IDs
	// and authoritative reads must recover every child and relation once.
	d.lin = lostGoalResponse{f}
	if err := d.runGoal(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	g, _ = d.goals.Get(g.ID)
	if g.State != "running" || g.Execution.PendingReplan != "" || len(g.Tasks) != 4 || *calls != 1 || len(f.Created) != initialIssues+2 {
		t.Fatalf("replan did not complete: state=%s reason=%s calls=%d issues=%d", g.State, g.Execution.Reason, *calls, len(f.Created))
	}
	if g.Execution.Tasks[original.ID].State != "superseded" || g.TaskSatisfied(original.ID) {
		t.Fatal("superseded original treated as integrated")
	}
	for _, child := range g.Tasks[2:] {
		if !d.goals.OwnsIssue(child.Issue.ID) || !child.Issue.Confirmed {
			t.Fatal("replacement not reserved and bound")
		}
	}
	// Reopen both stores before dispatch; completed plans cannot repeat calls
	// or create new children merely because the daemon restarted.
	var err error
	d.goals, err = goal.Open(filepath.Join(d.home, "state", "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	d.goalPlans, err = goalplan.Open(filepath.Join(d.home, "state", "goal-plans.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{2, 3, 1} {
		if err = d.runGoal(ctx, g.ID); err != nil {
			t.Fatal(err)
		}
		g, _ = d.goals.Get(g.ID)
		found := false
		for _, a := range g.Execution.Attempts {
			if a.State == "running" {
				if a.TaskID != g.Tasks[index].ID {
					t.Fatal("wrong dependency released")
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("replacement %d not dispatched: %s", index, g.Execution.Reason)
		}
		g = submitRunningGoalTask(t, d, g.ID)
		if err = d.verifyGoalIntegration(ctx, g.ID, g.Execution.PendingIntegration); err != nil {
			t.Fatal(err)
		}
		g, _ = d.goals.Get(g.ID)
	}
	if g.State != "verifying" || *workers != 4 || *calls != 1 || len(f.Created) != initialIssues+2 {
		t.Fatal("replacement execution duplicated work")
	}
	if err = d.runGoal(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	g, _ = d.goals.Get(g.ID)
	if err = d.verifyGoalFinal(ctx, g.ID, g.Execution.Final.ID); err != nil {
		t.Fatal(err)
	}
	g, _ = d.goals.Get(g.ID)
	if g.State != "ready" {
		t.Fatal("replacement graph did not reach independently verified final artifact")
	}
}

func TestReplanPartialPublicationRecoversWithoutNewModelOrIssues(t *testing.T) {
	d, f, g, calls, _ := goalReplanFixture(t)
	f.Errs = map[string]error{"CreateGoalRelation": errors.New("Linear unavailable")}
	if err := d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	g, _ = d.goals.Get(g.ID)
	op := g.Execution.PendingReplan
	if g.State != "blocked" || g.Execution.Replans[op].State != "provisioning" || len(g.Tasks) != 4 {
		t.Fatal("partial graph publication not retained", g.Execution.Reason)
	}
	issues := len(f.Created)
	if _, err := d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "retry-replan"}); err == nil {
		t.Fatal("partial publication abandoned")
	}
	delete(f.Errs, "CreateGoalRelation")
	var err error
	g, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "resume"})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	g, _ = d.goals.Get(g.ID)
	if g.State != "running" || g.Execution.PendingReplan != "" || *calls != 1 || len(f.Created) != issues {
		t.Fatal("partial publication did not recover idempotently", g.Execution.Reason)
	}
}

func TestPausedCoordinatorDoesNotApplyOutputOrAutomaticallyRepeatCall(t *testing.T) {
	d, f, g, calls, _ := goalReplanFixture(t)
	base := d.goalPlanRun
	entered := make(chan struct{})
	d.goalPlanRun = func(ctx context.Context, bin string, args []string, input []byte, env []string) ([]byte, error) {
		if args[0] == "run" {
			close(entered)
			<-ctx.Done()
			*calls++
			return nil, ctx.Err()
		}
		return base(ctx, bin, args, input, env)
	}
	issues := len(f.Created)
	if err := d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		current, _ := d.goals.Get(g.ID)
		t.Fatal("coordinator did not start:", current.Execution.Reason)
	}
	g, _ = d.goals.Get(g.ID)
	first := g.Execution.PendingReplan
	var err error
	g, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "pause"})
	if err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	g, _ = d.goals.Get(g.ID)
	if g.State != "paused" || len(g.Tasks) != 2 || len(f.Created) != issues {
		t.Fatal("canceled coordinator changed task graph")
	}
	g, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "resume"})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	g, _ = d.goals.Get(g.ID)
	if g.State != "blocked" || *calls != 1 {
		t.Fatal("resume repeated ambiguous paid call")
	}
	d.goalPlanRun = base
	g, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "retry-replan", Reason: "Retry canceled coordinator"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Execution.PendingReplan == first || g.Execution.Replans[first].State != "failed" {
		t.Fatal("explicit retry lost prior identity")
	}
	if err = d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	g, _ = d.goals.Get(g.ID)
	if g.State != "running" || *calls != 2 || len(g.Tasks) != 4 {
		t.Fatal("explicit bounded retry did not succeed", g.Execution.Reason)
	}
}

func TestInvalidCoordinatorOutputCannotMutateOriginalContract(t *testing.T) {
	for _, mode := range []string{"unknowns", "scope", "coverage"} {
		t.Run(mode, func(t *testing.T) {
			d, f, g, _, _ := goalReplanFixture(t)
			base := d.goalPlanRun
			issues := len(f.Created)
			d.goalPlanRun = func(ctx context.Context, bin string, args []string, input []byte, env []string) ([]byte, error) {
				b, err := base(ctx, bin, args, input, env)
				if args[0] != "run" || err != nil {
					return b, err
				}
				var out goalplan.Output
				_ = json.Unmarshal(fixtureAgentText(args, b), &out)
				switch mode {
				case "unknowns":
					out.Unknowns = []string{"Product decision required"}
				case "scope":
					out.Tasks[0].Apps = []string{"unassigned"}
				case "coverage":
					out.Tasks[0].Covers = nil
				}
				body, _ := json.Marshal(out)
				return fixtureAgentEvents(args, body), nil
			}
			if err := d.runGoal(context.Background(), g.ID); err != nil {
				t.Fatal(err)
			}
			d.connWg.Wait()
			g, _ = d.goals.Get(g.ID)
			if g.State != "blocked" || len(g.Tasks) != 2 || len(f.Created) != issues || g.Execution.Tasks[g.Tasks[0].ID].State != "pending" {
				t.Fatal("invalid plan changed execution", g.Execution.Reason)
			}
		})
	}
}

func TestRepairPlannerTimeoutCannotOutliveGoalDeadline(t *testing.T) {
	d, _, g, _, _ := goalReplanFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	outer, _ := ctx.Deadline()
	base := d.goalPlanRun
	d.goalPlanRun = func(ctx context.Context, bin string, args []string, input []byte, env []string) ([]byte, error) {
		if args[0] == "run" {
			deadline, ok := ctx.Deadline()
			if !ok || deadline.After(outer) {
				t.Fatal("planner exceeded goal deadline")
			}
		}
		return base(ctx, bin, args, input, env)
	}
	if err := d.runGoal(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
}

func TestCompletedCoordinatorWithNoWorkerBudgetProducesVisibleHold(t *testing.T) {
	d, f, g, calls, _ := goalReplanFixture(t)
	// A budget accepted before any work: preserve the existing charged failed
	// worker and allow exactly one coordinator, with no later worker calls.
	policy := g.Execution.Policy
	policy.Limits.AgentRuns = len(g.Execution.Runs) + 1
	g.Execution.Policy = policy
	g.Execution.PolicyDigest, _ = policy.Digest()
	// Use a fresh store for the fixture's preaccepted policy; production never
	// replaces the digest on an existing accepted execution.
	var err error
	d.goals, err = goal.Open(filepath.Join(t.TempDir(), "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	g.Revision = 0
	g, err = d.goals.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	g, _ = d.goals.Get(g.ID)
	if g.State != "running" || len(g.Tasks) != 4 || *calls != 1 || len(f.Created) != 5 {
		t.Fatal("reserved coordinator could not finish at its accepted cap", g.Execution.Reason)
	}
	if err = d.runGoal(context.Background(), g.ID); err == nil {
		t.Fatal("exhausted idle goal silently kept running")
	}
	g, _ = d.goals.Get(g.ID)
	if g.State != "blocked" || g.Execution.Reason == "" {
		t.Fatal("budget hold is not visible")
	}
}
