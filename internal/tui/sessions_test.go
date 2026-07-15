package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

// keyMsg builds a bubbletea v2 key-press for tests: named keys map to their
// KeyCode; anything else is treated as printable text (Code = first rune, Text
// = the whole string) so k.String() and k.Text both read correctly.
func keyMsg(s string) tea.KeyPressMsg {
	switch s {
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "space":
		return tea.KeyPressMsg{Code: ' ', Text: " "}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

// The cockpit shows sessions and polls together on one screen; tab cycles which
// panel owns navigation/action keys (focusSessions ⇄ focusPolls).
func TestCockpitRendersAndTabCyclesFocus(t *testing.T) {
	m := newTestRoot(t)
	m.Update(sessionsMsg{data: cannedSessions()})

	v := m.viewString()
	for _, want := range []string{"ISSUE", "PROJECT", "STATUS", "REACTING", "ENG-123", "ENG-456", "web", "api", "#7", "2h05m", "ci retry 1/2"} {
		if !strings.Contains(v, want) {
			t.Errorf("cockpit missing sessions content %q:\n%s", want, v)
		}
	}
	// Sessions are focused by default: the keybar advertises attach.
	if !strings.Contains(v, "enter attach") {
		t.Errorf("default focus keybar must advertise attach:\n%s", v)
	}
	// tab moves focus to the Polls panel: its keybar verbs appear.
	if _, _ = m.Update(keyMsg("tab")); m.focus != focusPolls {
		t.Fatalf("tab: focus = %d, want focusPolls", m.focus)
	}
	if !strings.Contains(m.viewString(), "space toggle") {
		t.Errorf("polls focus keybar must advertise toggle:\n%s", m.viewString())
	}
	// tab again returns focus to Sessions.
	if _, _ = m.Update(keyMsg("tab")); m.focus != focusSessions {
		t.Fatalf("tab: focus = %d, want focusSessions", m.focus)
	}
}

// PR display depends ONLY on PR presence (a number/URL), never on Status or
// review decision: a still-working session that already has a PR shows its PR
// badge in the list row, the prominent panel in the detail card, and on its
// kanban card — with the checks glyph so a passing/failing PR is scannable.
func TestPRShownRegardlessOfStatus(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	// A "working" session (NOT review_pending/approved/etc.) that already has a
	// passing, approved PR — proves display is not state-gated.
	m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{{
		ID: "s1", Project: "web", Issue: "ENG-1", Status: "working", Source: "native",
		PRNumber: 229, PRURL: "https://github.com/x/y/pull/229",
		Checks: "pass", Review: "APPROVED",
	}}}
	m.sessions.cursor = 0

	// List row: "#229" with the passing-checks glyph.
	v := m.viewString()
	if !strings.Contains(v, "#229") {
		t.Errorf("list row must show the PR number for a working session:\n%s", v)
	}
	if !strings.Contains(v, "✓") {
		t.Errorf("list row PR badge must carry a checks glyph:\n%s", v)
	}
	// Detail card (tmux-backed → preview variant): prominent PR panel + URL.
	for _, want := range []string{"PR: #229", "checks: pass", "review: APPROVED", "https://github.com/x/y/pull/229"} {
		if !strings.Contains(v, want) {
			t.Errorf("detail card missing prominent PR field %q:\n%s", want, v)
		}
	}
	// The footer advertises "o PR" because a PR exists.
	if !strings.Contains(v, "o PR") {
		t.Errorf("footer must advertise 'o PR' when a PR exists:\n%s", v)
	}

	// Kanban lens: the card carries "#229" with the checks glyph too.
	m.sessions.view = viewKanban
	kb := m.viewString()
	if !strings.Contains(kb, "#229") || !strings.Contains(kb, "✓") {
		t.Errorf("kanban card must show the PR badge with a checks glyph:\n%s", kb)
	}
}

// The checks glyph encodes CI state compactly and the badge is never gated on
// status/review presence — a failing PR reads "#7 ✗ci", a pending one "#7 ⧗".
func TestPRBadgeChecksGlyphs(t *testing.T) {
	cases := []struct {
		checks string
		want   string
	}{
		{"pass", "✓"},
		{"fail", "✗ci"},
		{"pending", "⧗"},
	}
	for _, c := range cases {
		got := prBadge(protocol.SessionInfo{PRNumber: 7, Checks: c.checks})
		if !strings.Contains(got, "#7") || !strings.Contains(got, c.want) {
			t.Errorf("prBadge(checks=%q) = %q, want #7 + %q", c.checks, got, c.want)
		}
	}
	// No PR → empty badge (callers render a placeholder / omit it).
	if got := prBadge(protocol.SessionInfo{}); got != "" {
		t.Errorf("prBadge(no PR) = %q, want empty", got)
	}
	// A PR with only a URL (no number) still renders.
	if got := prBadge(protocol.SessionInfo{PRURL: "https://x/y/pull/1"}); got == "" {
		t.Error("prBadge with a URL but no number must still render a badge")
	}
}

func TestStatusStyleMapping(t *testing.T) {
	cases := []struct {
		status string
		fg     color.Color
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

	v := m.viewString()
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
	v := m.viewString()
	if !strings.Contains(v, "[native]") {
		t.Errorf("native session detail must carry a [native] badge:\n%s", v)
	}
	if !strings.Contains(v, "/tmp/lola/worktrees/web/lola-web-eng-123") {
		t.Errorf("native session detail must show the worktree dir:\n%s", v)
	}

	m.sessions.cursor = 1 // s2: no source (pre-P2 / AO bridge) → [ao]
	v = m.viewString()
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
	// Pre-attach probe confirms a live pane on the lola server (seamed so the
	// test never touches a real tmux server).
	m.sessions.hasPane = func(string) bool { return true }

	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter on a tmux-backed session must return an exec command")
	}
	if m.sessions.flash != "" {
		t.Errorf("flash = %q, want empty", m.sessions.flash)
	}
	// The attach client targets the configured isolated server socket.
	if got := m.sessions.tmuxClient("lola").SocketName; got != "lola" {
		t.Errorf("attach client socket = %q, want lola", got)
	}
}

// The pre-attach liveness gate refuses a dead/ended session clearly and never
// execs a doomed attach; the has-session probe is not even consulted.
func TestAttachRefusesDeadSession(t *testing.T) {
	for _, status := range []string{"dead", "session_ended"} {
		m := newTestRoot(t)
		m.tab = tabSessions
		m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{{
			ID: "s1", Project: "web", Issue: "ENG-1", Status: status,
			TmuxName: "lola-eng-1", Source: "native",
		}}}
		m.sessions.cursor = 0
		probed := false
		m.sessions.hasPane = func(string) bool { probed = true; return true }

		_, cmd := m.Update(keyMsg("enter"))
		if cmd != nil {
			t.Errorf("%s: enter must not exec an attach for a dead session", status)
		}
		if probed {
			t.Errorf("%s: a dead session must not even probe has-session", status)
		}
		if !strings.Contains(m.sessions.flash, "nothing to attach") {
			t.Errorf("%s: flash = %q, want a clear 'nothing to attach' hint", status, m.sessions.flash)
		}
	}
}

// The pre-attach gate refuses when the pane is not live on the lola server
// (e.g. an orphaned pre-migration session on the DEFAULT server) and does not
// exec the doomed attach.
func TestAttachRefusesNoLivePane(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.sessions.data = cannedSessions()
	m.sessions.cursor = 0 // s1: working, tmux-backed
	m.sessions.hasPane = func(string) bool { return false }

	_, cmd := m.Update(keyMsg("enter"))
	if cmd != nil {
		t.Error("enter must not exec an attach when no live pane exists on the lola server")
	}
	if !strings.Contains(m.sessions.flash, "no live tmux pane") {
		t.Errorf("flash = %q, want a 'no live tmux pane' hint", m.sessions.flash)
	}
}

// The attachDoneMsg error flash is the final backstop and must survive a 5s
// refresh tick (a fetch that returns fresh sessions) so the user still sees it;
// only the next keypress clears it.
func TestAttachErrorFlashPersistsAcrossTick(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.sessions.data = cannedSessions()
	m.sessions.cursor = 0

	m.Update(attachDoneMsg{err: errors.New("boom")})
	if !strings.Contains(m.sessions.flash, "attach failed") {
		t.Fatalf("flash = %q, want an attach-failed backstop", m.sessions.flash)
	}
	// A refresh delivering fresh sessions (as the 5s tick would) must not clear it.
	m.Update(sessionsMsg{data: cannedSessions()})
	if !strings.Contains(m.sessions.flash, "attach failed") {
		t.Errorf("attach-failed flash was cleared by a refresh tick, want it to persist: %q", m.sessions.flash)
	}
	// The next keypress clears it.
	m.Update(keyMsg("j"))
	if m.sessions.flash != "" {
		t.Errorf("flash = %q, want it cleared on the next keypress", m.sessions.flash)
	}
}

// The pre-attach hint names the issue and the resolved detach key so the user
// knows how to get back before tmux takes over the terminal.
func TestAttachHintLine(t *testing.T) {
	if got, want := attachHintLine("ENG-123", "Ctrl-b d"),
		"attaching to ENG-123 — press Ctrl-b d to return to Lola"; got != want {
		t.Errorf("attachHintLine = %q, want %q", got, want)
	}
	// A bound single-key detach flows through as the hint key verbatim.
	if got, want := attachHintLine("ENG-9", "F12"),
		"attaching to ENG-9 — press F12 to return to Lola"; got != want {
		t.Errorf("attachHintLine custom key = %q, want %q", got, want)
	}
	// A blank issue still reads sensibly.
	if got := attachHintLine("", "Ctrl-b d"); !strings.Contains(got, "attaching to session") {
		t.Errorf("attachHintLine blank issue = %q, want a 'session' fallback", got)
	}
}

// The resolved detach hint comes from the [tmux] config: a custom detach_key
// becomes the hint key so the pre-attach line matches whatever actually detaches.
func TestAttachHintUsesResolvedDetachKey(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.Tmux.DetachKey = "F12"
	if got := attachHintLine("ENG-123", m.cfg.Tmux.DetachHint()); !strings.Contains(got, "press F12") {
		t.Errorf("hint = %q, want the configured F12 detach key", got)
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
	// Before confirming, the sessions keybar advertises the kill shortcut.
	if !strings.Contains(m.viewString(), "x kill") {
		t.Errorf("sessions footer must advertise 'x kill':\n%s", m.viewString())
	}
	m.Update(keyMsg("x"))
	if !m.sessions.confirmKill {
		t.Fatal("x must open the kill confirmation")
	}
	v := m.viewString()
	if !strings.Contains(v, `kill session "s1"? (y/n)`) {
		t.Errorf("view must prompt to confirm the kill:\n%s", v)
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
	if !strings.Contains(m.viewString(), "--force") {
		t.Errorf("the dirty-kept message must render in the view:\n%s", m.viewString())
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
	if v := m.viewString(); !strings.Contains(v, `kill session "s1"? (y/n)`) {
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

	v := m.viewString()
	if !strings.Contains(v, "daemon: not running") {
		t.Errorf("view must hint that the daemon is down:\n%s", v)
	}
}

func TestSessionDetailCardWithoutTmux(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.sessions.data = cannedSessions()
	m.sessions.cursor = 1 // no tmux -> detail card

	v := m.viewString()
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

	if v := m.viewString(); !strings.Contains(v, "(no preview yet)") {
		t.Errorf("preview pane must show placeholder before a capture arrives:\n%s", v)
	}

	m.Update(paneMsg{id: "s1", data: &protocol.PaneData{Text: "line1\nline2\n"}})
	v := m.viewString()
	if !strings.Contains(v, "line1") || !strings.Contains(v, "line2") {
		t.Errorf("preview pane must show capture output:\n%s", v)
	}

	// A stale capture for a no-longer-selected session is dropped.
	m.sessions.cursor = 1
	m.Update(paneMsg{id: "s1", data: &protocol.PaneData{Text: "stale"}})
	if strings.Contains(m.viewString(), "stale") {
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

// fakeRequest installs a canned requestFn for the pane/answer round-trips and
// restores the real one on cleanup. capture, when non-nil, records every
// request the model issued so a test can assert the exact cmd/text sent.
func fakeRequest(t *testing.T, capture *[]protocol.Request, resp *protocol.Response, err error) {
	t.Helper()
	prev := requestFn
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		if capture != nil {
			*capture = append(*capture, req)
		}
		return resp, err
	}
	t.Cleanup(func() { requestFn = prev })
}

// mustData marshals v into a Response.Data payload for a fake OK response.
func mustData(t *testing.T, v any) *protocol.Response {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return &protocol.Response{OK: true, Data: raw}
}

// needsInputRoot builds a sessions tab holding one needs_input session (s9,
// tmux-backed) with the given parsed pane already loaded as if a cmd=pane
// refresh had returned it.
func needsInputRoot(t *testing.T, pd *protocol.PaneData) *rootModel {
	t.Helper()
	m := newTestRoot(t)
	m.tab = tabSessions
	m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{{
		ID: "s9", Project: "web", Issue: "ENG-9", Status: "needs_input",
		TmuxName: "lola-eng-9", Source: "native",
	}}}
	m.sessions.cursor = 0
	m.sessions.preview, m.sessions.previewFor, m.sessions.paneData = pd.Text, "s9", pd
	return m
}

// A needs_input session renders the parsed question and its choices as an
// actionable card with the "a: answer" affordance.
func TestNeedsInputRendersQuestionAndChoices(t *testing.T) {
	pd := &protocol.PaneData{
		Text:        "Overwrite the file?\n1. Yes\n2. No\n",
		HasQuestion: true,
		Prompt:      "Overwrite the file?",
		Choices:     []protocol.PaneChoice{{Key: "1", Label: "Yes"}, {Key: "2", Label: "No"}},
	}
	m := needsInputRoot(t, pd)

	v := m.viewString()
	for _, want := range []string{"attention", "Overwrite the file?", "1. Yes", "2. No", "a: answer"} {
		if !strings.Contains(v, want) {
			t.Errorf("needs_input card missing %q:\n%s", want, v)
		}
	}
}

// Selecting a choice (arrow to it, enter) issues a cmd=answer with the chosen
// Key as Text.
func TestAnswerChoiceSendsKey(t *testing.T) {
	pd := &protocol.PaneData{
		Text: "Pick one\n1. Yes\n2. No\n", HasQuestion: true, Prompt: "Pick one",
		Choices: []protocol.PaneChoice{{Key: "1", Label: "Yes"}, {Key: "2", Label: "No"}},
	}
	m := needsInputRoot(t, pd)

	// "a" arms the card; the affordance flips to send/cancel.
	m.Update(keyMsg("a"))
	if !m.sessions.answering {
		t.Fatal("a must open the answer card on a needs_input session")
	}
	if !strings.Contains(m.viewString(), "enter send") {
		t.Errorf("open card must advertise send/cancel:\n%s", m.viewString())
	}

	// Arrow down to the second choice, then enter.
	m.Update(keyMsg("down"))
	if m.sessions.answerChoice != 1 {
		t.Fatalf("answerChoice = %d, want 1 after down", m.sessions.answerChoice)
	}

	var got []protocol.Request
	fakeRequest(t, &got, &protocol.Response{OK: true}, nil)
	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter must dispatch an answer command")
	}
	m.Update(cmd())
	if len(got) != 1 || got[0].Cmd != "answer" || got[0].Session != "s9" || got[0].Text != "2" {
		t.Fatalf("issued request = %+v, want cmd=answer session=s9 text=2", got)
	}
	if m.sessions.answering {
		t.Error("answering must close after send")
	}
}

// Pressing a digit that directly names a choice key sends that choice without
// moving the cursor first.
func TestAnswerChoiceByDigitKey(t *testing.T) {
	pd := &protocol.PaneData{
		Text: "Pick\n1. Yes\n2. No\n", HasQuestion: true, Prompt: "Pick",
		Choices: []protocol.PaneChoice{{Key: "1", Label: "Yes"}, {Key: "2", Label: "No"}},
	}
	m := needsInputRoot(t, pd)
	m.Update(keyMsg("a"))

	var got []protocol.Request
	fakeRequest(t, &got, &protocol.Response{OK: true}, nil)
	_, cmd := m.Update(keyMsg("2"))
	if cmd == nil {
		t.Fatal("a choice-key digit must dispatch an answer")
	}
	m.Update(cmd())
	if len(got) != 1 || got[0].Text != "2" {
		t.Fatalf("issued request = %+v, want text=2", got)
	}
}

// A free-form prompt takes typed text and sends it verbatim on enter.
func TestAnswerFreeFormSendsTypedText(t *testing.T) {
	pd := &protocol.PaneData{
		Text: "What is the branch name?\n> \n", HasQuestion: true,
		Prompt: "What is the branch name?", FreeForm: true,
	}
	m := needsInputRoot(t, pd)
	m.Update(keyMsg("a"))

	for _, r := range "fix-9" {
		m.Update(keyMsg(string(r)))
	}
	m.Update(keyMsg(" ")) // space handled outside KeyRunes
	if m.sessions.answerInput != "fix-9 " {
		t.Fatalf("answerInput = %q, want %q", m.sessions.answerInput, "fix-9 ")
	}
	if !strings.Contains(m.viewString(), "fix-9") {
		t.Errorf("free-form card must echo the typed answer:\n%s", m.viewString())
	}

	var got []protocol.Request
	fakeRequest(t, &got, &protocol.Response{OK: true}, nil)
	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter must dispatch the free-form answer")
	}
	m.Update(cmd())
	if len(got) != 1 || got[0].Cmd != "answer" || got[0].Text != "fix-9 " {
		t.Fatalf("issued request = %+v, want cmd=answer text=%q", got, "fix-9 ")
	}
}

// The daemon's refusal (the agent moved on) is surfaced verbatim as a warning
// flash; a delivered answer flashes green.
func TestAnswerRefusalAndSuccessFlash(t *testing.T) {
	pd := &protocol.PaneData{
		Text: "go?\n(y/n)\n", HasQuestion: true, Prompt: "go?",
		Choices: []protocol.PaneChoice{{Key: "y", Label: "Yes"}, {Key: "n", Label: "No"}},
	}

	// Refusal path.
	m := needsInputRoot(t, pd)
	m.Update(keyMsg("a"))
	refusal := "session s9 is not waiting for input (status working)"
	fakeRequest(t, nil, &protocol.Response{OK: false, Error: refusal}, nil)
	_, cmd := m.Update(keyMsg("enter"))
	m.Update(cmd())
	if m.sessions.flash != refusal || m.sessions.flashGood {
		t.Fatalf("refusal flash = %q good=%v, want the verbatim error as a warning", m.sessions.flash, m.sessions.flashGood)
	}
	if !strings.Contains(m.viewString(), refusal) {
		t.Errorf("refusal must render in the view:\n%s", m.viewString())
	}

	// Success path.
	m2 := needsInputRoot(t, pd)
	m2.Update(keyMsg("a"))
	fakeRequest(t, nil, &protocol.Response{OK: true}, nil)
	_, cmd2 := m2.Update(keyMsg("enter"))
	m2.Update(cmd2())
	if !m2.sessions.flashGood || m2.sessions.flash != "answer sent" {
		t.Fatalf("success flash = %q good=%v, want green 'answer sent'", m2.sessions.flash, m2.sessions.flashGood)
	}
}

// esc cancels the card without issuing any request.
func TestAnswerEscCancels(t *testing.T) {
	pd := &protocol.PaneData{
		Text: "q\n1. a\n2. b\n", HasQuestion: true, Prompt: "q",
		Choices: []protocol.PaneChoice{{Key: "1", Label: "a"}, {Key: "2", Label: "b"}},
	}
	m := needsInputRoot(t, pd)
	m.Update(keyMsg("a"))
	var got []protocol.Request
	fakeRequest(t, &got, &protocol.Response{OK: true}, nil)
	_, cmd := m.Update(keyMsg("esc"))
	if m.sessions.answering {
		t.Error("esc must close the answer card")
	}
	if cmd != nil {
		t.Error("esc must not dispatch a command")
	}
	if len(got) != 0 {
		t.Errorf("esc must issue no request, got %+v", got)
	}
}

// A non-needs_input session shows no answer card and "a" refuses to arm.
func TestNoAnswerAffordanceWhenNotNeedsInput(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.sessions.data = cannedSessions() // s1 is "working"
	m.sessions.cursor = 0
	m.sessions.preview, m.sessions.previewFor = "some pane text", "s1"

	v := m.viewString()
	if strings.Contains(v, "a: answer") || strings.Contains(v, "attention") {
		t.Errorf("a working session must not show an answer card:\n%s", v)
	}

	_, cmd := m.Update(keyMsg("a"))
	if m.sessions.answering {
		t.Error("a must not arm on a non-needs_input session")
	}
	if cmd != nil {
		t.Error("a on a working session must not dispatch a command")
	}
	if !strings.Contains(m.sessions.flash, "waits for input") {
		t.Errorf("flash should explain answer is unavailable, got %q", m.sessions.flash)
	}
}

// A needs_input session whose prompt the parser could NOT shape (HasQuestion
// false) must still be answerable in place: "a" opens a free-form card and the
// typed reply is delivered as cmd=answer. Otherwise the only recourse is to
// attach, defeating the answer-in-place goal.
func TestAnswerFreeFormFallbackOnParseMiss(t *testing.T) {
	pd := &protocol.PaneData{Text: "some prompt the parser did not recognize\n"} // HasQuestion false
	m := needsInputRoot(t, pd)

	// No card is shown before arming (nothing parsed to surface), but "a" must
	// still arm a free-form card rather than flashing a dead-end hint.
	if strings.Contains(m.viewString(), "answer>") {
		t.Errorf("no card should render before arming on a parse miss:\n%s", m.viewString())
	}
	m.Update(keyMsg("a"))
	if !m.sessions.answering {
		t.Fatal("a must arm a free-form card on a needs_input parse miss")
	}
	v := m.viewString()
	if !strings.Contains(v, "prompt not parsed") || !strings.Contains(v, "enter send") {
		t.Errorf("armed parse-miss card must offer a free-form field:\n%s", v)
	}

	for _, r := range "retry" {
		m.Update(keyMsg(string(r)))
	}
	var got []protocol.Request
	fakeRequest(t, &got, &protocol.Response{OK: true}, nil)
	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter must dispatch the free-form fallback answer")
	}
	m.Update(cmd())
	if len(got) != 1 || got[0].Cmd != "answer" || got[0].Session != "s9" || got[0].Text != "retry" {
		t.Fatalf("issued request = %+v, want cmd=answer session=s9 text=retry", got)
	}
}

// The answer card must clamp every line to the terminal width, exactly like the
// preview rows below it: an over-wide choice label or a long typed free-form
// reply that physically wrapped would make bubbletea (alt-screen) miscount
// rendered lines and smear the frame.
func TestAnswerCardClampedToTerminalWidth(t *testing.T) {
	pd := &protocol.PaneData{
		Text: "q\n", HasQuestion: true, Prompt: strings.Repeat("P", 60),
		Choices: []protocol.PaneChoice{{Key: "1", Label: strings.Repeat("L", 60)}},
	}
	m := needsInputRoot(t, pd)
	m.width = 20

	for _, ln := range strings.Split(m.attentionCard(), "\n") {
		if w := lipgloss.Width(ln); w > 20 {
			t.Errorf("card line width = %d, want <= 20 (no wrap):\n%q", w, ln)
		}
	}

	// A long free-form input must clamp too as the human keeps typing.
	ff := &protocol.PaneData{Text: "q\n", HasQuestion: true, Prompt: "name?", FreeForm: true}
	m2 := needsInputRoot(t, ff)
	m2.width = 20
	m2.Update(keyMsg("a"))
	m2.sessions.answerInput = strings.Repeat("z", 80)
	for _, ln := range strings.Split(m2.attentionCard(), "\n") {
		if w := lipgloss.Width(ln); w > 20 {
			t.Errorf("free-form card line width = %d, want <= 20:\n%q", w, ln)
		}
	}
}

// The compact/full toggle ("v") changes how many pane rows render.
func TestCompactToggleChangesLineCount(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{{
		ID: "s1", Project: "web", Issue: "ENG-1", Status: "working",
		TmuxName: "lola-eng-1", Source: "native",
	}}}
	m.sessions.cursor = 0

	// 30 uniquely-numbered pane lines so we can count how many survive.
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("row%02d", i))
	}
	m.sessions.preview, m.sessions.previewFor = strings.Join(lines, "\n"), "s1"

	countRows := func() int {
		v := m.viewString()
		n := 0
		for i := 0; i < 30; i++ {
			if strings.Contains(v, fmt.Sprintf("row%02d", i)) {
				n++
			}
		}
		return n
	}

	// Default is compact.
	if got := countRows(); got != previewLines {
		t.Fatalf("compact rendered %d rows, want %d", got, previewLines)
	}
	// "v" flips to full — the daemon refetch is stubbed to a no-op so the cached
	// preview stays put while paneLines() widens.
	fakeRequest(t, nil, mustData(t, protocol.PaneData{Text: strings.Join(lines, "\n")}), nil)
	_, cmd := m.Update(keyMsg("v"))
	if !m.sessions.full {
		t.Fatal("v must toggle to the full view")
	}
	if cmd != nil {
		m.Update(cmd()) // apply the refetch (same 30 lines)
	}
	if got := countRows(); got != fullPreviewLines {
		t.Fatalf("full rendered %d rows, want %d", got, fullPreviewLines)
	}
}

// "n" jumps the cursor to the next needs_input session.
func TestJumpToNextNeedsInput(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{
		{ID: "a", Status: "working", Source: "native"},
		{ID: "b", Status: "working", Source: "native"},
		{ID: "c", Status: "needs_input", TmuxName: "t-c", Source: "native"},
	}}
	m.sessions.cursor = 0

	fakeRequest(t, nil, mustData(t, protocol.PaneData{}), nil)
	m.Update(keyMsg("n"))
	if m.sessions.cursor != 2 {
		t.Fatalf("n must jump to the needs_input session at index 2, got %d", m.sessions.cursor)
	}

	// The needs_input row is flagged with a "!" marker in the list.
	m.sessions.cursor = 0
	if !strings.Contains(m.viewString(), "!") {
		t.Errorf("needs_input row must carry a '!' flag in the list:\n%s", m.viewString())
	}
}

func TestSessionPreviewClippedToTerminalWidth(t *testing.T) {
	m := newTestRoot(t)
	m.tab = tabSessions
	m.width = 20 // as set by tea.WindowSizeMsg
	m.sessions.data = cannedSessions()
	m.sessions.cursor = 0 // s1 is tmux-backed

	m.Update(paneMsg{id: "s1", data: &protocol.PaneData{Text: strings.Repeat("a", 100) + "TAIL\n"}})
	if strings.Contains(m.viewString(), "TAIL") {
		t.Error("over-width preview line must be clipped to the terminal width")
	}
	if !strings.Contains(m.viewString(), "aaa") {
		t.Error("clipped preview line must still render its head")
	}
}
