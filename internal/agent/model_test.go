package agent

import (
	"slices"
	"testing"
)

func TestValidModel(t *testing.T) {
	for _, ok := range []string{"opus", "claude-opus-4-1[1m]", "gpt-5-codex", "anthropic/claude-sonnet-4", "us.anthropic.claude-v1:0"} {
		if !ValidModel(ok) {
			t.Errorf("ValidModel(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "-rf", "a b", `x"y`, "a;b", "$(x)", "a'b"} {
		if ValidModel(bad) {
			t.Errorf("ValidModel(%q) = true, want false", bad)
		}
	}
}

func TestLaunchModelArgs(t *testing.T) {
	cases := []struct {
		k     Kind
		model string
		want  []string
	}{
		{Claude, "opus", []string{"--model", "opus"}},
		{OpenCode, "anthropic/claude-sonnet-4", []string{"--model", "anthropic/claude-sonnet-4"}},
		{Codex, "gpt-5-codex", []string{"-c", `model="gpt-5-codex"`}},
		{Claude, "", nil},
		{Codex, "--evil", nil},
	}
	for _, c := range cases {
		if got := LaunchModelArgs(c.k, c.model); !slices.Equal(got, c.want) {
			t.Errorf("LaunchModelArgs(%s, %q) = %q, want %q", c.k, c.model, got, c.want)
		}
	}
}
