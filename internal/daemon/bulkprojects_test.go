package daemon

import (
	"testing"

	"github.com/sushidev-team/lola/internal/linear"
)

func TestMatchLabelRef(t *testing.T) {
	ls := []linear.Label{
		{ID: "1", Name: "Bug"},
		{ID: "2", Name: "Backend", Parent: &linear.Label{Name: "Area"}},
		{ID: "3", Name: "Dup"}, {ID: "4", Name: "dup"},
	}
	for ref, want := range map[string]string{"bug": "1", "area/backend": "2", "Backend": "2"} {
		if got, err := matchLabelRef(ls, ref); err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", ref, got, err, want)
		}
	}
	for _, ref := range []string{"nope", "dup", "x/bug"} {
		if _, err := matchLabelRef(ls, ref); err == nil {
			t.Errorf("%q should fail", ref)
		}
	}
}
