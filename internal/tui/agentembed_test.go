package tui

import (
	"os/exec"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/vtterm"
)

// enter focuses the shown embed; with nothing embedded it flashes and stays in
// the cockpit.
func TestFocusEmbedGuard(t *testing.T) {
	m := newTestRoot(t)
	m.sessions.data = cannedSessions()
	m.sessions.cursor = 0 // no embed attached (tests don't spawn tmux)

	m.focusEmbed()
	if m.embedFocused {
		t.Error("focusEmbed with no live embed must not focus")
	}
	if m.sessions.flash == "" {
		t.Error("focusEmbed with no live embed must flash a hint")
	}
}

// focusEmbed focuses the live embed; Ctrl-q unfocuses it WITHOUT killing it.
func TestFocusEmbedThenCtrlQUnfocuses(t *testing.T) {
	m := newTestRoot(t)
	m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{{ID: "s1", Issue: "ENG-1", TmuxName: "lola-x", Status: "working"}}}
	m.sessions.selID, m.sessions.cursor = "s1", 0
	term, err := vtterm.New(exec.Command("cat"), 40, 10) // stands in for the attach
	if err != nil {
		t.Fatalf("vtterm.New: %v", err)
	}
	defer term.Close()
	m.agentTerm = &termView{term: term, sessionID: "s1", kind: termAgent}
	m.agentFor = "s1"

	if m.focusEmbed(); !m.embedFocused {
		t.Fatal("focusEmbed must focus the live embed")
	}
	m.handleEmbedKey(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if m.embedFocused {
		t.Error("ctrl+q must unfocus the embed")
	}
	if term.Exited() {
		t.Error("ctrl+q must NOT kill the embed")
	}
}

// A focused embed leaves the mouse with the terminal by DEFAULT (native
// selection/copy + ⌘-click work everywhere); Ctrl-g opts into scroll-mode
// (capture the mouse to forward the wheel) without unfocusing, and is swallowed
// rather than forwarded to the child.
func TestFocusEmbedCtrlGTogglesScrollMode(t *testing.T) {
	m := newTestRoot(t)
	m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{{ID: "s1", Issue: "ENG-1", TmuxName: "lola-x", Status: "working"}}}
	m.sessions.selID, m.sessions.cursor = "s1", 0
	term, err := vtterm.New(exec.Command("cat"), 40, 10)
	if err != nil {
		t.Fatalf("vtterm.New: %v", err)
	}
	defer term.Close()
	m.agentTerm = &termView{term: term, sessionID: "s1", kind: termAgent}
	m.agentFor = "s1"

	if m.focusEmbed(); !m.embedFocused {
		t.Fatal("focusEmbed must focus the live embed")
	}
	// Focused by default: the mouse stays with the terminal (no capture), so
	// ⌘-click and drag-select keep working.
	if got := m.View().MouseMode; got != tea.MouseModeNone {
		t.Fatalf("focused embed must leave the mouse to the terminal, got MouseMode %v", got)
	}
	m.handleEmbedKey(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if !m.embedScroll {
		t.Error("ctrl+g must enter scroll-mode")
	}
	if m.embedFocused == false {
		t.Error("ctrl+g must NOT unfocus the embed")
	}
	// Scroll-mode captures the mouse so the wheel can be forwarded to the agent.
	if got := m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("scroll-mode must capture the mouse, got MouseMode %v", got)
	}
	// Toggling back releases the mouse to the terminal again.
	m.handleEmbedKey(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if m.embedScroll {
		t.Error("ctrl+g again must leave scroll-mode")
	}
	if got := m.View().MouseMode; got != tea.MouseModeNone {
		t.Errorf("leaving scroll-mode must release the mouse, got MouseMode %v", got)
	}
}

// A paste is forwarded to the focused embed wrapped in bracketed-paste markers.
func TestEmbedPasteForwarded(t *testing.T) {
	m := newTestRoot(t)
	m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{{ID: "s1", TmuxName: "lola-x", Status: "working"}}}
	m.sessions.selID, m.sessions.cursor = "s1", 0
	term, err := vtterm.New(exec.Command("cat"), 40, 6) // echoes stdin
	if err != nil {
		t.Fatalf("vtterm.New: %v", err)
	}
	defer term.Close()
	m.agentTerm = &termView{term: term, sessionID: "s1", kind: termAgent}
	m.agentFor = "s1"

	m.handleEmbedPaste("hello-paste")
	// cat echoes the pasted bytes (incl. the bracketed-paste markers) back.
	got := false
	for range 40 {
		for _, ln := range term.Render() {
			if containsSub(ln, "hello-paste") {
				got = true
			}
		}
		if got {
			break
		}
		<-term.Frames()
	}
	if !got {
		t.Errorf("pasted text never reached the embed:\n%q", term.Render())
	}
}

// Moving the selection schedules a DEBOUNCED attach and bumps the token; a
// superseded (stale-token) debounce fires nothing.
func TestAgentSyncDebounces(t *testing.T) {
	m := newTestRoot(t)
	m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{
		{ID: "s1", TmuxName: "lola-x", Status: "working"},
		{ID: "s2", TmuxName: "lola-y", Status: "working"},
	}}
	m.sessions.selID, m.sessions.cursor = "s1", 0

	if cmd := m.scheduleAgentSync(); cmd == nil {
		t.Fatal("a selection change must schedule a debounced sync")
	}
	stale := m.agentDebounce
	if m.agentTerm != nil {
		t.Error("scheduling must NOT attach immediately (that is the debounce point)")
	}
	m.sessions.selID, m.sessions.cursor = "s2", 1
	m.scheduleAgentSync()
	if m.agentDebounce == stale {
		t.Error("a newer selection change must bump the debounce token")
	}
	if _, c := m.Update(agentDebounceMsg{token: stale}); c != nil {
		t.Error("a superseded debounce token must be ignored")
	}
}

// syncAgentPreview never opens an attach for a session with no tmux pane or a
// terminal (dead/ended) status.
func TestSyncAgentSkipsNonLive(t *testing.T) {
	for _, tc := range []struct {
		name string
		si   protocol.SessionInfo
	}{
		{"no tmux", protocol.SessionInfo{ID: "s1", Status: "working"}},
		{"dead", protocol.SessionInfo{ID: "s1", TmuxName: "lola-x", Status: "dead"}},
		{"ended", protocol.SessionInfo{ID: "s1", TmuxName: "lola-x", Status: "session_ended"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestRoot(t)
			m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{tc.si}}
			m.sessions.cursor = 0
			m.syncAgentPreview()
			if m.agentTerm != nil || m.agentFor != "" {
				t.Errorf("agent embed must stay empty (term=%v for=%q)", m.agentTerm, m.agentFor)
			}
		})
	}
}

// toggleShell on a session with no worktree flashes and opens nothing.
func TestToggleShellNoWorktree(t *testing.T) {
	m := newTestRoot(t)
	m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{{ID: "s1", TmuxName: "lola-x", Worktree: ""}}}
	m.sessions.cursor = 0
	m.toggleShell()
	if m.showShell || len(m.terms) != 0 {
		t.Error("toggleShell without a worktree must not open a shell")
	}
	if m.sessions.flash == "" {
		t.Error("toggleShell without a worktree must flash")
	}
}

func containsSub(s, sub string) bool {
	return len(stripANSI(s)) > 0 && indexOf(stripANSI(s), sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
