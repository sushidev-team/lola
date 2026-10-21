package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalcheck"
	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/linear"
)

func (d *Daemon) authorizeGoal(ctx context.Context, a goal.Authorization) (goal.Goal, error) {
	g, ok := d.goals.Get(a.ID)
	if !ok {
		return g, errors.New("unknown goal")
	}
	if a.Revision == 0 || g.Revision != a.Revision {
		return g, errors.New("goal changed; review its current revision before authorizing")
	}
	if err := d.validateGoalPolicy(g, a.Policy); err != nil {
		return g, err
	}
	if err := d.goalBindingsCurrent(ctx, g); err != nil {
		return g, err
	}
	if err := g.BeginPreparation(a.Policy, time.Now().UTC()); err != nil {
		return g, err
	}
	if g.PlanningID != "" {
		if d.goalPlans == nil || d.goalPlansErr != nil {
			return g, errors.New("accepted coordinator provenance unavailable")
		}
		r, ok := d.goalPlans.Get(g.PlanningID)
		if !ok || r.State != "accepted" || r.GoalID != g.ID {
			return g, errors.New("goal proposal acceptance is not acknowledged")
		}
		// The accepted proposal was already a paid coordinator run. Include
		// it in the total call budget before any worker can be reserved.
		g.Execution.Runs[r.ID] = goal.AgentRun{ID: r.ID, Kind: "coordinator", Subject: g.ID, ReservedAt: r.StartedAt, FinishedAt: r.FinishedAt}
		if r.Usage != nil {
			if err := g.ObserveUsage(r.ID, *r.Usage); err != nil {
				return g, err
			}
		}
	}
	// This durable authorization is the work queue. No socket deadline can
	// lose the intent or strand initialization halfway through a clone.
	return d.goals.Save(g)
}

func (d *Daemon) controlGoal(ctx context.Context, a goal.ControlRequest) (goal.Goal, error) {
	// Cancel the owned job before waiting for a publication effect's lock. Its
	// durable intent handles an ambiguous remote outcome; pause need not wait
	// for a network timeout. Validate the revision/action against a copy first.
	if a.Action == "pause" || a.Action == "cancel" {
		if snapshot, ok := d.goals.Get(a.ID); ok && a.Revision != 0 && snapshot.Revision == a.Revision {
			if err := snapshot.Control(a.Action, a.Reason, time.Now().UTC()); err == nil {
				d.cancelGoalVerification(a.ID)
			}
		}
	}
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	g, ok := d.goals.Get(a.ID)
	if !ok {
		return g, errors.New("unknown goal")
	}
	if g.Execution == nil {
		return g, errors.New("goal has not been authorized")
	}
	if a.Revision == 0 || g.Revision != a.Revision {
		return g, errors.New("goal changed; reload before steering")
	}
	if a.Action == "resume" || a.Action == "retry-verification" || a.Action == "retry-replan" {
		if err := d.goalDisputeHold(g); err != nil {
			return g, err
		}
		if err := d.validateGoalPolicy(g, g.Execution.Policy); err != nil {
			return g, err
		}
		if err := d.goalBindingsCurrent(ctx, g); err != nil {
			return g, err
		}
		g.SyncError = ""
	}
	if a.Action == "retry-replan" {
		d.goalJobsMu.Lock()
		busy := d.goalJobs[g.ID] != nil
		d.goalJobsMu.Unlock()
		if busy {
			return g, errors.New("coordinator is still stopping; retry after it finishes")
		}
		if err := g.RetryReplan(goal.NewID(), time.Now().UTC()); err != nil {
			return g, err
		}
		g.Execution.Reason = a.Reason
		return d.goals.Save(g)
	}
	if a.Action == "retry-verification" {
		d.goalJobsMu.Lock()
		busy := d.goalJobs[g.ID] != nil
		d.goalJobsMu.Unlock()
		if busy {
			return g, errors.New("verification is still stopping; retry after it has finished")
		}
		m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
		operation := g.Execution.PendingIntegration
		if g.Execution.Final != nil {
			operation = g.Execution.Final.ID
		}
		if r, ok := g.ActiveRefresh(); ok {
			operation = r.Round.ID
		}
		if operation == "" {
			return g, errors.New("no verification to retry")
		}
		d.mu.Lock()
		docker := d.cfg.Sandbox.DockerBin()
		d.mu.Unlock()
		if err := d.recoverGoalReviews(ctx, g, m); err != nil {
			return g, err
		}
		checks := &goalcheck.Runner{Workspaces: m, Docker: docker, Run: d.goalCheckRun}
		for _, check := range g.Execution.Policy.Checks {
			if _, err := checks.Recover(ctx, g.ID, verificationID(operation, "check", check.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return g, err
			}
		}
		if g.Execution.PendingIntegration != "" {
			if err := m.CanRetryIntegration(ctx, g.ID, g.Execution.PendingIntegration, g.Execution.Manifest); err != nil {
				return g, err
			}
		}
		if r, ok := g.ActiveRefresh(); ok {
			if err := m.CanRetryIntegration(ctx, g.ID, r.Round.ID, r.Base); err != nil {
				return g, err
			}
		}
		if err := g.RetryVerification(goal.NewID(), time.Now().UTC()); err != nil {
			return g, err
		}
		g.Execution.Reason = a.Reason
		return d.goals.Save(g)
	}
	if err := g.Control(a.Action, a.Reason, time.Now().UTC()); err != nil {
		return g, err
	}
	saved, err := d.goals.Save(g)
	if err == nil && (a.Action == "pause" || a.Action == "cancel") {
		d.cancelGoalVerification(g.ID)
	}
	return saved, err
}

func (d *Daemon) validateGoalPolicy(g goal.Goal, policy goalpolicy.Policy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	for _, task := range g.Tasks {
		if _, err := goal.TaskScopes(policy, task); err != nil {
			return err
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.cfg.ProjectByName(g.Project)
	if p == nil || !p.GoalMode || !p.Sandboxed() || p.InStack() {
		return errors.New("enable goal_mode on the configured Docker project or owning stack")
	}
	if p.TeamID != g.TeamID || p.ProjectID != g.LinearProjectID {
		return errors.New("goal Linear placement changed")
	}
	wanted := map[string]bool{}
	for _, task := range g.Tasks {
		for _, member := range task.Apps {
			wanted[member] = true
		}
	}
	if !p.IsStack() {
		wanted[p.Name] = true
	}
	if len(wanted) != len(policy.Members) {
		return errors.New("accepted members must exactly cover the published task routing")
	}
	for _, m := range policy.Members {
		member := d.cfg.ProjectByName(m.Project)
		if member == nil || !wanted[m.ID] || m.ID != member.Name || member.IsStack() || (p.IsStack() && member.Stack != p.Name) || (!p.IsStack() && member.Name != p.Name) {
			return fmt.Errorf("policy member %s is not a routed repository of this goal", m.ID)
		}
		target := member.DefaultBranch
		if target == "" {
			target = config.DefaultBranchName
		}
		if filepath.Clean(member.Path) != filepath.Clean(m.Source) || member.Repo != m.Repo || target != m.Target {
			return fmt.Errorf("policy member %s no longer matches its configured checkout, repository or delivery target", m.ID)
		}
	}
	for _, id := range policy.Profiles {
		found := false
		for _, profile := range d.cfg.ReviewProfiles {
			if profile.ID == id && profile.Enabled && (len(profile.Projects) == 0 || slices.Contains(profile.Projects, g.Project)) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("required profile %s is unavailable for this project", id)
		}
	}
	for _, profile := range d.cfg.ReviewProfiles {
		if profile.Enabled && profile.Required && (len(profile.Projects) == 0 || slices.Contains(profile.Projects, g.Project)) && !slices.Contains(policy.Profiles, profile.ID) {
			return fmt.Errorf("policy omits required project profile %s", profile.ID)
		}
	}
	return nil
}

// goalBindingsCurrent performs only reads. Even confirmed bindings are checked
// for archival, cancellation, changed scope or removed dependencies before any
// new execution effect; a human edit never expands the accepted permissions.
func (d *Daemon) goalBindingsCurrent(ctx context.Context, g goal.Goal) error {
	api, err := d.goalAPI()
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var drafts []goal.TaskDraft
	for _, t := range g.Tasks {
		drafts = append(drafts, t.TaskDraft)
	}
	p, err := d.goalPlacement(cctx, api, g.Project, drafts)
	if err != nil {
		return err
	}
	if p.TeamID != g.TeamID || p.ProjectID != g.LinearProjectID {
		return errors.New("goal placement changed")
	}
	if g.Milestone.ID != "" {
		m, err := api.LookupGoalMilestone(cctx, g.Milestone.ID)
		if err != nil {
			return err
		}
		if m == nil || m.ArchivedAt != "" || m.ProjectID != g.LinearProjectID {
			return errors.New("goal milestone missing, archived or moved")
		}
	}
	if err := checkGoalParent(cctx, api, g); err != nil {
		return err
	}
	for _, t := range g.Tasks {
		pendingChild := false
		if r, ok := g.ExecutionReplan(); ok {
			pendingChild = slices.Contains(r.Children, t.ID)
		}
		if pendingChild && !t.Issue.Confirmed {
			continue
		}
		if t.Adoption != nil {
			if err := checkAdoptedIssue(cctx, api, g, t); err != nil {
				return err
			}
			want := adoptionRelation(g, t)
			actual, err := api.LookupGoalRelation(cctx, want.IssueID, want.ID)
			if err != nil {
				return err
			}
			if !t.Adoption.Membership.Confirmed || actual == nil || *actual != want {
				return errors.New("adopted goal membership changed")
			}
		}
		input := linear.IssueCreate{ID: t.Issue.ID, TeamID: g.TeamID, ProjectID: g.LinearProjectID, MilestoneID: g.Milestone.ID, ParentID: g.Parent.ID, Title: t.Title, Description: g.TaskDescription(t), LabelIDs: t.LabelIDs}
		if t.Adoption == nil {
			if _, err := ensureGoalIssue(cctx, api, goal.Binding{ID: t.Issue.ID, Confirmed: true}, input, !g.TaskSatisfied(t.ID)); err != nil {
				return err
			}
		}
		for j, dep := range t.BlockedBy {
			if pendingChild && !t.Relations[j].Confirmed {
				continue
			}
			want := linear.GoalRelation{ID: t.Relations[j].ID, IssueID: g.Tasks[dep-1].Issue.ID, RelatedIssueID: t.Issue.ID, Type: "blocks"}
			actual, err := api.LookupGoalRelation(cctx, want.IssueID, want.ID)
			if err != nil {
				return err
			}
			if actual == nil || *actual != want {
				return errors.New("goal dependencies changed")
			}
		}
	}
	return nil
}
