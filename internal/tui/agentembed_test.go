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

// A focused embed captures the mouse by DEFAULT so the wheel forwards to the
// agent; Ctrl-g opts into select-mode (release the mouse for native
// selection/copy + ⌘-click) without unfocusing, and is swallowed rather than
// forwarded to the child.
func TestFocusEmbedCtrlGTogglesSelectMode(t *testing.T) {
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
	// Focused by default: the mouse is captured so the wheel forwards to the agent.
	if got := m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Fatalf("focused embed must capture the mouse, got MouseMode %v", got)
	}
	m.handleEmbedKey(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if !m.embedSelect {
		t.Error("ctrl+g must enter select-mode")
	}
	if m.embedFocused == false {
		t.Error("ctrl+g must NOT unfocus the embed")
	}
	// Select-mode releases the mouse so the terminal owns selection + ⌘-click.
	if got := m.View().MouseMode; got != tea.MouseModeNone {
		t.Errorf("select-mode must release the mouse, got MouseMode %v", got)
	}
	// Toggling back re-captures the mouse.
	m.handleEmbedKey(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if m.embedSelect {
		t.Error("ctrl+g again must leave select-mode")
	}
	if got := m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("leaving select-mode must re-capture the mouse, got MouseMode %v", got)
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

// newShell on a session with no worktree flashes and opens nothing (it returns
// before touching tmux).
func TestNewShellNoWorktree(t *testing.T) {
	m := newTestRoot(t)
	m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{{ID: "s1", TmuxName: "lola-x", Worktree: ""}}}
	m.sessions.cursor = 0
	m.newShell()
	if len(m.shellNames["s1"]) != 0 {
		t.Error("newShell without a worktree must not open a shell")
	}
	if m.sessions.flash == "" {
		t.Error("newShell without a worktree must flash")
	}
}

// activeTabTmux resolves the selected session's active tab to the tmux session
// the embed attaches to: the agent's pane on tab 0, the matching shell on a shell
// tab, and a fall-back to the agent for a stale index or a dead agent.
func TestActiveTabTmux(t *testing.T) {
	m := newTestRoot(t)
	m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{{ID: "s1", TmuxName: "s1", Worktree: "/wt", Status: "working"}}}
	m.sessions.cursor = 0
	m.shellNames = map[string][]string{"s1": {"s1-shell-1", "s1-shell-2"}}
	m.embedTab = map[string]int{}

	// Tab 0 → the agent's tmux session.
	if name, kind, ok := m.activeTabTmux(); !ok || kind != termAgent || name != "s1" {
		t.Fatalf("agent tab: got (%q, %d, %v), want (s1, agent, true)", name, kind, ok)
	}
	// Tab 2 → the second shell.
	m.embedTab["s1"] = 2
	if name, kind, ok := m.activeTabTmux(); !ok || kind != termShell || name != "s1-shell-2" {
		t.Fatalf("shell tab: got (%q, %d, %v), want (s1-shell-2, shell, true)", name, kind, ok)
	}
	// A stale index beyond the shell count falls back to the agent.
	m.embedTab["s1"] = 9
	if name, kind, _ := m.activeTabTmux(); kind != termAgent || name != "s1" {
		t.Fatalf("stale tab must fall back to the agent, got (%q, %d)", name, kind)
	}
	// A dead agent on tab 0 → nothing live to attach.
	m.embedTab["s1"] = 0
	m.sessions.data.Sessions[0].Status = "dead"
	if _, _, ok := m.activeTabTmux(); ok {
		t.Fatal("dead agent tab must report nothing live")
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
