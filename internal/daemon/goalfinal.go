package daemon

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"time"

	"github.com/sushidev-team/lola/internal/goalwork"
)

func (d *Daemon) verifyGoalOperation(ctx context.Context, id, operation string) error {
	g, ok := d.goals.Get(id)
	if !ok || g.Execution == nil {
		return errors.New("goal no longer exists")
	}
	if r, ok := g.ActiveRefresh(); ok && r.Round.ID == operation {
		return d.verifyGoalRefresh(ctx, id, operation)
	}
	if g.Execution.Final != nil && g.Execution.Final.ID == operation {
		return d.verifyGoalFinal(ctx, id, operation)
	}
	return d.verifyGoalIntegration(ctx, id, operation)
}

func (d *Daemon) verifyGoalFinal(ctx context.Context, id, operation string) error {
	g, ok := d.goals.Get(id)
	if !ok || g.Execution == nil || g.State != "verifying" || g.Execution.Final == nil || g.Execution.Final.ID != operation {
		return errors.New("goal no longer authorizes final verification")
	}
	f := g.Execution.Final
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	manifest := map[string]goalwork.Revision{}
	var candidates []goalwork.Candidate
	for _, member := range slices.Sorted(maps.Keys(f.Manifest)) {
		var c goalwork.Candidate
		var err error
		if len(f.Targets) > 0 {
			c, err = m.FinalCandidateAt(ctx, id, operation, member, f.Manifest[member], f.Targets[member])
		} else {
			c, err = m.FinalCandidate(ctx, id, operation, member, f.Manifest[member])
		}
		if err != nil {
			return err
		}
		candidates = append(candidates, c)
		manifest[member] = goalwork.Revision{Commit: c.Commit}
	}
	scope := goalReviewScope{Operation: operation, Subject: "final", Contract: g.ParentDescription() + "\nEvaluate the complete goal against every acceptance criterion. The diff spans the captured delivery target to the final goal revision.", At: f.CreatedAt}
	if len(g.Execution.Refreshes) > 0 {
		scope.Subject = "final/" + g.Execution.Refreshes[len(g.Execution.Refreshes)-1].ID
		scope.NoRepair = true
	}
	v, checks, err := d.verifyGoalCandidates(ctx, g, scope, candidates, manifest, f.Manifest, m)
	if err != nil {
		return err
	}
	v.Targets = maps.Clone(f.Targets)
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	current, _ := d.goals.Get(id)
	if err := ctx.Err(); err != nil {
		return err
	}
	if current.State != "verifying" || current.Execution == nil || current.Execution.Final == nil || current.Execution.Final.ID != operation || !maps.Equal(current.Execution.Manifest, f.Manifest) || !maps.Equal(current.Execution.Targets, f.Targets) {
		return errors.New("goal changed or paused during final verification")
	}
	path, err := m.RecordFinal(ctx, candidates, v, g.Execution.PolicyDigest, checks, g.Execution.Policy.Profiles)
	if err != nil {
		return err
	}
	if err := current.CompleteFinal(operation, path, time.Now().UTC()); err != nil {
		return err
	}
	_, err = d.goals.Save(current)
	return err
}
