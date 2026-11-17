package worktree

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sushidev-team/lola/internal/config"
)

// CheckoutExisting opens a named branch without resetting it. Local branches
// win; otherwise use origin's tracking ref, or a single other remote's ref.
// Only refs already known to this checkout are eligible, matching the picker.
// Neither failure nor later session cleanup owns this branch.
func (m *Manager) CheckoutExisting(ctx context.Context, p config.Project, id, branch string) (string, error) {
	if m.Root == "" {
		return "", errors.New("worktree: Root not set")
	}
	if err := validSegment(p.Name); err != nil {
		return "", err
	}
	if err := validSegment(id); err != nil {
		return "", err
	}
	if branch == "" || strings.HasPrefix(branch, "-") || strings.Contains(branch, "@{") {
		return "", errors.New("worktree: an existing branch name is required")
	}
	if _, _, err := m.git(ctx, "-C", p.Path, "check-ref-format", "refs/heads/"+branch); err != nil {
		return "", err
	}
	dir := filepath.Join(m.Root, p.Name, id)
	if err := m.ensureCleanDir(ctx, p, dir); err != nil {
		return "", err
	}
	args := []string{"-C", p.Path, "worktree", "add", dir, branch}
	if _, _, err := m.git(ctx, "-C", p.Path, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		out, _, err := m.git(ctx, "-C", p.Path, "for-each-ref", "--format=%(refname)", "refs/remotes/")
		if err != nil {
			return "", err
		}
		var matches []string
		for _, ref := range strings.Fields(out) {
			_, name, ok := strings.Cut(strings.TrimPrefix(ref, "refs/remotes/"), "/")
			if ok && name == branch && name != "HEAD" {
				matches = append(matches, ref)
				if ref == "refs/remotes/origin/"+branch {
					matches = []string{ref}
					break
				}
			}
		}
		if len(matches) != 1 {
			return "", fmt.Errorf("worktree: branch %q is missing or ambiguous across remotes; check it out locally first", branch)
		}
		args = []string{"-C", p.Path, "worktree", "add", "--track", "-b", branch, dir, matches[0]}
	}
	if _, stderr, err := m.git(ctx, args...); err != nil {
		if isBranchCheckedOut(stderr) {
			return "", fmt.Errorf("%w: %s; use its existing session or Main checkout", ErrBranchCheckedOut, branch)
		}
		return "", err
	}
	return dir, nil
}
