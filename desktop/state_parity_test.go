package main

// Anti-drift pin for the status vocabularies: theme.ts mirrors Go's
// internal/state tables with no compiler in common, so a text read of the real
// file is the only thing that can notice them diverging (the same pattern as
// the catppuccin canvas test in main_test.go).
//
// TWO vocabularies are pinned, because the app ships two. ALL_DISPLAYS is the
// PRIMARY pill's — the agent axis reduced by state.DisplayFor, which is what
// every desktop surface renders. ALL_STATUSES is the LEGACY rolled-up word
// state.Rollup still ships on protocol.SessionInfo.status and the mobile
// companion still reads; it is a wire-compatibility vocabulary, not a dead one,
// so it is pinned just as hard.

import (
	"os"
	"regexp"
	"testing"

	"github.com/sushidev-team/lola/internal/state"
)

const themeTS = "frontend/src/lib/theme.ts"

func readTheme(t *testing.T) []byte {
	t.Helper()
	src, err := os.ReadFile(themeTS)
	if err != nil {
		t.Fatalf("read %s: %v", themeTS, err)
	}
	return src
}

// tsArray parses a `<name>: <type>[] = [ … ]` literal out of theme.ts and
// returns the quoted lowercase words inside it, in order.
func tsArray(t *testing.T, src []byte, name string) []string {
	t.Helper()
	block := regexp.MustCompile(`(?s)` + name + `:\s*\w+\[\]\s*=\s*\[(.*?)\n\];`).FindSubmatch(src)
	if block == nil {
		t.Fatalf("%s: %s array not found — the file's shape changed and this test stopped guarding anything", themeTS, name)
	}
	var out []string
	for _, m := range regexp.MustCompile(`"([a-z_]+)"`).FindAllSubmatch(block[1], -1) {
		out = append(out, string(m[1]))
	}
	return out
}

func pinList(t *testing.T, what string, ts, goList []string) {
	t.Helper()
	if len(ts) != len(goList) {
		t.Fatalf("%s: theme.ts lists %d, Go lists %d\nts: %v\ngo: %v", what, len(ts), len(goList), ts, goList)
	}
	for i := range goList {
		if ts[i] != goList[i] {
			t.Errorf("%s[%d]: theme.ts %q != Go %q", what, i, ts[i], goList[i])
		}
	}
}

// TestDisplayVocabularyParity: theme.ts's ALL_DISPLAYS must be byte-identical
// (order included) to state.AllDisplays().
//
// This is the vocabulary the primary pill draws from, so a word Go grows and
// theme.ts does not is a session the app renders as "working" — the deliberate
// fallback in BOTH DisplayFor and displayFor, and therefore a divergence no
// runtime error can surface.
func TestDisplayVocabularyParity(t *testing.T) {
	goList := make([]string, 0, len(state.AllDisplays()))
	for _, d := range state.AllDisplays() {
		goList = append(goList, string(d))
	}
	pinList(t, "display", tsArray(t, readTheme(t), "ALL_DISPLAYS"), goList)
}

// TestStatusVocabularyParity: theme.ts's ALL_STATUSES must be byte-identical
// (order included) to state.AllStatuses().
func TestStatusVocabularyParity(t *testing.T) {
	pinList(t, "status", tsArray(t, readTheme(t), "ALL_STATUSES"), state.AllStatuses())
}

// TestKanbanParity: theme.ts's KANBAN_COLUMNS must match Go's key + title, in
// order.
//
// It used to pin the STATUS SET of each column too. state.KanbanColumn lost
// that field when column membership became a function of both axes
// (state.KanbanKeyFor), so there is nothing left to compare it against — the
// key is now the join between the two sides, and the title is what a human
// reads. Both surfaces derive membership from their own port of KanbanKeyFor,
// which the pair-grid tests in internal/state/display_test.go pin on the Go
// side and theme.test.ts pins on the TS side.
func TestKanbanParity(t *testing.T) {
	src := readTheme(t)
	block := regexp.MustCompile(`(?s)KANBAN_COLUMNS[^=]*=\s*\[(.*?)\n\];`).FindSubmatch(src)
	if block == nil {
		t.Fatalf("%s: KANBAN_COLUMNS not found", themeTS)
	}
	rowRe := regexp.MustCompile(`key:\s*"([a-z_]+)",\s*title:\s*"([^"]+)"`)
	rows := rowRe.FindAllSubmatch(block[1], -1)
	cols := state.KanbanColumns()
	if len(rows) != len(cols) {
		t.Fatalf("theme.ts has %d kanban columns, Go has %d", len(rows), len(cols))
	}
	for i, row := range rows {
		if string(row[1]) != cols[i].Key {
			t.Errorf("column[%d] key: theme.ts %q != Go %q", i, row[1], cols[i].Key)
		}
		if string(row[2]) != cols[i].Title {
			t.Errorf("column[%d] title: theme.ts %q != Go %q", i, row[2], cols[i].Title)
		}
	}
}
