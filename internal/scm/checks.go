package scm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// LolaStatusPrefix starts the context of every commit status lola posts
// itself (`lola/review/<kind>`). Such a status is a review VERDICT, never CI.
const LolaStatusPrefix = "lola/"

// IsLolaContext reports whether a check/status name is one of lola's own.
func IsLolaContext(name string) bool { return strings.HasPrefix(name, LolaStatusPrefix) }

// Check is one entry of a PR's check rollup: its name (a CheckRun's job name or
// a StatusContext's context — the string a ruleset's required check names) and
// its coarse bucket: pass | fail | pending.
type Check struct {
	Name   string `json:"name"`
	Bucket string `json:"bucket"`
}

// checkBucket maps a CheckRun status/conclusion or a StatusContext state onto
// pass | fail | pending. Anything not failing and not in flight is a pass
// (SUCCESS, NEUTRAL, SKIPPED), exactly as the rollup always read it.
func checkBucket(s string) string {
	if isFailingCheckState(s) {
		return "fail"
	}
	switch strings.ToUpper(s) {
	case "PENDING", "QUEUED", "IN_PROGRESS", "WAITING", "REQUESTED", "EXPECTED", "STALE":
		return "pending"
	}
	return "pass"
}

// ChecksStateAmong rolls checks up to pass | fail | pending | none, the same
// vocabulary as PR.ChecksState, ignoring lola's own statuses.
//
// With a non-empty required list only those checks count: a red check the
// branch does not require (a shadow-mode job, a non-blocking audit) must not
// read as failed CI, because ci_failed makes lola prompt the worker to fix it
// and holds the merge queue and the PR write-back. A required check that has
// not reported at all is PENDING — GitHub shows it as "Expected" and refuses
// the merge until it reports. A required lola status is skipped here: it is
// the review gate's business (see the daemon's reviewgate.go), not CI's.
func ChecksStateAmong(checks []Check, required []string) string {
	req := map[string]bool{}
	for _, r := range required {
		if r != "" && !IsLolaContext(r) {
			req[r] = true
		}
	}
	seen := map[string]bool{}
	counted, pending := 0, false
	for _, c := range checks {
		if IsLolaContext(c.Name) {
			continue
		}
		if len(required) > 0 && !req[c.Name] {
			continue
		}
		seen[c.Name] = true
		counted++
		switch c.Bucket {
		case "fail":
			return "fail" // fail outranks pending: report the break immediately
		case "pending":
			pending = true
		}
	}
	for r := range req {
		if !seen[r] {
			pending = true // required but not reported yet: GitHub's "Expected"
		}
	}
	switch {
	case pending:
		return "pending"
	case counted == 0:
		return "none"
	}
	return "pass"
}

// ApplyRequired re-judges ChecksState against the base branch's required
// checks. A nil required list (unknown, or the branch requires none) leaves the
// all-checks verdict, which is the stricter one. Facts without a breakdown
// (persisted before Checks existed) are left untouched.
func (p *PR) ApplyRequired(required []string) {
	if p == nil || len(required) == 0 || p.Checks == nil {
		return
	}
	p.ChecksState = ChecksStateAmong(p.Checks, required)
}

// ruleRow mirrors one entry of GET /repos/{repo}/rules/branches/{branch}.
type ruleRow struct {
	Type       string `json:"type"`
	Parameters struct {
		RequiredStatusChecks []struct {
			Context string `json:"context"`
		} `json:"required_status_checks"`
	} `json:"parameters"`
}

// RequiredChecks returns the status-check names the ACTIVE rulesets require on
// branch in repo — readable with plain read access, unlike classic branch
// protection, which needs admin and is therefore not consulted. (nil, nil)
// means the branch requires no check. A gh failure is an error: the caller
// must fall back to judging every check, never to "nothing is required".
func (c *Client) RequiredChecks(ctx context.Context, repo, branch string) ([]string, error) {
	if !repoSlugRe.MatchString(repo) || !branchRe.MatchString(branch) || strings.Contains(branch, "..") {
		return nil, fmt.Errorf("required checks: invalid repo %q or branch %q", repo, branch)
	}
	path := "repos/" + repo + "/rules/branches/" + url.PathEscape(branch)
	stdout, stderr, err := c.run(ctx, "api", path)
	if err != nil {
		return nil, ghError("gh api "+path, err, stderr)
	}
	var rows []ruleRow
	if err := json.Unmarshal(stdout, &rows); err != nil {
		return nil, fmt.Errorf("gh api %s: bad output: %w", path, err)
	}
	var out []string
	for _, r := range rows {
		if r.Type != "required_status_checks" {
			continue
		}
		for _, c := range r.Parameters.RequiredStatusChecks {
			if c.Context != "" && !slices.Contains(out, c.Context) {
				out = append(out, c.Context)
			}
		}
	}
	return out, nil
}

// Commit status states GitHub accepts.
const (
	StatusPending = "pending"
	StatusSuccess = "success"
	StatusFailure = "failure"
	StatusError   = "error"
)

// statusDescriptionMax is GitHub's limit on a commit status description.
const statusDescriptionMax = 140

var (
	repoSlugRe      = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	branchRe        = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._/-]*$`)
	statusContextRe = regexp.MustCompile(`^lola/[a-z0-9-]+(/[a-z0-9-]+)*$`)
)

// SetCommitStatus posts a commit status on sha. Only lola's own contexts are
// accepted (statusContextRe), so this can never overwrite a CI status; the
// description is lola's own text, clipped to GitHub's limit. targetURL may be
// empty.
func (c *Client) SetCommitStatus(ctx context.Context, repo, sha, state, statusContext, description, targetURL string) error {
	switch state {
	case StatusPending, StatusSuccess, StatusFailure, StatusError:
	default:
		return fmt.Errorf("invalid commit status state %q", state)
	}
	if !repoSlugRe.MatchString(repo) {
		return fmt.Errorf("invalid repo %q", repo)
	}
	if !shaRe.MatchString(sha) {
		return fmt.Errorf("invalid head sha %q", sha)
	}
	if !statusContextRe.MatchString(statusContext) {
		return fmt.Errorf("invalid status context %q", statusContext)
	}
	args := []string{"api", "-X", "POST", "repos/" + repo + "/statuses/" + sha,
		"-f", "state=" + state, "-f", "context=" + statusContext,
		"-f", "description=" + clipRunes(description, statusDescriptionMax)}
	if strings.HasPrefix(targetURL, "https://") {
		args = append(args, "-f", "target_url="+targetURL)
	}
	_, stderr, err := c.run(ctx, args...)
	if err != nil {
		return ghError("gh api repos/"+repo+"/statuses/"+sha, err, stderr)
	}
	return nil
}

// clipRunes cuts s to at most n runes without splitting a character.
func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
