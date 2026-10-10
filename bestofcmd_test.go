package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

func TestLooksLikeIssue(t *testing.T) {
	for s, want := range map[string]bool{
		"FE-231": true, "sushi-623": true, "fe-5-l3xk2": false, "lola-p1-fe-5-b1": false, "FE": false, "-1": false,
	} {
		if got := looksLikeIssue(s); got != want {
			t.Errorf("looksLikeIssue(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestRenderCompare(t *testing.T) {
	d := protocol.BestOfCompareData{
		Group: "fe-5-x", Issue: "FE-5", Title: "Fix it", Project: "web", Suggested: "lola-web-fe-5-b2",
		Attempts: []protocol.BestOfAttempt{
			{Session: "lola-web-fe-5-b1", Attempt: 1, Agent: "claude", Model: "opus", Display: "working", DiffError: "no merge-base"},
			{Session: "lola-web-fe-5-b2", Attempt: 2, Agent: "codex", Display: "idle", PRNumber: 12, PRState: "OPEN", Draft: true, Checks: "pass",
				Files: 3, Additions: 40, Deletions: 2, Review: &protocol.BestOfReviewTally{Passes: 1}, Usage: &protocol.UsageInfo{Tokens: 1_250_000}},
		},
	}
	var buf bytes.Buffer
	renderCompare(&buf, d)
	out := buf.String()
	for _, want := range []string{"claude@opus", "★2", "#12 draft", "clean", "3 files +40 -2", "1.2M", "lola bestof pick lola-web-fe-5-b2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
