package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalcheck"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/linear"
)

type goalBriefChanged struct{ Source goal.BriefSource }

func (e *goalBriefChanged) Error() string {
	return "Linear goal brief changed; inspect goal show, then reconcile the exact proposed brief revision"
}

// Parent placement is still authoritative and may not be accepted as prose.
func checkGoalParent(ctx context.Context, api linear.GoalAPI, g goal.Goal) error {
	detail, err := api.LookupGoalIssue(ctx, g.Parent.ID)
	if err != nil {
		return err
	}
	if detail == nil || detail.ID != g.Parent.ID || detail.TeamID != g.TeamID || detail.ProjectID != g.LinearProjectID || detail.MilestoneID != g.Milestone.ID || detail.ParentID != "" || detail.ArchivedAt != "" || detail.StateType == "canceled" || len(detail.LabelIDs) > 0 {
		return errors.New("goal parent is missing, moved, relabeled, archived or canceled; reconcile its placement")
	}
	title, body := g.ParentIssueText()
	if title != detail.Title || body != detail.Description {
		if !strings.Contains(detail.Description, goal.Marker(g.ID, "parent")) {
			return errors.New("goal parent ownership marker removed")
		}
		source, err := goal.NewBriefSource(detail.Title, detail.Description, time.Now().UTC())
		if err != nil {
			return err
		}
		return &goalBriefChanged{Source: source}
	}
	return nil
}
func captureGoalBrief(g *goal.Goal, cause error) error {
	var changed *goalBriefChanged
	if errors.As(cause, &changed) {
		return g.ObserveBrief(changed.Source)
	}
	return nil
}

// syncGoalBrief is explicitly callable while paused or before authorization.
// It reads Linear and saves only local state, never overwriting human prose.
func (d *Daemon) syncGoalBrief(ctx context.Context, id string) (goal.Goal, error) {
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	g, ok := d.goals.Get(id)
	if !ok {
		return g, errors.New("unknown goal")
	}
	if !g.Parent.Confirmed || g.State == "canceled" || g.State == "accepted" {
		return g, errors.New("goal cannot reconcile a brief in its current state")
	}
	cause := d.goalBindingsCurrent(ctx, g)
	if err := captureGoalBrief(&g, cause); err != nil {
		cause = errors.Join(cause, err)
	}
	if cause != nil {
		g.SyncError = cause.Error()
		if g.Execution != nil {
			if g.State != "paused" {
				g.State = "blocked"
			}
			g.Execution.Reason = cause.Error()
		}
	} else {
		g.PendingBrief = ""
		g.SyncError = ""
	}
	saved, err := d.goals.Save(g)
	if err != nil {
		return g, err
	}
	// A captured proposal is useful data, not a failed socket response.
	return saved, nil
}
func (d *Daemon) acceptGoalBrief(ctx context.Context, a goal.BriefAcceptance) (goal.Goal, error) {
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	g, ok := d.goals.Get(a.ID)
	if !ok {
		return g, errors.New("unknown goal")
	}
	d.goalJobsMu.Lock()
	busy := d.goalJobs[g.ID] != nil
	d.goalJobsMu.Unlock()
	if busy {
		return g, errors.New("goal job is still stopping; wait before reconciling")
	}
	before, _ := d.goals.Get(a.ID)
	if err := g.AcceptBrief(a, time.Now().UTC()); err != nil {
		return g, err
	}
	// Acceptance is tied to both the reviewed local revision and a fresh remote
	// read. A concurrent Linear edit invalidates this request; it is not retried.
	if err := d.goalBindingsCurrent(ctx, g); err != nil {
		return g, fmt.Errorf("brief acceptance did not match current Linear state: %w", err)
	}
	if g.Execution != nil {
		if err := d.validateGoalPolicy(g, g.Execution.Policy); err != nil {
			return g, err
		}
	}
	// Check the durable publication journal, including a lost response before
	// the delivery snapshot was saved. No accepted revision may discard an
	// upstream branch/PR operation that might already have happened.
	if before.Execution != nil && before.Execution.Final != nil {
		m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
		dir, err := m.DeliveryDirectory(before.ID, before.Execution.Final.ID)
		if err != nil {
			return before, err
		}
		if _, err := os.Lstat(filepath.Join(dir, "publication.json")); !errors.Is(err, os.ErrNotExist) {
			return before, errors.New("publication may already have started; retain this goal and reconcile delivery before a new brief")
		}
		if err := d.recoverGoalReviews(ctx, before, m); err != nil {
			return before, err
		}
		d.mu.Lock()
		docker := d.cfg.Sandbox.DockerBin()
		d.mu.Unlock()
		checks := &goalcheck.Runner{Workspaces: m, Docker: docker, Run: d.goalCheckRun}
		for _, spec := range before.Execution.Policy.Checks {
			if _, err := checks.Recover(ctx, before.ID, verificationID(before.Execution.Final.ID, "check", spec.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return before, err
			}
		}
	}
	return d.goals.Save(g)
}
