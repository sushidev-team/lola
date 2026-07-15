// Live embedded agent terminal shown in the Detail panel for the SELECTED
// session. It re-targets as the selection moves (always-live): each target is a
// fresh `tmux attach` into that session's tmux, rendered in-panel. 'enter'
// focuses + expands it into the main column (the cockpit chrome stays visible,
// so it is embedded, not a full-screen takeover); Ctrl-q shrinks it back. The
// tmux session is the durable thing, so a selection change closes the attach and
// opens a new one — the agent itself keeps running regardless.
//
// The terminal is sized to the EXPANDED (focused) dimensions and kept there, so
// focusing/unfocusing never resizes the tmux session (no reflow thrash); the
// small in-panel view is just a bottom viewport of the same render.
package tui

import (
	"os"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/vtterm"
)

// spinnerFrames is a hand-rolled braille spinner (no bubbles dependency) for the
// "attaching…" state before the first frame arrives.
var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// embedFrameMsg repaints when the embedded agent's screen changes. gen guards
// against stale waiters after a re-target (only the current generation re-arms).
type embedFrameMsg struct{ gen int }

// spinnerTickMsg advances the loading spinner.
type spinnerTickMsg struct{}

// agentDebounceMsg fires after the selection has been still for a moment; only
// the latest token actually attaches, so fast scrolling doesn't spawn a tmux
// attach per row.
type agentDebounceMsg struct{ token int }

// agentDebounceDelay is how long the selection must settle before the live agent
// attaches.
const agentDebounceDelay = 180 * time.Millisecond

// scheduleAgentSync drops the stale agent view and debounces a re-attach to the
// (soon-to-be-settled) selection. A no-op when the right agent is already shown.
func (m *rootModel) scheduleAgentSync() tea.Cmd {
	sel := m.sessions.selected()
	target := ""
	if sel != nil && sel.TmuxName != "" && sel.Status != "dead" && sel.Status != "session_ended" {
		target = sel.ID
	}
	if target == m.agentFor && (m.agentTerm != nil || target == "") {
		return nil
	}
	m.closeAgent() // clear the previous session's view immediately
	m.agentDebounce++
	tok := m.agentDebounce
	return tea.Tick(agentDebounceDelay, func(time.Time) tea.Msg { return agentDebounceMsg{token: tok} })
}

func waitEmbedFrame(t *vtterm.Term, gen int) tea.Cmd {
	return func() tea.Msg {
		<-t.Frames()
		return embedFrameMsg{gen: gen}
	}
}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// agentLoading reports whether the embedded agent is attaching (exists, alive,
// but has not drawn its first frame yet).
func (m *rootModel) agentLoading() bool {
	return m.agentTerm != nil && !m.agentTerm.term.Exited() && len(m.agentTerm.term.Render()) == 0
}

func (m *rootModel) spinnerFrame() string {
	return string(spinnerFrames[m.spin%len(spinnerFrames)])
}

// agentSize is the FIXED terminal size: the expanded (focused) inner dimensions
// of the main column, mirroring the cockpit layout math.
func (m *rootModel) agentSize() (int, int) {
	W, H := m.width, m.height
	if W <= 0 {
		W = 100
	}
	if H <= 0 {
		H = 24
	}
	railW := 32
	if W < 104 {
		railW = 28
	}
	innerW := (W - railW - 1) - 2
	innerH := (H - 2) - 8 // main column minus the Sessions strip, fields, borders
	if innerW < 8 {
		innerW = 8
	}
	if innerH < 6 {
		innerH = 6
	}
	return innerW, innerH
}

// syncAgentPreview makes the live embedded agent match the current selection: a
// tmux-backed, non-terminal session gets a fresh attach; anything else clears
// it. A no-op when already showing the right session. Returns the frame-wait
// (and, for a new attach, the spinner) command.
func (m *rootModel) syncAgentPreview() tea.Cmd {
	sel := m.sessions.selected()
	target := ""
	if sel != nil && sel.TmuxName != "" && sel.Status != "dead" && sel.Status != "session_ended" {
		target = sel.ID
	}
	if target == m.agentFor && (m.agentTerm != nil || target == "") {
		return nil
	}
	m.closeAgent()
	m.agentFor = target
	if target == "" {
		return m.armEmbed() // may still show a shell for this session
	}
	argv := m.sessions.tmuxClient(m.cfg.TmuxSocketName()).AttachArgs(sel.TmuxName)
	cw, ch := m.agentSize()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "LOLA_TERMINAL=1")
	t, err := vtterm.New(cmd, cw, ch)
	if err != nil {
		m.agentFor = ""
		return m.armEmbed()
	}
	m.agentTerm = &termView{term: t, sessionID: sel.ID, kind: termAgent, title: "agent · " + dash(sel.Issue), w: cw, h: ch}
	cmds := []tea.Cmd{m.armEmbed()}
	if !m.spinning {
		m.spinning = true
		cmds = append(cmds, spinnerTickCmd())
	}
	return tea.Batch(cmds...)
}

// closeAgent tears down the embedded agent attach (the tmux session survives)
// and bumps the generation so any in-flight frame waiter is ignored.
func (m *rootModel) closeAgent() {
	if m.agentTerm != nil {
		_ = m.agentTerm.term.Close()
		m.agentTerm = nil
	}
	m.embedFocused = false
	m.embedGen++
}

// currentEmbed is the terminal shown in the Detail panel for the selection: the
// SHELL when the user switched to it (and it is live), otherwise the live AGENT.
func (m *rootModel) currentEmbed() *termView {
	sel := m.sessions.selected()
	if sel == nil {
		return nil
	}
	if m.showShell {
		if tv := m.terms[sel.ID]; tv != nil && !tv.term.Exited() {
			return tv
		}
	}
	if m.agentTerm != nil && m.agentFor == sel.ID {
		return m.agentTerm
	}
	return nil
}

// armEmbed (re)starts the repaint waiter for the current embed, bumping the
// generation so any stale waiter (from a previous embed) is ignored.
func (m *rootModel) armEmbed() tea.Cmd {
	m.embedGen++
	if e := m.currentEmbed(); e != nil {
		return waitEmbedFrame(e.term, m.embedGen)
	}
	return nil
}

// resizeEmbed re-sizes the live agent and the shown shell to the current window.
func (m *rootModel) resizeEmbed() {
	w, h := m.agentSize()
	if m.agentTerm != nil {
		m.agentTerm.w, m.agentTerm.h = w, h
		m.agentTerm.term.Resize(w, h)
	}
	if e := m.currentEmbed(); e != nil && e.kind == termShell {
		e.w, e.h = w, h
		e.term.Resize(w, h)
	}
}

// focusEmbed expands + focuses whatever the Detail panel is showing (agent or
// shell) so keystrokes flow to it.
func (m *rootModel) focusEmbed() (tea.Model, tea.Cmd) {
	e := m.currentEmbed()
	if e == nil || e.term.Exited() {
		m.sessions.flash, m.sessions.flashGood = "no live terminal for this session", false
		return m, nil
	}
	m.embedFocused = true
	return m, nil
}

// toggleShell switches the Detail panel between the agent view and a per-session
// worktree shell (opening the shell on first use), and focuses it. Pressing it
// again returns to the agent.
func (m *rootModel) toggleShell() (tea.Model, tea.Cmd) {
	sel := m.sessions.selected()
	if sel == nil {
		return m, nil
	}
	if m.showShell { // currently on the shell → back to the agent
		m.showShell = false
		return m, m.armEmbed()
	}
	if sel.Worktree == "" {
		m.sessions.flash, m.sessions.flashGood = "no worktree for this session", false
		return m, nil
	}
	tv := m.terms[sel.ID]
	if tv != nil && tv.term.Exited() {
		m.reapTerm(tv, "")
		tv = nil
	}
	if tv == nil {
		var err error
		if tv, err = m.newShellTerm(sel.ID, "shell · "+dash(sel.Issue), sel.Worktree); err != nil {
			m.sessions.flash, m.sessions.flashGood = "shell failed: "+err.Error(), false
			return m, nil
		}
	}
	m.showShell, m.embedFocused = true, true
	return m, m.armEmbed()
}

// handleEmbedKey routes a keystroke while the embed is FOCUSED: Ctrl-q unfocuses
// back to the cockpit (the terminal keeps running); everything else is forwarded
// to whatever is shown (agent or shell).
func (m *rootModel) handleEmbedKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+q" {
		m.embedFocused = false
		return m, nil
	}
	if e := m.currentEmbed(); e != nil {
		if b := keyToBytes(k); len(b) > 0 {
			e.term.Write(b)
		}
	}
	return m, nil
}

// handleEmbedPaste forwards pasted text to the focused embed as a BRACKETED
// paste, so the child (agent / vim) treats it as one paste rather than
// keystrokes that submit on the first newline. bubbletea v2 delivers paste as a
// separate tea.PasteMsg, which the key encoder never sees — this is why pasting
// otherwise did nothing.
func (m *rootModel) handleEmbedPaste(content string) (tea.Model, tea.Cmd) {
	if content == "" {
		return m, nil
	}
	if e := m.currentEmbed(); e != nil {
		e.term.Write([]byte("\x1b[200~" + content + "\x1b[201~"))
	}
	return m, nil
}

// embedBody renders the shown embed into the Detail panel: a spinner while an
// agent is attaching, a note if it ended, otherwise the BOTTOM h rows of its
// screen (a viewport — the small panel shows the tail, the focused/expanded
// panel shows it all).
func (m *rootModel) embedBody(e *termView, w, h int) []string {
	if e.kind == termAgent && m.agentLoading() {
		return []string{"", "  " + faintText.Render(m.spinnerFrame()+" attaching to agent…")}
	}
	if e.term.Exited() {
		return []string{"", "  " + faintText.Render("terminal ended")}
	}
	lines := e.term.Render()
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	out := make([]string, len(lines))
	for i, ln := range lines {
		out[i] = previewLine(ln, w)
	}
	return out
}
