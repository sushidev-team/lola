// Sessions tab (PLAN P1.8): read-only observability over agent sessions.
// Data comes from the daemon's cmd=sessions snapshot (never exec'd on the
// request path); the preview pane polls tmux capture-pane best-effort.
package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/tmux"
)

// previewLines is how many pane rows the COMPACT preview shows (and requests as
// scrollback via cmd=pane); fullPreviewLines is the "fuller" toggle. Both are
// bounded well under the daemon's own cap so a capture never floods the frame.
const (
	previewLines     = 8
	fullPreviewLines = 20
)

var (
	statusBlue   = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	statusOrange = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))
	statusDeadBg = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("9"))
	srcNative    = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
)

// statusStyle maps a derived session status (scm.DeriveStatus / AO attention
// states / native runtime states) to its display style: working=blue,
// failures=red, approved=green, attention=orange, dead=red background,
// merged/session_ended/idle=dim; anything else unstyled.
func statusStyle(status string) lipgloss.Style {
	switch status {
	case "working":
		return statusBlue
	case "ci_failed", "changes_requested", "merge_conflict":
		return badText
	case "approved":
		return goodText
	case "needs_input", "no_signal":
		return statusOrange
	case "dead":
		return statusDeadBg
	case "merged", "session_ended", "idle":
		return faintText
	}
	return lipgloss.NewStyle()
}

// reactingStyle colors the reaction-posture label (protocol.SessionInfo.Reacting):
// "escalated" (needs a human) red, "ready to merge" green, an active retry or
// rework ("ci retry N/M", "addressing review", "rebasing") yellow. Everything
// else — "awaiting review" and the empty label — is unstyled so the urgent and
// done states stand out.
func reactingStyle(label string) lipgloss.Style {
	switch {
	case label == "escalated":
		return badText
	case label == "ready to merge":
		return goodText
	case strings.HasPrefix(label, "ci retry"), label == "addressing review", label == "rebasing":
		return warnText
	}
	return lipgloss.NewStyle()
}

// sourceBadge renders which backend spawned a session: native sessions are
// lola's own runners (P2); everything else — including pre-P2 records with an
// empty source — came through the AO bridge.
func sourceBadge(source string) string {
	if source == "native" {
		return srcNative.Render("[native]")
	}
	return faintText.Render("[ao]")
}

type sessionsModel struct {
	cursor      int
	data        *protocol.SessionsData // nil until the first successful fetch
	daemonDown  bool                   // last fetch failed to dial the socket
	dataErr     string                 // non-dial fetch failure, if any
	flash       string
	flashGood   bool   // flash is a success (green) rather than a warning (yellow)
	confirmKill bool   // "x" pressed: awaiting y/n to kill killTarget
	killTarget  string // session ID captured when "x" was pressed (pinned across refreshes)
	preview     string // rendered pane text (cmd=pane) for the selected session
	previewFor  string // session ID the preview + paneData belong to ("" = none)
	// paneData is the daemon's read of the selected session's pane: the rendered
	// text plus the attention parser's extracted question. It backs both the
	// compact preview and — when the session is needs_input — the answer card.
	// Only trustworthy when previewFor == selected().ID.
	paneData *protocol.PaneData
	full     bool // pane view mode: false = compact (previewLines), true = full (fullPreviewLines)

	// Inline-answer state (P7). answering is entered with "a" on a needs_input
	// session that has a parsed question; it owns every keypress until enter
	// (send) or esc (cancel). answerFor pins the target session ID; answerChoice
	// is the pick-list cursor for a choice prompt; answerInput accumulates a
	// free-form reply.
	answering    bool
	answerFor    string
	answerChoice int
	answerInput  string

	tmux *tmux.Client
}

// paneLines is how many trailing pane rows the current view mode shows and
// requests via cmd=pane — the compact/full toggle drives it.
func (s *sessionsModel) paneLines() int {
	if s.full {
		return fullPreviewLines
	}
	return previewLines
}

func (s *sessionsModel) tmuxClient() *tmux.Client {
	if s.tmux == nil {
		s.tmux = &tmux.Client{}
	}
	return s.tmux
}

func (s *sessionsModel) selected() *protocol.SessionInfo {
	if s.data == nil || s.cursor < 0 || s.cursor >= len(s.data.Sessions) {
		return nil
	}
	return &s.data.Sessions[s.cursor]
}

// ---- messages / commands ----

type sessionsMsg struct {
	data *protocol.SessionsData
	err  error
}

// paneMsg carries a cmd=pane result (the rendered pane text plus the daemon's
// parsed question) for a specific session; data is nil on error.
type paneMsg struct {
	id   string // session ID the capture was taken for
	data *protocol.PaneData
	err  error
}

// answerDoneMsg carries a cmd=answer outcome: ok on a delivered answer, else
// msg is the daemon's verbatim refusal (e.g. the agent moved on) or a dial
// error.
type answerDoneMsg struct {
	ok  bool
	msg string
}

type attachDoneMsg struct{ err error }

// killDoneMsg carries the outcome of a `cmd=kill` request. msg is the message
// to flash (a success line, or the daemon's verbatim dirty-kept error).
type killDoneMsg struct{ msg string }

// killSelectedCmd sends a (non-force) kill for id and reports the outcome to
// flash. Force is deliberately never offered here: removing a dirty worktree is
// CLI-only friction (`lola kill <id> --force`).
func killSelectedCmd(id string) tea.Cmd {
	return func() tea.Msg {
		resp, err := request(protocol.Request{Cmd: "kill", Session: id})
		if err != nil {
			return killDoneMsg{msg: err.Error()}
		}
		if !resp.OK {
			// Dirty worktree (or any refusal): surface the daemon message
			// verbatim so the user learns to rerun with `lola kill <id> --force`.
			return killDoneMsg{msg: resp.Error}
		}
		var d protocol.KillData
		if err := json.Unmarshal(resp.Data, &d); err == nil && d.Message != "" {
			return killDoneMsg{msg: d.Message}
		}
		return killDoneMsg{msg: "session killed"}
	}
}

func fetchSessionsCmd() tea.Msg {
	resp, err := request(protocol.Request{Cmd: "sessions"})
	if err != nil {
		return sessionsMsg{err: err}
	}
	if !resp.OK {
		return sessionsMsg{err: errors.New(resp.Error)}
	}
	var d protocol.SessionsData
	if err := json.Unmarshal(resp.Data, &d); err != nil {
		return sessionsMsg{err: err}
	}
	return sessionsMsg{data: &d}
}

// paneCmd asks the daemon (cmd=pane) for the last `lines` rendered rows of a
// session's pane plus the attention parser's read of it. The daemon owns the
// tmux exec (bounded); the TUI never captures directly, so this stays hermetic
// behind requestFn in tests.
func paneCmd(id string, lines int) tea.Cmd {
	return func() tea.Msg {
		resp, err := requestFn(protocol.Request{Cmd: "pane", Session: id, Lines: lines})
		if err != nil {
			return paneMsg{id: id, err: err}
		}
		if !resp.OK {
			return paneMsg{id: id, err: errors.New(resp.Error)}
		}
		var d protocol.PaneData
		if err := json.Unmarshal(resp.Data, &d); err != nil {
			return paneMsg{id: id, err: err}
		}
		return paneMsg{id: id, data: &d}
	}
}

// answerCmd delivers a HUMAN's inline reply (cmd=answer). The daemon refuses
// unless the session is still needs_input, so a non-OK response is surfaced
// verbatim (the agent moved on between render and send).
func answerCmd(id, text string) tea.Cmd {
	return func() tea.Msg {
		resp, err := requestFn(protocol.Request{Cmd: "answer", Session: id, Text: text})
		if err != nil {
			return answerDoneMsg{msg: err.Error()}
		}
		if !resp.OK {
			return answerDoneMsg{msg: resp.Error}
		}
		return answerDoneMsg{ok: true, msg: "answer sent"}
	}
}

// paneRefreshCmd returns a cmd=pane refresh for the selected session, or nil
// (clearing any stale preview/paneData) when there is nothing to capture — an
// AO desktop session has no tmux pane to read.
func (m *rootModel) paneRefreshCmd() tea.Cmd {
	sel := m.sessions.selected()
	if sel == nil || sel.TmuxName == "" {
		m.sessions.preview, m.sessions.previewFor, m.sessions.paneData = "", "", nil
		return nil
	}
	return paneCmd(sel.ID, m.sessions.paneLines())
}

// ---- update ----

// handleSessionsMsg absorbs a fetch result at the root level (ticks keep
// arriving regardless of the active tab).
func (m *rootModel) handleSessionsMsg(v sessionsMsg) tea.Cmd {
	s := &m.sessions
	if v.err != nil {
		s.data = nil
		s.daemonDown = errors.Is(v.err, errDaemonDown)
		s.dataErr = ""
		if !s.daemonDown {
			s.dataErr = v.err.Error()
		}
		s.preview, s.previewFor, s.paneData = "", "", nil
		return nil
	}
	s.data, s.daemonDown, s.dataErr = v.data, false, ""
	if s.cursor >= len(s.data.Sessions) {
		s.cursor = len(s.data.Sessions) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	return m.paneRefreshCmd()
}

func (m *rootModel) handlePaneMsg(v paneMsg) {
	s := &m.sessions
	sel := s.selected()
	if sel == nil || sel.ID != v.id {
		return // stale capture for a session no longer selected
	}
	if v.err != nil || v.data == nil {
		s.preview, s.previewFor, s.paneData = "", sel.ID, nil // renders as "(no preview)"
		return
	}
	s.preview, s.previewFor, s.paneData = v.data.Text, sel.ID, v.data
}

func (m *rootModel) updateSessions(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	s := &m.sessions
	// An open answer card owns every keypress until the human sends (enter) or
	// cancels (esc) — see updateAnswer. This is the ONE place typing into a live
	// agent is allowed, and only because a needs_input session is provably parked
	// at its own prompt.
	if s.answering {
		return m.updateAnswer(k)
	}
	// A pending kill confirmation owns the next keypress: y/Y kills, anything
	// else cancels. Force is never offered here (CLI-only friction). The target
	// is the ID captured when "x" was pressed — NOT s.selected() re-read now: a
	// background refresh (the 5s tick) can reorder/prune the list and shift the
	// cursor onto a different session between "x" and "y", which would otherwise
	// force-kill the wrong session's worktree/branch.
	if s.confirmKill {
		s.confirmKill = false
		target := s.killTarget
		s.killTarget = ""
		if target != "" && (k.String() == "y" || k.String() == "Y") {
			return m, killSelectedCmd(target)
		}
		return m, nil
	}
	s.flash, s.flashGood = "", false
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
			return m, m.paneRefreshCmd()
		}
	case "down", "j":
		if s.data != nil && s.cursor < len(s.data.Sessions)-1 {
			s.cursor++
			return m, m.paneRefreshCmd()
		}
	case "enter":
		return m.attachSelected()
	case "o":
		return m, m.openSelectedPR()
	case "x":
		if sel := s.selected(); sel != nil {
			s.confirmKill = true
			s.killTarget = sel.ID
		}
	case "v":
		// Toggle compact/full pane view; refetch so the fuller view fills in.
		s.full = !s.full
		return m, m.paneRefreshCmd()
	case "a":
		return m.startAnswer()
	case "n":
		return m.jumpNextNeedsInput()
	}
	return m, nil
}

// startAnswer opens the inline answer card for the selected session. It is only
// meaningful on a needs_input session whose pane we have already parsed into a
// question; anything else flashes a hint and stays put (never sends).
func (m *rootModel) startAnswer() (tea.Model, tea.Cmd) {
	s := &m.sessions
	sel := s.selected()
	if sel == nil || sel.Status != "needs_input" {
		s.flash, s.flashGood = "answer is only available while a session waits for input", false
		return m, nil
	}
	// We need a fresh pane read to render the card, but NOT a recognized question:
	// the parser is deliberately fallible (a scrolled-away prompt, an unenumerated
	// format), yet the session is genuinely needs_input and the daemon accepts a
	// free-form answer for it. When there is no parsed question (or only a
	// choice-less/non-free-form parse), fall through to a free-form card so a human
	// can always answer in place rather than being forced to attach.
	if s.previewFor != sel.ID || s.paneData == nil {
		s.flash, s.flashGood = "no pane captured yet", false
		return m, nil
	}
	s.answering = true
	s.answerFor = sel.ID
	s.answerChoice = 0
	s.answerInput = ""
	return m, nil
}

// updateAnswer drives the open answer card. A choice prompt is a pick-list
// (arrows/j/k move, enter sends the highlighted Key, a matching digit/letter
// sends that Key directly); a free-form prompt accumulates typed runes and
// sends on enter. esc cancels without sending. The daemon still re-checks
// needs_input, so a stale send is refused there, not here.
func (m *rootModel) updateAnswer(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := &m.sessions
	pd := s.paneData
	if pd == nil { // defensive: nothing to answer against
		s.answering = false
		return m, nil
	}
	if k.String() == "esc" {
		s.answering = false
		return m, nil
	}
	if len(pd.Choices) > 0 {
		switch k.String() {
		case "up", "k":
			if s.answerChoice > 0 {
				s.answerChoice--
			}
			return m, nil
		case "down", "j":
			if s.answerChoice < len(pd.Choices)-1 {
				s.answerChoice++
			}
			return m, nil
		case "enter":
			return m.sendAnswer(pd.Choices[s.answerChoice].Key)
		}
		// A keypress that directly names a choice key ("1", "y", …) sends it.
		for _, c := range pd.Choices {
			if k.String() == c.Key {
				return m.sendAnswer(c.Key)
			}
		}
		return m, nil
	}
	// Free-form entry.
	switch k.String() {
	case "enter":
		return m.sendAnswer(s.answerInput)
	case "backspace":
		if r := []rune(s.answerInput); len(r) > 0 {
			s.answerInput = string(r[:len(r)-1])
		}
		return m, nil
	}
	switch {
	case k.Type == tea.KeyRunes:
		s.answerInput += string(k.Runes)
	case k.String() == " ":
		s.answerInput += " "
	}
	return m, nil
}

// sendAnswer closes the card and dispatches the reply for the pinned target.
func (m *rootModel) sendAnswer(text string) (tea.Model, tea.Cmd) {
	s := &m.sessions
	id := s.answerFor
	s.answering = false
	s.answerInput = ""
	return m, answerCmd(id, text)
}

// jumpNextNeedsInput moves the cursor to the next session (wrapping) whose
// status is needs_input, so "who needs me" is one keypress away. Refetches the
// pane for the new selection.
func (m *rootModel) jumpNextNeedsInput() (tea.Model, tea.Cmd) {
	s := &m.sessions
	if s.data == nil || len(s.data.Sessions) == 0 {
		return m, nil
	}
	n := len(s.data.Sessions)
	for off := 1; off <= n; off++ {
		i := (s.cursor + off) % n
		if s.data.Sessions[i].Status == "needs_input" {
			if i == s.cursor {
				return m, nil // already on the only one
			}
			s.cursor = i
			return m, m.paneRefreshCmd()
		}
	}
	s.flash, s.flashGood = "no session is waiting for input", false
	return m, nil
}

// attachSelected suspends the TUI and execs `tmux attach-session` for the
// selected session; sessions without a tmux target (AO desktop runtime)
// cannot be attached.
func (m *rootModel) attachSelected() (tea.Model, tea.Cmd) {
	sel := m.sessions.selected()
	if sel == nil {
		return m, nil
	}
	if sel.TmuxName == "" {
		m.sessions.flash = "no tmux session (AO desktop runtime)"
		return m, nil
	}
	argv := m.sessions.tmuxClient().AttachArgs(sel.TmuxName)
	c := exec.Command(argv[0], argv[1:]...)
	return m, tea.ExecProcess(c, func(err error) tea.Msg { return attachDoneMsg{err: err} })
}

// openSelectedPR opens the selected session's PR in the default browser,
// best-effort (macOS /usr/bin/open; failures only flash).
func (m *rootModel) openSelectedPR() tea.Cmd {
	sel := m.sessions.selected()
	if sel == nil || sel.PRURL == "" {
		m.sessions.flash = "no PR for this session"
		return nil
	}
	url := sel.PRURL
	return func() tea.Msg {
		_ = exec.Command("/usr/bin/open", url).Start()
		return nil
	}
}

// ---- view ----

func (m *rootModel) sessionsView() string {
	s := &m.sessions
	var b strings.Builder
	b.WriteString(m.tabBar() + "\n")

	switch {
	case s.daemonDown:
		b.WriteString(badText.Render("daemon: not running") +
			faintText.Render("  (start with: lola run — sessions need the daemon)") + "\n\n")
	case s.dataErr != "":
		b.WriteString(badText.Render("sessions: "+s.dataErr) + "\n\n")
	case s.data == nil:
		b.WriteString(faintText.Render("fetching sessions…") + "\n\n")
	default:
		b.WriteString("\n")
	}

	if s.data != nil {
		b.WriteString(m.sessionsTable())
		b.WriteString("\n" + m.sessionDetail())
	}

	b.WriteString("\n")
	switch {
	case s.confirmKill:
		b.WriteString(warnText.Render(fmt.Sprintf("kill session %q? (y/n)", s.killTarget)) + "\n")
	case s.flash != "":
		style := warnText
		if s.flashGood {
			style = goodText
		}
		b.WriteString(style.Render(s.flash) + "\n")
	}
	if s.answering {
		b.WriteString(faintText.Render("enter send · esc cancel") + "\n")
	} else {
		b.WriteString(faintText.Render("↑/↓ move · enter attach · a answer · o PR · x kill · v view · n next! · tab switch · q quit") + "\n")
	}
	return b.String()
}

// sessionsTable renders the session list. Selection is marked with "›" only
// (no reverse video): the STATUS cell carries its own ANSI colors, and
// nesting them inside another style's escape sequences breaks both.
func (m *rootModel) sessionsTable() string {
	s := &m.sessions
	// REACTING replaces REVIEW here: the reaction posture already encodes the
	// actionable review state in human form ("awaiting review", "ready to
	// merge", …) and keeps the table one wide column instead of two. The raw
	// review decision still shows in the detail card below.
	headers := []string{" ", "ISSUE", "PROJECT", "STATUS", "PR", "CHECKS", "REACTING", "AGE"}
	rows := make([][]string, len(s.data.Sessions))
	for i, si := range s.data.Sessions {
		// The marker column flags "who needs me": a selected row shows the
		// cursor "›"; an unselected needs_input row shows a warn "!" (a selected
		// needs_input row is already obvious from the orange STATUS cell).
		marker := " "
		if si.Status == "needs_input" {
			marker = warnText.Render("!")
		}
		if i == s.cursor {
			marker = "›"
		}
		pr := "-"
		if si.PRNumber > 0 {
			pr = fmt.Sprintf("#%d", si.PRNumber)
		}
		rows[i] = []string{
			marker, si.Issue, si.Project,
			statusStyle(si.Status).Render(si.Status),
			pr, dash(si.Checks),
			reactingStyle(si.Reacting).Render(dash(si.Reacting)),
			dash(si.Age),
		}
	}

	var b strings.Builder
	w := colWidths(headers, rows)
	b.WriteString(tblHeader.Render(padCells(headers, w)) + "\n")
	if len(rows) == 0 {
		b.WriteString(faintText.Render("no sessions observed") + "\n")
	}
	for _, r := range rows {
		b.WriteString(padCells(r, w) + "\n")
	}
	return b.String()
}

// sessionDetail is the bottom pane: live capture-pane preview for tmux-backed
// sessions, a static detail card otherwise. Both variants lead with a source
// badge (ao|native); native sessions additionally show their worktree dir.
func (m *rootModel) sessionDetail() string {
	s := &m.sessions
	sel := s.selected()
	if sel == nil {
		return ""
	}
	var b strings.Builder
	if sel.TmuxName != "" {
		fresh := s.previewFor == sel.ID
		needsInput := sel.Status == "needs_input"
		header := tblHeader.Render("preview")
		if needsInput {
			header = statusOrange.Render("attention")
		}
		mode := "compact"
		if s.full {
			mode = "full"
		}
		b.WriteString(header + " " + sourceBadge(sel.Source) +
			faintText.Render(" — tmux "+sel.TmuxName+" · "+mode) + "\n")
		if sel.Worktree != "" {
			b.WriteString(faintText.Render("worktree: "+sel.Worktree) + "\n")
		}
		if sel.Review != "" || sel.Reacting != "" {
			// The table dropped the REVIEW column for REACTING; keep the raw
			// review decision reachable here for the selected session.
			b.WriteString(faintText.Render("review: "+dash(sel.Review)) +
				"   " + reactingStyle(sel.Reacting).Render(dash(sel.Reacting)) + "\n")
		}
		// Actionable answer card: only when the session is provably parked at its
		// prompt AND we have a fresh pane read. A recognized question renders its
		// choices/free-form field; when the parser missed the prompt we still open
		// a free-form card once the human arms it (s.answering), so a parse miss
		// never blocks answering in place.
		if needsInput && fresh && s.paneData != nil &&
			(s.paneData.HasQuestion || (s.answering && s.answerFor == sel.ID)) {
			b.WriteString(m.attentionCard())
		}
		if fresh && s.preview != "" {
			for _, ln := range lastLines(s.preview, s.paneLines()) {
				b.WriteString(previewLine(ln, m.width) + "\n")
			}
		} else {
			b.WriteString(faintText.Render("(no preview)") + "\n")
		}
		return b.String()
	}
	b.WriteString(tblHeader.Render("detail") + " " + sourceBadge(sel.Source) +
		faintText.Render(" — no tmux session (AO desktop runtime)") + "\n")
	fmt.Fprintf(&b, "issue:    %s\n", dash(sel.Issue))
	fmt.Fprintf(&b, "branch:   %s\n", dash(sel.Branch))
	fmt.Fprintf(&b, "worktree: %s\n", dash(sel.Worktree))
	fmt.Fprintf(&b, "pr:       %s\n", dash(sel.PRURL))
	fmt.Fprintf(&b, "status:   %s\n", statusStyle(sel.Status).Render(sel.Status))
	fmt.Fprintf(&b, "review:   %s\n", dash(sel.Review))
	if sel.Reacting != "" {
		fmt.Fprintf(&b, "reacting: %s\n", reactingStyle(sel.Reacting).Render(sel.Reacting))
	}
	fmt.Fprintf(&b, "age:      %s\n", dash(sel.Age))
	return b.String()
}

// attentionCard renders the actionable prompt for the selected needs_input
// session from paneData: the question line, then either the choices as a
// pick-list or a free-form text field, plus the affordance. Callers guarantee
// paneData is non-nil, fresh, and HasQuestion. It never sends — "a" arms it,
// enter (in updateAnswer) sends.
func (m *rootModel) attentionCard() string {
	s := &m.sessions
	pd := s.paneData
	var b strings.Builder
	// clamp keeps every card line within the terminal width so an over-wide
	// choice label, prompt, or growing free-form input can never physically wrap
	// — a wrapped row makes bubbletea (alt-screen, line-count repaint) miscount
	// rendered lines and smear the frame, the same hazard previewLine guards for
	// the pane rows below. A truncated line loses its style's trailing reset, so
	// re-append one (mirrors previewLine).
	clamp := func(line string) string {
		if m.width > 0 && lipgloss.Width(line) > m.width {
			return truncateANSI(line, m.width) + "\x1b[0m"
		}
		return line
	}
	if pd.Prompt != "" {
		b.WriteString(clamp(statusOrange.Render("? "+pd.Prompt)) + "\n")
	}
	switch {
	case len(pd.Choices) > 0:
		for i, c := range pd.Choices {
			cursor := "  "
			label := fmt.Sprintf("%s. %s", c.Key, c.Label)
			if s.answering && i == s.answerChoice {
				cursor = warnText.Render("› ")
				label = warnText.Render(label)
			}
			b.WriteString(clamp(cursor+label) + "\n")
		}
	default:
		// A parsed free-form prompt, OR a parser miss the human is answering
		// anyway (no choices, no recognized prompt): render the free-form field.
		switch {
		case s.answering:
			if pd.Prompt == "" && !pd.FreeForm {
				b.WriteString(faintText.Render("(prompt not parsed — type a free-form reply)") + "\n")
			}
			b.WriteString(clamp(warnText.Render("answer")+faintText.Render("> ")+s.answerInput+"_") + "\n")
		case pd.FreeForm:
			b.WriteString(faintText.Render("(free-form answer)") + "\n")
		}
	}
	if s.answering {
		b.WriteString(clamp(faintText.Render("enter send · esc cancel")) + "\n")
	} else {
		b.WriteString(clamp(warnText.Render("a: answer")+faintText.Render(" this prompt")) + "\n")
	}
	return b.String()
}

// previewLine makes one raw capture-pane row safe to inject into the view.
// Two hazards (capture-pane runs with -e, so lines carry the agent pane's
// full width and its ANSI SGR codes verbatim):
//   - a line wider than our terminal physically wraps into 2+ rows, which
//     corrupts bubbletea's line-count-based repaint → ANSI-aware truncation
//     to width (0 = window size unknown yet, leave as is);
//   - tmux does not guarantee a closing SGR reset at end of capture, so an
//     open foreground/background attribute would bleed into the rest of the
//     frame → append an explicit reset to every line.
func previewLine(ln string, width int) string {
	if width > 0 && lipgloss.Width(ln) > width {
		ln = truncateANSI(ln, width)
	}
	return ln + "\x1b[0m"
}

// truncateANSI clips s to at most width display columns, ANSI-aware: escape
// sequences (CSI like SGR color codes, OSC like hyperlinks) are copied
// wholesale and cost no columns, so clipping never cuts a sequence in half
// and never drops color state the un-clipped part established. Width is
// measured per rune via lipgloss.Width, so wide (CJK etc.) runes count as
// their real column span. Hand-rolled because the module's only terminal-
// width libraries are indirect dependencies (kept out of go.mod).
func truncateANSI(s string, width int) string {
	var b strings.Builder
	cols := 0
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] == 0x1b { // ESC: copy the entire sequence, zero columns
			j := i + 1
			if j < len(rs) {
				switch rs[j] {
				case '[': // CSI: params/intermediates, then final byte 0x40–0x7e
					j++
					for j < len(rs) && (rs[j] < 0x40 || rs[j] > 0x7e) {
						j++
					}
				case ']': // OSC: until BEL or ST (ESC \)
					j++
					for j < len(rs) && rs[j] != 0x07 && rs[j] != 0x1b {
						j++
					}
					if j+1 < len(rs) && rs[j] == 0x1b && rs[j+1] == '\\' {
						j++
					}
				}
			}
			if j >= len(rs) {
				j = len(rs) - 1
			}
			b.WriteString(string(rs[i : j+1]))
			i = j
			continue
		}
		w := lipgloss.Width(string(rs[i]))
		if cols+w > width {
			break
		}
		cols += w
		b.WriteRune(rs[i])
	}
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// lastLines returns at most n trailing lines of text, with trailing blank
// lines dropped first so a mostly-empty pane doesn't render as whitespace.
func lastLines(text string, n int) []string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
