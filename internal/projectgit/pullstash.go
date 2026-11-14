package projectgit

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
)

// stashPull is only reached through the explicitly confirmed pull-stash action.
// Fetch and source validation already succeeded; a network failure never hides
// the user's edits. A named stash survives any interrupted/conflicted merge.
func (c Client) stashPull(ctx context.Context, dir string, r BranchRequest, owner, incoming string) error {
	current, err := c.text(ctx, dir, "symbolic-ref", "--quiet", "HEAD")
	if err != nil || current != r.Source || owner == "" || !sameCheckout(owner, dir) {
		return errors.New("choose the checked-out local branch to stash and pull")
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	message := fmt.Sprintf("lola: before pull of %s (%x)", strings.TrimPrefix(r.Source, "refs/heads/"), nonce)
	if err := c.StashPush(ctx, dir, message, r.SourceSHA); err != nil {
		return fmt.Errorf("could not prepare local changes for pull; inspect Stashes before retrying: %w", err)
	}
	entries, err := c.Stashes(ctx, dir)
	if err != nil {
		return fmt.Errorf("local changes were saved; inspect Stashes before retrying pull: %w", err)
	}
	var saved Stash
	for _, entry := range entries {
		if strings.HasSuffix(entry.Subject, ": "+message) {
			saved = entry
			break
		}
	}
	if saved.SHA == "" {
		return errors.New("cannot identify the saved local changes; inspect Stashes before retrying pull")
	}
	pullErr := c.integratePull(ctx, dir, r, owner, incoming)
	// A timeout must not prevent restoring edits after a refused merge. This
	// bounded cleanup never aborts a merge or applies over a changed checkout.
	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	kept := func(cause error) error {
		return fmt.Errorf("pull stopped; local changes remain saved in Stashes (%s). Finish or abort any merge, then apply that stash: %w", saved.SHA, cause)
	}
	current, err = c.text(restoreCtx, dir, "symbolic-ref", "--quiet", "HEAD")
	if err != nil || current != r.Source {
		return kept(errors.New("checkout changed during pull"))
	}
	head, err := c.text(restoreCtx, dir, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return kept(err)
	}
	if pullErr != nil {
		op, err := c.Operation(restoreCtx, dir)
		if err != nil || op != "" || head != r.SourceSHA {
			return kept(pullErr)
		}
	}
	if err := c.StashApply(restoreCtx, dir, saved.SHA, head, true); err != nil {
		return fmt.Errorf("restoring pull's saved changes failed; stash %s was kept. Inspect the working files and resolve any conflicts: %w", saved.SHA, err)
	}
	if pullErr != nil {
		return fmt.Errorf("pull failed; local changes were restored: %w", pullErr)
	}
	return nil
}
