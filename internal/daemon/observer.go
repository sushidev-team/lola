package daemon

// Session observability (PLAN P1/P2): a read-only observer loop that merges
// lola's native sessions with GitHub PR state (scm), caching the result in a
// session.Store snapshot. The "sessions" socket command serves this cache — a
// client request never execs gh/tmux.

import (
	"context"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/attention"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
)

const observeInterval = 30 * time.Second

// staleWorkingThreshold is the anti-false-working guard's patience: a session
// whose stored status is "working" but which has had NO positive activity
// (session.LastActivityAt — a tool_use/user_prompt hook or an observed
// ActivityWorking pane) for longer than this, AND whose live pane does not
// itself show a working cue, stops being trusted as "working". It is comfortably
// larger than the observeInterval so a briefly-idle-between-hooks agent is not
// downgraded mid-turn: any hook or working pane within the last ~1.5 observe
// cycles keeps the session working. It also subsumes the "a very recent hook
// wins over an Unknown pane" precedence — an Unknown pane never downgrades a
// working status until this window has fully lapsed.
const staleWorkingThreshold = 45 * time.Second

// observePaneLines is how many trailing rows the observer captures to classify
// activity. The classifier only needs the last rendered screen (its status /
// input-box line), so a small tail keeps the per-cycle capture cheap.
const observePaneLines = 50

// observeExecTimeout bounds EVERY external exec (gh/tmux) of an observation
// cycle individually. The cycle runs on a WithoutCancel context (see
// safeObserve), so without per-call deadlines a single wedged gh call (dead
// network mid-TLS, an interactive prompt) would freeze the observer loop
// forever and block graceful shutdown at d.wg.Wait(). Every observer exec is
// read-only and always safe to abort.
const observeExecTimeout = 10 * time.Second

// sessionRetention: sessions not observed for this long age out of the store
// (a session that stops being upserted — a killed native runner — ages out).
const sessionRetention = 24 * time.Hour

// observeLoop runs observation cycles every observeInterval (plus one
// immediately at startup so the TUI has data right away) until shutdown.
// Same lifecycle discipline as reconcileLoop: registered on d.wg, stops on
// ctx cancellation.
func (d *Daemon) observeLoop(ctx context.Context) {
	defer d.wg.Done()
	t := time.NewTicker(observeInterval)
	defer t.Stop()
	d.safeObserve(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.safeObserve(ctx)
		}
	}
}

// safeObserve runs one cycle; a panic or error never crashes the daemon —
// problems surface in the daemon log only.
func (d *Daemon) safeObserve(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			d.logf("", "observe panic (daemon keeps running): %v", r)
		}
	}()
	// Shield the in-flight cycle from the shutdown cancellation like safeTick:
	// a cancelled context would SIGKILL a running gh/tmux exec and could leave
	// a half-written store. The loop itself still stops on ctx.Done, and every
	// exec inside the cycle is individually bounded by observeExecTimeout so
	// the shield can never turn into an indefinite hang.
	d.observe(context.WithoutCancel(ctx))
}

// observe runs one observation cycle over lola's native sessions (PLAN P2).
// ctx is unbounded (WithoutCancel); every exec below carves its own
// observeExecTimeout deadline from it.
func (d *Daemon) observe(ctx context.Context) {
	d.observeNative(ctx)
}

// observeNative merges every native-runtime session already in the
// store with fresh facts — liveness
// via runtime.Alive (native sessions ARE tmux sessions, so TmuxName is the
// session ID), PR state via the session's repo (recorded at spawn from the
// poll's project, config.Project.Repo; the project registry is the fallback
// for adopted records), and status via nativeStatus. A dead pane whose PR is
// not merged becomes "dead"; a stale needs_input just stays needs_input —
// P2 never auto-kills, no matter how old. Settled terminal records (dead, or
// merged with the pane gone) are not re-written, so their LastSeen freezes
// and sessionRetention ages them out of the store. Each record is written via
// Store.Update (atomic read-modify-write), never a stale-snapshot Upsert —
// hook events land concurrently and must not be erased.
func (d *Daemon) observeNative(ctx context.Context) {
	d.mu.Lock()
	nat := d.native
	repoByProject := make(map[string]string, len(d.cfg.Projects))
	for _, p := range d.cfg.Projects {
		repoByProject[p.Name] = p.Repo
	}
	brainOn := d.brainSummarize != nil
	bc := d.cfg.Brain
	reviewOn := d.reviewRun != nil
	rc := d.cfg.Review
	d.mu.Unlock()
	if nat == nil {
		return
	}

	// Per-cycle brain budget (P5.25): the OPT-IN summaries are the one exec on
	// this otherwise 10s-bounded loop that can run for the brain timeout (~120s).
	// Sharing a SINGLE brainTimeout across the whole cycle — derived from the
	// shutdown-cancellable root, not this WithoutCancel ctx — keeps a hung
	// `claude -p` from (a) delaying reactions to every LATER session in the
	// snapshot (the first hung call spends the budget; the rest short-circuit to
	// the generic template) and (b) delaying graceful shutdown (cancellation
	// aborts the read-only claude exec). Off by default → nil, no budget.
	if brainOn {
		parent := d.shutdownCtx
		if parent == nil {
			parent = ctx // no root set (tests) → fall back to the observe ctx
		}
		bctx, cancel := context.WithTimeout(parent, brainTimeout(bc))
		defer cancel()
		d.setBrainCycleCtx(bctx)
		defer d.setBrainCycleCtx(nil)
	}

	// Per-cycle review budget (P9): the QA review pass is the one exec on this
	// loop that can run for the review timeout (~300s). Like the brain, share a
	// SINGLE timeout across the whole cycle — derived from the shutdown-cancellable
	// root, not this WithoutCancel ctx — so a slow/hung `coderabbit review` can
	// neither stall the review of every LATER session in the snapshot (the first
	// slow call spends the budget; the rest abort fast) nor delay graceful
	// shutdown (cancellation aborts the read-only review exec). Off by default →
	// no budget. Only needed when the PR-open auto-trigger can fire (OnPROpen).
	if reviewOn && rc.OnPROpen {
		parent := d.shutdownCtx
		if parent == nil {
			parent = ctx
		}
		rctx, cancel := context.WithTimeout(parent, reviewTimeout(rc))
		defer cancel()
		d.setReviewCycleCtx(rctx)
		defer d.setReviewCycleCtx(nil)
	}

	// Title backfill (best-effort): sessions spawned before Session.Title
	// existed carry no title, so their list row can only show the issue key.
	// Resolve the Linear API once per cycle — nil (key unavailable) simply skips
	// the backfill this cycle; the key is never logged (secrets discipline).
	var lin linear.API
	if a, err := d.ensureLinear(); err == nil {
		lin = a
	}

	touched := false
	for _, s := range d.sessions.Snapshot() {
		if s.Source != "native" {
			continue
		}

		// Fetch a missing title from Linear (bounded, once — the next cycle sees
		// cur.Title set and skips). Kept OUTSIDE the store-lock Update closure.
		backfillTitle := ""
		if lin != nil && s.Title == "" && s.IssueUUID != "" {
			cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
			if t, err := lin.IssueTitle(cctx, s.IssueUUID); err != nil {
				d.logf("", "observe: title backfill for %s (issue %s) failed: %v", s.ID, s.Issue, err)
			} else {
				backfillTitle = t
			}
			cancel()
		}
		cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
		alive := nat.Alive(cctx, s)
		cancel()

		repo := s.Repo
		if repo == "" {
			repo = repoByProject[s.Project]
		}

		// PR state, log-and-continue like the AO half: keep the last known
		// facts unless this cycle produced an authoritative answer.
		var pr *scm.PR
		prKnown := false
		if s.Branch != "" && repo != "" {
			cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
			p, err := d.prForBranch(cctx, repo, s.Branch)
			cancel()
			if err != nil {
				d.logf("", "observe: PR check for native %s (branch %s in %s) failed: %v", s.ID, s.Branch, repo, err)
			} else {
				pr, prKnown = p, true
			}
		}

		// Live-pane activity corroboration (working-vs-waiting BULLETPROOF):
		// capture the pane ONCE this cycle for EVERY alive session. Claude Code
		// hooks do not reliably fire when the agent asks a plain-text question and
		// waits, so a stuck "working" (pre-PR) or an unsurfaced block (post-PR) is
		// exactly the reported bug; the live pane is the authority that corrects it.
		//   - Pre-PR (cur.PR == nil): the hook-driven worklife status (working /
		//     idle / needs_input) stands unchecked, so the pane is its full
		//     authority — see paneReconcile.
		//   - Post-PR: scm.DeriveStatus owns the status, EXCEPT that a definite
		//     waiting pane showing an answerable question still surfaces
		//     needs_input — an agent can ask a plain-text follow-up AFTER opening a
		//     PR (review feedback, "also bump the changelog?") with no reliable
		//     hook, and must not be masked by the PR-derived status.
		// A pane we cannot READ is treated as ActivityUnknown (not skipped): a pane
		// that cannot confirm work must not keep a hook-stuck "working" trusted, so
		// the anti-false-working staleness guard still runs. Classify/Parse are
		// pure reads of the (attacker-influenceable) text and are never executed or
		// trusted; the capture reuses the observer exec budget and aborts on
		// shutdown via the bounded ctx.
		paneClassified := false
		var paneAct attention.Activity
		var paneQuestion bool
		if alive {
			cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
			text, err := d.paneTail(cctx, paneTarget(s), observePaneLines)
			cancel()
			if err != nil {
				d.logf("", "observe: pane capture for native %s failed (treating as unknown): %v", s.ID, err)
				paneAct = attention.ActivityUnknown
			} else {
				// Classify against the session's coding-agent cues (claude|codex|
				// opencode); an empty/legacy Agent parses to Claude, byte-identical
				// to before.
				k := agent.Parse(s.Agent)
				paneAct = attention.Classify(text, k)
				_, paneQuestion = attention.Parse(text, k)
			}
			paneClassified = true
		}

		// Merge this cycle's facts as ONE atomic read-modify-write. The execs
		// above take seconds, and a hook event (needs_input / idle /
		// session_ended) can land on the record meanwhile — deriving the
		// status from this loop's stale snapshot and Upserting it back would
		// silently erase that transition, and permanently so: an agent
		// blocked on a permission prompt fires no further hooks. Update
		// re-reads the CURRENT record under the store lock, so a concurrent
		// needs_input flows into nativeStatus and is preserved.
		now := time.Now()
		becameDead, applied, titleBackfilled := false, false, false
		updated, known := d.sessions.Update(s.ID, func(cur *session.Session) bool {
			if backfillTitle != "" && cur.Title == "" {
				cur.Title = backfillTitle
				titleBackfilled = true
			}
			if cur.Repo == "" {
				cur.Repo = repo
			}
			if prKnown {
				cur.PR = pr
			}
			status := nativeStatus(cur.Status, alive, cur.PR)
			// Reconcile the hook-driven worklife status against the live pane.
			// Only pre-PR (cur.PR == nil) worklife statuses are pane-owned; a
			// PR-derived status stays authoritative EXCEPT for the post-PR waiting
			// backstop below. Guarded on a successful classify this cycle.
			if paneClassified && cur.PR == nil && isWorklife(status) {
				status = paneReconcile(cur, status, paneAct, paneQuestion, now)
			} else if paneClassified && cur.PR != nil && alive &&
				status != "merged" && paneAct == attention.ActivityWaiting && paneQuestion {
				// Post-PR waiting backstop (BULLETPROOF, part 2): a DEFINITE
				// waiting pane (input box at rest, no spinner) that ALSO shows an
				// answerable question is positive evidence the agent is blocked on
				// a human despite the open PR. Surface needs_input so the human is
				// told and P7's cmd=answer is permitted; the existing needs_input
				// rescue in nativeStatus then preserves it across cycles until a
				// hook or working pane moves it on. A merged PR is terminal and
				// never overridden; a bare idle prompt (no question) is left on its
				// PR-derived status so routine post-PR idling is not escalated.
				cur.AtPrompt = false
				status = "needs_input"
			}
			if !alive && status == cur.Status {
				// Already-settled terminal record: discard so LastSeen freezes and
				// the store's retention prune eventually drops it — UNLESS we just
				// backfilled its title, which must be committed (Update discards the
				// mutation on a false return).
				return titleBackfilled
			}
			becameDead = status == "dead" && cur.Status != "dead"
			cur.Status = status
			if cur.TmuxName == "" {
				cur.TmuxName = cur.ID
			}
			applied = true
			return true
		})
		if becameDead {
			d.logf("", "observe: native session %s pane is gone without a merged PR → dead", s.ID)
		}
		if applied || titleBackfilled {
			touched = true
		}
		// React to the just-updated record (PLAN P3): send-keys / notify /
		// merged-cleanup, each fired once per transition and gated on AtPrompt.
		// updated is the current record even when the merge above discarded
		// (a settled-merged session still needs its cleanup fired once).
		if known {
			// P4 Linear write-back BEFORE react: the PR-open and merged
			// transitions (and their one-shot guards) must land before react's
			// merged-cleanup drops the session, so a failed cleanup retries
			// without re-commenting.
			d.writeBack(ctx, updated)
			d.react(ctx, updated)
			// Escalation (blocked) write-back AFTER react — react is what sets
			// Escalated (CI retries exhausted). Re-read the record so the flag
			// react just wrote is visible this cycle. A dropped (merged-cleaned)
			// session is simply gone here, a no-op.
			if cur, ok := d.sessions.Get(s.ID); ok {
				d.writeBackEscalation(ctx, cur)
				// P9 QA review buddy: fire ONE bounded CodeRabbit pass the first
				// time this session has an open PR (opt-in), then deliver any
				// hand-off deferred because the worker was mid-turn. Both no-op
				// when review is off. Re-read for fresh PR / AtPrompt / guard facts.
				d.reviewOnPROpen(ctx, cur)
				// [coderabbit] PR-comment WATCH: poll the open PR for new
				// CodeRabbit (GitHub-app) comments and route them. No-op when the
				// watch is off. Uses the same fresh cur for PR / AtPrompt facts.
				d.coderabbitWatch(ctx, cur)
			}
			// Flush a deferred review / comment hand-off once the worker is idle at
			// its prompt again (each re-reads the record itself).
			d.flushPendingReview(ctx, s.ID)
			d.flushPendingCodeRabbit(ctx, s.ID)
		}
	}
	if !touched {
		return
	}
	d.sessions.PruneOlderThan(sessionRetention)
	if err := d.sessions.Save(); err != nil {
		d.logf("", "observe: persist sessions: %v", err)
	}
}

// nativeStatus derives a native session's status for this cycle from its
// hook-driven current status, tmux pane liveness, and the PR facts in hand:
//
//   - Known PR facts drive scm.DeriveStatus — the shared status vocabulary.
//   - A hook-reported needs_input outranks any PR-derived status while the
//     pane is alive (a human is being waited on), except "merged".
//   - No PR facts → the hook-driven status stands (working / idle / …).
//   - A dead tmux pane forces "dead" unless the PR is merged — a merged PR is
//     the one legitimate way for a native session to end in P2.
func nativeStatus(current string, alive bool, pr *scm.PR) string {
	status := current
	if pr != nil {
		status = scm.DeriveStatus(alive, pr)
	}
	if alive && current == "needs_input" && status != "merged" {
		status = "needs_input"
	}
	if !alive && status != "merged" {
		return "dead"
	}
	return status
}

// isWorklife reports whether status is one of the pre-PR "worklife" states the
// pane classifier is allowed to own — the hook-driven trio that stands unchecked
// until a PR exists. Terminal / PR-derived / session_ended statuses are left
// alone so the pane never fights an authoritative signal.
func isWorklife(status string) bool {
	return status == "working" || status == "idle" || status == "needs_input"
}

// paneReconcile is the working-vs-waiting authority: it adjusts a pre-PR
// worklife status using the live pane classification and upholds the invariant
// that "working" requires POSITIVE evidence of activity. It returns the
// reconciled status and may stamp LastActivityAt / clear AtPrompt on cur.
//
// Precedence (documented, non-flapping):
//
//   - ActivityWorking is positive proof of work: the status becomes "working"
//     and LastActivityAt is stamped, trusted even over a STALE hook-set
//     needs_input (the agent has provably resumed). This is the only upgrade
//     back to working from the pane.
//   - ActivityWaiting is a definite "human awaited" cue (input box at rest, no
//     spinner): the status becomes "needs_input". A definite waiting pane wins
//     over a "working" status (THE BUG FIX — a waiting agent no longer reports
//     working) and reinforces an existing hook-set needs_input. AtPrompt stays
//     false: the agent is at a prompt for the HUMAN, not safe for auto
//     send-keys, and P7's cmd=answer still permits a human reply on needs_input.
//   - ActivityUnknown does NOT derive working/waiting from the pane — the
//     hook-driven status stands, so a very recent hook (working from tool_use /
//     user_prompt, or needs_input from a Notification) always wins over an
//     ambiguous pane. The one exception is the anti-false-working guard below.
//
// Anti-false-working guard (ActivityUnknown only): a "working" status with no
// positive activity for longer than staleWorkingThreshold, which the pane
// cannot confirm, must stop asserting work — it downgrades to needs_input when a
// question/prompt is visible, else to idle. It never fires before the threshold
// (no flapping), and requires the pane to NOT show a working cue (Unknown). A
// working status that has never recorded activity (LastActivityAt zero — a
// freshly adopted/spawned session before its first heartbeat) starts the
// staleness clock from now instead of downgrading on first sight, so a genuinely
// starting agent is given the same grace window rather than flickering to idle.
func paneReconcile(cur *session.Session, status string, act attention.Activity, hasQuestion bool, now time.Time) string {
	switch act {
	case attention.ActivityWorking:
		cur.LastActivityAt = now
		cur.AtPrompt = false
		return "working"
	case attention.ActivityWaiting:
		cur.AtPrompt = false
		return "needs_input"
	default: // ActivityUnknown: keep the hook status, subject to the guard.
		if status != "working" {
			return status
		}
		if cur.LastActivityAt.IsZero() {
			cur.LastActivityAt = now // start the clock; grace this cycle
			return status
		}
		if now.Sub(cur.LastActivityAt) <= staleWorkingThreshold {
			return status // still within the activity window: trust the hook
		}
		cur.AtPrompt = false
		if hasQuestion {
			return "needs_input"
		}
		return "idle"
	}
}
