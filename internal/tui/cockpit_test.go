package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/protocol"
)

// Every status pill shows its word as a padded chip (leading/trailing space) so
// the STATUS column aligns regardless of fill. (Color/fill is a runtime concern:
// lipgloss renders without SGR under the no-TTY test profile.)
func TestStatusPill(t *testing.T) {
	for _, status := range []string{"needs_input", "ci_failed", "changes_requested", "working", "approved", "review_pending", "merged"} {
		p := stripANSI(statusPill(status))
		if !strings.Contains(p, statusLabel(status)) {
			t.Errorf("pill for %q missing the status label: %q", status, p)
		}
		if !strings.HasPrefix(p, " ") || !strings.HasSuffix(p, " ") {
			t.Errorf("pill for %q must be a padded chip: %q", status, p)
		}
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
