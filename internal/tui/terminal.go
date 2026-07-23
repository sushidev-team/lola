// Embedded terminal for the Detail panel. Both kinds — the live AGENT and a
// worktree SHELL — are `tmux attach` views onto a tmux session on lola's server;
// the tmux session is the durable thing (shared with the desktop app, which
// attaches to the very same sessions). Only the ACTIVE tab is attached at a time
// (a single embed), so switching tabs re-attaches; the other shells keep running
// detached. agentembed.go owns the attach + the per-session tab model
// (shellNames / embedTab); this file holds the shared value type and keystroke
// encoder.
package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/vtterm"
)

// Terminal kinds. Both are tmux attaches; the kind only drives labelling and the
// "attaching…" spinner (agent only).
const (
	termShell = iota
	termAgent
)

// termView holds the one live embed attach.
type termView struct {
	term      *vtterm.Term
	title     string
	sessionID string
	tmuxName  string // the tmux session this attaches to (agent name or "<id>-shell-N")
	kind      int    // termShell | termAgent
	w, h      int    // terminal grid size
}

// shellIndex parses the trailing N from a "<id>-shell-N" tmux name (0 if absent),
// so shells sort and number stably. Shared by the TUI and mirrored in the app.
func shellIndex(id, name string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(name, id+"-shell-"))
	return n
}

// cycleTabIndex advances a tab index across {agent(0), shell1…shellN}, wrapping.
// Pure so the wrap math is unit-testable without a tmux server.
func cycleTabIndex(cur, nShells, dir int) int {
	span := nShells + 1
	return ((cur+dir)%span + span) % span
}

// runningShell reports whether a session has any shell tab, so the cockpit can
// advertise the re-enter affordance. Reads the last discovered list.
func (m *rootModel) runningShell(sessionID string) bool {
	return len(m.shellNames[sessionID]) > 0
}

// closeAllTerms tears the live embed down — called on quit so the attach child
// isn't orphaned. The shells are tmux sessions that keep running (detached),
// exactly like the agent, so there is nothing else to close.
func (m *rootModel) closeAllTerms() {
	m.closeAgent()
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
