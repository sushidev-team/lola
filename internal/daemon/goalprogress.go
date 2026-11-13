package daemon

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/linear"
)

// Narration has its own bounded pass. A failed projection never changes the
// execution state, and cached UI reads never call Linear.
func (d *Daemon) syncGoalProgress(ctx context.Context) {
	if d.goals == nil || d.goalsErr != nil || !d.goalProgressMu.TryLock() {
		return
	}
	defer d.goalProgressMu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, g := range d.goals.List() {
		if cctx.Err() != nil {
			return
		}
		if g.Execution == nil {
			continue
		}
		one, stop := context.WithTimeout(cctx, 3*time.Second)
		if err := d.syncGoalProgressOne(one, g.ID, time.Now().UTC()); err != nil {
			d.logf("", "goal %s progress: %v", g.ID, err)
		}
		stop()
		workflowCtx, workflowStop := context.WithTimeout(cctx, 3*time.Second)
		if err := d.syncGoalWorkflow(workflowCtx, g.ID, time.Now().UTC()); err != nil {
			d.logf("", "goal %s workflow: %v", g.ID, err)
		}
		workflowStop()
	}
}
func (d *Daemon) syncGoalProgressOne(ctx context.Context, id string, now time.Time) error {
	d.goalMu.Lock()
	g, ok := d.goals.Get(id)
	if !ok || g.Execution == nil || !g.Parent.Confirmed {
		d.goalMu.Unlock()
		return nil
	}
	d.mu.Lock()
	p := d.cfg.ProjectByName(g.Project)
	enabled := p != nil && p.GoalMode && p.TeamID == g.TeamID && p.ProjectID == g.LinearProjectID
	d.mu.Unlock()
	if !enabled {
		d.goalMu.Unlock()
		return nil
	}
	_, err := g.ReserveProgress(goal.NewID(), now)
	if err != nil {
		d.goalMu.Unlock()
		return err
	}
	if len(g.ProgressUpdates) == 0 {
		d.goalMu.Unlock()
		return nil
	}
	u := g.ProgressUpdates[len(g.ProgressUpdates)-1]
	if u.Resolved() || (!u.AttemptedAt.IsZero() && now.Sub(u.AttemptedAt) < 2*time.Minute) {
		d.goalMu.Unlock()
		return nil
	}
	u.AttemptedAt = now
	g.ProgressUpdates[len(g.ProgressUpdates)-1] = u
	g, err = d.goals.Save(g)
	d.goalMu.Unlock()
	if err != nil {
		return err
	}
	fail := func(cause error) error {
		return errors.Join(cause, d.finishGoalProgress(id, u.ID, time.Time{}, "Progress could not be confirmed in Linear; retained update needs reconciliation."))
	}
	api, err := d.goalAPI()
	if err != nil {
		return fail(err)
	}
	comments, ok := api.(linear.GoalCommentAPI)
	if !ok {
		return fail(errors.New("Linear client does not support recoverable goal comments"))
	}
	// Progress can explain a changed brief, but must never follow an issue to a
	// different project or recreate an archived/deleted parent.
	parent, err := api.LookupGoalIssue(ctx, g.Parent.ID)
	if err != nil {
		return fail(err)
	}
	if parent == nil || parent.TeamID != g.TeamID || parent.ProjectID != g.LinearProjectID || parent.ArchivedAt != "" || !strings.Contains(parent.Description, goal.Marker(g.ID, "parent")) {
		return fail(errors.New("goal progress parent ownership unavailable"))
	}
	want := linear.GoalComment{ID: u.ID, IssueID: g.Parent.ID, Body: u.Body(g.ID)}
	actual, err := comments.LookupGoalComment(ctx, u.ID)
	if err != nil {
		return fail(err)
	}
	if actual == nil {
		if !u.SentAt.IsZero() {
			return fail(errors.New("previous goal comment send is unresolved or was deleted; refusing to recreate it"))
		}
		// Persist the send intent before the remote effect. A missing result after
		// an attempted send is ambiguous; only observing this exact UUID confirms it.
		d.goalMu.Lock()
		current, exists := d.goals.Get(id)
		if !exists || len(current.ProgressUpdates) == 0 {
			d.goalMu.Unlock()
			return fail(errors.New("goal progress intent disappeared"))
		}
		index := len(current.ProgressUpdates) - 1
		latest := current.ProgressUpdates[index]
		if latest.ID != u.ID || latest.Resolved() || !latest.SentAt.IsZero() {
			d.goalMu.Unlock()
			return errors.New("goal progress intent changed")
		}
		latest.SentAt = now
		current.ProgressUpdates[index] = latest
		_, err = d.goals.Save(current)
		d.goalMu.Unlock()
		if err != nil {
			return err
		}
		createErr := comments.CreateGoalComment(ctx, want)
		actual, err = comments.LookupGoalComment(ctx, u.ID)
		if err != nil || actual == nil {
			return fail(errors.Join(createErr, err, errors.New("goal comment creation unconfirmed")))
		}
	}
	if *actual != want {
		return fail(errors.New("goal progress comment was edited, moved or archived; preserving human content"))
	}
	return d.finishGoalProgress(id, u.ID, now, "")
}
func (d *Daemon) finishGoalProgress(id, update string, confirmed time.Time, message string) error {
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	g, ok := d.goals.Get(id)
	if !ok || len(g.ProgressUpdates) == 0 {
		return errors.New("goal progress intent missing")
	}
	i := len(g.ProgressUpdates) - 1
	if g.ProgressUpdates[i].ID != update {
		return errors.New("goal progress intent changed")
	}
	if g.ProgressUpdates[i].Resolved() {
		return nil
	}
	g.ProgressError = message
	if !confirmed.IsZero() {
		g.ProgressUpdates[i].ConfirmedAt = confirmed
	}
	_, err := d.goals.Save(g)
	return err
}
