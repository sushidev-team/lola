package state

import "github.com/sushidev-team/lola/internal/scm"

// DeliveryState is the PR axis: where the session's pull request stands.
type DeliveryState string

const (
	// DeliveryNone means no PR has been observed for the session's branch.
	DeliveryNone             DeliveryState = "none"
	DeliveryDraft            DeliveryState = "draft"
	DeliveryCIPending        DeliveryState = "ci_pending"
	DeliveryCIFailed         DeliveryState = "ci_failed"
	DeliveryMergeConflict    DeliveryState = "merge_conflict"
	DeliveryChangesRequested DeliveryState = "changes_requested"
	DeliveryReviewPending    DeliveryState = "review_pending"
	DeliveryApproved         DeliveryState = "approved"
	DeliveryMerged           DeliveryState = "merged"
	DeliveryClosed           DeliveryState = "closed"
)

// DeliveryFromStatus recognizes a legacy collapsed status string that IS a
// delivery word (used by the snapshot migration when a record carries a
// delivery-owned status but no PR facts to re-derive it from).
func DeliveryFromStatus(status string) (DeliveryState, bool) {
	switch d := DeliveryState(status); d {
	case DeliveryDraft, DeliveryCIPending, DeliveryCIFailed,
		DeliveryMergeConflict, DeliveryChangesRequested,
		DeliveryReviewPending, DeliveryApproved, DeliveryMerged,
		DeliveryClosed:
		return d, true
	}
	return DeliveryNone, false
}

// DeriveDelivery maps observed PR facts to the delivery axis, in strict
// priority order. prev is the session's current delivery state, consulted
// only for the Mergeable=UNKNOWN hysteresis.
//
//	pr == nil             → none
//	State MERGED          → merged
//	State CLOSED          → closed
//	ChecksState fail      → ci_failed
//	conflicting           → merge_conflict
//	IsDraft               → draft
//	CHANGES_REQUESTED     → changes_requested
//	APPROVED + pass/none  → approved
//	ChecksState pending   → ci_pending
//	otherwise             → review_pending
//
// Deliberate choices (each reverses an old DeriveStatus blind spot):
//
//   - ci_failed and merge_conflict outrank draft: a red or conflicting draft
//     is broken work the reactions must see, not a PR quietly "in draft".
//   - conflicting is sticky through UNKNOWN: GitHub recomputes mergeability
//     asynchronously after every push and reports UNKNOWN for a while; a PR
//     already known CONFLICTING stays merge_conflict until GitHub commits to
//     an answer, so the status cannot flap conflict→review→conflict per push.
//     A PR NOT previously conflicting treats UNKNOWN as not conflicting, as
//     before.
//   - APPROVED with no checks at all ("none") now parks as approved: a repo
//     without CI can still finish. APPROVED with running checks remains
//     ci_pending — never park a PR while its CI story is incomplete.
func DeriveDelivery(pr *scm.PR, prev DeliveryState) DeliveryState {
	if pr == nil {
		return DeliveryNone
	}
	switch pr.State {
	case "MERGED":
		return DeliveryMerged
	case "CLOSED":
		return DeliveryClosed
	}
	if pr.ChecksState == "fail" {
		return DeliveryCIFailed
	}
	if pr.Mergeable == "CONFLICTING" ||
		(pr.Mergeable == "UNKNOWN" && prev == DeliveryMergeConflict) {
		return DeliveryMergeConflict
	}
	if pr.IsDraft {
		return DeliveryDraft
	}
	if pr.ReviewDecision == "CHANGES_REQUESTED" {
		return DeliveryChangesRequested
	}
	if pr.ReviewDecision == "APPROVED" &&
		(pr.ChecksState == "pass" || pr.ChecksState == "none") {
		return DeliveryApproved
	}
	if pr.ChecksState == "pending" {
		return DeliveryCIPending
	}
	return DeliveryReviewPending
}

// aggregateRank orders the delivery states of a STACK session's apps by how
// much they need someone, most urgent first. A failing or conflicting PR beats
// a pending one, an app whose work is not yet a PR (DeliveryNone) beats a
// PR parked on a reviewer — the session is not done while it has work left —
// and the terminal states rank last, so the session reads merged only once
// every app's PR is.
var aggregateRank = []DeliveryState{
	DeliveryCIFailed, DeliveryMergeConflict, DeliveryChangesRequested,
	DeliveryCIPending, DeliveryDraft, DeliveryNone, DeliveryReviewPending,
	DeliveryApproved, DeliveryClosed, DeliveryMerged,
}

// AggregateDelivery is the delivery axis of a STACK session: the state of
// the app that needs attention most (see aggregateRank), so the one pill a
// session row shows is never greener than its worst PR. It returns the index
// of that app too (-1 when there is none), so callers can show which app the
// state is about. A stack whose apps are all merged or closed is merged as
// soon as one merged, closed only when every one was closed.
func AggregateDelivery(states []DeliveryState) (DeliveryState, int) {
	best, at := DeliveryNone, -1
	rank := func(d DeliveryState) int {
		for i, r := range aggregateRank {
			if r == d {
				return i
			}
		}
		return 0 // an unknown state is treated as the most urgent
	}
	allClosed := len(states) > 0
	for i, d := range states {
		if d != DeliveryClosed {
			allClosed = false
		}
		if at < 0 || rank(d) < rank(best) {
			best, at = d, i
		}
	}
	if best == DeliveryClosed && !allClosed {
		// Merged and closed only: the stack shipped (a closed PR is an app
		// the agent decided not to change after all).
		for i, d := range states {
			if d == DeliveryMerged {
				return DeliveryMerged, i
			}
		}
	}
	return best, at
}
