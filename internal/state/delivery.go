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
