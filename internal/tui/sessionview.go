// Session views (PLAN P8): pure view/model logic shared by the sessions tab's
// lenses (List / Board / Attention). Everything here is deterministic and
// bubbletea-free so it is fully unit-testable — the IO layers in sessions.go
// call these to order, filter, bucket, and label the SAME cmd=sessions data
// without re-deriving status. Nothing here mutates its input.
package tui

import (
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/protocol"
)

// attentionStatuses are the derived statuses that mean a human is on the
// critical path: the agent is blocked (needs_input) or its work regressed
// (ci_failed / changes_requested / merge_conflict). These are the "NEEDS YOU"
// set surfaced by the Attention lens, the header summary count, and the
// attention-first sort — one source of truth for "who needs me".
var attentionStatuses = map[string]bool{
	"needs_input":       true,
	"ci_failed":         true,
	"changes_requested": true,
	"merge_conflict":    true,
}

// needsHuman reports whether a status requires human action (see
// attentionStatuses).
func needsHuman(status string) bool { return attentionStatuses[status] }

// AttentionCount is how many sessions currently need a human, for the header
// summary bar (e.g. "3 need you").
func AttentionCount(in []protocol.SessionInfo) int {
	n := 0
	for _, s := range in {
		if needsHuman(s.Status) {
			n++
		}
	}
	return n
}

// sortRank buckets a status into the attention-first sort tiers (lower sorts
// first): 0 blocked-on-human, 1 action-needed (broken work), 2 actively
// working, 3 parked-for-review, 4 quiet (idle / no signal), 5 done. Any status
// outside the known vocabulary falls into tier 4 (quiet) — it is neither
// urgent nor terminal, so it parks above the done tier without jumping ahead of
// real work.
func sortRank(status string) int {
	switch status {
	case "needs_input":
		return 0
	case "ci_failed", "changes_requested", "merge_conflict":
		return 1
	case "working", "ci_pending", "draft":
		return 2
	case "review_pending", "approved", "pr_open":
		return 3
	case "merged", "dead", "session_ended", "closed":
		return 5
	}
	return 4
}

// SortSessions returns a new slice ordered attention-first: needs_input, then
// action-needed (ci_failed / changes_requested / merge_conflict), then active
// (working / ci_pending), then parked (review_pending / approved / pr_open),
// then done (merged / dead / session_ended); ties break by project then issue
// for a stable, deterministic order. The input slice is never mutated.
func SortSessions(in []protocol.SessionInfo) []protocol.SessionInfo {
	out := make([]protocol.SessionInfo, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := sortRank(out[i].Status), sortRank(out[j].Status)
		if ri != rj {
			return ri < rj
		}
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Issue < out[j].Issue
	})
	return out
}

// Filter narrows a session list along independent dimensions; the zero value
// matches everything. Text is a case-insensitive substring matched over a
// session's issue/project/branch/status. AttentionOnly keeps only sessions that
// need a human (see attentionStatuses). Project and Status, when non-empty,
// require an exact match.
type Filter struct {
	Text          string
	AttentionOnly bool
	Project       string
	Status        string
}

// matches reports whether a single session satisfies every set dimension of f.
func (f Filter) matches(s protocol.SessionInfo) bool {
	if f.AttentionOnly && !needsHuman(s.Status) {
		return false
	}
	if f.Project != "" && s.Project != f.Project {
		return false
	}
	if f.Status != "" && s.Status != f.Status {
		return false
	}
	if t := strings.TrimSpace(f.Text); t != "" {
		hay := strings.ToLower(s.Issue + " " + s.Project + " " + s.Branch + " " + s.Status)
		if !strings.Contains(hay, strings.ToLower(t)) {
			return false
		}
	}
	return true
}

// Apply returns a new slice of the sessions matching f, preserving input order.
// The input slice is never mutated; the zero Filter returns a copy of all.
func Apply(in []protocol.SessionInfo, f Filter) []protocol.SessionInfo {
	out := make([]protocol.SessionInfo, 0, len(in))
	for _, s := range in {
		if f.matches(s) {
			out = append(out, s)
		}
	}
	return out
}

// KanbanColumn is one Board lens column: a stable Key (map/index key), a human
// Title, and the set of statuses that land in it.
type KanbanColumn struct {
	Key      string
	Title    string
	Statuses []string
}

// kanbanFallbackKey is the column an unknown/unmapped status routes to. The
// Working column is the safe default: an unrecognized status most likely means
// a live agent in a state the vocabulary has not caught up to, so it belongs
// with the active work rather than hidden in Done.
const kanbanFallbackKey = "working"

// KanbanColumns returns the ordered Board columns, left-to-right by human
// triage priority: the leftmost column is the human's queue. Together the
// columns cover the derived status vocabulary; any status not listed here is
// grouped into the Working column by GroupKanban (see kanbanFallbackKey).
func KanbanColumns() []KanbanColumn {
	return []KanbanColumn{
		{Key: "needs", Title: "Needs You", Statuses: []string{"needs_input"}},
		{Key: "working", Title: "Working", Statuses: []string{"working", "ci_pending", "idle"}},
		{Key: "fixing", Title: "Fixing", Statuses: []string{"ci_failed", "changes_requested", "merge_conflict"}},
		{Key: "review", Title: "In Review", Statuses: []string{"review_pending", "approved", "pr_open"}},
		{Key: "done", Title: "Done", Statuses: []string{"merged", "dead", "session_ended"}},
	}
}

// kanbanKeyForStatus maps a status to its column Key, or kanbanFallbackKey when
// unmapped.
func kanbanKeyForStatus(status string) string {
	for _, col := range KanbanColumns() {
		for _, s := range col.Statuses {
			if s == status {
				return col.Key
			}
		}
	}
	return kanbanFallbackKey
}

// GroupKanban buckets sessions into Board columns keyed by KanbanColumn.Key.
// Every session lands in exactly one column: a status outside the mapped
// vocabulary goes to the Working column (kanbanFallbackKey). Every column Key
// is present in the result (empty slice when no session occupies it) so the
// Board can render every column, empty ones included. Order within a column
// mirrors the input order.
func GroupKanban(in []protocol.SessionInfo) map[string][]protocol.SessionInfo {
	out := make(map[string][]protocol.SessionInfo, len(KanbanColumns()))
	for _, col := range KanbanColumns() {
		out[col.Key] = nil
	}
	for _, s := range in {
		key := kanbanKeyForStatus(s.Status)
		out[key] = append(out[key], s)
	}
	return out
}

// statusBadge is the short (<=2 char) glyph paired with statusStyle's color so
// status reads by both shape and hue — never color alone (degrades on mono
// terminals and for colorblind users). Shared by every lens: List cells, Board
// column chips, and the Attention list.
func statusBadge(status string) string {
	switch status {
	case "working":
		return "wk"
	case "ci_pending":
		return "ci"
	case "needs_input":
		return "!!"
	case "ci_failed":
		return "!x"
	case "changes_requested":
		return "cr"
	case "merge_conflict":
		return "mc"
	case "review_pending":
		return "rv"
	case "approved":
		return "ok"
	case "pr_open":
		return "pr"
	case "merged":
		return "mg"
	case "dead":
		return "xx"
	case "session_ended":
		return "en"
	case "idle":
		return ".."
	case "draft":
		return "df"
	}
	return "??"
}

// StatusDisplay is the shared status presentation reused by every session lens:
// a color Style (from statusStyle) plus a short Badge glyph (from statusBadge).
// Keeping color and glyph in one helper guarantees the two views never drift.
type StatusDisplay struct {
	Style lipgloss.Style
	Badge string
}

// statusDisplay returns the shared color+glyph presentation for a status,
// folding statusStyle (color) and statusBadge (glyph) into one lookup so the
// List and Board lenses render status identically.
func statusDisplay(status string) StatusDisplay {
	return StatusDisplay{Style: statusStyle(status), Badge: statusBadge(status)}
}

// statusPill renders a status as a colored chip — a filled background is itself
// a shape (not color alone), so the pills read on mono/colorblind terminals too.
// The states that put a human on the critical path (needs_input + the
// broken-work set) get a SOLID, bold fill so the queue leaps out; the active and
// parked states get a SUBTLE tint; the quiet/terminal states are plain dim text.
// Shared so the cockpit table and any future lens stay identical.
func statusPill(status string) string {
	switch status {
	case "needs_input":
		return pillFill("208", "232", status) // solid orange, near-black text
	case "ci_failed", "changes_requested", "merge_conflict":
		return pillFill("174", "232", status) // solid soft-red
	case "working", "ci_pending", "draft":
		return pillTint("24", "117", status) // muted blue
	case "approved", "pr_open":
		return pillTint("22", "114", status) // muted green
	case "review_pending":
		return pillTint("238", "251", status) // neutral grey
	default: // merged / dead / session_ended / idle / unknown: quiet
		return " " + statusStyle(status).Render(status) + " "
	}
}

// pillFill renders a SOLID, bold chip (one space of padding each side).
func pillFill(bg, fg, text string) string {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(bg)).
		Foreground(lipgloss.Color(fg)).
		Bold(true).
		Render(" " + text + " ")
}

// pillTint renders a SUBTLE chip: a dark background tint with a bright-enough
// foreground to stay legible, no bold — for the non-urgent states.
func pillTint(bg, fg, text string) string {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(bg)).
		Foreground(lipgloss.Color(fg)).
		Render(" " + text + " ")
}
