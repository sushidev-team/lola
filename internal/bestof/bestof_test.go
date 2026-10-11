package bestof

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseMixString(t *testing.T) {
	got, err := ParseMixString("claude:2, codex ,claude@opus,opencode@anthropic/claude-sonnet-4")
	if err != nil {
		t.Fatal(err)
	}
	want := []Attempt{
		{Agent: "claude"}, {Agent: "claude"}, {Agent: "codex"},
		{Agent: "claude", Model: "opus"},
		{Agent: "opencode", Model: "anthropic/claude-sonnet-4"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseEntryColonModel(t *testing.T) {
	// A count is only the digits after the LAST colon; a model carrying a
	// colon therefore needs an explicit count.
	got, err := ParseEntry("claude@us.anthropic.claude-v1:0:2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Model != "us.anthropic.claude-v1:0" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseMixRejects(t *testing.T) {
	for in, frag := range map[string]string{
		"claude":            "at least 2",
		"gpt:2":             "unknown agent",
		"claude:0,codex":    "count must be",
		"claude:7":          "count must be",
		"claude:4,codex:3":  "more than",
		"claude@,codex":     "invalid model",
		"claude@-rf,codex":  "invalid model",
		"claude@a b,codex":  "invalid model",
		"":                  "at least 2",
		"claude,,":          "at least 2",
		"claude:2,codex@$x": "invalid model",
	} {
		_, err := ParseMixString(in)
		if err == nil || !strings.Contains(err.Error(), frag) {
			t.Errorf("ParseMixString(%q) err = %v, want it to mention %q", in, err, frag)
		}
	}
}

func TestAttemptLabel(t *testing.T) {
	if l := (Attempt{Agent: "claude", Model: "opus"}).Label(); l != "claude@opus" {
		t.Fatal(l)
	}
	if l := (Attempt{Agent: "codex"}).Label(); l != "codex" {
		t.Fatal(l)
	}
}

func TestSuggest(t *testing.T) {
	cases := []struct {
		name string
		fs   []Facts
		want int
	}{
		{"none has a PR", []Facts{{}, {Checks: "pass"}}, -1},
		{"only PR wins", []Facts{{}, {OpenPR: true, Checks: "fail"}}, 1},
		{"green beats red", []Facts{{OpenPR: true, Checks: "fail"}, {OpenPR: true, Checks: "pass"}}, 1},
		{"fewer blockers", []Facts{
			{OpenPR: true, Checks: "pass", Reviewed: true, Blocker: 1},
			{OpenPR: true, Checks: "pass", Reviewed: true, Major: 3},
		}, 1},
		{"reviewed clean beats unreviewed", []Facts{
			{OpenPR: true, Checks: "pass"},
			{OpenPR: true, Checks: "pass", Reviewed: true, Minor: 2},
		}, 1},
		{"smaller diff breaks a tie", []Facts{
			{OpenPR: true, Checks: "pass", Reviewed: true, Lines: 400},
			{OpenPR: true, Checks: "pass", Reviewed: true, Lines: 90},
		}, 1},
		{"full tie keeps the earlier", []Facts{
			{OpenPR: true, Checks: "pass"}, {OpenPR: true, Checks: "pass"},
		}, 0},
		{"pending CI ranks below no CI", []Facts{
			{OpenPR: true, Checks: "pending"}, {OpenPR: true, Checks: "none"},
		}, 1},
	}
	for _, c := range cases {
		if got := Suggest(c.fs); got != c.want {
			t.Errorf("%s: Suggest = %d, want %d", c.name, got, c.want)
		}
	}
}
