package worktree

import (
	"context"
	"strings"
	"testing"
)

func TestWorkSummaryHappyPath(t *testing.T) {
	gitBin, _ := fakeGit(t,
		stub{match: "rev-parse --verify --quiet refs/remotes/origin/main"},
		stub{match: "log --oneline -20 origin/main..HEAD", stdout: "abc123 fix login flow\ndef456 add tests"},
		stub{match: "diff --stat origin/main...HEAD", stdout: " main.go | 2 ++\n 1 file changed"},
		stub{match: "status --porcelain", stdout: " M main.go"},
	)
	m := &Manager{GitBin: gitBin, Root: t.TempDir()}

	logOut, statusOut, diffStat := m.WorkSummary(context.Background(), t.TempDir(), "main")
	if logOut != "abc123 fix login flow\ndef456 add tests" {
		t.Errorf("log = %q", logOut)
	}
	if statusOut != " M main.go" {
		t.Errorf("status = %q", statusOut)
	}
	if diffStat != " main.go | 2 ++\n 1 file changed" {
		t.Errorf("diffstat = %q", diffStat)
	}
}

// A base ref that resolves neither as origin/<base> nor as <base> (an offline
// clone, a renamed default branch) drops the log/diffstat but keeps the
// status — a handoff with partial facts is still a handoff.
func TestWorkSummaryWithoutBaseRef(t *testing.T) {
	gitBin, _ := fakeGit(t,
		stub{match: "rev-parse --verify --quiet", exit: 1},
		stub{match: "status --porcelain", stdout: "?? scratch.txt"},
	)
	m := &Manager{GitBin: gitBin, Root: t.TempDir()}

	logOut, statusOut, diffStat := m.WorkSummary(context.Background(), t.TempDir(), "main")
	if logOut != "" || diffStat != "" {
		t.Errorf("log/diffstat = %q/%q, want empty without a base ref", logOut, diffStat)
	}
	if statusOut != "?? scratch.txt" {
		t.Errorf("status = %q, want the porcelain line", statusOut)
	}
}

// Every piece is capped so the briefing stays a briefing.
func TestWorkSummaryCapsLongOutput(t *testing.T) {
	var status strings.Builder
	for i := 0; i < summaryStatusLines+10; i++ {
		status.WriteString(" M file\n")
	}
	gitBin, _ := fakeGit(t,
		stub{match: "rev-parse --verify --quiet", exit: 1},
		stub{match: "status --porcelain", stdout: strings.TrimRight(status.String(), "\n")},
	)
	m := &Manager{GitBin: gitBin, Root: t.TempDir()}

	_, statusOut, _ := m.WorkSummary(context.Background(), t.TempDir(), "main")
	if !strings.Contains(statusOut, "… (10 more)") {
		t.Errorf("capped status should note the dropped lines: %q", statusOut)
	}
}
