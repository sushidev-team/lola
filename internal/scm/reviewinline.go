package scm

// reviewinline.go adds the second gh WRITE in the package: PostPRReview creates
// ONE pull-request review carrying a summary body plus anchored inline comments
// — the shape CodeRabbit posts, and the only one GitHub gives a "Resolve
// conversation" button, because only a review THREAD can be resolved. The plain
// issue-comment path (PostPRComment) stays as the fallback for everything this
// one cannot do.
//
// Three properties of the endpoint drive the whole design:
//
//   - `gh pr review` cannot carry inline comments, so this goes through
//     `gh api repos/{repo}/pulls/{n}/reviews` with a JSON body.
//   - The body is JSON, and the findings are UNTRUSTED (diff/CI/pane-derived
//     text). It is therefore built with encoding/json and passed on STDIN via
//     `--input -`, never assembled as a string and never placed in argv: no
//     injection surface, no ARG_MAX limit, nothing untrusted in a process
//     argument or a log line.
//   - The call is ATOMIC. One comment naming a line outside the PR's diff fails
//     the WHOLE review with 422, so the caller must filter its anchors against
//     the diff first (internal/diffanchor) and fall back to a plain comment when
//     the post is rejected anyway. PRReviewTarget fetches the two facts that
//     filtering needs — the head SHA the review pins to, and the diff itself.
//
// event=COMMENT is deliberate and matches PostPRComment's reasoning: a COMMENT
// review is always allowed on lola's own PR (APPROVE / REQUEST_CHANGES on your
// own PR is not), needs no review eligibility, and cannot self-approve anything.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// shaRe accepts a git object id as gh reports it (40 hex chars today, 64 under
// SHA-256). Anything else is treated as "could not resolve the head", never
// passed through to the API.
var shaRe = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// postReviewMaxBytes bounds the JSON request body (~120KB). GitHub's own limit
// is far higher; this is defence in depth so a pathological findings blob can
// never turn into a multi-megabyte request. The caller already bounds the
// summary body (reviewmd.MaxBytes) and each comment (reviewmd.MaxInlineBytes),
// so exceeding this means something upstream is wrong — hence an error rather
// than a clip: clipping JSON produces an invalid request, not a smaller one.
const postReviewMaxBytes = 120 * 1024

// prDiffFullMaxBytes bounds the unified diff fetched for ANCHORING (~1MB,
// head-clipped). It is much larger than prDiffMaxBytes (the 12KB the brain
// summarizes) because every hunk that falls outside it costs its findings their
// inline thread — a clip here silently degrades to the summary body, which is
// the fail-open direction but still a loss.
const prDiffFullMaxBytes = 1024 * 1024

// InlineComment is one anchored review comment: a RIGHT-side line of the PR's
// diff plus the Markdown body of the thread's first comment. Line must be a line
// the diff actually carries (see internal/diffanchor) or the whole review is
// rejected with 422.
type InlineComment struct {
	Path string
	Line int
	Body string
}

// reviewRequest is the REST payload of `POST /repos/{repo}/pulls/{n}/reviews`.
// Field names are the API's snake_case; every value comes from encoding/json, so
// untrusted text is escaped by the encoder rather than by hand.
type reviewRequest struct {
	CommitID string              `json:"commit_id,omitempty"`
	Body     string              `json:"body"`
	Event    string              `json:"event"`
	Comments []reviewRequestNote `json:"comments,omitempty"`
}

// reviewRequestNote is one entry of the request's `comments` array. side=RIGHT
// pins the anchor to the post-change file: lola's findings are about the code as
// it now stands, never about a removed line.
type reviewRequestNote struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

// PostPRReview creates a COMMENT review on PR #pr in repo ("owner/name") with
// `body` as its summary and one anchored thread per entry of `comments`.
//
// commitID pins the review to a specific head SHA (from PRReviewTarget, so the
// anchors and the review describe the same commit); an empty commitID lets
// GitHub default to the PR's current head, which is only safe when the caller
// did not filter against a diff.
//
// It returns an error for every failure mode, wrapped via ghError (stderr only,
// no argv, no body), so the caller can classify permanent (422 unanchorable /
// 403 no write access ⇒ fall back to a plain comment) from transient (5xx,
// timeout ⇒ retry next cycle). Like PostPRComment, an empty (whitespace-only)
// body with no comments is a no-op WITHOUT exec: there is nothing to post.
func (c *Client) PostPRReview(ctx context.Context, repo string, pr int, commitID, body string, comments []InlineComment) error {
	if strings.TrimSpace(body) == "" && len(comments) == 0 {
		return nil
	}
	req := reviewRequest{CommitID: commitID, Body: body, Event: "COMMENT"}
	for _, cm := range comments {
		if cm.Path == "" || cm.Line <= 0 || strings.TrimSpace(cm.Body) == "" {
			// A malformed anchor would 422 the entire review; drop it rather than
			// lose every other thread with it. The caller's summary body still
			// carries the findings it could not anchor.
			continue
		}
		req.Comments = append(req.Comments, reviewRequestNote{
			Path: cm.Path,
			Line: cm.Line,
			Side: "RIGHT",
			Body: cm.Body,
		})
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("gh api pulls/%d/reviews --repo %s: encode: %w", pr, repo, err)
	}
	if len(payload) > postReviewMaxBytes {
		return fmt.Errorf("gh api pulls/%d/reviews --repo %s: payload %d bytes exceeds %d",
			pr, repo, len(payload), postReviewMaxBytes)
	}
	what := fmt.Sprintf("gh api repos/%s/pulls/%d/reviews", repo, pr)
	_, stderr, err := c.runStdin(ctx, payload, "api", "--method", "POST",
		fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, pr), "--input", "-")
	if err != nil {
		return ghError(what, err, stderr)
	}
	return nil
}

// PRReviewTarget fetches the two facts an inline review needs BEFORE it can be
// built: the PR's current head SHA (what the review pins to) and its unified
// diff (what decides which lines may carry a comment).
//
// Both come from gh in two bounded read-only calls. Either failure is surfaced:
// without them the caller must not guess an anchor — it posts a plain comment
// instead. The diff is UNTRUSTED and size-bounded (prDiffFullMaxBytes,
// head-clipped); a clip costs later hunks their anchors, never correctness,
// because a line absent from the parsed diff is simply not anchored.
func (c *Client) PRReviewTarget(ctx context.Context, repo string, pr int) (headSHA, diff string, err error) {
	stdout, stderr, err := c.run(ctx, "pr", "view", strconv.Itoa(pr),
		"--repo", repo, "--json", "headRefOid", "--jq", ".headRefOid")
	if err != nil {
		return "", "", ghError(fmt.Sprintf("gh pr view %d --repo %s --json headRefOid", pr, repo), err, stderr)
	}
	headSHA = strings.TrimSpace(string(stdout))
	if !shaRe.MatchString(headSHA) {
		// A garbled SHA would pin the review to nothing; treat it as "cannot
		// answer" rather than posting against an unknown commit.
		return "", "", fmt.Errorf("gh pr view %d --repo %s: unreadable head sha", pr, repo)
	}
	stdout, stderr, err = c.run(ctx, "pr", "diff", strconv.Itoa(pr), "--repo", repo)
	if err != nil {
		return "", "", ghError(fmt.Sprintf("gh pr diff %d --repo %s", pr, repo), err, stderr)
	}
	return headSHA, boundHead(string(stdout), prDiffFullMaxBytes), nil
}
