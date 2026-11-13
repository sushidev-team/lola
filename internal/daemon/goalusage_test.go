package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/usage"
)

func TestGoalUsageStopsActiveWorkAndPreservesFiles(t *testing.T) {
	d, _, g, policy, launched := goalExecutionFixture(t)
	ctx := context.Background()
	var err error
	g, err = d.authorizeGoal(ctx, goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.runGoal(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	g, _ = d.goals.Get(g.ID)
	var attempt string
	for id := range g.Execution.Attempts {
		attempt = id
	}
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	dir, err := m.LaunchDirectory(g.ID, attempt)
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(dir, "workspace", "uncommitted.txt")
	if err := os.WriteFile(work, []byte("preserve this work"), 0600); err != nil {
		t.Fatal(err)
	}
	reviewCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	d.goalJobs = map[string]context.CancelFunc{g.ID: cancel}
	now := time.Now().UTC()
	d.goalUsageScan = func(_ context.Context, path, agent string, at time.Time) usage.Observation {
		if path != dir {
			t.Fatal("scanner received an unowned path")
		}
		return usage.Observation{Agent: agent, Status: "partial", CostStatus: "estimate", At: at, Days: map[string]usage.Totals{at.Local().Format(usage.DayFormat): {Input: policy.Limits.Tokens}}}
	}
	d.goalUsagePass(ctx, now)
	current, _ := d.goals.Get(g.ID)
	if current.State != "blocked" || current.Execution.StopWorkersAt.IsZero() || !current.UsageExceeded() || reviewCtx.Err() == nil {
		t.Fatal("budget did not persist a hold and cancel active verification")
	}
	if err := d.runGoal(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if *launched != 1 {
		t.Fatal("budget hold started more work")
	}
	if b, err := os.ReadFile(work); err != nil || string(b) != "preserve this work" {
		t.Fatal("budget stop removed worker output")
	}
	current, _ = d.goals.Get(g.ID)
	if current.Execution.Attempts[attempt].State != "stopped" || !strings.Contains(current.Execution.Reason, "usage budget") {
		t.Fatal("worker did not stop with durable budget reason")
	}
	finished := current.Execution.Runs[attempt].FinishedAt
	if finished.IsZero() || !finished.Equal(current.Execution.Attempts[attempt].FinishedAt) {
		t.Fatal("confirmed worker stop left its usage run open")
	}
	if _, err := d.controlGoal(ctx, goal.ControlRequest{ID: g.ID, Revision: current.Revision, Action: "resume"}); err == nil {
		t.Fatal("resume bypassed exhausted budget")
	}
	ledger := &usage.Ledger{}
	if !recordGoalUsage(ledger, d.goals.List()) || recordGoalUsage(ledger, d.goals.List()) {
		t.Fatal("ledger attribution is not idempotent")
	}
	all, by := ledger.Day(now.Local().Format(usage.DayFormat))
	if all.Tokens() != policy.Limits.Tokens || by[g.Project].Tokens() != policy.Limits.Tokens {
		t.Fatal("usage missing from global/project ledger")
	}
	d.goals, err = goal.Open(filepath.Join(d.home, "state", "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	current, _ = d.goals.Get(g.ID)
	if !current.UsageExceeded() {
		t.Fatal("restart forgot the exhausted budget")
	}
}

func TestGoalUsageReadOnlyCommandShowsMissingCoverage(t *testing.T) {
	d, _, g, policy, _ := goalExecutionFixture(t)
	ctx := context.Background()
	var err error
	g, err = d.authorizeGoal(ctx, goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.runGoal(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	g, _ = d.goals.Get(g.ID)
	// Include a coordinator call with no telemetry: zero must not look exact.
	if err := g.ReserveRun("plan-again", "coordinator", "replan", time.Now()); err != nil {
		t.Fatal(err)
	}
	g, err = d.goals.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	d.goalUsagePass(ctx, time.Now())
	args, _ := json.Marshal(protocol.GoalArgs{ID: g.ID})
	before, _ := d.goals.Get(g.ID)
	response := d.handleGoals(ctx, protocol.Request{Cmd: "goalUsage", Args: args})
	if response.Error != "" {
		t.Fatal(response.Error)
	}
	var report goal.UsageReport
	if err := json.Unmarshal(response.Data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Coverage.Runs != 2 || report.Coverage.Missing != 2 || report.Coverage.CostUnavailable != 2 {
		t.Fatalf("unknown telemetry hidden: %+v", report.Coverage)
	}
	after, _ := d.goals.Get(g.ID)
	if before.Revision != after.Revision {
		t.Fatal("usage read mutated goal")
	}
}

func TestGoalUsageScanPreservesConcurrentPause(t *testing.T) {
	d, _, g, policy, _ := goalExecutionFixture(t)
	ctx := context.Background()
	var err error
	g, err = d.authorizeGoal(ctx, goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.runGoal(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	d.goalUsageScan = func(_ context.Context, _ string, agent string, at time.Time) usage.Observation {
		current, _ := d.goals.Get(g.ID)
		if _, err := d.controlGoal(ctx, goal.ControlRequest{ID: g.ID, Revision: current.Revision, Action: "pause", Reason: "human pause"}); err != nil {
			t.Fatal(err)
		}
		return usage.Observation{Agent: agent, Status: "partial", CostStatus: "unavailable", At: at, Days: map[string]usage.Totals{at.Local().Format(usage.DayFormat): {Input: 10}}}
	}
	d.goalUsagePass(ctx, time.Now())
	current, _ := d.goals.Get(g.ID)
	if current.State != "paused" || current.Execution.Reason != "human pause" || current.Execution.ObservedTokens != 10 {
		t.Fatal("usage write lost human steering")
	}
}
