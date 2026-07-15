package daemon

// review.go wires the PLAN P9 QA BUDDY into the daemon: an OPT-IN,
// EVENT-TRIGGERED CodeRabbit review pass — NOT a persistent second agent. On the
// observer's PR-open transition (opt-in) it runs exactly one bounded `coderabbit
// review` against the session's worktree and hands the findings back to the
// human and (sanitized, idle-gated) to the worker; the manual `lola review
// <session>` command forces the same pass on demand.
//
// Four invariants dominate this file (they are the whole point of the feature):
//
//   - OPT-IN + ZERO REGRESSION. reviewRun is nil unless [review].enabled is true
//     AND coderabbit resolves; every entry point returns immediately then, so a
//     disabled or coderabbit-less daemon behaves exactly as before P9.
//   - BOUNDED + FIRE-ONCE + GRACEFUL. Each pass is ONE `coderabbit review` call
//     wrapped in the review timeout AND in a SINGLE per-cycle budget shared across
//     every session (observeNative's reviewCycleCtx), so a slow/hung coderabbit
//     can never stall the review of later sessions or delay graceful shutdown
//     beyond that one bound — the budget derives from the shutdown-cancellable
//     root, so cancellation aborts the read-only review exec. The auto-trigger
//     fires at most once per PR: ReviewedPR is stamped BEFORE the (long) exec, so
//     a crash or the next 30s cycle cannot double-fire. On any error the pass is
//     skipped gracefully (logged, guard left set — a later commit that changes
//     the PR is a human/CI concern, NOT a re-review loop).
//   - UNTRUSTED OUTPUT. The findings embed attacker-influenceable diff content,
//     so they are untrusted text fit ONLY for a notify body / Linear comment shown
//     to a human, OR — for the worker hand-off — text that FIRST passes the P3
//     sanitizeAgentText control-char stripper AND the AtPrompt idle-gate. They are
//     never a command and never an unsanitized send-keys payload.
//   - NO SECRETS. The review.Client inherits the daemon env (coderabbit's own auth
//     session); this file never reads, sets, or logs a credential, and the
//     review package scrubs any surfaced stderr.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/review"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
)

// reviewNotifyHeadBytes bounds the findings HEAD placed in a notification body:
// a notification is a glanceable summary, not the full (up-to-16KB) review. The
// worker hand-off and the Linear comment carry the full findings.
const reviewNotifyHeadBytes = 600

// buildReview constructs the QA review client for rc, or nil when [review] is
// disabled OR enabled-but-coderabbit-is-unavailable. A nil result is the
// daemon's "review off" signal, so a missing coderabbit degrades gracefully to
// no QA rather than erroring per cycle. The Command override is split to an argv
// (bin + args-minus-base); an empty Command uses the review package default.
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
		return nil // enabled but coderabbit not on PATH: caller logs once, review off
	}
	return cl
}

// setReviewLocked (re)builds the review client and its exec seam from rc. Caller
// holds d.mu. A nil client leaves reviewRun nil, which every entry point treats
// as "review off". Called from Run and handleReload so enabling/disabling the
// buddy (or changing its command/timeout) takes effect live.
func (d *Daemon) setReviewLocked(rc config.ReviewConfig) {
	d.review = buildReview(rc)
	if d.review == nil {
		d.reviewRun = nil
		return
	}
	d.reviewRun = d.review.Review
}

// reviewTimeout is the wall-clock bound applied to a review pass (and to the
// whole per-cycle budget), independent of the review.Client's own bound so the
// observer is protected even if the seam forgot to self-bound. Mirrors
// config.DefaultReviewTimeoutSeconds.
func reviewTimeout(rc config.ReviewConfig) time.Duration {
	if rc.TimeoutSeconds > 0 {
		return time.Duration(rc.TimeoutSeconds) * time.Second
	}
	return config.DefaultReviewTimeoutSeconds * time.Second
}

// setReviewCycleCtx installs (or clears, with nil) the current observe cycle's
// shared review budget context — see the Daemon.reviewCycleCtx field.
func (d *Daemon) setReviewCycleCtx(ctx context.Context) {
	d.reviewMu.Lock()
	d.reviewCycleCtx = ctx
	d.reviewMu.Unlock()
}

// reviewContext returns the observe cycle's shared, shutdown-cancellable review
// budget context when one is active, else fallback. The single (long) review
// exec runs under it so a hung coderabbit is bounded ONCE per cycle, not once
// per session, and is aborted at shutdown.
//
// It is consulted ONLY by the in-cycle auto-trigger (useCycleBudget). The manual
// `lola review` command runs on its OWN socket-handler goroutine, concurrently
// with the observe loop, and must NOT adopt the cycle's budget ctx: a
// concurrently-finishing cycle would cancel the in-flight manual exec (surfacing
// a spurious failure and poisoning the ReviewedPR guard). The manual path passes
// its caller ctx straight to the exec instead, where the review.Client's own
// Timeout still bounds the run.
func (d *Daemon) reviewContext(fallback context.Context) context.Context {
	d.reviewMu.Lock()
	c := d.reviewCycleCtx
	d.reviewMu.Unlock()
	if c == nil {
		return fallback
	}
	return c
}

// isReviewablePROpen reports whether pr is a real, open PR the QA pass should
// trigger on: a genuine PR number in the OPEN state (not merged/closed). This
// mirrors the P4 write-back's PR-open predicate so the review fires on the same
// authoritative fact.
func isReviewablePROpen(pr *scm.PR) bool {
	return pr != nil && pr.Number > 0 && strings.EqualFold(pr.State, "OPEN")
}

// reviewOnPROpen is the observer-driven auto-trigger: it runs ONE QA pass the
// first time native session s has an open PR (opt-in via OnPROpen), guarded so it
// fires at most once per PR (ReviewedPR). It is a no-op when review is off, the
// PR is not open, the auto-trigger is disabled, or this PR was already reviewed.
func (d *Daemon) reviewOnPROpen(ctx context.Context, s session.Session) {
	d.mu.Lock()
	run := d.reviewRun
	rc := d.cfg.Review
	d.mu.Unlock()
	if run == nil || !rc.OnPROpen {
		return
	}
	if s.Source != "native" || !isReviewablePROpen(s.PR) {
		return
	}
	if s.ReviewedPR == s.PR.Number {
		return // already reviewed this PR — never re-run on the 30s cadence
	}
	// In-cycle: the exec runs under the observe cycle's shared per-cycle budget.
	d.runReviewPass(ctx, s, true)
}

// reviewResult is the outcome of one QA pass, for the manual command to report.
// The observer ignores it.
type reviewResult struct {
	Ran      bool   // the review exec ran (findings may be empty = clean)
	Findings string // trimmed, size-capped findings ("" = clean)
	Skipped  string // non-empty ⇒ the pass did not run, with a human reason
	Err      error  // non-nil ⇒ the review exec failed (skipped gracefully)
}

// runReviewPass executes ONE bounded CodeRabbit review for s and routes the
// findings. It is the FORCE executor: it always runs the exec (the auto-trigger's
// ReviewedPR gate lives in reviewOnPROpen; the manual command calls here
// directly to ignore that gate). It stamps ReviewedPR = s.PR.Number BEFORE the
// (long) exec so a crash or the next observer cycle can never double-fire; on
// exec error it logs and LEAVES the guard set (documented above: a changed PR is
// a human/CI concern, not a re-review loop). Returns the outcome for the caller.
//
// useCycleBudget selects the exec context: the in-cycle auto-trigger passes true
// to run under the observe cycle's shared per-cycle budget (reviewContext); the
// manual command passes false so the exec runs under its OWN caller ctx, immune
// to a concurrently-finishing cycle cancelling it (see reviewContext).
func (d *Daemon) runReviewPass(ctx context.Context, s session.Session, useCycleBudget bool) reviewResult {
	d.mu.Lock()
	run := d.reviewRun
	rc := d.cfg.Review
	home := d.home
	p := d.cfg.ProjectByName(s.Project)
	d.mu.Unlock()

	if run == nil {
		return reviewResult{Skipped: "not enabled"}
	}
	if s.Project == "" || p == nil {
		// No resolvable project ⇒ no worktree dir / base branch to review.
		return reviewResult{Skipped: "session has no project to review"}
	}

	// The PR is opened against the project's default branch, so that is the review
	// base. config.Load defaults DefaultBranch, but guard for adopted records.
	base := p.DefaultBranch
	if base == "" {
		base = config.DefaultBranchName
	}
	// The worktree the session runs in: <home>/worktrees/<project>/<id> (see
	// newNativeRuntime / handleKill). The review runs IN this dir.
	dir := filepath.Join(home, "worktrees", p.Name, s.ID)

	// Stamp the one-shot guard BEFORE the long exec so a crash or the next cycle
	// cannot double-fire this PR. A no-op when the PR number already matches
	// (manual re-review of the same PR) or the session has no PR yet (manual pass
	// on a pre-PR branch — nothing to guard).
	if s.PR != nil && s.PR.Number > 0 {
		d.sessions.Update(s.ID, func(cur *session.Session) bool {
			if cur.ReviewedPR == s.PR.Number {
				return false
			}
			cur.ReviewedPR = s.PR.Number
			return true
		})
		d.reviewSave()
	}

	// The one exec. The in-cycle auto-trigger runs it under the cycle's shared
	// budget; the manual command runs it under its own caller ctx (never the
	// cycle's, which a concurrent cycle could cancel out from under it). The
	// review.Client also self-bounds each call to its Timeout, so the effective
	// deadline is min(remaining budget, client timeout).
	execCtx := ctx
	if useCycleBudget {
		execCtx = d.reviewContext(ctx)
	}
	findings, err := run(execCtx, dir, base)
	if err != nil {
		// Graceful skip: log, leave the guard set, do not retry. The findings text
		// is never touched on error, so nothing untrusted is surfaced.
		prNum := 0
		if s.PR != nil {
			prNum = s.PR.Number
		}
		d.logf("", "review: pass for %s (PR #%d) failed, skipping: %v", s.ID, prNum, err)
		return reviewResult{Err: err}
	}

	d.routeReviewFindings(ctx, s, rc, findings)
	return reviewResult{Ran: true, Findings: strings.TrimSpace(findings)}
}

// routeReviewFindings surfaces the (UNTRUSTED) findings at up to three sinks:
//
//   - CLEAN (findings == ""): an optional Info notify ("no issues"), NO worker
//     message, NO Linear comment.
//   - Otherwise ALWAYS: an Action notify with a short HEAD of the findings, so the
//     human (and the attention/pane layer) sees the buddy ran.
//   - If SendToAgent: hand the FULL findings to the worker — sanitized and
//     idle-gated (deferred, never dropped, when the agent is mid-turn).
//   - If CommentOnLinear: post the FULL findings as a Linear comment (P4 path).
//
// A comment at each sink marks it as an untrusted-text destination.
func (d *Daemon) routeReviewFindings(ctx context.Context, s session.Session, rc config.ReviewConfig, findings string) {
	d.mu.Lock()
	notifier := d.notifier
	d.mu.Unlock()
	if notifier == nil {
		notifier = notify.New(notify.NotifyConfig{})
	}

	findings = strings.TrimSpace(findings)
	if findings == "" {
		// Clean review: low-priority Info so the human knows the pass ran and found
		// nothing. Never message the worker; never comment.
		notifier.Notify(ctx, notify.Note{
			Title:    config.ReviewNotifyTitle,
			Body:     fmt.Sprintf("%s: CodeRabbit found no issues", issueLabel(s)),
			Priority: notify.Info,
			URL:      prURL(s),
		})
		d.logf("", "review: %s clean (no issues)", s.ID)
		return
	}

	// Always surface to the human. UNTRUSTED sink #1: a notify body is display-only
	// text, so the diff-derived head is safe here (never a command).
	notifier.Notify(ctx, notify.Note{
		Title:    config.ReviewNotifyTitle,
		Body:     reviewHead(findings, reviewNotifyHeadBytes),
		Priority: notify.Action,
		URL:      prURL(s),
	})
	d.logf("", "review: %s found issues — surfaced to the human", s.ID)

	// UNTRUSTED sink #2: the worker agent. Handed off ONLY through sanitizeAgentText
	// + the AtPrompt idle-gate (sendReviewToAgent), so it can never submit
	// mid-payload or land in a busy pane.
	if rc.SendToAgent {
		d.sendReviewToAgent(ctx, s, findings)
	}

	// UNTRUSTED sink #3: a Linear comment (an API payload shown to a human, so no
	// control-byte sanitization is needed — see renderWriteback).
	if rc.CommentOnLinear {
		d.commentReviewOnLinear(ctx, s, findings)
	}
}

// sendReviewToAgent is the P9 send-keys path for the review hand-off. It enforces
// the SAME send-keys safety gate as the reaction engine's reactSendAgent:
//
//   - Agent not idle at its prompt (AtPrompt false) → the hand-off is DEFERRED:
//     the raw findings are stashed on PendingReviewFindings and a later cycle
//     (flushPendingReview, once a Stop hook sets AtPrompt) delivers them. Nothing
//     is typed.
//   - Agent idle → the sanitized message is rendered, then AtPrompt is CONSUMED
//     atomically (and PendingReviewFindings cleared) in one Store.Update. Only if
//     that atomic consume wins is the text actually sent, so a hook that flipped
//     AtPrompt false meanwhile cancels the send (re-stashed for the next cycle).
//
// findings is the RAW (untrusted) review text; it is sanitized here, immediately
// before the send, and never earlier — so the stash and every other sink hold the
// human-readable text while the pane only ever receives sanitized bytes.
func (d *Daemon) sendReviewToAgent(ctx context.Context, s session.Session, findings string) {
	if s.TmuxName == "" {
		return
	}
	if !s.AtPrompt {
		d.deferReviewHandoff(s.ID, findings)
		d.logf("", "review: %s worker is mid-turn — deferring the review hand-off", s.ID)
		return
	}

	// Sanitize the UNTRUSTED findings before they can reach the pane: strip CR (the
	// send-keys submit vector) and other control bytes so the payload cannot submit
	// mid-transport or steer the line editor (see sanitizeAgentText).
	msg := sanitizeAgentText(config.ReviewToAgentPreamble + findings)

	var (
		sent     bool
		tmuxName string
	)
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		if !cur.AtPrompt {
			return false // a hook resumed the agent between the read above and here
		}
		cur.AtPrompt = false
		cur.PendingReviewFindings = "" // consumed
		tmuxName = cur.TmuxName
		sent = true
		return true
	})
	if !sent {
		// Lost the race: re-stash so the next idle cycle retries, never dropped.
		d.deferReviewHandoff(s.ID, findings)
		d.logf("", "review: %s worker no longer idle at prompt — deferring the hand-off", s.ID)
		return
	}

	sctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	if err := d.sendKeys(sctx, tmuxName, msg); err != nil {
		// The gate is already consumed; do not roll back (that would re-fire and
		// spam). A genuine later idle re-derives nothing — the findings are gone
		// from the stash — so this hand-off is best-effort, like every send.
		d.logf("", "review: send-keys of findings to %s failed: %v", s.ID, err)
		return
	}
	d.reviewSave()
	d.logf("", "review: handed CodeRabbit findings to the worker %s", s.ID)
}

// deferReviewHandoff stashes the raw findings for a later idle-cycle delivery,
// idempotently (a repeat stash of the same text is a no-op), and persists.
func (d *Daemon) deferReviewHandoff(id, findings string) {
	changed := false
	d.sessions.Update(id, func(cur *session.Session) bool {
		if cur.PendingReviewFindings == findings {
			return false
		}
		cur.PendingReviewFindings = findings
		changed = true
		return true
	})
	if changed {
		d.reviewSave()
	}
}

// flushPendingReview delivers a review hand-off deferred earlier (the worker was
// mid-turn) once the worker is idle at its prompt again. It re-reads the record
// itself and no-ops when there is nothing pending or the worker is still busy
// (sendReviewToAgent just re-stashes then). Called every observer cycle.
func (d *Daemon) flushPendingReview(ctx context.Context, id string) {
	s, ok := d.sessions.Get(id)
	if !ok || s.PendingReviewFindings == "" || !s.AtPrompt {
		return
	}
	d.sendReviewToAgent(ctx, s, s.PendingReviewFindings)
}

// commentReviewOnLinear posts the findings as a Linear comment via the P4
// write-back client, best-effort. The body is bounded (the review package caps
// findings at ~16KB) and UNTRUSTED but display-only, so no control-byte
// sanitization is needed. Linear-unavailable / auth failures are logged (and drop
// the cached client on auth error) — never fatal, never blocking the lifecycle.
func (d *Daemon) commentReviewOnLinear(ctx context.Context, s session.Session, findings string) {
	if s.IssueUUID == "" {
		return
	}
	api, err := d.ensureLinear()
	if err != nil {
		d.logf("", "review: linear unavailable for %s comment: %v", s.ID, err)
		return
	}
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	body := config.ReviewNotifyTitle + " findings:\n\n" + findings
	if err := api.CreateComment(cctx, s.IssueUUID, body); err != nil {
		d.wbLinErr("review comment for "+issueLabel(s), err)
		return
	}
	d.logf("", "review: posted CodeRabbit findings as a Linear comment on %s", issueLabel(s))
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

// handleReview serves cmd=review (`lola review <session>`, PLAN P9): it FORCES a
// QA pass now for the named session, ignoring the ReviewedPR one-shot guard, and
// routes the findings the same way the auto-trigger does. It reports a short
// outcome for the CLI to print: skipped (review off / no project), clean, found
// issues, or error. An unknown session is an error.
func (d *Daemon) handleReview(ctx context.Context, sessionID string) (protocol.ReviewData, error) {
	if sessionID == "" {
		return protocol.ReviewData{}, fmt.Errorf("session id required")
	}
	s, ok := d.sessions.Get(sessionID)
	if !ok {
		return protocol.ReviewData{}, fmt.Errorf("unknown session %s", sessionID)
	}

	// force: runReviewPass always runs the exec. useCycleBudget=false — this runs
	// on the socket-handler goroutine, so it must use its OWN ctx, not the observe
	// cycle's budget (which a concurrent cycle could cancel mid-review).
	res := d.runReviewPass(ctx, s, false)
	switch {
	case res.Skipped != "":
		return protocol.ReviewData{
			Skipped: res.Skipped,
			Message: "review skipped: " + res.Skipped,
		}, nil
	case res.Err != nil:
		// Surface the error to the CLI (exits nonzero). The message is the review
		// package's classified, secret-scrubbed error — safe to print.
		return protocol.ReviewData{}, fmt.Errorf("review failed: %w", res.Err)
	case res.Findings == "":
		return protocol.ReviewData{
			Ran:     true,
			Clean:   true,
			Message: fmt.Sprintf("review complete: CodeRabbit found no issues in %s", sessionID),
		}, nil
	default:
		return protocol.ReviewData{
			Ran:      true,
			Findings: res.Findings,
			Message:  "review complete: CodeRabbit reported findings (sent to the configured sinks)\n\n" + reviewHead(res.Findings, reviewNotifyHeadBytes),
		}, nil
	}
}
