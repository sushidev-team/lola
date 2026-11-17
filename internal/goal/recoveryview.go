package goal

import "time"

// RecoveryView is an on-demand, read-only snapshot for reviewing recovery.
// It includes the latest decisions, not private execution paths or raw policy.
// Actions are local eligibility hints; the daemon rechecks live jobs, Linear,
// verification and publication journals before any change.
type RecoveryView struct {
	Goal                Overview           `json:"goal"`
	Brief               BriefContract      `json:"brief"`
	PendingBrief        *BriefSource       `json:"pendingBrief,omitempty"`
	LatestBrief         *BriefDecisionView `json:"latestBrief,omitempty"`
	LatestProgress      *ProgressUpdate    `json:"latestProgress,omitempty"`
	ProgressError       string             `json:"progressError"`
	DeliveryEvidenceURL string             `json:"deliveryEvidenceUrl"`
	Actions             []string           `json:"actions"`
}

// BriefDecisionView deliberately excludes archived verification receipt paths.
type BriefDecisionView struct {
	SourceID   string        `json:"sourceId"`
	Before     BriefContract `json:"before"`
	After      BriefContract `json:"after"`
	Reason     string        `json:"reason"`
	AcceptedAt time.Time     `json:"acceptedAt"`
}

func (g Goal) Recovery(now time.Time) RecoveryView {
	// Recovery is requested explicitly, not polled with every project overview.
	// Isolate mutation-based eligibility probes and all returned nested slices.
	g = copyGoal(g)
	v := RecoveryView{Goal: g.Overview(now), Brief: g.BriefContract(), ProgressError: g.ProgressError, Actions: []string{}}
	for _, source := range g.BriefSources {
		if source.ID == g.PendingBrief {
			s := source
			v.PendingBrief = &s
			break
		}
	}
	if n := len(g.BriefRevisions); n > 0 {
		r := g.BriefRevisions[n-1]
		v.LatestBrief = &BriefDecisionView{SourceID: r.SourceID, Before: r.Before, After: r.After, Reason: r.Reason, AcceptedAt: r.AcceptedAt}
	}
	if n := len(g.ProgressUpdates); n > 0 {
		u := g.ProgressUpdates[n-1]
		v.LatestProgress = &u
		trial := copyGoal(g)
		if trial.DismissProgress(ProgressResolution{ID: g.ID, Revision: g.Revision, UpdateID: u.ID, Reason: "Availability probe"}, now) == nil {
			v.Actions = append(v.Actions, "dismiss-progress")
		}
	}
	if g.Parent.Confirmed && g.State != "accepted" && g.State != "canceled" {
		v.Actions = append(v.Actions, "sync-brief")
	}
	if v.PendingBrief != nil {
		trial := copyGoal(g)
		if trial.AcceptBrief(BriefAcceptance{ID: g.ID, Revision: g.Revision, SourceID: g.PendingBrief, Contract: g.BriefContract(), Reason: "Availability probe"}, now) == nil {
			v.Actions = append(v.Actions, "accept-brief")
		}
	}
	if e := g.Execution; e != nil {
		v.DeliveryEvidenceURL = e.DeliveryEvidenceURL
		if g.State == "ready" && e.Final != nil && e.Final.Receipt != "" && e.Delivery == nil {
			v.Actions = append(v.Actions, "delivery-evidence")
		}
		if g.SyncError == "" && g.PendingBrief == "" && (g.State == "paused" || g.State == "blocked") {
			trial := copyGoal(g)
			if trial.RetryVerification("recovery-availability-probe", now) == nil {
				v.Actions = append(v.Actions, "retry-verification")
			}
			trial = copyGoal(g)
			if trial.RetryReplan("recovery-availability-probe", now) == nil {
				v.Actions = append(v.Actions, "retry-replan")
			}
		}
	}
	return v
}
