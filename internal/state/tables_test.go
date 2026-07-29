package state

import (
	"slices"
	"testing"
)

func TestHoldsSlot(t *testing.T) {
	holds := []string{"working", "needs_input", "draft", "ci_failed",
		"changes_requested", "ci_pending", "merge_conflict"}
	for _, s := range holds {
		if !HoldsSlot(s) {
			t.Errorf("HoldsSlot(%q) = false, want true", s)
		}
	}
	free := []string{"approved", "review_pending", "merged", "closed", "idle",
		"dead", "session_ended", "shell", "orphaned", ""}
	for _, s := range free {
		if HoldsSlot(s) {
			t.Errorf("HoldsSlot(%q) = true, want false", s)
		}
	}
}

func TestPresent(t *testing.T) {
	gone := []string{"dead", "session_ended", "closed"}
	for _, s := range gone {
		if Present(s) {
			t.Errorf("Present(%q) = true, want false", s)
		}
	}
	present := []string{"working", "needs_input", "idle", "ci_failed",
		"merged", "orphaned", "shell", "draft"}
	for _, s := range present {
		if !Present(s) {
			t.Errorf("Present(%q) = false, want true", s)
		}
	}
}

func TestNeedsAttention(t *testing.T) {
	want := []string{"needs_input", "ci_failed", "changes_requested", "merge_conflict"}
	for _, s := range AllStatuses() {
		if got := NeedsAttention(s); got != slices.Contains(want, s) {
			t.Errorf("NeedsAttention(%q) = %v", s, got)
		}
	}
}

func TestNotable(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{"", "working", true},           // spawn
		{"", "", false},                 // empty
		{"working", "idle", false},      // turn churn
		{"ci_pending", "idle", false},   // post-PR churn
		{"working", "orphaned", false},  // adoption anomaly, not feed-worthy
		{"idle", "working", false},      // routine turn start
		{"needs_input", "working", true},// resumed after waiting on a human
		{"working", "needs_input", true},
		{"ci_pending", "ci_failed", true},
		{"review_pending", "merged", true},
		{"working", "closed", true},
	}
	for _, c := range cases {
		if got := Notable(c.from, c.to); got != c.want {
			t.Errorf("Notable(%q, %q) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestSortRank(t *testing.T) {
	cases := map[string]int{
		"needs_input":       0,
		"ci_failed":         1,
		"changes_requested": 1,
		"merge_conflict":    1,
		"working":           2,
		"ci_pending":        2,
		"draft":             2,
		"review_pending":    3,
		"approved":          3,
		"idle":              4,
		"shell":             4,
		"orphaned":          4,
		"totally-unknown":   4,
		"merged":            5,
		"dead":              5,
		"session_ended":     5,
		"closed":            5,
	}
	for s, want := range cases {
		if got := SortRank(s); got != want {
			t.Errorf("SortRank(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestKanban(t *testing.T) {
	cases := map[string]string{
		"needs_input":       "needs",
		"working":           "working",
		"ci_pending":        "working",
		"idle":              "working",
		"draft":             "working",
		"ci_failed":         "fixing",
		"changes_requested": "fixing",
		"merge_conflict":    "fixing",
		"review_pending":    "review",
		"approved":          "review",
		"merged":            "done",
		"closed":            "done",
		"dead":              "done",
		"session_ended":     "done",
		"shell":             KanbanFallbackKey,
		"orphaned":          KanbanFallbackKey,
		"unknown-word":      KanbanFallbackKey,
	}
	for s, want := range cases {
		if got := KanbanKeyFor(s); got != want {
			t.Errorf("KanbanKeyFor(%q) = %q, want %q", s, got, want)
		}
	}
	// Every column key referenced above must exist.
	keys := map[string]bool{}
	for _, col := range KanbanColumns() {
		keys[col.Key] = true
	}
	for _, want := range cases {
		if !keys[want] {
			t.Errorf("column key %q not in KanbanColumns()", want)
		}
	}
}

// TestVocabularyClosed: every string any table classifies specially must be
// in AllStatuses, and the dead pre-axis words must be gone for good.
func TestVocabularyClosed(t *testing.T) {
	all := AllStatuses()
	for _, col := range KanbanColumns() {
		for _, s := range col.Statuses {
			if !slices.Contains(all, s) {
				t.Errorf("kanban status %q missing from AllStatuses", s)
			}
		}
	}
	for s := range holdsSlot {
		if !slices.Contains(all, s) {
			t.Errorf("holdsSlot status %q missing from AllStatuses", s)
		}
	}
	for s := range attention {
		if !slices.Contains(all, s) {
			t.Errorf("attention status %q missing from AllStatuses", s)
		}
	}
	for _, dead := range []string{"no_pr", "pr_open", "no_signal", "none"} {
		if slices.Contains(all, dead) {
			t.Errorf("dead vocabulary %q resurrected in AllStatuses", dead)
		}
	}
	// And the rollup can actually produce every listed status.
	produced := map[string]bool{}
	for _, a := range allAgentStates {
		for _, d := range allDeliveryStates {
			produced[Rollup(a, d)] = true
		}
	}
	for _, s := range all {
		if !produced[s] {
			t.Errorf("AllStatuses lists %q but Rollup never produces it", s)
		}
	}
}
