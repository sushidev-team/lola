package daemon

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalcheck"
	"github.com/sushidev-team/lola/internal/goalpublish"
	"github.com/sushidev-team/lola/internal/goalwork"
)

func (d *Daemon) queueGoalPublication(ctx context.Context, g goal.Goal) error {
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
	jobCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	d.goalJobs[g.ID] = cancel
	go func() {
		defer d.connWg.Done()
		defer cancel()
		defer func() { d.goalJobsMu.Lock(); delete(d.goalJobs, g.ID); d.goalJobsMu.Unlock() }()
		defer func() {
			if v := recover(); v != nil {
				d.recordGoalPublication(g.ID, goalpublish.Record{}, fmt.Errorf("publication interrupted: %v", v))
			}
		}()
		r, err := d.publishVerifiedGoal(jobCtx, g.ID)
		if errors.Is(jobCtx.Err(), context.Canceled) {
			return
		}
		d.recordGoalPublication(g.ID, r, err)
		if errors.Is(err, goalpublish.ErrTargetChanged) {
			if refreshErr := d.reserveGoalRefresh(jobCtx, g.ID, r.Request.Operation); refreshErr != nil {
				d.recordGoalRefreshError(g.ID, refreshErr)
			}
		}
	}()
	return nil
}

func (d *Daemon) publishVerifiedGoal(ctx context.Context, id string) (goalpublish.Record, error) {
	g, ok := d.goals.Get(id)
	if !ok || g.Execution == nil || g.State != "ready" || g.Execution.Final == nil || !g.Execution.Policy.PublishPR {
		return goalpublish.Record{}, errors.New("goal is not ready and authorized for publication")
	}
	if err := d.validateGoalPolicy(g, g.Execution.Policy); err != nil {
		return goalpublish.Record{}, err
	}
	if err := d.goalBindingsCurrent(ctx, g); err != nil {
		return goalpublish.Record{}, err
	}
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	v, err := d.readyGoalEvidence(ctx, g, m)
	if err != nil {
		return goalpublish.Record{}, err
	}
	want := goalpublish.Request{Goal: id, Operation: g.Execution.Final.ID, Policy: g.Execution.Policy, Manifest: maps.Clone(g.Execution.Manifest), Targets: maps.Clone(g.Execution.Targets), Title: "feat(goal): " + strings.Join(strings.Fields(g.Title), " "), Body: goalPRBody(g, v)}
	if len(g.Execution.Refreshes) > 0 {
		want.PreviousOperation = g.Execution.Refreshes[len(g.Execution.Refreshes)-1].Final.ID
	}
	p := &goalpublish.Publisher{Workspaces: m, Run: d.goalPublishRun, Guard: func(ctx context.Context) (func(), error) {
		d.goalMu.Lock()
		current, _ := d.goals.Get(id)
		if ctx.Err() != nil || current.State != "ready" || current.Execution == nil || !current.Execution.Policy.PublishPR || current.Execution.PolicyDigest != g.Execution.PolicyDigest || current.Execution.DeliveryEvidenceURL != g.Execution.DeliveryEvidenceURL || current.Execution.Final == nil || current.Execution.Final.ID != want.Operation || !maps.Equal(current.Execution.Manifest, want.Manifest) || !maps.Equal(current.Execution.Targets, want.Targets) {
			d.goalMu.Unlock()
			return nil, errors.New("publication stopped by goal steering or changed authority")
		}
		if err := d.validateGoalPolicy(current, current.Execution.Policy); err != nil {
			d.goalMu.Unlock()
			return nil, err
		}
		if err := d.goalBindingsCurrent(ctx, current); err != nil {
			d.goalMu.Unlock()
			return nil, err
		}
		if _, err := d.readyGoalEvidence(ctx, current, m); err != nil {
			d.goalMu.Unlock()
			return nil, err
		}
		return d.goalMu.Unlock, nil
	}}
	return p.Publish(ctx, want)
}

// Re-read check receipts, artifact hashes and exact rubric records. A stored
// ready label alone never grants permission to publish changed evidence.
func (d *Daemon) readyGoalEvidence(ctx context.Context, g goal.Goal, m *goalwork.Manager) (goalwork.Verification, error) {
	e := g.Execution
	f := e.Final
	var checks []string
	refs := map[string]string{}
	manifest := map[string]goalwork.Revision{}
	for member, head := range e.Manifest {
		manifest[member] = goalwork.Revision{Commit: head}
	}
	for _, spec := range e.Policy.Checks {
		checks = append(checks, spec.ID)
		refs[spec.ID] = verificationID(f.ID, "check", spec.ID)
	}
	v, err := m.VerifyFinalAt(ctx, g.ID, f.ID, e.PolicyDigest, e.Manifest, e.Targets, checks, e.Policy.Profiles)
	if err != nil {
		return v, err
	}
	runner := &goalcheck.Runner{Workspaces: m}
	if _, err := runner.Evidence(g.ID, f.ID, e.PolicyDigest, manifest, e.Policy.Checks, refs); err != nil {
		return v, err
	}
	needsSharedEvidence := false
	for _, spec := range e.Policy.Checks {
		for _, a := range spec.Artifacts {
			if a.Kind == "screenshot" {
				needsSharedEvidence = true
			}
		}
	}
	if needsSharedEvidence && g.Execution.DeliveryEvidenceURL == "" {
		return v, errors.New("share the captured screenshots as an artifact and attach its HTTPS URL with goal delivery-evidence before publication")
	}
	for _, result := range v.Reviews {
		if d.profileReviews == nil {
			return v, errors.New("specialist evidence unavailable")
		}
		for _, key := range strings.Split(result.Artifact, ",") {
			r, ok := d.profileReviews.Get(key)
			if !ok || r.Profile != result.ID || r.Inputs.ContextID != f.ID || (r.State != "pass" && r.State != "not_applicable") {
				return v, errors.New("final specialist evidence changed")
			}
			if err := d.goalReviewEvidence(g, r, m); err != nil {
				return v, err
			}
			p, _, err := d.profileFor(r.Profile)
			if err != nil || p.Key(r.Inputs) != key {
				return v, errors.New("final specialist rubric changed")
			}
		}
	}
	return v, nil
}

func (d *Daemon) recordGoalPublication(id string, r goalpublish.Record, cause error) {
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	g, ok := d.goals.Get(id)
	if !ok || g.Execution == nil || g.Execution.Final == nil {
		return
	}
	var changedBrief *goalBriefChanged
	if errors.As(cause, &changedBrief) {
		_ = d.holdGoal(id, cause, false)
		return
	}
	result := goal.Delivery{Operation: g.Execution.Final.ID, State: "publishing", Members: map[string]goal.DeliveryMember{}, CheckedAt: time.Now().UTC()}
	if g.Execution.Delivery != nil {
		result.Members = maps.Clone(g.Execution.Delivery.Members)
	}
	all, changed, closed := r.Complete, false, false
	for id, m := range r.Members {
		result.Members[id] = goal.DeliveryMember{Repo: m.Repo, Target: m.Target, Head: m.Head, State: m.State, Number: m.Number, URL: m.URL, Draft: m.Draft}
		if m.State != "unchanged" {
			changed = true
		}
		if m.State != "merged" && m.State != "unchanged" {
			all = false
		}
		if m.State == "closed" {
			closed = true
		}
	}
	if r.Complete {
		result.State = "published"
	}
	if all && changed {
		result.State = "merged"
	} else if closed {
		result.State = "closed"
	}
	if cause != nil {
		if err := captureGoalBrief(&g, cause); err != nil {
			d.logf("", "goal brief capture: %v", err)
		}
		if g.PendingBrief != "" {
			g.SyncError = cause.Error()
			if g.State != "paused" && g.State != "canceled" && g.State != "accepted" {
				g.State = "blocked"
			}
		}
		result.State = "error"
		result.Error = cause.Error()
	}
	if err := g.ObserveDelivery(result); err != nil {
		d.logf("", "goal %s publication observation: %v", id, err)
		return
	}
	if _, err := d.goals.Save(g); err != nil {
		d.logf("", "goal %s publication persistence: %v", id, err)
	}
}

func (d *Daemon) setGoalDeliveryEvidence(a goal.DeliveryEvidenceRequest) (goal.Goal, error) {
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	g, ok := d.goals.Get(a.ID)
	if !ok || g.Execution == nil || g.Execution.Final == nil || g.State != "ready" || a.Revision == 0 || a.Revision != g.Revision {
		return g, errors.New("attach delivery evidence to the current ready goal revision")
	}
	if err := goal.ValidateEvidenceURL(a.URL); err != nil {
		return g, err
	}
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	dir, err := m.DeliveryDirectory(g.ID, g.Execution.Final.ID)
	if err != nil {
		return g, err
	}
	if _, err := os.Lstat(filepath.Join(dir, "publication.json")); err == nil {
		return g, errors.New("publication already reserved its PR description; preserve it and share additional evidence on the PR")
	} else if !errors.Is(err, os.ErrNotExist) {
		return g, err
	}
	g.Execution.DeliveryEvidenceURL = a.URL
	if g.Execution.Delivery != nil {
		g.Execution.Delivery.CheckedAt = time.Time{}
	}
	return d.goals.Save(g)
}

func goalPRBody(g goal.Goal, v goalwork.Verification) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## TL;DR\n\n%s\n\nGoal: %s. This draft contains a verified goal revision for human review and merge.\n\n## Changes\n\n", prExcerpt(g.Outcome, 4000), prExcerpt(g.Parent.Identifier, 64))
	for _, t := range g.Tasks[:min(len(g.Tasks), 20)] {
		fmt.Fprintf(&b, "- %s (%s)\n", prExcerpt(t.Title, 160), prExcerpt(t.Issue.Identifier, 64))
	}
	if len(g.Tasks) > 20 {
		fmt.Fprintf(&b, "- %d further tasks are recorded in the goal brief.\n", len(g.Tasks)-20)
	}
	b.WriteString("\n## Risk assessment\n\n- Automated checks establish only the declared acceptance coverage; domain correctness and rollout need human review.\n- Review cross-repository compatibility before merging. No automatic merge or deployment is authorized.\n- Use normal repository rollback procedures if delivery regresses behavior.\n\n## Test plan\n\n### Automated\n\n")
	for _, c := range g.Execution.Policy.Checks[:min(len(g.Execution.Policy.Checks), 20)] {
		fmt.Fprintf(&b, "- Acceptance check `%s` passed on the final repository combination. Exact commands and image digests are retained in Lola's verification receipt.\n", c.ID)
	}
	for _, r := range v.Reviews[:min(len(v.Reviews), 20)] {
		fmt.Fprintf(&b, "- Specialist `%s`: accepted final evidence (including explicit applicability checks).\n", r.ID)
	}
	b.WriteString("\n### Manual checks\n\n")
	b.WriteString("This checklist summarizes the goal. Review the complete acceptance contract and all check results in Lola before merging.\n\n")
	for _, t := range g.Tasks[:min(len(g.Tasks), 10)] {
		fmt.Fprintf(&b, "#### %s\n\n", prExcerpt(t.Title, 160))
		for _, criterion := range t.Acceptance[:min(len(t.Acceptance), 3)] {
			fmt.Fprintf(&b, "- [ ] %s\n", prExcerpt(criterion, 200))
		}
		b.WriteString("\n")
	}
	b.WriteString("## Repository combination\n\nReview and coordinate all changed repositories before merging. The configured order below is the proposed human merge order; confirm compatibility for your rollout.\n\n")
	for _, m := range g.Execution.Policy.Members {
		fmt.Fprintf(&b, "- [%s](https://github.com/%s/pulls?q=%s): `%s`, targeting `%s`.\n", m.ID, m.Repo, url.QueryEscape("is:pr head:goal/"+g.ID), g.Execution.Manifest[m.ID], prExcerpt(m.Target, 80))
		if v.Targets[m.ID] != "" {
			fmt.Fprintf(&b, "  Verified with target commit `%s`.\n", v.Targets[m.ID])
		}
	}
	if g.Execution.DeliveryEvidenceURL != "" {
		fmt.Fprintf(&b, "\n## Screenshots and runtime evidence\n\n[Shared verification artifact](<%s>). Confirm its sharing permissions before requesting review.\n", g.Execution.DeliveryEvidenceURL)
	}
	fmt.Fprintf(&b, "\nVerification: `%s`. Policy: `%s`.\n", v.ID, g.Execution.PolicyDigest)
	return b.String()
}

func prExcerpt(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "…"
}
