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
	"github.com/sushidev-team/lola/internal/state"
)

// needsHuman reports whether a status requires human action: the agent is
// blocked (needs_input) or its work regressed (ci_failed / changes_requested /
// merge_conflict). Delegates to state.NeedsAttention — the ONE attention
// classification, shared with the daemon and mirrored by the desktop.
func needsHuman(status string) bool { return state.NeedsAttention(status) }

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
// first) — state.SortRank, the shared classification.
func sortRank(status string) int { return state.SortRank(status) }

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

// KanbanColumn is one Board lens column — state.KanbanColumn, the shared
// column→statuses mapping (mirrored by the desktop's theme.ts).
type KanbanColumn = state.KanbanColumn

// KanbanColumns returns the ordered Board columns, left-to-right by human
// triage priority: the leftmost column is the human's queue.
func KanbanColumns() []KanbanColumn { return state.KanbanColumns() }

// kanbanFallbackKey is the column an unknown/unmapped status routes to.
const kanbanFallbackKey = state.KanbanFallbackKey

// kanbanKeyForStatus maps a status to its column Key, or the fallback
// (Working) when unmapped.
func kanbanKeyForStatus(status string) string { return state.KanbanKeyFor(status) }

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
	case "closed":
		return "cl"
	case "shell":
		return "sh"
	case "orphaned":
		return "or"
	}
	return "??"
}

// agentBadge is the ≤2-char glyph for the AGENT axis — the truthful "what is
// the agent itself doing" underneath a delivery-owned rollup. "" for states
// not worth a badge.
func agentBadge(agentState string) string {
	switch agentState {
	case "working", "starting":
		return "wk"
	case "waiting_input":
		return "?!"
	case "idle":
		return ".."
	case "exited":
		return "en"
	}
	return ""
}

// statusPillFor renders the STATUS cell for a session: the rolled-up pill,
// plus ONE qualifier —
//
//   - the [statusagent] interpreter's DISAGREEING judgement, marked "≈" (it is
//     an approximation from untrusted material, never the deterministic
//     truth): "working ≈stuck" says the pipeline believes working, the
//     interpreter believes wedged;
//   - else a faint agent-axis badge when the axes diverge under an open PR —
//     "ci_pending ·wk" says CI is running AND the agent is still typing.
//
// Suppressed when the rollup already tells the whole story (pre-PR,
// needs_input, merged/closed/dead).
func statusPillFor(si protocol.SessionInfo) string {
	pill := statusPill(si.Status)
	if si.InterpretedState != "" {
		return pill + statusOrange.Render("≈"+statusLabel(si.InterpretedState))
	}
	b := agentBadge(si.AgentState)
	if b == "" || si.Delivery == "" || si.Delivery == "none" ||
		si.Status == "merged" || si.Status == "closed" || si.Status == "dead" ||
		si.Status == "needs_input" || si.Status == si.AgentState {
		return pill
	}
	return pill + faintText.Render("·"+b)
}

// agentDetailLine renders the detail panel's agent-axis line: state, why it is
// waiting (InputReason), the tool the in-flight turn runs, and how fresh the
// last positive activity evidence is (shortAgo, shared with the home screen).
// "" when the record carries no axis (an older daemon).
func agentDetailLine(si protocol.SessionInfo) string {
	if si.AgentState == "" {
		return ""
	}
	parts := []string{statusLabel(si.AgentState)}
	if si.InputReason != "" {
		parts = append(parts, strings.ReplaceAll(si.InputReason, "_", " "))
	}
	if si.CurrentTool != "" {
		parts = append(parts, "tool "+si.CurrentTool)
	}
	if !si.LastActivityAt.IsZero() {
		parts = append(parts, "active "+shortAgo(si.LastActivityAt)+" ago")
	}
	return "agent:    " + strings.Join(parts, " · ")
}

// interpretedLines renders the [statusagent] overlay for the detail panel:
// the one-line headline (marked "≈" — an untrusted approximation) and, when
// the interpreter believes the agent is blocked, what it is waiting on.
// Empty when no valid judgement is on the wire.
func interpretedLines(si protocol.SessionInfo) []string {
	if si.Headline == "" {
		return nil
	}
	head := "≈ " + si.Headline
	if si.HeadlineAgo != "" {
		head += " (" + si.HeadlineAgo + " ago)"
	}
	out := []string{head}
	if si.WaitingOn != "" {
		out = append(out, "≈ waiting on: "+si.WaitingOn)
	}
	return out
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
	label := statusLabel(status)
	switch status {
	case "needs_input":
		return pillFill(pillUrgentBg, pillUrgentFg, label) // solid amber, near-black text
	case "ci_failed", "changes_requested", "merge_conflict":
		return pillFill(pillBrokenBg, pillBrokenFg, label) // solid rose
	case "working", "ci_pending", "draft":
		return pillTint(pillWorkBg, pillWorkFg, label) // tinted blue
	case "approved":
		return pillTint(pillDoneBg, pillDoneFg, label) // tinted green
	case "review_pending":
		return pillTint(pillGreyBg, pillGreyFg, label) // neutral grey
	default: // merged / dead / session_ended / idle / unknown: quiet
		return " " + statusStyle(status).Render(label) + " "
	}
}

// statusLabel is the human label for a status (or agent-axis / interpreted)
// word: every raw identifier gets a readable spelling — a rendered
// "ci_failed" reads like a translation placeholder, not a badge. Long words
// are shortened so the STATUS column stays tight ("changes_requested" is 17
// columns). The mapping is display-only; every control-flow comparison still
// uses the raw status string. The fallback de-underscores, so an unmapped
// future word can never leak an identifier into the UI.
func statusLabel(status string) string {
	switch status {
	case "changes_requested":
		return "changes"
	case "review_pending":
		return "review"
	case "merge_conflict":
		return "conflict"
	case "session_ended":
		return "ended"
	case "ci_pending":
		return "ci running"
	case "ci_failed":
		return "ci failed"
	case "needs_input":
		return "needs you"
	case "waiting_input": // agent axis / interpreted overlay
		return "waiting"
	}
	return strings.ReplaceAll(status, "_", " ")
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
