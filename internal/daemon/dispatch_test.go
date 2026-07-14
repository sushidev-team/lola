package daemon

import (
	"reflect"
	"testing"
	"time"

	"github.com/you/aop/internal/linear"
)

func TestBudget(t *testing.T) {
	cases := []struct {
		name                     string
		pollCap, globalCap, live int
		want                     int
	}{
		{"pollCapBinds", 3, 10, 2, 3},
		{"globalHeadroomBinds", 5, 4, 1, 3},
		{"exactlyCappedOut", 5, 4, 4, 0},
		{"liveExceedsGlobal", 5, 4, 9, -5},
		{"zeroPollCap", 0, 10, 0, 0},
		{"negativePollCap", -1, 10, 0, -1},
		{"zeroLive", 2, 8, 0, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Budget(tc.pollCap, tc.globalCap, tc.live); got != tc.want {
				t.Errorf("Budget(%d, %d, %d) = %d, want %d", tc.pollCap, tc.globalCap, tc.live, got, tc.want)
			}
		})
	}
}

func sortIssue(ident string, prio float64, created string) linear.Issue {
	return linear.Issue{ID: "uuid-" + ident, Identifier: ident, Priority: prio, CreatedAt: created}
}

func identifiers(issues []linear.Issue) []string {
	out := make([]string, len(issues))
	for i, is := range issues {
		out[i] = is.Identifier
	}
	return out
}

func TestSortIssues(t *testing.T) {
	input := []linear.Issue{
		sortIssue("FE-3", 4, "2024-01-01T00:00:00Z"), // low priority
		sortIssue("FE-1", 1, "2024-01-02T00:00:00Z"), // urgent, newer
		sortIssue("FE-0", 0, "2023-01-01T00:00:00Z"), // none: sorts LAST despite oldest createdAt
		sortIssue("FE-9", 2, "2024-01-01T00:00:00Z"), // equal keys with FE-8
		sortIssue("FE-2", 1, "2024-01-01T00:00:00Z"), // urgent, older -> before FE-1
		sortIssue("FE-8", 2, "2024-01-01T00:00:00Z"), // equal keys with FE-9 -> identifier tiebreak
	}
	want := []string{"FE-2", "FE-1", "FE-8", "FE-9", "FE-3", "FE-0"}

	got := make([]linear.Issue, len(input))
	copy(got, input)
	SortIssues(got, nil) // nil -> default ["priority","createdAt"]
	if !reflect.DeepEqual(identifiers(got), want) {
		t.Errorf("SortIssues order = %v, want %v", identifiers(got), want)
	}

	// Deterministic: a reversed input permutation yields the same order.
	rev := make([]linear.Issue, len(input))
	for i, is := range input {
		rev[len(input)-1-i] = is
	}
	SortIssues(rev, []string{"priority", "createdAt"})
	if !reflect.DeepEqual(identifiers(rev), want) {
		t.Errorf("SortIssues not deterministic across permutations: %v, want %v", identifiers(rev), want)
	}
}

func TestSortIssuesUrgentBeforeLowNoneLast(t *testing.T) {
	got := []linear.Issue{
		sortIssue("FE-NONE", 0, "2024-01-01T00:00:00Z"),
		sortIssue("FE-LOW", 4, "2024-01-01T00:00:00Z"),
		sortIssue("FE-URGENT", 1, "2024-01-01T00:00:00Z"),
	}
	SortIssues(got, nil)
	want := []string{"FE-URGENT", "FE-LOW", "FE-NONE"}
	if !reflect.DeepEqual(identifiers(got), want) {
		t.Errorf("order = %v, want %v", identifiers(got), want)
	}
}

func TestNewLabelIDs(t *testing.T) {
	cases := []struct {
		name            string
		current         []string
		removeID, setID string
		want            []string
	}{
		{"removeAndAppend", []string{"a", "trigger", "b"}, "trigger", "sent", []string{"a", "b", "sent"}},
		{"setAlreadyPresentNoDup", []string{"a", "sent"}, "trigger", "sent", []string{"a", "sent"}},
		{"removeAbsentNoop", []string{"a", "b"}, "trigger", "sent", []string{"a", "b", "sent"}},
		{"orderStable", []string{"z", "m", "a"}, "", "sent", []string{"z", "m", "a", "sent"}},
		{"emptyCurrent", nil, "trigger", "sent", []string{"sent"}},
		{"emptySet", []string{"a", "trigger"}, "trigger", "", []string{"a"}},
		{"dedupsCurrent", []string{"a", "a", "b"}, "", "sent", []string{"a", "b", "sent"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NewLabelIDs(tc.current, tc.removeID, tc.setID)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("NewLabelIDs(%v, %q, %q) = %v, want %v", tc.current, tc.removeID, tc.setID, got, tc.want)
			}
		})
	}
}

func TestPruneSeenLabelMode(t *testing.T) {
	now := time.Now()
	seen := map[string]time.Time{
		"fresh":   now.Add(-10 * time.Minute),
		"expired": now.Add(-2 * time.Hour),
	}
	got := PruneSeen(seen, nil, "label", now, time.Hour)
	if _, ok := got["fresh"]; !ok {
		t.Error("label mode: entry within TTL must be kept")
	}
	if _, ok := got["expired"]; ok {
		t.Error("label mode: entry older than TTL must be pruned")
	}
}

func TestPruneSeenSeenMode(t *testing.T) {
	now := time.Now()
	seen := map[string]time.Time{
		"still-matching": now.Add(-100 * time.Hour), // age irrelevant in seen mode
		"gone":           now,
	}
	matched := map[string]bool{"still-matching": true}
	got := PruneSeen(seen, matched, "seen", now, time.Hour)
	if _, ok := got["still-matching"]; !ok {
		t.Error("seen mode: entry still in the match set must be kept regardless of age")
	}
	if _, ok := got["gone"]; ok {
		t.Error("seen mode: entry no longer matching must be pruned so a reopened ticket re-queues")
	}
}
