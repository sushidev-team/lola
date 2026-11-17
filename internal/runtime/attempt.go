package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
)

// Empty claim directories are atomic, never released, and separate from session
// retention. Read the high-water mark so an issue can have more than 20 rounds.
func (n *Native) attemptClaims(baseID string) (string, int, error) {
	if err := validContextSegment(baseID); err != nil {
		return "", 0, err
	}
	if n.Home == "" {
		return "", 0, fmt.Errorf("reserve attempt: runtime home is required")
	}
	dir := filepath.Join(n.Home, "state", "attempts", baseID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, fmt.Errorf("reserve attempt: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, fmt.Errorf("read attempts: %w", err)
	}
	next := 1
	for _, e := range entries {
		i, err := strconv.Atoi(e.Name())
		if err != nil || i < 1 || i > int(^uint(0)>>1)-maxSpawnAttempts-1 {
			return "", 0, fmt.Errorf("invalid attempt reservation %q", e.Name())
		}
		if i >= next {
			next = i + 1
		}
	}
	return dir, next, nil
}

func (n *Native) issuePrompt(p config.Project, issue linear.Issue, branch string) []byte {
	var b strings.Builder
	b.Write(promptMD(p, issue, branch))
	b.WriteString("\n## Current round of work\n\nThis is a fresh work attempt. Read the latest Linear comments and shared notes, and compare the current checkout with previous PRs before changing code. The original description may already be implemented: address the outstanding requests, preserve completed work, and do not repeat changes that already shipped. Previous merged PRs are context, not this attempt's deliverable; open a new PR on the assigned branch.\n")
	if n.PreviousWork != nil {
		if previous := n.PreviousWork(p.Name, issue.Identifier); previous != "" {
			b.WriteString("\nPrevious attempts (historical context):\n\n")
			b.WriteString(previous)
		}
	}
	return []byte(b.String())
}
