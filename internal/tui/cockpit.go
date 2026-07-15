// The unified cockpit frame (TUI redesign): one screen composed of an always-on
// vitals bar, a left rail (Triage + Polls), a main column (Sessions + Detail),
// and a persistent keybar — the btop/lazygit/k9s structure. It is built
// ADDITIVELY: the body of each panel reuses the existing render helpers
// (sessionsTable/sessionDetail/listRows/…), clipped into an exact box() so the
// alt-screen repaint stays line-count-stable. Focus (m.focus) selects which
// panel owns navigation/action keys; the border of the focused panel is drawn
// in the accent color.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/sushidev-team/lola/internal/protocol"
)

// focus targets — which panel currently owns navigation/action keystrokes.
const (
	focusSessions = iota
	focusPolls
)

// cockpitView renders the whole single-screen frame. Below a minimum size it
// degrades to a plain stacked view (narrowCockpit) rather than smearing
// unreadable slivers, mirroring the kanbanNarrow discipline.
func (m *rootModel) cockpitView() string {
	return strings.Join(m.cockpitLines(), "\n")
}

// cockpitLines is cockpitView split out so a modal overlay can composite over
// the exact frame lines (placeModal). It always returns the full-height frame.
func (m *rootModel) cockpitLines() []string {
	W, H := m.width, m.height
	// Before the first WindowSizeMsg (and in unit tests) the size is unknown;
	// fall back to a sensible default frame rather than rendering nothing.
	if W <= 0 {
		W = 100
	}
	if H <= 0 {
		H = 24
	}
	if W < 72 || H < 18 {
		return strings.Split(m.narrowCockpit(), "\n")
	}

	vitals := m.vitalsBar(W)
	keys := m.keybar(W)
	msg := m.cockpitMessage()

	var topExtra []string
	if m.sessions.filtering || m.sessions.filter.Text != "" {
		topExtra = append(topExtra, m.filterBar())
	}

	usedTop := 1 + len(topExtra) // vitals + optional filter bar
	usedBottom := 1              // keybar
	if msg != "" {
		usedBottom++
	}
	midH := H - usedTop - usedBottom
	if midH < 6 {
		midH = 6
	}

	railW := 32
	if W < 104 {
		railW = 28
	}
	const gap = 1
	mainW := W - railW - gap

	mid := joinCols(gap, m.railColumn(railW, midH), m.mainColumn(mainW, midH))

	lines := []string{vitals}
	lines = append(lines, topExtra...)
	lines = append(lines, mid...)
	if msg != "" {
		lines = append(lines, msg)
	}
	lines = append(lines, keys)
	return lines
}

// modalOver composites a bordered, focused modal box (built from title +
// content) centered over the dimmed cockpit frame — the lazygit/k9s floating
// overlay. The cockpit stays visible (faint) behind it for context.
func (m *rootModel) modalOver(title string, content []string) string {
	W, H := m.width, m.height
	if W <= 0 {
		W = 100
	}
	if H <= 0 {
		H = 24
	}
	bg := m.cockpitLines()
	mw := W - 8
	if mw > 76 {
		mw = 76
	}
	if mw < 20 {
		mw = 20
	}
	mh := len(content) + 2 // + borders
	if max := H - 4; mh > max {
		mh = max
	}
	if mh < 3 {
		mh = 3
	}
	modal := box(title, content, mw, mh, true)
	return strings.Join(placeModal(bg, modal, W), "\n")
}

// doctorModal renders the health report (or the running placeholder) as a modal
// floating over the cockpit. It reuses doctorReportLines and the same scroll
// window the full-screen overlay used, sized to the modal height.
func (m *rootModel) doctorModal() string {
	if m.doctorReport == nil {
		return m.modalOver("doctor", []string{faintText.Render("running checks…"), "", faintText.Render("esc close")})
	}
	rep := *m.doctorReport
	lines := doctorReportLines(rep)
	H := m.height
	if H <= 0 {
		H = 24
	}
	win := H - 10 // leave room for borders, summary, hint, and the ↑/↓ markers
	if win < 4 {
		win = 4
	}
	if maxScroll := len(lines) - win; m.doctorScroll > maxScroll {
		if maxScroll < 0 {
			maxScroll = 0
		}
		m.doctorScroll = maxScroll
	}
	start := m.doctorScroll
	end := start + win
	if end > len(lines) {
		end = len(lines)
	}
	var content []string
	if start > 0 {
		content = append(content, faintText.Render("  ↑ more"))
	}
	content = append(content, lines[start:end]...)
	if end < len(lines) {
		content = append(content, faintText.Render("  ↓ more"))
	}
	content = append(content, "", rep.Summary(), faintText.Render("↑/↓ scroll · esc close"))
	return m.modalOver("doctor", content)
}

// railColumn stacks the fixed-height Triage panel over a Polls panel that takes
// the remaining height.
func (m *rootModel) railColumn(w, h int) []string {
	triageH := 10
	if triageH > h-4 {
		triageH = h - 4
	}
	if triageH < 4 {
		triageH = 4
	}
	pollsH := h - triageH
	triage := box("❶ Triage", m.triageBody(w-2), w, triageH, false)
	polls := box("❸ Polls", m.pollsBody(w-2, pollsH-2), w, pollsH, m.focus == focusPolls)
	return stackRows(triage, polls)
}

// mainColumn stacks the Sessions table over a Detail panel sized to the current
// pane view (compact/full), clamped so Sessions always keeps a usable height.
func (m *rootModel) mainColumn(w, h int) []string {
	detailH := m.sessions.paneLines() + 8 // header + meta + card + pane + borders
	if maxD := h - 8; detailH > maxD {
		detailH = maxD
	}
	if detailH < 6 {
		detailH = 6
	}
	sessH := h - detailH
	sess := box("❷ Sessions", m.sessionsBody(w-2, sessH-2), w, sessH, m.focus == focusSessions)
	det := box("❹ Detail", m.detailBody(w-2, detailH-2), w, detailH, false)
	return stackRows(sess, det)
}

// ---- panel bodies (reuse existing helpers, clipped to the box) ----

// sessionsBody renders the attention-first table as body lines, windowed so the
// selected row stays visible. Column widths are computed over the FULL list
// (not just the visible window) so columns don't jitter while scrolling.
func (m *rootModel) sessionsBody(w, h int) []string {
	s := &m.sessions
	if s.data == nil {
		return []string{faintText.Render("fetching sessions…")}
	}
	// Lead with the compact summary strip (lens label · attention count ·
	// working/ready/done · needs-you-only flag) — the textual k9s-style header
	// that complements the Triage meters in the rail.
	summary := previewLine(m.sessionsSummary(), w)
	// The Board lens (V) swaps the table for kanban columns within the panel.
	if s.view == viewKanban {
		return append([]string{summary}, m.kanbanBodyAt(w, h-1)...)
	}
	headers := []string{" ", "ISSUE", "PROJECT", "STATUS", "PR", "REACTING", "AGE"}
	list := s.listRows()
	selID := m.effectiveSelID()
	rows := make([][]string, len(list))
	selRow := -1
	for i, si := range list {
		marker := " "
		if si.Status == "needs_input" {
			marker = warnText.Render("!")
		}
		if si.ID == selID {
			marker = "›"
			selRow = i
		}
		pr := prBadge(si)
		if pr == "" {
			pr = "-"
		}
		rows[i] = []string{
			marker, si.Issue, si.Project,
			statusPill(si.Status), pr,
			reactingStyle(si.Reacting).Render(dash(si.Reacting)), dash(si.Age),
		}
	}
	colw := colWidths(headers, rows)
	out := []string{summary, previewLine(tblHeader.Render(padCells(headers, colw)), w)}
	if len(rows) == 0 {
		empty := "no sessions observed"
		if len(s.data.Sessions) > 0 {
			empty = "no sessions match the filter"
		}
		return append(out, faintText.Render(empty))
	}
	bodyH := h - 2 // minus the summary + header rows
	if bodyH < 1 {
		bodyH = 1
	}
	start := viewportStart(selRow, len(rows), bodyH)
	end := start + bodyH
	if end > len(rows) {
		end = len(rows)
	}
	for i := start; i < end; i++ {
		out = append(out, previewLine(padCells(rows[i], colw), w))
	}
	return out
}

// detailBody wraps sessionDetail() into body lines, clipped to width and held
// to the top of the block (the answer card + meta lead; the live pane tail
// clips first when space is tight).
func (m *rootModel) detailBody(w, h int) []string {
	raw := m.sessionDetail()
	if strings.TrimSpace(raw) == "" {
		return []string{faintText.Render("no session selected")}
	}
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		out = append(out, previewLine(ln, w))
	}
	if len(out) > h {
		out = out[:h]
	}
	return out
}

// pollsBody lists the configured polls with a health dot (green ok / red error /
// hollow paused) and last-run age; the cursor marker shows only while the Polls
// panel is focused.
func (m *rootModel) pollsBody(w, h int) []string {
	l := &m.list
	if len(m.cfg.Polls) == 0 {
		return []string{faintText.Render("no polls — n to add")}
	}
	rows := make([]string, 0, len(m.cfg.Polls))
	for i, p := range m.cfg.Polls {
		enabled := p.Enabled
		last, errd := "-", false
		if ps := l.pollStatus(p.Name); ps != nil {
			enabled = ps.Enabled
			last = fmtAgo(ps.LastRun)
			if ps.Running {
				last = "run…"
			}
			errd = ps.LastError != ""
		}
		dot := faintText.Render("○")
		switch {
		case errd:
			dot = badText.Render("●")
		case enabled:
			dot = goodText.Render("●")
		}
		marker := " "
		if m.focus == focusPolls && i == l.cursor {
			marker = boxTitleHi.Render("›")
		}
		name := p.Name
		if !enabled {
			name = faintText.Render(name)
		}
		left := marker + dot + " " + name
		// Right-align the last-run age within the width.
		gap := w - lipgloss.Width(left) - lipgloss.Width(last)
		if gap < 1 {
			gap = 1
		}
		rows = append(rows, previewLine(left+strings.Repeat(" ", gap)+faintText.Render(last), w))
	}
	if len(rows) > h {
		rows = rows[:h]
	}
	return rows
}

// triageBody is the btop-style summary: a hero "N need you", then proportional
// meters for working / ready / fixing, then a total. The sparkline is added in a
// later pass.
func (m *rootModel) triageBody(w int) []string {
	var sess []protocol.SessionInfo
	if m.sessions.data != nil {
		sess = m.sessions.data.Sessions
	}
	need := AttentionCount(sess)
	work, ready, fix := 0, 0, 0
	for _, x := range sess {
		switch sortRank(x.Status) {
		case 1:
			fix++
		case 2:
			work++
		case 3:
			ready++
		}
	}
	total := len(sess)

	var out []string
	if need > 0 {
		out = append(out, statusOrange.Render(fmt.Sprintf("%d", need))+" "+statusOrange.Render("NEED YOU"))
	} else {
		out = append(out, goodText.Render("0")+" "+faintText.Render("all clear"))
	}
	if spark := sparkline(m.attnHist, w-10); spark != "" {
		out = append(out, spark+" "+faintText.Render("needs-you"))
	} else {
		out = append(out, "")
	}
	meterW := w - 12
	if meterW < 4 {
		meterW = 4
	}
	out = append(out, triageMeter("working", work, total, meterW, statusBlue))
	out = append(out, triageMeter("ready", ready, total, meterW, goodText))
	out = append(out, triageMeter("fixing", fix, total, meterW, badText))
	out = append(out, faintText.Render(fmt.Sprintf("%d total", total)))
	return out
}

// triageMeter renders "Nlabel  ███░░░" — a right-padded count+label followed by
// a proportional bar (filled cells in the category color, empty faint).
func triageMeter(label string, n, total, w int, style lipgloss.Style) string {
	head := fmt.Sprintf("%2d %-7s", n, label)
	filled := 0
	if total > 0 {
		filled = n * w / total
	}
	if filled > w {
		filled = w
	}
	bar := style.Render(strings.Repeat("█", filled)) + faintText.Render(strings.Repeat("░", w-filled))
	return head + bar
}

// vitalsBar is the always-on top strip: daemon/runtime/linear health, session
// and poll counts, and a clock right-aligned.
func (m *rootModel) vitalsBar(w int) string {
	st := m.list.status
	sep := faintText.Render(" · ")
	parts := []string{}
	if st == nil {
		parts = append(parts, "daemon "+badText.Render("○ down"))
	} else {
		parts = append(parts, "daemon "+goodText.Render("● running"))
		rt := goodText.Render("✓")
		if !st.RuntimeOK {
			rt = badText.Render("✗")
		}
		parts = append(parts, "runtime "+rt, "linear "+yesNoStyled(st.LinearOK, "✓", "✗"))
	}
	nSess := 0
	if m.sessions.data != nil {
		nSess = len(m.sessions.data.Sessions)
	}
	en := 0
	for _, p := range m.cfg.Polls {
		enabled := p.Enabled
		if ps := m.list.pollStatus(p.Name); ps != nil {
			enabled = ps.Enabled
		}
		if enabled {
			en++
		}
	}
	parts = append(parts, fmt.Sprintf("sessions %d", nSess), fmt.Sprintf("polls %d/%d", en, len(m.cfg.Polls)))

	brand := lipgloss.NewStyle().Bold(true).Render("lola")
	left := brand + "  " + strings.Join(parts, sep)
	clock := time.Now().Format("15:04")
	pad := w - lipgloss.Width(left) - lipgloss.Width(clock)
	if pad < 1 {
		pad = 1
	}
	return previewLine(left+strings.Repeat(" ", pad)+faintText.Render(clock), w)
}

// cockpitMessage is the single optional status line above the keybar: a pending
// confirmation, a flash, or the daemon-down banner.
func (m *rootModel) cockpitMessage() string {
	s := &m.sessions
	switch {
	case s.confirmKill:
		return warnText.Render(fmt.Sprintf("kill session %q? (y/n)", s.killTarget))
	case m.list.confirmDelete:
		name := ""
		if p := m.selectedPoll(); p != nil {
			name = p.Name
		}
		return warnText.Render(fmt.Sprintf("delete poll %q? (y/n)", name))
	case s.flash != "":
		if s.flashGood {
			return goodText.Render(s.flash)
		}
		return warnText.Render(s.flash)
	case m.list.flash != "":
		return faintText.Render(m.list.flash)
	case s.daemonDown:
		return badText.Render("daemon: not running") + faintText.Render("  (start with: lola run)")
	case s.dataErr != "":
		return badText.Render("sessions: " + s.dataErr)
	}
	return ""
}

// keybar is the persistent bottom strip, context-sensitive to focus, the active
// mode (filter/answer/confirm), and the selected session.
func (m *rootModel) keybar(w int) string {
	s := &m.sessions
	switch {
	case s.filtering:
		return previewLine(faintText.Render("type to filter · enter apply · esc clear"), w)
	case s.answering:
		return previewLine(faintText.Render("enter send · esc cancel"), w)
	case s.confirmKill:
		return previewLine(warnText.Render("y")+faintText.Render(" kill · ")+warnText.Render("n")+faintText.Render(" cancel"), w)
	case m.list.confirmDelete:
		return previewLine(warnText.Render("y")+faintText.Render(" delete poll · ")+warnText.Render("n")+faintText.Render(" cancel"), w)
	}
	var keys []string
	if m.focus == focusPolls {
		keys = []string{"↑↓ move", "n new", "enter edit", "space toggle", "x delete", "r cache", "tab → sessions"}
	} else {
		keys = []string{"↑↓ move", "enter attach"}
		if sel := s.selected(); sel != nil {
			if sel.Status == "needs_input" {
				keys = append(keys, "a answer")
			}
			if sel.PRURL != "" {
				keys = append(keys, "o PR")
			}
		}
		keys = append(keys, "x kill", "/ filter", "! needs-you", "V lens", "n next!", "tab → polls")
	}
	keys = append(keys, "d doctor", "q quit")
	return previewLine(faintText.Render(strings.Join(keys, " · ")), w)
}

// narrowCockpit is the small-terminal fallback: vitals, the sessions table, and
// the keybar stacked plainly (no boxes) so triage survives a cramped window.
func (m *rootModel) narrowCockpit() string {
	W, H := m.width, m.height
	if W <= 0 {
		W = 100
	}
	if H <= 0 {
		H = 24
	}
	rem := H - 2 // vitals + keybar
	if rem < 2 {
		rem = 2
	}
	sessH := rem / 2
	detH := rem - sessH
	out := []string{m.vitalsBar(W)}
	out = append(out, m.sessionsBody(W, sessH)...)
	out = append(out, m.detailBody(W, detH)...)
	out = append(out, m.keybar(W))
	return strings.Join(out, "\n")
}

// kanbanBodyAt renders the Board lens into panel body lines at a given width,
// reusing the same column/narrow-fallback logic as the standalone kanbanBoard
// but bounded to the panel box (width for the columns, height for the clip).
func (m *rootModel) kanbanBodyAt(width, height int) []string {
	s := &m.sessions
	cols, groups := s.kanbanLayout()
	n := len(cols)
	const gap = 1
	if width < 4 {
		width = 4
	}
	var lines []string
	colW := (width - (n-1)*gap) / n
	if colW < 14 {
		lines = strings.Split(strings.TrimRight(m.kanbanNarrow(cols, groups, width), "\n"), "\n")
	} else {
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
			lines = append(lines, previewLine(strings.TrimRight(strings.Join(cells, sep), " "), width))
		}
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

// attnHistCap bounds the needs-you sparkline ring.
const attnHistCap = 60

// recordAttn samples the current "need you" count into the bounded history ring
// that backs the Triage sparkline. Called once per sessions fetch.
func (m *rootModel) recordAttn() {
	n := 0
	if m.sessions.data != nil {
		n = AttentionCount(m.sessions.data.Sessions)
	}
	m.attnHist = append(m.attnHist, n)
	if len(m.attnHist) > attnHistCap {
		m.attnHist = m.attnHist[len(m.attnHist)-attnHistCap:]
	}
}

// sparkline renders the last `width` samples as colored block glyphs scaled to
// the window's max (green low → yellow → orange high) so the needs-you trend
// reads at a glance. A zero sample renders as a faint dot. Empty history (or a
// non-positive width) renders nothing.
func sparkline(vals []int, width int) string {
	if width < 1 || len(vals) == 0 {
		return ""
	}
	if len(vals) > width {
		vals = vals[len(vals)-width:]
	}
	max := 1
	for _, v := range vals {
		if v > max {
			max = v
		}
	}
	blocks := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, v := range vals {
		if v <= 0 {
			b.WriteString(faintText.Render("·"))
			continue
		}
		lvl := (v*len(blocks) - 1) / max
		if lvl < 0 {
			lvl = 0
		}
		if lvl >= len(blocks) {
			lvl = len(blocks) - 1
		}
		style := goodText
		switch {
		case lvl >= 6:
			style = statusOrange
		case lvl >= 3:
			style = warnText
		}
		b.WriteString(style.Render(string(blocks[lvl])))
	}
	return b.String()
}

// viewportStart returns the first visible index of an h-tall window over n rows
// that keeps sel roughly centered and within [0, n-h].
func viewportStart(sel, n, h int) int {
	if n <= h || sel < 0 {
		return 0
	}
	start := sel - h/2
	if start < 0 {
		start = 0
	}
	if start > n-h {
		start = n - h
	}
	return start
}
