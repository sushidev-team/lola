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

// agentFrameMsg repaints when the embedded agent's screen changes. gen guards
// against stale waiters after a re-target (only the current generation re-arms).
type agentFrameMsg struct{ gen int }

// spinnerTickMsg advances the loading spinner.
type spinnerTickMsg struct{}

func waitAgentFrame(t *vtterm.Term, gen int) tea.Cmd {
	return func() tea.Msg {
		<-t.Frames()
		return agentFrameMsg{gen: gen}
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
		return nil
	}
	argv := m.sessions.tmuxClient(m.cfg.TmuxSocketName()).AttachArgs(sel.TmuxName)
	cw, ch := m.agentSize()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "LOLA_TERMINAL=1")
	t, err := vtterm.New(cmd, cw, ch)
	if err != nil {
		m.agentFor = ""
		return nil
	}
	m.agentTerm = &termView{term: t, sessionID: sel.ID, kind: termAgent, title: "agent · " + dash(sel.Issue), w: cw, h: ch}
	m.agentGen++
	cmds := []tea.Cmd{waitAgentFrame(t, m.agentGen)}
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
	m.agentFocused = false
	m.agentGen++
}

// resizeAgent re-sizes the embedded agent to the current window (called on a
// window resize, not on focus toggle).
func (m *rootModel) resizeAgent() {
	if m.agentTerm == nil {
		return
	}
	w, h := m.agentSize()
	m.agentTerm.w, m.agentTerm.h = w, h
	m.agentTerm.term.Resize(w, h)
}

// focusAgent expands + focuses the embedded agent (keyboard → agent).
func (m *rootModel) focusAgent() (tea.Model, tea.Cmd) {
	if m.agentTerm == nil || m.agentTerm.term.Exited() {
		m.sessions.flash, m.sessions.flashGood = "no live agent for this session", false
		return m, nil
	}
	m.agentFocused = true
	return m, nil
}

// handleAgentKey routes a keystroke while the embedded agent is FOCUSED: Ctrl-q
// unfocuses back to the cockpit (the agent keeps running); everything else is
// forwarded to the agent.
func (m *rootModel) handleAgentKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+q" {
		m.agentFocused = false
		return m, nil
	}
	if m.agentTerm != nil {
		if b := keyToBytes(k); len(b) > 0 {
			m.agentTerm.term.Write(b)
		}
	}
	return m, nil
}
