package tui

import (
	"slices"
	"testing"
)

// A typed label name is not a picker option; confirming the picker must keep it.
func TestApplyPickKeepsTypedLabelNames(t *testing.T) {
	f := &settingsForm{fields: []setField{{key: "def_match_labels", kind: sfList, wsPick: true, lines: []string{"apps/website", "id-1"}}}}
	p := &setPicker{key: "def_match_labels", multi: true, sel: map[string]bool{"id-1": true, "id-2": true},
		opts: []setPickOpt{{"id-1", "one"}, {"id-2", "two"}}}
	f.applyPick(p)
	if got := f.fields[0].lines; !slices.Equal(got, []string{"apps/website", "id-1", "id-2"}) {
		t.Fatalf("lines = %v", got)
	}
}
