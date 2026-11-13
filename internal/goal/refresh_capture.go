package goal

import (
	"errors"
	"maps"
	"slices"
	"time"
)

// RefreshCapture retains an abandoned target combination and every check/review
// operation against it. Retargeting spends another accepted refresh, never an
// implicit verification retry or a rewrite of captured evidence.
type RefreshCapture struct {
	Targets      map[string]string `json:"targets"`
	Round        RefreshRound      `json:"round"`
	History      []RefreshRound    `json:"history,omitempty"`
	SupersededAt time.Time         `json:"superseded_at"`
}

func (r Refresh) currentCapture() RefreshCapture {
	return RefreshCapture{Targets: maps.Clone(r.Targets), Round: r.Round, History: slices.Clone(r.History)}
}

func (c RefreshCapture) firstRound() RefreshRound {
	if len(c.History) > 0 {
		return c.History[0]
	}
	return c.Round
}

// CaptureID keys per-target review limits; explicit retries retain this ID.
func (r Refresh) CaptureID() string { return r.currentCapture().firstRound().ID }

func (g Goal) RefreshCount() int {
	if g.Execution == nil {
		return 0
	}
	n := len(g.Execution.Refreshes)
	for _, r := range g.Execution.Refreshes {
		n += len(r.SupersededTargets)
	}
	return n
}

// RetargetRefresh is daemon-only after proving there is no manifest/member
// ref-update intent and all old check containers have stopped. Its caller saves
// this reservation before fetching the newly captured commits or spending calls.
func (g *Goal) RetargetRefresh(operation, id string, targets map[string]string, now time.Time) error {
	if err := g.CanRefresh(operation, now); err != nil {
		return err
	}
	e := g.Execution
	r, _ := g.ActiveRefresh()
	if now.Before(r.Round.CreatedAt) {
		return errors.New("target supersession precedes its current round")
	}
	if g.RefreshCount() >= e.Policy.Limits.TargetRefreshes {
		return errors.New("accepted target refresh limit exhausted")
	}
	if g.verificationIdentityUsed(id) {
		return errors.New("target capture identity already used")
	}
	if len(e.Policy.Profiles) > 0 && len(e.Runs) >= e.Policy.Limits.AgentRuns {
		return errors.New("no remaining agent budget for another target capture")
	}
	if !maps.Equal(e.Manifest, r.Base) || e.PendingIntegration != "" || e.PendingReplan != "" || e.Final != nil {
		return errors.New("target refresh no longer owns the verified base")
	}
	if err := validateManifest(e.Policy, targets); err != nil {
		return err
	}
	if maps.Equal(targets, r.Targets) || maps.Equal(targets, r.PriorTargets) {
		return errors.New("retargeting requires a new delivery target combination")
	}
	archived := r.currentCapture()
	archived.SupersededAt = now
	r.SupersededTargets = append(r.SupersededTargets, archived)
	r.Targets = maps.Clone(targets)
	r.Round = RefreshRound{ID: id, CreatedAt: now}
	r.History = nil
	e.Refreshes[len(e.Refreshes)-1] = r
	e.Reason = "delivery target moved again; previous evidence retained and a new bounded capture reserved"
	return nil
}

func (g Goal) validateRefreshCaptures(r Refresh, ids map[string]bool) error {
	e := g.Execution
	captures := append(slices.Clone(r.SupersededTargets), r.currentCapture())
	if r.ID == "" || r.ID != captures[0].firstRound().ID {
		return errors.New("refresh lost original capture identity")
	}
	for i, c := range captures {
		if err := validateManifest(e.Policy, c.Targets); err != nil {
			return err
		}
		if maps.Equal(c.Targets, r.PriorTargets) || i > 0 && maps.Equal(c.Targets, captures[i-1].Targets) {
			return errors.New("refresh target history contains an unchanged capture")
		}
		if len(c.History)+1 > e.Policy.Limits.ReviewRounds {
			return errors.New("target capture exceeded verification round budget")
		}
		var last time.Time
		for _, round := range append(slices.Clone(c.History), c.Round) {
			if round.ID == "" || ids[round.ID] || round.CreatedAt.Before(e.StartedAt) || !round.CreatedAt.Before(e.Deadline) || round.CreatedAt.Before(last) {
				return errors.New("invalid target verification identity or timestamp")
			}
			ids[round.ID] = true
			last = round.CreatedAt
		}
		if i < len(captures)-1 {
			if c.SupersededAt.Before(last) || !c.SupersededAt.Equal(captures[i+1].firstRound().CreatedAt) {
				return errors.New("target supersession lost its next reservation")
			}
		} else if !c.SupersededAt.IsZero() {
			return errors.New("active target capture is marked superseded")
		}
	}
	return nil
}
