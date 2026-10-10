package daemon

// plan.go is the orchestrator pass (`lola plan`): decompose one large Linear
// issue into ordered sub-issues that dispatch then runs in dependency order.
//
// It is two commands on purpose, because the plan is untrusted model output
// that ends up as tasks for coding agents:
//
//   - cmd=plan runs ONE bounded headless claude (internal/planner) and returns
//     the proposal. It writes nothing anywhere.
//   - cmd=planApply is the human's confirmation. It re-validates whatever steps
//     it is handed (the CLI lets the human edit them first), creates them as
//     sub-issues of the parent, and links every dependency as a "blocks"
//     relation.
//
// The sub-issues are PLACED so the chosen project's poll matches them — team,
// project, cycle, assignee, workflow state and trigger labels are derived from
// the poll's filter, mirroring linear.BuildIssueFilter — which is the whole
// trick: no new dispatch path exists. The existing tick picks them up, and
// dependencyHold (dispatch.go) holds each one until its blockers are finished
// or merged, and holds the parent while any sub-issue is open.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/brain"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/planner"
	"github.com/sushidev-team/lola/internal/protocol"
)

const (
	// planTimeout bounds the planning claude. It stays under the CLI client's
	// 5-minute socket deadline so the answer always reaches the human.
	planTimeout = 4 * time.Minute
	// planApplyTimeout bounds the whole create pass (≤ MaxSteps issues plus
	// their relations, each with the client's own retry/backoff).
	planApplyTimeout = 2 * time.Minute
)

// planSummarizer returns the planning exec seam: the test override when set,
// else a fresh bounded claude honoring [brain].model. The planner is NOT gated
// on [brain].enabled — `lola plan` is itself the explicit opt-in.
func (d *Daemon) planSummarizer() planner.Summarize {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.planSummarize != nil {
		return d.planSummarize
	}
	cl := &brain.Client{Model: d.cfg.Brain.Model, Timeout: planTimeout}
	if !cl.Available() {
		return nil
	}
	return cl.Summarize
}

// planCtx derives a bounded context from the shutdown-cancellable root, so a
// dropped client cannot abort a half-created plan but shutdown still does.
func (d *Daemon) planCtx(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	base := d.shutdownCtx
	if base == nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, timeout)
}

// handlePlan serves cmd=plan: read the issue, run the planner, return the
// proposal. Nothing is created.
func (d *Daemon) handlePlan(ctx context.Context, a protocol.PlanArgs) (protocol.PlanData, error) {
	issue := strings.TrimSpace(a.Issue)
	if issue == "" {
		return protocol.PlanData{}, errors.New("plan: issue required")
	}
	api, err := d.ensureLinear()
	if err != nil {
		return protocol.PlanData{}, err
	}
	cctx, cancel := d.planCtx(ctx, planTimeout+30*time.Second)
	defer cancel()

	parent, err := api.IssueDetail(cctx, issue)
	if err != nil {
		return protocol.PlanData{}, d.linearErr(fmt.Sprintf("read %s", issue), err)
	}
	if parent.Children > 0 {
		return protocol.PlanData{}, fmt.Errorf("%s already has %d sub-issue(s); it is decomposed", parent.Identifier, parent.Children)
	}
	steps, err := planner.Plan(cctx, d.planSummarizer(), parent.Identifier, parent.Title, parent.Description)
	if err != nil {
		return protocol.PlanData{}, fmt.Errorf("plan %s: %w", parent.Identifier, err)
	}
	d.logf("", "plan: proposed %d sub-issue(s) for %s", len(steps), parent.Identifier)
	return protocol.PlanData{Issue: parent.Identifier, Title: parent.Title, Steps: steps}, nil
}

// handlePlanApply serves cmd=planApply: create the confirmed steps as
// sub-issues and link their dependencies. Creation cannot be rolled back
// atomically, so a failure part-way reports exactly what already exists.
func (d *Daemon) handlePlanApply(ctx context.Context, a protocol.PlanApplyArgs) (protocol.PlanApplyData, error) {
	issue := strings.TrimSpace(a.Issue)
	if issue == "" {
		return protocol.PlanApplyData{}, errors.New("planApply: issue required")
	}
	steps, err := planner.Validate(a.Steps)
	if err != nil {
		return protocol.PlanApplyData{}, err
	}
	api, err := d.ensureLinear()
	if err != nil {
		return protocol.PlanApplyData{}, err
	}
	cctx, cancel := d.planCtx(ctx, planApplyTimeout)
	defer cancel()

	parent, err := api.IssueDetail(cctx, issue)
	if err != nil {
		return protocol.PlanApplyData{}, d.linearErr(fmt.Sprintf("read %s", issue), err)
	}
	// The guard against a double apply: a second run would create a second set
	// of sub-issues, and both would dispatch.
	if parent.Children > 0 {
		return protocol.PlanApplyData{}, fmt.Errorf("%s already has %d sub-issue(s); not creating another set", parent.Identifier, parent.Children)
	}

	p, err := d.planProject(a.Project, parent.TeamID)
	if err != nil {
		return protocol.PlanApplyData{}, err
	}
	activeCycle, viewerID := "", ""
	if p.CycleMode == "active" {
		c, _, err := api.Cycles(cctx, p.TeamID)
		if err != nil {
			return protocol.PlanApplyData{}, d.linearErr("resolve active cycle", err)
		}
		if c == nil {
			return protocol.PlanApplyData{}, errors.New("no active cycle for team")
		}
		activeCycle = c.ID
	}
	if p.AssigneeMode == "me" {
		if viewerID, err = d.viewer(cctx, api); err != nil {
			return protocol.PlanApplyData{}, d.linearErr("resolve viewer", err)
		}
	}
	tmpl := subIssuePlacement(p, parent, activeCycle, viewerID)

	res := protocol.PlanApplyData{Parent: parent.Identifier, Project: p.Name}
	ids := make([]string, 0, len(steps))
	partial := func(stage string, err error) (protocol.PlanApplyData, error) {
		if len(res.Created) > 0 {
			stage += fmt.Sprintf(" (already created: %s)", strings.Join(res.Created, ", "))
		}
		return res, d.linearErr(stage, err)
	}
	// A created step is visible to the poll at once, so it must never match the
	// filter before its blockers are linked: a tick in that gap would dispatch it
	// unblocked. Each step is therefore linked right after it is created (its
	// blockers are earlier steps, so they exist), and a blocked step is created
	// WITHOUT the trigger labels, which are added only once its links exist. A
	// failure part-way leaves such a step unlabeled — undispatchable, not early.
	for i, s := range steps {
		in := tmpl
		in.LabelIDs = slices.Clone(tmpl.LabelIDs)
		gated := len(s.BlockedBy) > 0 && len(p.MatchLabels) > 0
		if gated {
			in.LabelIDs = ApplyLabelDelta(tmpl.LabelIDs, p.MatchLabels, nil)
		}
		in.Title = s.Title
		in.Description = stepDescription(s, i+1, len(steps), parent.Identifier)
		id, ident, err := api.CreateIssue(cctx, in)
		if err != nil {
			return partial(fmt.Sprintf("create step %d", i+1), err)
		}
		ids = append(ids, id)
		res.Created = append(res.Created, ident)
		for _, b := range s.BlockedBy {
			if err := api.CreateBlocksRelation(cctx, ids[b-1], id); err != nil {
				return partial(fmt.Sprintf("link %s blocked by %s", ident, res.Created[b-1]), err)
			}
		}
		if gated {
			if err := api.SetIssueLabels(cctx, id, tmpl.LabelIDs); err != nil {
				return partial(fmt.Sprintf("add trigger labels to %s", ident), err)
			}
		}
	}

	// The parent's work now lives in its sub-issues. dependencyHold keeps it out
	// of dispatch while any is open; dropping its trigger labels keeps it out
	// once they are all done (label/seen dedup only — a state-mode parent is
	// left to the human, since moving it would be a workflow decision).
	if p.DedupMode != "state" && len(p.MatchLabels) > 0 && len(intersectLabels(p.MatchLabels, parent.LabelIDs)) > 0 {
		if err := api.SetIssueLabels(cctx, parent.ID, ApplyLabelDelta(parent.LabelIDs, p.MatchLabels, nil)); err != nil {
			res.Message = fmt.Sprintf("could not remove the trigger labels from %s: %v", parent.Identifier, err)
		}
	}
	d.logf(p.Name, "plan: created %d sub-issue(s) of %s: %s", len(res.Created), parent.Identifier, strings.Join(res.Created, ", "))
	return res, nil
}

// linearErr wraps a Linear failure, dropping the cached client on an auth
// error so the next call re-resolves the key.
func (d *Daemon) linearErr(stage string, err error) error {
	if isAuthErr(err) {
		d.invalidateLinear()
	}
	return fmt.Errorf("%s: %w", stage, err)
}

// planProject resolves the [[project]] whose poll should dispatch the
// sub-issues: the named one, or else the single project on the parent's team.
// It returns a copy, so the config lock is not held while Linear is called.
func (d *Daemon) planProject(name, teamID string) (config.Project, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if name = strings.TrimSpace(name); name != "" {
		pp := d.cfg.ProjectByName(name)
		if pp == nil {
			return config.Project{}, fmt.Errorf("unknown project %q", name)
		}
		if pp.TeamID != teamID {
			return config.Project{}, fmt.Errorf("project %q polls a different Linear team than the issue", name)
		}
		return cloneProject(*pp), nil
	}
	var found []config.Project
	for _, p := range d.cfg.Projects {
		if p.TeamID != "" && p.TeamID == teamID {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 1:
		return cloneProject(found[0]), nil
	case 0:
		return config.Project{}, errors.New("no project polls this issue's Linear team; pass --project")
	default:
		names := make([]string, len(found))
		for i, p := range found {
			names[i] = p.Name
		}
		return config.Project{}, fmt.Errorf("several projects poll this issue's team (%s); pass --project", strings.Join(names, ", "))
	}
}

func cloneProject(p config.Project) config.Project {
	p.StateIDs = slices.Clone(p.StateIDs)
	p.MatchLabels = slices.Clone(p.MatchLabels)
	return p
}

// subIssuePlacement derives where a sub-issue must live for p's poll to match
// it — the inverse of linear.BuildIssueFilter, field by field — falling back
// to the parent's placement wherever the filter leaves a field open.
func subIssuePlacement(p config.Project, parent linear.IssueDetail, activeCycleID, viewerID string) linear.IssueCreate {
	in := linear.IssueCreate{
		TeamID:    parent.TeamID,
		ParentID:  parent.ID,
		ProjectID: parent.ProjectID,
		CycleID:   parent.CycleID,
	}
	if p.ProjectID != "" {
		in.ProjectID = p.ProjectID
	}
	switch p.CycleMode {
	case "active":
		in.CycleID = activeCycleID
	case "pinned":
		in.CycleID = p.CycleID
	}
	switch p.AssigneeMode {
	case "me":
		in.AssigneeID = viewerID
	case "user":
		in.AssigneeID = p.AssigneeUserID
	default:
		in.AssigneeID = parent.AssigneeID
	}
	// A state filter must be satisfied: keep the parent's state when it already
	// matches, else the poll's first state. Without one, the team default.
	if len(p.StateIDs) > 0 {
		in.StateID = p.StateIDs[0]
		if slices.Contains(p.StateIDs, parent.StateID) {
			in.StateID = parent.StateID
		}
	}
	// Carry the parent's labels, minus the ones that mark a dispatched or
	// blocked issue, plus EVERY trigger label (satisfies match_mode any and all).
	in.LabelIDs = ApplyLabelDelta(parent.LabelIDs, []string{p.OnSentSetLabel, p.BlockedLabelID}, p.MatchLabels)
	return in
}

// stepDescription is the sub-issue body: the step's own text plus a footer
// pointing the implementing agent at the bigger picture.
func stepDescription(s protocol.PlanStep, n, total int, parent string) string {
	var b strings.Builder
	b.WriteString(s.Description)
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "---\n_Step %d of %d of %s, planned by lola._", n, total, parent)
	if len(s.BlockedBy) > 0 {
		deps := make([]string, len(s.BlockedBy))
		for i, x := range s.BlockedBy {
			deps[i] = fmt.Sprint(x)
		}
		fmt.Fprintf(&b, " _Builds on step(s) %s._", strings.Join(deps, ", "))
	}
	return b.String()
}
