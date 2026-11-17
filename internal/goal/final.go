package goal

import (
	"errors"
	"maps"
	"time"
)

// FinalVerification is separate from task integration: it evaluates the whole
// goal on the exact final repository combination before human delivery.
type FinalVerification struct {
	ID          string            `json:"id"`
	Manifest    map[string]string `json:"manifest"`
	Targets     map[string]string `json:"targets,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	CompletedAt time.Time         `json:"completed_at,omitempty"`
	Receipt     string            `json:"receipt,omitempty"`
}

func (g *Goal) ReserveFinal(id string, now time.Time) error {
	if err := g.canOperate(now, true); err != nil {
		return err
	}
	e := g.Execution
	if g.State != "verifying" || id == "" || e.PendingIntegration != "" || e.PendingRefresh != "" || e.Final != nil {
		return errors.New("goal is not awaiting a new final verification")
	}
	if !g.AllTasksSatisfied() {
		return errors.New("goal still has unintegrated tasks")
	}
	if g.briefFinalRounds()+len(e.FinalHistory) >= e.Policy.Limits.ReviewRounds {
		return errors.New("whole-goal review budget exhausted")
	}
	if g.verificationIdentityUsed(id) {
		return errors.New("final verification identity already used")
	}
	e.Final = &FinalVerification{ID: id, Manifest: maps.Clone(e.Manifest), Targets: maps.Clone(e.Targets), CreatedAt: now}
	return nil
}

// CompleteFinal is daemon-only, after independently re-reading the final
// receipt. Ready means a verified artifact awaits human delivery, never merged.
func (g *Goal) CompleteFinal(id, receipt string, now time.Time) error {
	if err := g.canOperate(now, true); err != nil {
		return err
	}
	e := g.Execution
	if g.State != "verifying" || e.PendingRefresh != "" || e.Final == nil || e.Final.ID != id || receipt == "" || !maps.Equal(e.Manifest, e.Final.Manifest) || !maps.Equal(e.Targets, e.Final.Targets) {
		return errors.New("final evidence does not match this goal revision")
	}
	if !g.AllTasksSatisfied() {
		return errors.New("goal still has unintegrated tasks")
	}
	e.Final.Receipt = receipt
	e.Final.CompletedAt = now
	g.State = "ready"
	return nil
}
