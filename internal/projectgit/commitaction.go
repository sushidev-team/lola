package projectgit

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

func (c Client) commitAction(ctx context.Context, dir string, r BranchRequest) error {
	if err := c.requireClean(ctx, dir); err != nil {
		return err
	}
	if err := c.requireHead(ctx, dir, r.TargetSHA); err != nil {
		return err
	}
	current, _ := c.text(ctx, dir, "symbolic-ref", "--quiet", "HEAD")
	if current != r.Target {
		return errors.New("checkout changed since this action was selected; refresh and try again")
	}
	sha, err := c.commitSHA(ctx, dir, r.SourceSHA)
	if err != nil {
		return err
	}
	parents, err := c.text(ctx, dir, "rev-list", "--parents", "-n", "1", sha)
	if err != nil {
		return err
	}
	count := len(strings.Fields(parents)) - 1
	args := []string{"-c", "core.editor=true", r.Action, "--no-edit"}
	if count > 1 {
		if r.Mainline < 1 || r.Mainline > count {
			return errors.New("choose the mainline parent for this merge commit")
		}
		args = append(args, "--mainline", strconv.Itoa(r.Mainline))
	} else if r.Mainline != 0 {
		return errors.New("mainline is only valid for merge commits")
	}
	args = append(args, "--", sha)
	_, err = c.git(ctx, dir, "", args...)
	return err
}
