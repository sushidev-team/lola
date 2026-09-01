// Session views (PLAN P8): pure view/model logic shared by the sessions tab's
// lenses (List / Board / Attention). Everything here is deterministic and
// bubbletea-free so it is fully unit-testable — the IO layers in sessions.go
// call these to order, filter, bucket, and label the SAME cmd=sessions data
// without re-deriving status. Nothing here mutates its input.
//
// TWO AXES, NOT ONE WORD. Every classification below takes the pair
// (AgentState, DeliveryState) through internal/state's pair-based tables, and
// every rendering shows both: the PRIMARY pill is the agent axis reduced to
// state.Display (what the agent is doing), the SECONDARY chip is the delivery
// axis (where the PR stands). The rolled-up SessionInfo.Status is still on the
// wire — the phone client reads it — but nothing here keys display off it any
// more, because collapsing the axes is exactly what made the old pill lie: a
// post-PR agent was invisible behind its delivery word, and the one agent state
// that did punch through (needs_input) was minted 90% of the time by the coding
// agent's own 60s idle nudge rather than by a real question.
package tui

import (
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/state"
)

// axesOf reads a wire record's two state axes as typed values.
//
// Both axis fields are `omitempty`, so a daemon older than the axis split ships
// only the rolled-up Status — and the TUI is a client of whatever daemon
// happens to be running (`make build` alone never reaches it; see CLAUDE.md).
// So an absent axis is BACKFILLED from Status exactly the way the daemon
// backfills its own snapshots on load: DeliveryFromStatus recovers the delivery
// word, FromLegacy recovers the agent word. A record from a current daemon
// never takes that path.
func axesOf(si protocol.SessionInfo) (state.AgentState, state.DeliveryState) {
	d, _ := state.DeliveryFromStatus(si.Status)
	if si.Delivery != "" {
		d = state.DeliveryState(si.Delivery)
	}
	if si.AgentState != "" {
		return state.AgentState(si.AgentState), d
	}
	a, d := state.FromLegacy(si.Status, d)
	return a, d
}

// displayOf reduces a session's AGENT axis to the primary-pill vocabulary.
func displayOf(si protocol.SessionInfo) state.Display {
	a, _ := axesOf(si)
	return state.DisplayFor(a)
}

// waitingOnHuman reports whether the AGENT itself is blocked on a person. It is
// the narrower half of needsHuman and drives the row/card "!" marker and the
// n/N jump: those answer "which session is asking ME something", which is a
// question about the agent axis alone. The broader "this needs a human" —
// including a red build on a happily working agent — is needsHuman, and the
// delivery half of it is visible on the PR chip's own red glyphs.
func waitingOnHuman(si protocol.SessionInfo) bool {
	a, _ := axesOf(si)
	return a == state.AgentWaitingInput
}

// answerable mirrors the daemon's answer gate (internal/daemon/answer.go's
// `answerable`) over the wire record, so the affordance and the daemon agree.
// They MUST: an "a answer" key that opens a card the daemon then refuses is
// worse than no key at all, and the reverse — a session lola would happily
// accept a reply for, with no key offered — is what the old
// `Status == "needs_input"` check produced once a finished turn started
// reporting AgentIdle instead of waiting_input.
//
// Accepted: waiting_input for a reason a human's prose actually answers
// (question, permission prompt, or the "" of a legacy/pane-derived record), and
// idle while the send-keys gate is open. Refused: a modal dialog (the widget
// swallows typed prose and reads the submit Enter as its own answer) and a
// usage limit (the agent cannot act on the reply until its quota resets).
//
// It is the RECORD half only. The daemon additionally re-captures the pane and
// insists it still reads as waiting before one byte is typed, so a card armed
// here can still be refused — correctly — at send time.
func answerable(si protocol.SessionInfo) bool {
	a, _ := axesOf(si)
	switch a {
	case state.AgentWaitingInput:
		switch state.InputReason(si.InputReason) {
		case state.InputQuestion, state.InputPermission, "":
			return true
		}
		return false
	case state.AgentIdle:
		return si.AtPrompt
	}
	return false
}

// needsHuman reports whether a session puts a human on the critical path —
// state.Attention, the ONE attention predicate, shared with the daemon and
// mirrored by the desktop. It is a predicate over BOTH axes rather than a
// status value: the agent is blocked on a person, OR its delivered work
// regressed (CI red, changes requested, branch conflicting). Both can be true
// at once, which is precisely what the collapsed word could not express.
func needsHuman(si protocol.SessionInfo) bool {
	a, d := axesOf(si)
	return state.Attention(a, d)
}

// AttentionCount is how many sessions currently need a human, for the header
// summary bar (e.g. "3 need you").
func AttentionCount(in []protocol.SessionInfo) int {
	n := 0
	for _, s := range in {
		if needsHuman(s) {
			n++
		}
	}
	return n
}

// sortRank buckets a session into the attention-first sort tiers (lower sorts
// first) — state.SortRank over the axis pair, the shared classification.
func sortRank(si protocol.SessionInfo) int {
	a, d := axesOf(si)
	return state.SortRank(a, d)
}

// SortSessions returns a new slice ordered attention-first: blocked on a human,
// then regressed work (ci_failed / changes_requested / merge_conflict), then
// active (a working agent, or a PR still moving under its own steam), then
// parked for review, then quiet, then done; ties break by project then issue
// for a stable, deterministic order. The input slice is never mutated.
func SortSessions(in []protocol.SessionInfo) []protocol.SessionInfo {
	out := make([]protocol.SessionInfo, len(in))
	copy(out, in)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := sortRank(out[i]), sortRank(out[j])
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
// session's issue/project/branch and BOTH of its state words (the rolled-up
// legacy status, so an operator's muscle memory for "ci_failed" still works,
// plus the primary Display word so "/idle" finds what the pill says).
// AttentionOnly keeps only sessions that need a human (needsHuman). Project and
// Status, when non-empty, require an exact match — Status against the rolled-up
// wire word, which is what a caller naming one has in hand.
type Filter struct {
	Text          string
	AttentionOnly bool
	Project       string
	Status        string
}

// matches reports whether a single session satisfies every set dimension of f.
func (f Filter) matches(s protocol.SessionInfo) bool {
	if f.AttentionOnly && !needsHuman(s) {
		return false
	}
	if f.Project != "" && s.Project != f.Project {
		return false
	}
	if f.Status != "" && s.Status != f.Status {
		return false
	}
	if t := strings.TrimSpace(f.Text); t != "" {
		hay := strings.ToLower(s.Issue + " " + s.Project + " " + s.Branch + " " +
			s.Status + " " + displayLabel(displayOf(s)))
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
// Key/Title pair. It carries no status set any more: membership is a function
// of the axis pair, answered by state.KanbanKeyFor.
type KanbanColumn = state.KanbanColumn

// KanbanColumns returns the ordered Board columns, left-to-right by human
// triage priority: the leftmost column is the human's queue.
func KanbanColumns() []KanbanColumn { return state.KanbanColumns() }

// kanbanFallbackKey is the column an unknown/unmapped session routes to.
const kanbanFallbackKey = state.KanbanFallbackKey

// kanbanKeyFor buckets a session into its Board column Key.
func kanbanKeyFor(si protocol.SessionInfo) string {
	a, d := axesOf(si)
	return state.KanbanKeyFor(a, d)
}

// GroupKanban buckets sessions into Board columns keyed by KanbanColumn.Key.
// Every session lands in exactly one column: a pair outside the mapped
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
		out[kanbanKeyFor(s)] = append(out[kanbanKeyFor(s)], s)
	}
	return out
}

// displayBadge is the short (<=2 char) glyph paired with displayStyle's color
// so the PRIMARY axis reads by both shape and hue — never color alone (that
// degrades on mono terminals and for colorblind users). One glyph per Display
// value, all six distinct. Shared by every lens: List cells, Board column
// chips, and the Attention list.
func displayBadge(d state.Display) string {
	switch d {
	case state.DisplayWorking:
		return "wk"
	case state.DisplayIdle:
		return ".."
	case state.DisplayNeedsYou:
		return "!!"
	case state.DisplayGone:
		return "xx"
	case state.DisplayShell:
		return "sh"
	case state.DisplayOrphaned:
		return "or"
	}
	return "??"
}

// displayLabel is the human label for a primary-pill value. Five of the six
// words are already readable as-is; only needs_you has an identifier shape, so
// only it is respelled. Nothing here can leak an underscore, because Display is
// a closed vocabulary.
func displayLabel(d state.Display) string {
	if d == state.DisplayNeedsYou {
		return "needs you"
	}
	return string(d)
}

// statusPillFor renders the STATUS cell for a session: the PRIMARY pill (the
// agent axis) plus, when present, the [statusagent] interpreter's DISAGREEING
// judgement, marked "≈" — it is an approximation from untrusted material, never
// the deterministic truth. "working ≈stuck" says the pipeline believes working,
// the interpreter believes wedged.
//
// The old faint agent-axis badge is gone: it existed only to smuggle the agent
// axis past a delivery-owned rollup, and the pill IS the agent axis now. So is
// the divergence suppression that went with it — there is no divergence left to
// suppress, and the delivery story it was competing with has its own chip
// (prBadge) beside this cell.
func statusPillFor(si protocol.SessionInfo) string {
	pill := displayPill(displayOf(si), si.InputReason)
	if si.InterpretedState != "" {
		return pill + statusOrange.Render("≈"+statusLabel(si.InterpretedState))
	}
	return pill
}

// agentDetailLine renders the detail panel's agent-axis line: the RAW axis word
// (not the reduced Display — the detail panel is where the distinction between
// "starting" and "working", or "exited" and "dead", is worth having), why it is
// waiting (InputReason), the tool the in-flight turn runs, and how fresh the
// last positive activity evidence is (shortAgo, shared with the home screen).
// "" when the record carries no axis (an older daemon).
func agentDetailLine(si protocol.SessionInfo) string {
	if si.AgentState == "" {
		return ""
	}
	parts := []string{statusLabel(si.AgentState)}
	if si.InputReason != "" {
		parts = append(parts, inputReasonLabel(si.InputReason))
	}
	if si.CurrentTool != "" {
		parts = append(parts, "tool "+si.CurrentTool)
	}
	if !si.LastActivityAt.IsZero() {
		parts = append(parts, "active "+shortAgo(si.LastActivityAt)+" ago")
	}
	return "agent:    " + strings.Join(parts, " · ")
}

// inputReasonLabel humanizes why an agent waits. It rides INSIDE the needs_you
// pill ("needs you: permission"), which is what makes that pill actionable
// rather than merely alarming — so the phrases are kept to one short word where
// the de-underscored form would not fit a table column, and two reasons get a
// human phrase their identifier does not carry: "quota_limited" reads like a
// dashboard state rather than the provider's usage limit hitting, and
// "permission_prompt" says twice what "permission" says once. Everything else
// de-underscores like statusLabel's fallback, so an unmapped future reason can
// never leak an identifier.
func inputReasonLabel(reason string) string {
	switch state.InputReason(reason) {
	case state.InputQuotaLimited:
		return "usage limit"
	case state.InputPermission:
		return "permission"
	case state.InputIdleNotify:
		return "idle nudge"
	}
	return strings.ReplaceAll(reason, "_", " ")
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

// StatusDisplay is the shared PRIMARY-axis presentation reused by every session
// lens: a color Style (from displayStyle) plus a short Badge glyph (from
// displayBadge). Keeping color and glyph in one helper guarantees the two views
// never drift.
type StatusDisplay struct {
	Style lipgloss.Style
	Badge string
}

// sessionDisplay returns the shared color+glyph presentation for a session's
// agent axis, folding displayStyle (color) and displayBadge (glyph) into one
// lookup so the List and Board lenses render the primary axis identically.
func sessionDisplay(si protocol.SessionInfo) StatusDisplay {
	d := displayOf(si)
	return StatusDisplay{Style: displayStyle(d), Badge: displayBadge(d)}
}

// displayPill renders the PRIMARY axis as a colored chip — a filled background
// is itself a shape (not color alone), so the pills read on mono/colorblind
// terminals too.
//
// Only ONE state puts a human on the agent's critical path, so only it gets a
// SOLID, bold fill: needs_you, which also carries WHY it is blocked
// ("needs you: permission") — a pill that says a human is needed without saying
// what for is an alarm, not an affordance, and the reason was previously buried
// in a single detail line. The two live-agent states get a SUBTLE tint (blue
// for a running turn, neutral grey for a resting one, so the working↔idle
// distinction the old rollup hid is legible at a glance); the terminal ones are
// plain dim text, with the orphan anomaly in warn so it is noticeable without
// joining the queue.
//
// Note what is NOT here: nothing about the PR. Delivery is the secondary chip
// (prBadge) drawn beside this cell — that separation IS the design.
func displayPill(d state.Display, reason string) string {
	label := displayLabel(d)
	switch d {
	case state.DisplayNeedsYou:
		if why := inputReasonLabel(reason); why != "" {
			label += ": " + why
		}
		return pillFill(pillUrgentBg, pillUrgentFg, label) // solid amber, near-black text
	case state.DisplayWorking:
		return pillTint(pillWorkBg, pillWorkFg, label) // tinted blue
	case state.DisplayIdle:
		return pillTint(pillGreyBg, pillGreyFg, label) // neutral grey
	case state.DisplayOrphaned:
		return " " + warnText.Render(label) + " "
	}
	// gone / shell: quiet, terminal, nothing to act on.
	return " " + faintText.Render(label) + " "
}

// statusLabel is the human label for a RAW axis word — the agent axis
// (agentDetailLine) and the [statusagent] interpreter's judgement
// (statusPillFor's "≈" marker) — plus the legacy rolled-up words the activity
// feed still carries. Every raw identifier gets a readable spelling: a rendered
// "ci_failed" reads like a translation placeholder, not a badge. Long words are
// shortened so a narrow column stays tight ("changes_requested" is 17 columns).
// The mapping is display-only; every control-flow comparison uses the typed
// axis values. The fallback de-underscores, so an unmapped future word can
// never leak an identifier into the UI.
//
// The PRIMARY pill does NOT come through here — it is a closed vocabulary with
// its own displayLabel.
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
