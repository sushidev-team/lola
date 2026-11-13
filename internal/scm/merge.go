package scm

// merge.go holds the two gh calls behind lola's local merge queue
// (internal/daemon/mergequeue.go): how far a PR's head is BEHIND its base, and
// the merge itself. The merge is the only gh write that changes a branch, so
// it is pinned to the exact head commit the caller judged (`--match-head-commit`):
// a push that lands between the queue's check and the merge makes GitHub refuse
// the merge instead of landing code nothing approved or tested.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The head sha is checked with shaRe (reviewinline.go) and the base with refRe:
// both reach argv and a URL path, so they are checked rather than trusted.

// refRe is a conservative branch-name shape for the compare URL: no "..",
// no leading "-", nothing that could change the path or the query.
var refRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// BehindBy reports how many commits base has that headSHA lacks, via the REST
// compare endpoint (`repos/<repo>/compare/<base>...<head>` → behind_by). 0 means
// the head already contains the tip of base: merging it lands exactly what CI
// ran on. Any failure is an error — "could not tell" must never read as "up to
// date".
func (c *Client) BehindBy(ctx context.Context, repo, base, headSHA string) (int, error) {
	if !strings.Contains(repo, "/") || strings.HasPrefix(repo, "-") {
		return 0, fmt.Errorf("invalid repo %q", repo)
	}
	if !refRe.MatchString(base) || strings.Contains(base, "..") {
		return 0, fmt.Errorf("invalid base branch %q", base)
	}
	if !shaRe.MatchString(headSHA) {
		return 0, fmt.Errorf("invalid head sha %q", headSHA)
	}
	path := "repos/" + repo + "/compare/" + base + "..." + headSHA
	stdout, stderr, err := c.run(ctx, "api", path, "--jq", ".behind_by")
	if err != nil {
		return 0, ghError("gh api "+path, err, stderr)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(stdout)))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("gh api %s: unexpected behind_by %q", path, strings.TrimSpace(string(stdout)))
	}
	return n, nil
}

// MergePR merges PR #pr in repo with method (squash|merge|rebase), but only if
// its head is still headSHA. The remote branch is left alone: lola's own
// teardown removes the local branch once the PR reads merged, and deleting the
// remote one is the repository's setting to make.
func (c *Client) MergePR(ctx context.Context, repo string, pr int, method, headSHA string) error {
	switch method {
	case "squash", "merge", "rebase":
	default:
		return fmt.Errorf("invalid merge method %q", method)
	}
	if pr <= 0 {
		return errors.New("invalid PR number")
	}
	if !shaRe.MatchString(headSHA) {
		return fmt.Errorf("invalid head sha %q", headSHA)
	}
	_, stderr, err := c.run(ctx, "pr", "merge", strconv.Itoa(pr), "--repo", repo,
		"--"+method, "--match-head-commit", headSHA)
	if err != nil {
		return ghError("gh pr merge "+strconv.Itoa(pr)+" --repo "+repo, err, stderr)
	}
	return nil
}
