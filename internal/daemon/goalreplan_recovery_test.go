package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalplan"
	"github.com/sushidev-team/lola/internal/linear"
)

func TestReplanRestartNeverRepeatsAnAmbiguousPaidCall(t *testing.T) {
	d, f, g, calls, _ := goalReplanFixture(t)
	id := goal.NewID()
	if err := g.ReserveReplan(g.Tasks[0].ID, id, time.Now()); err != nil {
		t.Fatal(err)
	}
	var err error
	g, err = d.goals.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	in, err := d.goalRepairInput(context.Background(), g, g.Execution.Replans[id])
	if err != nil {
		t.Fatal(err)
	}
	if _, fresh, err := d.goalPlans.Reserve(in, "codex", "image"); err != nil || !fresh {
		t.Fatal(err)
	}
	d.goalPlans, err = goalplan.Open(filepath.Join(d.home, "state", "goal-plans.json"))
	if err != nil {
		t.Fatal(err)
	}
	issues := len(f.Created)
	if err = d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	g, _ = d.goals.Get(g.ID)
	if g.State != "blocked" || *calls != 0 || len(f.Created) != issues || g.Execution.PendingReplan != id {
		t.Fatal("restart replayed or discarded ambiguous coordinator intent")
	}
}

type stalledReplacementCreate struct {
	*linear.Fake
	entered chan struct{}
}

func (f stalledReplacementCreate) CreateIssue(ctx context.Context, in linear.IssueCreate) (string, string, error) {
	close(f.entered)
	<-ctx.Done()
	return "", "", ctx.Err()
}

func TestReplanPauseInterruptsPublicationAndResumesReservedChildren(t *testing.T) {
	d, f, g, calls, _ := goalReplanFixture(t)
	entered := make(chan struct{})
	d.lin = stalledReplacementCreate{f, entered}
	issues := len(f.Created)
	if err := d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		current, _ := d.goals.Get(g.ID)
		t.Fatal("publication did not start", current.Execution.Reason)
	}
	g, _ = d.goals.Get(g.ID)
	operation := g.Execution.PendingReplan
	child := g.Tasks[2].Issue.ID
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var err error
	g, err = d.controlGoal(ctx, goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "pause"})
	if err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	g, _ = d.goals.Get(g.ID)
	if g.State != "paused" || g.Execution.PendingReplan != operation || len(f.Created) != issues {
		t.Fatal("pause lost reserved graph or continued writes")
	}
	d.lin = f
	g, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "resume"})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	g, _ = d.goals.Get(g.ID)
	if g.State != "running" || g.Tasks[2].Issue.ID != child || *calls != 1 || len(f.Created) != issues+2 {
		t.Fatal("publication resume recreated graph or coordinator call", g.Execution.Reason)
	}
}
