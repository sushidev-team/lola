package scm

// reviewpost.go adds the first gh WRITE in the package: PostPRComment posts a
// review provider's findings to a PR as a plain issue-comment (the `github`
// transport of the flexible-review system, PLAN §5.2 + Locked decision 1). It
// is COMMENT-only — never `gh pr review` — because a plain PR comment is always
// allowed on lola's own PR, needs no review-eligibility, and can't self-approve.
//
// The findings body is UNTRUSTED (diff/CI-derived) and multi-KB with newlines,
// so it is passed on STDIN via `--body-file -`, never as an argv token: no
// injection surface, no ARG_MAX limit, and nothing untrusted ever reaches a
// process argument or a log line. gh auth is inherited from the daemon env /
// keychain (never argv), so — like every other seam here — the command carries
// no secret and ghError surfaces only gh's own stderr.
//
// AuthedLogin resolves lola's own gh login once (memoized on the Client) so the
// watch provider can drop lola-posted comments without adding a per-cycle exec.

import (
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"strings"
)

// postCommentMaxBytes bounds the comment body (~16KB, head-clipped). The daemon
// sink also bounds before calling; this is defence in depth so a pathologically
// large findings blob can never blow past gh's own comment size limit. Larger
// than the reaction/watch 4KB budget because a full review's findings carry more
// signal and land on the PR for a human, not into a send-keys payload.
const postCommentMaxBytes = 16 * 1024

// runStdin is the stdin-carrying sibling of run (reaction.go): it feeds `stdin`
// to gh's standard input under the same reactionExecTimeout bound, so the WRITE
// path reuses resolveBin + the timeout + separate stdout/stderr capture. The
// exit error (if any) is returned verbatim for the caller to wrap via ghError.
func (c *Client) runStdin(ctx context.Context, stdin []byte, args ...string) (stdout, stderr []byte, err error) {
	bin, err := c.resolveBin()
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, reactionExecTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

// PostPRComment posts `body` to PR #pr in repo ("owner/name") as a plain
// issue-comment via `gh pr comment <pr> --repo <repo> --body-file -`, with the
// body on STDIN. An empty (or whitespace-only) body returns nil WITHOUT exec —
// there is nothing to post and gh rejects an empty comment. The body is
// head-clipped to postCommentMaxBytes but NOT sanitized: this is a human sink
// (the comment is read on GitHub, never re-fed to the agent as control), so the
// full untrusted findings text is preserved. A gh failure is surfaced (wrapped
// via ghError, stderr-only) so the caller can classify permanent vs transient;
// the caller owns the per-PR settle guard and fail-closed behaviour on a
// missing repo / unauthenticated gh.
func (c *Client) PostPRComment(ctx context.Context, repo string, pr int, body string) error {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	body = boundHead(body, postCommentMaxBytes)
	_, stderr, err := c.runStdin(ctx, []byte(body), "pr", "comment", strconv.Itoa(pr),
		"--repo", repo, "--body-file", "-")
	if err != nil {
		return ghError("gh pr comment "+strconv.Itoa(pr)+" --repo "+repo, err, stderr)
	}
	return nil
}

// AuthedLogin returns the login of the gh-authenticated user
// (`gh api user --jq .login`), memoized so it execs at most ONCE per process —
// this is what lets the self-feedback filter (see CodeRabbitCommentsExcluding)
// avoid adding a second gh call to the 30s watch cycle. On failure it caches and
// returns the error; the caller treats a resolution failure as fail-open (skip
// the filter), so the daemon never retries it per-cycle. The first caller's ctx
// governs the (bounded) exec.
func (c *Client) AuthedLogin(ctx context.Context) (string, error) {
	c.loginOnce.Do(func() {
		stdout, stderr, err := c.run(ctx, "api", "user", "--jq", ".login")
		if err != nil {
			c.loginErr = ghError("gh api user", err, stderr)
			return
		}
		c.login = strings.TrimSpace(string(stdout))
	})
	return c.login, c.loginErr
}
