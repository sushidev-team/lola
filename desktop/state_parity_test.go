package main

// Anti-drift pin for the status vocabulary: theme.ts mirrors Go's
// internal/state tables with no compiler in common, so a text read of the real
// file is the only thing that can notice them diverging (the same pattern as
// the catppuccin canvas test in main_test.go).

import (
	"os"
	"regexp"
	"testing"

	"github.com/sushidev-team/lola/internal/state"
)

const themeTS = "frontend/src/lib/theme.ts"

// tsAllStatuses parses theme.ts's ALL_STATUSES array literal.
func tsAllStatuses(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile(themeTS)
	if err != nil {
		t.Fatalf("read %s: %v", themeTS, err)
	}
	block := regexp.MustCompile(`(?s)ALL_STATUSES:\s*string\[\]\s*=\s*\[(.*?)\]`).FindSubmatch(src)
	if block == nil {
		t.Fatalf("%s: ALL_STATUSES array not found — the file's shape changed and this test stopped guarding anything", themeTS)
	}
	var out []string
	for _, m := range regexp.MustCompile(`"([a-z_]+)"`).FindAllSubmatch(block[1], -1) {
		out = append(out, string(m[1]))
	}
	return out
}

// TestStatusVocabularyParity: theme.ts's ALL_STATUSES must be byte-identical
// (order included) to state.AllStatuses().
func TestStatusVocabularyParity(t *testing.T) {
	ts := tsAllStatuses(t)
	goList := state.AllStatuses()
	if len(ts) != len(goList) {
		t.Fatalf("theme.ts lists %d statuses, Go lists %d\nts: %v\ngo: %v", len(ts), len(goList), ts, goList)
	}
	for i := range goList {
		if ts[i] != goList[i] {
			t.Errorf("status[%d]: theme.ts %q != Go %q", i, ts[i], goList[i])
		}
	}
}

// TestKanbanParity: theme.ts's KANBAN_COLUMNS status sets must match Go's.
func TestKanbanParity(t *testing.T) {
	src, err := os.ReadFile(themeTS)
	if err != nil {
		t.Fatalf("read %s: %v", themeTS, err)
	}
	block := regexp.MustCompile(`(?s)KANBAN_COLUMNS[^=]*=\s*\[(.*?)\n\];`).FindSubmatch(src)
	if block == nil {
		t.Fatalf("%s: KANBAN_COLUMNS not found", themeTS)
	}
	rowRe := regexp.MustCompile(`title:\s*"([^"]+)",\s*statuses:\s*\[([^\]]*)\]`)
	statusRe := regexp.MustCompile(`"([a-z_]+)"`)
	rows := rowRe.FindAllSubmatch(block[1], -1)
	cols := state.KanbanColumns()
	if len(rows) != len(cols) {
		t.Fatalf("theme.ts has %d kanban columns, Go has %d", len(rows), len(cols))
	}
	for i, row := range rows {
		if string(row[1]) != cols[i].Title {
			t.Errorf("column[%d] title: theme.ts %q != Go %q", i, row[1], cols[i].Title)
		}
		var ts []string
		for _, m := range statusRe.FindAllSubmatch(row[2], -1) {
			ts = append(ts, string(m[1]))
		}
		if len(ts) != len(cols[i].Statuses) {
			t.Errorf("column %q: theme.ts %v != Go %v", cols[i].Title, ts, cols[i].Statuses)
			continue
		}
		for j := range ts {
			if ts[j] != cols[i].Statuses[j] {
				t.Errorf("column %q status[%d]: theme.ts %q != Go %q", cols[i].Title, j, ts[j], cols[i].Statuses[j])
			}
		}
	}
}
