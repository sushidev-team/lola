package daemon

// Session observability (PLAN P1/P2): a read-only observer loop that merges
// lola's native sessions with GitHub PR state (scm), caching the result in a
// session.Store snapshot. The "sessions" socket command serves this cache — a
// client request never execs gh/tmux.

import (
	"context"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/agentlog"
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

// transcripts is the observer's rendering-INDEPENDENT second opinion on the
// agent axis: internal/agentlog reads the coding agent's own JSONL transcript
// (whose path every claude hook already reports onto Session.TranscriptPath)
// and says whether a turn is in flight. See agentReconcile for where its
// verdict sits in the precedence, and the agentlog package doc for why the
// pane alone is not a safe sole corroborator.
//
// It is a package-level var rather than a Daemon field only because it is a
// pure cache: keyed by ABSOLUTE path, holding no config and no lifecycle, with
// its own mutex. Two Daemons in one process (the tests) therefore share it
// harmlessly — every entry is validated against the file's own size and mtime
// before it is used, so the worst a shared entry can do is be discarded. Move
// it onto the Daemon the day it acquires either of those two properties.
var transcripts = agentlog.NewReader()

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
	// Before the session work: has the machine moved? A laptop that changes
	// networks leaves the phone listener bound to addresses that no longer
	// exist — silently, and nothing else would ever notice, since reload only
	// rebinds when [remote] changed and nothing about the config changes when
	// the Wi-Fi does. One interface enumeration, and a no-op for every bind
	// mode except "lan".
	d.reconcileRemoteBind()
	d.observeNative(ctx)
}

// observeNative merges every native-runtime session already in the
// store with fresh facts — liveness
// via runtime.Alive (native sessions ARE tmux sessions, so TmuxName is the
// session ID), PR state via the session's repo (recorded at spawn from the
// poll's project, config.Project.Repo; the project registry is the fallback
// for adopted records), and the two AXES: the delivery axis from those PR
// facts (DeriveDelivery) and the agent axis from liveness, the hooks already
// merged into the record, the pane classifier and tmux's own activity stamp
// (agentReconcile). A pane that is gone forces the agent axis to dead; a
// session parked on a human just stays parked — the observer never auto-kills,
// no matter how old. Settled terminal records (a dead pane, or a merged PR
// whose pane is gone) are not re-written, so their LastSeen freezes and
// sessionRetention ages them out of the store. Each record is written via
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

	// No per-cycle review budget lives here any more: the pass no longer runs on
	// this loop at all. The observer queues it and the review worker
	// (reviewworker.go) runs one pass at a time on the cancellable run context,
	// so a slow provider delays neither the rest of this cycle nor shutdown.

	// Title backfill (best-effort): sessions spawned before Session.Title
	// existed carry no title, so their list row can only show the issue key.
	// Resolve the Linear API once per cycle — nil (key unavailable) simply skips
	// the backfill this cycle; the key is never logged (secrets discipline).
	var lin linear.API
	if a, err := d.ensureLinear(); err == nil {
		lin = a
	}

	// ONE `tmux ls` answers liveness for every session this cycle and carries
	// #{session_activity} (tmux's own "when did the pane last emit bytes"
	// stamp). On error — or without the seam — fall back to the per-session
	// Alive probes, with no activity signal (zero time = unknown).
	var (
		aliveByName    map[string]bool
		activityByName map[string]time.Time
	)
	if d.listTmuxSessions != nil {
		cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
		ls, err := d.listTmuxSessions(cctx)
		cancel()
		if err != nil {
			d.logf("", "observe: tmux ls failed (falling back to per-session probes): %v", err)
		} else {
			aliveByName = make(map[string]bool, len(ls))
			activityByName = make(map[string]time.Time, len(ls))
			for _, ts := range ls {
				aliveByName[ts.Name] = true
				if !ts.Activity.IsZero() {
					activityByName[ts.Name] = ts.Activity
				}
			}
		}
	}
	sessionAlive := func(s session.Session) (alive bool, activity time.Time) {
		if aliveByName != nil {
			return aliveByName[paneTarget(s)], activityByName[paneTarget(s)]
		}
		cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
		defer cancel()
		return nat.Alive(cctx, s), time.Time{}
	}

	touched := false
	// Dev tabs are derived from the same listing, for EVERY session — including
	// the agentless `lola open` shells the agent loop below skips, which are
	// exactly the sessions a human checks a PR out in and wants the dev server
	// on. Without the listing (the fallback path) the last known state stands.
	if aliveByName != nil && d.reconcileDevTabs(ctx, aliveByName) {
		touched = true
	}
	interpretQueued := 0
	d.mu.Lock()
	interpretPerCycle := d.cfg.StatusAgent.MaxPerCycle
	d.mu.Unlock()
	for _, s := range d.sessions.Snapshot() {
		if s.Source != "native" {
			continue
		}
		alive, tmuxActivity := sessionAlive(s)
		// An agent-less shell (`lola open`, the manual-shell flow) has no coding
		// agent: its status is pure tmux liveness and it must NEVER reach the
		// reaction / write-back / review / coderabbit engines below (which would
		// send-keys into the human's interactive shell). Refresh it in isolation
		// and skip the whole agent path.
		if s.IsAgentless() {
			if d.observeManualShell(s, alive) {
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
		// The pane is no longer the SOLE corroborator: the transcript read below
		// is a second, rendering-independent opinion for exactly the cases where
		// the pane's cues are weakest. See agentReconcile for the precedence.
		paneClassified := false
		var paneAct attention.Activity
		var paneQuestion bool
		transcriptSays := agentlog.Unknown
		if alive {
			// Classify against the session's coding-agent cues (claude|codex|
			// opencode); an empty/legacy Agent parses to Claude, byte-identical
			// to before.
			k := agent.Parse(s.Agent)
			cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
			text, err := d.paneTail(cctx, paneTarget(s), observePaneLines)
			cancel()
			if err != nil {
				d.logf("", "observe: pane capture for native %s failed (treating as unknown): %v", s.ID, err)
				paneAct = attention.ActivityUnknown
			} else {
				paneAct = attention.Classify(text, k)
				_, paneQuestion = attention.Parse(text, k)
			}
			paneClassified = true

			// The rendering-INDEPENDENT corroborator, read beside the pane and
			// consulted by agentReconcile only where the pane's evidence is weak.
			// Three gates, each load-bearing:
			//   - ONLY CLAUDE writes a transcript. TranscriptPath is populated
			//     exclusively from claude-code hook payloads, so a codex/opencode
			//     session must not even stat a file; internal/agentlog is a
			//     stdlib-only leaf and cannot make this check itself.
			//   - Only a LIVE pane is read. A dead pane is terminal for the agent
			//     axis (the branch below sets AgentDead outright), so the answer
			//     could not be used — and a killed session's transcript would go
			//     on claiming a tool was in flight for workingClaimMaxAge.
			//   - No exec deadline wraps it, unlike every gh/tmux call on this
			//     loop, because there is nothing to bound: os.Stat and one
			//     ReadAt on a local file take no context, and wrapping a
			//     page-cache read in a goroutine to time-box it would leak the
			//     goroutine in the pathological case instead of fixing it. The
			//     WORK is bounded instead (one stat; at most 64KB read, and only
			//     when the file grew), and the path is always the local
			//     ~/.claude/projects/… the agent itself reported — the same
			//     assumption statusagentwire.go's tailFile already makes on this
			//     same loop.
			if k == agent.Claude {
				transcriptSays = transcripts.Verdict(s.TranscriptPath, time.Now())
			}
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
			prevAgent := cur.AgentState
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

			// 2. AGENT axis — liveness + hooks (already merged into cur) + pane
			// + tmux's own activity stamp.
			agentChanged := false
			if !alive {
				// A dead pane is terminal for the agent axis; the rollup still
				// reads "merged" when the delivery axis says so (Rollup rule 1).
				agentChanged = cur.SetAgentState(state.AgentDead, "", now)
			} else {
				// Fresh pane output SUSTAINS a working/starting axis (refreshing
				// the anti-false-working anchor before the guard below reads it)
				// but never upgrades a resting axis — output is not a new turn
				// (idle agents redraw their prompt too). Applied first so codex/
				// opencode sessions, whose hook set has no tool_use heartbeat,
				// stop being downgraded while genuinely working.
				if !tmuxActivity.IsZero() && tmuxActivity.After(cur.LastActivityAt) &&
					(cur.AgentState == state.AgentWorking || cur.AgentState == state.AgentStarting) {
					cur.TouchActivity(state.SourceTmuxActivity, tmuxActivity)
				}
				if paneClassified {
					agentChanged = agentReconcile(cur, paneAct, paneQuestion, transcriptSays, now)
				}
			}

			if !alive && !agentChanged && !deliveryChanged {
				// Already-settled terminal record: discard so LastSeen freezes and
				// the store's retention prune eventually drops it — UNLESS we just
				// backfilled its title, which must be committed (Update discards the
				// mutation on a false return).
				return titleBackfilled
			}
			// The anomaly worth a log line is the AGENT axis going dead while
			// the PR is still in play; a pane that disappears after its PR
			// merged is the normal close-out (react's merged cleanup killed
			// it). Read off the rolled-up status that exclusion was implicit —
			// Rollup only returns "dead" when the delivery axis is not merged —
			// so it is stated here now that nothing else reads the rollup.
			becameDead = cur.AgentState == state.AgentDead && prevAgent != state.AgentDead &&
				cur.Delivery != state.DeliveryMerged
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
			// Agent fallback (SUSHI-585): a quota-limited pane maps to
			// waiting_input/quota_limited on the agent axis; the engine notifies
			// once per episode or auto-switches per the configured chain.
			d.maybeFallback(ctx, updated)
			// Escalation (blocked) write-back AFTER react — react is what sets
			// Escalated (CI retries exhausted). Re-read the record so the flag
			// react just wrote is visible this cycle. A dropped (merged-cleaned)
			// session is simply gone here, a no-op.
			if cur, ok := d.sessions.Get(s.ID); ok {
				d.writeBackEscalation(ctx, cur)
				// Flexible review: watch providers poll their watermark inline (one
				// bounded gh call); each enabled PASS provider is QUEUED for the review
				// worker, which runs it off this loop — a pass exec takes minutes and
				// used to stall the whole cycle. All no-op when no providers are
				// configured. Re-read for fresh PR / AtPrompt / guard facts.
				d.queueReviewProviders(ctx, cur)
			}
			// Flush any hand-off deferred because the worker was mid-turn, once it is
			// idle at its prompt again (re-reads the record itself; one delivered
			// per cycle since a send consumes AtPrompt).
			d.flushReviewHandoffs(ctx, s.ID)
			// Status interpreter, ambiguous-state sweep: a session whose
			// deterministic story is thin (waiting on a human, unreadable pane,
			// long-quiet "working") queues an interpretation — capped per cycle,
			// debounced + hash-deduped by the worker itself.
			if interpretQueued < interpretPerCycle && alive &&
				shouldInterpretAmbiguous(updated, paneClassified && paneAct == attention.ActivityUnknown, now) {
				if d.maybeQueueInterpret(updated.ID) {
					interpretQueued++
				}
			}
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
func (d *Daemon) observeManualShell(s session.Session, alive bool) bool {
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
// adjusts a live session's AgentState using the pane classification, the
// agent's own transcript, and the invariant that "working" requires POSITIVE
// evidence of activity. It runs pre- AND post-PR — the delivery axis owns the
// post-PR rollup regardless, so pane evidence never fights PR facts; it just
// keeps the agent axis truthful underneath. Returns whether the axis changed.
//
// # Two corroborators, and why the second one exists
//
// attention.Classify used to be the ONLY corroborator, and it is a MIRROR OF
// CLAUDE-CODE'S RENDERING — nine of its cues carry an explicit "Fragility:"
// note saying a reworded build slips past them, and two such rewordings have
// each cost a debugging session (the U+00A0-padded caret; the always-drawn
// composer — see CLAUDE.md). The failure mode is the bad one: no error, no log
// line, a load-bearing gate silently disabled.
//
// agentlog reads the agent's OWN transcript instead (internal/agentlog), which
// is a fact about its conversation state rather than about how it paints a
// terminal this month. It is a second opinion, never a replacement — it cannot
// see the screen at all, so it slots in BELOW every pane outcome that carries
// positive evidence and ABOVE the two that do not.
//
// # Precedence (documented, non-flapping)
//
//   - Exited / shell / orphaned sessions are not pane-owned: the pane of an
//     exited agent is a plain shell and must not resurrect any state.
//   - ActivityBlocked is a MODAL the agent put up over its own pane. The turn
//     has ended and a human must dismiss it, so the axis becomes waiting_input
//     with InputDialog — regardless of the delivery axis, because unlike a bare
//     resting prompt post-PR this is not routine idling: nothing advances until
//     someone presses a key, and the session holds a concurrency slot meanwhile.
//     AtPrompt is closed (and marked verified): the pane is live evidence that
//     the gate a Stop hook opened moments earlier now points at a dialog, which
//     is exactly the state no send-keys path may type into. The transcript
//     CANNOT see a modal — nothing is written when one opens — so it never gets
//     a vote here.
//   - ActivityQuotaLimited, same reasoning: a usage-limit banner is a fact
//     about the screen with no transcript record behind it.
//   - ActivityWorking is positive proof of work: the axis becomes working and
//     LastActivityAt is stamped, trusted even over a STALE hook-set
//     waiting_input (the agent has provably resumed). A live pane working cue
//     outranks the transcript, and in practice the two agree.
//   - ActivityWaiting WITH an answerable question is positive evidence of a
//     block, and it outranks the transcript deliberately. A permission prompt
//     is the case that forces this: claude-code writes the assistant record
//     carrying the tool_use and THEN asks for approval, so the transcript
//     reads "a tool is in flight" for the entire time the agent sits waiting
//     for a human. The screen is the only witness to that, exactly as with a
//     modal.
//   - ActivityWaiting with NOTHING to answer is the WEAK cue — the composer is
//     drawn at all times, so this outcome is really "no live working cue was
//     recognized", which is precisely what a rendering change breaks. Here the
//     transcript votes first: a turn it says is in flight makes the axis
//     working. Otherwise the pane's reading stands and the axis settles to idle
//     with AtPrompt OPEN. The delivery axis does not enter into it — it is a
//     fact about the PR, not evidence about the agent. (It used to: pre-PR a
//     resting composer meant needs_input unconditionally, so idle was
//     unreachable for a session's whole pre-PR life and every finished turn
//     read as "Needs You".) The idle outcome may also RELEASE a waiting_input
//     park this same pane authority could have made — a stale InputQuestion,
//     the "" the old rule minted, or the idle nudge — but never one backed by
//     positive block evidence (InputPermission / InputDialog /
//     InputQuotaLimited), and neither may the transcript promotion.
//   - ActivityUnknown does NOT derive working/waiting from the pane — the
//     hook-driven axis stands, so a very recent hook always wins over an
//     ambiguous pane. The transcript refines the anti-false-working guard
//     below, and nothing else.
//
// # Anti-false-working guard (ActivityUnknown only)
//
// A working/starting axis with no positive activity for longer than
// staleWorkingThreshold, which the pane cannot confirm, must stop asserting
// work — it downgrades to waiting_input when a question/prompt is visible, else
// to idle. It never fires before the threshold (no flapping). An axis that has
// never recorded activity (LastActivityAt zero — an adopted session before its
// first heartbeat) starts the staleness clock from now instead of downgrading
// on first sight. The transcript adjusts it in both directions:
//
//   - A transcript that says WORKING re-stamps LastActivityAt and the guard
//     stands down. This is the single biggest win of reading the file at all: a
//     dispatched tool writes nothing until it returns, so an agent 20 minutes
//     into a test suite has no hook, no tmux activity and nothing recognizable
//     on screen — and used to be downgraded out of "working" after 45 seconds.
//     agentlog expires that claim itself (workingClaimMaxAge) so an agent that
//     died mid-tool still lands here eventually.
//   - A transcript that says IDLE lets the downgrade happen NOW instead of
//     after the threshold: the agent's own file recording that the turn stopped
//     is better evidence than waiting out a timer. It does NOT open AtPrompt —
//     the pane is unreadable, and no send-keys gate may be opened by a signal
//     that cannot see the screen.
//
// Deliberately absent: the transcript never promotes an idle or waiting_input
// axis to working while the pane is Unknown. It may only SUSTAIN work already
// believed to be underway. A promotion there would have no screen evidence
// behind it at all, and the permission-prompt shape above (a tool_use record
// with a human-blocking dialog on a pane lola could not read) is exactly the
// case it would get wrong.
func agentReconcile(cur *session.Session, act attention.Activity, hasQuestion bool, tv agentlog.Verdict, now time.Time) bool {
	switch cur.AgentState {
	case state.AgentExited, state.AgentShell, state.AgentOrphaned:
		return false // not pane-owned
	}
	switch act {
	case attention.ActivityBlocked:
		cur.AtPrompt = false
		cur.AtPromptVerified = true // live positive evidence: the gate state is current
		changed := cur.SetAgentState(state.AgentWaitingInput, "", now)
		cur.InputReason = state.InputDialog
		return changed
	case attention.ActivityQuotaLimited:
		// The agent's own usage-limit banner is on screen: the turn is over
		// and the agent cannot take another until the quota resets. Maps to
		// waiting_input with the quota reason — the remedy is the fallback
		// hand-off (maybeFallback), never a typed answer, so the send-keys
		// gate is closed exactly as for a modal.
		cur.AtPrompt = false
		cur.AtPromptVerified = true // live positive evidence: the gate state is current
		changed := cur.SetAgentState(state.AgentWaitingInput, "", now)
		cur.InputReason = state.InputQuotaLimited
		return changed
	case attention.ActivityWorking:
		cur.AtPrompt = false
		cur.AtPromptVerified = true // live positive evidence: the gate state is current
		return cur.SetAgentState(state.AgentWorking, state.SourcePane, now)
	case attention.ActivityWaiting:
		if hasQuestion {
			// An ANSWERABLE question is on screen: the agent is genuinely
			// blocked on a human. Close the gate — a send here would answer the
			// question with the wrong text. Outranks the transcript, which shows
			// a permission prompt as an in-flight tool call (see the precedence
			// note above).
			cur.AtPrompt = false
			cur.AtPromptVerified = true
			changed := cur.SetAgentState(state.AgentWaitingInput, "", now)
			cur.InputReason = state.InputQuestion
			return changed
		}
		// A resting composer with NOTHING to answer is IDLE, whatever the
		// delivery axis says. This used to read `hasQuestion || cur.Delivery ==
		// state.DeliveryNone`, i.e. pre-PR a resting composer meant needs_input
		// UNCONDITIONALLY — which made AgentIdle unreachable for the entire
		// pre-PR life of a session and turned every finished turn into "Needs
		// You". Combined with the idle-nudge hook (see server.go) that one rule
		// produced ~90% of the measured needs_input population. The delivery
		// axis is a fact about the PR, never evidence about what the agent is
		// doing, so it has no business deciding this.
		switch cur.AgentState {
		case state.AgentWaitingInput:
			// A pane read may RELEASE a park it could itself have made, but it
			// must never overrule positive evidence of a real block: a modal, a
			// y/n approval and a usage-limit banner are all states where the
			// composer can look at rest while nothing can proceed, and demoting
			// them to idle would hand a send-keys path a gate it must not have.
			// The transcript promotion below sits BEHIND this check for the same
			// reason, and needs it more: a pending permission prompt is a
			// tool_use record in the file, so without this an approval dialog
			// whose question attention.Parse failed to recognize would read as
			// "working" and stop surfacing as needing a human.
			switch cur.InputReason {
			case state.InputPermission, state.InputDialog, state.InputQuotaLimited:
				return false
			}
			// Everything else may leave: InputIdleNotify (the nudge — a record
			// parked by a pre-change daemon, or carried across a restart, must
			// be able to get out or it is stuck on "Needs You" forever),
			// InputQuestion (pane-derived, and THIS pane read is the same
			// authority one cycle fresher — the question is no longer on
			// screen), and "" (what the old rule above minted for every pre-PR
			// resting pane, so it is the bulk of the records this fixes).
		case state.AgentWorking, state.AgentStarting, state.AgentIdle:
			// The ordinary path. AgentIdle is listed so an already-idle session
			// still gets its gate re-opened below: the observer parks sessions
			// on idle WITHOUT AtPrompt (a stale working axis over an unreadable
			// pane), and those were unreachable by every send-keys path forever.
		default:
			return false
		}
		if tv == agentlog.Working {
			// The screen says "resting composer, nothing to answer" and the
			// agent's own transcript says a turn is in flight. The transcript
			// wins, because THIS is the cue that breaks: claude-code draws its
			// composer mid-turn too, so this outcome means only "no live working
			// cue was recognized" — the exact state a reworded status line
			// produces. Closing AtPrompt is the safe direction either way: a
			// mistaken working axis costs a delayed status, while leaving the
			// gate open on a streaming agent lets a send-keys path type into a
			// mid-turn pane.
			cur.AtPrompt = false
			cur.AtPromptVerified = true
			return cur.SetAgentState(state.AgentWorking, state.SourceTranscript, now)
		}
		// A resting composer is live positive evidence that the gate is open —
		// the same fact the "stop" hook asserts, observed directly. Every
		// send-keys path still re-proves it against a fresh pane capture
		// (handoffPromptProof) before typing, so this only makes a session a
		// CANDIDATE.
		cur.AtPrompt = true
		cur.AtPromptVerified = true
		return cur.SetAgentState(state.AgentIdle, "", now)
	default: // ActivityUnknown: keep the hook-driven axis, subject to the guard.
		if cur.AgentState != state.AgentWorking && cur.AgentState != state.AgentStarting {
			return false
		}
		if tv == agentlog.Working {
			// The transcript proves the turn is still in flight, so the guard
			// has nothing to correct: re-stamp the activity anchor and leave the
			// axis alone. Checked before the zero-LastActivityAt grace because
			// it is strictly better evidence than "we have never heard anything,
			// give it one cycle".
			cur.TouchActivity(state.SourceTranscript, now)
			return false
		}
		if cur.LastActivityAt.IsZero() {
			cur.TouchActivity("", now) // start the clock; grace this cycle
			return false
		}
		if tv != agentlog.Idle && now.Sub(cur.LastActivityAt) <= staleWorkingThreshold {
			return false // still within the activity window: trust the hook
		}
		// Either the window lapsed with nothing to confirm the work, or the
		// transcript positively recorded the turn ending — which is better
		// evidence than the timer and needs no further wait.
		cur.AtPrompt = false
		if hasQuestion {
			changed := cur.SetAgentState(state.AgentWaitingInput, "", now)
			cur.InputReason = state.InputQuestion
			return changed
		}
		return cur.SetAgentState(state.AgentIdle, "", now)
	}
}
