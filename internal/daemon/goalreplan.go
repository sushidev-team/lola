package daemon

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalplan"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/usage"
)

func (d *Daemon) queueGoalReplan(ctx context.Context, g goal.Goal) error {
	d.goalJobsMu.Lock()
	defer d.goalJobsMu.Unlock()
	if d.goalJobs == nil {
		d.goalJobs = map[string]context.CancelFunc{}
	}
	if d.goalJobs[g.ID] != nil {
		return nil
	}
	if !d.beginConnWork() {
		return errors.New("daemon is shutting down")
	}
	job, cancel := context.WithDeadline(ctx, g.Execution.Deadline)
	d.goalJobs[g.ID] = cancel
	go func() {
		defer d.connWg.Done()
		defer cancel()
		defer func() { d.goalJobsMu.Lock(); delete(d.goalJobs, g.ID); d.goalJobsMu.Unlock() }()
		defer func() {
			if v := recover(); v != nil {
				_ = d.holdGoal(g.ID, fmt.Errorf("coordinator interrupted: %v", v), false)
			}
		}()
		if err := d.runGoalReplan(job, g.ID, g.Execution.PendingReplan); err != nil {
			if errors.Is(job.Err(), context.Canceled) {
				return
			}
			current, _ := d.goals.Get(g.ID)
			if current.State == "running" {
				_ = d.holdGoal(g.ID, err, false)
			}
			d.logf("", "goal %s replan: %v", g.ID, err)
		}
	}()
	return nil
}

func (d *Daemon) runGoalReplan(ctx context.Context, id, operation string) error {
	if d.goalPlans == nil || d.goalPlansErr != nil {
		return errors.New("coordinator journal unavailable")
	}
	g, ok := d.goals.Get(id)
	if !ok || g.Execution == nil || g.State != "running" || g.Execution.PendingReplan != operation {
		return errors.New("goal no longer authorizes replanning")
	}
	r := g.Execution.Replans[operation]
	if r.State == "provisioning" {
		return d.publishGoalReplan(ctx, id, operation)
	}
	if r.State != "reserved" {
		return errors.New("coordinator operation is not reserved")
	}
	if !d.goalPlanMu.TryLock() {
		return nil
	}
	defer d.goalPlanMu.Unlock()
	record, exists := d.goalPlans.Get(operation)
	if !exists {
		in, err := d.goalRepairInput(ctx, g, r)
		if err != nil {
			return err
		}
		d.mu.Lock()
		sandbox := d.cfg.Sandbox
		sandbox.PassEnv = slices.Clone(sandbox.PassEnv)
		p := d.cfg.ProjectByName(g.Project)
		kind := d.cfg.AgentForProject(g.Project)
		image := d.cfg.SandboxImageFor(p)
		d.mu.Unlock()
		var fresh bool
		record, fresh, err = d.goalPlans.Reserve(in, kind, image)
		if err != nil {
			return err
		}
		if fresh {
			out, runErr := (goalplan.Runner{Docker: sandbox.DockerBin(), PassEnv: sandbox.PassEnv, Run: d.goalPlanRun, OnUsage: func(o usage.Observation) error {
				if err := d.goalPlans.ObserveUsage(record.ID, o); err != nil {
					return err
				}
				return d.observeGoalRunUsage(g.ID, record.ID, o)
			}}).Plan(ctx, record)
			record, err = d.goalPlans.Finish(record.ID, &out, runErr)
			if err != nil {
				return err
			}
		}
	}
	if record.Usage != nil {
		if err := d.observeGoalRunUsage(g.ID, record.ID, *record.Usage); err != nil {
			return err
		}
	}
	if record.State != "proposed" || record.Output == nil {
		return fmt.Errorf("coordinator result %s: %s; inspect goal proposal %s and explicitly retry-replan if appropriate", record.State, record.Error, record.ID)
	}
	proof := record.Input.Repair
	if proof == nil || record.GoalID != g.ID || proof.GoalID != g.ID || proof.TaskID != r.TaskID || proof.AttemptID != r.AttemptID || proof.PolicyDigest != g.Execution.PolicyDigest || !maps.Equal(proof.Manifest, r.Base) {
		return errors.New("coordinator evidence does not match its reserved execution contract")
	}
	if len(record.Output.Unknowns) > 0 {
		return fmt.Errorf("coordinator needs a decision before changing the task graph: %v", record.Output.Unknowns)
	}
	d.goalMu.Lock()
	current, _ := d.goals.Get(id)
	if err := ctx.Err(); err != nil {
		d.goalMu.Unlock()
		return err
	}
	if current.Execution == nil || current.Execution.PendingReplan != operation || current.State != "running" {
		d.goalMu.Unlock()
		return errors.New("goal steering changed during planning")
	}
	if err := d.validateGoalPolicy(current, current.Execution.Policy); err != nil {
		d.goalMu.Unlock()
		return err
	}
	api, err := d.goalAPI()
	if err == nil {
		_, err = d.goalPlacement(ctx, api, current.Project, record.Output.Draft(record.Input.Request).Tasks)
	}
	if err == nil {
		err = current.Decompose(operation, record.Output.Tasks, time.Now().UTC())
	}
	if err == nil {
		run := current.Execution.Runs[operation]
		run.FinishedAt = record.FinishedAt
		current.Execution.Runs[operation] = run
		_, err = d.goals.Save(current)
	}
	d.goalMu.Unlock()
	if err != nil {
		return err
	}
	return d.publishGoalReplan(ctx, id, operation)
}

func (d *Daemon) goalRepairInput(ctx context.Context, g goal.Goal, r goal.Replan) (goalplan.Input, error) {
	var parent goal.Task
	for _, t := range g.Tasks {
		if t.ID == r.TaskID {
			parent = t
		}
	}
	request := goalplan.Request{ID: r.ID, Project: g.Project, Title: parent.Title, Outcome: g.Outcome, Acceptance: slices.Clone(parent.Acceptance), NonGoals: slices.Clone(g.NonGoals), MaxTasks: min(10, g.Execution.Policy.Limits.MaxTasks-len(g.Tasks)), TimeoutSeconds: 180}
	if err := request.Normalize(); err != nil {
		return goalplan.Input{}, err
	}
	if request.MaxTasks < 2 {
		return goalplan.Input{}, errors.New("no remaining decomposition task budget")
	}
	in := goalplan.Input{Request: request, TeamID: g.TeamID, LinearProjectID: g.LinearProjectID}
	scopes, err := goal.TaskScopes(g.Execution.Policy, parent)
	if err != nil {
		return in, err
	}
	apps := slices.Sorted(maps.Keys(scopes))
	in.Repair = &goalplan.RepairContext{GoalID: g.ID, TaskID: parent.ID, AttemptID: r.AttemptID, PolicyDigest: g.Execution.PolicyDigest, Manifest: maps.Clone(r.Base), GoalOutcome: g.Outcome, GoalAcceptance: slices.Clone(g.Acceptance), Original: goalplan.TaskContext{Contract: g.TaskContract(parent), Apps: apps, LabelIDs: slices.Clone(parent.LabelIDs), Paths: parent.Paths}, Failure: g.Execution.Attempts[r.AttemptID].Error}
	in.Repair.Original.Contract += g.Execution.Policy.RolloutContract()
	// Resolve inherited policy restrictions too: an omitted task map must
	// not tell the coordinator that an accepted narrow member is unrestricted.
	in.Repair.Original.Paths = map[string][]string{}
	for id, scope := range scopes {
		if !scope.All {
			in.Repair.Original.Paths[id] = slices.Clone(scope.Paths)
		}
	}
	d.mu.Lock()
	p := d.cfg.ProjectByName(g.Project)
	if p != nil {
		in.Stack = p.IsStack()
	}
	d.mu.Unlock()
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	for _, id := range apps {
		repo, err := m.Repository(ctx, g.ID, id)
		if err != nil {
			return in, err
		}
		snapshot, err := m.SnapshotOwned(ctx, id, repo.GitDir, repo.Branch, scopes[id])
		if err != nil {
			return in, err
		}
		if snapshot.Commit != r.Base[id] {
			return in, errors.New("goal repository advanced during coordinator snapshot")
		}
		in.Members = append(in.Members, goalplan.Member{ID: id, Snapshot: snapshot})
	}
	api, err := d.goalAPI()
	if err != nil {
		return in, err
	}
	labels, err := api.Labels(ctx, g.TeamID)
	if err != nil {
		return in, err
	}
	workspace, err := api.WorkspaceLabels(ctx)
	if err != nil {
		return in, err
	}
	for _, l := range append(labels, workspace...) {
		if !slices.Contains(parent.LabelIDs, l.ID) {
			continue
		}
		item := goalplan.Label{ID: l.ID, Name: l.Name}
		if l.Parent != nil {
			item.Group = l.Parent.Name
		}
		in.Labels = append(in.Labels, item)
	}
	if d.knowledge != nil {
		in.Knowledge = d.selectKnowledge(ctx, knowledge.Query{Project: g.Project, GoalID: g.ID, Text: parent.Description, MaxBytes: 8000})
	}
	_, err = in.Bytes()
	return in, err
}

// Only new, durably reserved children may be created. Existing original or
// acknowledged resources are checked for drift; missing ones are never recreated.
func (d *Daemon) publishGoalReplan(ctx context.Context, id, operation string) error {
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	g, ok := d.goals.Get(id)
	if !ok || g.Execution == nil || g.State != "running" || g.Execution.PendingReplan != operation {
		return errors.New("goal no longer authorizes replacement publication")
	}
	r := g.Execution.Replans[operation]
	if r.State != "provisioning" {
		return errors.New("replacement identities were not reserved")
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := d.validateGoalPolicy(g, g.Execution.Policy); err != nil {
		return err
	}
	if err := d.goalBindingsCurrent(cctx, g); err != nil {
		return err
	}
	api, err := d.goalAPI()
	if err != nil {
		return err
	}
	save := func() error { var err error; g, err = d.goals.Save(g); return err }
	for i, t := range g.Tasks {
		if !slices.Contains(r.Children, t.ID) {
			continue
		}
		if err := cctx.Err(); err != nil {
			return err
		}
		input := linear.IssueCreate{ID: t.Issue.ID, TeamID: g.TeamID, ProjectID: g.LinearProjectID, MilestoneID: g.Milestone.ID, ParentID: g.Parent.ID, Title: t.Title, Description: g.TaskDescription(t), LabelIDs: t.LabelIDs}
		binding, err := ensureGoalIssue(cctx, api, t.Issue, input)
		if err != nil {
			return err
		}
		g.Tasks[i].Issue = binding
		if err = save(); err != nil {
			return err
		}
	}
	for i, t := range g.Tasks {
		if !slices.Contains(r.Children, t.ID) {
			continue
		}
		for j, dep := range t.BlockedBy {
			if err := cctx.Err(); err != nil {
				return err
			}
			binding := t.Relations[j]
			want := linear.GoalRelation{ID: binding.ID, IssueID: g.Tasks[dep-1].Issue.ID, RelatedIssueID: t.Issue.ID, Type: "blocks"}
			actual, err := api.LookupGoalRelation(cctx, want.IssueID, want.ID)
			if err != nil {
				return err
			}
			if actual == nil {
				if binding.Confirmed {
					return errors.New("replacement dependency removed; refusing recreation")
				}
				createErr := api.CreateGoalRelation(cctx, want)
				actual, err = api.LookupGoalRelation(cctx, want.IssueID, want.ID)
				if err != nil || actual == nil {
					return fmt.Errorf("replacement dependency creation unconfirmed: %w", errors.Join(createErr, err, errors.New("missing authoritative relation")))
				}
			}
			if *actual != want {
				return errors.New("replacement dependency drift")
			}
			g.Tasks[i].Relations[j].Confirmed = true
			if err = save(); err != nil {
				return err
			}
		}
	}
	if err := cctx.Err(); err != nil {
		return err
	}
	if err := d.goalBindingsCurrent(cctx, g); err != nil {
		return err
	}
	if err := g.CompleteReplan(operation, time.Now().UTC()); err != nil {
		return err
	}
	if err := save(); err != nil {
		return err
	}
	if record, ok := d.goalPlans.Get(operation); ok && record.State == "proposed" {
		if _, err := d.goalPlans.Accept(operation, record.Revision); err != nil {
			d.logf("", "goal %s replan acknowledgement: %v (execution journal is complete)", id, err)
		}
	}
	return nil
}
