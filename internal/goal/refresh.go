package goal

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"time"
)

type RefreshRound struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

// Refresh retains the preceding deliverable and every verification identity.
// Targets are captured before fetching. Only a complete stack receipt can move
// the execution manifest; the separate final pass still gates publication.
type Refresh struct {
	ID                  string              `json:"id"`
	Round               RefreshRound        `json:"round"`
	History             []RefreshRound      `json:"history,omitempty"`
	Base                map[string]string   `json:"base"`
	PriorTargets        map[string]string   `json:"prior_targets"`
	Targets             map[string]string   `json:"targets"`
	SupersededTargets   []RefreshCapture    `json:"superseded_targets,omitempty"`
	Manifest            map[string]string   `json:"manifest,omitempty"`
	Receipt             string              `json:"receipt,omitempty"`
	CompletedAt         time.Time           `json:"completed_at,omitempty"`
	Final               FinalVerification   `json:"final"`
	FinalHistory        []FinalVerification `json:"final_history,omitempty"`
	Delivery            *Delivery           `json:"delivery,omitempty"`
	DeliveryEvidenceURL string              `json:"delivery_evidence_url,omitempty"`
}

func (g Goal) ActiveRefresh() (Refresh, bool) {
	if g.Execution != nil {
		for _, r := range g.Execution.Refreshes {
			if r.ID == g.Execution.PendingRefresh && r.Receipt == "" {
				return r, true
			}
		}
	}
	return Refresh{}, false
}

func (g *Goal) ReserveRefresh(id string, prior, targets map[string]string, now time.Time) error {
	e := g.Execution
	if e == nil || g.State != "ready" || e.Final == nil || e.Final.Receipt == "" || e.PendingRefresh != "" || e.PendingIntegration != "" || e.PendingReplan != "" || !e.StopWorkersAt.IsZero() || id == "" {
		return errors.New("target refresh requires a ready goal with no active work")
	}
	if e.Policy.Limits.TargetRefreshes == 0 || g.RefreshCount() >= e.Policy.Limits.TargetRefreshes {
		return errors.New("automatic target refresh is disabled or its accepted limit is exhausted")
	}
	copy := *g
	copy.State = "verifying"
	if err := copy.canOperate(now, true); err != nil {
		return err
	}
	if len(e.Policy.Profiles) > 0 && len(e.Runs) >= e.Policy.Limits.AgentRuns {
		return errors.New("no remaining agent budget to verify a target refresh")
	}
	if !g.AllTasksSatisfied() || !maps.Equal(e.Final.Manifest, e.Manifest) || !maps.Equal(e.Final.Targets, e.Targets) {
		return errors.New("refresh lacks current whole-goal verification")
	}
	for _, a := range e.Attempts {
		if activeAttempt(a.State) {
			return errors.New("refresh cannot overlap a worker attempt")
		}
	}
	for _, m := range []map[string]string{prior, targets} {
		if err := validateManifest(e.Policy, m); err != nil {
			return err
		}
	}
	if maps.Equal(prior, targets) || (len(e.Targets) > 0 && !maps.Equal(e.Targets, prior)) {
		return errors.New("refresh requires changed targets relative to the verified delivery bases")
	}
	if g.verificationIdentityUsed(id) {
		return errors.New("refresh identity already used")
	}
	e.Refreshes = append(e.Refreshes, Refresh{ID: id, Round: RefreshRound{ID: id, CreatedAt: now}, Base: maps.Clone(e.Manifest), PriorTargets: maps.Clone(prior), Targets: maps.Clone(targets), Final: *e.Final, FinalHistory: e.FinalHistory, Delivery: e.Delivery, DeliveryEvidenceURL: e.DeliveryEvidenceURL})
	e.PendingRefresh = id
	e.Final, e.FinalHistory, e.Delivery = nil, nil, nil
	e.DeliveryEvidenceURL = ""
	e.Reason = "delivery target advanced; verifying the refreshed repository combination"
	g.State = "verifying"
	return nil
}

func (g *Goal) CompleteRefresh(operation, receipt string, manifest map[string]string, now time.Time) error {
	if err := g.CanRefresh(operation, now); err != nil {
		return err
	}
	r, ok := g.ActiveRefresh()
	if !ok || g.State != "verifying" || r.Round.ID != operation || receipt == "" || !maps.Equal(g.Execution.Manifest, r.Base) || !g.Execution.StopWorkersAt.IsZero() {
		return errors.New("refresh completion does not match pending authority")
	}
	if err := validateManifest(g.Execution.Policy, manifest); err != nil {
		return err
	}
	r.Manifest, r.Receipt, r.CompletedAt = maps.Clone(manifest), receipt, now
	e := g.Execution
	e.Refreshes[len(e.Refreshes)-1] = r
	e.Manifest, e.Targets = maps.Clone(manifest), maps.Clone(r.Targets)
	e.PendingRefresh = ""
	e.Reason = "refreshed targets integrated; whole-goal verification pending"
	return nil
}

func (g Goal) CanRefresh(operation string, now time.Time) error {
	if err := g.canOperate(now, true); err != nil {
		return err
	}
	r, ok := g.ActiveRefresh()
	if !ok || g.State != "verifying" || r.Round.ID != operation || !g.Execution.StopWorkersAt.IsZero() {
		return errors.New("goal no longer authorizes this refresh operation")
	}
	return nil
}

func (g *Goal) retryRefresh(id string, now time.Time) error {
	r, ok := g.ActiveRefresh()
	if !ok || g.verificationIdentityUsed(id) || len(r.History)+1 >= g.Execution.Policy.Limits.ReviewRounds {
		return errors.New("refresh verification retry limit reached or identity reused")
	}
	r.History = append(r.History, r.Round)
	r.Round = RefreshRound{ID: id, CreatedAt: now}
	g.Execution.Refreshes[len(g.Execution.Refreshes)-1] = r
	g.Execution.StopWorkersAt = time.Time{}
	g.State = "verifying"
	return nil
}

func (g Goal) verificationIdentityUsed(id string) bool {
	e := g.Execution
	if e == nil || id == "" {
		return true
	}
	for _, r := range g.BriefRevisions {
		for _, f := range r.ArchivedFinals {
			if f.ID == id {
				return true
			}
		}
	}
	if _, ok := e.Integrations[id]; ok {
		return true
	}
	if _, ok := e.Runs[id]; ok {
		return true
	}
	if _, ok := e.Replans[id]; ok {
		return true
	}
	if e.Final != nil && e.Final.ID == id {
		return true
	}
	for _, f := range e.FinalHistory {
		if f.ID == id {
			return true
		}
	}
	for _, r := range e.Refreshes {
		if r.ID == id || r.Round.ID == id || r.Final.ID == id {
			return true
		}
		for _, h := range r.History {
			if h.ID == id {
				return true
			}
		}
		for _, capture := range r.SupersededTargets {
			if capture.Round.ID == id {
				return true
			}
			for _, h := range capture.History {
				if h.ID == id {
					return true
				}
			}
		}
		for _, h := range r.FinalHistory {
			if h.ID == id {
				return true
			}
		}
	}
	return false
}

func (g Goal) validateRefreshes() error {
	e := g.Execution
	if g.RefreshCount() > e.Policy.Limits.TargetRefreshes {
		return errors.New("refresh history exceeds accepted limit")
	}
	if len(e.Targets) > 0 {
		if err := validateManifest(e.Policy, e.Targets); err != nil {
			return err
		}
	}
	ids := map[string]bool{}
	var lastManifest, lastTargets map[string]string
	for i, r := range e.Refreshes {
		if err := g.validateRefreshCaptures(r, ids); err != nil {
			return err
		}
		for _, m := range []map[string]string{r.Base, r.PriorTargets, r.Targets} {
			if err := validateManifest(e.Policy, m); err != nil {
				return err
			}
		}
		if maps.Equal(r.PriorTargets, r.Targets) || i > 0 && (!maps.Equal(lastManifest, r.Base) || !maps.Equal(lastTargets, r.PriorTargets)) {
			return errors.New("refresh history lost its verified predecessor")
		}
		if r.Final.ID == "" || ids[r.Final.ID] || r.Final.CreatedAt.IsZero() || r.Final.Receipt == "" || r.Final.CompletedAt.IsZero() || r.Final.CompletedAt.Before(r.Final.CreatedAt) || !maps.Equal(r.Final.Manifest, r.Base) || !maps.Equal(r.Final.Targets, lastTargets) {
			return errors.New("refresh lost prior final evidence")
		}
		ids[r.Final.ID] = true
		if len(r.FinalHistory)+1 > e.Policy.Limits.ReviewRounds {
			return errors.New("archived final retries exceeded policy")
		}
		for _, f := range r.FinalHistory {
			if f.ID == "" || ids[f.ID] || f.CreatedAt.IsZero() || f.Receipt != "" || !f.CompletedAt.IsZero() || !maps.Equal(f.Manifest, r.Base) || !maps.Equal(f.Targets, lastTargets) {
				return errors.New("invalid archived final retry")
			}
			ids[f.ID] = true
		}
		if r.DeliveryEvidenceURL != "" {
			if err := ValidateEvidenceURL(r.DeliveryEvidenceURL); err != nil {
				return err
			}
		}
		if r.Delivery != nil {
			previous := *e
			previous.Manifest = r.Base
			previous.Final = &r.Final
			copy := g
			copy.Execution = &previous
			if _, err := copy.validateDelivery(*r.Delivery); err != nil {
				return err
			}
		}
		if r.Receipt == "" {
			if i != len(e.Refreshes)-1 || e.PendingRefresh != r.ID || !r.CompletedAt.IsZero() || len(r.Manifest) != 0 || !maps.Equal(e.Manifest, r.Base) || !maps.Equal(e.Targets, lastTargets) || e.Final != nil || e.Delivery != nil || e.PendingIntegration != "" || e.PendingReplan != "" || !slices.Contains([]string{"verifying", "paused", "blocked", "canceled", "failed"}, g.State) {
				return errors.New("invalid pending refresh barrier")
			}
		} else {
			if r.CompletedAt.IsZero() || r.CompletedAt.Before(r.Round.CreatedAt) || e.PendingRefresh == r.ID {
				return errors.New("invalid refresh completion")
			}
			if err := validateManifest(e.Policy, r.Manifest); err != nil {
				return err
			}
			lastManifest, lastTargets = r.Manifest, r.Targets
		}
	}
	if e.PendingRefresh != "" {
		if _, ok := g.ActiveRefresh(); !ok {
			return errors.New("pending refresh has no reservation")
		}
	} else if len(e.Refreshes) > 0 && (!maps.Equal(e.Manifest, lastManifest) || !maps.Equal(e.Targets, lastTargets)) {
		return errors.New("current goal differs from last completed refresh")
	}
	if len(e.Refreshes) == 0 && len(e.Targets) > 0 {
		return errors.New("delivery targets changed without refresh evidence")
	}
	if len(e.Refreshes) > 0 && !g.AllTasksSatisfied() {
		return errors.New("refresh has unfinished tasks")
	}
	if e.Final != nil && ids[e.Final.ID] {
		return errors.New("final identity reused from refresh history")
	}
	for _, f := range e.FinalHistory {
		if ids[f.ID] {
			return errors.New("final retry identity reused from refresh history")
		}
	}
	return nil
}

// Completed history and captured inputs cannot be rewritten by a stale client
// or recovery path. A pending capture may be superseded only by archiving it.
func preserveRefreshes(old, next *Execution) error {
	if len(next.Refreshes) < len(old.Refreshes) {
		return errors.New("refresh history must be retained")
	}
	for i, a := range old.Refreshes {
		b := next.Refreshes[i]
		if a.Receipt != "" {
			if !reflect.DeepEqual(a, b) {
				return errors.New("completed refresh history is immutable")
			}
			continue
		}
		a.Round, b.Round = RefreshRound{}, RefreshRound{}
		a.History, b.History = nil, nil
		a.Targets, b.Targets = nil, nil
		a.SupersededTargets, b.SupersededTargets = nil, nil
		a.Manifest, b.Manifest = nil, nil
		a.Receipt, b.Receipt = "", ""
		a.CompletedAt, b.CompletedAt = time.Time{}, time.Time{}
		if !reflect.DeepEqual(a, b) {
			return errors.New("captured refresh inputs and prior evidence are immutable")
		}
		prior, current := old.Refreshes[i], next.Refreshes[i]
		if !maps.Equal(prior.Targets, current.Targets) {
			if len(current.SupersededTargets) != len(prior.SupersededTargets)+1 || !slices.EqualFunc(current.SupersededTargets[:len(prior.SupersededTargets)], prior.SupersededTargets, func(a, b RefreshCapture) bool { return reflect.DeepEqual(a, b) }) || len(current.History) != 0 || current.Receipt != "" {
				return errors.New("target replacement lost prior captures")
			}
			archived := current.SupersededTargets[len(current.SupersededTargets)-1]
			expected := prior.currentCapture()
			expected.SupersededAt = current.Round.CreatedAt
			if !reflect.DeepEqual(archived, expected) {
				return errors.New("superseded targets do not retain their exact operation history")
			}
			continue
		}
		if !reflect.DeepEqual(prior.SupersededTargets, current.SupersededTargets) {
			return errors.New("superseded target history is immutable")
		}
		if current.Round != prior.Round {
			if len(current.History) != len(prior.History)+1 || current.History[len(current.History)-1] != prior.Round || !slices.Equal(current.History[:len(prior.History)], prior.History) {
				return errors.New("refresh retry lost prior operations")
			}
		} else if !slices.Equal(prior.History, current.History) {
			return errors.New("refresh operation history changed")
		}
	}
	return nil
}
