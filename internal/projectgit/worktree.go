package projectgit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// NewWorktreePath resolves the existing parent, including symlinks. Creation
// only accepts a new directory; no reuse, force, pruning or deletion is needed.
func NewWorktreePath(path string) (string, error) {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return "", errors.New("choose an absolute path for the new worktree")
	}
	path = filepath.Clean(path)
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return "", errors.New("choose a folder that does not already exist")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", errors.New("the parent folder must already exist")
	}
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return "", errors.New("the parent folder must be a directory")
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

func (c Client) CreateWorktree(ctx context.Context, dir, source, sha, path, newBranch string) error {
	if _, err := c.branchRef(ctx, dir, source, sha, false); err != nil {
		return err
	}
	if err := c.requireIdle(ctx, dir); err != nil {
		return err
	}
	destination, err := NewWorktreePath(path)
	if err != nil {
		return err
	}
	args := []string{"worktree", "add"}
	if newBranch != "" {
		if strings.HasPrefix(newBranch, "-") || strings.HasPrefix(newBranch, "refs/") || newBranch == "HEAD" {
			return errors.New("enter a valid new branch name")
		}
		if _, err := c.git(ctx, dir, "", "check-ref-format", "refs/heads/"+newBranch); err != nil {
			return errors.New("enter a valid new branch name")
		}
		args = append(args, "--no-track", "-b", newBranch, "--", destination, sha)
	} else {
		if !strings.HasPrefix(source, "refs/heads/") {
			return errors.New("name a local branch for the remote worktree")
		}
		if owner, err := c.branchOwner(ctx, dir, source); err != nil {
			return err
		} else if owner != "" {
			return errors.New("branch already has a checkout; open it or choose a new branch name")
		}
		args = append(args, "--", destination, strings.TrimPrefix(source, "refs/heads/"))
	}
	_, err = c.git(ctx, dir, "", args...)
	return err
}
