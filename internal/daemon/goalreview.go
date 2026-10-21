package daemon

import (
	"context"
	"errors"
	"os"
	"slices"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalreview"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/reviewprofile"
	"github.com/sushidev-team/lola/internal/usage"
)

// Goal specialists always use an isolated runner. The host adapter remains
// available to ordinary sessions; it is never a fallback for goal work.
func (d *Daemon) goalReviewRunner(g goal.Goal, profile string, m *goalwork.Manager) (goalreview.Runner, error) {
	p, provider, err := d.profileFor(profile)
	if err != nil {
		return goalreview.Runner{}, err
	}
	kind, ok := config.ReviewAgentFor(p.Provider)
	if !ok {
		return goalreview.Runner{}, errors.New("goal specialist requires an agent provider")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return goalreview.Runner{Workspaces: m, Docker: d.cfg.Sandbox.DockerBin(), Image: d.cfg.SandboxImageFor(d.cfg.ProjectByName(g.Project)), Agent: kind, Model: provider.Model, PassEnv: slices.Clone(d.cfg.Sandbox.PassEnv), Run: d.goalReviewRun, OnUsage: func(id string, o usage.Observation) error { return d.observeGoalRunUsage(g.ID, id, o) }}, nil
}

func (d *Daemon) goalReviewEvidence(g goal.Goal, r reviewprofile.Record, m *goalwork.Manager) error {
	// profileRun is an in-process test seam, never configured in production.
	if d.profileRun != nil || r.State == "not_applicable" {
		return nil
	}
	runner, err := d.goalReviewRunner(g, r.Profile, m)
	if err != nil {
		return err
	}
	for _, member := range g.Execution.Policy.Members {
		id := verificationID(r.Inputs.ContextID, r.Profile, member.ID)
		if runner.Evidence(g.ID, id, r.Key, r.Result) == nil {
			return nil
		}
	}
	return errors.New("specialist result lacks matching completed container evidence")
}

// Stop uncertain runs before granting a fresh verification identity. Recovery
// requires no model credentials and never launches another paid call.
func (d *Daemon) recoverGoalReviews(ctx context.Context, g goal.Goal, m *goalwork.Manager) error {
	d.mu.Lock()
	runner := goalreview.Runner{Workspaces: m, Docker: d.cfg.Sandbox.DockerBin(), Run: d.goalReviewRun}
	d.mu.Unlock()
	for id, run := range g.Execution.Runs {
		if run.Kind != "reviewer" {
			continue
		}
		if err := runner.Recover(ctx, g.ID, id); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
