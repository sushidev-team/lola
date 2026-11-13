package daemon

// requiredchecks.go narrows the CI verdict to the checks the base branch
// REQUIRES.
//
// The delivery axis used to roll up every check on the PR. That is right until
// a repository runs a job it deliberately does not require — a shadow-mode
// suite, a non-blocking audit — at which point that job's red reads as
// ci_failed: lola prompts the worker to "fix" it, the merge queue holds and the
// PR write-back waits. So the facts are re-judged against the base branch's
// required checks, read from the repository's active RULESETS (plain read
// access; classic branch protection needs admin and is not consulted).
//
// Every unknown keeps the stricter all-checks verdict: no seam, a gh failure,
// a branch that requires nothing. A wrong "pass" would let the merge queue land
// a PR GitHub itself would have refused; a wrong "fail" only costs a prompt.

import (
	"context"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/scm"
)

const (
	// requiredChecksTTL bounds how stale a ruleset answer may be. A ruleset is
	// edited by hand, rarely; one gh call per repo/branch per TTL is the cost.
	requiredChecksTTL = 10 * time.Minute
	// requiredChecksRetry is the shorter TTL of a FAILED lookup, so a transient
	// gh error is retried soon without being retried every cycle.
	requiredChecksRetry = 2 * time.Minute
)

type requiredEntry struct {
	checks []string
	at     time.Time
	ttl    time.Duration
}

type requiredCache struct {
	mu      sync.Mutex
	entries map[string]requiredEntry
}

// requiredChecksFor returns the base branch's required check names, or nil
// when they are unknown or none are required. Cached per repo/branch.
func (d *Daemon) requiredChecksFor(ctx context.Context, repo, branch string) []string {
	if d.requiredChecks == nil || repo == "" || branch == "" {
		return nil
	}
	key := repo + "\x00" + branch
	now := time.Now()
	d.required.mu.Lock()
	if e, ok := d.required.entries[key]; ok && now.Sub(e.at) < e.ttl {
		d.required.mu.Unlock()
		return e.checks
	}
	d.required.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
	checks, err := d.requiredChecks(cctx, repo, branch)
	cancel()
	e := requiredEntry{checks: checks, at: now, ttl: requiredChecksTTL}
	if err != nil {
		d.logf("", "observe: required checks for %s@%s unknown, judging every check: %v", repo, branch, err)
		e = requiredEntry{at: now, ttl: requiredChecksRetry}
	}
	d.required.mu.Lock()
	if d.required.entries == nil {
		d.required.entries = map[string]requiredEntry{}
	}
	d.required.entries[key] = e
	d.required.mu.Unlock()
	return e.checks
}

// applyRequiredChecks re-judges pr.ChecksState against its base branch's
// required checks (no-op for a nil PR or unknown requirements).
func (d *Daemon) applyRequiredChecks(ctx context.Context, repo string, pr *scm.PR) {
	if pr == nil {
		return
	}
	pr.ApplyRequired(d.requiredChecksFor(ctx, repo, pr.BaseRef))
}
