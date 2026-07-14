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
			Source: "native", Worktree: "/tmp/lola/worktrees/web/lola-web-eng-123",
		},
		{
			ID: "s2", Project: "api", Issue: "ENG-456", Branch: "lola/eng-456",
			Status: "ci_failed", PRURL: "https://github.com/x/y/pull/7",
			PRNumber: 7, Checks: "fail", Review: "REVIEW_REQUIRED", Age: "3d2h",
			CIRetries: 1, Reacting: "ci retry 1/2",
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
	for _, want := range []string{"ISSUE", "PROJECT", "STATUS", "REACTING", "ENG-123", "ENG-456", "web", "api", "#7", "2h05m", "ci retry 1/2"} {
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
	for _, dim := range []string{"merged", "session_ended", "idle"} {
		if !statusStyle(dim).GetFaint() {
			t.Errorf("statusStyle(%q) must be faint", dim)
		}
	}
	if bg := statusStyle("dead").GetBackground(); bg != lipgloss.Color("9") {
		t.Errorf("statusStyle(dead) background = %v, want red (9)", bg)
	}
	if fg := statusStyle("review_pending").GetForeground(); fg != (lipgloss.NoColor{}) {
		t.Errorf("statusStyle(review_pending) foreground = %v, want none", fg)
	}
}

// P3: the reaction posture label is colored — escalated red (needs a human),
// ready-to-merge green, an active retry/rework yellow, everything else unstyled.
func TestReactingStyleMapping(t *testing.T) {
	if fg := reactingStyle("escalated").GetForeground(); fg != lipgloss.Color("9") {
		t.Errorf("escalated foreground = %v, want red (9)", fg)
	}
	if fg := reactingStyle("ready to merge").GetForeground(); fg != lipgloss.Color("10") {
		t.Errorf("ready to merge foreground = %v, want green (10)", fg)
	}
	for _, y := range []string{"ci retry 1/2", "addressing review", "rebasing"} {
		if fg := reactingStyle(y).GetForeground(); fg != lipgloss.Color("11") {
			t.Errorf("reactingStyle(%q) foreground = %v, want yellow (11)", y, fg)
		}
	}
	for _, none := range []string{"", "awaiting review"} {
		if fg := reactingStyle(none).GetForeground(); fg != (lipgloss.NoColor{}) {
			t.Errorf("reactingStyle(%q) foreground = %v, want none", none, fg)
		}
	}
}

// P3: an escalated session surfaces its posture in the table (a REACTING column
// entry) and the detail card, colored so it stands out; the raw review decision
// moves to the detail card.
func TestSessionsRenderReactingLabel(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{{
		ID: "s1", Project: "web", Issue: "ENG-9", Status: "ci_failed", Source: "native",
		CIRetries: 2, Escalated: true, Reacting: "escalated", Review: "REVIEW_REQUIRED",
	}}}
	m.sessions.cursor = 0

	v := m.View()
	if !strings.Contains(v, "REACTING") {
		t.Errorf("sessions table must carry a REACTING column:\n%s", v)
	}
	if !strings.Contains(v, "escalated") {
		t.Errorf("escalated posture must render in the view:\n%s", v)
	}
	// The reacting cell for an escalated session is styled red so it stands out.
	if fg := reactingStyle(m.sessions.data.Sessions[0].Reacting).GetForeground(); fg != lipgloss.Color("9") {
		t.Errorf("escalated cell foreground = %v, want red (9)", fg)
	}
	// No tmux → detail card; it carries the reacting line and the raw review.
	for _, want := range []string{"reacting:", "review:", "REVIEW_REQUIRED"} {
		if !strings.Contains(v, want) {
			t.Errorf("detail card missing %q:\n%s", want, v)
		}
	}
}

// P2: the detail pane shows which backend spawned the session and, for
// native sessions, the worktree directory.
func TestSessionDetailSourceBadgeAndWorktree(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.sessions.data = cannedSessions()

	m.sessions.cursor = 0 // s1: native, tmux-backed → preview header
	v := m.View()
	if !strings.Contains(v, "[native]") {
		t.Errorf("native session detail must carry a [native] badge:\n%s", v)
	}
	if !strings.Contains(v, "/tmp/lola/worktrees/web/lola-web-eng-123") {
		t.Errorf("native session detail must show the worktree dir:\n%s", v)
	}

	m.sessions.cursor = 1 // s2: no source (pre-P2 / AO bridge) → [ao]
	v = m.View()
	if !strings.Contains(v, "[ao]") {
		t.Errorf("ao session detail must carry an [ao] badge:\n%s", v)
	}
	if strings.Contains(v, "[native]") {
		t.Errorf("ao session detail must not carry a [native] badge:\n%s", v)
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

// The sessions tab kills the selected session behind a y/n confirm, mirroring
// the polls-tab delete. Force is never offered here (CLI-only friction).
func TestSessionsKillConfirmAndSend(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.sessions.data = cannedSessions()
	m.sessions.cursor = 0 // s1

	// "x" opens the confirm; the footer already advertises the shortcut.
	m.Update(keyMsg("x"))
	if !m.sessions.confirmKill {
		t.Fatal("x must open the kill confirmation")
	}
	v := m.View()
	if !strings.Contains(v, `kill session "s1"? (y/n)`) {
		t.Errorf("view must prompt to confirm the kill:\n%s", v)
	}
	if !strings.Contains(v, "x kill") {
		t.Errorf("sessions footer must advertise 'x kill':\n%s", v)
	}
	if strings.Contains(v, "force") || strings.Contains(v, "--force") {
		t.Errorf("the TUI must never offer a force path:\n%s", v)
	}

	// "y" confirms: the confirmation closes and a kill command is dispatched.
	_, cmd := m.Update(keyMsg("y"))
	if m.sessions.confirmKill {
		t.Error("y must close the kill confirmation")
	}
	if cmd == nil {
		t.Fatal("y must dispatch a kill command")
	}

	// A kill outcome flashes verbatim (here: the daemon's dirty-kept message).
	dirty := `session s1 terminated; worktree kept (uncommitted changes) at /x — rerun with --force to remove it`
	m.Update(killDoneMsg{msg: dirty})
	if m.sessions.flash != dirty {
		t.Errorf("flash = %q, want the verbatim kill message", m.sessions.flash)
	}
	if !strings.Contains(m.View(), "--force") {
		t.Errorf("the dirty-kept message must render in the view:\n%s", m.View())
	}
}

// "n" (or any non-y key) at the confirm cancels without dispatching a kill.
func TestSessionsKillConfirmCancel(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.sessions.data = cannedSessions()
	m.sessions.cursor = 0

	m.Update(keyMsg("x"))
	_, cmd := m.Update(keyMsg("n"))
	if m.sessions.confirmKill {
		t.Error("n must close the kill confirmation")
	}
	if cmd != nil {
		t.Error("n must not dispatch a kill command")
	}
}

// A background refresh between "x" and "y" must NOT retarget the kill: the
// target is pinned to the session ID captured when "x" was pressed, even if the
// list is reordered/pruned and the cursor now points at a different session.
func TestSessionsKillTargetPinnedAcrossRefresh(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.sessions.data = cannedSessions()
	m.sessions.cursor = 0 // s1

	m.Update(keyMsg("x"))
	if m.sessions.killTarget != "s1" {
		t.Fatalf("x must pin the kill target to s1, got %q", m.sessions.killTarget)
	}

	// A refresh arrives mid-confirm with a new session sorted ahead of s1, so
	// the fixed cursor (0) now selects a DIFFERENT session.
	reordered := &protocol.SessionsData{Sessions: []protocol.SessionInfo{
		{ID: "s3", Project: "aaa", Issue: "ENG-1", Status: "working", Source: "native"},
		m.sessions.data.Sessions[0], // s1, now at index 1
	}}
	m.Update(sessionsMsg{data: reordered})
	if sel := m.sessions.selected(); sel == nil || sel.ID != "s3" {
		t.Fatalf("cursor should now point at s3 after the reshuffle, got %v", sel)
	}

	// The pinned target — and the prompt — must still reference s1.
	if m.sessions.killTarget != "s1" {
		t.Errorf("kill target must stay pinned to s1, got %q", m.sessions.killTarget)
	}
	if v := m.View(); !strings.Contains(v, `kill session "s1"? (y/n)`) {
		t.Errorf("prompt must still name s1, not the reshuffled cursor:\n%s", v)
	}

	// "y" confirms against the pinned s1 and resets the target.
	_, cmd := m.Update(keyMsg("y"))
	if cmd == nil {
		t.Fatal("y must dispatch a kill command")
	}
	if m.sessions.killTarget != "" {
		t.Errorf("kill target must reset after confirm, got %q", m.sessions.killTarget)
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
