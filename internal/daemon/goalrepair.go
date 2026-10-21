package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/sushidev-team/lola/internal/goalwork"
)

type goalRepairError struct{ Feedback string }

func (e *goalRepairError) Error() string { return e.Feedback }

func (d *Daemon) repairGoal(ctx context.Context, id, operation, feedback string) error {
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	g, ok := d.goals.Get(id)
	if !ok || g.Execution == nil || g.State != "running" || g.Execution.PendingIntegration != operation {
		return errors.New("goal no longer authorizes repair")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	if err := m.CanRetryIntegration(ctx, id, operation, g.Execution.Manifest); err != nil {
		return err
	}
	if len(feedback) > 16000 {
		feedback = feedback[:16000]
	}
	if err := g.RejectIntegration(operation, feedback, time.Now().UTC()); err != nil {
		return err
	}
	_, err := d.goals.Save(g)
	return err
}
