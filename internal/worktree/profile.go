package worktree

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var commitObjectID = regexp.MustCompile(`^(?:[a-fA-F0-9]{40}|[a-fA-F0-9]{64})$`)

// ProfilePaths records the reviewed change scope from exact commits, with local
// source verified clean. No project hooks or tests execute in a source review.
func (m *Manager) ProfilePaths(ctx context.Context, dir, head, base string) ([]string, error) {
	if !commitObjectID.MatchString(head) || !commitObjectID.MatchString(base) {
		return nil, fmt.Errorf("specialist review needs exact commit object IDs")
	}
	if err := m.VerifyReviewHead(ctx, dir, head); err != nil {
		return nil, err
	}
	out, _, err := m.git(ctx, "-C", dir, "diff", "--no-ext-diff", "--name-only", "-z", base+"..."+head, "--")
	if err != nil {
		return nil, err
	}
	if len(out) > 65536 {
		return nil, fmt.Errorf("specialist changed-path list exceeds 64 KiB")
	}
	if out == "" {
		return []string{}, nil
	}
	return strings.Split(strings.TrimSuffix(out, "\x00"), "\x00"), nil
}
