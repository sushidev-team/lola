package tui

import (
	"reflect"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

// mixedSessions is a deliberately out-of-order set spanning every sort tier,
// with intra-tier project/issue ties to exercise the stable tiebreak.
func mixedSessions() []protocol.SessionInfo {
	return []protocol.SessionInfo{
		{ID: "1", Project: "web", Issue: "ENG-9", Status: "merged"},
		{ID: "2", Project: "api", Issue: "ENG-2", Status: "working"},
		{ID: "3", Project: "web", Issue: "ENG-1", Status: "needs_input"},
		{ID: "4", Project: "api", Issue: "ENG-7", Status: "review_pending"},
		{ID: "5", Project: "web", Issue: "ENG-3", Status: "ci_failed"},
		{ID: "6", Project: "api", Issue: "ENG-4", Status: "working"},
		{ID: "7", Project: "api", Issue: "ENG-5", Status: "changes_requested"},
		{ID: "8", Project: "web", Issue: "ENG-8", Status: "approved"},
		{ID: "9", Project: "api", Issue: "ENG-6", Status: "dead"},
		{ID: "10", Project: "api", Issue: "ENG-1", Status: "ci_pending"},
	}
}

func statusOrder(in []protocol.SessionInfo) []string {
	ids := make([]string, len(in))
	for i, s := range in {
		ids[i] = s.ID
	}
	return ids
}

func TestSortSessionsAttentionFirst(t *testing.T) {
	in := mixedSessions()
	got := SortSessions(in)

	// Expected order by tier then project,issue:
	// tier0 needs_input:      s3 (web,ENG-1)
	// tier1 action-needed:    s7 (api,ENG-5 changes_requested), s5 (web,ENG-3 ci_failed)
	// tier2 active:           s10 (api,ENG-1 ci_pending), s2 (api,ENG-2), s6 (api,ENG-4)
	// tier3 parked:           s4 (api,ENG-7 review), s8 (web,ENG-8 approved)
	// tier5 done:             s9 (api,ENG-6 dead), s1 (web,ENG-9 merged)
	want := []string{"3", "7", "5", "10", "2", "6", "4", "8", "9", "1"}
	if g := statusOrder(got); !reflect.DeepEqual(g, want) {
		t.Errorf("SortSessions order = %v, want %v", g, want)
	}
}

func TestSortSessionsDoesNotMutate(t *testing.T) {
	in := mixedSessions()
	before := statusOrder(in)
	_ = SortSessions(in)
	if after := statusOrder(in); !reflect.DeepEqual(before, after) {
		t.Errorf("SortSessions mutated input: %v -> %v", before, after)
	}
}

func TestSortSessionsStableTiebreak(t *testing.T) {
	// Same tier (all working), same project — issue is the final deterministic key.
	in := []protocol.SessionInfo{
		{ID: "b", Project: "web", Issue: "ENG-2", Status: "working"},
		{ID: "a", Project: "web", Issue: "ENG-1", Status: "working"},
		{ID: "c", Project: "web", Issue: "ENG-3", Status: "working"},
	}
	got := statusOrder(SortSessions(in))
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tiebreak order = %v, want %v", got, want)
	}
}

func TestApply(t *testing.T) {
	in := []protocol.SessionInfo{
		{ID: "1", Project: "web", Issue: "ENG-100", Branch: "lola/auth", Status: "working"},
		{ID: "2", Project: "api", Issue: "ENG-200", Branch: "lola/billing", Status: "needs_input"},
		{ID: "3", Project: "web", Issue: "ENG-300", Branch: "lola/authz", Status: "ci_failed"},
		{ID: "4", Project: "api", Issue: "ENG-400", Branch: "lola/search", Status: "merged"},
	}
	ids := func(ss []protocol.SessionInfo) []string { return statusOrder(ss) }

	cases := []struct {
		name string
		f    Filter
		want []string
	}{
		{"empty matches all", Filter{}, []string{"1", "2", "3", "4"}},
		{"text over issue", Filter{Text: "eng-200"}, []string{"2"}},
		{"text over branch", Filter{Text: "auth"}, []string{"1", "3"}},
		{"text over project", Filter{Text: "api"}, []string{"2", "4"}},
		{"text over status", Filter{Text: "ci_failed"}, []string{"3"}},
		{"text case-insensitive", Filter{Text: "AUTH"}, []string{"1", "3"}},
		{"text no match", Filter{Text: "zzz"}, nil},
		{"attention only", Filter{AttentionOnly: true}, []string{"2", "3"}},
		{"project exact", Filter{Project: "web"}, []string{"1", "3"}},
		{"status exact", Filter{Status: "merged"}, []string{"4"}},
		{"combined project+attention", Filter{Project: "web", AttentionOnly: true}, []string{"3"}},
		{"combined text+status", Filter{Text: "auth", Status: "working"}, []string{"1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ids(Apply(in, c.f))
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Apply(%+v) = %v, want %v", c.f, got, c.want)
			}
		})
	}
}

func TestApplyDoesNotMutate(t *testing.T) {
	in := []protocol.SessionInfo{
		{ID: "1", Status: "working"},
		{ID: "2", Status: "needs_input"},
	}
	before := statusOrder(in)
	_ = Apply(in, Filter{AttentionOnly: true})
	if after := statusOrder(in); !reflect.DeepEqual(before, after) {
		t.Errorf("Apply mutated input: %v -> %v", before, after)
	}
}

func TestAttentionCount(t *testing.T) {
	if n := AttentionCount(mixedSessions()); n != 3 {
		// needs_input(1) + ci_failed(1) + changes_requested(1) = 3
		t.Errorf("AttentionCount = %d, want 3", n)
	}
	if n := AttentionCount(nil); n != 0 {
		t.Errorf("AttentionCount(nil) = %d, want 0", n)
	}
}

// allStatuses is the full derived status vocabulary the views must handle.
var allStatuses = []string{
	"working", "needs_input", "idle", "ci_failed", "changes_requested",
	"merge_conflict", "ci_pending", "review_pending", "approved", "pr_open",
	"merged", "dead", "session_ended",
	// extended vocabulary that DeriveStatus can emit:
	"draft", "no_pr", "closed", "no_signal",
}

func TestKanbanColumnsUniqueKeysAndStatuses(t *testing.T) {
	seenKey := map[string]bool{}
	seenStatus := map[string]string{}
	for _, col := range KanbanColumns() {
		if seenKey[col.Key] {
			t.Errorf("duplicate column key %q", col.Key)
		}
		seenKey[col.Key] = true
		for _, s := range col.Statuses {
			if prev, ok := seenStatus[s]; ok {
				t.Errorf("status %q in two columns (%q and %q)", s, prev, col.Key)
			}
			seenStatus[s] = col.Key
		}
	}
}

func TestGroupKanbanEveryStatusExactlyOneColumn(t *testing.T) {
	// One session per status; each must appear in exactly one column, and the
	// grouping must not drop or duplicate any session.
	in := make([]protocol.SessionInfo, len(allStatuses))
	for i, s := range allStatuses {
		in[i] = protocol.SessionInfo{ID: s, Status: s}
	}
	groups := GroupKanban(in)

	// Every column key from KanbanColumns is present (even if empty).
	for _, col := range KanbanColumns() {
		if _, ok := groups[col.Key]; !ok {
			t.Errorf("GroupKanban missing column key %q", col.Key)
		}
	}

	total := 0
	placed := map[string]int{}
	for _, sessions := range groups {
		total += len(sessions)
		for _, s := range sessions {
			placed[s.Status]++
		}
	}
	if total != len(allStatuses) {
		t.Errorf("GroupKanban placed %d sessions, want %d", total, len(allStatuses))
	}
	for _, s := range allStatuses {
		if placed[s] != 1 {
			t.Errorf("status %q placed %d times, want exactly 1", s, placed[s])
		}
	}

	// Unknown/extended statuses land in the fallback (Working) column.
	for _, s := range []string{"no_pr", "closed", "no_signal", "totally_made_up"} {
		if got := kanbanKeyForStatus(s); got != kanbanFallbackKey {
			t.Errorf("kanbanKeyForStatus(%q) = %q, want fallback %q", s, got, kanbanFallbackKey)
		}
	}
}

func TestStatusDisplayReusesStyleAndHasBadge(t *testing.T) {
	for _, s := range allStatuses {
		d := statusDisplay(s)
		if d.Badge == "" {
			t.Errorf("statusDisplay(%q) empty badge", s)
		}
		if len(d.Badge) > 2 {
			t.Errorf("statusDisplay(%q) badge %q longer than 2 chars", s, d.Badge)
		}
		// Style must be exactly what statusStyle yields (no divergence).
		if d.Style.GetForeground() != statusStyle(s).GetForeground() {
			t.Errorf("statusDisplay(%q) style diverged from statusStyle", s)
		}
	}
}
