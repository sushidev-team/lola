package goal

import (
	"errors"
	"time"
)

// RetryVerification reserves a fresh identity after explicit human recovery.
// The daemon must first prove no ref-update intent exists for the abandoned
// operation. Existing outputs, failed checks and reviewer records are retained.
func (g *Goal) RetryVerification(id string, now time.Time) error {
	e := g.Execution
	if e == nil || (g.State != "paused" && g.State != "blocked") || g.SyncError != "" || id == "" {
		return errors.New("verification retry requires a reconciled paused or blocked goal")
	}
	if !now.Before(e.Deadline) || e.ObservedTokens >= e.Policy.Limits.Tokens || (e.Policy.Limits.SpendUSD > 0 && e.ObservedSpendUSD >= e.Policy.Limits.SpendUSD) {
		return errors.New("goal budget exhausted")
	}
	if e.PendingRefresh != "" {
		return g.retryRefresh(id, now)
	}
	if e.Final != nil {
		if e.Final.Receipt != "" || g.briefFinalRounds()+len(e.FinalHistory)+1 >= e.Policy.Limits.ReviewRounds {
			return errors.New("final verification retry budget reached or result already complete")
		}
		for _, f := range e.FinalHistory {
			if f.ID == id {
				return errors.New("verification identity reused")
			}
		}
		if e.Final.ID == id {
			return errors.New("verification identity reused")
		}
		e.FinalHistory = append(e.FinalHistory, *e.Final)
		e.Final = nil
		g.State = "verifying"
		return g.ReserveFinal(id, now)
	}
	i, ok := e.Integrations[e.PendingIntegration]
	if !ok || i.State != "reserved" {
		return errors.New("no pending verification to retry")
	}
	if _, exists := e.Integrations[id]; exists {
		return errors.New("verification identity reused")
	}
	rounds := 0
	for _, old := range e.Integrations {
		if old.AttemptID == i.AttemptID {
			rounds++
		}
	}
	if rounds >= e.Policy.Limits.ReviewRounds {
		return errors.New("task verification retry budget reached")
	}
	i.State = "failed"
	i.CompletedAt = now
	e.Integrations[i.ID] = i
	e.PendingIntegration = ""
	a := e.Attempts[i.AttemptID]
	a.State = "submitted"
	e.Attempts[a.ID] = a
	g.State = "running"
	_, err := g.ReserveIntegration(id, a.ID, now)
	return err
}
