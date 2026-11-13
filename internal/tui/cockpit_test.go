package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/state"
)

// Every PRIMARY pill shows its word as a padded chip (leading/trailing space) so
// the STATUS column aligns regardless of fill, and the six Display values render
// six distinct labels — the pill is the agent axis, so it must never collapse two
// agent states into one word. (Color/fill is a runtime concern: lipgloss renders
// without SGR under the no-TTY test profile.)
func TestDisplayPill(t *testing.T) {
	seen := map[string]state.Display{}
	for _, d := range state.AllDisplays() {
		p := stripANSI(displayPill(d, ""))
		if !strings.Contains(p, displayLabel(d)) {
			t.Errorf("pill for %q missing its label: %q", d, p)
		}
		if !strings.HasPrefix(p, " ") || !strings.HasSuffix(p, " ") {
			t.Errorf("pill for %q must be a padded chip: %q", d, p)
		}
		if prev, dup := seen[p]; dup {
			t.Errorf("pill for %q renders identically to %q: %q", d, prev, p)
		}
		seen[p] = d
	}
}

// The needs_you pill carries WHY it is blocked, which is what makes it
// actionable rather than merely alarming; no other pill takes a reason.
func TestDisplayPillCarriesTheInputReason(t *testing.T) {
	cases := map[string]string{
		"permission_prompt": "permission",
		"question":          "question",
		"dialog":            "dialog",
		"quota_limited":     "usage limit",
	}
	for reason, want := range cases {
		got := stripANSI(displayPill(state.DisplayNeedsYou, reason))
		if !strings.Contains(got, "needs you: "+want) {
			t.Errorf("needs_you pill for reason %q = %q, want the %q qualifier", reason, got, want)
		}
	}
	// No reason on the record (a legacy pane-derived block): the bare pill.
	if got := stripANSI(displayPill(state.DisplayNeedsYou, "")); !strings.Contains(got, "needs you") || strings.Contains(got, ":") {
		t.Errorf("reasonless needs_you pill = %q, want a bare label", got)
	}
	// A reason on any other state is ignored — only needs_you is blocked on one.
	if got := stripANSI(displayPill(state.DisplayWorking, "permission_prompt")); strings.Contains(got, "permission") {
		t.Errorf("working pill must not carry an input reason: %q", got)
	}
}

// The whole point of the split, end to end: the STATUS column shows what the
// AGENT is doing and the PR column shows where the DELIVERY stands, at the same
// time, on the same row. Under the old rollup each of these rows collapsed to
// the delivery word and the agent axis was simply not on screen.
func TestSessionsBodyShowsBothAxes(t *testing.T) {
	m := newTestRoot(t)
	m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{
		// A live agent typing away while its build is red: the row must say both.
		{ID: "1", Issue: "ENG-1", Project: "web", Status: "ci_failed",
			AgentState: "working", Delivery: "ci_failed", PRNumber: 11, Checks: "fail"},
		// A finished turn parked on a reviewer — the case the 60s idle nudge used
		// to mint as needs_input 90% of the time.
		{ID: "2", Issue: "ENG-2", Project: "web", Status: "review_pending",
			AgentState: "idle", Delivery: "review_pending", PRNumber: 12, Review: "REVIEW_REQUIRED"},
		// An agent that exited over an open PR: the rollup showed the PR only.
		{ID: "3", Issue: "ENG-3", Project: "web", Status: "ci_pending",
			AgentState: "exited", Delivery: "ci_pending", PRNumber: 13, Checks: "pending"},
		// Blocked on a human, and the pill says what for.
		{ID: "4", Issue: "ENG-4", Project: "web", Status: "needs_input",
			AgentState: "waiting_input", InputReason: "permission_prompt", Delivery: "none"},
	}}
	m.sessions.selID = "1"

	body := stripANSI(strings.Join(m.sessionsBody(140, 12), "\n"))
	for _, want := range []string{
		"working", "#11", "✗ci", // agent live, build red
		"idle", "#12", "⧗rev", // turn done, parked on a reviewer
		"gone", "#13", "⧗", // agent exited, CI still running
		"needs you: permission", // blocked, and why
	} {
		if !strings.Contains(body, want) {
			t.Errorf("sessions body must carry %q:\n%s", want, body)
		}
	}
	// The "!" queue marker belongs to the blocked agent alone — a red build is
	// flagged by its own chip, not by borrowing the prompt queue.
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "ENG-1") && strings.Contains(line, "!") {
			t.Errorf("a working agent under a red build must not carry the ! marker:\n%s", line)
		}
	}
	if !strings.Contains(body, "! ENG-4") && !strings.Contains(body, "!  ENG-4") {
		t.Errorf("the blocked session must carry the ! marker:\n%s", body)
	}
}

// The Linear title rides an adaptive column: visible when the panel is wide,
// dropped (never clipping the state columns) when it is narrow.
func TestSessionsTitleColumn(t *testing.T) {
	m := newTestRoot(t)
	m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{
		{ID: "1", Issue: "ENG-1", Title: "Fix the audit table migration", Project: "web", Status: "working"},
	}}
	m.sessions.selID = "1"

	wide := stripANSI(strings.Join(m.sessionsBody(120, 12), "\n"))
	if !strings.Contains(wide, "TITLE") || !strings.Contains(wide, "Fix the audit table") {
		t.Errorf("wide body must carry a TITLE column with the issue title:\n%s", wide)
	}

	// A modest width still shows the column with an ellipsized short title rather
	// than dropping it — the state columns keep their width, the title takes only
	// the leftover.
	mid := stripANSI(strings.Join(m.sessionsBody(64, 12), "\n"))
	if !strings.Contains(mid, "TITLE") {
		t.Errorf("modest-width body must still carry the TITLE column:\n%s", mid)
	}
	if !strings.Contains(mid, "…") || strings.Contains(mid, "migration") {
		t.Errorf("modest-width title must be ellipsized (not the full title):\n%s", mid)
	}

	narrow := stripANSI(strings.Join(m.sessionsBody(46, 12), "\n"))
	if strings.Contains(narrow, "TITLE") {
		t.Errorf("narrow body must drop the TITLE column:\n%s", narrow)
	}
	if !strings.Contains(narrow, "STATUS") {
		t.Errorf("narrow body must keep the state columns:\n%s", narrow)
	}
}

// The rail carries a dedicated Activity panel and renders the feed's events
// inside the full cockpit frame (not just the isolated body helper).
func TestCockpitRailShowsActivity(t *testing.T) {
	m := newTestRoot(t)
	m.sessions.data = &protocol.SessionsData{
		Sessions: []protocol.SessionInfo{{ID: "1", Issue: "ENG-1", Status: "working"}},
		Events:   []protocol.Event{{Issue: "ENG-1", From: "working", To: "needs_input", Ago: "1m"}},
	}
	frame := stripANSI(strings.Join(m.cockpitLines(), "\n"))
	if !strings.Contains(frame, "Activity") {
		t.Errorf("cockpit frame must carry the Activity panel:\n%s", frame)
	}
	if !strings.Contains(frame, "needs you") {
		t.Errorf("cockpit frame must render the activity event:\n%s", frame)
	}
}

// eventPhrase reads a spawn as "spawned", a resume out of needs_input as
// "resumed", maps known statuses to short phrases, and falls back to the raw
// word for anything unmapped.
func TestEventPhrase(t *testing.T) {
	cases := []struct{ from, to, want string }{
		{"", "working", "spawned"},
		{"needs_input", "working", "resumed"},
		{"working", "needs_input", "needs you"},
		{"working", "ci_failed", "CI failed"},
		{"ci_failed", "merged", "merged"},
		{"working", "somethingelse", "somethingelse"},
	}
	for _, c := range cases {
		if got := eventPhrase(c.from, c.to); got != c.want {
			t.Errorf("eventPhrase(%q,%q) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}

// activityBody renders one "ISSUE phrase age" line per event, newest first,
// clipped to width, and says so when the feed is empty.
func TestActivityBody(t *testing.T) {
	m := newTestRoot(t)

	empty := stripANSI(strings.Join(m.activityBody(24, 6), "\n"))
	if !strings.Contains(empty, "no activity") {
		t.Errorf("empty feed must say so, got %q", empty)
	}

	m.sessions.data = &protocol.SessionsData{Events: []protocol.Event{
		{Issue: "ENG-9", From: "working", To: "needs_input", Ago: "2m"},
		{Issue: "ENG-7", From: "", To: "working", Ago: "5m"},
	}}
	body := m.activityBody(24, 6)
	flat := stripANSI(strings.Join(body, "\n"))
	if !strings.Contains(flat, "ENG-9") || !strings.Contains(flat, "needs you") || !strings.Contains(flat, "2m") {
		t.Errorf("feed must render the newest event line, got %q", flat)
	}
	if !strings.Contains(flat, "spawned") {
		t.Errorf("feed must render the spawn event, got %q", flat)
	}
	// Height clamps the number of lines shown (freshest win).
	if got := m.activityBody(24, 1); len(got) != 1 {
		t.Errorf("height 1 must clamp to a single line, got %d", len(got))
	}
	// Every line is width-clipped so a long title can't smear the rail.
	for _, ln := range body {
		if w := lipgloss.Width(ln); w > 24 {
			t.Errorf("line exceeds width 24: %d (%q)", w, stripANSI(ln))
		}
	}
}

// The project rail used to hard-truncate to the first h rows, so a project past
// the panel height vanished and the cursor could sit on a row nothing on screen
// reflected. It windows around the cursor now, and says how many are hidden.
func TestProjectRailWindowsAroundCursorInsteadOfTruncating(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.Projects = nil
	for i := 0; i < 12; i++ {
		m.cfg.Projects = append(m.cfg.Projects, config.Project{
			Name: fmt.Sprintf("proj-%02d", i), Path: "/tmp/p", DefaultBranch: "main",
		})
	}
	m.list = newListModel(m.cfg)
	m.focus = focusPolls

	const h = 5
	// Cursor near the END: the old truncation showed rows 0..4 and the selected
	// project was simply not on screen.
	m.list.cursor = 11
	out := stripANSI(strings.Join(m.projectRailBody(30, h), "\n"))
	if !strings.Contains(out, "proj-11") {
		t.Errorf("the selected project must be visible:\n%s", out)
	}
	if strings.Contains(out, "proj-00") {
		t.Errorf("the window must have scrolled past the first rows:\n%s", out)
	}
	if !strings.Contains(out, "more") {
		t.Errorf("hidden rows must be announced:\n%s", out)
	}

	// Cursor at the TOP still shows the first project.
	m.list.cursor = 0
	out = stripANSI(strings.Join(m.projectRailBody(30, h), "\n"))
	if !strings.Contains(out, "proj-00") {
		t.Errorf("cursor at the top must show the first project:\n%s", out)
	}

	// It never renders more lines than it was given.
	if got := len(m.projectRailBody(30, h)); got != h {
		t.Errorf("rail rendered %d lines, want exactly %d", got, h)
	}
}
