package session

import (
	"maps"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/state"
)

// STACK SESSIONS AND LANES.
//
// A stack session is ONE agent working ONE issue in a folder that holds a
// worktree per repository (App). Each app has its own PR, and everything lola
// does about a PR — the delivery axis, the reactions and their one-shot
// guards, the review passes and their per-PR guards, the merge queue — has to
// happen per app: a failing CI on the website must not be hidden by a green
// app, and a review of one PR must never be posted on another.
//
// All of that code reads and writes PR facts as fields of a Session (s.PR,
// s.Repo, s.ReviewedPRs …). Rather than teach every one of those sites about
// apps, a stack session exposes each app as a LANE: a view of the session in
// which the PR-scoped fields are the app's own. A lane is addressed by a lane
// ID, "<session id>~<app>", and the Store resolves it — Get hands out the
// view, Update runs the mutation on the view and folds it back: the
// PR-scoped fields into the app, everything else (the idle gate, the agent
// axis, pending feedback) into the session, which is shared by every lane
// because it is one agent. So a reaction that consumes the session's AtPrompt
// gate for the website lane closes it for the app lane too, exactly as it
// must — there is one prompt to type into.
//
// The session's OWN PR fields describe the stack as a whole: Delivery is the
// most urgent app's (state.AggregateDelivery), PR and Repo are that app's, so
// write-back, the slot count, the list row and the merged cleanup read one
// value. Nothing that acts on a PR may read them — those paths run per lane.

// LaneSep joins a session ID and an app name into a lane ID. Session IDs and
// app names are both [A-Za-z0-9._-], so the separator is unambiguous.
const LaneSep = "~"

// LaneID is the store key of app's lane of session id.
func LaneID(id, app string) string { return id + LaneSep + app }

// SplitLaneID splits a lane ID into its session ID and app ("" for a plain
// session ID).
func SplitLaneID(id string) (base, app string) {
	if b, a, ok := strings.Cut(id, LaneSep); ok {
		return b, a
	}
	return id, ""
}

// BaseID is the session ID a (possibly lane) ID belongs to.
func BaseID(id string) string {
	b, _ := SplitLaneID(id)
	return b
}

// App is one repository of a stack session: a worktree at
// <session folder>/<Name> on the session's branch, cut from Base, plus the
// app's PR LANE — every field below Touched is the per-PR state a plain
// session keeps on itself (see the lane fields in Session for each one's
// meaning).
type App struct {
	// Name is the member project's name, which is also the folder name.
	Name string `json:"name"`
	// Repo is the member's GitHub owner/name ("" = no PR can be found).
	Repo string `json:"repo,omitempty"`
	// Path is the member's main checkout (the worktree was cut from it).
	Path     string `json:"path,omitempty"`
	Worktree string `json:"worktree"`
	Branch   string `json:"branch"`
	// Base is the branch the worktree was cut from and the PR targets.
	Base string `json:"base,omitempty"`
	// GitPin is this worktree's trusted git layout (see Session.GitPin).
	GitPin *gitguard.Pin `json:"git_pin,omitempty"`
	// Touched reports that the worktree has changes against Base (the
	// observer's local git read). An app with work and no PR yet keeps the
	// stack from reading merged when its other apps' PRs merge.
	Touched bool `json:"touched,omitempty"`

	PR                *scm.PR                `json:"pr,omitempty"`
	PRObservedAt      time.Time              `json:"pr_observed_at,omitzero"`
	PRFetchFailures   int                    `json:"pr_fetch_failures,omitempty"`
	Delivery          state.DeliveryState    `json:"delivery_state,omitempty"`
	DeliverySince     time.Time              `json:"delivery_since,omitzero"`
	LastReactedStatus string                 `json:"last_reacted_status,omitempty"`
	CIRetries         int                    `json:"ci_retries,omitempty"`
	Escalated         bool                   `json:"escalated,omitempty"`
	PendingReaction   string                 `json:"pending_reaction,omitempty"`
	ReviewedPRs       map[string]int         `json:"reviewed_prs,omitempty"`
	ReviewWatermarks  map[string]time.Time   `json:"review_watermarks,omitempty"`
	PendingHandoffs   map[string]string      `json:"pending_handoffs,omitempty"`
	PostedGitHubPRs   map[string]int         `json:"posted_github_prs,omitempty"`
	InlineReviewPRs   map[string]int         `json:"inline_review_prs,omitempty"`
	ReviewTallies     map[string]ReviewTally `json:"review_tallies,omitempty"`
	MergeQueueGuard   *MergeQueueGuard       `json:"merge_queue_guard,omitempty"`
	MergeQueuePos     int                    `json:"-"`
	MergeQueueStep    string                 `json:"-"`
}

// IsStack reports whether s is a stack session (a folder of app worktrees).
func (s Session) IsStack() bool { return s.Stack }

// AppIndex returns the index of the named app, or -1.
func (s Session) AppIndex(name string) int {
	for i := range s.Apps {
		if s.Apps[i].Name == name {
			return i
		}
	}
	return -1
}

// LaneView returns the view of s in which app's PR lane is the session's own
// PR state (see the file comment). ok=false for an unknown app.
func (s Session) LaneView(app string) (Session, bool) {
	i := s.AppIndex(app)
	if i < 0 {
		return Session{}, false
	}
	a := s.Apps[i]
	v := s
	v.ID = LaneID(s.ID, app)
	v.Lane = app
	v.Repo, v.Branch, v.Worktree, v.GitPin = a.Repo, a.Branch, a.Worktree, a.GitPin
	v.PR = clonePR(a.PR)
	v.PRObservedAt, v.PRFetchFailures = a.PRObservedAt, a.PRFetchFailures
	v.Delivery, v.DeliverySince = a.Delivery, a.DeliverySince
	if v.Delivery == "" {
		v.Delivery = state.DeliveryNone
	}
	v.LastReactedStatus, v.CIRetries, v.Escalated, v.PendingReaction = a.LastReactedStatus, a.CIRetries, a.Escalated, a.PendingReaction
	v.ReviewedPRs, v.ReviewWatermarks, v.PendingHandoffs = maps.Clone(a.ReviewedPRs), maps.Clone(a.ReviewWatermarks), maps.Clone(a.PendingHandoffs)
	v.PostedGitHubPRs, v.InlineReviewPRs, v.ReviewTallies = maps.Clone(a.PostedGitHubPRs), maps.Clone(a.InlineReviewPRs), maps.Clone(a.ReviewTallies)
	v.MergeQueueGuard, v.MergeQueuePos, v.MergeQueueStep = a.MergeQueueGuard, a.MergeQueuePos, a.MergeQueueStep
	// The legacy single-provider scalars are not lane-scoped; a lane starts
	// from none so the review state migration cannot resurrect the
	// session's into every app.
	v.ReviewedPR, v.LastCodeRabbitAt, v.PendingCodeRabbit, v.PendingReviewFindings = 0, time.Time{}, "", ""
	v.Apps = nil
	v.Reroll(time.Now())
	return v, true
}

// Lanes returns a view per app, in app order (none for a plain session).
func (s Session) Lanes() []Session {
	if !s.Stack {
		return nil
	}
	out := make([]Session, 0, len(s.Apps))
	for _, a := range s.Apps {
		if v, ok := s.LaneView(a.Name); ok {
			out = append(out, v)
		}
	}
	return out
}

// putLane folds a lane view written by an Update back into s: the PR-scoped
// fields into the app, every other field into the session itself, then the
// stack aggregate is re-derived.
func (s *Session) putLane(v Session, now time.Time) {
	i := s.AppIndex(v.Lane)
	if i < 0 {
		return
	}
	a := s.Apps[i]
	a.Repo, a.Branch, a.Worktree, a.GitPin = v.Repo, v.Branch, v.Worktree, v.GitPin
	a.PR = clonePR(v.PR)
	a.PRObservedAt, a.PRFetchFailures = v.PRObservedAt, v.PRFetchFailures
	a.Delivery, a.DeliverySince = v.Delivery, v.DeliverySince
	a.LastReactedStatus, a.CIRetries, a.Escalated, a.PendingReaction = v.LastReactedStatus, v.CIRetries, v.Escalated, v.PendingReaction
	a.ReviewedPRs, a.ReviewWatermarks, a.PendingHandoffs = v.ReviewedPRs, v.ReviewWatermarks, v.PendingHandoffs
	a.PostedGitHubPRs, a.InlineReviewPRs, a.ReviewTallies = v.PostedGitHubPRs, v.InlineReviewPRs, v.ReviewTallies
	a.MergeQueueGuard, a.MergeQueuePos, a.MergeQueueStep = v.MergeQueueGuard, v.MergeQueuePos, v.MergeQueueStep

	// Everything else is the session's: take the view wholesale, then
	// restore the session's own copies of the lane-scoped fields.
	keep := *s
	*s = v
	s.ID, s.Lane = keep.ID, ""
	s.Apps = append([]App(nil), keep.Apps...)
	s.Apps[i] = a
	s.Repo, s.Branch, s.Worktree, s.GitPin = keep.Repo, keep.Branch, keep.Worktree, keep.GitPin
	s.PR, s.PRObservedAt, s.PRFetchFailures = keep.PR, keep.PRObservedAt, keep.PRFetchFailures
	s.Delivery, s.DeliverySince = keep.Delivery, keep.DeliverySince
	s.LastReactedStatus, s.CIRetries, s.Escalated, s.PendingReaction = keep.LastReactedStatus, keep.CIRetries, keep.Escalated, keep.PendingReaction
	s.ReviewedPR, s.LastCodeRabbitAt, s.PendingCodeRabbit, s.PendingReviewFindings = keep.ReviewedPR, keep.LastCodeRabbitAt, keep.PendingCodeRabbit, keep.PendingReviewFindings
	s.ReviewedPRs, s.ReviewWatermarks, s.PendingHandoffs = keep.ReviewedPRs, keep.ReviewWatermarks, keep.PendingHandoffs
	s.PostedGitHubPRs, s.InlineReviewPRs, s.ReviewTallies = keep.PostedGitHubPRs, keep.InlineReviewPRs, keep.ReviewTallies
	s.MergeQueueGuard, s.MergeQueuePos, s.MergeQueueStep = keep.MergeQueueGuard, keep.MergeQueuePos, keep.MergeQueueStep
	// The view's rollup was the lane's; the session's is re-derived below
	// (SetAgentState on the view already moved the agent axis it shares).
	s.Status, s.StatusSince = keep.Status, keep.StatusSince
	s.Reaggregate(now)
}

// Reaggregate re-derives a stack session's own PR fields from its apps (see
// the file comment): the delivery axis of the most urgent app, that app's PR
// and repo, and Escalated when any app's CI retries ran out. An app with no
// PR and no changes is not counted — a repository the issue did not need
// must not keep the session pending forever. A no-op for a plain session.
func (s *Session) Reaggregate(now time.Time) {
	if !s.Stack {
		return
	}
	var states []state.DeliveryState
	var idx []int
	escalated := false
	for i, a := range s.Apps {
		if a.Escalated {
			escalated = true
		}
		d := a.Delivery
		if d == "" {
			d = state.DeliveryNone
		}
		if a.PR == nil && d == state.DeliveryNone && !a.Touched {
			continue
		}
		states = append(states, d)
		idx = append(idx, i)
	}
	d, at := state.AggregateDelivery(states)
	s.Escalated = escalated
	if at >= 0 {
		a := s.Apps[idx[at]]
		s.PR, s.Repo = clonePR(a.PR), a.Repo
		if !a.PRObservedAt.IsZero() {
			s.PRObservedAt = a.PRObservedAt
		}
		s.PRFetchFailures = a.PRFetchFailures
	} else {
		s.PR, s.Repo = nil, ""
	}
	s.SetDelivery(d, now)
}

// FocusApp names the app the session's aggregate PR fields describe ("" when
// none has a PR or changes).
func (s Session) FocusApp() string {
	if !s.Stack || s.PR == nil {
		return ""
	}
	for _, a := range s.Apps {
		if a.PR != nil && a.PR.Number == s.PR.Number && a.Repo == s.Repo {
			return a.Name
		}
	}
	return ""
}

// AppsSettled reports whether every app is KNOWN to have no open PR as of
// since (the moment the stack read merged) — the condition for tearing the
// session down. "Known" is the point: an app with a GitHub slug counts only
// once a lookup SUCCEEDED at or after since, because a failed lookup leaves
// the last facts standing and a nil PR from before the merge says nothing
// about a PR opened after it. An app with no slug cannot be asked; its dirty
// check at teardown still protects it.
func (s Session) AppsSettled(since time.Time) bool {
	for _, a := range s.Apps {
		if a.Repo == "" {
			continue
		}
		if a.PRObservedAt.IsZero() || a.PRObservedAt.Before(since) {
			return false
		}
		if a.PR != nil && a.PR.State == "OPEN" {
			return false
		}
	}
	return true
}

func clonePR(pr *scm.PR) *scm.PR {
	if pr == nil {
		return nil
	}
	c := *pr
	return &c
}
