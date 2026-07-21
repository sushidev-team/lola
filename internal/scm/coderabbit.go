package scm

// coderabbit.go adds the read-only PR-COMMENT WATCH fetch the [coderabbit]
// feature polls on the observer's cadence: the comments and reviews a reviewer
// bot (CodeRabbit by default) has left on a PR, filtered to those NEWER than a
// caller-held watermark. Like the reaction-content helpers it is best-effort,
// side-effect-free, size-bounded, and every gh invocation is bounded by
// reactionExecTimeout so a hung gh can never wedge the daemon.
//
// One gh call does the whole job: `gh pr view <pr> --json comments,reviews`
// returns both the issue-comment thread (where CodeRabbit posts its summary /
// walkthrough) and the PR reviews (its COMMENTED / CHANGES_REQUESTED bodies), so
// the watch never adds a second exec to the 30s cycle.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// prActivity mirrors `gh pr view <pr> --json comments,reviews`: an object keyed
// by each requested field.
type prActivity struct {
	Comments []issueComment `json:"comments"`
	Reviews  []reviewRow    `json:"reviews"`
}

// issueComment mirrors one entry of the PR's issue-comment thread as gh emits it
// under --json comments: author.login, the body, and the RFC3339 createdAt.
type issueComment struct {
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

// botItem is one matched comment/review, normalized for formatting and sorting.
type botItem struct {
	kind  string // "comment" | "review"
	state string // review state (COMMENTED|CHANGES_REQUESTED|…); "" for comments
	body  string
	at    time.Time
}

// CodeRabbitComments returns the reviewer-bot feedback on a PR that is STRICTLY
// newer than `since`, plus the timestamp of the newest matched item (the new
// watermark). author is matched case-insensitively as a login SUBSTRING (the
// CodeRabbit app posts as "coderabbitai[bot]"); empty defaults to
// DefaultCodeRabbitAuthor's value ("coderabbitai").
//
// Returns (text, latest, nil):
//   - text is the size-bounded (≤reactionMaxBytes) formatted list of the new
//     items, or "" when nothing matched is newer than `since`.
//   - latest is the max timestamp over ALL matched items (new or not), never
//     before `since` — the caller stores it as the next watermark. On a gh error
//     latest is returned == since (no advance) so the watch retries next cycle.
//
// A gh failure IS surfaced as an error (the caller must never mistake "could not
// check" for "no new comments"); items with an unparseable timestamp are skipped
// (they cannot be watermarked safely).
func (c *Client) CodeRabbitComments(ctx context.Context, repo string, pr int, since time.Time, author string) (string, time.Time, error) {
	return c.codeRabbitComments(ctx, repo, pr, since, author, "")
}

// CodeRabbitCommentsExcluding is CodeRabbitComments with a self-feedback guard:
// any comment/review whose author login equals `selfLogin` (case-insensitive,
// exact) is dropped BEFORE author matching, so a review lola itself posted via
// the `github` transport (PostPRComment) is never re-ingested by its own watch
// (PLAN §4.4 / §5.3). An empty selfLogin disables the filter (the default
// author "coderabbitai" already won't collide with lola's gh login, so the
// caller passes "" when AuthedLogin can't be resolved — fail-open).
func (c *Client) CodeRabbitCommentsExcluding(ctx context.Context, repo string, pr int, since time.Time, author, selfLogin string) (string, time.Time, error) {
	return c.codeRabbitComments(ctx, repo, pr, since, author, selfLogin)
}

func (c *Client) codeRabbitComments(ctx context.Context, repo string, pr int, since time.Time, author, selfLogin string) (string, time.Time, error) {
	if author == "" {
		author = "coderabbitai"
	}
	stdout, stderr, err := c.run(ctx, "pr", "view", strconv.Itoa(pr),
		"--repo", repo, "--json", "comments,reviews")
	if err != nil {
		return "", since, ghError(fmt.Sprintf("gh pr view %d --repo %s", pr, repo), err, stderr)
	}
	var act prActivity
	if jsonErr := json.Unmarshal(bytes.TrimSpace(stdout), &act); jsonErr != nil {
		return "", since, fmt.Errorf("gh pr view %d --repo %s: unparseable output: %w", pr, repo, jsonErr)
	}

	needle := strings.ToLower(author)
	self := strings.ToLower(selfLogin)
	matches := func(login string) bool {
		l := strings.ToLower(login)
		if self != "" && l == self {
			return false // never treat lola's own posted comment as bot feedback
		}
		return strings.Contains(l, needle)
	}

	latest := since
	var items []botItem
	consider := func(it botItem) {
		if it.at.After(latest) {
			latest = it.at
		}
		if it.at.After(since) {
			items = append(items, it)
		}
	}
	for _, cm := range act.Comments {
		if !matches(cm.Author.Login) {
			continue
		}
		body := strings.TrimSpace(cm.Body)
		at, err := time.Parse(time.RFC3339, cm.CreatedAt)
		if body == "" || err != nil {
			continue
		}
		consider(botItem{kind: "comment", body: body, at: at})
	}
	for _, r := range act.Reviews {
		if !matches(r.Author.Login) || r.State == "PENDING" {
			continue
		}
		body := strings.TrimSpace(r.Body)
		at, err := time.Parse(time.RFC3339, r.SubmittedAt)
		if body == "" || err != nil {
			continue
		}
		consider(botItem{kind: "review", state: r.State, body: body, at: at})
	}
	if len(items) == 0 {
		return "", latest, nil
	}

	// Oldest-first so the agent reads feedback in the order it was left.
	sort.SliceStable(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })

	var b strings.Builder
	fmt.Fprintf(&b, "New CodeRabbit feedback on PR #%d:\n\n", pr)
	for _, it := range items {
		if it.kind == "review" {
			state := it.state
			if state == "" {
				state = "COMMENTED"
			}
			fmt.Fprintf(&b, "[review %s]\n%s\n\n", state, it.body)
			continue
		}
		fmt.Fprintf(&b, "[comment]\n%s\n\n", it.body)
	}
	return boundHead(strings.TrimSpace(b.String()), reactionMaxBytes), latest, nil
}
