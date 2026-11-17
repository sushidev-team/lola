package projectgit

import (
	"context"
	"errors"
	"strings"
)

// CreateBranch creates a new local branch at the exact commit the human chose.
// Native -c is transactional with checkout: if local changes prevent switching,
// Git does not leave a half-created branch. Neither path ever resets a branch,
// discards working files, or stashes changes on the user's behalf.
func (c Client) CreateBranch(ctx context.Context, dir, name, startSHA string, checkout bool) error {
	if name == "" || name != strings.TrimSpace(name) || len(name) > 1024 || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "refs/") || name == "HEAD" || strings.ContainsRune(name, 0) {
		return errors.New("enter a valid new branch name")
	}
	// Use the full ref, not check-ref-format --branch: the latter expands @{-1}.
	if _, err := c.git(ctx, dir, "", "check-ref-format", "refs/heads/"+name); err != nil {
		return errors.New("enter a valid new branch name")
	}
	sha, err := c.commitSHA(ctx, dir, startSHA)
	if err != nil {
		return err
	}
	if checkout {
		if err = c.requireIdle(ctx, dir); err != nil {
			return err
		}
		_, err = c.git(ctx, dir, "", "switch", "--no-track", "-c", name, "--", sha)
	} else {
		_, err = c.git(ctx, dir, "", "branch", "--no-track", "--", name, sha)
	}
	return err
}
