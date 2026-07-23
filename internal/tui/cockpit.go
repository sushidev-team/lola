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

	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/protocol"
)

// focus targets — which panel currently owns navigation/action keystrokes.
const (
	focusSessions = iota
	focusPolls
)

// meterTrack is the unfilled portion of a Triage bar: a thin, very dark rule so
// the empty track reads as a subtle line, never a gray block.
var meterTrack = lipgloss.NewStyle().Foreground(lipgloss.Color(colBorder))

// paneBadge renders a pane's name as a small, solid slate chip with a bold
// near-white label. It previously held an ordinal digit (` 1 `), but the digit
// read as a "press N to jump" affordance that nothing honored, so the name now
// lives in the chip instead. A neutral chip (rather than accent) keeps the cyan
// focus border as the sole focus cue.
var paneBadge = lipgloss.NewStyle().
	Background(lipgloss.Color(colBorder)).
	Foreground(lipgloss.Color("#eef2f6")).
	Bold(true)

// paneTitle composes a cockpit pane heading: the name chip, then optional faint
// context. It carries its own ANSI so box() places it verbatim (the chip is
// neutral regardless of focus; the border color signals focus).
func paneTitle(name, extra string) string {
	t := paneBadge.Render(" " + name + " ")
	if extra != "" {
		t += faintText.Render(" · " + extra)
	}
	return t
}

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
		// Clip to width: flashes carry raw daemon errors (e.g. a git failure on a
		// dirty-worktree kill) that would otherwise wrap and smear the frame.
		lines = append(lines, previewLine(msg, W))
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
	bg := m.backdropLines()
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

// helpModal floats a static keybinding reference over the current screen. It is
// the home for every shortcut the trimmed keybar no longer prints; '?' opens it
// from any screen (cockpit, home, detail, pickers) and esc/'?'/q close it — both
// wired in Update. Two columns so the full cheat-sheet fits without scrolling.
func (m *rootModel) helpModal() string {
	keyStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colAccent))
	const keyW = 9
	row := func(k, d string) string { return padTo(keyStyle.Render(k), keyW) + faintText.Render(d) }
	head := func(s string) string { return titleStyle.Render(s) }

	left := []string{
		head("Navigate"),
		row("↑/k ↓/j", "move selection"),
		row("g / G", "first / last"),
		row("h/← l/→", "back / enter"),
		row("tab", "switch panel"),
		row("enter", "focus terminal"),
		row("esc", "back / minimize"),
		row("/", "filter"),
		row("!", "who-needs-me"),
		row("V", "cycle lens"),
		row("v", "preview size"),
		"",
		head("Session actions"),
		row("s", "new worktree shell"),
		row("< / >", "prev / next terminal tab"),
		row("w", "close shell tab"),
		row("a", "answer input"),
		row("x", "kill session"),
		row("o", "open PR"),
		row("c", "coderabbit now"),
		row("R", "revive dead"),
		row("O", "open branch/PR"),
		row("n / N", "next / prev input"),
	}
	right := []string{
		head("Projects & polls"),
		row("p", "projects list"),
		row("P", "edit project"),
		row("n", "new project"),
		row("space", "toggle polling"),
		row("x", "stop polling"),
		row("r", "refresh cache"),
		"",
		head("Global"),
		row("S", "settings"),
		row("d", "doctor"),
		row("?", "this help"),
		row("^r / ^x", "restart / stop"),
		row("q / ^c", "quit"),
		"",
		head("Embedded terminal"),
		row("^q", "back to cockpit"),
		row("^g", "select-mode (copy)"),
	}
	// joinCols pads each column's missing rows to the width of its FIRST line
	// only, so hold every line to a fixed width first for clean alignment.
	const colW = 32
	pad := func(col []string) []string {
		out := make([]string, len(col))
		for i, l := range col {
			out[i] = padTo(l, colW)
		}
		return out
	}
	return m.modalOver("keybindings  ·  esc to close", joinCols(3, pad(left), pad(right)))
}

// formModal floats the poll edit form (or its open picker) as a centered modal
// over the dimmed cockpit. The form renders itself to the modal's inner height
// so its own picker scroll-window sizes correctly; its leading title line is
// lifted into the box title to avoid a doubled heading.
func (m *rootModel) formModal() string {
	W, H := m.width, m.height
	if W <= 0 {
		W = 100
	}
	if H <= 0 {
		H = 24
	}
	mw := W - 8
	if mw > 72 {
		mw = 72
	}
	if mw < 28 {
		mw = 28
	}
	mh := H - 4
	if mh > 26 {
		mh = 26
	}
	if mh < 8 {
		mh = 8
	}
	lines := strings.Split(strings.TrimRight(m.form.view(mh-2), "\n"), "\n")
	title := "poll"
	if len(lines) > 0 {
		title = stripANSI(lines[0])
	}
	body := lines
	if len(body) >= 2 {
		body = body[2:] // drop the form's own title + blank line; it becomes the box title
	}
	for i := range body {
		body[i] = previewLine(body[i], mw-4)
	}
	modal := box(title, body, mw, mh, true)
	return strings.Join(placeModal(m.backdropLines(), modal, W), "\n")
}

// railColumn stacks three panels: a fixed Triage summary, a Projects panel sized
// to the project list sitting directly under it, and a flexible Activity feed
// (the live "what's happening" ticker / history) anchored at the bottom that
// soaks up the rail's otherwise wasted space. The three heights always sum to
// exactly h so the column matches the frame.
func (m *rootModel) railColumn(w, h int) []string {
	// Triage: hero + 3 meters + total = 5 content lines → 7 with the box; +1
	// slack. Clamp so the other two panels always keep a usable slice.
	triageH := 8
	if triageH > h-8 {
		triageH = h - 8
	}
	if triageH < 5 {
		triageH = 5
	}
	rest := h - triageH
	// Projects: one row per configured project + title + 2 borders. Bounded so a
	// long project list never starves the Activity feed, and a short one never
	// leaves a yawning gap.
	railH := len(m.cfg.Projects) + 3
	if railH < 5 {
		railH = 5
	}
	if maxRail := rest - 5; railH > maxRail { // leave ≥5 for Activity
		railH = maxRail
	}
	if railH < 3 {
		railH = 3
	}
	activityH := rest - railH

	triage := box(paneTitle("Triage", ""), m.triageBody(w-4), w, triageH, false)
	activity := box("Activity", m.activityBody(w-4, activityH-2), w, activityH, false)
	projects := box(paneTitle("Projects", fmt.Sprintf("%d", len(m.cfg.Projects))), m.projectRailBody(w-4, railH-2), w, railH, m.focus == focusPolls)
	return stackRows(triage, projects, activity)
}

// mainColumn stacks the Sessions table over the Detail/Agent panel. When the
// embedded agent is FOCUSED it expands to fill the column (Sessions shrinks to a
// thin strip); otherwise the Detail panel takes its usual slice at the bottom.
func (m *rootModel) mainColumn(w, h int) []string {
	if m.embedFocused && m.currentEmbed() != nil {
		sessH := 4
		if sessH > h-8 {
			sessH = h - 8
		}
		if sessH < 3 {
			sessH = 3
		}
		embedH := h - sessH
		sess := box(m.sessionsTitle(), m.sessionsBody(w-4, sessH-2), w, sessH, false)
		embed := box(m.detailTitle(), m.detailBody(w-4, embedH-2), w, embedH, true) // focused accent
		return stackRows(sess, embed)
	}
	detailH := m.sessions.paneLines() + 8 // header + meta + card + pane + borders
	if maxD := h - 8; detailH > maxD {
		detailH = maxD
	}
	if detailH < 6 {
		detailH = 6
	}
	sessH := h - detailH
	sess := box(m.sessionsTitle(), m.sessionsBody(w-4, sessH-2), w, sessH, m.focus == focusSessions)
	det := box(m.detailTitle(), m.detailBody(w-4, detailH-2), w, detailH, false)
	return stackRows(sess, det)
}

// detailTitle names the panel and folds the selected session's key facts into
// the header (issue · project · #PR checks), plus the agent focus hint. It reads
// "Agent" when a live agent is embedded, "Detail" otherwise.
func (m *rootModel) detailTitle() string {
	sel := m.sessions.selected()
	if sel == nil {
		return paneTitle("Detail", "")
	}
	label := "Detail"
	if e := m.currentEmbed(); e != nil {
		if e.kind == termShell {
			label = "Shell"
		} else {
			label = "Agent"
		}
	}
	extra := sel.Issue
	if sel.Project != "" {
		extra += " · " + sel.Project
	}
	if sel.PRNumber > 0 {
		extra += fmt.Sprintf(" · #%d", sel.PRNumber)
		if sel.Checks != "" {
			extra += " " + sel.Checks
		}
	} else if sel.Status != "" {
		extra += " · " + sel.Status
	}
	if m.embedFocused {
		if m.embedSelect {
			extra += " · ✂ select — ⌘-click/drag · Ctrl-g wheel · Ctrl-q back"
		} else {
			extra += " · ⛶ focused — wheel→agent · Ctrl-g select · Ctrl-q back"
		}
	} else if m.currentEmbed() != nil {
		extra += " · enter to focus"
	}
	return paneTitle(label, extra)
}

// sessionsTitle names the panel plus the active lens and any standing filter
// state (attention-only). The count/triage numbers live in the vitals bar and
// Triage panel now, so the in-panel summary strip is gone — this title carries
// the lens context the strip used to.
func (m *rootModel) sessionsTitle() string {
	lens := "list"
	if m.sessions.view == viewKanban {
		lens = "kanban"
	}
	extra := lens
	if m.sessions.filter.AttentionOnly {
		extra += " · needs-you only"
	}
	return paneTitle("Sessions", extra)
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
	// The Board lens (V) swaps the table for kanban columns within the panel.
	if s.view == viewKanban {
		return m.kanbanBodyAt(w, h)
	}
	headers := []string{" ", "ISSUE", "PROJECT", "STATUS", "PR", "REACTING", "AGE"}
	list := s.listRows()
	selID := m.effectiveSelID()
	rows := make([][]string, len(list))
	selRow := -1
	anyTitle := false
	for i, si := range list {
		marker := " "
		issue := si.Issue
		if si.Status == "needs_input" {
			marker = warnText.Render("!")
		}
		if si.ID == selID {
			marker = boxTitleHi.Render("›")
			issue = boxTitleHi.Render(si.Issue) // cyan bold on the selected row
			selRow = i
		}
		pr := prBadge(si)
		if pr == "" {
			pr = "-"
		}
		if si.Title != "" {
			anyTitle = true
		}
		rows[i] = []string{
			marker, issue, si.Project,
			statusPill(si.Status), pr,
			reactingStyle(si.Reacting).Render(dash(si.Reacting)), dash(si.Age),
		}
	}
	colw := colWidths(headers, rows)

	// Adaptive TITLE column (after ISSUE): so a session is identifiable by what
	// it's ABOUT, not just its key. It claims whatever width is left after the
	// dense fixed columns and truncPlain ellipsizes the title to fit — a wide
	// terminal shows most of the Linear title, a modest one an ellipsized short
	// version. Only when the leftover is below titleColMin does the column drop
	// entirely (an identifier-only table), so the state columns (STATUS/PR/AGE)
	// are never clipped. Two-pass because the budget depends on the fixed
	// columns' measured widths.
	const titleColMin, titleColMax = 10, 72
	if anyTitle {
		baseW := 2 * (len(colw) - 1) // padCells joins columns with two spaces
		for _, cw := range colw {
			baseW += cw
		}
		if budget := w - baseW - 2; budget >= titleColMin {
			if budget > titleColMax {
				budget = titleColMax
			}
			headers = insAt(headers, 2, "TITLE")
			colw = insAt(colw, 2, budget)
			for i, si := range list {
				rows[i] = insAt(rows[i], 2, faintText.Render(truncPlain(si.Title, budget)))
			}
		}
	}
	out := []string{
		previewLine(tblHeader.Render(padCells(headers, colw)), w),
		faintText.Render(strings.Repeat("─", w)),
	}
	if len(rows) == 0 {
		empty := "no sessions observed"
		if len(s.data.Sessions) > 0 {
			empty = "no sessions match the filter"
		}
		return append(out, faintText.Render(empty))
	}
	bodyH := h - 2 // minus the header + rule rows
	if bodyH < 1 {
		bodyH = 1
	}
	start := viewportStart(selRow, len(rows), bodyH)
	end := start + bodyH
	if end > len(rows) {
		end = len(rows)
	}
	for i := start; i < end; i++ {
		line := padCells(rows[i], colw)
		if i == selRow {
			out = append(out, highlightRow(line, w, bgSGR(colSel)))
		} else {
			out = append(out, previewLine(line, w))
		}
	}
	return out
}

// embedTabBar renders the Detail terminal's tab row: the agent plus one tab per
// shell (active highlighted), and a trailing "+" for "s new shell". Empty when
// the selection has no shells AND the panel is not focused — nothing to switch,
// so no chrome (matches the desktop). A stale active index (its shell exited but
// hasn't been reaped yet) simply highlights that tab for one frame.
func (m *rootModel) embedTabBar(w int) string {
	sel := m.sessions.selected()
	if sel == nil {
		return ""
	}
	shells := m.shellNames[sel.ID]
	if len(shells) == 0 && !m.embedFocused {
		return ""
	}
	active := m.embedTab[sel.ID]
	tab := func(label string, idx int) string {
		if idx == active {
			return boxTitleHi.Render("[" + label + "]")
		}
		return faintText.Render(" " + label + " ")
	}
	parts := []string{tab("agent", 0)}
	for i := range shells {
		parts = append(parts, tab(fmt.Sprintf("sh%d", i+1), i+1))
	}
	parts = append(parts, faintText.Render("+"))
	return previewLine(strings.Join(parts, " "), w)
}

// detailBody renders the panel body: the live embedded AGENT/SHELL (a bottom
// viewport of its screen, under a tab row) when one is attached for the
// selection, otherwise the static detail card / capture preview.
func (m *rootModel) detailBody(w, h int) []string {
	if e := m.currentEmbed(); e != nil {
		if bar := m.embedTabBar(w); bar != "" && h > 1 {
			return append([]string{bar}, m.embedBody(e, w, h-1)...)
		}
		return m.embedBody(e, w, h)
	}
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

// projectRailBody lists every configured project with a poll-state dot and, for
// polling projects, the last-run age. The dot is shape-distinct (not colour
// only): ● green polling+enabled, ● red poll error, ○ paused poll, · not
// polling. The cursor marker shows only while the rail is focused.
func (m *rootModel) projectRailBody(w, h int) []string {
	l := &m.list
	if len(m.cfg.Projects) == 0 {
		return []string{faintText.Render("no projects — press p to add one")}
	}
	rows := make([]string, 0, len(m.cfg.Projects))
	for i, p := range m.cfg.Projects {
		polls := p.Polls()
		enabled, last, errd := p.Enabled, "", false
		if polls {
			last = "-"
			if ps := l.pollStatus(p.Name); ps != nil {
				enabled = ps.Enabled
				last = fmtAgo(ps.LastRun)
				if ps.Running {
					last = "run…"
				}
				errd = ps.LastError != ""
			}
		}
		var dot string
		switch {
		case !polls:
			dot = faintText.Render("·")
		case errd:
			dot = badText.Render("●")
		case enabled:
			dot = goodText.Render("●")
		default:
			dot = faintText.Render("○")
		}
		marker := " "
		if m.focus == focusPolls && i == l.cursor {
			marker = boxTitleHi.Render("›")
		}
		name := p.DisplayName()
		if polls && !enabled {
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
	// Nothing running: no bars (they'd read as a gray smear) — say so plainly.
	if total == 0 {
		return []string{faintText.Render("no active sessions")}
	}
	meterW := w - 13
	if meterW < 4 {
		meterW = 4
	}
	// Uniform meter rows (matches the desktop Triage): "need you" is the SAME bar
	// format as the rest, just emphasised — an orange, coloured label — not a
	// special hero line with its own indentation.
	out := []string{
		triageMeter("need you", need, total, meterW, statusOrange, true),
		triageMeter("working", work, total, meterW, statusBlue, false),
		triageMeter("ready", ready, total, meterW, goodText, false),
		triageMeter("fixing", fix, total, meterW, badText, false),
	}
	withPR := 0
	for _, x := range sess {
		if x.PRNumber > 0 {
			withPR++
		}
	}
	totalLine := fmt.Sprintf("%d total", total)
	if withPR > 0 {
		totalLine += fmt.Sprintf(" · %d with PR", withPR)
	}
	out = append(out, faintText.Render(totalLine))
	return out
}

// triageMeter renders "N label ━━━━" — a colored+bold count, a label, then a thin
// proportional bar: the filled portion in the category color, the rest a very
// dark track rule (so an empty bar is a subtle line, never a gray block). strong
// tints the LABEL in the category colour (used for "need you"); otherwise the
// label is muted and uniform, like the desktop's Meter.
func triageMeter(label string, n, total, w int, style lipgloss.Style, strong bool) string {
	filled := 0
	if total > 0 {
		filled = n * w / total
	}
	if filled > w {
		filled = w
	}
	if n > 0 && filled == 0 {
		filled = 1
	}
	labelStyle := faintText
	if strong {
		labelStyle = style.Bold(true)
	}
	head := style.Bold(true).Render(fmt.Sprintf("%2d", n)) + labelStyle.Render(fmt.Sprintf(" %-8s", label))
	bar := style.Render(strings.Repeat("━", filled)) + meterTrack.Render(strings.Repeat("━", w-filled))
	return head + " " + bar
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
	nSess, need := 0, 0
	if m.sessions.data != nil {
		nSess = len(m.sessions.data.Sessions)
		need = AttentionCount(m.sessions.data.Sessions)
	}
	needStr := fmt.Sprintf("%d need you", need)
	if need > 0 {
		needStr = statusOrange.Render(needStr)
	}
	polling := m.cfg.PollingProjects()
	en := 0
	for _, p := range polling {
		enabled := p.Enabled
		if ps := m.list.pollStatus(p.Name); ps != nil {
			enabled = ps.Enabled
		}
		if enabled {
			en++
		}
	}
	parts = append(parts, needStr, fmt.Sprintf("sessions %d", nSess), fmt.Sprintf("projects %d", len(m.cfg.Projects)))
	if len(polling) > 0 {
		parts = append(parts, fmt.Sprintf("polls %d/%d", en, len(polling)))
	}

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
		label := s.killTarget
		if s.data != nil {
			for i := range s.data.Sessions {
				if info := s.data.Sessions[i]; info.ID == s.killTarget && info.Issue != "" {
					label = info.Issue
					break
				}
			}
		}
		return warnText.Render(fmt.Sprintf("kill %s? removes worktree, stops agent (y/n)", label))
	case m.list.confirmDelete:
		name := ""
		if p := m.selectedRailProject(); p != nil {
			name = p.DisplayName()
		}
		return warnText.Render(fmt.Sprintf("stop polling %q? (y/n)", name))
	case s.flash != "":
		if s.flashGood {
			return goodText.Render(s.flash)
		}
		return warnText.Render(s.flash)
	case m.list.flash != "":
		return faintText.Render(m.list.flash)
	case m.daemonOp != "":
		return warnText.Render("daemon: " + m.daemonOp + "…")
	case s.daemonDown:
		return badText.Render("daemon: not running") + faintText.Render(m.daemonDownHint())
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
	case s.opening:
		proj := s.openProject
		if proj == "" {
			proj = "?"
		}
		return previewLine(warnText.Render("open ")+faintText.Render("in "+proj+": ")+s.openInput+"_"+
			faintText.Render("  · enter open · esc cancel · \"<project> <branch|PR#>\" to pick project"), w)
	case s.answering:
		return previewLine(faintText.Render("enter send · esc cancel"), w)
	case s.confirmKill:
		return previewLine(warnText.Render("y")+faintText.Render(" kill · ")+warnText.Render("n")+faintText.Render(" cancel"), w)
	case m.list.confirmDelete:
		return previewLine(warnText.Render("y")+faintText.Render(" stop polling · ")+warnText.Render("n")+faintText.Render(" cancel"), w)
	}
	// Trimmed to the essentials: the full reference lives in the '?' overlay
	// (helpModal), so the keybar prints only the everyday keys plus a couple of
	// context-sensitive ones (answer when a session is asking, a live-shell ●
	// re-enter hint). Everything rarer — o/c/R/O/!/n/N/P/d, restart/stop — is one
	// '?' away.
	var keys []string
	if m.focus == focusPolls {
		keys = []string{"↑↓ move", "enter open", "space toggle", "n new", "tab → sessions"}
	} else {
		keys = []string{"↑↓ move", "enter focus", "tab → projects", "/ filter", "x kill"}
		if sel := s.selected(); sel != nil {
			if sel.Status == "needs_input" {
				keys = append(keys, "a answer")
			}
			if sel.Worktree != "" {
				if len(m.shellNames[sel.ID]) > 0 {
					keys = append(keys, "s +shell "+goodText.Render("●")+" · < > tabs") // live shells to switch
				} else {
					keys = append(keys, "s shell")
				}
			}
		}
		keys = append(keys, "V lens")
	}
	keys = append(keys, "p projects", "S settings")
	if m.manageDaemon() && m.list.status == nil {
		keys = append(keys, "^r start daemon") // urgent while the daemon is down
	}
	keys = append(keys, "? help", "q quit")
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
		// Columns fill the panel evenly (no upper cap) so the board reads as a
		// balanced grid instead of packing left with a wide empty gutter.
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

// insAt returns s with v spliced in at index i (0 <= i <= len(s)), leaving the
// input slice untouched — used to widen the sessions table with an optional
// TITLE column without mutating the base row slices.
func insAt[T any](s []T, i int, v T) []T {
	out := make([]T, 0, len(s)+1)
	out = append(out, s[:i]...)
	out = append(out, v)
	return append(out, s[i:]...)
}

// eventPhrase maps a transition's target status to the short human phrase the
// activity feed shows (a spawn — from "" — reads "spawned"; a resume out of
// needs_input reads "resumed"). Kept terse so an "ISSUE phrase age" line fits
// the narrow rail; an unmapped status falls back to its raw word.
func eventPhrase(from, to string) string {
	if from == "" {
		return "spawned"
	}
	switch to {
	case "working":
		return "resumed"
	case "needs_input":
		return "needs you"
	case "draft":
		return "PR opened"
	case "review_pending":
		return "in review"
	case "ci_pending":
		return "CI running"
	case "ci_failed":
		return "CI failed"
	case "changes_requested":
		return "changes req"
	case "merge_conflict":
		return "conflict"
	case "approved":
		return "approved"
	case "merged":
		return "merged"
	case "closed":
		return "PR closed"
	case "session_ended":
		return "ended"
	case "dead":
		return "died"
	default:
		return to
	}
}

// activityBody renders the daemon's activity feed (newest first) as one
// "ISSUE phrase age" line per event, the phrase colored by the target status so
// a needs-you reads orange, a failure red, a merge green. The issue key leads;
// the age trails faint. Lines are clipped to width; box() clips the stack to the
// panel height (so the freshest events always win the visible rows). An empty
// feed says so plainly.
func (m *rootModel) activityBody(w, h int) []string {
	var evs []protocol.Event
	if m.sessions.data != nil {
		evs = m.sessions.data.Events
	}
	if len(evs) == 0 {
		return []string{faintText.Render("no activity yet")}
	}
	if h > 0 && len(evs) > h {
		evs = evs[:h]
	}
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		key := e.Issue
		if key == "" {
			key = shortID(e.ID)
		}
		phrase := statusStyle(e.To).Render(eventPhrase(e.From, e.To))
		line := boxTitle.Render(key) + " " + phrase
		if e.Ago != "" {
			line += faintText.Render(" · " + e.Ago)
		}
		out = append(out, truncateANSI(line, w))
	}
	return out
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
