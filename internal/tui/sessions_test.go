package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sushidev-team/lola/internal/protocol"
)

func cannedSessions() *protocol.SessionsData {
	return &protocol.SessionsData{Sessions: []protocol.SessionInfo{
		{
			ID: "s1", Project: "web", Issue: "ENG-123", Branch: "lola/eng-123",
			Status: "working", TmuxName: "lola-eng-123", Age: "2h05m",
		},
		{
			ID: "s2", Project: "api", Issue: "ENG-456", Branch: "lola/eng-456",
			Status: "ci_failed", PRURL: "https://github.com/x/y/pull/7",
			PRNumber: 7, Checks: "fail", Review: "REVIEW_REQUIRED", Age: "3d2h",
		},
	}}
}

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestTabSwitchRendersSessionsTable(t *testing.T) {
	m := newTestRoot(t)

	if _, _ = m.Update(keyMsg("tab")); m.tab != tabSessions {
		t.Fatalf("tab key: tab = %d, want tabSessions", m.tab)
	}
	m.Update(sessionsMsg{data: cannedSessions()})

	v := m.View()
	for _, want := range []string{"ISSUE", "PROJECT", "STATUS", "ENG-123", "ENG-456", "web", "api", "#7", "2h05m"} {
		if !strings.Contains(v, want) {
			t.Errorf("sessions view missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "LAST SPAWN") {
		t.Error("sessions view must not render the polls table")
	}

	// "1" returns to polls, "2" goes back to sessions.
	if _, _ = m.Update(keyMsg("1")); m.tab != tabPolls {
		t.Fatalf("key 1: tab = %d, want tabPolls", m.tab)
	}
	if !strings.Contains(m.View(), "LAST SPAWN") {
		t.Error("polls view must render after switching back")
	}
	if _, _ = m.Update(keyMsg("2")); m.tab != tabSessions {
		t.Fatalf("key 2: tab = %d, want tabSessions", m.tab)
	}
}

func TestStatusStyleMapping(t *testing.T) {
	cases := []struct {
		status string
		fg     lipgloss.TerminalColor
	}{
		{"working", lipgloss.Color("12")},
		{"ci_failed", lipgloss.Color("9")},
		{"changes_requested", lipgloss.Color("9")},
		{"merge_conflict", lipgloss.Color("9")},
		{"approved", lipgloss.Color("10")},
		{"needs_input", lipgloss.Color("208")},
		{"no_signal", lipgloss.Color("208")},
	}
	for _, c := range cases {
		if got := statusStyle(c.status).GetForeground(); got != c.fg {
			t.Errorf("statusStyle(%q) foreground = %v, want %v", c.status, got, c.fg)
		}
	}
	if !statusStyle("merged").GetFaint() {
		t.Error("statusStyle(merged) must be faint")
	}
	if fg := statusStyle("review_pending").GetForeground(); fg != (lipgloss.NoColor{}) {
		t.Errorf("statusStyle(review_pending) foreground = %v, want none", fg)
	}
}

func TestAttachGuardedWithoutTmux(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.sessions.data = cannedSessions()
	m.sessions.cursor = 1 // s2 has TmuxName ""

	_, cmd := m.Update(keyMsg("enter"))
	if cmd != nil {
		t.Error("enter on a tmux-less session must not return a command")
	}
	if got := m.sessions.flash; got != "no tmux session (AO desktop runtime)" {
		t.Errorf("flash = %q, want tmux-less hint", got)
	}
}

func TestAttachWithTmuxReturnsExec(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.sessions.data = cannedSessions()
	m.sessions.cursor = 0 // s1 has a tmux session

	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter on a tmux-backed session must return an exec command")
	}
	if m.sessions.flash != "" {
		t.Errorf("flash = %q, want empty", m.sessions.flash)
	}
}

func TestSessionsDaemonDownHint(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.Update(sessionsMsg{err: errDaemonDown})

	v := m.View()
	if !strings.Contains(v, "daemon: not running") {
		t.Errorf("view must hint that the daemon is down:\n%s", v)
	}
}

func TestSessionDetailCardWithoutTmux(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.sessions.data = cannedSessions()
	m.sessions.cursor = 1 // no tmux -> detail card

	v := m.View()
	for _, want := range []string{"detail", "lola/eng-456", "https://github.com/x/y/pull/7", "3d2h"} {
		if !strings.Contains(v, want) {
			t.Errorf("detail card missing %q:\n%s", want, v)
		}
	}
}

func TestSessionPreviewPlaceholderWithTmux(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.sessions.data = cannedSessions()
	m.sessions.cursor = 0 // tmux-backed, no capture yet

	if v := m.View(); !strings.Contains(v, "(no preview)") {
		t.Errorf("preview pane must show placeholder before a capture arrives:\n%s", v)
	}

	m.Update(previewMsg{id: "s1", text: "line1\nline2\n"})
	v := m.View()
	if !strings.Contains(v, "line1") || !strings.Contains(v, "line2") {
		t.Errorf("preview pane must show capture output:\n%s", v)
	}

	// A stale capture for a no-longer-selected session is dropped.
	m.sessions.cursor = 1
	m.Update(previewMsg{id: "s1", text: "stale"})
	if strings.Contains(m.View(), "stale") {
		t.Error("stale preview for an unselected session must be ignored")
	}
}

// previewLine: capture-pane -e output is raw — lines carry the agent pane's
// full width and possibly unclosed SGR sequences. Over-width lines corrupt
// bubbletea's repaint (physical wrapping), open SGR bleeds color into the
// rest of the frame.
func TestPreviewLineTruncatesAndResets(t *testing.T) {
	long := "\x1b[31m" + strings.Repeat("x", 50) // red, never reset, 50 cols wide
	got := previewLine(long, 20)
	if !strings.HasSuffix(got, "\x1b[0m") {
		t.Errorf("previewLine must append an SGR reset, got %q", got)
	}
	if w := lipgloss.Width(got); w > 20 {
		t.Errorf("previewLine width = %d, want <= 20 (ANSI-aware truncation)", w)
	}
	if !strings.Contains(got, "\x1b[31m") {
		t.Errorf("truncation must be ANSI-aware and keep escape sequences, got %q", got)
	}
	// A mid-line SGR sequence after the cut point is dropped whole, never
	// sliced in half; one before the cut point survives whole.
	mid := "\x1b[32m" + strings.Repeat("y", 10) + "\x1b[41m" + strings.Repeat("z", 10)
	tr := truncateANSI(mid, 5)
	if tr != "\x1b[32myyyyy" {
		t.Errorf("truncateANSI = %q, want green prefix + 5 columns", tr)
	}
	// Wide runes count their real column span.
	if got := truncateANSI("ab日本", 3); got != "ab" {
		t.Errorf("truncateANSI wide runes = %q, want %q (日 needs 2 columns)", got, "ab")
	}

	// Width 0 = no WindowSizeMsg yet: no truncation, but still reset.
	if got := previewLine("abc", 0); got != "abc\x1b[0m" {
		t.Errorf("previewLine(abc, 0) = %q, want abc + reset", got)
	}
	// Narrow lines pass through untruncated.
	if got := previewLine("ok", 20); got != "ok\x1b[0m" {
		t.Errorf("previewLine(ok, 20) = %q, want ok + reset", got)
	}
}

func TestSessionPreviewClippedToTerminalWidth(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.width = 20 // as set by tea.WindowSizeMsg
	m.sessions.data = cannedSessions()
	m.sessions.cursor = 0 // s1 is tmux-backed

	m.Update(previewMsg{id: "s1", text: strings.Repeat("a", 100) + "TAIL\n"})
	if strings.Contains(m.View(), "TAIL") {
		t.Error("over-width preview line must be clipped to the terminal width")
	}
	if !strings.Contains(m.View(), "aaa") {
		t.Error("clipped preview line must still render its head")
	}
}
