package state

import (
	"testing"

	"github.com/sushidev-team/lola/internal/scm"
)

// ---- Oracles: verbatim transcriptions of the PRE-AXIS derivation ----------
//
// oldDeriveStatus is scm.DeriveStatus as it stood before the axis split
// (including the draft-over-ci_failed ordering and APPROVED+none →
// review_pending quirks — those are DELIBERATE divergences of DeriveDelivery,
// tested separately in delivery_test.go; the fixtures below avoid them so the
// matrix isolates the Rollup layer).
func oldDeriveStatus(sessionAlive bool, pr *scm.PR) string {
	if pr == nil {
		if sessionAlive {
			return "working"
		}
		return "no_pr"
	}
	switch pr.State {
	case "MERGED":
		return "merged"
	case "CLOSED":
		return "closed"
	}
	if pr.IsDraft {
		return "draft"
	}
	if pr.ChecksState == "fail" {
		return "ci_failed"
	}
	if pr.Mergeable == "CONFLICTING" {
		return "merge_conflict"
	}
	if pr.ReviewDecision == "CHANGES_REQUESTED" {
		return "changes_requested"
	}
	if pr.ReviewDecision == "APPROVED" && pr.ChecksState == "pass" {
		return "approved"
	}
	if pr.ChecksState == "pending" {
		return "ci_pending"
	}
	return "review_pending"
}

// oldNativeStatus is daemon.nativeStatus as it stood before the axis split.
func oldNativeStatus(current string, alive bool, pr *scm.PR) string {
	status := current
	if pr != nil {
		status = oldDeriveStatus(alive, pr)
	}
	if alive && current == "needs_input" && status != "merged" {
		status = "needs_input"
	}
	if !alive && status != "merged" {
		return "dead"
	}
	return status
}

// prFor returns a PR fixture that BOTH the old DeriveStatus and the new
// DeriveDelivery map to the same word, so the matrix compares only the
// composition layer. nil for DeliveryNone.
func prFor(d DeliveryState) *scm.PR {
	switch d {
	case DeliveryNone:
		return nil
	case DeliveryMerged:
		return &scm.PR{State: "MERGED"}
	case DeliveryClosed:
		return &scm.PR{State: "CLOSED"}
	case DeliveryDraft:
		return &scm.PR{State: "OPEN", IsDraft: true, Mergeable: "MERGEABLE", ChecksState: "pass"}
	case DeliveryCIFailed:
		return &scm.PR{State: "OPEN", Mergeable: "MERGEABLE", ChecksState: "fail"}
	case DeliveryMergeConflict:
		return &scm.PR{State: "OPEN", Mergeable: "CONFLICTING", ChecksState: "pass"}
	case DeliveryChangesRequested:
		return &scm.PR{State: "OPEN", Mergeable: "MERGEABLE", ReviewDecision: "CHANGES_REQUESTED", ChecksState: "pass"}
	case DeliveryApproved:
		return &scm.PR{State: "OPEN", Mergeable: "MERGEABLE", ReviewDecision: "APPROVED", ChecksState: "pass"}
	case DeliveryCIPending:
		return &scm.PR{State: "OPEN", Mergeable: "MERGEABLE", ChecksState: "pending"}
	case DeliveryReviewPending:
		return &scm.PR{State: "OPEN", Mergeable: "MERGEABLE", ChecksState: "pass"}
	}
	panic("unknown delivery fixture: " + string(d))
}

// legacyFor maps an AgentState to the (current status, alive) pair the old
// pipeline would have held for it. starting is new vocabulary: spawn used to
// write "working" immediately.
func legacyFor(a AgentState) (current string, alive bool, ok bool) {
	switch a {
	case AgentStarting, AgentWorking:
		return "working", true, true
	case AgentIdle:
		return "idle", true, true
	case AgentWaitingInput:
		return "needs_input", true, true
	case AgentExited:
		return "session_ended", true, true
	case AgentDead:
		return "dead", false, true
	case AgentOrphaned:
		return "orphaned", true, true
	case AgentShell:
		// Shells never went through nativeStatus (observeManualShell owns
		// them) and never have PR facts — excluded from the matrix.
		return "", false, false
	}
	panic("unknown agent state: " + string(a))
}

var allAgentStates = []AgentState{
	AgentStarting, AgentWorking, AgentWaitingInput, AgentIdle,
	AgentExited, AgentDead, AgentShell, AgentOrphaned,
}

var allDeliveryStates = []DeliveryState{
	DeliveryNone, DeliveryDraft, DeliveryCIPending, DeliveryCIFailed,
	DeliveryMergeConflict, DeliveryChangesRequested, DeliveryReviewPending,
	DeliveryApproved, DeliveryMerged, DeliveryClosed,
}

// TestRollupMatrixMatchesLegacy is the load-bearing compat proof: for every
// AgentState × DeliveryState combination reachable in the old pipeline,
// Rollup produces byte-identical output to oldNativeStatus. The one mapped
// divergence: the old pipeline could yield "no_pr" only through an
// unreachable branch (the observer never called DeriveStatus with a nil PR);
// dead+none legitimately rolls up "dead" on both sides.
func TestRollupMatrixMatchesLegacy(t *testing.T) {
	for _, a := range allAgentStates {
		for _, d := range allDeliveryStates {
			current, alive, ok := legacyFor(a)
			if !ok {
				continue
			}
			pr := prFor(d)
			want := oldNativeStatus(current, alive, pr)
			got := Rollup(a, d)
			if got != want {
				t.Errorf("Rollup(%s, %s) = %q, legacy nativeStatus(%q, alive=%v, pr=%v) = %q",
					a, d, got, current, alive, d, want)
			}
		}
	}
}

// TestRollupShell covers the axis the matrix excludes: agentless shells.
func TestRollupShell(t *testing.T) {
	if got := Rollup(AgentShell, DeliveryNone); got != "shell" {
		t.Fatalf("Rollup(shell, none) = %q, want shell", got)
	}
	if got := Rollup(AgentDead, DeliveryNone); got != "dead" {
		t.Fatalf("Rollup(dead, none) = %q, want dead", got)
	}
}

// TestRollupGoldens pins the rules that matter most, independent of oracles.
func TestRollupGoldens(t *testing.T) {
	cases := []struct {
		a    AgentState
		d    DeliveryState
		want string
	}{
		{AgentDead, DeliveryMerged, "merged"},         // merged beats dead
		{AgentWaitingInput, DeliveryMerged, "merged"}, // merged beats waiting
		{AgentDead, DeliveryCIFailed, "dead"},         // dead beats live PR states
		{AgentDead, DeliveryClosed, "dead"},
		{AgentWaitingInput, DeliveryCIPending, "needs_input"}, // the rescue
		{AgentWaitingInput, DeliveryClosed, "needs_input"},    // rescue beats closed
		{AgentIdle, DeliveryCIPending, "ci_pending"},          // delivery owns rollup post-PR
		{AgentWorking, DeliveryCIPending, "ci_pending"},
		{AgentExited, DeliveryReviewPending, "review_pending"},
		{AgentStarting, DeliveryNone, "working"}, // spawn holds its slot
		{AgentIdle, DeliveryNone, "idle"},
		{AgentExited, DeliveryNone, "session_ended"},
		{AgentOrphaned, DeliveryNone, "orphaned"},
	}
	for _, c := range cases {
		if got := Rollup(c.a, c.d); got != c.want {
			t.Errorf("Rollup(%s, %s) = %q, want %q", c.a, c.d, got, c.want)
		}
	}
}
