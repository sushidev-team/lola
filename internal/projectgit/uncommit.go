package projectgit

import (
	"context"
	"errors"
	"strings"
)

// Uncommit removes the checkout's newest commit and leaves its changes staged.
// It is an explicit human action, pinned to the commit the human saw. Nothing
// in the index or the working files is touched, so no change can be lost: the
// commit's own changes join whatever was already staged. A commit some remote
// already has, a merge and the first commit are refused, never rewritten.
func (c Client) Uncommit(ctx context.Context, dir, expectedHead string) error {
	if !shaPattern.MatchString(expectedHead) {
		return errors.New("refresh the repository and choose the commit to undo")
	}
	if err := c.requireIdle(ctx, dir); err != nil {
		return err
	}
	head, err := c.text(ctx, dir, "rev-parse", "--verify", "HEAD")
	if err != nil || head != expectedHead {
		return errors.New("HEAD changed since this commit was selected; refresh and try again")
	}
	line, err := c.text(ctx, dir, "rev-list", "--parents", "-n", "1", head)
	if err != nil {
		return err
	}
	parents := strings.Fields(line)
	if len(parents) < 2 {
		return errors.New("the first commit of a repository cannot be undone here")
	}
	if len(parents) > 2 {
		return errors.New("a merge commit cannot be undone here; revert it instead")
	}
	// A failed query is not proof that the commit is local.
	remote, err := c.text(ctx, dir, "for-each-ref", "--format=%(refname)", "--contains="+head, "refs/remotes/")
	if err != nil {
		return err
	}
	if remote != "" {
		return errors.New("this commit is already on a remote; revert it instead of rewriting shared history")
	}
	files, err := c.status(ctx, dir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.Conflict {
			return errors.New("resolve conflicted files before undoing a commit")
		}
	}
	// --soft moves only the branch tip. Git records the old tip in ORIG_HEAD
	// and the reflog, so the commit itself stays recoverable.
	_, err = c.git(ctx, dir, "", "reset", "--soft", parents[1])
	return err
}
