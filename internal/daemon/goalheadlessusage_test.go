package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/goalreview"
	"github.com/sushidev-team/lola/internal/usage"
)

func TestUnacceptedPlannerUsageCountsAndAcceptanceCannotDoubleCharge(t *testing.T) {
	d, _, request, _ := goalPlanFixture(t)
	ctx := context.Background()
	r, err := d.proposeGoal(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	ledger := &usage.Ledger{}
	if !recordGoalPlanUsage(ledger, d.goalPlans.UsageRecords()) {
		t.Fatal("unaccepted proposal absent from ledger")
	}
	day := time.Now().Local().Format(usage.DayFormat)
	all, by := ledger.Day(day)
	if all.Tokens() != 120 || by[request.Project].Tokens() != 120 {
		t.Fatal("proposal not attributed to project")
	}
	g, err := d.acceptGoalPlan(ctx, goalPlanArgs{ID: r.ID, Revision: r.Revision})
	if err != nil {
		t.Fatal(err)
	}
	g, err = d.publishGoal(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := d.cfg.ProjectByName(g.Project)
	policy := goalpolicy.Policy{Members: []goalpolicy.Member{{ID: p.Name, Project: p.Name, Source: p.Path, Repo: p.Repo, Target: "main"}}, Checks: []goalpolicy.Check{{ID: "tests", Command: "true", Image: "sha256:" + strings.Repeat("a", 64), TimeoutSeconds: 30}}, Limits: goalpolicy.Limits{Concurrency: 1, MaxTasks: 10, AttemptsPerTask: 2, ReviewRounds: 2, AgentRuns: 10, RuntimeMinutes: 60, AttemptMinutes: 10, Tokens: 100}}
	g, err = d.authorizeGoal(ctx, goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if g.Execution.ObservedTokens != 120 || g.Execution.Runs[r.ID].Usage == nil {
		t.Fatal("authorization forgot paid proposal")
	}
	recordGoalUsage(ledger, d.goals.List())
	all, _ = ledger.Day(day)
	if all.Tokens() != 120 {
		t.Fatal("accepted proposal charged twice")
	}
	if err := d.runGoal(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	g, _ = d.goals.Get(g.ID)
	if g.State != "blocked" || len(g.Execution.Attempts) != 0 {
		t.Fatal("exhausted proposal budget launched worker")
	}
}

func TestFailedCoordinatorTelemetryStillEntersLedger(t *testing.T) {
	d, _, request, _ := goalPlanFixture(t)
	base := d.goalPlanRun
	d.goalPlanRun = func(ctx context.Context, bin string, args []string, input []byte, env []string) ([]byte, error) {
		body, err := base(ctx, bin, args, input, env)
		if args[0] == "run" {
			return body, errors.New("exit status 1")
		}
		return body, err
	}
	r, err := d.proposeGoal(context.Background(), request)
	if err != nil || r.State != "failed" || r.Usage == nil {
		t.Fatalf("failed proposal usage: %+v %v", r, err)
	}
	ledger := &usage.Ledger{}
	recordGoalPlanUsage(ledger, d.goalPlans.UsageRecords())
	all, _ := ledger.Day(time.Now().Local().Format(usage.DayFormat))
	if all.Tokens() != 120 {
		t.Fatal("failed coordinator usage lost")
	}
}

func TestReviewerUsageRecoveredWithoutRelaunch(t *testing.T) {
	d, g, m, _ := submittedGoalFixture(t, nil)
	now := time.Now().UTC()
	id := "interrupted-review"
	if err := g.ReserveRun(id, "reviewer", "backend", now); err != nil {
		t.Fatal(err)
	}
	var err error
	g, err = d.goals.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := m.ReviewDirectory(g.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	o := usage.Observation{Agent: "codex", Status: "partial", CostStatus: "unavailable", At: now, Days: map[string]usage.Totals{now.Local().Format(usage.DayFormat): {Input: 400}}}
	b, _ := json.Marshal(goalreview.Receipt{Goal: g.ID, ID: id, Key: "backend:known-input", Agent: "codex", Image: "image", ImageID: "sha256:" + strings.Repeat("a", 64), State: "running", StartedAt: now, Usage: &o})
	if err := os.WriteFile(filepath.Join(dir, "review.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	d.goalReviewRun = func(context.Context, string, []string, []byte, []string) ([]byte, error) {
		t.Fatal("usage recovery launched a process")
		return nil, nil
	}
	d.goalUsagePass(context.Background(), now.Add(time.Second))
	current, _ := d.goals.Get(g.ID)
	if current.Execution.ObservedTokens != 400 || current.Execution.Runs[id].Usage == nil {
		t.Fatal("numeric receipt not recovered")
	}
	d.goalUsagePass(context.Background(), now.Add(2*time.Second))
	current, _ = d.goals.Get(g.ID)
	if current.Execution.ObservedTokens != 400 {
		t.Fatal("recovery double counted")
	}
}

func TestSpecialistUsageLimitPreventsIntegration(t *testing.T) {
	d, g, m, _ := submittedGoalFixture(t, nil)
	calls := isolateGoalReviewFixture(t, d)
	base := d.goalReviewRun
	d.goalReviewRun = func(ctx context.Context, bin string, args []string, input []byte, env []string) ([]byte, error) {
		b, err := base(ctx, bin, args, input, env)
		if args[0] == "run" {
			b = []byte(strings.ReplaceAll(string(b), `"input_tokens":100`, `"input_tokens":100000`))
		}
		return b, err
	}
	if err := d.verifyGoalIntegration(context.Background(), g.ID, g.Execution.PendingIntegration); err == nil {
		t.Fatal("exhausted specialist budget integrated output")
	}
	current, _ := d.goals.Get(g.ID)
	if current.State != "blocked" || !current.UsageExceeded() || *calls != 1 {
		t.Fatal("specialist consumption did not block goal")
	}
	if tip, err := m.Tip(context.Background(), g.ID, "p1"); err != nil || tip != g.Execution.Manifest["p1"] {
		t.Fatal("budget failure advanced goal revision")
	}
}
