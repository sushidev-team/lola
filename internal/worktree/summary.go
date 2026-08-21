package worktree

import (
	"context"
	"strconv"
	"strings"
)

// Caps for the WorkSummary pieces — a handoff briefing stays a briefing.
const (
	summaryLogLines    = 20
	summaryStatusLines = 50
	summaryDiffLines   = 40
)

// capLines trims s to its first n lines, noting how many were dropped.
func capLines(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + "\n… (" + strconv.Itoa(len(lines)-n) + " more)"
}

// WorkSummary gathers read-only git facts about the worktree at dir for a
// handoff briefing: the session branch's commit log and diffstat against the
// base branch, plus the porcelain status (uncommitted work). It is
// BEST-EFFORT by design — each piece silently degrades to "" when git cannot
// answer (a missing base ref must never cost the handoff), and each piece is
// capped so the briefing stays a briefing.
func (m *Manager) WorkSummary(ctx context.Context, dir, base string) (logOut, statusOut, diffStat string) {
	baseRef := ""
	if base != "" {
		// Prefer the remote-tracking ref (Create's own start-point rule);
		// fall back to the local branch for offline clones.
		if _, _, err := m.git(ctx, "-C", dir, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+base); err == nil {
			baseRef = "origin/" + base
		} else if _, _, err := m.git(ctx, "-C", dir, "rev-parse", "--verify", "--quiet", base); err == nil {
			baseRef = base
		}
	}
	if baseRef != "" {
		if out, _, err := m.git(ctx, "-C", dir, "log", "--oneline", "-"+strconv.Itoa(summaryLogLines), baseRef+"..HEAD"); err == nil {
			logOut = strings.TrimRight(out, "\n")
		}
		if out, _, err := m.git(ctx, "-C", dir, "diff", "--stat", baseRef+"...HEAD"); err == nil {
			diffStat = capLines(out, summaryDiffLines)
		}
	}
	if out, _, err := m.git(ctx, "-C", dir, "status", "--porcelain"); err == nil {
		statusOut = capLines(out, summaryStatusLines)
	}
	return logOut, statusOut, diffStat
}
