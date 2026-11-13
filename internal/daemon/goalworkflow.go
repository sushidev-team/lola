package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/linear"
)

func workflowTarget(p config.Project, stage string) (string, string) {
	switch stage {
	case "started":
		return p.OnSpawnStateID, "started"
	case "integrated":
		return p.OnPRStateID, "started"
	case "delivered":
		return p.OnMergedStateID, "completed"
	}
	return "", ""
}

// Runs under the projection mutex. All network effects remain bounded by the
// projection pass; a state write does not unlock execution dependencies.
func (d *Daemon) syncGoalWorkflow(ctx context.Context, id string, now time.Time) error {
	g, ok := d.goals.Get(id)
	if !ok {
		return nil
	}
	var failures []error
	for _, t := range g.Tasks {
		if ctx.Err() != nil {
			break
		}
		if err := d.syncGoalTaskWorkflow(ctx, id, t.ID, now); err != nil {
			failures = append(failures, fmt.Errorf("task %s: %w", t.ID, err))
		}
	}
	if len(failures) > 0 {
		failures = append(failures, d.recordGoalWorkflowError(id, "Linear workflow synchronization is held; inspect the configured states and retained transition before continuing."))
	}
	return errors.Join(failures...)
}
func (d *Daemon) syncGoalTaskWorkflow(ctx context.Context, id, taskID string, now time.Time) error {
	g, ok := d.goals.Get(id)
	if !ok || g.Execution == nil {
		return nil
	}
	d.mu.Lock()
	pp := d.cfg.ProjectByName(g.Project)
	var p config.Project
	if pp != nil {
		p = cloneProject(*pp)
	}
	d.mu.Unlock()
	if pp == nil || !p.GoalMode || p.TeamID != g.TeamID || p.ProjectID != g.LinearProjectID || (p.OnSpawnStateID == "" && p.OnPRStateID == "" && p.OnMergedStateID == "") {
		return nil
	}
	var selected *goal.Task
	var stage, target, typ string
	var pending goal.WorkflowTransition
	for i := range g.Tasks {
		t := g.Tasks[i]
		if t.ID != taskID {
			continue
		}
		if !t.Issue.Confirmed {
			continue
		}
		desired := g.WorkflowStage(t.ID)
		if desired == "" {
			continue
		}
		last, exists := g.LastWorkflow(t.ID)
		if exists && last.ConfirmedAt.IsZero() {
			selected = &t
			stage = last.Stage
			target, typ = workflowTarget(p, stage)
			pending = last
			break
		}
		next, kind := workflowTarget(p, desired)
		if next == "" || (exists && goal.WorkflowRank(last.Stage) >= goal.WorkflowRank(desired)) {
			continue
		}
		selected = &t
		stage, target, typ = desired, next, kind
		break
	}
	if selected == nil {
		return nil
	}
	if target == "" || (pending.TaskID != "" && pending.To != target) {
		return errors.New("configured workflow target differs from the retained intent")
	}
	api, err := d.goalAPI()
	if err != nil {
		return err
	}
	states, err := api.States(ctx, g.TeamID)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(states, func(s linear.State) bool { return s.ID == target && s.Type == typ }) {
		return fmt.Errorf("%s goal workflow state must be an existing team state of type %s", stage, typ)
	}
	if err = checkGoalParent(ctx, api, g); err != nil {
		return err
	}
	actual, err := api.LookupGoalIssue(ctx, selected.Issue.ID)
	if err != nil {
		return err
	}
	if err := workflowTaskCurrent(ctx, api, g, *selected, actual); err != nil {
		return err
	}
	if actual == nil || actual.StateID == "" {
		return errors.New("task workflow state unavailable")
	}
	last, hasLast := g.LastWorkflow(selected.ID)
	if pending.TaskID == "" {
		// Never pull terminal human work back into progress or replace a manually
		// selected started state with another started state on first observation.
		if actual.StateID != target {
			if actual.StateType == "completed" || actual.StateType == "canceled" {
				return errors.New("human terminal task state is preserved")
			}
			if hasLast && actual.StateID != last.To {
				return errors.New("task workflow changed after the previous confirmed transition")
			}
			if !hasLast && !slices.Contains([]string{"triage", "backlog", "unstarted"}, actual.StateType) && actual.StateID != p.OnSpawnStateID && actual.StateID != p.OnPRStateID {
				return errors.New("unrecognized human task workflow state is preserved")
			}
		}
		d.goalMu.Lock()
		current, _ := d.goals.Get(id)
		if current.Revision != g.Revision {
			d.goalMu.Unlock()
			return errors.New("goal changed before workflow reservation")
		}
		err = current.ReserveWorkflow(selected.ID, stage, actual.StateID, target, now)
		if err == nil {
			g, err = d.goals.Save(current)
		}
		d.goalMu.Unlock()
		if err != nil {
			return err
		}
		pending, _ = g.LastWorkflow(selected.ID)
	}
	if actual.StateID == pending.To {
		return d.confirmGoalWorkflow(id, pending, now)
	}
	if !pending.SentAt.IsZero() {
		return errors.New("previous workflow write is unconfirmed or was changed; reconcile in Linear without an automatic retry")
	}
	if actual.StateID != pending.From {
		return errors.New("task workflow changed before send; preserving human choice")
	}
	// Recheck live authority and the remote source immediately before the write.
	// Linear offers no conditional state update; the small read/write race cannot
	// be eliminated by a local journal. Never retry a sent mutation blindly.
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	current, _ := d.goals.Get(id)
	if goal.WorkflowRank(current.WorkflowStage(selected.ID)) < goal.WorkflowRank(stage) {
		return errors.New("workflow stopped by goal steering")
	}
	d.mu.Lock()
	configured := d.cfg.ProjectByName(g.Project)
	stillEnabled := configured != nil && configured.GoalMode
	liveTarget := ""
	if configured != nil {
		liveTarget, _ = workflowTarget(*configured, stage)
		stillEnabled = stillEnabled && configured.TeamID == g.TeamID && configured.ProjectID == g.LinearProjectID
	}
	d.mu.Unlock()
	if !stillEnabled || liveTarget != target {
		return errors.New("workflow configuration changed")
	}
	fresh, err := api.LookupGoalIssue(ctx, selected.Issue.ID)
	if err != nil {
		return err
	}
	if err := workflowTaskCurrent(ctx, api, current, *selected, fresh); err != nil {
		return err
	}
	if fresh == nil || fresh.StateID != pending.From {
		return errors.New("task changed immediately before workflow send")
	}
	index := workflowIndex(current, pending)
	if index < 0 {
		return errors.New("workflow intent changed")
	}
	current.Workflow[index].SentAt = now
	if _, err = d.goals.Save(current); err != nil {
		return err
	}
	// This is the only writer. No labels, issue content, hierarchy or milestone
	// are changed; the target type was verified against this exact team above.
	if err = api.SetIssueState(ctx, selected.Issue.ID, target); err != nil {
		return err
	}
	return nil // next observation confirms the effect, including lost responses
}
func workflowIndex(g goal.Goal, w goal.WorkflowTransition) int {
	for i, v := range g.Workflow {
		if v.TaskID == w.TaskID && v.Stage == w.Stage && v.From == w.From && v.To == w.To {
			return i
		}
	}
	return -1
}
func (d *Daemon) confirmGoalWorkflow(id string, w goal.WorkflowTransition, now time.Time) error {
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	g, _ := d.goals.Get(id)
	i := workflowIndex(g, w)
	if i < 0 {
		return errors.New("workflow intent changed")
	}
	if !g.Workflow[i].ConfirmedAt.IsZero() {
		return nil
	}
	g.Workflow[i].ConfirmedAt = now
	g.WorkflowError = ""
	_, err := d.goals.Save(g)
	return err
}
func (d *Daemon) recordGoalWorkflowError(id, message string) error {
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	g, ok := d.goals.Get(id)
	if !ok || g.WorkflowError == message {
		return nil
	}
	g.WorkflowError = message
	_, err := d.goals.Save(g)
	return err
}

func workflowTaskCurrent(ctx context.Context, api linear.GoalAPI, g goal.Goal, t goal.Task, actual *linear.IssueDetail) error {
	if t.Adoption != nil {
		if err := validateAdoptedDetail(g, t, actual); err != nil {
			return err
		}
		relation, err := api.LookupGoalRelation(ctx, g.Parent.ID, t.Adoption.Membership.ID)
		if err != nil {
			return err
		}
		if relation == nil || *relation != adoptionRelation(g, t) {
			return errors.New("adopted workflow task membership changed")
		}
		return nil
	}
	if actual == nil {
		return errors.New("workflow task is missing")
	}
	labels, want := slices.Clone(actual.LabelIDs), slices.Clone(t.LabelIDs)
	slices.Sort(labels)
	slices.Sort(want)
	if actual.ID != t.Issue.ID || actual.TeamID != g.TeamID || actual.ProjectID != g.LinearProjectID || actual.ParentID != g.Parent.ID || actual.MilestoneID != g.Milestone.ID || actual.ArchivedAt != "" || actual.StateType == "canceled" || actual.Title != t.Title || actual.Description != g.TaskDescription(t) || !slices.Equal(labels, want) {
		return errors.New("workflow task contract, ownership or placement changed")
	}
	return nil
}
