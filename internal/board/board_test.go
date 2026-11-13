package board

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func apply(t *testing.T, b Board, argv ...string) Board {
	t.Helper()
	nb, err := Apply(b, argv, t0)
	if err != nil {
		t.Fatalf("Apply(%q): %v", argv, err)
	}
	return nb
}

func TestTodoLifecycle(t *testing.T) {
	b := apply(t, Board{}, "todo", "set", "read issue", "write code", "test")
	if b.Current() != "read issue" || b.Total() != 3 {
		t.Fatalf("set: current %q total %d", b.Current(), b.Total())
	}
	b = apply(t, b, "todo", "done", "1")
	if b.Current() != "write code" {
		t.Fatalf("done should advance to the next pending item, current %q", b.Current())
	}
	b = apply(t, b, "todo", "start", "3")
	if b.Current() != "test" || b.Todos[1].State != TodoPending {
		t.Fatalf("start must move the single active marker: %+v", b.Todos)
	}
	b = apply(t, b, "todo", "add", "polish")
	if b.Total() != 4 || b.Todos[3].State != TodoPending {
		t.Fatalf("add: %+v", b.Todos)
	}
	b = apply(t, b, "todo", "done", "2", "3", "4")
	if pct, derived, ok := b.Percent(); !ok || !derived || pct != 100 {
		t.Fatalf("derived percent = %d %v %v", pct, derived, ok)
	}
	if b.Current() != "" {
		t.Fatalf("nothing pending, nothing active; got %q", b.Current())
	}
	b = apply(t, b, "todo", "remove", "1")
	if b.Total() != 3 {
		t.Fatalf("remove: %+v", b.Todos)
	}
}

func TestApplyNeverMutatesInput(t *testing.T) {
	b := apply(t, Board{}, "todo", "set", "a", "b")
	before := b.Todos[0].State
	_ = apply(t, b, "todo", "done", "1")
	if b.Todos[0].State != before {
		t.Fatal("Apply aliased the input board's todo slice")
	}
}

func TestProgress(t *testing.T) {
	cases := map[string]int{"60": 60, "60%": 60, "0.6": 60, "3/4": 75, "150": 100, "-3": 0}
	for in, want := range cases {
		b := apply(t, Board{}, "progress", in, "migrating", "tables")
		if pct, derived, _ := b.Percent(); pct != want || derived {
			t.Errorf("progress %s = %d (derived %v), want %d", in, pct, derived, want)
		}
		if b.ProgressLabel != "migrating tables" {
			t.Errorf("label = %q", b.ProgressLabel)
		}
	}
	b := apply(t, apply(t, Board{}, "progress", "40"), "progress", "clear")
	if _, _, ok := b.Percent(); ok {
		t.Fatal("progress clear with no todos should leave nothing to draw")
	}
}

func TestBlockedKeepsFirstTimestamp(t *testing.T) {
	b, _ := Apply(Board{}, []string{"blocked", "need", "staging", "key"}, t0)
	b, _ = Apply(b, []string{"blocked", "still need it"}, t0.Add(time.Hour))
	if !b.BlockedAt.Equal(t0) || b.Blocked != "still need it" {
		t.Fatalf("blocker re-statement must keep its start: %v %q", b.BlockedAt, b.Blocked)
	}
	b = apply(t, b, "unblocked")
	if b.Blocked != "" || !b.BlockedAt.IsZero() {
		t.Fatal("unblocked must clear both fields")
	}
}

func TestChecksUpsertByName(t *testing.T) {
	b := apply(t, Board{}, "check", "tests", "running")
	b = apply(t, b, "check", "Tests", "pass", "142/142")
	b = apply(t, b, "check", "lint", "failed", "3 issues")
	if len(b.Checks) != 2 || b.Checks[0].State != CheckPass || b.Checks[0].Summary != "142/142" || b.Checks[1].State != CheckFail {
		t.Fatalf("checks: %+v", b.Checks)
	}
	b = apply(t, b, "check", "clear", "lint")
	if len(b.Checks) != 1 {
		t.Fatalf("clear one: %+v", b.Checks)
	}
}

func TestRejectsAndLeavesBoardUntouched(t *testing.T) {
	b := apply(t, Board{}, "phase", "testing")
	for _, argv := range [][]string{
		{},
		{"dance"},
		{"phase", "sleeping"},
		{"phase"},
		{"todo", "done", "1"},
		{"todo", "set"},
		{"progress", "lots"},
		{"blocked"},
		{"check", "tests", "maybe"},
	} {
		nb, err := Apply(b, argv, t0.Add(time.Minute))
		if !errors.Is(err, ErrUsage) {
			t.Errorf("Apply(%q) err = %v, want ErrUsage", argv, err)
		}
		if nb.Phase != "testing" || !nb.UpdatedAt.Equal(b.UpdatedAt) {
			t.Errorf("Apply(%q) changed the board on rejection", argv)
		}
	}
}

func TestCaps(t *testing.T) {
	items := make([]string, MaxTodos+1)
	for i := range items {
		items[i] = "x"
	}
	if _, err := Apply(Board{}, append([]string{"todo", "set"}, items...), t0); err == nil {
		t.Fatal("over-long plan accepted")
	}
	b := apply(t, Board{}, "note", strings.Repeat("é", MaxNoteRunes+50))
	if n := len([]rune(b.Note)); n != MaxNoteRunes {
		t.Fatalf("note clipped to %d runes, want %d", n, MaxNoteRunes)
	}
}

func TestSanitizesControlCharacters(t *testing.T) {
	b := apply(t, Board{}, "note", "\x1b[31mred\x1b[0m\nline\ttwo​")
	if b.Note != "[31mred[0m line two" {
		t.Fatalf("note = %q", b.Note)
	}
}

func TestClear(t *testing.T) {
	b := apply(t, Board{}, "todo", "set", "a")
	b = apply(t, b, "clear")
	if !b.Empty() || !b.UpdatedAt.Equal(t0) {
		t.Fatalf("clear: %+v", b)
	}
}
