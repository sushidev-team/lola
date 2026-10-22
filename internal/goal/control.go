package goal

import (
	"errors"
	"slices"
	"time"
)

// Control changes durable intent only. Stopping processes is the daemon's next
// reconciled action, after Save has committed the intent. Workspaces, receipts
// and issue ownership are retained for every action, including cancellation.
func (g *Goal) Control(action, reason string, now time.Time) error {
	e := g.Execution
	if e == nil || now.IsZero() || g.State == "accepted" || g.State == "canceled" {
		return errors.New("goal is not steerable in its current state")
	}
	if len(reason) > 2000 {
		return errors.New("control reason exceeds 2000 bytes")
	}
	switch action {
	case "accept-delivery":
		if err := g.acceptDelivery(reason, now); err != nil {
			return err
		}
	case "pause":
		if !slices.Contains([]string{"preparing", "running", "verifying", "ready", "blocked", "paused"}, g.State) {
			return errors.New("goal cannot pause in its current state")
		}
		g.State = "paused"
	case "resume":
		if !slices.Contains([]string{"paused", "blocked"}, g.State) && e.StopWorkersAt.IsZero() {
			return errors.New("goal is not paused, blocked, or stopping workers")
		}
		verified := e.Final != nil && e.Final.Receipt != ""
		if g.SyncError != "" || g.PendingBrief != "" || (!verified && !now.Before(e.Deadline)) {
			return errors.New("reconcile Linear and runtime budget before resuming")
		}
		if !verified && (e.ObservedTokens >= e.Policy.Limits.Tokens || (e.Policy.Limits.SpendUSD > 0 && e.ObservedSpendUSD >= e.Policy.Limits.SpendUSD)) {
			return errors.New("goal usage budget exhausted")
		}
		for id, progress := range e.Tasks {
			if progress.State == "blocked" && len(progress.AttemptIDs) < e.Policy.Limits.AttemptsPerTask {
				progress.State = "pending"
				e.Tasks[id] = progress
			}
		}
		g.State = "running"
		e.StopWorkersAt = time.Time{}
		if g.AllTasksSatisfied() {
			g.State = "verifying"
			if e.Final != nil && e.Final.Receipt != "" {
				g.State = "ready"
			}
		}
		if !e.Initialized {
			g.State = "preparing"
		}
	case "stop-workers":
		e.StopWorkersAt = now
	case "cancel":
		g.State = "canceled"
		e.StopWorkersAt = now
	default:
		return errors.New("unknown goal control action")
	}
	e.Reason = reason
	return nil
}
