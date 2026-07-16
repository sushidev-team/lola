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

// sparkline is right-aligned to the last `width` samples, one glyph per sample,
// and renders nothing for empty history.
func TestSparkline(t *testing.T) {
	if got := sparkline(nil, 10); got != "" {
		t.Errorf("empty history must render nothing, got %q", got)
	}
	if got := sparkline([]int{1, 2, 3}, 0); got != "" {
		t.Errorf("zero width must render nothing, got %q", got)
	}
	// 6 samples into a width-4 window keeps the last 4 (one visible column each).
	got := sparkline([]int{0, 1, 2, 3, 4, 5}, 4)
	if w := lipgloss.Width(got); w != 4 {
		t.Errorf("sparkline width = %d, want 4 (%q)", w, stripANSI(got))
	}
	// A zero sample is a faint dot; positive samples are block glyphs.
	if !strings.Contains(sparkline([]int{0}, 4), "·") {
		t.Errorf("zero sample must render a dot")
	}
}

// recordAttn pushes one sample per call and never grows past the ring cap.
func TestRecordAttnRing(t *testing.T) {
	m := newTestRoot(t)
	m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{
		{Status: "needs_input"}, {Status: "working"},
	}}
	for i := 0; i < attnHistCap+10; i++ {
		m.recordAttn()
	}
	if len(m.attnHist) != attnHistCap {
		t.Errorf("history len = %d, want cap %d", len(m.attnHist), attnHistCap)
	}
	if last := m.attnHist[len(m.attnHist)-1]; last != 1 {
		t.Errorf("last sample = %d, want 1 (one needs_input)", last)
	}
}
