package daemon

import (
	"strings"
	"testing"
)

// TestNeutralizeBotTriggers verifies that a @coderabbit / @coderabbitai mention in
// a body lola posts to a PR is defused so it can never trigger a NEW CodeRabbit
// review (the "check the PR but never trigger a new CodeRabbit there" guarantee).
func TestNeutralizeBotTriggers(t *testing.T) {
	const zwsp = "\u200b"
	cases := []struct{ in, want string }{
		{"please @coderabbitai review this", "please @" + zwsp + "coderabbitai review this"},
		{"CC @CodeRabbit and @coderabbitai full review", "CC @" + zwsp + "CodeRabbit and @" + zwsp + "coderabbitai full review"},
		{"no mention here", "no mention here"},
		{"an email like a@coderabbit.dev is also defused", "an email like a@" + zwsp + "coderabbit.dev is also defused"},
	}
	for _, c := range cases {
		got := neutralizeBotTriggers(c.in)
		if got != c.want {
			t.Errorf("neutralizeBotTriggers(%q) = %q, want %q", c.in, got, c.want)
		}
		// No live, contiguous "@coderabbit" token may survive (case-insensitive) —
		// that is exactly what the CodeRabbit app parses as a command.
		if strings.Contains(strings.ToLower(got), "@coderabbit") {
			t.Errorf("neutralized body still carries a live @coderabbit mention: %q", got)
		}
	}
}
