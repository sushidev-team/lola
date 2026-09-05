package state

import (
	"fmt"
	"slices"
	"testing"
)

// Every grid below has ONE column per delivery state, in allDeliveryStates
// order (rollup_test.go owns that list, deliberately — a new delivery state
// then breaks these tests loudly instead of silently shifting a column):
//
//	none  draft  ci_pending  ci_failed  merge_conflict  changes_requested
//	review_pending  approved  merged  closed
//
// gridCheck walks agents × deliveries and reports every mismatch with both
// axes named, so a failure says which PAIR broke rather than which index.
func gridCheck[T comparable](t *testing.T, name string, grid map[AgentState][]T, got func(AgentState, DeliveryState) T) {
	t.Helper()
	if len(grid) != len(allAgentStates) {
		t.Fatalf("%s: grid has %d agent rows, allAgentStates has %d", name, len(grid), len(allAgentStates))
	}
	for _, a := range allAgentStates {
		row, ok := grid[a]
		if !ok {
			t.Fatalf("%s: no grid row for agent state %q", name, a)
		}
		if len(row) != len(allDeliveryStates) {
			t.Fatalf("%s[%q]: row has %d columns, allDeliveryStates has %d", name, a, len(row), len(allDeliveryStates))
		}
		for i, d := range allDeliveryStates {
			if g := got(a, d); g != row[i] {
				t.Errorf("%s(%q, %q) = %v, want %v", name, a, d, g, row[i])
			}
		}
	}
}

// The pre-split string-keyed tables, FROZEN here as a historical record. They
// were exported shims in tables.go until their last production caller moved to
// the pair form; they live on only so the two census tests below can state, in
// one place, exactly where the new tables deliberately disagree with the old
// ones. Nothing in lola classifies by these any more — do not re-export them.
var frozenLegacyHoldsSlot = map[string]bool{
	"working":           true,
	"needs_input":       true,
	"draft":             true,
	"ci_failed":         true,
	"changes_requested": true,
	"ci_pending":        true,
	"merge_conflict":    true,
}

func legacyHoldsSlot(status string) bool { return frozenLegacyHoldsSlot[status] }

func legacyPresent(status string) bool {
	switch status {
	case "dead", "session_ended", "closed":
		return false
	}
	return true
}

func TestDisplayFor(t *testing.T) {
	cases := map[AgentState]Display{
		AgentStarting:     DisplayWorking,
		AgentWorking:      DisplayWorking,
		AgentIdle:         DisplayIdle,
		AgentWaitingInput: DisplayNeedsYou,
		AgentExited:       DisplayGone,
		AgentDead:         DisplayGone,
		AgentShell:        DisplayShell,
		AgentOrphaned:     DisplayOrphaned,
	}
	for _, a := range allAgentStates {
		want, ok := cases[a]
		if !ok {
			t.Fatalf("agent state %q has no expected Display — the vocabulary grew", a)
		}
		if got := DisplayFor(a); got != want {
			t.Errorf("DisplayFor(%q) = %q, want %q", a, got, want)
		}
	}
	// An unknown or zero agent state must read as a LIVE agent, never as
	// gone: a session in a state the vocabulary has not caught up to must
	// stay visible in the views built to surface live work.
	for _, a := range []AgentState{"", "brand-new-state"} {
		if got := DisplayFor(a); got != DisplayWorking {
			t.Errorf("DisplayFor(%q) = %q, want %q (unknown must fail toward live)", a, got, DisplayWorking)
		}
	}
}

func TestAllDisplays(t *testing.T) {
	all := AllDisplays()
	seen := map[Display]bool{}
	for _, d := range all {
		if seen[d] {
			t.Errorf("AllDisplays lists %q twice", d)
		}
		seen[d] = true
	}
	// Every listed value must be reachable from some agent state, and every
	// agent state must land on a listed value — the two halves of "this list
	// is the complete primary vocabulary".
	produced := map[Display]bool{}
	for _, a := range allAgentStates {
		d := DisplayFor(a)
		produced[d] = true
		if !seen[d] {
			t.Errorf("DisplayFor(%q) = %q, which AllDisplays does not list", a, d)
		}
	}
	for _, d := range all {
		if !produced[d] {
			t.Errorf("AllDisplays lists %q but no agent state produces it", d)
		}
	}
}

func TestHoldsSlotGrid(t *testing.T) {
	// A live agent holds its slot for every delivery state except the parked
	// (review_pending, approved) and terminal (merged, closed) ones. An agent
	// that is not running — dead, exited, an agentless shell, an orphan —
	// never holds one, whatever its PR says.
	live := []bool{true, true, true, true, true, true, false, false, false, false}
	none := []bool{false, false, false, false, false, false, false, false, false, false}
	gridCheck(t, "HoldsSlot", map[AgentState][]bool{
		AgentStarting:     live,
		AgentWorking:      live,
		AgentIdle:         live,
		AgentWaitingInput: live,
		AgentExited:       none,
		AgentDead:         none,
		AgentShell:        none,
		AgentOrphaned:     none,
	}, HoldsSlot)
}

// TestHoldsSlotCountsIdlePreRPR pins the DELIBERATE behavior change: a live
// agent with no PR holds its slot even while idle.
//
// The legacy table said no, and that was survivable only because such a
// session became needs_input within 60s — the agent's own idle nudge — and
// needs_input did hold a slot. Once that nudge stops minting needs_input, an
// idle pre-PR session would hold no slot forever and dispatch would spawn
// straight past the concurrency cap.
func TestHoldsSlotCountsIdlePrePR(t *testing.T) {
	if !HoldsSlot(AgentIdle, DeliveryNone) {
		t.Error("an idle agent with no PR must hold its slot — the cap counts runners")
	}
	if legacyHoldsSlot(Rollup(AgentIdle, DeliveryNone)) {
		t.Error("the legacy table is supposed to say false here; this test no longer pins a change")
	}
}

// TestHoldsSlotDivergenceCensus pins EVERY pair where the pair-based table
// disagrees with the legacy one via Rollup, so a later edit to either side has
// to state its case here. Three families, each a Rollup precedence quirk the
// pair form drops:
//
//   - waiting_input masked the delivery axis, so a PR parked for review (or
//     closed) still counted a slot while its agent waited on a human.
//   - a delivery state masked exited/shell/orphaned, so a session with no
//     running agent counted a slot purely because its PR was open.
//   - idle pre-PR — the deliberate change above, and the only one that adds a
//     slot rather than removing one.
func TestHoldsSlotDivergenceCensus(t *testing.T) {
	want := []string{
		"waiting_input/review_pending",
		"waiting_input/approved",
		"waiting_input/closed",
		"idle/none",
		"exited/draft",
		"exited/ci_pending",
		"exited/ci_failed",
		"exited/merge_conflict",
		"exited/changes_requested",
		"shell/draft",
		"shell/ci_pending",
		"shell/ci_failed",
		"shell/merge_conflict",
		"shell/changes_requested",
		"orphaned/draft",
		"orphaned/ci_pending",
		"orphaned/ci_failed",
		"orphaned/merge_conflict",
		"orphaned/changes_requested",
	}
	var got []string
	for _, a := range allAgentStates {
		for _, d := range allDeliveryStates {
			if HoldsSlot(a, d) != legacyHoldsSlot(Rollup(a, d)) {
				got = append(got, fmt.Sprintf("%s/%s", a, d))
			}
		}
	}
	slices.Sort(got)
	sortedWant := slices.Clone(want)
	slices.Sort(sortedWant)
	if !slices.Equal(got, sortedWant) {
		t.Errorf("HoldsSlot divergence set changed\n got: %v\nwant: %v", got, sortedWant)
	}
}

func TestPresentGrid(t *testing.T) {
	// A running agent shields its issue unless the PR was explicitly closed;
	// dead and exited records shield nothing at all.
	livePresent := []bool{true, true, true, true, true, true, true, true, true, false}
	gonePresent := []bool{false, false, false, false, false, false, false, false, false, false}
	gridCheck(t, "Present", map[AgentState][]bool{
		AgentStarting:     livePresent,
		AgentWorking:      livePresent,
		AgentIdle:         livePresent,
		AgentWaitingInput: livePresent,
		AgentShell:        livePresent,
		AgentOrphaned:     livePresent,
		AgentExited:       gonePresent,
		AgentDead:         gonePresent,
	}, Present)
}

// TestPresentKeepsTheLegacyWords: the three excluding words are unchanged, so
// the classification did not move — only Rollup's precedence, which could hide
// one of them behind the other axis, is gone.
func TestPresentKeepsTheLegacyWords(t *testing.T) {
	for _, s := range AllStatuses() {
		wantGone := s == "dead" || s == "session_ended" || s == "closed"
		if legacyPresent(s) == wantGone {
			t.Errorf("legacyPresent(%q) = %v — the frozen legacy word set moved", s, !wantGone)
		}
	}
	if Present(AgentDead, DeliveryMerged) {
		t.Error("a dead pane over a merged PR must read gone (Rollup used to say \"merged\")")
	}
	if Present(AgentExited, DeliveryDraft) {
		t.Error("an exited agent over an open PR must read gone (Rollup used to say \"draft\")")
	}
}

func TestAttentionGrid(t *testing.T) {
	// Off the agent axis, attention is exactly the three regressed delivery
	// states; a waiting agent needs a human whatever its PR says.
	quiet := []bool{false, false, false, true, true, true, false, false, false, false}
	always := []bool{true, true, true, true, true, true, true, true, true, true}
	gridCheck(t, "Attention", map[AgentState][]bool{
		AgentStarting:     quiet,
		AgentWorking:      quiet,
		AgentIdle:         quiet,
		AgentExited:       quiet,
		AgentDead:         quiet,
		AgentShell:        quiet,
		AgentOrphaned:     quiet,
		AgentWaitingInput: always,
	}, Attention)
}

// TestAttentionIsAPredicateNotAWord: the whole point of moving attention off
// the status string is that the two reasons live on different axes and can be
// true at once — the collapsed word had to pick one.
func TestAttentionIsAPredicateNotAWord(t *testing.T) {
	if !Attention(AgentWorking, DeliveryCIFailed) {
		t.Error("a happily working agent with red CI still needs a human")
	}
	if !Attention(AgentWaitingInput, DeliveryReviewPending) {
		t.Error("a blocked agent needs a human even while its PR waits on a reviewer")
	}
	if Attention(AgentIdle, DeliveryNone) {
		t.Error("a resting agent with no PR is not an attention item — that is the 60s-nudge population")
	}
}

func TestSortRankGrid(t *testing.T) {
	// Tier order is the contract, so these rows are read straight off the
	// documented evaluation order: 0 blocked, 1 regressed, 2 moving, 3 parked,
	// 5 done, 4 quiet.
	//
	// Two consequences of that order worth stating explicitly, because both
	// look like typos: a working agent outranks even a merged/closed PR
	// (tier 2 — it is still running), and draft/ci_pending outrank a dead pane
	// (tier 2 — a PR still moving is the more useful sort key than the pane
	// that opened it).
	working := []int{2, 2, 2, 1, 1, 1, 2, 2, 2, 2}
	waiting := []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	quiet := []int{4, 2, 2, 1, 1, 1, 3, 3, 5, 5}
	done := []int{5, 2, 2, 1, 1, 1, 3, 3, 5, 5}
	gridCheck(t, "SortRank", map[AgentState][]int{
		AgentStarting:     working,
		AgentWorking:      working,
		AgentWaitingInput: waiting,
		AgentIdle:         quiet,
		AgentShell:        quiet,
		AgentOrphaned:     quiet,
		AgentExited:       done,
		AgentDead:         done,
	}, SortRank)
}

func TestKanbanKeyForGrid(t *testing.T) {
	live := []string{"working", "working", "working", "fixing", "fixing", "fixing", "review", "review", "done", "done"}
	// done wins over needs: an exited agent parked on a red build is not work
	// to fix, and a waiting agent on a merged PR has nothing left to answer.
	waiting := []string{"needs", "needs", "needs", "needs", "needs", "needs", "needs", "needs", "done", "done"}
	gone := []string{"done", "done", "done", "done", "done", "done", "done", "done", "done", "done"}
	gridCheck(t, "KanbanKeyFor", map[AgentState][]string{
		AgentStarting:     live,
		AgentWorking:      live,
		AgentIdle:         live,
		AgentShell:        live,
		AgentOrphaned:     live,
		AgentWaitingInput: waiting,
		AgentExited:       gone,
		AgentDead:         gone,
	}, KanbanKeyFor)
}

func TestKanbanColumns(t *testing.T) {
	cols := KanbanColumns()
	want := []KanbanColumn{
		{Key: "needs", Title: "Needs You"},
		{Key: "working", Title: "Working"},
		{Key: "fixing", Title: "Fixing"},
		{Key: "review", Title: "In Review"},
		{Key: "done", Title: "Done"},
	}
	if !slices.Equal(cols, want) {
		t.Fatalf("KanbanColumns() = %v, want %v", cols, want)
	}
	// Every pair must land in exactly one REAL column — the Board renders the
	// column list, so a key outside it would drop the card entirely.
	keys := map[string]bool{}
	for _, c := range cols {
		keys[c.Key] = true
	}
	for _, a := range allAgentStates {
		for _, d := range allDeliveryStates {
			if k := KanbanKeyFor(a, d); !keys[k] {
				t.Errorf("KanbanKeyFor(%q, %q) = %q, which is not a column", a, d, k)
			}
		}
	}
	// An unknown pair must still land somewhere live, for the same reason
	// DisplayFor fails toward working.
	if k := KanbanKeyFor("brand-new-state", "brand-new-delivery"); k != KanbanFallbackKey {
		t.Errorf("KanbanKeyFor(unknown, unknown) = %q, want %q", k, KanbanFallbackKey)
	}
}
