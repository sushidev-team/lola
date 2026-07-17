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

// railColumn stacks three panels: a fixed Triage summary, a flexible Activity
// feed (the live "what's happening" ticker) that soaks up the rail's otherwise
// wasted middle, and a Polls panel sized to the poll list. The three heights
// always sum to exactly h so the column matches the frame.
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
	// Polls: one row per configured poll + title + 2 borders. Bounded so a long
	// poll list never starves the Activity feed, and a short one never leaves a
	// yawning gap.
	pollsH := len(m.cfg.PollingProjects()) + 3
	if pollsH < 5 {
		pollsH = 5
	}
	if maxPolls := rest - 5; pollsH > maxPolls { // leave ≥5 for Activity
		pollsH = maxPolls
	}
	if pollsH < 3 {
		pollsH = 3
	}
	activityH := rest - pollsH

	triage := box(paneTitle("Triage", ""), m.triageBody(w-4), w, triageH, false)
	activity := box("Activity", m.activityBody(w-4, activityH-2), w, activityH, false)
	polls := box(paneTitle("Polls", ""), m.pollsBody(w-4, pollsH-2), w, pollsH, m.focus == focusPolls)
	return stackRows(triage, activity, polls)
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

// detailBody renders the panel body: the live embedded AGENT (a bottom viewport
// of its screen) when one is attached for the selection, otherwise the static
// detail card / capture preview.
func (m *rootModel) detailBody(w, h int) []string {
	if e := m.currentEmbed(); e != nil {
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

// pollsBody lists the configured polls with a health dot (green ok / red error /
// hollow paused) and last-run age; the cursor marker shows only while the Polls
// panel is focused.
func (m *rootModel) pollsBody(w, h int) []string {
	l := &m.list
	polls := m.cfg.PollingProjects()
	if len(polls) == 0 {
		return []string{faintText.Render("no polling projects — configure one from a project (P)")}
	}
	rows := make([]string, 0, len(polls))
	for i, p := range polls {
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
	// Hero: a bold count + "NEED YOU" (orange), or an all-clear note.
	if need > 0 {
		out = append(out, statusOrange.Bold(true).Render(fmt.Sprintf("%d", need))+"  "+statusOrange.Render("NEED YOU"))
	} else {
		out = append(out, goodText.Bold(true).Render("0")+"  "+faintText.Render("all clear"))
	}
	// Nothing running: no bars (they'd read as a gray smear) — say so plainly.
	if total == 0 {
		out = append(out, "", faintText.Render("no active sessions"))
		return out
	}
	meterW := w - 12
	if meterW < 4 {
		meterW = 4
	}
	out = append(out, triageMeter("working", work, total, meterW, statusBlue))
	out = append(out, triageMeter("ready", ready, total, meterW, goodText))
	out = append(out, triageMeter("fixing", fix, total, meterW, badText))
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

// triageMeter renders "N label ━━━━" — a colored+bold count, a muted label, then
// a thin proportional bar: the filled portion in the category color, the rest a
// very dark track rule (so an empty bar is a subtle line, never a gray block).
func triageMeter(label string, n, total, w int, style lipgloss.Style) string {
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
	head := style.Bold(true).Render(fmt.Sprintf("%2d", n)) + faintText.Render(fmt.Sprintf(" %-7s", label))
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
	parts = append(parts, needStr, fmt.Sprintf("sessions %d", nSess), fmt.Sprintf("polls %d/%d", en, len(polling)))

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
		return previewLine(warnText.Render("y")+faintText.Render(" delete poll · ")+warnText.Render("n")+faintText.Render(" cancel"), w)
	}
	var keys []string
	if m.focus == focusPolls {
		keys = []string{"↑↓ move", "n new", "enter edit", "space toggle", "x delete", "r cache", "tab → sessions"}
	} else {
		keys = []string{"↑↓ move", "enter focus"}
		if sel := s.selected(); sel != nil {
			if m.showShell {
				keys = append(keys, "s agent") // toggle back to the agent view
			} else if sel.Worktree != "" {
				shell := "s shell"
				if m.runningShell(sel.ID) {
					shell += " " + goodText.Render("●") // a live shell to re-enter
				}
				keys = append(keys, shell)
			}
			if sel.Status == "needs_input" {
				keys = append(keys, "a answer")
			}
			if sel.Status == "dead" || sel.Status == "session_ended" {
				keys = append(keys, "R revive")
			}
			if sel.PRURL != "" {
				keys = append(keys, "o PR", "c coderabbit")
			}
		}
		keys = append(keys, "x kill", "O open", "/ filter", "! needs-you", "V lens", "n next!", "tab → polls")
	}
	keys = append(keys, "p projects", "P edit", "S settings", "d doctor")
	if m.manageDaemon() {
		if m.list.status == nil {
			keys = append(keys, "^r start daemon")
		} else {
			keys = append(keys, "^r restart", "^x stop")
		}
	}
	keys = append(keys, "q quit")
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
