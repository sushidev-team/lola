package daemon

// review.go holds the coderabbit-cli PASS CLIENT builder, the shared per-cycle
// review budget, and the manual `lola review` force command. The trigger,
// fallback chain, transport dispatch, and kind-keyed guards now live in the
// provider-agnostic reviewer.go — this file keeps only the coderabbit-cli-shaped
// bits (its client construction + timeout) plus the budget/context plumbing and
// small shared helpers (isReviewablePROpen, reviewHead, reviewSave).
//
// The dominant invariants are unchanged and now enforced per provider in
// reviewer.go: opt-in / zero-regression, bounded + fire-once + graceful skip,
// untrusted output (human sinks full text, the worker sink sanitized + idle-gated),
// and no secrets (the review.Client inherits the daemon env; stderr is scrubbed).

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/review"
	"github.com/sushidev-team/lola/internal/scm"
)

// reviewNotifyHeadBytes bounds the findings HEAD placed in a notification body:
// a notification is a glanceable summary, not the full (up-to-16KB) review. The
// worker hand-off and the Linear comment carry the full findings.
const reviewNotifyHeadBytes = 600

// buildReview constructs the coderabbit-cli review client for rc, or nil when it
// is disabled OR enabled-but-coderabbit-is-unavailable. A nil result is the
// caller's "this pass can't answer" signal (a fallback chain skips it), so a
// missing coderabbit degrades gracefully rather than erroring per cycle. The
// Command override is split to an argv (bin + args-minus-base); an empty Command
// uses the review package default.
func buildReview(rc config.ReviewConfig) *review.Client {
	if !rc.Enabled {
		return nil
	}
	cl := &review.Client{}
	if argv := rc.CommandArgs(); len(argv) > 0 {
		cl.Bin = argv[0]
		cl.Args = argv[1:] // review always appends --base itself
	}
	if rc.TimeoutSeconds > 0 {
		cl.Timeout = time.Duration(rc.TimeoutSeconds) * time.Second
	}
	if !cl.Available() {
		return nil // enabled but coderabbit not on PATH: caller logs once, pass off
	}
	return cl
}

// setReviewCycleCtx installs (or clears, with nil) the current observe cycle's
// shared review budget context — see the Daemon.reviewCycleCtx field.
func (d *Daemon) setReviewCycleCtx(ctx context.Context) {
	d.reviewMu.Lock()
	d.reviewCycleCtx = ctx
	d.reviewMu.Unlock()
}

// reviewContext returns the observe cycle's shared, shutdown-cancellable review
// budget context when one is active, else fallback. A pass exec runs under it so
// a hung provider is bounded ONCE per cycle, not once per session, and is aborted
// at shutdown.
//
// It is consulted ONLY by the in-cycle auto-trigger (useCycleBudget). The manual
// `lola review` command runs on its OWN socket-handler goroutine, concurrently
// with the observe loop, and must NOT adopt the cycle's budget ctx: a
// concurrently-finishing cycle would cancel the in-flight manual exec (surfacing
// a spurious failure and poisoning the guard). The manual path passes its caller
// ctx straight to the exec instead, where the client's own Timeout still bounds it.
func (d *Daemon) reviewContext(fallback context.Context) context.Context {
	d.reviewMu.Lock()
	c := d.reviewCycleCtx
	d.reviewMu.Unlock()
	if c == nil {
		return fallback
	}
	return c
}

// isReviewablePROpen reports whether pr is a real, open PR the review should
// trigger on: a genuine PR number in the OPEN state (not merged/closed). Mirrors
// the P4 write-back's PR-open predicate so review fires on the same fact.
func isReviewablePROpen(pr *scm.PR) bool {
	return pr != nil && pr.Number > 0 && strings.EqualFold(pr.State, "OPEN")
}

// reviewResult is the outcome of one pass/chain, for the manual command to report.
// The observer ignores it.
type reviewResult struct {
	Ran      bool   // the review exec ran (findings may be empty = clean)
	Findings string // trimmed, size-capped findings ("" = clean)
	Skipped  string // non-empty ⇒ the pass did not run, with a human reason
	Err      error  // non-nil ⇒ the review exec failed (skipped gracefully)
}

// reviewHead returns the head of s bounded to max bytes (rune-safe), with an
// ellipsis marker when it clips — for a glanceable notification body.
func reviewHead(s string, max int) string {
	if len(s) <= max {
		return s
	}
	keep := max
	for keep > 0 && !utf8ValidBoundary(s, keep) {
		keep--
	}
	return strings.TrimRight(s[:keep], " \n\t") + " …"
}

// utf8ValidBoundary reports whether index i is a UTF-8 rune boundary in s (i.e.
// s[i] is not a continuation byte), so a head clip never splits a rune.
func utf8ValidBoundary(s string, i int) bool {
	if i <= 0 || i >= len(s) {
		return true
	}
	return s[i]&0xC0 != 0x80
}

// reviewSave persists the session store after a review mutated it, logging any
// failure. Review state is best-effort durable — at worst a re-review or re-send
// after a restart.
func (d *Daemon) reviewSave() {
	if err := d.sessions.Save(); err != nil {
		d.logf("", "review: persist sessions: %v", err)
	}
}

// handleReview serves cmd=review (`lola review <session>`): it FORCES a pass now
// for the named session's primary pass provider, ignoring the once-per-PR guard,
// and routes the findings the same way the auto-trigger does. It reports a short
// outcome for the CLI to print: skipped (no provider enabled / no project), clean,
// found issues, or error. An unknown session is an error.
func (d *Daemon) handleReview(ctx context.Context, sessionID string) (protocol.ReviewData, error) {
	return d.handleReviewProvider(ctx, sessionID, "")
}

// handleReviewProvider is handleReview with the Phase 6 `--provider` selector:
// kind picks WHICH pass provider to force (coderabbit-cli | claude-session);
// "" forces the daemon's primary (first enabled) pass provider. A kind naming a
// disabled/absent provider is a "skipped" outcome; a kind naming a WATCH
// provider is an error (its force path is `lola coderabbit`).
func (d *Daemon) handleReviewProvider(ctx context.Context, sessionID, kind string) (protocol.ReviewData, error) {
	if sessionID == "" {
		return protocol.ReviewData{}, fmt.Errorf("session id required")
	}
	s, ok := d.sessions.Get(sessionID)
	if !ok {
		return protocol.ReviewData{}, fmt.Errorf("unknown session %s", sessionID)
	}
	p, ok := d.resolveReviewForce(kind)
	if !ok {
		msg := "review skipped: no review provider is enabled"
		if kind != "" {
			msg = fmt.Sprintf("review skipped: provider %q is not enabled", kind)
		}
		return protocol.ReviewData{Skipped: "not enabled", Message: msg}, nil
	}
	if p.Shape != shapePass {
		return protocol.ReviewData{}, fmt.Errorf("provider %q is a watch provider; force it with `lola coderabbit`", kind)
	}

	// force: runReviewChain always runs the exec (the once-per-PR gate lives in
	// the auto-trigger). useCycleBudget=false — this runs on the socket-handler
	// goroutine, so it must use its OWN ctx, not the cycle's budget.
	res := d.runReviewChain(ctx, s, p, false)
	switch {
	case res.Skipped != "":
		return protocol.ReviewData{
			Skipped: res.Skipped,
			Message: "review skipped: " + res.Skipped,
		}, nil
	case res.Err != nil:
		return protocol.ReviewData{}, fmt.Errorf("review failed: %w", res.Err)
	case res.Findings == "":
		return protocol.ReviewData{
			Ran:     true,
			Clean:   true,
			Message: fmt.Sprintf("review complete: %s found no issues in %s", labelsFor(p.Kind).notifyTitle, sessionID),
		}, nil
	default:
		return protocol.ReviewData{
			Ran:      true,
			Findings: res.Findings,
			Message:  "review complete: reported findings (sent to the configured sinks)\n\n" + reviewHead(res.Findings, reviewNotifyHeadBytes),
		}, nil
	}
}
