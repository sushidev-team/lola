package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

func TestBoardChip(t *testing.T) {
	if got := boardChip(protocol.SessionInfo{}); got != "" {
		t.Fatalf("no board, no chip: %q", got)
	}
	fresh := time.Now()
	si := protocol.SessionInfo{Board: &protocol.BoardInfo{HasProgress: true, ProgressDerived: true, Percent: 40, Done: 2, Total: 5, UpdatedAt: fresh}}
	if got := stripANSI(boardChip(si)); got != "▰▰▱▱▱ 2/5" {
		t.Fatalf("derived chip = %q", got)
	}
	si.Board.Blocked = "need a key"
	if got := stripANSI(boardChip(si)); got != "⏸ blocked" {
		t.Fatalf("a blocker outranks progress: %q", got)
	}
}

func TestBoardLines(t *testing.T) {
	si := protocol.SessionInfo{Board: &protocol.BoardInfo{
		Phase: "testing", Done: 1, Total: 2, UpdatedAgo: "3m", UpdatedAt: time.Now(),
		Todos:  []protocol.BoardTodo{{Text: "build", State: "done"}, {Text: "test", State: "active"}},
		Checks: []protocol.BoardCheck{{Name: "lint", State: "fail", Summary: "3"}},
		Note:   "almost there",
	}}
	got := stripANSI(strings.Join(boardLines(si, 80), "\n"))
	for _, want := range []string{"plan:     testing · 1/2 done · reported 3m ago", "✓ build", "▸ test", "note: almost there", "✗ lint 3"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// The claim audit (SUSHI-622): a mismatch outranks the plan in the chip, and the
// detail card names it plus each audited check's verdict.
func TestBoardClaimAudit(t *testing.T) {
	si := protocol.SessionInfo{Board: &protocol.BoardInfo{
		HasProgress: true, Percent: 100, UpdatedAt: time.Now(),
		Checks:     []protocol.BoardCheck{{Name: "tests", State: "pass", Evidence: "unverified"}},
		Mismatches: []string{`"tests" claimed pass, but no test command ran before the claim`},
	}}
	if got := stripANSI(boardChip(si)); got != "⚠ unverified" {
		t.Fatalf("chip = %q", got)
	}
	got := stripANSI(strings.Join(boardLines(si, 120), "\n"))
	for _, want := range []string{`⚠ "tests" claimed pass, but no test command ran`, "✓ tests (no run found)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
