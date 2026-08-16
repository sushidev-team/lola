package proctree

import (
	"context"
	"os"
	"reflect"
	"testing"
)

// The shape this package exists for: a descendant that LEFT its ancestor's
// process group. A coding agent's Bash tool creates exactly that, and a kill of
// the pane's group alone never reaches it — so the port it holds stays held.
func TestTreeGroupsSpansChildrenThatLeftTheGroup(t *testing.T) {
	tbl := Table{
		{PID: 100, PPID: 1, PGID: 100},   // the tmux pane's process
		{PID: 101, PPID: 100, PGID: 100}, // a child that stayed in the group
		{PID: 200, PPID: 100, PGID: 200}, // the agent's Bash tool: own group
		{PID: 201, PPID: 200, PGID: 200}, // php artisan serve
		{PID: 202, PPID: 201, PGID: 200}, // php -S, holding :8000
		{PID: 300, PPID: 1, PGID: 300},   // unrelated
	}

	if got, want := tbl.TreeGroups(100), []int{100, 200}; !reflect.DeepEqual(got, want) {
		t.Errorf("TreeGroups(100) = %v, want %v — the escaped group is what keeps the port", got, want)
	}
	if got, want := tbl.Descendants(100), []int{101, 200, 201, 202}; !reflect.DeepEqual(got, want) {
		t.Errorf("Descendants(100) = %v, want %v", got, want)
	}
}

// Groups 0 and 1 mean "every process we may signal" and init. A table that
// somehow reports one must not put it in front of a killer.
func TestTreeGroupsDropsTheDangerousGroups(t *testing.T) {
	tbl := Table{
		{PID: 100, PPID: 1, PGID: 100},
		{PID: 101, PPID: 100, PGID: 1},
		{PID: 102, PPID: 100, PGID: 0},
	}
	if got, want := tbl.TreeGroups(100), []int{100}; !reflect.DeepEqual(got, want) {
		t.Errorf("TreeGroups = %v, want %v", got, want)
	}
	if got := tbl.TreeGroups(1); got != nil {
		t.Errorf("TreeGroups(1) = %v, want nothing — init is never a root", got)
	}
}

// A pid the snapshot does not carry exited between the two questions. Answering
// 0 keeps that indistinguishable from "no group", which every caller treats as
// "nothing to signal".
func TestGroupAndParentAnswerZeroForAnUnknownPID(t *testing.T) {
	tbl := Table{{PID: 100, PPID: 7, PGID: 100}}
	if got := tbl.Group(999); got != 0 {
		t.Errorf("Group(999) = %d, want 0", got)
	}
	if got := tbl.Parent(999); got != 0 {
		t.Errorf("Parent(999) = %d, want 0", got)
	}
	if got := tbl.Parent(100); got != 7 {
		t.Errorf("Parent(100) = %d, want 7", got)
	}
}

// A self-parenting row (a table lola did not write) must not loop the walk.
func TestDescendantsSurvivesASelfParentingRow(t *testing.T) {
	tbl := Table{
		{PID: 100, PPID: 1, PGID: 100},
		{PID: 101, PPID: 101, PGID: 101},
		{PID: 102, PPID: 100, PGID: 100},
	}
	if got, want := tbl.Descendants(100), []int{102}; !reflect.DeepEqual(got, want) {
		t.Errorf("Descendants = %v, want %v", got, want)
	}
}

// The one exec: this test process must appear in its own process table, with
// the pgid the kernel reports for it.
func TestReadFindsThisProcess(t *testing.T) {
	tbl, err := Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	self := os.Getpid()
	if got := tbl.Group(self); got == 0 {
		t.Fatalf("the process table has no row for this test process (pid %d)", self)
	}
}
