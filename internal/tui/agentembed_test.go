package tui

import (
	"os/exec"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/vtterm"
)

// enter focuses the embedded agent; with no live agent it flashes and stays in
// the cockpit.
func TestFocusAgentGuard(t *testing.T) {
	m := newTestRoot(t)
	m.sessions.data = cannedSessions()
	m.sessions.cursor = 0 // no agentTerm attached (tests don't spawn tmux)

	m.focusAgent()
	if m.agentFocused {
		t.Error("focusAgent with no live agent must not focus")
	}
	if got := m.sessions.flash; got == "" {
		t.Error("focusAgent with no live agent must flash a hint")
	}
}

// focusAgent focuses a live embedded agent; Ctrl-q unfocuses it WITHOUT killing
// the attach (the agent keeps running).
func TestFocusAgentThenCtrlQUnfocuses(t *testing.T) {
	m := newTestRoot(t)
	term, err := vtterm.New(exec.Command("cat"), 40, 10) // stands in for the attach
	if err != nil {
		t.Fatalf("vtterm.New: %v", err)
	}
	defer term.Close()
	m.agentTerm = &termView{term: term, sessionID: "s1", kind: termAgent}
	m.agentFor = "s1"

	if m.focusAgent(); !m.agentFocused {
		t.Fatal("focusAgent must focus a live agent")
	}
	m.handleAgentKey(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if m.agentFocused {
		t.Error("ctrl+q must unfocus the agent")
	}
	if term.Exited() {
		t.Error("ctrl+q must NOT kill the embedded agent")
	}
}

// Moving the selection schedules a DEBOUNCED attach and bumps the token; a
// superseded (stale-token) debounce fires nothing, so fast scrolling attaches
// only once.
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
	// A second change supersedes the first.
	m.sessions.selID, m.sessions.cursor = "s2", 1
	m.scheduleAgentSync()
	if m.agentDebounce == stale {
		t.Error("a newer selection change must bump the debounce token")
	}
	// The stale debounce fires nothing (no attach).
	if _, c := m.Update(agentDebounceMsg{token: stale}); c != nil {
		t.Error("a superseded debounce token must be ignored")
	}
}

// syncAgentPreview never opens an attach for a session that has no tmux pane or
// whose agent has exited — it leaves the embed empty.
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
			if cmd := m.syncAgentPreview(); cmd != nil {
				t.Error("must not open an agent for a non-live session")
			}
			if m.agentTerm != nil || m.agentFor != "" {
				t.Errorf("agent embed must stay empty (term=%v for=%q)", m.agentTerm, m.agentFor)
			}
		})
	}
}
