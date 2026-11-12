package projectgit

import (
	"context"
	"errors"
	"strings"
)

func (c Client) publishBranch(ctx context.Context, dir string, r BranchRequest) error {
	if _, err := c.branchRef(ctx, dir, r.Source, r.SourceSHA, true); err != nil {
		return err
	}
	names, err := c.text(ctx, dir, "remote")
	if err != nil {
		return err
	}
	found := false
	for _, name := range strings.Split(names, "\n") {
		if name != "" && name == r.Remote {
			found = true
		}
	}
	if !found {
		return errors.New("choose an existing remote")
	}
	if r.RemoteBranch == "" || strings.HasPrefix(r.RemoteBranch, "-") || strings.HasPrefix(r.RemoteBranch, "refs/") || r.RemoteBranch == "HEAD" {
		return errors.New("enter a valid remote branch name")
	}
	destination := "refs/heads/" + r.RemoteBranch
	if _, err := c.git(ctx, dir, "", "check-ref-format", destination); err != nil {
		return errors.New("enter a valid remote branch name")
	}
	// Explicit refspec and --no-follow-tags avoid publishing unrelated refs.
	// Native rejection preserves a divergent destination; tracking is set only
	// for a successful/up-to-date push. No force, config-wide defaults or checkout.
	_, err = c.git(ctx, dir, "", "-c", "push.followTags=false", "-c", "remote."+r.Remote+".mirror=false", "push", "--set-upstream", "--no-follow-tags", "--", r.Remote, r.Source+":"+destination)
	return err
}
