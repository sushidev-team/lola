package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalcheck"
	"github.com/sushidev-team/lola/internal/goalpublish"
	"github.com/sushidev-team/lola/internal/goalwork"
)

// reserveGoalRefresh is entered only on an observed target drift. Read-only
// discovery precedes the durable reservation; fetching, checks and reviews do
// not start until the next controller tick sees that reservation.
func (d *Daemon) reserveGoalRefresh(ctx context.Context, id, final string) error {
	g, ok := d.goals.Get(id)
	if !ok || g.Execution == nil || g.State != "ready" || g.Execution.Final == nil || g.Execution.Final.ID != final {
		return errors.New("target refresh lost its ready goal revision")
	}
	if g.Execution.Policy.Limits.TargetRefreshes == 0 {
		return nil
	}
	if err := d.validateGoalPolicy(g, g.Execution.Policy); err != nil {
		return err
	}
	if err := d.goalBindingsCurrent(ctx, g); err != nil {
		return err
	}
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	p := &goalpublish.Publisher{Workspaces: m, Run: d.goalPublishRun}
	targets, err := p.CaptureTargets(ctx, id, g.Execution.Policy)
	if err != nil {
		return err
	}
	prior := maps.Clone(g.Execution.Targets)
	if len(prior) == 0 {
		prior = map[string]string{}
		for _, member := range g.Execution.Policy.Members {
			r, err := m.Repository(ctx, id, member.ID)
			if err != nil {
				return err
			}
			prior[member.ID] = r.Start
		}
	}
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	current, _ := d.goals.Get(id)
	if err := ctx.Err(); err != nil {
		return err
	}
	if current.Revision != g.Revision {
		return errors.New("goal changed while capturing delivery targets")
	}
	if err := current.ReserveRefresh(goal.NewID(), prior, targets, time.Now().UTC()); err != nil {
		return err
	}
	_, err = d.goals.Save(current)
	return err
}

func (d *Daemon) verifyGoalRefresh(ctx context.Context, id, operation string) error {
	g, ok := d.goals.Get(id)
	r, active := g.ActiveRefresh()
	if !ok || !active || g.State != "verifying" || r.Round.ID != operation || !g.Execution.StopWorkersAt.IsZero() {
		return errors.New("goal no longer authorizes this refresh")
	}
	if err := g.CanRefresh(operation, time.Now().UTC()); err != nil {
		return err
	}
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	p := &goalpublish.Publisher{Workspaces: m, Run: d.goalPublishRun}
	p.Guard = func(ctx context.Context) (func(), error) {
		d.goalMu.Lock()
		current, _ := d.goals.Get(id)
		pending, ok := current.ActiveRefresh()
		if ctx.Err() != nil || !ok || current.State != "verifying" || pending.Round.ID != operation || !current.Execution.StopWorkersAt.IsZero() || current.Execution.PolicyDigest != g.Execution.PolicyDigest || !maps.Equal(pending.Targets, r.Targets) {
			d.goalMu.Unlock()
			return nil, errors.New("refresh stopped by steering or changed authority")
		}
		if err := current.CanRefresh(operation, time.Now().UTC()); err != nil {
			d.goalMu.Unlock()
			return nil, err
		}
		if err := d.validateGoalPolicy(current, current.Execution.Policy); err != nil {
			d.goalMu.Unlock()
			return nil, err
		}
		if err := d.goalBindingsCurrent(ctx, current); err != nil {
			d.goalMu.Unlock()
			return nil, err
		}
		return d.goalMu.Unlock, nil
	}
	// Recovery of a ref intent keeps its original proof. Otherwise detect stale
	// captures before fetching or spending a check/review on them.
	started, err := m.HasIntegrationIntent(id, operation)
	if err != nil {
		return err
	}
	if !started {
		targets, err := p.CaptureTargets(ctx, id, g.Execution.Policy)
		if err != nil {
			return err
		}
		if !maps.Equal(targets, r.Targets) {
			return goalpublish.ErrTargetChanged
		}
	}
	var candidates []goalwork.Candidate
	manifest := map[string]goalwork.Revision{}
	heads := map[string]string{}
	for _, member := range slices.Sorted(maps.Keys(r.Base)) {
		store, err := p.FetchTarget(ctx, id, operation, g.Execution.Policy, member, r.Base[member], r.Targets[member])
		if err != nil {
			return err
		}
		for _, previous := range r.SupersededTargets {
			if err := m.TargetDescendsFrom(ctx, store, previous.Targets[member], r.Targets[member]); err != nil {
				return err
			}
		}
		c, err := m.StageRefresh(ctx, store, r.PriorTargets[member], r.Targets[member])
		if err != nil {
			return err
		}
		candidates = append(candidates, c)
		manifest[member] = goalwork.Revision{Commit: c.Commit, Operation: operation}
		heads[member] = c.Commit
	}
	encoded, _ := json.Marshal(r.Targets)
	scope := goalReviewScope{Operation: operation, Subject: "refresh/" + r.CaptureID(), Contract: g.ParentDescription() + "\nVerify that integrating the delivery target preserves the whole goal and all upstream behavior. Captured delivery targets: " + string(encoded), At: r.Round.CreatedAt, NoRepair: true}
	v, checks, err := d.verifyGoalCandidates(ctx, g, scope, candidates, manifest, heads, m)
	if err != nil {
		return err
	}
	v.Targets = maps.Clone(r.Targets)
	// A partially written manifest must finish with its original proof, even
	// if upstream moved again. A fresh operation may not begin integration with
	// stale targets; the controller may reserve another bounded target capture.
	receiptPath := filepath.Join(m.Root, id, "integrations", operation+".json")
	started, err = m.HasIntegrationIntent(id, operation)
	if err != nil {
		return err
	}
	if !started {
		currentTargets, err := p.CaptureTargets(ctx, id, g.Execution.Policy)
		if err != nil {
			return err
		}
		if !maps.Equal(currentTargets, r.Targets) {
			return goalpublish.ErrTargetChanged
		}
	}
	release, err := p.Guard(ctx)
	if err != nil {
		return err
	}
	defer release()
	current, _ := d.goals.Get(id)
	receipt, err := m.AdvanceManifest(ctx, id, operation, r.Base, candidates, v, g.Execution.PolicyDigest, checks, g.Execution.Policy.Profiles)
	if err != nil {
		return err
	}
	if !receipt.Complete {
		return errors.New("refresh stack update is incomplete")
	}
	if err := current.CompleteRefresh(operation, receiptPath, heads, time.Now().UTC()); err != nil {
		return err
	}
	_, err = d.goals.Save(current)
	return err
}

// retargetGoalRefresh abandons only an operation which has never begun writing
// refs. Old containers must be stopped and the replacement durably saved before
// another controller tick can fetch or review the new target combination.
func (d *Daemon) retargetGoalRefresh(ctx context.Context, id, operation string) error {
	g, ok := d.goals.Get(id)
	if !ok {
		return errors.New("goal no longer exists")
	}
	if err := g.CanRefresh(operation, time.Now().UTC()); err != nil {
		return err
	}
	r, _ := g.ActiveRefresh()
	if g.RefreshCount() >= g.Execution.Policy.Limits.TargetRefreshes {
		return errors.New("accepted target refresh limit exhausted")
	}
	if err := d.validateGoalPolicy(g, g.Execution.Policy); err != nil {
		return err
	}
	if err := d.goalBindingsCurrent(ctx, g); err != nil {
		return err
	}
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	if err := m.CanRetryIntegration(ctx, id, operation, r.Base); err != nil {
		return err
	}
	d.mu.Lock()
	docker := d.cfg.Sandbox.DockerBin()
	d.mu.Unlock()
	if err := d.recoverGoalReviews(ctx, g, m); err != nil {
		return err
	}
	checks := &goalcheck.Runner{Workspaces: m, Docker: docker, Run: d.goalCheckRun}
	for _, check := range g.Execution.Policy.Checks {
		if _, err := checks.Recover(ctx, id, verificationID(operation, "check", check.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	p := &goalpublish.Publisher{Workspaces: m, Run: d.goalPublishRun}
	targets, err := p.CaptureTargets(ctx, id, g.Execution.Policy)
	if err != nil {
		return err
	}
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	current, ok := d.goals.Get(id)
	if !ok || current.Revision != g.Revision {
		return errors.New("goal changed while recapturing delivery targets")
	}
	if err := d.validateGoalPolicy(current, current.Execution.Policy); err != nil {
		return err
	}
	if err := d.goalBindingsCurrent(ctx, current); err != nil {
		return err
	}
	if err := m.CanRetryIntegration(ctx, id, operation, r.Base); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := current.RetargetRefresh(operation, goal.NewID(), targets, time.Now().UTC()); err != nil {
		return err
	}
	_, err = d.goals.Save(current)
	return err
}

func (d *Daemon) recordGoalRefreshError(id string, cause error) {
	d.logf("", "goal %s target refresh: %v", id, cause)
	// Keep a verified ready artifact ready when its refresh budget is exhausted;
	// the delivery error explains why automatic publication is held.
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	g, ok := d.goals.Get(id)
	if !ok || g.Execution == nil || g.State != "ready" || g.Execution.Delivery == nil {
		return
	}
	g.Execution.Delivery.Error = fmt.Sprintf("%v; target refresh: %v", goalpublish.ErrTargetChanged, cause)
	_, _ = d.goals.Save(g)
}
