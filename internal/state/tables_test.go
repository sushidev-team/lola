package state

import (
	"slices"
	"testing"
)

func TestNotable(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{"", "working", true},            // spawn
		{"", "", false},                  // empty
		{"working", "idle", false},       // turn churn
		{"ci_pending", "idle", false},    // post-PR churn
		{"working", "orphaned", false},   // adoption anomaly, not feed-worthy
		{"idle", "working", false},       // routine turn start
		{"needs_input", "working", true}, // resumed after waiting on a human
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

// TestVocabularyClosed: AllStatuses must be exactly what Rollup can produce —
// no word listed that the rollup never mints (a client would theme a status
// that cannot happen), and none produced that is missing from the list (the
// desktop and mobile both key off it, so an unlisted word renders unthemed).
// The pre-axis words that a P1-era daemon used to write are asserted to stay
// dead, because they are still recognized on the READ side (FromLegacy) and it
// would be easy to let one leak back into the live vocabulary.
func TestVocabularyClosed(t *testing.T) {
	all := AllStatuses()
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
	for s := range produced {
		if !slices.Contains(all, s) {
			t.Errorf("Rollup produces %q but AllStatuses does not list it", s)
		}
	}
	for _, dead := range []string{"no_pr", "pr_open", "no_signal", "none"} {
		if slices.Contains(all, dead) {
			t.Errorf("dead vocabulary %q resurrected in AllStatuses", dead)
		}
	}
}
