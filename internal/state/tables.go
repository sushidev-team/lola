package state

// The LEGACY rolled-up vocabulary's own helpers — what is left of them.
//
// This file used to hold four string-keyed consumer tables (slot, present,
// attention, sort) plus the kanban status sets, because the whole daemon and
// both UIs classified sessions by the one collapsed word. Everything that
// CLASSIFIES now takes the axis PAIR and lives in display.go; the shims that
// bridged the migration are gone with their last caller.
//
// What remains is the vocabulary itself and the one classification that is
// genuinely ABOUT the collapsed word rather than about a session:
//
//   - AllStatuses — the wire vocabulary state.Rollup produces for
//     protocol.SessionInfo.status, mirrored by the desktop's theme.ts and read
//     by the mobile companion.
//   - Notable — the activity-feed filter. Its inputs are a RECORDED from→to
//     transition (protocol.Event), i.e. two historical status strings, not a
//     live session whose axes could be re-read. It stays string-keyed for
//     exactly as long as the feed carries words instead of pairs.

// Notable decides whether a from→to rolled-up transition is worth an
// activity-feed line. A spawn (from "") always is. Routine noise is dropped:
// the idle↔working turn churn (working is kept ONLY as a "resumed" signal out
// of needs_input) and the orphaned adoption anomaly, which is surfaced by
// doctor/logs rather than the feed.
func Notable(from, to string) bool {
	if to == "" {
		return false
	}
	if from == "" {
		return true // spawn
	}
	switch to {
	case "idle", "orphaned":
		return false
	case "working":
		return from == "needs_input" // resumed after waiting on a human
	default:
		return true
	}
}

// AllStatuses is the complete rolled-up vocabulary, in a stable order. The
// desktop's theme.ts mirrors this list; a Go test asserts the two stay
// identical (the UIThemes / THEME_IDS parity pattern).
//
// It is a WIRE vocabulary, not a dead one: Rollup still ships every one of
// these words on protocol.SessionInfo.status, and the mobile companion still
// keys off them. See Rollup's own comment before shortening this list.
func AllStatuses() []string {
	return []string{
		"working", "idle", "needs_input", "session_ended", "dead",
		"shell", "orphaned",
		"draft", "ci_pending", "ci_failed", "merge_conflict",
		"changes_requested", "review_pending", "approved",
		"merged", "closed",
	}
}
