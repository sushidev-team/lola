package projectgit

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (c Client) mergeBranch(ctx context.Context, dir string, r BranchRequest) error {
	current, err := c.text(ctx, dir, "rev-parse", "--symbolic-full-name", "HEAD")
	if err != nil {
		return err
	}
	if current != r.Target {
		// Edits in another checkout do not belong to the merge target.
		if err := c.requireClean(ctx, dir); err != nil {
			return err
		}
		if err := c.checkout(ctx, dir, r.Target); err != nil {
			return err
		}
	}
	source := strings.TrimPrefix(strings.TrimPrefix(r.Source, "refs/heads/"), "refs/remotes/")
	target := strings.TrimPrefix(r.Target, "refs/heads/")
	merge := func() error {
		// Switching or saving edits can take time. Recheck the destination and
		// the selected tips before writing a merge commit from this index.
		current, err := c.text(ctx, dir, "symbolic-ref", "--quiet", "HEAD")
		if err != nil || current != r.Target {
			return errors.New("checkout changed before merge; refresh and try again")
		}
		if _, err := c.branchRef(ctx, dir, r.Source, r.SourceSHA, false); err != nil {
			return err
		}
		if _, err := c.branchRef(ctx, dir, r.Target, r.TargetSHA, true); err != nil {
			return err
		}
		if err := c.requireClean(ctx, dir); err != nil {
			return err
		}
		message := fmt.Sprintf("Merge branch '%s' into %s", source, target)
		_, err = c.git(ctx, dir, "", "merge", "--no-edit", "--no-autostash", "--no-overwrite-ignore", "-m", message, "--", r.SourceSHA)
		return err
	}
	files, err := c.status(ctx, dir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return merge()
	}
	return c.withStashedChanges(ctx, dir, r.Target, r.TargetSHA, "merge", "merge of "+source+" into "+target, merge)
}
