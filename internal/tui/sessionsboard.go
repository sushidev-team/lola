// Sessions-tab lenses (PLAN P8): the List/Kanban rendering and the shared
// ID-based navigation that lets a lens switch keep the same session focused.
// Every helper here is pure over the SAME cmd=sessions snapshot — ordering,
// filtering, and grouping live in sessionview.go; this file is only the
// bubbletea glue (selection movement + string rendering). Every rendered line
// is ANSI-clipped (previewLine / padTo) so no lens can corrupt the repaint.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sushidev-team/lola/internal/protocol"
)

// indexOfID returns the position of the session with id in ss, or -1. Used to
// keep s.cursor (a raw index, the one selected() reads) in sync with s.selID
// (the authoritative, reorder-proof selection) after any data change.
func indexOfID(ss []protocol.SessionInfo, id string) int {
	if id == "" {
		return -1
	}
	for i := range ss {
		if ss[i].ID == id {
			return i
		}
	}
	return -1
}

// effectiveSelID is the session ID the views should highlight: the pinned
// selID, or — on legacy/direct paths that only set the raw cursor — the ID that
// selected() resolves to. Keeping the two in agreement means the highlighted
// row and the detail pane always describe the same session.
func (m *rootModel) effectiveSelID() string {
	if m.sessions.selID != "" {
		return m.sessions.selID
	}
	if sel := m.sessions.selected(); sel != nil {
		return sel.ID
	}
	return ""
}

// listRows is the List lens's ordering: attention-first (SortSessions) then
// narrowed by the active filter (Apply). Pure and cheap — recomputed each
// render so a mid-interaction tick never re-sorts a row out from under the
// cursor (selection is by ID, not by position).
func (s *sessionsModel) listRows() []protocol.SessionInfo {
	if s.data == nil {
		return nil
	}
	return Apply(SortSessions(s.data.Sessions), s.filter)
}

// kanbanLayout returns the Board columns plus the sessions grouped into them,
// each column attention-first (SortSessions) and filtered (Apply) exactly like
// the List lens so the two lenses never disagree about what is visible.
func (s *sessionsModel) kanbanLayout() ([]KanbanColumn, map[string][]protocol.SessionInfo) {
	cols := KanbanColumns()
	var in []protocol.SessionInfo
	if s.data != nil {
		in = SortSessions(Apply(s.data.Sessions, s.filter))
	}
	return cols, GroupKanban(in)
}

// primaryOrder flattens the active lens into a single visitation order, used to
// pick a fresh selection when the pinned session vanishes (see
// handleSessionsMsg): the List order, or the Kanban columns left-to-right.
func (s *sessionsModel) primaryOrder() []protocol.SessionInfo {
	if s.view == viewKanban {
		cols, groups := s.kanbanLayout()
		var out []protocol.SessionInfo
		for _, c := range cols {
			out = append(out, groups[c.Key]...)
		}
		return out
	}
	return s.listRows()
}

// selectID pins the selection to id (and syncs the raw cursor selected() reads)
// then refetches that session's pane so the glance/attention card follow the
// cursor. Returns nil when id is unknown (no selection change).
func (m *rootModel) selectID(id string) tea.Cmd {
	s := &m.sessions
	idx := indexOfID(s.data.Sessions, id)
	if idx < 0 {
		return nil
	}
	s.selID, s.cursor = id, idx
	return m.paneRefreshCmd()
}

// sessMove is the shared cursor movement for both lenses. In List, dRow steps
// the flat ordering (dCol is inert). In Kanban, dRow moves within a column and
// dCol jumps to the nearest non-empty column left/right, keeping the row index.
func (m *rootModel) sessMove(dCol, dRow int) (tea.Model, tea.Cmd) {
	s := &m.sessions
	if s.data == nil {
		return m, nil
	}
	if s.view == viewKanban {
		return m.kanbanMove(dCol, dRow)
	}
	// List lens: only vertical movement matters.
	if dRow == 0 {
		return m, nil
	}
	rows := s.listRows()
	if len(rows) == 0 {
		return m, nil
	}
	i := indexOfID(rows, s.selID)
	if i < 0 {
		return m, m.selectID(rows[0].ID)
	}
	j := i + dRow
	if j < 0 || j >= len(rows) {
		return m, nil
	}
	return m, m.selectID(rows[j].ID)
}

// sessJumpEdge jumps to the top (g) or bottom (G) of the current lens's
// ordering — O(1) navigation for long lists.
func (m *rootModel) sessJumpEdge(top bool) (tea.Model, tea.Cmd) {
	ord := m.sessions.primaryOrder()
	if len(ord) == 0 {
		return m, nil
	}
	if top {
		return m, m.selectID(ord[0].ID)
	}
	return m, m.selectID(ord[len(ord)-1].ID)
}

// kanbanPos locates selID as a (column, row) coordinate in the grouped board,
// or (-1,-1) when the selection is not currently on the board.
func kanbanPos(cols []KanbanColumn, groups map[string][]protocol.SessionInfo, selID string) (int, int) {
	for ci, c := range cols {
		for ri, si := range groups[c.Key] {
			if si.ID == selID {
				return ci, ri
			}
		}
	}
	return -1, -1
}

// kanbanMove moves the board cursor: dRow within the current column, dCol to
// the nearest non-empty column in that direction (row index clamped). When the
// selection is off-board it lands on the first card of the first non-empty
// column.
func (m *rootModel) kanbanMove(dCol, dRow int) (tea.Model, tea.Cmd) {
	s := &m.sessions
	cols, groups := s.kanbanLayout()
	ci, ri := kanbanPos(cols, groups, s.selID)
	if ci < 0 {
		for _, c := range cols {
			if len(groups[c.Key]) > 0 {
				return m, m.selectID(groups[c.Key][0].ID)
			}
		}
		return m, nil
	}
	if dRow != 0 {
		col := groups[cols[ci].Key]
		nr := ri + dRow
		if nr < 0 || nr >= len(col) {
			return m, nil
		}
		return m, m.selectID(col[nr].ID)
	}
	// Column move: skip empty columns so h/l always land on a card.
	for nc := ci + dCol; nc >= 0 && nc < len(cols); nc += dCol {
		col := groups[cols[nc].Key]
		if len(col) == 0 {
			continue
		}
		nr := ri
		if nr >= len(col) {
			nr = len(col) - 1
		}
		return m, m.selectID(col[nr].ID)
	}
	return m, nil
}

// reselectVisible re-pins the selection to the top of the visible List order
// when the current selection was filtered out (e.g. after toggling
// AttentionOnly), so the cursor never points at a hidden row. Returns nil when
// the selection is still visible.
func (m *rootModel) reselectVisible() tea.Cmd {
	s := &m.sessions
	rows := s.listRows()
	if indexOfID(rows, s.selID) >= 0 {
		return nil
	}
	if len(rows) > 0 {
		return m.selectID(rows[0].ID)
	}
	return nil
}

// updateFilter drives the "/" filter bar: printable runes narrow the list live
// (Apply re-runs each render), backspace deletes, enter applies and closes, esc
// clears and closes. Every edit re-pins selection so it stays on a visible row.
func (m *rootModel) updateFilter(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := &m.sessions
	switch k.String() {
	case "esc":
		s.filtering, s.filter.Text = false, ""
		return m, m.reselectVisible()
	case "enter":
		s.filtering = false
		return m, m.reselectVisible()
	case "backspace":
		if r := []rune(s.filter.Text); len(r) > 0 {
			s.filter.Text = string(r[:len(r)-1])
		}
		return m, m.reselectVisible()
	}
	switch {
	case k.Type == tea.KeyRunes:
		s.filter.Text += string(k.Runes)
	case k.String() == " ":
		s.filter.Text += " "
	}
	return m, m.reselectVisible()
}

// ---- rendering ----

// sessionsSummary is the persistent header strip: the active lens plus a
// glance-level triage count recomputed each tick (attention-first). "N need
// you" is orange when non-zero so the human's queue depth reads at a glance.
func (m *rootModel) sessionsSummary() string {
	s := &m.sessions
	var sess []protocol.SessionInfo
	if s.data != nil {
		sess = s.data.Sessions
	}
	need := AttentionCount(sess)
	work, ready, done := 0, 0, 0
	for _, x := range sess {
		switch sortRank(x.Status) {
		case 2:
			work++
		case 3:
			ready++
		case 5:
			done++
		}
	}
	label := "list"
	if s.view == viewKanban {
		label = "kanban"
	}
	needStr := fmt.Sprintf("%d need you", need)
	if need > 0 {
		needStr = statusOrange.Render(needStr)
	}
	line := fmt.Sprintf("%s  ·  %s  ·  %d working  ·  %d ready  ·  %d done  ·  %d total",
		titleStyle.Render(label), needStr, work, ready, done, len(sess))
	if s.filter.AttentionOnly {
		line += faintText.Render("  ·  [needs-you only]")
	}
	return previewLine(line, m.width)
}

// filterBar renders the live "/" filter input (k9s-style). While open it shows
// a trailing caret; once applied it shows the standing filter text.
func (m *rootModel) filterBar() string {
	s := &m.sessions
	caret := ""
	if s.filtering {
		caret = "_"
	}
	return previewLine(warnText.Render("/")+s.filter.Text+caret, m.width)
}

// sessionsFooter shows only the keys relevant to the current lens and the
// selected session (e.g. "a answer" only when the selection is needs_input),
// then the standing verbs. A single clipped line — discoverable without clutter.
func (m *rootModel) sessionsFooter() string {
	s := &m.sessions
	if s.filtering {
		return previewLine(faintText.Render("type to filter · enter apply · esc clear"), m.width)
	}
	if s.answering {
		return previewLine(faintText.Render("enter send · esc cancel"), m.width)
	}
	move := "↑/↓ move"
	if s.view == viewKanban {
		move = "↑↓←→ move"
	}
	keys := []string{move, "enter attach"}
	if sel := s.selected(); sel != nil {
		if sel.Status == "needs_input" {
			keys = append(keys, "a answer")
		}
		if hasPR(*sel) {
			keys = append(keys, "o PR")
		}
	}
	keys = append(keys, "x kill", "V lens", "/ filter", "! needs-you", "n next!", "v pane", "tab switch", "q quit")
	return previewLine(faintText.Render(strings.Join(keys, " · ")), m.width)
}

// kanbanBoard renders the Board lens: fixed human-intent columns side by side,
// each a titled stack of compact cards. Columns shrink to fit the width; below
// a legibility floor it degrades to a condensed vertical grouping
// (kanbanNarrow) rather than smearing unreadable slivers.
func (m *rootModel) kanbanBoard() string {
	s := &m.sessions
	cols, groups := s.kanbanLayout()
	width := m.width
	if width <= 0 {
		width = 80
	}
	n := len(cols)
	const gap = 1
	colW := (width - (n-1)*gap) / n
	if colW < 14 {
		return m.kanbanNarrow(cols, groups, width)
	}
	if colW > 26 {
		colW = 26
	}
	selID := m.effectiveSelID()
	rendered := make([][]string, n)
	maxH := 0
	for i, c := range cols {
		rendered[i] = m.kanbanColumnLines(c, groups[c.Key], colW, selID)
		if len(rendered[i]) > maxH {
			maxH = len(rendered[i])
		}
	}
	var b strings.Builder
	sep := strings.Repeat(" ", gap)
	for row := 0; row < maxH; row++ {
		cells := make([]string, n)
		for i := range cols {
			cell := ""
			if row < len(rendered[i]) {
				cell = rendered[i][row]
			}
			cells[i] = padTo(cell, colW)
		}
		b.WriteString(strings.TrimRight(strings.Join(cells, sep), " ") + "\n")
	}
	return b.String()
}

// kanbanColumnLines builds one column's lines: a bold title with its count,
// then two lines per card (issue with marker, then a status-badge/PR/reacting
// meta line). The selected card gets a "›" + reverse; an unselected needs_input
// card a warn "!" so the human's queue is visible column-wide.
func (m *rootModel) kanbanColumnLines(c KanbanColumn, sess []protocol.SessionInfo, w int, selID string) []string {
	// Header: bold title + count badge, then a faint rule the column's width so
	// each column reads as a distinct region even when empty.
	rule := w - 1
	if rule < 1 {
		rule = 1
	}
	out := []string{
		tblHeader.Render(c.Title) + faintText.Render(fmt.Sprintf(" %d", len(sess))),
		faintText.Render(strings.Repeat("─", rule)),
	}
	if len(sess) == 0 {
		// A faint, indented placeholder rather than a lone dash at the margin.
		return append(out, "  "+faintText.Render("empty"))
	}
	for _, si := range sess {
		marker := "  "
		issue := si.Issue
		switch {
		case si.ID == selID:
			marker = "› "
			issue = selStyle.Render(issue)
		case si.Status == "needs_input":
			marker = warnText.Render("! ")
		}
		out = append(out, marker+issue)

		d := statusDisplay(si.Status)
		meta := d.Style.Render(d.Badge)
		// PR badge ("#229 ✓") on any card with a PR — the number plus the checks
		// glyph, scannable at a glance and never gated on status/review state. The
		// reacting label is deliberately omitted here: at column width it clips
		// mid-word; it stays legible in the List lens and the Detail panel.
		if pr := prBadge(si); pr != "" {
			meta += " " + pr
		}
		out = append(out, "  "+meta, "")
	}
	return out
}

// kanbanNarrow is the small-width fallback for the Board: a condensed vertical
// list grouped by column title, one line per card, so triage survives on an
// 80-cols-and-under terminal without horizontal smear.
func (m *rootModel) kanbanNarrow(cols []KanbanColumn, groups map[string][]protocol.SessionInfo, width int) string {
	selID := m.effectiveSelID()
	var b strings.Builder
	// The hint (~50 cols) and per-column title lines must be width-clipped too:
	// this fallback stays active up to width 73, so at the very narrow widths it
	// targets an unclipped line would physically wrap and desync bubbletea's
	// line-count repaint — the same hazard previewLine guards for the cards.
	b.WriteString(previewLine(faintText.Render("(board condensed — widen the terminal for columns)"), width) + "\n")
	for _, c := range cols {
		sess := groups[c.Key]
		b.WriteString(previewLine(tblHeader.Render(c.Title)+faintText.Render(fmt.Sprintf(" (%d)", len(sess))), width) + "\n")
		for _, si := range sess {
			marker := "  "
			switch {
			case si.ID == selID:
				marker = "› "
			case si.Status == "needs_input":
				marker = warnText.Render("! ")
			}
			d := statusDisplay(si.Status)
			b.WriteString(previewLine(marker+si.Issue+" "+d.Style.Render(d.Badge), width) + "\n")
		}
	}
	return b.String()
}

// padTo pads (or ANSI-clips) an already-styled cell to exactly w display
// columns so kanban columns align regardless of the ANSI in a status badge or
// reacting label — the same width hazard previewLine guards for pane rows. A
// clipped cell loses its trailing reset, so re-append one.
func padTo(s string, w int) string {
	cw := lipgloss.Width(s)
	if cw > w {
		return truncateANSI(s, w) + "\x1b[0m"
	}
	return s + strings.Repeat(" ", w-cw)
}
