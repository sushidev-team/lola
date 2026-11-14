package projectgit

import (
	"context"
	"strings"
)

// CanFastForward predicts the normal merge action without changing any refs.
// Unknown/custom merge settings keep the generic Merge label.
func (c Client) CanFastForward(ctx context.Context, dir string, r BranchRequest) (bool, error) {
	if _, err := c.branchRef(ctx, dir, r.Source, r.SourceSHA, false); err != nil {
		return false, err
	}
	if _, err := c.branchRef(ctx, dir, r.Target, r.TargetSHA, true); err != nil {
		return false, err
	}
	if r.SourceSHA == r.TargetSHA {
		return false, nil
	}
	ff, err := c.text(ctx, dir, "config", "--type=bool-or-str", "--default=true", "--get", "merge.ff")
	if err != nil || (ff != "true" && ff != "only") {
		return false, err
	}
	options, err := c.text(ctx, dir, "config", "--default=", "--get", "branch."+strings.TrimPrefix(r.Target, "refs/heads/")+".mergeOptions")
	if err != nil || options != "" {
		return false, err
	}
	_, err = c.git(ctx, dir, "", "merge-base", "--is-ancestor", r.TargetSHA, r.SourceSHA)
	// A failed ancestry check (including incomplete history) cannot prove a fast-forward.
	return err == nil, nil
}
