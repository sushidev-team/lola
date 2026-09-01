package state

// Two-axis presentation and the pair-based consumer tables.
//
// The rolled-up 16-word status (rollup.go) COLLAPSES the agent axis into the
// delivery axis, and 20MB of daemon.log says what that costs. Measured over
// one machine's history:
//
//   - 458 transitions into needs_input, 412 of them (90%) minted by the coding
//     agent's own 60s "waiting for your input" nudge rather than by an actual
//     question. Only 45 were real permission prompts.
//   - needs_input↔review_pending flapped 264 times, plus 97 more against
//     ci_pending — about 52% of ALL status churn was that one loop, with a
//     median dwell of exactly 60s (the nudge's period).
//   - Post-PR, Rollup returns the DELIVERY word for both AgentWorking and
//     AgentIdle, so the agent axis is invisible; only waiting_input punches
//     through, and that one is 90% false.
//
// So the two axes stop being collapsed for display: the PRIMARY pill is the
// agent axis reduced to the Display vocabulary below, the SECONDARY chip is
// the delivery axis unchanged, and "does a human need to look at this" is a
// PREDICATE over both (Attention) rather than a value either axis can hold.
//
// Everything in this file takes the PAIR, and everything in lola that
// classifies a session now calls it. The string-keyed equivalents it replaced
// (holdsSlot / present / needsAttention / sortRank / the kanban status sets)
// are gone; what survives in tables.go is the wire vocabulary itself plus
// Notable, which classifies a recorded TRANSITION rather than a session.

// Display is the PRIMARY pill vocabulary: what the agent runner is doing,
// derived from the agent axis ALONE and never masked by PR facts. It is
// deliberately smaller than AgentState — starting collapses into working (a
// spawn that has not yet heartbeat is still a running agent, and a pill that
// flickers "starting" for one cycle is noise), and exited/dead collapse into
// gone (the distinction between "the agent program ended" and "the pane is
// gone" matters to teardown, not to a human reading a pill).
type Display string

const (
	// DisplayWorking is a live turn: AgentStarting or AgentWorking.
	DisplayWorking Display = "working"
	// DisplayIdle is a finished turn with the agent resting at its prompt.
	// It ABSORBS what used to become needs_input via the agent's own 60s idle
	// nudge — "the turn ended and nobody looked" is idleness, not a question.
	DisplayIdle Display = "idle"
	// DisplayNeedsYou is AgentWaitingInput: the agent cannot proceed without a
	// human. The WHY rides alongside on Session.InputReason
	// (question | permission_prompt | dialog | quota_limited), so the pill
	// stays one word while the UI can still label the block.
	DisplayNeedsYou Display = "needs_you"
	// DisplayGone is AgentExited or AgentDead: no agent is running any more.
	DisplayGone Display = "gone"
	// DisplayShell is AgentShell: an agentless checkout (`lola open`), which
	// never had an agent to have a state.
	DisplayShell Display = "shell"
	// DisplayOrphaned is AgentOrphaned: an adoption anomaly (a lola pane with
	// no matching worktree). Reported, never killed.
	DisplayOrphaned Display = "orphaned"
)

// DisplayFor reduces the agent axis to the primary pill vocabulary.
//
// An unrecognized or zero AgentState reports DisplayWorking, matching Rollup's
// own default and KanbanFallbackKey's reasoning: a state the vocabulary has
// not caught up to is most likely a LIVE agent, and showing a live agent as
// "gone" would hide it from the very views built to surface it.
func DisplayFor(a AgentState) Display {
	switch a {
	case AgentIdle:
		return DisplayIdle
	case AgentWaitingInput:
		return DisplayNeedsYou
	case AgentExited, AgentDead:
		return DisplayGone
	case AgentShell:
		return DisplayShell
	case AgentOrphaned:
		return DisplayOrphaned
	}
	// AgentStarting, AgentWorking, and anything unknown.
	return DisplayWorking
}

// AllDisplays is the complete primary-pill vocabulary in a stable order
// (busiest first, terminal last), for UIs that enumerate it — a theme table, a
// filter menu, a legend.
func AllDisplays() []Display {
	return []Display{
		DisplayWorking, DisplayIdle, DisplayNeedsYou,
		DisplayGone, DisplayShell, DisplayOrphaned,
	}
}

// Attention reports whether a session puts a human on the critical path. It is
// a PREDICATE over both axes, not a status value, because the two reasons a
// human is needed live on different axes and can be true at once: the agent is
// blocked on a person (any InputReason — a question, a permission prompt, a
// modal, an exhausted quota), or its delivered work regressed (CI red, a
// reviewer asked for changes, the branch conflicts).
//
// Collapsing these into one word is what made the old needs_input both
// over-broad and lossy: a red CI on a happily working agent had to pick one.
func Attention(a AgentState, d DeliveryState) bool {
	if a == AgentWaitingInput {
		return true
	}
	switch d {
	case DeliveryCIFailed, DeliveryChangesRequested, DeliveryMergeConflict:
		return true
	}
	return false
}

// HoldsSlot reports whether a session occupies an agent slot for the dispatch
// Budget: the agent process is alive AND its PR is neither parked for review
// nor terminal.
//
//   - A dead/exited pane, an agentless shell and an orphan run no agent, so
//     they can never hold a runner slot.
//   - merged/closed are terminal and review_pending/approved are PARKED on a
//     human — holding a slot for those would let a queue of finished PRs stall
//     every new pickup, which is the behavior the axis split inherited intact.
//   - draft, ci_pending, ci_failed, changes_requested and merge_conflict all
//     count: the reaction engine is (or is about to be) re-prompting that
//     agent, so its runner is occupied.
//
// *** DELIBERATE BEHAVIOR CHANGE. *** Under the old string-keyed table an idle
// PRE-PR session held NO slot, because "idle" was not in holdsSlot. That never
// showed up in practice only because such a session became needs_input within
// 60s — the agent's own idle nudge — and needs_input DID hold a slot. Once
// that nudge stops minting needs_input (which is the entire point of the
// Display split above), a pre-PR idle session would sit there forever holding
// no slot, and lola would keep spawning straight past its concurrency cap.
// So a LIVE agent with no PR holds its slot regardless of whether it is
// working, idle, or waiting on a human: the runner is allocated either way,
// and the cap counts runners.
func HoldsSlot(a AgentState, d DeliveryState) bool {
	switch a {
	case AgentDead, AgentExited, AgentShell, AgentOrphaned:
		return false
	}
	switch d {
	case DeliveryMerged, DeliveryClosed, DeliveryReviewPending, DeliveryApproved:
		return false
	}
	return true
}

// Present reports whether a session still counts as "present" for the
// reconcile orphan-revert shield: a present session legitimately explains a
// labeled-but-unworked Linear issue, so the issue must NOT be reverted.
//
// Dead and exited records are gone, and a closed PR no longer shields either —
// the work was explicitly rejected, so the issue has to become revertable
// instead of being shielded forever by a lingering pane.
//
// The three excluding words of the old string-keyed table map one-to-one onto
// the three cases below — "dead" is AgentDead, "session_ended" is AgentExited,
// "closed" is DeliveryClosed — so the classification itself is unchanged. What
// the pair form drops is Rollup's PRECEDENCE, which could hide one of them
// behind another axis: a dead pane over a merged PR rolled up as "merged" and
// so read as present, and an exited agent over any open PR rolled up as that
// PR's delivery word. Both now read gone, which is what the shield is actually
// asking about.
func Present(a AgentState, d DeliveryState) bool {
	switch a {
	case AgentDead, AgentExited:
		return false
	}
	return d != DeliveryClosed
}

// SortRank buckets a session into the attention-first sort tiers (lower sorts
// first), reproducing the legacy tiers over the pair rather than over the
// collapsed word:
//
//	0  blocked on a human
//	1  action needed — the delivered work regressed
//	2  actively working, or a PR still moving under its own steam
//	3  parked for review
//	4  quiet (idle / shell / orphaned / unknown)
//	5  done
//
// The cases are evaluated in exactly the order written, and that order is the
// contract: a working agent whose CI just went red sorts as tier 1 (fix it),
// not tier 2, and a session waiting on a human outranks everything.
func SortRank(a AgentState, d DeliveryState) int {
	if a == AgentWaitingInput {
		return 0
	}
	switch d {
	case DeliveryCIFailed, DeliveryChangesRequested, DeliveryMergeConflict:
		return 1
	}
	if a == AgentWorking || a == AgentStarting {
		return 2
	}
	switch d {
	case DeliveryCIPending, DeliveryDraft:
		return 2
	case DeliveryReviewPending, DeliveryApproved:
		return 3
	}
	switch a {
	case AgentDead, AgentExited:
		return 5
	}
	switch d {
	case DeliveryMerged, DeliveryClosed:
		return 5
	}
	// idle, shell, orphaned, and anything the vocabulary has not caught up to:
	// neither urgent nor terminal.
	return 4
}

// KanbanFallbackKey is the column a session whose PAIR the vocabulary does not
// recognize routes to. The Working column is the safe default: an unrecognized
// state most likely means a live agent lola has not caught up to, and hiding it
// in Done is how a running session disappears from the board.
const KanbanFallbackKey = "working"

// KanbanColumn is one Board column: a stable Key and a human Title.
//
// It carries NO status set any more. Membership is a function of the PAIR and
// is answered by KanbanKeyFor; a list of collapsed status words could not
// express "working agent, red CI" landing in Fixing while its agent axis stays
// visible on the card.
type KanbanColumn struct {
	Key   string
	Title string
}

// KanbanColumns returns the ordered Board columns, left-to-right by human
// triage priority: the leftmost column is the human's queue.
func KanbanColumns() []KanbanColumn {
	return []KanbanColumn{
		{Key: "needs", Title: "Needs You"},
		{Key: "working", Title: "Working"},
		{Key: "fixing", Title: "Fixing"},
		{Key: "review", Title: "In Review"},
		{Key: "done", Title: "Done"},
	}
}

// KanbanKeyFor buckets a session into exactly one Board column.
//
// A session can satisfy several rules at once (a dead pane over a merged PR, a
// waiting agent over a red build), so the order below IS the semantics and the
// first match wins:
//
//  1. done — the session is over, whichever axis ended it. Terminal beats
//     everything: an exited agent parked on a red CI is not work to fix.
//  2. needs — a human is blocked. Ahead of fixing because the human cannot act
//     on the CI failure until they have answered the agent anyway.
//  3. fixing — the delivered work regressed.
//  4. review — parked on someone else.
//  5. working (KanbanFallbackKey) — everything else: working, starting, idle,
//     shell, orphaned, and a PR still moving on its own (draft, ci_pending).
//     Also the catch-all for an unknown state, for KanbanFallbackKey's reason —
//     an unrecognized session is most likely a live one.
func KanbanKeyFor(a AgentState, d DeliveryState) string {
	switch a {
	case AgentDead, AgentExited:
		return "done"
	}
	switch d {
	case DeliveryMerged, DeliveryClosed:
		return "done"
	}
	if a == AgentWaitingInput {
		return "needs"
	}
	switch d {
	case DeliveryCIFailed, DeliveryChangesRequested, DeliveryMergeConflict:
		return "fixing"
	case DeliveryReviewPending, DeliveryApproved:
		return "review"
	}
	return KanbanFallbackKey
}
