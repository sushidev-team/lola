package daemon

// reactions.go is the P3 reaction engine (PLAN P3.16–19): after every observer
// cycle re-derives a native session's two axes, react() decides whether lola
// should ACT on where the PR now stands — re-prompt the live agent (send-keys),
// notify the operator, or close the loop by cleaning up a merged session.
//
// Every reaction here is DELIVERY-driven: the dispatch, and each reaction's own
// atomic re-check under the store lock, read s.Delivery rather than the
// rolled-up s.Status, which collapses the agent axis over the PR axis and hid
// most of this engine's triggers behind a waiting agent (see react).
//
// Two invariants dominate this file:
//
//   - SEND-KEYS SAFETY. Typing into a live agent while it is mid-turn corrupts
//     it. Every path that types goes through reactSendAgent, which consumes the
//     AtPrompt gate ATOMICALLY (Store.Update) and only then sends; a session
//     that is not idle at its prompt has its reaction DEFERRED (PendingReaction)
//     for a later cycle, never forced.
//   - FIRE ONCE PER TRANSITION. A persistent ci_failed / changes_requested /
//     merge_conflict / approved state must re-prompt the agent once per entry
//     into that state, not on every 30s observer tick. LastReactedStatus is the
//     one-shot guard: it is stamped when the engine acts and cleared when the
//     session leaves the reacted state (resetReactionGuards), so a later
//     re-entry reacts afresh. The ci retry counter (CIRetries) and Escalated
//     flag deliberately survive across the ci_failed⇄ci_pending retry loop and
//     reset only once CI is no longer in play.
//
// Every external exec (gh for reaction content, tmux for send-keys, the merged
// cleanup's git worktree removal) is bounded by reactExecTimeout and runs on the
// observer's shutdown-shielded context. react never panics the observer:
// safeObserve recovers, and the engine treats every seam failure as best-effort.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/attention"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
	"github.com/sushidev-team/lola/internal/worktree"
)

// reactExecTimeout bounds every gh/tmux/git exec a single reaction drives, so a
// wedged tool can never freeze the observer loop it runs inside (which itself
// runs on a WithoutCancel context — see safeObserve).
const reactExecTimeout = 30 * time.Second

// react is the per-session reaction decision, called from observeNative with the
// session record as just updated by that cycle's axis/PR merge. It reads the
// resolved reactions config and the notifier under d.mu, then dispatches on the
// DELIVERY axis. Each reaction fires at most once per transition
// (LastReactedStatus guard) and anything that types into the agent is gated on
// AtPrompt.
//
// The dispatch reads s.Delivery, never the rolled-up s.Status, because the
// rollup MASKS the PR axis: a live agent waiting on a human outranks every
// delivery state in Rollup, so a permission prompt mid-fix hid the red CI, the
// requested changes or the conflict the engine exists to act on — and, with 90%
// of that waiting population turning out to be the coding agent's own 60s idle
// nudge, it hid them most of the time. merge_conflict was carved out of the
// rollup for exactly that reason long before the rest; every delivery-driven
// reaction now reads the same axis. The PRIORITY between them is unchanged
// (merged, ci_failed, merge_conflict, changes_requested, approved, closed), and
// so is every one-shot guard.
//
// The decision is computed from the passed (post-update) record; all reaction
// STATE is applied back via Store.Update, which re-reads the current record
// under the store lock so a concurrent hook write (a Stop that set AtPrompt, a
// tool_use that cleared it) is never clobbered.
func (d *Daemon) react(ctx context.Context, s session.Session) {
	// Only native sessions with a tmux target are actionable. Everything else —
	// an adopted shell, a record with no pane — falls through to
	// resetReactionGuards below, which never sends anything.
	if s.Source != "native" || s.TmuxName == "" {
		return
	}
	// The observer hands an axis-bearing record; a direct caller (tests, future
	// paths) may still hold a bare legacy snapshot — the delivery-axis dispatch
	// below needs the axes either way.
	s.EnsureAxes()

	d.mu.Lock()
	rc := d.cfg.Reactions
	notifier := d.notifier
	d.mu.Unlock()
	if notifier == nil {
		notifier = notify.New(notify.NotifyConfig{})
	}

	switch {
	case s.Delivery == state.DeliveryMerged:
		// Loop close: clean up the worktree and free the slot. Gated by the
		// Merged.auto toggle; a dirty post-merge worktree is kept, not
		// force-removed. Auto-merge is intentionally NOT implemented anywhere in
		// P3 — merged means a human already merged. There is deliberately no
		// LastReactedStatus guard here: reactMerged is idempotent by DROPPING
		// the store entry on success, and a failed cleanup must retry next
		// cycle (a guard would have suppressed the retry and could never be
		// stamped anyway — the entry is gone when it succeeds).
		//
		// That retry is BOUNDED and BACKED OFF (Session.CleanupFailures): a
		// worktree git can never remove is otherwise re-attempted every 30s
		// forever, logging the same line and telling nobody — one real store
		// carried 1953 identical retries for a single session across 16 hours.
		// See reactMerged / cleanupRetryDue / noteCleanupFailure.
		if rc.Merged.Auto {
			d.reactMerged(ctx, s, notifier)
		}

	case s.AgentState == state.AgentDead:
		// The pane is gone, so there is nothing to re-prompt and nothing to
		// escalate — the record is the reconcile pass's problem now
		// (state.Present is false for it) and a human's after that.
		//
		// The rollup used to deliver this for free: rule 2, "a dead pane forces
		// dead", put every non-merged dead session in the default branch below.
		// Dispatching on the delivery axis takes that away, so the rule is
		// stated here rather than silently dropped — this is a RESTATEMENT, not
		// a new policy, and the guards are reset exactly as they were. It sits
		// BELOW merged deliberately: the merged cleanup is the one reaction a
		// dead pane still needs (Kill stops tmux before it touches git, so
		// every retry after the first attempt is by definition a dead session).
		//
		// It matters most on the send paths. SetAgentState does NOT close
		// AtPrompt when a pane dies, so a session that was resting at its
		// prompt when tmux went away carries an OPEN, verified gate; without
		// this the engine would consume that gate, stamp the one-shot guard and
		// send-keys into a tmux session that no longer exists — and the
		// reaction would then be silently skipped if the session were revived.
		d.resetReactionGuards(s)

	case s.Delivery == state.DeliveryCIFailed:
		d.reactCIFailed(ctx, s, rc.CIFailed, notifier)

	case s.Delivery == state.DeliveryMergeConflict:
		// ci_failed cannot mask this: DeriveDelivery ranks ci_failed above
		// merge_conflict, so the branch above has already handled (and returned
		// for) such a session — the rebase would re-run CI anyway.
		if rc.MergeConflict.Auto {
			d.reactSendAgent(ctx, s, "merge_conflict", rc.MergeConflict.Message, notifier, nil)
		}

	case s.Delivery == state.DeliveryChangesRequested:
		if rc.ChangesRequested.Auto {
			d.reactSendAgent(ctx, s, "changes_requested", rc.ChangesRequested.Message, notifier,
				func() string { return d.fetchReviewComments(ctx, s) })
		}

	case s.Delivery == state.DeliveryApproved:
		// approved+green: notify and PARK. Never auto-merge — auto=true still
		// only notifies (documented: there is no merge action in P3).
		d.reactApproved(ctx, s, notifier)

	case s.Delivery == state.DeliveryClosed:
		// A PR closed WITHOUT merging: the work was rejected or abandoned.
		// Notify once (LastReactedStatus guard) and leave everything alone —
		// never send-keys, never auto-kill; the human decides what happens to
		// the worktree. state.Present is false for a closed PR, so the
		// reconcile orphan-revert stops shielding this issue and the label can
		// revert.
		d.reactClosed(ctx, s, notifier)

	default:
		// Delivery is none / draft / ci_pending / review_pending: the PR left
		// whatever state was last reacted to, so clear the one-shot guards for
		// a clean re-entry.
		d.resetReactionGuards(s)
	}
}

// reactClosed surfaces a closed-unmerged PR to the operator exactly once per
// entry into "closed". Read-only apart from the one-shot stamp: no send-keys,
// no cleanup — a closed PR may be reopened, and destroying the worktree is a
// human decision (kill).
func (d *Daemon) reactClosed(ctx context.Context, s session.Session, notifier notify.Notifier) {
	if s.LastReactedStatus == "closed" {
		return
	}
	acted := false
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		if cur.Delivery != state.DeliveryClosed || cur.LastReactedStatus == "closed" {
			return false
		}
		cur.LastReactedStatus = "closed"
		cur.PendingReaction = ""
		acted = true
		return true
	})
	if !acted {
		return
	}
	d.reactSave()
	notifier.Notify(ctx, notify.Note{
		Title:    "PR closed without merging",
		Body:     fmt.Sprintf("%s: its PR was closed without merging — the session and worktree are kept; kill the session when you are done with it", issueLabel(s)),
		Priority: notify.Action,
		URL:      prURL(s),
	})
	d.logf("", "react: %s PR closed without merging — notified, session kept", s.ID)
}

// maxCleanupAttempts bounds how many CONSECUTIVE times the merged cleanup is
// re-attempted for one session before the engine stops trying and hands the
// worktree to a human. Eight attempts on the schedule below span ~17 minutes
// from the merge — long enough to ride out the transient causes (a tmux server
// restarting, a git index lock, a file briefly held open by a build) and short
// enough that a permanent one (a worktree git refuses to delete, a missing
// binary) becomes a notification the same coffee break rather than a log line
// nobody reads for 16 hours.
const maxCleanupAttempts = 8

// cleanupBackoffCap is the ceiling on the per-attempt wait. The schedule
// doubles from the observer's own cadence — 30s, 1m, 2m, 4m — and then holds
// here, because a retry that is not going to work does not get more likely by
// waiting an hour, and the attempt budget is what actually ends the loop.
const cleanupBackoffCap = 5 * time.Minute

// cleanupBackoff is the wait owed AFTER the nth consecutive cleanup failure,
// before attempt n+1 may run.
//
// The FIRST failure owes nothing, which keeps the long-standing contract
// literally true — "a failed cleanup retries on the next observer cycle" — and
// is the right shape besides: the cheap causes (a git index lock, a file the
// pane teardown had not released yet) clear within one cadence, and one free
// retry costs a single exec. The wait starts growing from the SECOND failure,
// where the cause has stopped looking transient: observeInterval doubled per
// further failure, capped at cleanupBackoffCap. So the spacing reads 0, 30s,
// 1m, 2m, 4m, 5m, 5m — the full budget lands ~17 minutes after the merge
// instead of grinding for the daemon's whole lifetime.
func cleanupBackoff(failures int) time.Duration {
	if failures < 2 {
		return 0
	}
	d := observeInterval
	for i := 2; i < failures; i++ {
		if d >= cleanupBackoffCap {
			break
		}
		d *= 2
	}
	if d > cleanupBackoffCap {
		return cleanupBackoffCap
	}
	return d
}

// cleanupBackoffTotal is the cumulative wait owed after `failures` consecutive
// failures — the offset from the schedule's anchor at which attempt
// failures+1 comes due. Clamped at maxCleanupAttempts so a hand-edited or
// corrupted counter cannot turn this into a long loop.
func cleanupBackoffTotal(failures int) time.Duration {
	if failures > maxCleanupAttempts {
		failures = maxCleanupAttempts
	}
	var total time.Duration
	for i := 1; i <= failures; i++ {
		total += cleanupBackoff(i)
	}
	return total
}

// cleanupRetryDue reports whether this merged session's next cleanup attempt
// may run now. The first attempt (no failures yet) is always due; afterwards
// the attempt is due once cleanupBackoffTotal has elapsed since the schedule's
// ANCHOR.
//
// The anchor is DeliverySince — the moment the PR became merged — rather than
// a "last attempt" stamp, because there is no such stamp on the record and the
// two coincide in practice: the first cleanup runs in the very observer cycle
// that flipped the delivery axis to merged, and neither the axis nor its Since
// moves again while the session stays merged (the agent axis going Dead
// underneath it cannot touch them). Deriving the schedule from a persisted
// anchor also means it survives a daemon restart instead of resetting to
// attempt-every-cycle.
//
// Two deliberate consequences. A record with NO anchor (a legacy snapshot, a
// hand-built session) fails OPEN — it retries on the next cycle, exactly as
// the unbounded loop used to, and is still stopped by maxCleanupAttempts. And
// a session that had already been merged for a long while when its first
// attempt ran (merged.auto switched on after the fact) has its whole schedule
// already in the past, so it spends the attempt budget over consecutive
// cycles; the BOUND, not the spacing, is what protects that case.
func cleanupRetryDue(s session.Session, now time.Time) bool {
	if s.CleanupFailures < 1 {
		return true
	}
	if s.DeliverySince.IsZero() {
		return true
	}
	return !now.Before(s.DeliverySince.Add(cleanupBackoffTotal(s.CleanupFailures)))
}

// reactMerged closes the loop for a merged PR by REUSING the kill/cleanup path:
// terminate the tmux agent AND its shell/review tabs, remove the worktree
// (dirty-safe, never force) together with the local branch it was checked out
// on, drop the store entry, and free the issue's in-flight claim — then notify
// Info. Branch deletion is runtime.Kill's, so it holds the same two rules there:
// only a lola-OWNED branch is deleted (a pr session's is upstream), and a dirty
// worktree keeps both the checkout and its branch. A
// worktree with uncommitted changes is KEPT (not force-removed) and the operator
// is notified, but the store entry is dropped either way: a clean and a dirty
// merge both leave the sessions view, differing only in whether the checkout
// survives on disk. Dropping the dirty case too is deliberate — a lingering
// "merged" entry is re-observed every cycle and never ages out, so keeping it
// would let dirty merges pile up permanently. A non-dirty removal error keeps
// the entry (un-dropped) so the next cycle retries the cleanup.
//
// That retry is BOUNDED and BACKED OFF. It used to be neither, and the failure
// mode was silent: a `git worktree remove` that exits 255 for a reason nothing
// here can fix is re-attempted on every 30s observer cycle for as long as the
// daemon runs, writing one identical log line per attempt and telling nobody.
// So each failure is counted on the record (Session.CleanupFailures), the next
// attempt waits cleanupBackoff, and at maxCleanupAttempts the engine STOPS and
// converts the loop into one Action notification naming the session and the
// last error — see noteCleanupFailure. Nothing about the refusals changes:
// force is still never passed, so a dirty worktree is still kept rather than
// destroyed, and the branch rule stays runtime.Kill's.
func (d *Daemon) reactMerged(ctx context.Context, s session.Session, notifier notify.Notifier) {
	if s.CleanupFailures >= maxCleanupAttempts {
		// Given up already (noteCleanupFailure notified when the budget ran
		// out). Attempt nothing — but keep the record ALIVE, or the very
		// problem we just reported would erase its own evidence: the pane is
		// gone by now (Kill stops tmux before it touches git), so the observer
		// stops re-writing this record, its LastSeen freezes, and
		// sessionRetention silently drops the one thing pointing at the
		// worktree that still needs a hand.
		d.keepCleanupVisible(s)
		return
	}
	if !cleanupRetryDue(s, time.Now()) {
		return // backing off between attempts; a later cycle carries this one
	}

	d.mu.Lock()
	nat := d.native
	p := d.cfg.ProjectByName(s.Project)
	home := d.home
	d.mu.Unlock()
	if nat == nil {
		return
	}

	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()

	// Project gone from config: no safe worktree to target, so terminate the
	// agent only (removeWorktree=false keeps runtime.Kill away from git).
	removeWorktree := p != nil
	dir := ""
	if p != nil {
		dir = filepath.Join(home, "worktrees", p.Name, s.ID)
	}

	err := nat.Kill(cctx, s, removeWorktree, false) // never force: dirty is kept
	if errors.Is(err, worktree.ErrDirty) {
		// Kill stops the tmux agent BEFORE touching the worktree (see
		// runtime.Native.Kill), so on ErrDirty the agent is already down and only
		// the uncommitted checkout survives on disk. Drop the store entry — freeing
		// the in-flight claim and clearing the session from the view exactly like
		// the clean path — but leave the worktree at <dir> for inspection, and
		// notify the operator ONCE so the kept work is not silently lost. Dropping
		// rather than stamping a phantom "merged" record is what stops dirty merges
		// from piling up in the sessions store forever (they are re-observed every
		// cycle and never age out while an entry lingers).
		d.dropSession(s)
		notifier.Notify(cctx, notify.Note{
			Title:    "PR merged — worktree kept",
			Body:     fmt.Sprintf("%s merged; agent stopped and slot freed, but its worktree has uncommitted changes and was kept at %s", issueLabel(s), dir),
			Priority: notify.Info,
			URL:      prURL(s),
		})
		d.logf("", "react: %s merged; worktree kept (uncommitted changes) at %s, store entry dropped", s.ID, dir)
		return
	}
	if err != nil {
		// Left un-stamped on purpose: a later observer cycle retries the
		// cleanup of this still-merged session — counted and spaced by
		// noteCleanupFailure, which also decides when to stop.
		d.noteCleanupFailure(ctx, s, dir, err, notifier)
		return
	}

	d.dropSession(s) // drops the store entry, frees the in-flight claim, persists
	removed := "worktree removed"
	if s.OwnsBranch() && s.Branch != "" {
		removed = fmt.Sprintf("worktree and branch %s removed", s.Branch)
	}
	notifier.Notify(cctx, notify.Note{
		Title:    "PR merged — cleaned up",
		Body:     fmt.Sprintf("%s merged; %s and the slot freed", issueLabel(s), removed),
		Priority: notify.Info,
		URL:      prURL(s),
	})
	d.logf("", "react: %s merged; %s, tabs closed, slot freed", s.ID, removed)
}

// noteCleanupFailure records one failed merged-cleanup attempt and decides what
// the operator hears about it. Inside the budget it is a log line naming the
// attempt and the wait before the next one; on the attempt that EXHAUSTS the
// budget it is one Action notification — the loop stops there and the worktree
// becomes a human's job.
//
// The count is incremented inside Store.Update so two writers cannot both read
// the same value, and the give-up notification fires on the exact transition
// (failures == maxCleanupAttempts) rather than on ">= max", which is what makes
// it one-shot: every later cycle short-circuits in reactMerged before reaching
// here. A record that has vanished under us (a concurrent kill dropped it)
// leaves failures at 0 and is simply dropped — there is nothing left to retry
// or report.
func (d *Daemon) noteCleanupFailure(ctx context.Context, s session.Session, dir string, cause error, notifier notify.Notifier) {
	failures := 0
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.CleanupFailures++
		failures = cur.CleanupFailures
		return true
	})
	if failures == 0 {
		return // the session was dropped meanwhile
	}
	d.reactSave()

	if failures < maxCleanupAttempts {
		next := "on the next cycle"
		if w := cleanupBackoff(failures); w > 0 {
			next = "in " + w.String()
		}
		d.logf("", "react: merged cleanup of %s failed (attempt %d/%d, retrying %s): %v",
			s.ID, failures, maxCleanupAttempts, next, cause)
		return
	}

	where := dir
	if where == "" {
		where = "its worktree" // project gone from config: no path to name
	}
	d.logf("", "react: merged cleanup of %s failed %d times — giving up; %s needs a hand (last error: %v)",
		s.ID, failures, where, cause)
	notifier.Notify(ctx, notify.Note{
		Title: "Merged cleanup gave up — needs a hand",
		Body: fmt.Sprintf("%s merged, but its cleanup failed %d times and lola has stopped retrying. Clear %s by hand, then kill the session. Last error: %s",
			issueLabel(s), failures, where, clipCause(cause)),
		Priority: notify.Action,
		URL:      prURL(s),
	})
}

// keepCleanupVisible re-stamps a given-up session's LastSeen so the store's
// retention prune cannot age it out while its worktree is still stranded. The
// closure changes no field: the mutation IS the LastSeen stamp Store.Update
// applies on a true return (and it fires no transition event, since Status is
// untouched). It returns true only while the give-up state actually holds, so
// this can never keep an unrelated record alive.
//
// Only the in-memory stamp matters: the observer runs its prune at the END of a
// cycle, after react, so a record loaded from disk with a long-stale LastSeen is
// rescued before the prune of that same cycle ever sees it. The record then
// leaves the store the way it should — when a human kills the session.
func (d *Daemon) keepCleanupVisible(s session.Session) {
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		return cur.CleanupFailures >= maxCleanupAttempts
	})
}

// clipCause renders an exec error for a notification body: newlines and runs of
// whitespace collapsed to single spaces (a git/tmux failure is routinely
// multi-line, and a desktop banner shows one) and clipped to a length a banner
// and a Slack line can both carry. The clip counts RUNES, not bytes — a git
// error naming a path routinely carries non-ASCII, and a byte clip would emit a
// half rune into a JSON webhook body.
func clipCause(err error) string {
	if err == nil {
		return ""
	}
	s := strings.Join(strings.Fields(err.Error()), " ")
	const max = 240
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// reactCIFailed handles a red PR (PLAN P3.16): while inside the retry budget it
// re-prompts the agent with the failing logs (gated on AtPrompt via
// reactSendAgent, which also increments CIRetries); once the retries are
// exhausted it escalates to the operator ONCE and stops auto-retrying until CI
// is green again (Escalated). Both CIRetries and Escalated persist across the
// ci_failed⇄ci_pending loop and reset only in resetReactionGuards.
func (d *Daemon) reactCIFailed(ctx context.Context, s session.Session, r config.Reaction, notifier notify.Notifier) {
	if !r.Auto {
		return
	}
	if s.Escalated {
		return // already handed to a human; do not re-prompt or re-notify
	}
	if s.LastReactedStatus == "ci_failed" {
		return // already acted on this entry into ci_failed; await a transition out
	}
	retries := r.Retries
	if retries < 0 {
		retries = 0
	}
	if s.CIRetries >= retries {
		// Retries exhausted: escalate once. This is a notify, not a send, so it
		// is NOT gated on AtPrompt.
		escalated := false
		d.sessions.Update(s.ID, func(cur *session.Session) bool {
			if cur.Delivery != state.DeliveryCIFailed || cur.Escalated || cur.LastReactedStatus == "ci_failed" {
				return false
			}
			cur.Escalated = true
			cur.LastReactedStatus = "ci_failed"
			cur.PendingReaction = ""
			escalated = true
			return true
		})
		if !escalated {
			return
		}
		d.reactSave()
		body := fmt.Sprintf("%s: CI is still failing after %d automatic attempt(s); handing off", issueLabel(s), retries)
		// Brain (PLAN P5.25): replace the generic escalation body with a bounded,
		// one-shot claude summary of WHY the session is blocked. This fires once
		// per escalation because it is inside the Escalated one-shot guard above.
		// The summary is UNTRUSTED (its context — pane tail, CI logs — is
		// attacker-influenceable): it goes only into this notify body and the P4
		// blocked Linear comment (stashed for writeBackEscalation), NEVER into
		// tmux send-keys. On any error/disabled it stays the generic body.
		if summary := d.escalationSummary(ctx, s); summary != "" {
			body = summary
			d.stashEscalationSummary(s.ID, summary)
		}
		notifier.Notify(ctx, notify.Note{
			Title:    "CI still failing — needs a human",
			Body:     body,
			Priority: notify.Urgent,
			URL:      prURL(s),
		})
		d.logf("", "react: %s CI failed %d time(s) — escalated to a human", s.ID, retries)
		return
	}

	d.reactSendAgent(ctx, s, "ci_failed", r.Message, notifier,
		func() string { return d.fetchFailingChecks(ctx, s) })
}

// reactApproved fires the approved+green reaction (PLAN P3.19): notify the
// operator that the PR is ready to merge and PARK — never merge, never touch the
// worktree. Fires once per entry into "approved".
func (d *Daemon) reactApproved(ctx context.Context, s session.Session, notifier notify.Notifier) {
	if s.LastReactedStatus == "approved" {
		return
	}
	acted := false
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		if cur.Delivery != state.DeliveryApproved || cur.LastReactedStatus == "approved" {
			return false
		}
		cur.LastReactedStatus = "approved"
		cur.PendingReaction = ""
		acted = true
		return true
	})
	if !acted {
		return
	}
	d.reactSave()
	body := fmt.Sprintf("%s is approved and green — ready to merge", issueLabel(s))
	// Brain (PLAN P5.25): replace the generic approved body with a bounded,
	// one-shot claude risk summary of the PR diff. Fires once per entry into
	// "approved" (inside the LastReactedStatus guard consumed above). The diff is
	// attacker-authored, so the summary is UNTRUSTED: it goes into this notify
	// body only, NEVER into tmux send-keys. On any error/disabled it stays
	// generic. A Linear comment would be added only if a comment toggle existed
	// for this transition — none does in P4, so this is notify-only.
	if summary := d.approvedSummary(ctx, s); summary != "" {
		body = summary
	}
	notifier.Notify(ctx, notify.Note{
		Title:    "PR approved and green",
		Body:     body,
		Priority: notify.Action,
		URL:      prURL(s),
	})
	d.logf("", "react: %s approved and green — parked for human merge", s.ID)
}

// ensurePromptVerified re-verifies an AtPrompt gate that was carried across a
// daemon restart (adoption preserves the gate but marks it UNVERIFIED — see
// Session.AtPromptVerified) against the live pane, so the first send-keys
// after a restart can never type into an agent that resumed while the daemon
// was down. A verified gate returns true immediately. Otherwise the pane is
// captured and classified (a pure read, never trusted beyond this gate):
//
//   - ActivityWaiting: the agent really is resting at a prompt — the gate is
//     marked verified (atomically, only while it is still open) and trusted.
//   - ActivityWorking: the agent is mid-turn — the stale gate is CLOSED
//     without sending; a real Stop hook re-opens it.
//   - ActivityUnknown / capture failure: not proven either way — fail CLOSED:
//     the send defers and a later hook or pane cycle settles the gate.
//
// "Still open" is handoffDeliverable, not a bare AtPrompt read, so a worker
// parked on an idle notification can be verified after a restart too — adoption
// marks every carried gate unverified, and an AtPrompt-only check could never
// re-verify a session whose AtPrompt the notification had already closed,
// stranding its review hand-off forever. This cannot loosen the reaction path:
// reactSendAgent requires AtPrompt itself before calling here, and its own
// atomic consume re-checks AtPrompt after.
func (d *Daemon) ensurePromptVerified(ctx context.Context, s session.Session) bool {
	// Re-read the live record: the caller's copy is a cycle-old snapshot and a
	// hook may have verified (or closed) the gate since.
	if cur, ok := d.sessions.Get(s.ID); ok {
		s = cur
	}
	if s.AtPromptVerified {
		return true
	}
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	text, err := d.paneTail(cctx, paneTarget(s), observePaneLines)
	cancel()
	if err != nil {
		d.logf("", "react: %s prompt-gate verification capture failed (deferring send): %v", s.ID, err)
		return false
	}
	switch attention.Classify(text, agent.Parse(s.Agent)) {
	case attention.ActivityWaiting:
		verified := false
		d.sessions.Update(s.ID, func(cur *session.Session) bool {
			if !handoffDeliverable(*cur) {
				return false // a hook resumed the agent meanwhile: nothing to verify
			}
			cur.AtPromptVerified = true
			verified = true
			return true
		})
		return verified
	case attention.ActivityWorking:
		d.sessions.Update(s.ID, func(cur *session.Session) bool {
			if !handoffDeliverable(*cur) {
				return false
			}
			cur.AtPrompt = false // the carried gate was stale: close it
			cur.AtPromptVerified = true
			return true
		})
		d.logf("", "react: %s carried AtPrompt gate was stale (pane is mid-turn) — closed without sending", s.ID)
		return false
	default:
		return false
	}
}

// reactSendAgent is the ONLY path that types into a live agent. It enforces the
// send-keys safety gate:
//
//   - Already reacted to this state-entry (LastReactedStatus == key) → no-op.
//   - Agent not idle at its prompt (AtPrompt false), or the gate was carried
//     across a restart and cannot be verified against the live pane
//     (ensurePromptVerified) → the reaction is DEFERRED: PendingReaction is
//     recorded and a later cycle retries. Nothing is typed.
//   - Agent verifiably idle at its prompt → the (optional) detail is fetched,
//     the message rendered, then AtPrompt is CONSUMED atomically together with
//     stamping LastReactedStatus (and, for ci_failed, bumping CIRetries) in one
//     Store.Update. Only if that atomic consume wins is the text actually sent —
//     so a hook that flipped AtPrompt false in the meantime cancels the send.
//
// key is one of "ci_failed" | "changes_requested" | "merge_conflict"; it doubles
// as the LastReactedStatus / PendingReaction marker and selects the notify text.
func (d *Daemon) reactSendAgent(ctx context.Context, s session.Session, key, template string, notifier notify.Notifier, fetchDetail func() string) {
	if s.LastReactedStatus == key {
		return // one-shot: already sent for this entry into the state
	}

	if !s.AtPrompt || !d.ensurePromptVerified(ctx, s) {
		// Defer: the agent is mid-turn (or its carried gate is unverifiable).
		// Record the pending reaction (idempotently) so it is visible; the
		// LastReactedStatus guard is what actually makes the next AtPrompt
		// cycle retry.
		d.sessions.Update(s.ID, func(cur *session.Session) bool {
			if cur.PendingReaction == key {
				return false
			}
			cur.PendingReaction = key
			return true
		})
		d.logf("", "react: %s PR is %s but the agent is mid-turn — deferring %s reaction", s.ID, s.Delivery, key)
		return
	}

	detail := ""
	if fetchDetail != nil {
		detail = fetchDetail()
	}
	msg := renderReaction(template, s, detail)
	if key == "changes_requested" {
		// The feedback this reaction relays IS a set of review threads — a human
		// reviewer's, or a bot's inline comments (CodeRabbit posts its findings
		// that way). Tell the worker to close the ones it fixes, and how: an
		// agent that is never told leaves the conversations open and the PR reads
		// as untouched however much of it was actually fixed. lola's own text,
		// appended AFTER the rendered (untrusted) detail so nothing in the
		// feedback can rewrite the instruction, and conditional in its wording
		// because nothing here proves a thread is open right now.
		msg += prThreadNote(s)
	}

	// Atomically re-check AtPrompt under the store lock and consume it. This is
	// the true gate: the passed copy's AtPrompt may be stale by microseconds.
	var (
		sent     bool
		tmuxName string
		attempt  int
	)
	updated, _ := d.sessions.Update(s.ID, func(cur *session.Session) bool {
		if !cur.AtPrompt || cur.LastReactedStatus == key {
			return false // a hook resumed the agent, or another writer reacted
		}
		cur.AtPrompt = false
		cur.LastReactedStatus = key
		cur.PendingReaction = ""
		if key == "ci_failed" {
			cur.CIRetries++
		}
		tmuxName = cur.TmuxName
		sent = true
		return true
	})
	if !sent {
		d.logf("", "react: %s reaction %s skipped — agent no longer idle at prompt", s.ID, key)
		return
	}
	attempt = updated.CIRetries

	// AtPrompt is already consumed; the send now happens exactly once. A tmux
	// failure is logged but not rolled back — the guard stays set so we do not
	// spam the agent, and a genuine later transition re-reacts.
	sctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	if err := d.sendKeys(sctx, tmuxName, msg); err != nil {
		d.logf("", "react: send-keys (%s) to %s failed: %v", key, s.ID, err)
		return
	}
	d.reactSave()

	title, body := reactNotifyText(key, s, attempt)
	notifier.Notify(ctx, notify.Note{Title: title, Body: body, Priority: notify.Action, URL: prURL(s)})
	d.logf("", "react: %s %s — re-prompted the agent", s.ID, key)
}

// resetReactionGuards clears the one-shot guards once a session's DELIVERY axis
// has left every state the engine reacts to, so a later re-entry into a reacted
// state fires afresh.
//
// Reaching here IS the derivation, and it is a delivery-axis one: react's switch
// handles merged / ci_failed / merge_conflict / changes_requested / approved /
// closed off s.Delivery, so its default branch — this function's only caller in
// the engine — can only be delivery ∈ {none, draft, ci_pending, review_pending}.
// The PR is demonstrably no longer in a reacted state.
//
// There used to be a `Status == "needs_input"` bail-out at the top of this
// function, and it existed SOLELY because the rolled-up status masked the
// delivery axis: a live agent waiting on a human outranks every PR state in
// Rollup, so a permission prompt mid-fix arrived here carrying a still-red,
// still-conflicting, still-changes-requested PR. Clearing the guards for it
// would have zeroed the CI retry streak, dropped the Escalated backstop and
// released the one-shot LastReactedStatus — re-prompting and re-escalating the
// agent every single time it returned to its prompt. Dispatching off the
// delivery axis removes the mask at the source: such a session now lands in
// reactCIFailed / reactSendAgent, which keep their own guards, and never
// reaches this function at all. So the agent axis is not consulted here in
// either direction — it no longer carries any information about whether the PR
// left its reacted state.
//
// The ci retry streak (CIRetries / Escalated) is preserved while CI is still in
// play on the delivery axis and reset only once CI is out of the picture.
func (d *Daemon) resetReactionGuards(s session.Session) {
	// ci_failed cannot reach here through react, but a direct caller can hand
	// any record over; both CI states are named so the streak survives either.
	ciResolved := s.Delivery != state.DeliveryCIFailed && s.Delivery != state.DeliveryCIPending
	if s.LastReactedStatus == "" && s.PendingReaction == "" && s.CIRetries == 0 && !s.Escalated &&
		s.CleanupFailures == 0 {
		return // nothing to clear
	}
	changed := false
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		c := false
		if cur.LastReactedStatus != "" {
			cur.LastReactedStatus = ""
			c = true
		}
		if cur.PendingReaction != "" {
			cur.PendingReaction = ""
			c = true
		}
		if ciResolved && (cur.CIRetries != 0 || cur.Escalated) {
			cur.CIRetries = 0
			cur.Escalated = false
			c = true
		}
		// The merged-cleanup streak counts CONSECUTIVE failures within ONE
		// merged episode, and reaching here means the session is no longer
		// merged — the only partial progress this engine can actually observe
		// (a successful or dirty cleanup drops the record outright, so neither
		// needs a reset path). A PR that was reopened and merges again gets a
		// fresh attempt budget rather than inheriting a spent one.
		if cur.CleanupFailures != 0 {
			cur.CleanupFailures = 0
			c = true
		}
		changed = c
		return c
	})
	if changed {
		d.reactSave()
	}
}

// fetchFailingChecks pulls the size-bounded failing-CI summary for the agent,
// best-effort: any error (or a PR/repo we cannot address) yields "" so the
// reaction still sends a (detail-less) recovery prompt rather than nothing.
func (d *Daemon) fetchFailingChecks(ctx context.Context, s session.Session) string {
	if s.PR == nil || s.Repo == "" {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	detail, err := d.failingChecks(cctx, s.Repo, s.PR.Number)
	if err != nil {
		d.logf("", "react: fetch failing checks for %s failed: %v", s.ID, err)
		return ""
	}
	return detail
}

// fetchReviewComments pulls the size-bounded review feedback for the agent,
// best-effort (see fetchFailingChecks).
func (d *Daemon) fetchReviewComments(ctx context.Context, s session.Session) string {
	if s.PR == nil || s.Repo == "" {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	detail, err := d.reviewComments(cctx, s.Repo, s.PR.Number)
	if err != nil {
		d.logf("", "react: fetch review comments for %s failed: %v", s.ID, err)
		return ""
	}
	return detail
}

// renderReaction fills a reaction message template. It is deliberately a plain
// simultaneous string replace (strings.Replacer), NOT text/template, so an
// agent-authored PR body or a failing log surfaced in {{.Detail}} can never
// inject template directives or reach an eval surface. The fully rendered
// result is passed through sanitizeAgentText before it is returned so that
// control bytes carried by {{.Detail}} (raw CI logs, PR/review bodies) can
// never reach the tmux send-keys transport (see sanitizeAgentText).
func renderReaction(template string, s session.Session, detail string) string {
	prRef := ""
	if s.PR != nil {
		prRef = fmt.Sprintf("#%d", s.PR.Number)
	}
	msg := strings.NewReplacer(
		"{{.Detail}}", detail,
		"{{.Issue}}", s.Issue,
		"{{.PR}}", prRef,
	).Replace(template)
	return sanitizeAgentText(msg)
}

// ansiEscapeRe matches the ANSI escape sequences (CSI and OSC) that CI logs and
// terminal captures routinely emit, so they can be stripped whole rather than
// left as visible garbage once the lone ESC byte is removed.
var ansiEscapeRe = regexp.MustCompile("\x1b\\[[0-9;?:<>=]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")

// sanitizeAgentText makes text safe to hand to tmux send-keys for typing into a
// live agent's pane. The transport (tmux.Client.SendKeys) types the payload
// with `send-keys -l` and then submits with a SEPARATE Enter (\r); any \r
// already inside the payload is an INDISTINGUISHABLE submit, so an embedded
// carriage return — routine in `gh run view --log-failed` output (npm/pytest/
// cargo/webpack progress bars) and injectable via attacker-authored PR/review
// bodies — would submit a partial prompt and defeat the AtPrompt send-keys gate
// mid-transport. So:
//
//   - ANSI escape sequences are stripped whole.
//   - CR (\r) is dropped: it is the submit vector. A CRLF collapses to a clean
//     LF; a bare CR (progress bars) vanishes.
//   - Other C0 controls (and DEL / C1) are dropped so nothing else can steer
//     the pane's line editor.
//   - LF (\n) and TAB (\t) are PRESERVED: reaction templates are intentionally
//     multi-line (see config.DefaultCIFailedMessage) and the agent pane inserts
//     LF literally — only CR/Enter submits.
func sanitizeAgentText(s string) string {
	s = ansiEscapeRe.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			// CR, other C0 controls, DEL, and C1 controls: drop.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// reactNotifyText builds the operator notification for a send reaction.
func reactNotifyText(key string, s session.Session, attempt int) (title, body string) {
	label := issueLabel(s)
	switch key {
	case "ci_failed":
		return "CI failed — re-prompted agent",
			fmt.Sprintf("%s: relayed the failing CI output to the agent (attempt %d)", label, attempt)
	case "changes_requested":
		return "Changes requested — relayed to agent",
			fmt.Sprintf("%s: relayed reviewer feedback to the agent", label)
	case "merge_conflict":
		return "Merge conflict — asked agent to rebase",
			fmt.Sprintf("%s: asked the agent to rebase and resolve conflicts", label)
	default:
		return "Reaction sent", label
	}
}

// issueLabel is a human-friendly session identifier for notifications: the
// Linear issue identifier when known, else the session ID.
func issueLabel(s session.Session) string {
	if s.Issue != "" {
		return s.Issue
	}
	return s.ID
}

// prURL returns the session's PR URL, or "" when it has no PR.
func prURL(s session.Session) string {
	if s.PR != nil {
		return s.PR.URL
	}
	return ""
}

// reactSave persists the session store after a reaction mutated it, logging any
// failure. Reaction state is best-effort durable — an unwritten reaction guard
// at worst re-sends after a restart.
func (d *Daemon) reactSave() {
	if err := d.sessions.Save(); err != nil {
		d.logf("", "react: persist sessions: %v", err)
	}
}
