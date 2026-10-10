package planner

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

func TestParseToleratesFenceAndProse(t *testing.T) {
	out := "Here is the plan:\n```json\n{\"steps\":[{\"title\":\" A \",\"description\":\"x\\ny\"},{\"title\":\"B\",\"blockedBy\":[1]}]}\n```\n"
	steps, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []protocol.PlanStep{{Title: "A", Description: "x\ny"}, {Title: "B", BlockedBy: []int{1}}}
	if !reflect.DeepEqual(steps, want) {
		t.Errorf("steps = %+v, want %+v", steps, want)
	}
}

func TestParseRejectsNonJSON(t *testing.T) {
	for _, out := range []string{"", "no plan here", "{not json}"} {
		if _, err := Parse(out); err == nil {
			t.Errorf("Parse(%q) = nil error", out)
		}
	}
}

func TestValidate(t *testing.T) {
	two := func(b []int) []protocol.PlanStep {
		return []protocol.PlanStep{{Title: "a"}, {Title: "b", BlockedBy: b}}
	}
	many := make([]protocol.PlanStep, MaxSteps+1)
	for i := range many {
		many[i].Title = "t"
	}
	bad := map[string][]protocol.PlanStep{
		"too few":      {{Title: "only"}},
		"too many":     many,
		"empty title":  {{Title: "a"}, {Title: "  \x07 "}},
		"self dep":     two([]int{2}),
		"forward dep":  {{Title: "a", BlockedBy: []int{2}}, {Title: "b"}},
		"zero dep":     two([]int{0}),
		"negative dep": two([]int{-1}),
		"out of range": two([]int{3}),
	}
	for name, steps := range bad {
		if _, err := Validate(steps); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}

	in := []protocol.PlanStep{
		{Title: "a\x1b[31m\nred", Description: "line1\nline2\x00"},
		{Title: "b"},
		{Title: "c", BlockedBy: []int{2, 1, 2}},
	}
	out, err := Validate(in)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if out[0].Title != "a[31m red" || out[0].Description != "line1\nline2" {
		t.Errorf("sanitized step = %+v", out[0])
	}
	if !reflect.DeepEqual(out[2].BlockedBy, []int{1, 2}) {
		t.Errorf("deps = %v, want [1 2]", out[2].BlockedBy)
	}
	if !reflect.DeepEqual(in[2].BlockedBy, []int{2, 1, 2}) {
		t.Error("Validate must not mutate its input")
	}

	long := strings.Repeat("x", MaxTitleRunes+50)
	out, _ = Validate([]protocol.PlanStep{{Title: long}, {Title: "b"}})
	if n := len([]rune(out[0].Title)); n != MaxTitleRunes {
		t.Errorf("clipped title has %d runes, want %d", n, MaxTitleRunes)
	}
}

func TestPlanUsesStdinForIssueText(t *testing.T) {
	var instr, stdin string
	run := func(_ context.Context, i, s string) (string, error) {
		instr, stdin = i, s
		return `{"steps":[{"title":"a"},{"title":"b","blockedBy":[1]}]}`, nil
	}
	if _, err := Plan(context.Background(), run, "FE-1", "Title", "secret body"); err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if strings.Contains(instr, "secret body") || !strings.Contains(stdin, "secret body") {
		t.Error("issue text must travel on stdin, never in the instruction")
	}
	if _, err := Plan(context.Background(), nil, "FE-1", "", ""); err == nil {
		t.Error("nil runner must error")
	}
	boom := errors.New("boom")
	if _, err := Plan(context.Background(), func(context.Context, string, string) (string, error) { return "", boom }, "FE-1", "", ""); !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
}
