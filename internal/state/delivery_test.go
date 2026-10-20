package state

import (
	"testing"

	"github.com/sushidev-team/lola/internal/scm"
)

func TestDeriveDeliveryPriority(t *testing.T) {
	cases := []struct {
		name string
		pr   *scm.PR
		prev DeliveryState
		want DeliveryState
	}{
		{"nil pr", nil, DeliveryNone, DeliveryNone},
		{"merged", &scm.PR{State: "MERGED"}, DeliveryNone, DeliveryMerged},
		{"closed", &scm.PR{State: "CLOSED"}, DeliveryNone, DeliveryClosed},
		// Bug fix: a red DRAFT is ci_failed, not silently "draft".
		{"fail beats draft",
			&scm.PR{State: "OPEN", IsDraft: true, ChecksState: "fail"},
			DeliveryNone, DeliveryCIFailed},
		// Conflict also beats draft — the rebase reaction must see it.
		{"conflict beats draft",
			&scm.PR{State: "OPEN", IsDraft: true, Mergeable: "CONFLICTING", ChecksState: "pass"},
			DeliveryNone, DeliveryMergeConflict},
		{"plain draft",
			&scm.PR{State: "OPEN", IsDraft: true, Mergeable: "MERGEABLE", ChecksState: "pending"},
			DeliveryNone, DeliveryDraft},
		// Draft still outranks the review states.
		{"draft beats changes_requested",
			&scm.PR{State: "OPEN", IsDraft: true, Mergeable: "MERGEABLE", ReviewDecision: "CHANGES_REQUESTED", ChecksState: "pass"},
			DeliveryNone, DeliveryDraft},
		{"fail beats conflict",
			&scm.PR{State: "OPEN", Mergeable: "CONFLICTING", ChecksState: "fail"},
			DeliveryNone, DeliveryCIFailed},
		{"changes_requested",
			&scm.PR{State: "OPEN", Mergeable: "MERGEABLE", ReviewDecision: "CHANGES_REQUESTED", ChecksState: "pass"},
			DeliveryNone, DeliveryChangesRequested},
		{"approved green",
			&scm.PR{State: "OPEN", Mergeable: "MERGEABLE", ReviewDecision: "APPROVED", ChecksState: "pass"},
			DeliveryNone, DeliveryApproved},
		// Bug fix: a repo with no CI at all can still park approved.
		{"approved no checks",
			&scm.PR{State: "OPEN", Mergeable: "MERGEABLE", ReviewDecision: "APPROVED", ChecksState: "none"},
			DeliveryNone, DeliveryApproved},
		// Never park while CI is still running.
		{"approved pending stays ci_pending",
			&scm.PR{State: "OPEN", Mergeable: "MERGEABLE", ReviewDecision: "APPROVED", ChecksState: "pending"},
			DeliveryNone, DeliveryCIPending},
		{"pending",
			&scm.PR{State: "OPEN", Mergeable: "MERGEABLE", ChecksState: "pending"},
			DeliveryNone, DeliveryCIPending},
		{"review_pending fallthrough",
			&scm.PR{State: "OPEN", Mergeable: "MERGEABLE", ChecksState: "pass"},
			DeliveryNone, DeliveryReviewPending},
		{"review_pending no checks",
			&scm.PR{State: "OPEN", Mergeable: "MERGEABLE", ChecksState: "none"},
			DeliveryNone, DeliveryReviewPending},
	}
	for _, c := range cases {
		if got := DeriveDelivery(c.pr, c.prev); got != c.want {
			t.Errorf("%s: DeriveDelivery = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestDeriveDeliveryUnknownHysteresis: GitHub reports Mergeable=UNKNOWN while
// recomputing after every push. A PR already known conflicting must stay
// merge_conflict through UNKNOWN (no flap); one not previously conflicting
// treats UNKNOWN as not conflicting.
func TestDeriveDeliveryUnknownHysteresis(t *testing.T) {
	unknown := &scm.PR{State: "OPEN", Mergeable: "UNKNOWN", ChecksState: "pass"}
	if got := DeriveDelivery(unknown, DeliveryMergeConflict); got != DeliveryMergeConflict {
		t.Errorf("UNKNOWN with prev=merge_conflict = %q, want merge_conflict (sticky)", got)
	}
	if got := DeriveDelivery(unknown, DeliveryReviewPending); got != DeliveryReviewPending {
		t.Errorf("UNKNOWN with prev=review_pending = %q, want review_pending", got)
	}
	if got := DeriveDelivery(unknown, DeliveryNone); got != DeliveryReviewPending {
		t.Errorf("UNKNOWN with prev=none = %q, want review_pending", got)
	}
	// The stickiness releases the moment GitHub commits to an answer.
	resolved := &scm.PR{State: "OPEN", Mergeable: "MERGEABLE", ChecksState: "pass"}
	if got := DeriveDelivery(resolved, DeliveryMergeConflict); got != DeliveryReviewPending {
		t.Errorf("MERGEABLE with prev=merge_conflict = %q, want review_pending (released)", got)
	}
}

func TestAggregateDelivery(t *testing.T) {
	for _, c := range []struct {
		in   []DeliveryState
		want DeliveryState
		at   int
	}{
		{nil, DeliveryNone, -1},
		{[]DeliveryState{DeliveryReviewPending, DeliveryCIFailed}, DeliveryCIFailed, 1},
		{[]DeliveryState{DeliveryApproved, DeliveryNone}, DeliveryNone, 1},
		{[]DeliveryState{DeliveryMerged, DeliveryReviewPending}, DeliveryReviewPending, 1},
		{[]DeliveryState{DeliveryMerged, DeliveryMerged}, DeliveryMerged, 0},
		{[]DeliveryState{DeliveryClosed, DeliveryMerged}, DeliveryMerged, 1},
		{[]DeliveryState{DeliveryClosed, DeliveryClosed}, DeliveryClosed, 0},
		{[]DeliveryState{DeliveryCIPending, DeliveryMergeConflict}, DeliveryMergeConflict, 1},
	} {
		got, at := AggregateDelivery(c.in)
		if got != c.want || at != c.at {
			t.Errorf("AggregateDelivery(%v) = %s@%d, want %s@%d", c.in, got, at, c.want, c.at)
		}
	}
}
