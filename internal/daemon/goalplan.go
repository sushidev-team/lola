package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalplan"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/usage"
)

type goalPlanArgs struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
}

func (d *Daemon) handleGoalPlan(ctx context.Context, req protocol.Request) protocol.Response {
	if d.goalPlans == nil || d.goalPlansErr != nil || d.goals == nil || d.goalsErr != nil {
		return protocol.Response{Error: "goal or proposal store unavailable"}
	}
	if req.Cmd == "goalPropose" {
		a, err := goalplan.DecodeRequest(req.Args)
		if err != nil {
			return protocol.Response{Error: err.Error()}
		}
		r, err := d.proposeGoal(ctx, a)
		if err != nil {
			return protocol.Response{Error: err.Error()}
		}
		return dataResponse(r)
	}
	if req.Cmd == "goalEditPlan" {
		a, err := goalplan.DecodeEdit(req.Args)
		if err != nil {
			return protocol.Response{Error: err.Error()}
		}
		r, err := d.editGoalPlan(ctx, a)
		if err != nil {
			return protocol.Response{Error: err.Error()}
		}
		return dataResponse(r)
	}
	if req.Cmd == "goalProposals" {
		var a struct {
			Project string `json:"project"`
		}
		if err := json.Unmarshal(req.Args, &a); err != nil {
			return protocol.Response{Error: err.Error()}
		}
		return dataResponse(d.goalPlans.List(a.Project))
	}
	var a goalPlanArgs
	if err := json.Unmarshal(req.Args, &a); err != nil {
		return protocol.Response{Error: err.Error()}
	}
	if req.Cmd == "goalProposal" {
		r, ok := d.goalPlans.Get(a.ID)
		if !ok {
			return protocol.Response{Error: "unknown goal proposal"}
		}
		return dataResponse(r)
	}
	g, err := d.acceptGoalPlan(ctx, a)
	if err != nil {
		return protocol.Response{Error: err.Error()}
	}
	return dataResponse(g)
}

func (d *Daemon) goalPlanInput(ctx context.Context, r goalplan.Request) (goalplan.Input, config.SandboxConfig, string, string, error) {
	in := goalplan.Input{Request: r}
	api, err := d.goalAPI()
	if err != nil {
		return in, config.SandboxConfig{}, "", "", err
	}
	p, err := d.goalPlacement(ctx, api, r.Project, nil)
	if err != nil {
		return in, config.SandboxConfig{}, "", "", err
	}
	d.mu.Lock()
	current := d.cfg.ProjectByName(r.Project)
	if current == nil || current.InStack() || !current.GoalMode || !current.Sandboxed() {
		d.mu.Unlock()
		return in, config.SandboxConfig{}, "", "", errors.New("enable goal_mode on the configured Docker project or owning stack before requesting a coordinator")
	}
	members := d.cfg.StackMembers(r.Project)
	if !current.IsStack() {
		members = []config.Project{cloneProject(*current)}
	}
	sandbox := d.cfg.Sandbox
	sandbox.PassEnv = append([]string(nil), sandbox.PassEnv...)
	image := d.cfg.SandboxImageFor(current)
	kind := d.cfg.AgentForProject(r.Project)
	d.mu.Unlock()
	if len(members) < 1 || len(members) > 50 || image == "" {
		return in, sandbox, image, kind, errors.New("coordinator requires 1–50 configured repositories and a sandbox image")
	}
	in.TeamID, in.LinearProjectID, in.Stack = p.TeamID, p.ProjectID, p.IsStack()
	known := map[string]bool{}
	work := &goalwork.Manager{}
	for _, m := range members {
		known[m.Name] = true
		target := m.DefaultBranch
		if target == "" {
			target = config.DefaultBranchName
		}
		snapshot, err := work.Snapshot(ctx, m.Name, m.Path, target, r.ContextPaths[m.Name])
		if err != nil {
			return in, sandbox, image, kind, fmt.Errorf("snapshot %s: %w", m.Name, err)
		}
		in.Members = append(in.Members, goalplan.Member{ID: m.Name, Repo: m.Repo, RoutingLabels: append([]string(nil), m.Labels...), Snapshot: snapshot})
	}
	for id := range r.ContextPaths {
		if !known[id] {
			return in, sandbox, image, kind, fmt.Errorf("context names unknown repository %s", id)
		}
	}
	labels, err := api.Labels(ctx, p.TeamID)
	if err != nil {
		return in, sandbox, image, kind, err
	}
	workspace, err := api.WorkspaceLabels(ctx)
	if err != nil {
		return in, sandbox, image, kind, err
	}
	seen := map[string]bool{}
	for _, l := range append(labels, workspace...) {
		if seen[l.ID] {
			continue
		}
		seen[l.ID] = true
		item := goalplan.Label{ID: l.ID, Name: l.Name}
		if l.Parent != nil {
			item.Group = l.Parent.Name
		}
		in.Labels = append(in.Labels, item)
	}
	sort.Slice(in.Labels, func(i, j int) bool { return in.Labels[i].ID < in.Labels[j].ID })
	if d.knowledge != nil {
		in.Knowledge = d.selectKnowledge(ctx, knowledge.Query{Project: r.Project, Text: r.Outcome, MaxBytes: 8000})
	}
	_, err = in.Bytes()
	return in, sandbox, image, kind, err
}

func (d *Daemon) proposeGoal(ctx context.Context, r goalplan.Request) (goalplan.Record, error) {
	if err := r.Normalize(); err != nil {
		return goalplan.Record{}, err
	}
	if old, ok := d.goalPlans.Get(r.ID); ok {
		if !goalplan.SameRequest(old.Input.Request, r) {
			return old, errors.New("proposal ID already belongs to a different brief")
		}
		return old, nil
	}
	if !d.goalPlanMu.TryLock() {
		return goalplan.Record{}, errors.New("another goal proposal is running; retry this ID after it finishes")
	}
	defer d.goalPlanMu.Unlock()
	cctx, cancel := d.planCtx(ctx, time.Duration(r.TimeoutSeconds+30)*time.Second)
	defer cancel()
	in, sandbox, image, kind, err := d.goalPlanInput(cctx, r)
	if err != nil {
		return goalplan.Record{}, err
	}
	record, fresh, err := d.goalPlans.Reserve(in, kind, image)
	if err != nil || !fresh {
		return record, err
	}
	output, runErr := (goalplan.Runner{Docker: sandbox.DockerBin(), PassEnv: sandbox.PassEnv, Run: d.goalPlanRun, OnUsage: func(o usage.Observation) error { return d.goalPlans.ObserveUsage(record.ID, o) }}).Plan(cctx, record)
	if runErr == nil {
		// The actual dispatch router, not a model approximation, owns routing.
		api, apiErr := d.goalAPI()
		if apiErr != nil {
			runErr = apiErr
		} else {
			_, runErr = d.goalPlacement(cctx, api, r.Project, output.Draft(r).Tasks)
		}
	}
	return d.goalPlans.Finish(record.ID, &output, runErr)
}

func (d *Daemon) acceptGoalPlan(ctx context.Context, a goalPlanArgs) (goal.Goal, error) {
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	r, ok := d.goalPlans.Get(a.ID)
	if !ok {
		return goal.Goal{}, errors.New("unknown proposal")
	}
	if r.Input.Repair != nil {
		return goal.Goal{}, errors.New("execution replans are applied by their owning goal, not accepted as new goals")
	}
	// A crash between the goal commit and proposal acknowledgement cannot
	// mint another goal or require a new model call.
	if g, exists := d.goals.Get(r.GoalID); exists {
		if g.PlanningID != r.ID {
			return goal.Goal{}, errors.New("reserved goal identity is already owned")
		}
		if r.State == "accepted" {
			return g, nil
		}
		if a.Revision == 0 || a.Revision != r.Revision {
			return g, errors.New("proposal changed; review it before accepting")
		}
		_, err := d.goalPlans.Accept(r.ID, r.Revision)
		return g, err
	}
	if a.Revision == 0 || a.Revision != r.Revision || r.State != "proposed" || r.Output == nil {
		return goal.Goal{}, errors.New("review a successful proposal's current revision before accepting")
	}
	cctx, cancel := d.planCtx(ctx, planApplyTimeout)
	defer cancel()
	current, _, _, _, err := d.goalPlanInput(cctx, r.Input.Request)
	if err != nil {
		return goal.Goal{}, err
	}
	if current.TeamID != r.Input.TeamID || current.LinearProjectID != r.Input.LinearProjectID || current.Stack != r.Input.Stack || !reflect.DeepEqual(current.Members, r.Input.Members) || !reflect.DeepEqual(current.Labels, r.Input.Labels) {
		return goal.Goal{}, errors.New("repository commits or routing changed since this proposal; request and review a fresh proposal")
	}
	draft := r.Output.Draft(r.Input.Request)
	api, err := d.goalAPI()
	if err != nil {
		return goal.Goal{}, err
	}
	if _, err = d.goalPlacement(cctx, api, draft.Project, draft.Tasks); err != nil {
		return goal.Goal{}, err
	}
	g, err := goal.New(draft, r.Input.TeamID, r.Input.LinearProjectID)
	if err != nil {
		return g, err
	}
	g.ID, g.PlanningID = r.GoalID, r.ID
	if g.Milestone.ID != "" && g.Milestone.Confirmed {
		m, err := api.LookupGoalMilestone(cctx, g.Milestone.ID)
		if err != nil {
			return g, err
		}
		if m == nil || m.ProjectID != g.LinearProjectID || m.ArchivedAt != "" {
			return g, errors.New("milestone must be active and belong to the configured Linear project")
		}
	}
	g, err = d.goals.Save(g)
	if err != nil {
		return g, err
	}
	_, err = d.goalPlans.Accept(r.ID, r.Revision)
	return g, err
}

// Shares acceptance's goal lock so a crash after local goal creation cannot
// leave a still-proposed record editable while that goal already owns its plan.
func (d *Daemon) editGoalPlan(ctx context.Context, a goalplan.EditRequest) (goalplan.Record, error) {
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	r, ok := d.goalPlans.Get(a.ID)
	if !ok || r.Input.Request.Project != a.Project {
		return goalplan.Record{}, errors.New("proposal unavailable in this project")
	}
	if r.HasEdit(a.EditID) {
		return d.goalPlans.Edit(a)
	}
	if _, exists := d.goals.Get(r.GoalID); exists {
		return goalplan.Record{}, errors.New("proposal already created a goal; its graph cannot be edited here")
	}
	if err := a.Validate(); err != nil {
		return goalplan.Record{}, err
	}
	if r.State != "proposed" || r.Revision != a.Revision || r.Input.Repair != nil {
		return goalplan.Record{}, errors.New("review an unaccepted initial proposal's current revision before editing")
	}
	body, _ := json.Marshal(a.Output)
	if _, err := goalplan.Parse(body, r.Input); err != nil {
		return goalplan.Record{}, err
	}
	cctx, cancel := d.planCtx(ctx, planApplyTimeout)
	defer cancel()
	api, err := d.goalAPI()
	if err != nil {
		return goalplan.Record{}, err
	}
	if _, err = d.goalPlacement(cctx, api, a.Project, a.Output.Draft(r.Input.Request).Tasks); err != nil {
		return goalplan.Record{}, err
	}
	return d.goalPlans.Edit(a)
}
