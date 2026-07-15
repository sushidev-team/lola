// Embedded terminal surface: a live vtterm.Term rendered full-screen over the
// cockpit. The Ctrl-q leader is the ONLY reserved key — every other keystroke is
// encoded and forwarded to the child PTY, so shortcuts inside (the shell, vim,
// the agent) keep working.
//
// Terminals are PERSISTENT: Ctrl-q DETACHES (the shell keeps running in the
// background so `composer dev` survives), and 's' on the same session RE-ENTERS
// it. A terminal is reaped only when its process exits on its own (type `exit`
// or Ctrl-d), when the session is killed, or when lola quits. The per-session
// registry lives on the rootModel (m.terms).
package tui

import (
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/vtterm"
)

// termChromeRows is the title bar + hint bar the surface reserves around the
// terminal content.
const termChromeRows = 2

// Terminal kinds differ in what "detach" means. A SHELL owns a durable process
// (composer dev), so Ctrl-q keeps its PTY running and it lives in the registry
// to be re-entered. An AGENT terminal is just a `tmux attach` view onto the
// agent's tmux session — the tmux session is the durable thing, so Ctrl-q simply
// closes the attach (detaching the client) and it is never registered.
const (
	termShell = iota
	termAgent
)

// termView holds one embedded terminal.
type termView struct {
	term      *vtterm.Term
	title     string
	sessionID string
	kind      int // termShell | termAgent
	w, h      int // terminal CONTENT size (window minus chrome)
}

// termFrameMsg wakes the update loop when the terminal screen may have changed.
type termFrameMsg struct{}

// waitTermFrame blocks until the terminal signals a new frame (or its reader
// exits, which also signals), then asks for a repaint. Re-issued each frame.
func waitTermFrame(t *vtterm.Term) tea.Cmd {
	return func() tea.Msg {
		<-t.Frames()
		return termFrameMsg{}
	}
}

// openShellForSelected re-enters the selected session's existing shell if one is
// still running, otherwise opens a fresh $SHELL in its worktree — the surface
// for running the app (composer dev, tests, …) next to the agent that built it.
func (m *rootModel) openShellForSelected() (tea.Model, tea.Cmd) {
	sel := m.sessions.selected()
	if sel == nil {
		return m, nil
	}
	if sel.Worktree == "" {
		m.sessions.flash, m.sessions.flashGood = "no worktree for this session", false
		return m, nil
	}
	if tv := m.terms[sel.ID]; tv != nil {
		if tv.term.Exited() { // stale shell that has since exited: replace it
			m.reapTerm(tv, "")
		} else {
			return m, m.attachTerm(tv) // re-enter the running shell
		}
	}
	return m, m.openShellTerm(sel.ID, "shell · "+dash(sel.Issue), sel.Worktree)
}

// openShellTerm starts $SHELL (login) in dir as a new embedded terminal for
// sessionID, registers it, attaches to it, and returns the first frame-wait
// command.
func (m *rootModel) openShellTerm(sessionID, title, dir string) tea.Cmd {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-l")
	if dir != "" {
		cmd.Dir = dir
	}
	// TERM tells the child which sequences to emit; xterm-256color is what x/vt
	// models. Keep the user's env otherwise (PATH, etc. for composer/node/…).
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "LOLA_TERMINAL=1")

	cw, ch := m.termContentSize()
	t, err := vtterm.New(cmd, cw, ch)
	if err != nil {
		m.sessions.flash, m.sessions.flashGood = "shell failed: "+err.Error(), false
		return nil
	}
	tv := &termView{term: t, title: title, sessionID: sessionID, kind: termShell, w: cw, h: ch}
	if m.terms == nil {
		m.terms = map[string]*termView{}
	}
	m.terms[sessionID] = tv
	m.term = tv
	return waitTermFrame(t)
}

// attachTerm focuses an existing (running) terminal, fits it to the current
// window, and resumes its repaint loop.
func (m *rootModel) attachTerm(tv *termView) tea.Cmd {
	m.term = tv
	m.resizeTerm()
	return waitTermFrame(tv.term)
}

// termContentSize is the terminal grid size for the current window (floored).
func (m *rootModel) termContentSize() (int, int) {
	w, h := m.width, m.height
	if w <= 0 {
		w = 100
	}
	if h <= 0 {
		h = 24
	}
	ch := h - termChromeRows
	if ch < 1 {
		ch = 1
	}
	return w, ch
}

// resizeTerm re-sizes the open terminal to the current window.
func (m *rootModel) resizeTerm() {
	if m.term == nil {
		return
	}
	w, h := m.termContentSize()
	m.term.w, m.term.h = w, h
	m.term.term.Resize(w, h)
}

// detachTerm returns to the cockpit but leaves the terminal RUNNING in the
// registry, so a long-lived process (composer dev) survives and can be
// re-entered later with 's'.
func (m *rootModel) detachTerm() {
	m.term = nil
}

// reapTerm kills + closes a terminal and removes it from the registry — used
// when its process has exited, its session is killed, or lola quits. If it is
// the attached terminal, focus returns to the cockpit (with an optional flash).
func (m *rootModel) reapTerm(tv *termView, flash string) {
	if tv == nil {
		return
	}
	_ = tv.term.Close()
	delete(m.terms, tv.sessionID)
	if m.term == tv {
		m.term = nil
	}
	if flash != "" {
		m.sessions.flash, m.sessions.flashGood = flash, false
	}
}

// sweepTerms reaps any DETACHED terminal whose process has exited, so a
// backgrounded shell that ended on its own doesn't linger as a zombie. Called
// off the status tick; the attached terminal is reaped on its frame instead.
func (m *rootModel) sweepTerms() {
	for id, tv := range m.terms {
		if tv != m.term && tv.term.Exited() {
			_ = tv.term.Close()
			delete(m.terms, id)
		}
	}
}

// closeAllTerms tears every terminal down — called on quit so no child process
// is orphaned (shells, a focused shell, and the embedded agent attach).
func (m *rootModel) closeAllTerms() {
	if m.term != nil {
		_ = m.term.term.Close()
	}
	for id, tv := range m.terms {
		_ = tv.term.Close()
		delete(m.terms, id)
	}
	m.term = nil
	m.closeAgent()
}

// runningShell reports whether a session has a live (non-exited) shell to
// re-enter, so the cockpit can advertise it.
func (m *rootModel) runningShell(sessionID string) bool {
	tv := m.terms[sessionID]
	return tv != nil && !tv.term.Exited()
}

// handleTermKey routes a keystroke while a terminal is open: Ctrl-q DETACHES
// back to the cockpit (leaving the shell running); everything else is encoded
// and forwarded to the child.
func (m *rootModel) handleTermKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+q" {
		if m.term.kind == termAgent {
			m.reapTerm(m.term, "") // close the attach; the agent lives on in tmux
		} else {
			m.detachTerm() // keep the shell (and its composer dev) running
		}
		return m, nil
	}
	if b := keyToBytes(k); len(b) > 0 {
		m.term.term.Write(b)
	}
	return m, nil
}

// keyToBytes encodes a bubbletea v2 key press as the input bytes a PTY child
// expects. bubbletea parses stdin into key events (losing the raw bytes), so
// this re-encodes the common set: the named keys, arrows/nav, ctrl combos
// (ctrl+a → 0x01 … ctrl+z → 0x1a), and printable text (k.Text). Alt prefixes an
// ESC. Exotic sequences are not round-tripped.
func keyToBytes(k tea.KeyPressMsg) []byte {
	var b []byte
	switch k.Code {
	case tea.KeyEnter:
		b = []byte("\r")
	case tea.KeyTab:
		b = []byte("\t")
	case tea.KeyBackspace:
		b = []byte{0x7f}
	case tea.KeyEscape:
		b = []byte{0x1b}
	case tea.KeyDelete:
		b = []byte("\x1b[3~")
	case tea.KeyUp:
		b = []byte("\x1b[A")
	case tea.KeyDown:
		b = []byte("\x1b[B")
	case tea.KeyRight:
		b = []byte("\x1b[C")
	case tea.KeyLeft:
		b = []byte("\x1b[D")
	case tea.KeyHome:
		b = []byte("\x1b[H")
	case tea.KeyEnd:
		b = []byte("\x1b[F")
	case tea.KeyPgUp:
		b = []byte("\x1b[5~")
	case tea.KeyPgDown:
		b = []byte("\x1b[6~")
	default:
		switch {
		case k.Mod.Contains(tea.ModCtrl) && k.Code >= 'a' && k.Code <= 'z':
			b = []byte{byte(k.Code-'a') + 1} // ctrl+letter → ASCII control byte
		case k.Text != "":
			b = []byte(k.Text) // printable runes (and space)
		}
	}
	if k.Mod.Contains(tea.ModAlt) && len(b) > 0 {
		b = append([]byte{0x1b}, b...)
	}
	return b
}

// termSurfaceView renders the full-screen terminal: a title bar, the emulated
// screen (padded to the content height), and a hint bar.
func (m *rootModel) termSurfaceView() string {
	tv := m.term
	W := m.width
	if W <= 0 {
		W = 100
	}
	var b strings.Builder
	b.WriteString(previewLine(boxTitleHi.Render("⛶ "+tv.title)+faintText.Render("  ·  embedded terminal"), W) + "\n")

	lines := tv.term.Render()
	lines = fitHeight(lines, tv.h)
	for _, ln := range lines {
		b.WriteString(previewLine(ln, W) + "\n")
	}
	var hint string
	if tv.kind == termAgent {
		hint = goodText.Render("Ctrl-q") + faintText.Render(" detach — the agent keeps running in tmux · other keys → agent")
	} else {
		hint = goodText.Render("Ctrl-q") + faintText.Render(" detach (shell keeps running) · exit/Ctrl-d ends it · other keys → terminal")
	}
	b.WriteString(previewLine(hint, W))
	return b.String()
}
