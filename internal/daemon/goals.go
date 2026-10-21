package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
)

// goalHold is also applied to marker-bearing issues absent from our store. A
// different LOLA_HOME or an interrupted provisioning pass must not bypass goal
// ownership. A damaged store holds ALL ordinary issue dispatch until repaired.
func (d *Daemon) goalHold(id, description string, relations ...linear.IssueRelations) string {
	if d.goalsErr != nil {
		return "goal reservations unavailable: " + d.goalsErr.Error()
	}
	if goal.Marked(description) || (d.goals != nil && d.goals.OwnsIssue(id)) {
		return "managed by a goal; ordinary dispatch is disabled"
	}
	for _, r := range relations {
		if r.RelationsTruncated {
			return "issue relations incomplete; goal ownership cannot be ruled out"
		}
		for _, link := range r.RelatedIssues {
			if goal.Marked(link.Description) {
				return "related to a goal; ordinary dispatch is disabled"
			}
		}
	}
	return ""
}

func (d *Daemon) goalAPI() (linear.GoalAPI, error) {
	api, err := d.ensureLinear()
	if err != nil {
		return nil, err
	}
	g, ok := api.(linear.GoalAPI)
	if !ok {
		return nil, errors.New("Linear client does not support recoverable goal operations")
	}
	return g, nil
}

func (d *Daemon) handleGoals(ctx context.Context, req protocol.Request) protocol.Response {
	if d.goalsErr != nil || d.goals == nil {
		return protocol.Response{Error: fmt.Sprintf("goal store unavailable: %v", d.goalsErr)}
	}
	if req.Cmd == "goalOverview" {
		var a struct {
			Project string `json:"project"`
		}
		if len(req.Args) > 0 {
			if err := json.Unmarshal(req.Args, &a); err != nil {
				return protocol.Response{Error: err.Error()}
			}
		}
		out := []goal.Overview{}
		now := time.Now().UTC()
		for _, g := range d.goals.List() {
			if a.Project == "" || g.Project == a.Project {
				out = append(out, g.Overview(now))
			}
		}
		return dataResponse(out)
	}
	if req.Cmd == "goals" {
		return dataResponse(d.goals.List())
	}
	if req.Cmd == "goalStart" {
		var a goal.Authorization
		if err := json.Unmarshal(req.Args, &a); err != nil {
			return protocol.Response{Error: err.Error()}
		}
		g, err := d.authorizeGoal(ctx, a)
		if err != nil {
			return protocol.Response{Error: err.Error()}
		}
		return dataResponse(g)
	}
	if req.Cmd == "goalDismissProgress" {
		var a goal.ProgressResolution
		if err := json.Unmarshal(req.Args, &a); err != nil {
			return protocol.Response{Error: err.Error()}
		}
		d.goalProgressMu.Lock()
		defer d.goalProgressMu.Unlock()
		d.goalMu.Lock()
		defer d.goalMu.Unlock()
		g, ok := d.goals.Get(a.ID)
		if !ok {
			return protocol.Response{Error: "unknown goal"}
		}
		if err := g.DismissProgress(a, time.Now().UTC()); err != nil {
			return protocol.Response{Error: err.Error()}
		}
		saved, err := d.goals.Save(g)
		if err != nil {
			return protocol.Response{Error: err.Error()}
		}
		return dataResponse(saved)
	}
	if req.Cmd == "goalAcceptBrief" {
		var a goal.BriefAcceptance
		if err := json.Unmarshal(req.Args, &a); err != nil {
			return protocol.Response{Error: err.Error()}
		}
		g, err := d.acceptGoalBrief(ctx, a)
		if err != nil {
			return protocol.Response{Error: err.Error()}
		}
		return dataResponse(g)
	}
	if req.Cmd == "goalControl" {
		var a goal.ControlRequest
		if err := json.Unmarshal(req.Args, &a); err != nil {
			return protocol.Response{Error: err.Error()}
		}
		g, err := d.controlGoal(ctx, a)
		if err != nil {
			return protocol.Response{Error: err.Error()}
		}
		return dataResponse(g)
	}
	if req.Cmd == "goalDeliveryEvidence" {
		var a goal.DeliveryEvidenceRequest
		if err := json.Unmarshal(req.Args, &a); err != nil {
			return protocol.Response{Error: err.Error()}
		}
		g, err := d.setGoalDeliveryEvidence(a)
		if err != nil {
			return protocol.Response{Error: err.Error()}
		}
		return dataResponse(g)
	}
	if req.Cmd == "goalCreate" {
		var draft goal.Draft
		if err := json.Unmarshal(req.Args, &draft); err != nil {
			return protocol.Response{Error: err.Error()}
		}
		g, err := d.createGoal(ctx, draft)
		if err != nil {
			return protocol.Response{Error: err.Error()}
		}
		return dataResponse(g)
	}
	var a protocol.GoalArgs
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return protocol.Response{Error: err.Error()}
	}
	if req.Cmd == "goalShow" || req.Cmd == "goalUsage" {
		g, ok := d.goals.Get(a.ID)
		if !ok {
			return protocol.Response{Error: "unknown goal " + a.ID}
		}
		if req.Cmd == "goalUsage" {
			return dataResponse(d.goalUsageReport(ctx, g))
		}
		return dataResponse(g)
	}
	if req.Cmd == "goalSyncBrief" {
		g, err := d.syncGoalBrief(ctx, a.ID)
		if err != nil {
			return protocol.Response{Error: err.Error()}
		}
		return dataResponse(g)
	}
	g, err := d.publishGoal(ctx, a.ID)
	if err != nil {
		return protocol.Response{Error: err.Error()}
	}
	return dataResponse(g)
}

func (d *Daemon) createGoal(ctx context.Context, draft goal.Draft) (goal.Goal, error) {
	if err := goal.ValidateDraft(draft); err != nil {
		return goal.Goal{}, err
	}
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	api, err := d.goalAPI()
	if err != nil {
		return goal.Goal{}, err
	}
	cctx, cancel := d.planCtx(ctx, planApplyTimeout)
	defer cancel()
	p, err := d.goalPlacement(cctx, api, draft.Project, draft.Tasks)
	if err != nil {
		return goal.Goal{}, err
	}
	g, err := goal.New(draft, p.TeamID, p.ProjectID)
	if err != nil {
		return goal.Goal{}, err
	}
	if err := d.captureGoalAdoptions(cctx, api, &g); err != nil {
		return goal.Goal{}, err
	}
	if g.Milestone.ID != "" && g.Milestone.Confirmed {
		m, err := api.LookupGoalMilestone(cctx, g.Milestone.ID)
		if err != nil {
			return goal.Goal{}, err
		}
		if m == nil || m.ProjectID != g.LinearProjectID || m.ArchivedAt != "" {
			return goal.Goal{}, errors.New("milestone must be active and belong to the configured Linear project")
		}
	}
	return d.goals.Save(g)
}

// goalPlacement reuses SelectApps, including always-included members. Explicit
// task scopes are checked against the actual labels; parent labels are never
// copied to children. Re-run before each publishing pass after config reloads.
func (d *Daemon) goalPlacement(ctx context.Context, api linear.API, project string, tasks []goal.TaskDraft) (config.Project, error) {
	d.mu.Lock()
	pp := d.cfg.ProjectByName(project)
	var p config.Project
	if pp != nil {
		p = cloneProject(*pp)
	}
	members := d.cfg.StackMembers(project)
	d.mu.Unlock()
	if pp == nil || p.TeamID == "" || p.ProjectID == "" {
		return p, errors.New("configure an existing Linear team_id and project_id on the goal's project or stack")
	}
	projects, err := api.Projects(ctx, p.TeamID)
	if err != nil {
		return p, err
	}
	if !slices.ContainsFunc(projects, func(v linear.Project) bool { return v.ID == p.ProjectID }) {
		return p, errors.New("configured Linear project is not available to this team")
	}
	labels, err := api.Labels(ctx, p.TeamID)
	if err != nil {
		return p, err
	}
	workspace, err := api.WorkspaceLabels(ctx)
	if err != nil {
		return p, err
	}
	labels = append(labels, workspace...)
	byID := map[string]linear.Label{}
	for _, l := range labels {
		byID[l.ID] = l
	}
	for i, t := range tasks {
		var names, groups []string
		seen := map[string]bool{}
		for _, id := range t.LabelIDs {
			l, ok := byID[id]
			if !ok || seen[id] {
				return p, fmt.Errorf("task %d has an unavailable or duplicate label UUID %q", i+1, id)
			}
			seen[id] = true
			names = append(names, l.Name)
			group := ""
			if l.Parent != nil {
				group = l.Parent.Name
			}
			groups = append(groups, group)
		}
		want := slices.Clone(t.Apps)
		slices.Sort(want)
		if !p.IsStack() {
			if len(want) != 0 && !slices.Equal(want, []string{p.Name}) {
				return p, fmt.Errorf("task %d selects repositories outside this project", i+1)
			}
			continue
		}
		var got []string
		for _, m := range config.SelectApps(members, t.LabelIDs, names, groups) {
			got = append(got, m.Name)
		}
		slices.Sort(got)
		if len(got) == 0 || !slices.Equal(want, got) {
			return p, fmt.Errorf("task %d labels select %v; apps must declare exactly these members (including always-included members)", i+1, got)
		}
	}
	return p, nil
}

func (d *Daemon) publishGoal(ctx context.Context, id string) (out goal.Goal, retErr error) {
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	g, ok := d.goals.Get(id)
	if !ok {
		return out, fmt.Errorf("unknown goal %s", id)
	}
	if g.Execution != nil {
		return g, errors.New("an authorized goal uses execution reconciliation; republishing cannot replace its contract")
	}
	// Record sync failure without losing successful bindings. A retry first
	// re-reads every resource, including confirmed ones, and never recreates a
	// confirmed resource somebody deleted or moved.
	defer func() {
		if retErr != nil {
			if err := captureGoalBrief(&g, retErr); err != nil {
				retErr = errors.Join(retErr, err)
			}
			g.SyncError = retErr.Error()
			if saved, err := d.goals.Save(g); err == nil {
				out = saved
			} else {
				retErr = errors.Join(retErr, err)
			}
		}
	}()
	api, err := d.goalAPI()
	if err != nil {
		return g, err
	}
	cctx, cancel := d.planCtx(ctx, planApplyTimeout)
	defer cancel()
	var drafts []goal.TaskDraft
	for _, t := range g.Tasks {
		drafts = append(drafts, t.TaskDraft)
	}
	p, err := d.goalPlacement(cctx, api, g.Project, drafts)
	if err != nil {
		return g, err
	}
	if p.TeamID != g.TeamID || p.ProjectID != g.LinearProjectID {
		return g, errors.New("goal's configured Linear placement changed; reconcile before publishing")
	}
	g.State, g.SyncError = "provisioning", ""
	save := func() error {
		saved, err := d.goals.Save(g)
		if err == nil {
			g = saved
		}
		return err
	}
	if err := save(); err != nil {
		return g, err
	}
	if g.Milestone.ID != "" {
		m, err := api.LookupGoalMilestone(cctx, g.Milestone.ID)
		if err != nil {
			return g, err
		}
		if m == nil {
			if g.Milestone.Confirmed {
				return g, errors.New("goal milestone is missing; refusing to recreate it")
			}
			input := linear.GoalMilestone{ID: g.Milestone.ID, ProjectID: g.LinearProjectID, Name: g.MilestoneName,
				Description: goal.Marker(g.ID, "milestone") + "\nGoal brief: https://linear.app/issue/" + g.Parent.ID}
			createErr := api.CreateGoalMilestone(cctx, input)
			m, err = api.LookupGoalMilestone(cctx, g.Milestone.ID)
			if err != nil {
				return g, errors.Join(createErr, err)
			}
			if m == nil {
				return g, fmt.Errorf("milestone creation unconfirmed: %v", createErr)
			}
		}
		if m.ProjectID != g.LinearProjectID || m.ArchivedAt != "" || (g.MilestoneName != "" && !strings.Contains(m.Description, goal.Marker(g.ID, "milestone"))) {
			return g, errors.New("goal milestone moved, archived, or has a different owner")
		}
		g.Milestone.Confirmed = true
		if err := save(); err != nil {
			return g, err
		}
	}
	if g.Parent.Confirmed {
		if err := checkGoalParent(cctx, api, g); err != nil {
			return g, err
		}
	}
	parentTitle, parentBody := g.ParentIssueText()
	parent := linear.IssueCreate{ID: g.Parent.ID, TeamID: g.TeamID, ProjectID: g.LinearProjectID,
		MilestoneID: g.Milestone.ID, Title: parentTitle, Description: parentBody}
	b, err := ensureGoalIssue(cctx, api, g.Parent, parent)
	if err != nil {
		return g, err
	}
	g.Parent = b
	if err := save(); err != nil {
		return g, err
	}
	for i := range g.Tasks {
		t := g.Tasks[i]
		if t.Adoption != nil {
			if err := checkAdoptedIssue(cctx, api, g, t); err != nil {
				return g, err
			}
			b, err := ensureGoalRelation(cctx, api, t.Adoption.Membership, adoptionRelation(g, t))
			if err != nil {
				return g, err
			}
			g.Tasks[i].Adoption.Membership = b
			if err := save(); err != nil {
				return g, err
			}
			continue
		}
		input := linear.IssueCreate{ID: t.Issue.ID, TeamID: g.TeamID, ProjectID: g.LinearProjectID,
			MilestoneID: g.Milestone.ID, ParentID: g.Parent.ID, Title: t.Title, Description: g.TaskDescription(t), LabelIDs: t.LabelIDs}
		b, err := ensureGoalIssue(cctx, api, t.Issue, input)
		if err != nil {
			return g, fmt.Errorf("task %d: %w", i+1, err)
		}
		g.Tasks[i].Issue = b
		if err := save(); err != nil {
			return g, err
		}
	}
	for i, t := range g.Tasks {
		for j, dep := range t.BlockedBy {
			binding := t.Relations[j]
			input := linear.GoalRelation{ID: binding.ID, IssueID: g.Tasks[dep-1].Issue.ID, RelatedIssueID: t.Issue.ID, Type: "blocks"}
			r, err := api.LookupGoalRelation(cctx, input.IssueID, input.ID)
			if err != nil {
				return g, err
			}
			if r == nil {
				if binding.Confirmed {
					return g, errors.New("goal dependency removed; reconcile before continuing")
				}
				createErr := api.CreateGoalRelation(cctx, input)
				r, err = api.LookupGoalRelation(cctx, input.IssueID, input.ID)
				if err != nil {
					return g, errors.Join(createErr, err)
				}
				if r == nil {
					return g, fmt.Errorf("dependency creation unconfirmed: %v", createErr)
				}
			}
			if *r != input {
				return g, errors.New("goal dependency changed")
			}
			g.Tasks[i].Relations[j].Confirmed = true
			if err := save(); err != nil {
				return g, err
			}
		}
	}
	g.State = "planned"
	if err := save(); err != nil {
		return g, err
	}
	return g, nil
}

func ensureGoalIssue(ctx context.Context, api linear.GoalAPI, b goal.Binding, input linear.IssueCreate, holdCompleted ...bool) (goal.Binding, error) {
	detail, err := api.LookupGoalIssue(ctx, b.ID)
	if err != nil {
		return b, err
	}
	if detail == nil {
		if b.Confirmed {
			return b, fmt.Errorf("goal issue %s is missing; refusing to recreate it", b.ID)
		}
		_, _, createErr := api.CreateIssue(ctx, input)
		detail, err = api.LookupGoalIssue(ctx, b.ID)
		if err != nil {
			return b, errors.Join(createErr, err)
		}
		if detail == nil {
			return b, fmt.Errorf("issue creation unconfirmed: %v", createErr)
		}
	}
	if len(holdCompleted) > 0 && holdCompleted[0] && detail.StateType == "completed" {
		return b, errors.New("Linear task marked completed before verified integration; reconcile before continuing")
	}
	labels, wantLabels := slices.Clone(detail.LabelIDs), slices.Clone(input.LabelIDs)
	slices.Sort(labels)
	slices.Sort(wantLabels)
	if detail.ID != b.ID || detail.TeamID != input.TeamID || detail.ProjectID != input.ProjectID ||
		detail.ParentID != input.ParentID || detail.MilestoneID != input.MilestoneID ||
		detail.Title != input.Title || detail.Description != input.Description || !slices.Equal(labels, wantLabels) ||
		detail.ArchivedAt != "" || detail.StateType == "canceled" {
		return b, fmt.Errorf("goal issue %s changed or is archived/canceled; reconcile the human edit before continuing", b.ID)
	}
	return goal.Binding{ID: b.ID, Identifier: detail.Identifier, Confirmed: true}, nil
}
