package scm

// reaction.go adds the read-only "reaction content" helpers the P3 reaction
// engine feeds to a live agent (PLAN P3.16 ci-failed, P3.17 changes-requested):
// a summary of failing CI checks and the human review feedback on a PR. Both
// return size-bounded plain text meant to be typed into the agent's tmux pane,
// never machine-parsed. They are deliberately best-effort and side-effect-free
// (no writes, no state mutation), and every gh invocation is bounded by
// reactionExecTimeout so a hung gh can never wedge the daemon.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// reactionMaxBytes bounds every reaction content string to ~4KB so a
	// pathological log or review can never blow up a send-keys payload. The
	// returned strings are guaranteed to be at most this many bytes.
	reactionMaxBytes = 4096

	// reactionExecTimeout caps each individual gh invocation these helpers
	// make, independent of the (possibly long-lived) caller context.
	reactionExecTimeout = 30 * time.Second

	// maxLogRuns caps how many distinct Actions runs FailingChecks pulls
	// --log-failed output for, bounding both exec count and peak memory.
	maxLogRuns = 3
)

// truncMarker is appended (head clip) or prepended (tail clip) when content is
// cut to the byte budget; its length is reserved so the result never exceeds
// the cap. "…" is a 3-byte rune, so this constant is 15 bytes.
const truncMarker = "… (truncated)"

// runIDRe extracts the Actions run id from a CheckRun details URL of the form
// https://github.com/<owner>/<repo>/actions/runs/<run-id>[/job/<job-id>].
var runIDRe = regexp.MustCompile(`/actions/runs/(\d+)`)

// resolveBin returns the gh binary to exec: the configured GhBin, else "gh"
// resolved on PATH (launchd contexts should configure an absolute path).
func (c *Client) resolveBin() (string, error) {
	if c.GhBin != "" {
		return c.GhBin, nil
	}
	bin, err := exec.LookPath("gh")
	if err != nil {
		return "", fmt.Errorf("gh not on PATH: %w", err)
	}
	return bin, nil
}

// run executes gh with args under a reactionExecTimeout-bounded child context,
// capturing stdout and stderr separately. The exit error (if any) is returned
// verbatim so callers decide whether a non-zero exit is fatal: `gh pr checks`
// exits non-zero merely because a check is failing/pending, which is NOT an
// error to these helpers.
func (c *Client) run(ctx context.Context, args ...string) (stdout, stderr []byte, err error) {
	bin, err := c.resolveBin()
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, reactionExecTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

// ghError wraps a gh failure with the invoked command and any stderr text (the
// URL/key discipline lives at the caller; these commands carry no secrets).
func ghError(what string, err error, stderr []byte) error {
	if s := bytes.TrimSpace(stderr); len(s) > 0 {
		return fmt.Errorf("%s: %w: %s", what, err, s)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// checkRow mirrors one entry of
// `gh pr checks <pr> --repo <repo> --json name,state,bucket,link,workflow`
// (gh 2.x emits a top-level JSON array). state is the per-check GraphQL
// state/conclusion (SUCCESS|FAILURE|TIMED_OUT|…); bucket is gh's own coarse
// classification (pass|fail|pending|skipping|cancel); link is the details URL;
// workflow is the Actions workflow name (empty for external status contexts).
type checkRow struct {
	Name     string `json:"name"`
	State    string `json:"state"`
	Bucket   string `json:"bucket"`
	Link     string `json:"link"`
	Workflow string `json:"workflow"`
}

// FailingChecks returns a concise, size-bounded (≤reactionMaxBytes) summary of
// the PR's failing CI checks for handing to the agent: each failing check by
// workflow/name, plus the tail of the failed-step logs pulled via
// `gh run view <run-id> --repo <repo> --log-failed` for up to maxLogRuns
// distinct Actions runs.
//
// gh commands (read-only):
//   - gh pr checks <pr> --repo <repo> --json name,state,bucket,link,workflow
//     — the authoritative check list. gh exits non-zero when a check is
//     failing (1) or pending (8); that is NOT a command error, so the JSON on
//     stdout is parsed regardless of exit code. A genuine failure (auth,
//     network) yields no parseable stdout and IS surfaced as an error.
//   - gh run view <run-id> --repo <repo> --log-failed — failed-step logs;
//     strictly best-effort: a fetch failure never fails the whole call.
//
// A PR with no failing checks returns ("", nil) — deliberately distinct from a
// gh error so the caller never mistakes "could not check" for "all green".
// (In practice this is invoked only after DeriveStatus reports ci_failed, so a
// failing check exists; a genuinely check-less PR surfaces gh's "no checks
// reported" as an error, which is acceptable for that never-hit path.)
func (c *Client) FailingChecks(ctx context.Context, repo string, pr int) (string, error) {
	stdout, stderr, err := c.run(ctx, "pr", "checks", strconv.Itoa(pr),
		"--repo", repo, "--json", "name,state,bucket,link,workflow")
	var rows []checkRow
	if jsonErr := json.Unmarshal(bytes.TrimSpace(stdout), &rows); jsonErr != nil {
		// No parseable JSON: distinguish a real gh failure from garbage.
		if err != nil {
			return "", ghError(fmt.Sprintf("gh pr checks %d --repo %s", pr, repo), err, stderr)
		}
		return "", fmt.Errorf("gh pr checks %d --repo %s: unparseable output: %w", pr, repo, jsonErr)
	}

	var failing []checkRow
	for _, r := range rows {
		if r.Bucket == "fail" || isFailingCheckState(r.State) {
			failing = append(failing, r)
		}
	}
	if len(failing) == 0 {
		return "", nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d failing check(s) on PR #%d:\n", len(failing), pr)
	for _, r := range failing {
		name := r.Name
		if r.Workflow != "" {
			name = r.Workflow + " / " + r.Name
		}
		state := strings.ToUpper(r.State)
		if state == "" {
			state = "FAILURE"
		}
		fmt.Fprintf(&b, "- %s: %s\n", name, state)
		if r.Link != "" {
			fmt.Fprintf(&b, "  %s\n", r.Link)
		}
	}
	names := b.String()

	logs := c.failedLogs(ctx, repo, failing)
	if logs == "" {
		return boundHead(names, reactionMaxBytes), nil
	}
	prefix := "\n--- failed step logs (tail) ---\n"
	budget := reactionMaxBytes - len(names) - len(prefix)
	if budget < 80 { // no room for meaningful logs; ship the named checks alone
		return boundHead(names, reactionMaxBytes), nil
	}
	return names + prefix + tailBytes(logs, budget), nil
}

// failedLogs pulls `gh run view <run-id> --repo <repo> --log-failed` for the
// Actions runs referenced by the failing checks (deduped, capped at
// maxLogRuns). Best-effort: checks without an Actions run link (external
// status contexts) and any run whose fetch fails are skipped; each run's
// output is tailed so the aggregate stays bounded.
func (c *Client) failedLogs(ctx context.Context, repo string, failing []checkRow) string {
	seen := map[string]bool{}
	var b strings.Builder
	for _, r := range failing {
		m := runIDRe.FindStringSubmatch(r.Link)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		if len(seen) > maxLogRuns {
			break
		}
		out, _, err := c.run(ctx, "run", "view", m[1], "--repo", repo, "--log-failed")
		if err != nil && len(bytes.TrimSpace(out)) == 0 {
			continue
		}
		log := strings.TrimRight(string(out), "\n")
		if strings.TrimSpace(log) == "" {
			continue
		}
		fmt.Fprintf(&b, "\n[run %s]\n%s\n", m[1], tailBytes(log, reactionMaxBytes))
	}
	return strings.TrimSpace(b.String())
}

// reviewsEnvelope mirrors `gh pr view <pr> --json reviews`, which returns an
// object keyed by the requested field (not a bare array).
type reviewsEnvelope struct {
	Reviews []reviewRow `json:"reviews"`
}

// reviewRow is one PR review: author.login, the summary body, and the review
// state (APPROVED|CHANGES_REQUESTED|COMMENTED|DISMISSED|PENDING).
type reviewRow struct {
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Body        string `json:"body"`
	State       string `json:"state"`
	SubmittedAt string `json:"submittedAt"`
}

// inlineComment mirrors one entry of the REST review-comment list
// (`gh api repos/<repo>/pulls/<pr>/comments`), which uses snake_case. line is
// null (0) for outdated comments, in which case original_line still anchors it.
type inlineComment struct {
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	Body         string `json:"body"`
	Path         string `json:"path"`
	Line         int    `json:"line"`
	OriginalLine int    `json:"original_line"`
}

// ReviewComments returns a readable, size-bounded (≤reactionMaxBytes) list of
// the human review feedback on a PR — the summary bodies of CHANGES_REQUESTED
// and COMMENTED reviews, followed by inline review comments — for handing to
// the agent.
//
// gh commands (read-only):
//   - gh pr view <pr> --repo <repo> --json reviews — returns
//     {"reviews":[{author:{login}, body, state, submittedAt}, …]}. Only
//     CHANGES_REQUESTED and COMMENTED reviews with a non-empty body are kept
//     (APPROVED/DISMISSED/PENDING and empty summaries dropped). A gh failure
//     here IS surfaced as an error.
//   - gh api repos/<repo>/pulls/<pr>/comments — the REST review-comment
//     (inline) list: {user:{login}, body, path, line, original_line}. First
//     page only (≤30; unpaginated to keep the output a single JSON array),
//     strictly best-effort: a failure here is swallowed and the review bodies
//     are still returned.
//
// No qualifying feedback returns ("", nil), distinct from a gh error.
func (c *Client) ReviewComments(ctx context.Context, repo string, pr int) (string, error) {
	stdout, stderr, err := c.run(ctx, "pr", "view", strconv.Itoa(pr),
		"--repo", repo, "--json", "reviews")
	if err != nil {
		return "", ghError(fmt.Sprintf("gh pr view %d --repo %s", pr, repo), err, stderr)
	}
	var env reviewsEnvelope
	if jsonErr := json.Unmarshal(bytes.TrimSpace(stdout), &env); jsonErr != nil {
		return "", fmt.Errorf("gh pr view %d --repo %s: unparseable output: %w", pr, repo, jsonErr)
	}

	var b strings.Builder
	for _, r := range env.Reviews {
		if r.State != "CHANGES_REQUESTED" && r.State != "COMMENTED" {
			continue
		}
		body := strings.TrimSpace(r.Body)
		if body == "" {
			continue
		}
		author := r.Author.Login
		if author == "" {
			author = "unknown"
		}
		fmt.Fprintf(&b, "%s (%s): %s\n\n", author, r.State, body)
	}
	reviews := strings.TrimSpace(b.String())

	inline := c.inlineComments(ctx, repo, pr)

	switch {
	case reviews == "" && inline == "":
		return "", nil
	case inline == "":
		return boundHead("Review feedback on PR #"+strconv.Itoa(pr)+":\n\n"+reviews, reactionMaxBytes), nil
	case reviews == "":
		return boundHead("Inline review comments on PR #"+strconv.Itoa(pr)+":\n\n"+inline, reactionMaxBytes), nil
	default:
		return boundHead("Review feedback on PR #"+strconv.Itoa(pr)+":\n\n"+reviews+
			"\n\nInline comments:\n"+inline, reactionMaxBytes), nil
	}
}

// inlineComments formats the PR's inline review comments, or "" when there are
// none or the fetch fails (best-effort — inline comments are a bonus on top of
// the review bodies, never a reason to fail ReviewComments).
func (c *Client) inlineComments(ctx context.Context, repo string, pr int) string {
	stdout, _, err := c.run(ctx, "api", fmt.Sprintf("repos/%s/pulls/%d/comments", repo, pr))
	if err != nil {
		return ""
	}
	var comments []inlineComment
	if json.Unmarshal(bytes.TrimSpace(stdout), &comments) != nil {
		return ""
	}
	var b strings.Builder
	for _, cm := range comments {
		body := strings.TrimSpace(cm.Body)
		if body == "" {
			continue
		}
		author := cm.User.Login
		if author == "" {
			author = "unknown"
		}
		line := cm.Line
		if line == 0 {
			line = cm.OriginalLine
		}
		switch {
		case cm.Path != "" && line > 0:
			fmt.Fprintf(&b, "- %s on %s:%d: %s\n", author, cm.Path, line, body)
		case cm.Path != "":
			fmt.Fprintf(&b, "- %s on %s: %s\n", author, cm.Path, body)
		default:
			fmt.Fprintf(&b, "- %s: %s\n", author, body)
		}
	}
	return strings.TrimSpace(b.String())
}

// prDiffMaxBytes bounds the unified diff PRDiff returns (~12KB, head-clipped).
// It is the size-bound at the source for the P5 approved-summary context; the
// brain caps its stdin again at its own maxContextBytes, so this is defence in
// depth against a pathologically large PR blowing up the claude context.
const prDiffMaxBytes = 12 * 1024

// PRDiff returns the PR's unified diff ("gh pr diff <pr> --repo <repo>"),
// size-bounded to ~prDiffMaxBytes (head-clipped: the file list and first hunks
// carry the most signal). It is the read-only context the P5 brain summarizes
// at approved+green. The diff is UNTRUSTED (attacker-authored) — it is only ever
// fed to the summarizer as input and shown to a human, never executed or typed
// into an agent. A gh failure is surfaced as an error so the caller falls back
// to its generic notification.
func (c *Client) PRDiff(ctx context.Context, repo string, pr int) (string, error) {
	stdout, stderr, err := c.run(ctx, "pr", "diff", strconv.Itoa(pr), "--repo", repo)
	if err != nil {
		return "", ghError(fmt.Sprintf("gh pr diff %d --repo %s", pr, repo), err, stderr)
	}
	return boundHead(string(stdout), prDiffMaxBytes), nil
}

// isFailingCheckState reports whether a CheckRun conclusion / StatusContext
// state is a terminal-bad outcome. Single source of truth shared by
// checksState (status derivation) and FailingChecks (reaction content), so a
// state that reads ci_failed always yields at least one "failing check".
func isFailingCheckState(s string) bool {
	switch strings.ToUpper(s) {
	case "FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
		return true
	}
	return false
}

// boundHead clips s to at most max bytes, keeping the HEAD (earliest content),
// cutting back to a line boundary and appending truncMarker. Used where the
// earliest lines matter most (the named checks, the first reviews).
func boundHead(s string, max int) string {
	if len(s) <= max {
		return s
	}
	keep := max - len(truncMarker) - 1 // -1 for the joining newline
	if keep < 0 {
		keep = 0
	}
	cut := s[:keep]
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i]
	}
	return cut + "\n" + truncMarker
}

// tailBytes clips s to at most max bytes, keeping the TAIL (latest content),
// advancing to a line boundary and prepending truncMarker. Used for logs,
// where the failure is at the end.
func tailBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	keep := max - len(truncMarker) - 1
	if keep < 0 {
		keep = 0
	}
	cut := s[len(s)-keep:]
	if i := strings.IndexByte(cut, '\n'); i >= 0 && i+1 < len(cut) {
		cut = cut[i+1:]
	}
	return truncMarker + "\n" + cut
}
