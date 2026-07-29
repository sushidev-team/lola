package state

import "slices"

// Consumer tables: the single classification of the rolled-up status
// vocabulary. These replace four previously divergent copies (daemon
// dispatch/reconcile/events, TUI sessionview) — change classifications HERE,
// never at a call site.

// holdsSlot is the set of rolled-up statuses under which a native session
// occupies an agent slot. Parked-for-review states (approved, review_pending,
// idle after the PR opened) and terminal states (merged, closed, dead,
// session_ended) hold no slot, so held PRs never stall new pickups.
// merge_conflict counts (a change from the pre-axis table): the reaction
// engine is actively re-prompting that agent to rebase, so its runner is
// occupied. draft counts for the same reason — the agent is still iterating.
var holdsSlot = map[string]bool{
	"working":           true,
	"needs_input":       true,
	"draft":             true,
	"ci_failed":         true,
	"changes_requested": true,
	"ci_pending":        true,
	"merge_conflict":    true,
}

// HoldsSlot reports whether a session in this rolled-up status occupies an
// agent slot for the dispatch Budget.
func HoldsSlot(status string) bool { return holdsSlot[status] }

// Present reports whether a session in this rolled-up status still counts as
// "present" for the reconcile orphan-revert shield: a present session
// legitimately explains a labeled-but-unworked Linear issue. Dead and
// session_ended records are gone; a closed PR no longer shields either (a
// change from the pre-axis table) — the work was explicitly rejected, so the
// issue must become revertable instead of being shielded forever by a
// lingering pane.
func Present(status string) bool {
	switch status {
	case "dead", "session_ended", "closed":
		return false
	}
	return true
}

// attention is the set of rolled-up statuses that put a human on the critical
// path: the agent is blocked (needs_input) or its work regressed.
var attention = map[string]bool{
	"needs_input":       true,
	"ci_failed":         true,
	"changes_requested": true,
	"merge_conflict":    true,
}

// NeedsAttention reports whether a rolled-up status requires human action.
func NeedsAttention(status string) bool { return attention[status] }

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

// SortRank buckets a rolled-up status into the attention-first sort tiers
// (lower sorts first): 0 blocked-on-human, 1 action-needed (broken work),
// 2 actively working, 3 parked-for-review, 4 quiet (idle / shell / unknown),
// 5 done. Any status outside the known vocabulary falls into tier 4 — neither
// urgent nor terminal.
func SortRank(status string) int {
	switch status {
	case "needs_input":
		return 0
	case "ci_failed", "changes_requested", "merge_conflict":
		return 1
	case "working", "ci_pending", "draft":
		return 2
	case "review_pending", "approved":
		return 3
	case "merged", "dead", "session_ended", "closed":
		return 5
	}
	return 4
}

// KanbanColumn is one Board column: a stable Key, a human Title, and the set
// of rolled-up statuses that land in it.
type KanbanColumn struct {
	Key      string
	Title    string
	Statuses []string
}

// KanbanFallbackKey is the column an unknown/unmapped status routes to. The
// Working column is the safe default: an unrecognized status most likely
// means a live agent in a state the vocabulary has not caught up to.
const KanbanFallbackKey = "working"

// KanbanColumns returns the ordered Board columns, left-to-right by human
// triage priority. Together they cover the rolled-up vocabulary; anything
// unmapped is grouped into the Working column (KanbanFallbackKey).
func KanbanColumns() []KanbanColumn {
	return []KanbanColumn{
		{Key: "needs", Title: "Needs You", Statuses: []string{"needs_input"}},
		{Key: "working", Title: "Working", Statuses: []string{"working", "ci_pending", "idle", "draft"}},
		{Key: "fixing", Title: "Fixing", Statuses: []string{"ci_failed", "changes_requested", "merge_conflict"}},
		{Key: "review", Title: "In Review", Statuses: []string{"review_pending", "approved"}},
		{Key: "done", Title: "Done", Statuses: []string{"merged", "closed", "dead", "session_ended"}},
	}
}

// KanbanKeyFor maps a rolled-up status to its column Key, or
// KanbanFallbackKey when unmapped.
func KanbanKeyFor(status string) string {
	for _, col := range KanbanColumns() {
		if slices.Contains(col.Statuses, status) {
			return col.Key
		}
	}
	return KanbanFallbackKey
}

// AllStatuses is the complete rolled-up vocabulary, in a stable order. The
// desktop's theme.ts mirrors this list; a Go test asserts the two stay
// identical (the UIThemes / THEME_IDS parity pattern).
func AllStatuses() []string {
	return []string{
		"working", "idle", "needs_input", "session_ended", "dead",
		"shell", "orphaned",
		"draft", "ci_pending", "ci_failed", "merge_conflict",
		"changes_requested", "review_pending", "approved",
		"merged", "closed",
	}
}
