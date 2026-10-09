package tui

import (
	"strings"
	"testing"
)

// A selected id the picker cannot list (the org→team label move minted new
// ids) must stay visible and selectable — applyPick rebuilds the selection
// from the options, so a hidden id would be dropped without a word.
func TestWithSelectedExtras(t *testing.T) {
	f := &formModel{}
	opts := []pickOpt{{"new-ready", "agent-ready"}}
	got := f.withSelectedExtras(opts, []string{"new-ready", "13ad06f0-a662-dead"}, func(id string) string { return shortID(id) })
	if len(got) != 2 || got[1].id != "13ad06f0-a662-dead" || !strings.Contains(got[1].label, "no longer in Linear") {
		t.Fatalf("extras = %+v", got)
	}
	named := f.withSelectedExtras(opts, []string{"ws-1"}, func(string) string { return "Bug" })
	if named[1].label != "Bug" {
		t.Fatalf("a resolvable id (workspace label) must keep its name: %+v", named)
	}
}
