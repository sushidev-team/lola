package goal

import (
	"errors"
	"time"
)

// RejectIntegration schedules a bounded repair after conclusive negative
// evidence. The daemon first verifies that no integration effect has begun.
// Infrastructure errors and uncertain outcomes are not negative evidence.
func (g *Goal) RejectIntegration(id, feedback string, now time.Time) error {
	if err := g.canAdvance(now); err != nil {
		return err
	}
	e := g.Execution
	i, ok := e.Integrations[id]
	if !ok || e.PendingIntegration != id || i.State != "reserved" || feedback == "" || len(feedback) > 16000 {
		return errors.New("repair requires a pending integration and bounded failure evidence")
	}
	i.State = "failed"
	i.CompletedAt = now
	e.Integrations[id] = i
	e.PendingIntegration = ""
	a := e.Attempts[i.AttemptID]
	a.State = "failed"
	a.Error = feedback
	a.FinishedAt = now
	e.Attempts[a.ID] = a
	p := e.Tasks[a.TaskID]
	p.State = "pending"
	e.Tasks[a.TaskID] = p // eligibility needs the conclusive failure above
	if len(p.AttemptIDs) >= e.Policy.Limits.AttemptsPerTask || len(e.Runs) >= e.Policy.Limits.AgentRuns {
		if e.Policy.Replan && len(g.Tasks)+2 <= e.Policy.Limits.MaxTasks && len(e.Runs) < e.Policy.Limits.AgentRuns {
			return nil
		}
		p.State = "blocked"
		g.State = "blocked"
		e.Reason = "repair budget exhausted: " + feedback
	}
	e.Tasks[a.TaskID] = p
	return nil
}
