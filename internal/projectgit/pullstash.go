package projectgit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Fetch and source validation finish before the pull-stash action saves edits.
func (c Client) stashPull(ctx context.Context, dir string, r BranchRequest, owner, incoming string) error {
	if owner == "" || !sameCheckout(owner, dir) {
		return errors.New("choose the checked-out local branch to stash and pull")
	}
	return c.withStashedChanges(ctx, dir, r.Source, r.SourceSHA, "pull", "pull of "+strings.TrimPrefix(r.Source, "refs/heads/"), func() error {
		return c.integratePull(ctx, dir, r, owner, incoming)
	})
}

// withStashedChanges restores the original index and working tree together.
// A paused merge keeps its named stash separate from the conflict resolution.
func (c Client) withStashedChanges(ctx context.Context, dir, ref, originalHead, action, description string, integrate func() error) error {
	current, err := c.text(ctx, dir, "symbolic-ref", "--quiet", "HEAD")
	if err != nil || current != ref {
		return fmt.Errorf("checkout changed before %s; refresh and try again", action)
	}
	saved, err := c.saveForRestore(ctx, dir, originalHead, description)
	if err != nil {
		return err
	}
	integrateErr := integrate()
	// A timeout must not prevent restoring edits after a refused merge. This
	// bounded cleanup never aborts a merge or applies over a changed checkout.
	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	kept := func(cause error) error {
		return fmt.Errorf("%s stopped; local changes remain saved in Stashes (%s). Finish or abort any merge, then apply that stash: %w", action, saved.SHA, cause)
	}
	current, err = c.text(restoreCtx, dir, "symbolic-ref", "--quiet", "HEAD")
	if err != nil || current != ref {
		return kept(fmt.Errorf("checkout changed during %s", action))
	}
	head, err := c.text(restoreCtx, dir, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return kept(err)
	}
	if integrateErr != nil {
		op, err := c.Operation(restoreCtx, dir)
		if err != nil || op != "" || head != originalHead {
			return kept(integrateErr)
		}
	}
	if err := c.StashApply(restoreCtx, dir, saved.SHA, head, true); err != nil {
		return fmt.Errorf("restoring %s's saved changes failed; stash %s was kept. Inspect the working files and resolve any conflicts: %w", action, saved.SHA, err)
	}
	if integrateErr != nil {
		return fmt.Errorf("%s failed; local changes were restored: %w", action, integrateErr)
	}
	return nil
}
