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
	"github.com/sushidev-team/lola/internal/state"
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
	reviewBudgetOn := d.anyPassOnPROpenLocked()
	reviewBudget := d.reviewCycleBudgetLocked()
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
	// no budget. Installed whenever ANY enabled pass provider triggers on PR-open;
	// the budget is the largest such provider's timeout (each exec self-bounds).
	if reviewBudgetOn {
		parent := d.shutdownCtx
		if parent == nil {
			parent = ctx
		}
		rctx, cancel := context.WithTimeout(parent, reviewBudget)
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
		// An agent-less shell (`lola open`, the manual-shell flow) has no coding
		// agent: its status is pure tmux liveness and it must NEVER reach the
		// reaction / write-back / review / coderabbit engines below (which would
		// send-keys into the human's interactive shell). Refresh it in isolation
		// and skip the whole agent path.
		if s.IsAgentless() {
			if d.observeManualShell(ctx, nat, s) {
				touched = true
			}
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
		// waits, so a stuck "working" is exactly the reported bug; the live pane
		// is the authority that corrects it. The pane owns the AGENT axis only —
		// pre- AND post-PR, because the axes no longer fight: the delivery axis
		// (gh facts) owns the post-PR rollup while the agent axis stays truthful
		// underneath (see agentReconcile).
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
		// axes from this loop's stale snapshot and Upserting them back would
		// silently erase that transition, and permanently so: an agent
		// blocked on a permission prompt fires no further hooks. Update
		// re-reads the CURRENT record under the store lock, so a concurrent
		// hook transition flows into the merge and is preserved.
		now := time.Now()
		prFetchAttempted := s.Branch != "" && repo != ""
		becameDead, applied, titleBackfilled := false, false, false
		updated, known := d.sessions.Update(s.ID, func(cur *session.Session) bool {
			prevStatus := cur.Status
			if backfillTitle != "" && cur.Title == "" {
				cur.Title = backfillTitle
				titleBackfilled = true
			}
			if cur.Repo == "" {
				cur.Repo = repo
			}

			// 1. DELIVERY axis — owned solely by gh facts. A failed fetch keeps
			// the last known facts and counts the failure, so staleness is
			// VISIBLE (PRStale on the wire) instead of silently pinning an
			// hours-old state; facts are never invented.
			deliveryChanged := false
			if prFetchAttempted {
				if prKnown {
					cur.PR = pr
					cur.PRObservedAt = now
					cur.PRFetchFailures = 0
					deliveryChanged = cur.SetDelivery(state.DeriveDelivery(pr, cur.Delivery), now)
				} else {
					cur.PRFetchFailures++
				}
			}

			// 2. AGENT axis — liveness + hooks (already merged into cur) + pane.
			agentChanged := false
			if !alive {
				// A dead pane is terminal for the agent axis; the rollup still
				// reads "merged" when the delivery axis says so (Rollup rule 1).
				agentChanged = cur.SetAgentState(state.AgentDead, "", now)
			} else if paneClassified {
				agentChanged = agentReconcile(cur, paneAct, paneQuestion, now)
			}

			if !alive && !agentChanged && !deliveryChanged {
				// Already-settled terminal record: discard so LastSeen freezes and
				// the store's retention prune eventually drops it — UNLESS we just
				// backfilled its title, which must be committed (Update discards the
				// mutation on a false return).
				return titleBackfilled
			}
			becameDead = cur.Status == "dead" && prevStatus != "dead"
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
				// Flexible review: run every independently-applying provider for this
				// session — each enabled pass provider fires its bounded PR-open chain
				// (guarded once per PR per kind), each watch provider polls its
				// watermark. All no-op when no providers are configured. Re-read for
				// fresh PR / AtPrompt / guard facts.
				d.runReviewProviders(ctx, cur)
			}
			// Flush any hand-off deferred because the worker was mid-turn, once it is
			// idle at its prompt again (re-reads the record itself; one delivered
			// per cycle since a send consumes AtPrompt).
			d.flushReviewHandoffs(ctx, s.ID)
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

// observeManualShell refreshes a manually-opened (`lola open`) session: it has
// no coding agent, so — unlike observeNative's agent path — it gets NO pane
// classification, NO PR derivation, and NO reaction/write-back/review. Its
// status is pure tmux liveness: "shell" while the pane is alive, "dead" once it
// is gone (a dead shell then ages out of the store via the retention prune). An
// alive shell is always re-stamped so its LastSeen stays fresh and a long-lived
// checkout never ages out from under the human. Returns whether it wrote.
func (d *Daemon) observeManualShell(ctx context.Context, nat NativeAPI, s session.Session) bool {
	cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
	alive := nat.Alive(cctx, s)
	cancel()
	wrote := false
	now := time.Now()
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		if cur.TmuxName == "" {
			cur.TmuxName = cur.ID
		}
		if alive {
			cur.SetAgentState(state.AgentShell, "", now)
			wrote = true
			return true // keep LastSeen fresh so an open shell never ages out
		}
		if cur.AgentState == state.AgentDead {
			return false // already settled: freeze LastSeen so retention drops it
		}
		cur.SetAgentState(state.AgentDead, "", now)
		wrote = true
		return true
	})
	return wrote
}

// agentReconcile is the working-vs-waiting authority for the AGENT axis: it
// adjusts a live session's AgentState using the pane classification and
// upholds the invariant that "working" requires POSITIVE evidence of
// activity. It runs pre- AND post-PR — the delivery axis owns the post-PR
// rollup regardless, so pane evidence never fights PR facts; it just keeps
// the agent axis truthful underneath. Returns whether the axis changed.
//
// Precedence (documented, non-flapping):
//
//   - Exited / shell / orphaned sessions are not pane-owned: the pane of an
//     exited agent is a plain shell and must not resurrect any state.
//   - ActivityWorking is positive proof of work: the axis becomes working and
//     LastActivityAt is stamped, trusted even over a STALE hook-set
//     waiting_input (the agent has provably resumed). This is the only
//     upgrade back to working from the pane.
//   - ActivityWaiting is a definite "input box at rest, no spinner" cue.
//     Pre-PR (delivery none) it means blocked-on-a-human regardless of a
//     visible question — an agent resting WITHOUT having produced a PR is
//     waiting for the human either way (unchanged from the pre-axis rule).
//     Post-PR it needs an answerable question to mean blocked (routine
//     post-PR idling must not escalate); without one the axis just settles
//     to idle — truthful underneath, while the rollup still shows the
//     delivery state. AtPrompt is closed on the blocked outcomes only: a
//     bare resting prompt post-PR is exactly where a Stop hook legitimately
//     opened the send-keys gate.
//   - ActivityUnknown does NOT derive working/waiting from the pane — the
//     hook-driven axis stands, so a very recent hook always wins over an
//     ambiguous pane. The one exception is the anti-false-working guard.
//
// Anti-false-working guard (ActivityUnknown only): a working/starting axis
// with no positive activity for longer than staleWorkingThreshold, which the
// pane cannot confirm, must stop asserting work — it downgrades to
// waiting_input when a question/prompt is visible, else to idle. It never
// fires before the threshold (no flapping). An axis that has never recorded
// activity (LastActivityAt zero — an adopted session before its first
// heartbeat) starts the staleness clock from now instead of downgrading on
// first sight.
func agentReconcile(cur *session.Session, act attention.Activity, hasQuestion bool, now time.Time) bool {
	switch cur.AgentState {
	case state.AgentExited, state.AgentShell, state.AgentOrphaned:
		return false // not pane-owned
	}
	switch act {
	case attention.ActivityWorking:
		cur.AtPrompt = false
		cur.AtPromptVerified = true // live positive evidence: the gate state is current
		return cur.SetAgentState(state.AgentWorking, state.SourcePane, now)
	case attention.ActivityWaiting:
		if hasQuestion || cur.Delivery == state.DeliveryNone {
			cur.AtPrompt = false
			cur.AtPromptVerified = true
			changed := cur.SetAgentState(state.AgentWaitingInput, "", now)
			if hasQuestion {
				cur.InputReason = state.InputQuestion
			}
			return changed
		}
		if cur.AgentState == state.AgentWorking || cur.AgentState == state.AgentStarting {
			return cur.SetAgentState(state.AgentIdle, "", now)
		}
		return false
	default: // ActivityUnknown: keep the hook-driven axis, subject to the guard.
		if cur.AgentState != state.AgentWorking && cur.AgentState != state.AgentStarting {
			return false
		}
		if cur.LastActivityAt.IsZero() {
			cur.TouchActivity("", now) // start the clock; grace this cycle
			return false
		}
		if now.Sub(cur.LastActivityAt) <= staleWorkingThreshold {
			return false // still within the activity window: trust the hook
		}
		cur.AtPrompt = false
		if hasQuestion {
			changed := cur.SetAgentState(state.AgentWaitingInput, "", now)
			cur.InputReason = state.InputQuestion
			return changed
		}
		return cur.SetAgentState(state.AgentIdle, "", now)
	}
}
