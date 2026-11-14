package projectgit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RemoveWorktree is only for human checkouts authorized by the service. Target
// is the exact listed path; Source and SourceSHA pin the checkout being removed.
func (c Client) RemoveWorktree(ctx context.Context, dir string, r BranchRequest) error {
	if !filepath.IsAbs(r.Target) || sameCheckout(dir, r.Target) {
		return errors.New("the project's repository folder cannot be removed")
	}
	trees, err := c.Worktrees(ctx, dir)
	if err != nil {
		return err
	}
	var found *Worktree
	for i := range trees {
		if sameCheckout(trees[i].Path, r.Target) {
			if i == 0 {
				return errors.New("the main worktree cannot be removed")
			}
			found = &trees[i]
		}
	}
	if found == nil || found.Locked || found.Prunable {
		return errors.New("worktree is missing, locked or unavailable; refresh the repository")
	}
	ref := ""
	if found.Branch != "" {
		ref = "refs/heads/" + found.Branch
	}
	if ref != r.Source || !shaPattern.MatchString(r.SourceSHA) || found.Head != r.SourceSHA {
		return errors.New("worktree changed since it was selected; refresh and try again")
	}
	if err = c.requireIdle(ctx, dir); err != nil {
		return err
	}
	if r.ForceRemoveWorktree {
		err = c.requireIdle(ctx, found.Path)
	} else {
		err = c.requireClean(ctx, found.Path)
	}
	if err != nil {
		return err
	}
	deleteBranch := r.Action == "remove-worktree-branch"
	if deleteBranch {
		if ref == "" {
			return errors.New("detached worktree has no branch to delete")
		}
		if _, err = c.branchRef(ctx, dir, ref, r.SourceSHA, true); err != nil {
			return err
		}
		if !r.ForceDeleteBranch {
			// Match branch --delete's merged check before removing the checkout,
			// so an unmerged branch leaves both the folder and branch intact.
			base, err := c.text(ctx, dir, "for-each-ref", "--format=%(upstream)", ref)
			if err != nil {
				return err
			}
			if base == "" {
				base = "HEAD"
			}
			if _, err = c.git(ctx, dir, "", "merge-base", "--is-ancestor", r.SourceSHA, base); err != nil {
				return errors.New("branch is not fully merged; remove only the worktree to keep its branch")
			}
		}
	}
	args := []string{"worktree", "remove"}
	if r.ForceRemoveWorktree {
		args = append(args, "--force")
	}
	if _, err = c.git(ctx, dir, "", append(args, "--", found.Path)...); err != nil {
		// Git can unregister the checkout before reporting a failed directory
		// deletion (Finder may recreate .DS_Store during removal).
		if cleanupErr := c.finishRemovedWorktree(ctx, dir, found.Path); cleanupErr != nil {
			return fmt.Errorf("%w; %v", err, cleanupErr)
		}
	}
	if deleteBranch {
		r.Action = "delete-branch"
		if err = c.BranchAction(ctx, dir, r); err != nil {
			return fmt.Errorf("worktree removed, but branch was kept: %w", err)
		}
	}
	return nil
}

// finishRemovedWorktree only cleans an empty directory or Finder metadata
// left by the removal we just authorized. It never recursively deletes files.
func (c Client) finishRemovedWorktree(ctx context.Context, dir, path string) error {
	trees, err := c.Worktrees(ctx, dir)
	if err != nil {
		return err
	}
	for _, tree := range trees {
		if sameCheckout(tree.Path, path) {
			return errors.New("worktree is still registered")
		}
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("worktree removed, but its path was replaced; remaining files were kept")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("worktree path changed; remaining files were kept")
	}
	folder, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := folder.ReadDir(-1)
	folder.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != ".DS_Store" || entry.IsDir() {
			return errors.New("worktree removed, but files remain; remaining files and branch were kept")
		}
	}
	if len(entries) > 0 {
		if err = root.Remove(".DS_Store"); err != nil {
			return err
		}
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) {
		return errors.New("worktree path changed; remaining files were kept")
	}
	return os.Remove(path)
}

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
