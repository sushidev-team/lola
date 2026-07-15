// Embedded terminal surface: a live vtterm.Term rendered full-screen over the
// cockpit. The Ctrl-q leader is the ONLY reserved key — every other keystroke is
// encoded and forwarded to the child PTY, so shortcuts inside (the shell, vim,
// the agent) keep working. This is the first surface of the "workspace"; tabs
// and the agent view build on it.
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

// termView holds one open embedded terminal.
type termView struct {
	term  *vtterm.Term
	title string
	w, h  int // terminal CONTENT size (window minus chrome)
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

// openShellTerm starts $SHELL (login) in dir (a session's worktree) as an
// embedded terminal sized to the current window, and returns the first
// frame-wait command.
func (m *rootModel) openShellTerm(title, dir string) tea.Cmd {
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
	m.term = &termView{term: t, title: title, w: cw, h: ch}
	return waitTermFrame(t)
}

// openShellForSelected opens an embedded shell in the selected session's
// worktree — the surface for running the app (composer dev, tests, …) next to
// the agent that built it.
func (m *rootModel) openShellForSelected() (tea.Model, tea.Cmd) {
	sel := m.sessions.selected()
	if sel == nil {
		return m, nil
	}
	if sel.Worktree == "" {
		m.sessions.flash, m.sessions.flashGood = "no worktree for this session", false
		return m, nil
	}
	return m, m.openShellTerm("shell · "+dash(sel.Issue), sel.Worktree)
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

// closeTerm tears down the open terminal (killing the child) and optionally
// flashes a message in the cockpit it returns to.
func (m *rootModel) closeTerm(flash string) {
	if m.term != nil {
		_ = m.term.term.Close()
		m.term = nil
	}
	if flash != "" {
		m.sessions.flash, m.sessions.flashGood = flash, false
	}
}

// handleTermKey routes a keystroke while a terminal is open: Ctrl-q exits back
// to the cockpit; everything else is encoded and forwarded to the child.
func (m *rootModel) handleTermKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+q" {
		m.closeTerm("")
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
	b.WriteString(previewLine(faintText.Render("Ctrl-q back to lola")+" · "+faintText.Render("all other keys go to the terminal"), W))
	return b.String()
}
