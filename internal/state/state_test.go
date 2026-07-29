package state

import "testing"

func TestFromLegacy(t *testing.T) {
	cases := []struct {
		status string
		d      DeliveryState
		wantA  AgentState
	}{
		{"working", DeliveryNone, AgentWorking},
		{"idle", DeliveryNone, AgentIdle},
		{"needs_input", DeliveryCIPending, AgentWaitingInput},
		{"session_ended", DeliveryNone, AgentExited},
		{"dead", DeliveryNone, AgentDead},
		{"no_pr", DeliveryNone, AgentDead}, // legacy dead-without-PR word
		{"shell", DeliveryNone, AgentShell},
		{"orphaned", DeliveryNone, AgentOrphaned},
		// Delivery-vocabulary statuses: the agent axis is unknowable from the
		// collapsed string — Idle is the seed; Rollup(Idle, d) reproduces the
		// stored status.
		{"ci_pending", DeliveryCIPending, AgentIdle},
		{"merged", DeliveryMerged, AgentIdle},
		{"draft", DeliveryDraft, AgentIdle},
		{"", DeliveryNone, AgentIdle},
	}
	for _, c := range cases {
		a, d := FromLegacy(c.status, c.d)
		if a != c.wantA || d != c.d {
			t.Errorf("FromLegacy(%q, %s) = (%s, %s), want (%s, %s)",
				c.status, c.d, a, d, c.wantA, c.d)
		}
	}
}

// TestFromLegacyRoundTrips: for every legacy status the backfilled axes must
// roll BACK UP to the same status — otherwise loading an old snapshot would
// change what the user sees.
func TestFromLegacyRoundTrips(t *testing.T) {
	prByStatus := map[string]DeliveryState{
		"working": DeliveryNone, "idle": DeliveryNone,
		"needs_input": DeliveryNone, "session_ended": DeliveryNone,
		"dead": DeliveryNone, "shell": DeliveryNone, "orphaned": DeliveryNone,
		"draft": DeliveryDraft, "ci_pending": DeliveryCIPending,
		"ci_failed": DeliveryCIFailed, "merge_conflict": DeliveryMergeConflict,
		"changes_requested": DeliveryChangesRequested,
		"review_pending":    DeliveryReviewPending,
		"approved":          DeliveryApproved,
		"merged":            DeliveryMerged, "closed": DeliveryClosed,
	}
	for status, d := range prByStatus {
		a, dd := FromLegacy(status, d)
		if got := Rollup(a, dd); got != status {
			t.Errorf("FromLegacy(%q) → Rollup = %q, want round-trip", status, got)
		}
	}
}

func TestClassifyNotification(t *testing.T) {
	cases := []struct {
		message, typ string
		want         InputReason
	}{
		{"Claude needs your permission to use Bash", "", InputPermission},
		{"", "approval-requested", InputPermission}, // codex notify type
		{"Claude is waiting for your input", "", InputIdleNotify},
		{"", "idle_timeout", InputIdleNotify},
		{"", "", InputIdleNotify},
	}
	for _, c := range cases {
		if got := ClassifyNotification(c.message, c.typ); got != c.want {
			t.Errorf("ClassifyNotification(%q, %q) = %s, want %s", c.message, c.typ, got, c.want)
		}
	}
}
