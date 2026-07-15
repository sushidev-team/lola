// Embedded terminals for the Detail panel. Both kinds — the live AGENT (a tmux
// attach) and a per-session SHELL (its worktree) — render in-panel and share one
// focus/keyboard model (see agentembed.go: currentEmbed / handleEmbedKey). This
// file owns the SHELL side: the per-session registry (m.terms, persistent so a
// `composer dev` survives a detach and can be re-entered), reaping, and the
// keystroke encoder shared by both kinds.
package tui

import (
	"os"
	"os/exec"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/vtterm"
)

// Terminal kinds. A SHELL owns a durable process (composer dev), so it lives in
// the registry and Ctrl-q merely unfocuses it. An AGENT is a `tmux attach` view
// onto the agent's tmux session — the tmux session is the durable thing.
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
	w, h      int // terminal grid size
}

// newShellTerm starts $SHELL (login) in dir as a per-session embedded shell,
// registers it (persistent), and returns it. Sized to the embed dimensions so
// the Detail viewport and the focused-expanded view share one geometry.
func (m *rootModel) newShellTerm(sessionID, title, dir string) (*termView, error) {
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

	cw, ch := m.agentSize()
	t, err := vtterm.New(cmd, cw, ch)
	if err != nil {
		return nil, err
	}
	tv := &termView{term: t, title: title, sessionID: sessionID, kind: termShell, w: cw, h: ch}
	if m.terms == nil {
		m.terms = map[string]*termView{}
	}
	m.terms[sessionID] = tv
	return tv, nil
}

// reapTerm kills + closes a terminal and removes it from the registry — used
// when its process has exited, its session is killed, or lola quits.
func (m *rootModel) reapTerm(tv *termView, flash string) {
	if tv == nil {
		return
	}
	_ = tv.term.Close()
	delete(m.terms, tv.sessionID)
	if flash != "" {
		m.sessions.flash, m.sessions.flashGood = flash, false
	}
}

// sweepTerms reaps any registered shell whose process has exited but which is
// not the one currently shown, so a backgrounded shell that ended on its own
// doesn't linger as a zombie. Called off the status tick.
func (m *rootModel) sweepTerms() {
	cur := m.currentEmbed()
	for id, tv := range m.terms {
		if tv != cur && tv.term.Exited() {
			_ = tv.term.Close()
			delete(m.terms, id)
		}
	}
}

// closeAllTerms tears every terminal down — called on quit so no child process
// is orphaned (shells and the embedded agent attach).
func (m *rootModel) closeAllTerms() {
	for id, tv := range m.terms {
		_ = tv.term.Close()
		delete(m.terms, id)
	}
	m.closeAgent()
}

// runningShell reports whether a session has a live (non-exited) shell, so the
// cockpit can advertise the re-enter affordance.
func (m *rootModel) runningShell(sessionID string) bool {
	tv := m.terms[sessionID]
	return tv != nil && !tv.term.Exited()
}

// keyToBytes encodes a bubbletea v2 key press as the input bytes a PTY child
// expects. bubbletea parses stdin into key events (losing the raw bytes), so
// this re-encodes the common set: the named keys, arrows/nav, ctrl combos
// (ctrl+a → 0x01 … ctrl+z → 0x1a), and printable text (k.Text). Alt prefixes an
// ESC. Exotic sequences are not round-tripped; PASTE arrives separately as a
// tea.PasteMsg (see handleEmbedPaste).
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
