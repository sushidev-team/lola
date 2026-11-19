package projectgit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// switchBranch first lets Git carry compatible edits in place. Only an
// overwrite refusal on a human-requested checkout triggers a stash and retry.
func (c Client) switchBranch(ctx context.Context, dir string, r BranchRequest) error {
	local, err := c.ActionBranch(ctx, dir, r)
	if err != nil {
		return err
	}
	targetSHA := r.SourceSHA
	existingLocal := true
	switchTo := func() error { return c.checkout(ctx, dir, local) }
	if local != r.Source {
		sha, err := c.text(ctx, dir, "show-ref", "--verify", "--hash", local)
		if err == nil {
			targetSHA = sha
		} else {
			existingLocal = false
			switchTo = func() error {
				_, err := c.git(ctx, dir, "", "switch", "--no-overwrite-ignore", "--track", "-c", strings.TrimPrefix(local, "refs/heads/"), "--", r.Source)
				return err
			}
		}
	}
	origin, originErr := c.text(ctx, dir, "rev-parse", "--symbolic-full-name", "HEAD")
	head, headErr := c.text(ctx, dir, "rev-parse", "--verify", "HEAD")
	err = switchTo()
	if err == nil || !strings.Contains(err.Error(), "would be overwritten by checkout") || originErr != nil || headErr != nil {
		return err
	}
	at := func(ctx context.Context, ref, sha string) bool {
		current, err := c.text(ctx, dir, "rev-parse", "--symbolic-full-name", "HEAD")
		return err == nil && current == ref && c.requireHead(ctx, dir, sha) == nil
	}
	if !at(ctx, origin, head) {
		return errors.New("checkout changed before saving local changes; refresh and try again")
	}
	saved, err := c.saveForRestore(ctx, dir, head, "switch to "+strings.TrimPrefix(local, "refs/heads/"))
	if err != nil {
		return err
	}
	if !at(ctx, origin, head) {
		return fmt.Errorf("checkout changed; local changes remain in stash %s", saved.SHA)
	}
	_, err = c.branchRef(ctx, dir, r.Source, r.SourceSHA, false)
	if err == nil && local != r.Source && existingLocal {
		_, err = c.branchRef(ctx, dir, local, targetSHA, true)
	}
	if err == nil {
		err = c.requireIdle(ctx, dir)
	}
	if err == nil {
		err = switchTo()
	}
	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	ref, sha := local, targetSHA
	if err != nil {
		ref, sha = origin, head
	}
	if !at(restoreCtx, ref, sha) {
		return fmt.Errorf("checkout changed; local changes remain in stash %s", saved.SHA)
	}
	if applyErr := c.StashApply(restoreCtx, dir, saved.SHA, sha, true); applyErr != nil {
		return fmt.Errorf("restoring switch's saved changes failed on %s; inspect stash %s and the working files: %w", ref, saved.SHA, applyErr)
	}
	return err
}
