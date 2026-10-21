package agent

import (
	"slices"
	"testing"
)

func TestTelemetryArgsPreserveReviewPosture(t *testing.T) {
	for _, kind := range []Kind{Claude, Codex, OpenCode} {
		args := TelemetryArgs(kind, "exact instruction", "selected-model", true)
		if !slices.Contains(args, "selected-model") || !slices.Contains(args, "exact instruction") {
			t.Fatal("lost explicit instruction/model")
		}
		for _, forbidden := range []string{"--auto", "--dangerously-skip-permissions", "--dangerously-bypass-approvals-and-sandbox", "--full-auto"} {
			if slices.Contains(args, forbidden) {
				t.Fatal("telemetry widened agent permissions")
			}
		}
		switch kind {
		case Codex:
			if !slices.Contains(args, "--json") || !slices.Contains(args, "read-only") || !slices.Contains(args, "--skip-git-repo-check") || args[len(args)-1] != "exact instruction" {
				t.Fatal("invalid Codex telemetry posture")
			}
		case Claude:
			if !slices.Contains(args, "stream-json") || !slices.Contains(args, "--verbose") {
				t.Fatal("missing Claude event stream")
			}
		case OpenCode:
			if !slices.Contains(args, "--format") || !slices.Contains(args, "json") || args[len(args)-1] != "exact instruction" {
				t.Fatal("missing OpenCode event stream")
			}
		}
	}
}
