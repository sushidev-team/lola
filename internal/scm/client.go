// Package scm observes GitHub PR/CI state via the gh CLI (PLAN P1.7).
//
// It is a pure request/derive layer: one gh invocation per call, no internal
// polling loops — the daemon owns cadence. DeriveStatus is the single
// deterministic status derivation Lola uses everywhere (caps, reactions,
// reconcile, TUI).
//
// JSON assumptions (verified against gh 2.x `pr list --json`):
//   - Output is a top-level JSON array of PR objects; `[]` when the branch
//     has no PR (gh exits 0 in that case).
//   - `state` is upper-case: OPEN | CLOSED | MERGED.
//   - `mergeable` is upper-case GraphQL enum: MERGEABLE | CONFLICTING |
//     UNKNOWN; passed through untouched.
//   - `reviewDecision` is APPROVED | CHANGES_REQUESTED | REVIEW_REQUIRED, or
//     "" when the repo requires no review; passed through untouched.
//   - `statusCheckRollup` is an array mixing two GraphQL types:
//     CheckRun{status: QUEUED|IN_PROGRESS|COMPLETED|WAITING|REQUESTED|PENDING,
//     conclusion: SUCCESS|FAILURE|NEUTRAL|SKIPPED|CANCELLED|TIMED_OUT|
//     ACTION_REQUIRED|STARTUP_FAILURE|STALE (set once COMPLETED)} and
//     StatusContext{state: SUCCESS|FAILURE|ERROR|PENDING|EXPECTED}.
//     It is `[]` (or null) when the PR has no checks and no commit statuses.
package scm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// PR is the observed state of one pull request. The JSON tags are for lola's
// own persistence (internal/session snapshots), not for gh output — gh JSON
// is decoded via the unexported prRow.
type PR struct {
	Number         int    `json:"number"`
	URL            string `json:"url"`
	State          string `json:"state"` // OPEN | CLOSED | MERGED
	IsDraft        bool   `json:"is_draft"`
	Mergeable      string `json:"mergeable"`       // MERGEABLE | CONFLICTING | UNKNOWN
	ReviewDecision string `json:"review_decision"` // APPROVED | CHANGES_REQUESTED | REVIEW_REQUIRED | ""
	ChecksState    string `json:"checks_state"`    // pass | fail | pending | none
}

// Client shells out to the gh CLI. GhBin is the binary to invoke; empty means
// resolve "gh" via LookPath (launchd contexts should set an absolute path).
type Client struct{ GhBin string }

// prRow mirrors the gh JSON field names requested via --json.
type prRow struct {
	Number            int           `json:"number"`
	URL               string        `json:"url"`
	State             string        `json:"state"`
	IsDraft           bool          `json:"isDraft"`
	Mergeable         string        `json:"mergeable"`
	ReviewDecision    string        `json:"reviewDecision"`
	StatusCheckRollup []rollupEntry `json:"statusCheckRollup"`
}

// rollupEntry accepts both statusCheckRollup shapes: StatusContext carries
// `state`; CheckRun carries `status` (+ `conclusion` once COMPLETED).
type rollupEntry struct {
	State      string `json:"state"`      // StatusContext
	Status     string `json:"status"`     // CheckRun lifecycle
	Conclusion string `json:"conclusion"` // CheckRun result (when COMPLETED)
}

// PRForBranch returns the most recent PR for branch in repo ("owner/name"),
// or (nil, nil) when the branch has no PR at all. --state all means merged
// and closed PRs are found too; with --limit 1 gh returns the most recently
// created PR first, which is the one Lola cares about. Any gh failure returns
// an error — callers must never conflate "could not check" with "no PR".
func (c *Client) PRForBranch(ctx context.Context, repo, branch string) (*PR, error) {
	bin := c.GhBin
	if bin == "" {
		var err error
		bin, err = exec.LookPath("gh")
		if err != nil {
			return nil, fmt.Errorf("gh not on PATH: %w", err)
		}
	}
	out, err := exec.CommandContext(ctx, bin, "pr", "list",
		"--repo", repo, "--head", branch, "--state", "all", "--limit", "1",
		"--json", "number,url,state,isDraft,mergeable,reviewDecision,statusCheckRollup",
	).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("gh pr list --repo %s --head %s: %w: %s",
				repo, branch, err, bytes.TrimSpace(ee.Stderr))
		}
		return nil, fmt.Errorf("gh pr list --repo %s --head %s: %w", repo, branch, err)
	}
	var rows []prRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("gh pr list --repo %s --head %s: bad output: %w", repo, branch, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	r := rows[0]
	return &PR{
		Number:         r.Number,
		URL:            r.URL,
		State:          r.State,
		IsDraft:        r.IsDraft,
		Mergeable:      r.Mergeable,
		ReviewDecision: r.ReviewDecision,
		ChecksState:    checksState(r.StatusCheckRollup),
	}, nil
}

// checksState collapses a statusCheckRollup array to pass|fail|pending|none.
// Priority: any failure-ish entry → "fail"; else any pending-ish → "pending";
// else (all success-ish: SUCCESS, NEUTRAL, SKIPPED) → "pass"; empty → "none".
// The failure bucket extends the spec's FAILURE/ERROR with gh's other
// terminal-bad conclusions (TIMED_OUT, CANCELLED, ACTION_REQUIRED,
// STARTUP_FAILURE) so a timed-out check never reads as "pass".
func checksState(rollup []rollupEntry) string {
	if len(rollup) == 0 {
		return "none"
	}
	pending := false
	for _, e := range rollup {
		s := e.State // StatusContext
		if s == "" { // CheckRun: in-flight status until COMPLETED, then conclusion
			if e.Status != "" && e.Status != "COMPLETED" {
				s = e.Status
			} else {
				s = e.Conclusion
			}
		}
		switch strings.ToUpper(s) {
		case "FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
			return "fail" // fail outranks pending: report the break immediately
		case "PENDING", "QUEUED", "IN_PROGRESS", "WAITING", "REQUESTED", "EXPECTED", "STALE":
			pending = true
		}
	}
	if pending {
		return "pending"
	}
	return "pass"
}

// DeriveStatus is the single deterministic session-status derivation, applied
// in strict priority order:
//
//	pr == nil            → "working" (session alive) | "no_pr" (dead)
//	State MERGED         → "merged"
//	State CLOSED         → "closed"
//	IsDraft              → "draft"
//	ChecksState fail     → "ci_failed"
//	Mergeable CONFLICTING → "merge_conflict"
//	CHANGES_REQUESTED    → "changes_requested"
//	APPROVED + pass      → "approved"
//	ChecksState pending  → "ci_pending"
//	otherwise            → "review_pending"
//
// Note the deliberate asymmetry: APPROVED only yields "approved" when checks
// pass; APPROVED with no checks at all ("none") stays "review_pending" and
// APPROVED with running checks is "ci_pending" — never park a PR as approved
// while its CI story is incomplete.
//
// merge_conflict (PLAN P1.7, consumed by the P3.18 rebase reaction) sits
// deliberately below ci_failed — a red CI on the agent's own code is the more
// immediate signal, and the rebase that resolves the conflict re-runs CI
// either way — and above the review states: a CONFLICTING PR cannot merge no
// matter what review says, so APPROVED+green+CONFLICTING must never read
// "approved" (which per PLAN semantics means park-and-notify). Mergeable
// UNKNOWN (GitHub still computing) is treated as not conflicting.
func DeriveStatus(sessionAlive bool, pr *PR) string {
	if pr == nil {
		if sessionAlive {
			return "working"
		}
		return "no_pr"
	}
	switch pr.State {
	case "MERGED":
		return "merged"
	case "CLOSED":
		return "closed"
	}
	if pr.IsDraft {
		return "draft"
	}
	if pr.ChecksState == "fail" {
		return "ci_failed"
	}
	if pr.Mergeable == "CONFLICTING" {
		return "merge_conflict"
	}
	if pr.ReviewDecision == "CHANGES_REQUESTED" {
		return "changes_requested"
	}
	if pr.ReviewDecision == "APPROVED" && pr.ChecksState == "pass" {
		return "approved"
	}
	if pr.ChecksState == "pending" {
		return "ci_pending"
	}
	return "review_pending"
}
