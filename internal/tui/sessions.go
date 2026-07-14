// Sessions tab (PLAN P1.8): read-only observability over agent sessions.
// Data comes from the daemon's cmd=sessions snapshot (never exec'd on the
// request path); the preview pane polls tmux capture-pane best-effort.
package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/tmux"
)

// previewLines is how many pane rows the preview shows (and requests as
// scrollback from capture-pane).
const previewLines = 8

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
	confirmKill bool   // "x" pressed: awaiting y/n to kill killTarget
	killTarget  string // session ID captured when "x" was pressed (pinned across refreshes)
	preview     string // capture-pane output for the selected tmux session
	previewFor  string // session ID the preview belongs to ("" = none)
	tmux        *tmux.Client
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

type previewMsg struct {
	id   string // session ID the capture was taken for
	text string
	err  error
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

func previewCmd(c *tmux.Client, id, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := c.CapturePane(ctx, name, previewLines)
		return previewMsg{id: id, text: out, err: err}
	}
}

// previewRefreshCmd returns a capture-pane refresh for the selected session,
// or nil (clearing any stale preview) when there is nothing to capture.
func (m *rootModel) previewRefreshCmd() tea.Cmd {
	sel := m.sessions.selected()
	if sel == nil || sel.TmuxName == "" {
		m.sessions.preview, m.sessions.previewFor = "", ""
		return nil
	}
	return previewCmd(m.sessions.tmuxClient(), sel.ID, sel.TmuxName)
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
		s.preview, s.previewFor = "", ""
		return nil
	}
	s.data, s.daemonDown, s.dataErr = v.data, false, ""
	if s.cursor >= len(s.data.Sessions) {
		s.cursor = len(s.data.Sessions) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	return m.previewRefreshCmd()
}

func (m *rootModel) handlePreviewMsg(v previewMsg) {
	s := &m.sessions
	sel := s.selected()
	if sel == nil || sel.ID != v.id {
		return // stale capture for a session no longer selected
	}
	if v.err != nil {
		s.preview, s.previewFor = "", sel.ID // renders as "(no preview)"
		return
	}
	s.preview, s.previewFor = v.text, sel.ID
}

func (m *rootModel) updateSessions(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	s := &m.sessions
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
	s.flash = ""
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
			return m, m.previewRefreshCmd()
		}
	case "down", "j":
		if s.data != nil && s.cursor < len(s.data.Sessions)-1 {
			s.cursor++
			return m, m.previewRefreshCmd()
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
	}
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
		b.WriteString(warnText.Render(s.flash) + "\n")
	}
	b.WriteString(faintText.Render("↑/↓ move · enter attach · o open PR · x kill · tab/1/2 switch view · q quit") + "\n")
	return b.String()
}

// sessionsTable renders the session list. Selection is marked with "›" only
// (no reverse video): the STATUS cell carries its own ANSI colors, and
// nesting them inside another style's escape sequences breaks both.
func (m *rootModel) sessionsTable() string {
	s := &m.sessions
	headers := []string{" ", "ISSUE", "PROJECT", "STATUS", "PR", "CHECKS", "REVIEW", "AGE"}
	rows := make([][]string, len(s.data.Sessions))
	for i, si := range s.data.Sessions {
		marker := " "
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
			pr, dash(si.Checks), dash(si.Review), dash(si.Age),
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
		b.WriteString(tblHeader.Render("preview") + " " + sourceBadge(sel.Source) +
			faintText.Render(" — tmux "+sel.TmuxName) + "\n")
		if sel.Worktree != "" {
			b.WriteString(faintText.Render("worktree: "+sel.Worktree) + "\n")
		}
		if s.previewFor == sel.ID && s.preview != "" {
			for _, ln := range lastLines(s.preview, previewLines) {
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
	fmt.Fprintf(&b, "age:      %s\n", dash(sel.Age))
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
