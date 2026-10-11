package scm

// prstate.go holds the two gh writes behind a best-of pick
// (internal/daemon/bestof.go): mark the kept attempt's draft PR ready for
// review, and close a discarded attempt's PR. Neither touches a branch — the
// remote branch of a closed PR is left alone, so a discarded attempt can still
// be reopened by hand.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func checkRepoPR(repo string, pr int) error {
	if !strings.Contains(repo, "/") || strings.HasPrefix(repo, "-") {
		return fmt.Errorf("invalid repo %q", repo)
	}
	if pr <= 0 {
		return errors.New("invalid PR number")
	}
	return nil
}

// MarkPRReady takes PR #pr out of draft (`gh pr ready`). An already-ready PR
// is not an error to gh, so the call is idempotent.
func (c *Client) MarkPRReady(ctx context.Context, repo string, pr int) error {
	if err := checkRepoPR(repo, pr); err != nil {
		return err
	}
	_, stderr, err := c.run(ctx, "pr", "ready", strconv.Itoa(pr), "--repo", repo)
	if err != nil {
		return ghError("gh pr ready "+strconv.Itoa(pr)+" --repo "+repo, err, stderr)
	}
	return nil
}

// ClosePR closes PR #pr with comment (lola's own text, never findings or pane
// output) WITHOUT deleting its branch.
func (c *Client) ClosePR(ctx context.Context, repo string, pr int, comment string) error {
	if err := checkRepoPR(repo, pr); err != nil {
		return err
	}
	args := []string{"pr", "close", strconv.Itoa(pr), "--repo", repo}
	if comment = strings.TrimSpace(comment); comment != "" {
		args = append(args, "--comment", comment)
	}
	_, stderr, err := c.run(ctx, args...)
	if err != nil {
		return ghError("gh pr close "+strconv.Itoa(pr)+" --repo "+repo, err, stderr)
	}
	return nil
}
