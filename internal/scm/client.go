// Package scm observes GitHub PR/CI state via the gh CLI (PLAN P1.7).
//
// It is a pure request/fact layer: one gh invocation per call, no internal
// polling loops — the daemon owns cadence — and no status derivation: PR
// facts feed internal/state.DeriveDelivery, the single delivery-axis
// derivation Lola uses everywhere (caps, reactions, reconcile, TUI). scm
// stays a leaf below internal/state, so it must never import it.
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
	"sync"
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
//
// loginOnce/login/loginErr memoize AuthedLogin (see reviewpost.go) so the
// self-feedback filter costs at most ONE `gh api user` exec for the process
// lifetime — the "no new per-cycle gh exec" invariant. A Client therefore
// carries a sync.Once and must only ever be used through a pointer (all
// construction sites already do: &scm.Client{}).
type Client struct {
	GhBin string

	loginOnce sync.Once
	login     string
	loginErr  error
}

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
	bin, err := c.resolveBin()
	if err != nil {
		return nil, err
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

// OpenPR is one open pull request as the PR picker needs it: identity, head
// branch, CI/review/mergeability facts (via the shared checksState), and a
// fork flag (the head is a different owner than the base repo) so the caller can
// refuse push-back tracking on forks. The caller derives any status word from
// these facts (internal/state.DeriveDelivery) — scm ships facts only.
type OpenPR struct {
	Number    int
	Title     string
	Author    string
	Branch    string // headRefName
	IsDraft   bool
	IsFork    bool
	Checks    string // pass | fail | pending | none
	Review    string // APPROVED | CHANGES_REQUESTED | REVIEW_REQUIRED | ""
	Mergeable string // MERGEABLE | CONFLICTING | UNKNOWN
	URL       string
	UpdatedAt string // RFC3339, gh's updatedAt
}

// openPRRow mirrors the wider gh JSON the picker requests.
type openPRRow struct {
	Number            int           `json:"number"`
	Title             string        `json:"title"`
	URL               string        `json:"url"`
	IsDraft           bool          `json:"isDraft"`
	Mergeable         string        `json:"mergeable"`
	ReviewDecision    string        `json:"reviewDecision"`
	StatusCheckRollup []rollupEntry `json:"statusCheckRollup"`
	HeadRefName       string        `json:"headRefName"`
	UpdatedAt         string        `json:"updatedAt"`
	Author            struct {
		Login string `json:"login"`
	} `json:"author"`
	HeadRepositoryOwner struct {
		Login string `json:"login"`
	} `json:"headRepositoryOwner"`
}

// ListOpenPRs returns every open PR in repo ("owner/name") for the picker, newest
// updates first (gh's default order). Any gh failure returns an error — a caller
// must never conflate "could not list" with "no open PRs".
func (c *Client) ListOpenPRs(ctx context.Context, repo string) ([]OpenPR, error) {
	bin, err := c.resolveBin()
	if err != nil {
		return nil, err
	}
	out, err := exec.CommandContext(ctx, bin, "pr", "list",
		"--repo", repo, "--state", "open", "--limit", "50",
		"--json", "number,title,url,isDraft,mergeable,reviewDecision,statusCheckRollup,headRefName,updatedAt,author,headRepositoryOwner",
	).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("gh pr list --repo %s --state open: %w: %s", repo, err, bytes.TrimSpace(ee.Stderr))
		}
		return nil, fmt.Errorf("gh pr list --repo %s --state open: %w", repo, err)
	}
	var rows []openPRRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("gh pr list --repo %s --state open: bad output: %w", repo, err)
	}
	owner := repoOwner(repo)
	prs := make([]OpenPR, 0, len(rows))
	for _, r := range rows {
		prs = append(prs, OpenPR{
			Number:    r.Number,
			Title:     r.Title,
			Author:    r.Author.Login,
			Branch:    r.HeadRefName,
			IsDraft:   r.IsDraft,
			IsFork:    r.HeadRepositoryOwner.Login != "" && owner != "" && !strings.EqualFold(r.HeadRepositoryOwner.Login, owner),
			Checks:    checksState(r.StatusCheckRollup),
			Review:    r.ReviewDecision,
			Mergeable: r.Mergeable,
			URL:       r.URL,
			UpdatedAt: r.UpdatedAt,
		})
	}
	return prs, nil
}

// repoOwner returns the owner segment of an "owner/name" repo, or "" if malformed.
func repoOwner(repo string) string {
	if i := strings.IndexByte(repo, '/'); i > 0 {
		return repo[:i]
	}
	return ""
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
		if isFailingCheckState(s) {
			return "fail" // fail outranks pending: report the break immediately
		}
		switch strings.ToUpper(s) {
		case "PENDING", "QUEUED", "IN_PROGRESS", "WAITING", "REQUESTED", "EXPECTED", "STALE":
			pending = true
		}
	}
	if pending {
		return "pending"
	}
	return "pass"
}

