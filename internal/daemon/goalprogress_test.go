package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/linear"
)

type lostGoalComment struct {
	*linear.Fake
	afterCreate bool
	failRead    bool
}

func (f *lostGoalComment) CreateGoalComment(ctx context.Context, c linear.GoalComment) error {
	if err := f.Fake.CreateGoalComment(ctx, c); err != nil {
		return err
	}
	f.afterCreate = true
	return errors.New("response lost")
}
func (f *lostGoalComment) LookupGoalComment(ctx context.Context, id string) (*linear.GoalComment, error) {
	if f.afterCreate && f.failRead {
		return nil, errors.New("read unavailable")
	}
	return f.Fake.LookupGoalComment(ctx, id)
}
func TestGoalProgressRecoversLostResponseAndCoalesces(t *testing.T) {
	d, f, g, p, _ := goalExecutionFixture(t)
	ctx := context.Background()
	g, err := d.authorizeGoal(ctx, goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: p})
	if err != nil {
		t.Fatal(err)
	}
	remote := &lostGoalComment{Fake: f, failRead: true}
	d.lin = remote
	now := time.Now().UTC()
	if err = d.syncGoalProgressOne(ctx, g.ID, now); err == nil {
		t.Fatal("uncertain send not reported")
	}
	stored, _ := d.goals.Get(g.ID)
	if len(f.GoalComments) != 1 || len(stored.ProgressUpdates) != 1 || stored.ProgressUpdates[0].SentAt.IsZero() || stored.State != g.State || stored.SyncError != "" {
		t.Fatal("projection lost intent or changed execution")
	}
	d.goals, err = goal.Open(filepath.Join(d.home, "state", "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	remote.failRead = false
	if err = d.syncGoalProgressOne(ctx, g.ID, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	stored, _ = d.goals.Get(g.ID)
	if stored.ProgressUpdates[0].ConfirmedAt.IsZero() || stored.ProgressError != "" || len(f.GoalComments) != 1 {
		t.Fatal("recovery duplicated or did not acknowledge")
	}
	if err = d.syncGoalProgressOne(ctx, g.ID, now.Add(4*time.Minute)); err != nil || len(f.GoalComments) != 1 {
		t.Fatal("unchanged state spammed", err)
	}
	stored, err = d.controlGoal(ctx, goal.ControlRequest{ID: g.ID, Revision: stored.Revision, Action: "pause"})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.syncGoalProgressOne(ctx, g.ID, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(f.GoalComments) != 2 || !strings.Contains(f.GoalComments[1].Body, "paused") {
		t.Fatal("pause not visible")
	}
	if f.Details[0].Description != g.ParentDescription() {
		t.Fatal("progress changed human brief")
	}
}
func TestGoalProgressPreservesEditedOrDeletedUncertainComment(t *testing.T) {
	for _, mode := range []string{"edited", "deleted", "moved"} {
		t.Run(mode, func(t *testing.T) {
			d, f, g, p, _ := goalExecutionFixture(t)
			ctx := context.Background()
			var err error
			g, err = d.authorizeGoal(ctx, goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: p})
			if err != nil {
				t.Fatal(err)
			}
			remote := &lostGoalComment{Fake: f, failRead: true}
			d.lin = remote
			now := time.Now().UTC()
			if err = d.syncGoalProgressOne(ctx, g.ID, now); err == nil {
				t.Fatal("expected lost response")
			}
			switch mode {
			case "edited":
				f.GoalComments[0].Body = "human edited this"
			case "deleted":
				f.GoalComments = nil
			case "moved":
				f.GoalComments[0].IssueID = "different-issue"
			}
			remote.failRead = false
			if err = d.syncGoalProgressOne(ctx, g.ID, now.Add(3*time.Minute)); err == nil {
				t.Fatal("changed comment accepted")
			}
			stored, _ := d.goals.Get(g.ID)
			if stored.ProgressUpdates[0].ConfirmedAt.IsZero() == false || stored.State != g.State {
				t.Fatal("projection affected execution")
			}
			if mode == "deleted" && len(f.GoalComments) != 0 {
				t.Fatal("recreated deleted comment")
			}
			if mode == "edited" && f.GoalComments[0].Body != "human edited this" {
				t.Fatal("overwrote human comment")
			}
		})
	}
}
func TestGoalProgressChecksParentAndProjectOptIn(t *testing.T) {
	d, f, g, p, _ := goalExecutionFixture(t)
	ctx := context.Background()
	g, err := d.authorizeGoal(ctx, goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: p})
	if err != nil {
		t.Fatal(err)
	}
	d.cfg.ProjectByName(g.Project).GoalMode = false
	if err = d.syncGoalProgressOne(ctx, g.ID, time.Now().UTC()); err != nil || len(f.GoalComments) > 0 {
		t.Fatal("disabled project narrated", err)
	}
	d.cfg.ProjectByName(g.Project).GoalMode = true
	f.Details[0].ProjectID = "other"
	if err = d.syncGoalProgressOne(ctx, g.ID, time.Now().UTC()); err == nil || len(f.GoalComments) > 0 {
		t.Fatal("followed moved parent", err)
	}
}

func TestGoalProgressControllerPass(t *testing.T) {
	d, f, g, p, _ := goalExecutionFixture(t)
	ctx := context.Background()
	g, err := d.authorizeGoal(ctx, goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: p})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.controlGoal(ctx, goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "pause"})
	if err != nil {
		t.Fatal(err)
	}
	d.runGoals(ctx)
	if len(f.GoalComments) != 1 || !strings.Contains(f.GoalComments[0].Body, "paused") {
		t.Fatal("controller did not project durable state")
	}
	d.runGoals(ctx)
	if len(f.GoalComments) != 1 {
		t.Fatal("controller repeated unchanged update")
	}
}
