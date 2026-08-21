// Package state is the single home of lola's session-status vocabulary.
//
// A session's condition is two ORTHOGONAL axes, not one string:
//
//   - AgentState — what the coding agent itself is doing (working, waiting on
//     a human, idle at the prompt, exited, pane dead …). Owned by lifecycle
//     hooks, the pane classifier, and tmux liveness. NEVER masked by PR facts.
//   - DeliveryState — where the pull request stands (draft, CI red, awaiting
//     review, merged …). Owned solely by observed gh facts.
//
// Rollup composes the two axes into the legacy one-string status that the
// wire protocol, counting tables, reactions, and both UIs key by. It is the
// ONLY producer of that vocabulary; nothing else may mint a status string.
// The consumer tables (tables.go) are likewise the only slot/attention/
// notability classifications — the previous four divergent copies in
// daemon/tui are gone.
//
// This package is a leaf: stdlib + internal/scm (itself a pure gh-facts leaf)
// only. It must not import config/session/daemon/tui.
package state

import "strings"

// AgentState is the agent axis: what the coding agent process is doing right
// now, independent of any PR.
type AgentState string

const (
	// AgentStarting is a freshly spawned session before its first heartbeat
	// (hook event or positive pane cue). Rolls up as "working" so the
	// dispatch budget counts the slot from the moment of spawn.
	AgentStarting AgentState = "starting"
	// AgentWorking is positive evidence of an in-flight turn (tool_use /
	// user_prompt hook, working pane cue, fresh tmux output).
	AgentWorking AgentState = "working"
	// AgentWaitingInput means the agent is blocked on a human: a Notification
	// hook, or a definite waiting pane with an answerable question.
	AgentWaitingInput AgentState = "waiting_input"
	// AgentIdle is a finished turn: the agent sits at its prompt (Stop hook),
	// or the anti-false-working guard gave up believing "working".
	AgentIdle AgentState = "idle"
	// AgentExited means the agent program ended its session (SessionEnd hook)
	// while the tmux pane may still be alive.
	AgentExited AgentState = "exited"
	// AgentDead means the tmux pane itself is gone.
	AgentDead AgentState = "dead"
	// AgentShell is an agentless checkout (`lola open`): a plain shell, no
	// coding agent to have a state.
	AgentShell AgentState = "shell"
	// AgentOrphaned is an adoption anomaly: a lola tmux pane with no matching
	// worktree. Reported, never killed.
	AgentOrphaned AgentState = "orphaned"
)

// InputReason says WHY an agent is waiting_input, so the UI can label the
// block ("permission prompt" vs "question") instead of a bare needs_input.
type InputReason string

const (
	// InputPermission is a tool/permission approval prompt (Claude
	// Notification mentioning permission, codex approval-requested).
	InputPermission InputReason = "permission_prompt"
	// InputQuestion is an answerable question detected in the pane.
	InputQuestion InputReason = "question"
	// InputIdleNotify is the agent's own "waiting for your input" nudge with
	// no more specific evidence.
	InputIdleNotify InputReason = "idle_notification"
	// InputDialog is a MODAL the agent put up over its own pane (claude-code's
	// setup/onboarding overlays), detected from the pane as attention.
	// ActivityBlocked. Like InputPermission it is a keypress-driven form, not a
	// composer, so it must never be admitted by a send-keys gate — typed prose is
	// swallowed by the widget and the submit Enter answers the dialog.
	InputDialog InputReason = "dialog"
	// InputQuotaLimited means the agent's pane shows its own usage-limit banner
	// (attention.ActivityQuotaLimited): the turn is over and the agent cannot
	// take another until the quota resets. The remedy is a hand-off to a
	// fallback agent (internal/daemon/fallback.go), not an answer — so like
	// InputDialog it must never be admitted by a send-keys gate.
	InputQuotaLimited InputReason = "quota_limited"
)

// ActivitySource records which signal last stamped LastActivityAt, so a
// displayed "active 2m ago" can say how it knows.
type ActivitySource string

const (
	SourceHook         ActivitySource = "hook"
	SourcePane         ActivitySource = "pane"
	SourceTmuxActivity ActivitySource = "tmux_activity"
)

// ClassifyNotification maps a Notification hook's message/type to an
// InputReason. Deterministic string matching only — the message is rendered
// agent output and must never be executed or fed back anywhere.
func ClassifyNotification(message, notificationType string) InputReason {
	m := strings.ToLower(message + " " + notificationType)
	if strings.Contains(m, "permission") || strings.Contains(m, "approval") {
		return InputPermission
	}
	return InputIdleNotify
}

// FromLegacy backfills the two axes from a pre-axis snapshot record: the
// collapsed status string plus the persisted PR facts. The delivery axis is
// exact (PR facts were persisted); the agent axis is exact for agent-owned
// statuses and unknowable for delivery-owned ones, where AgentIdle is the
// safe seed — the first hook event or pane classification corrects it, and
// Rollup(AgentIdle, d) reproduces the stored status either way.
func FromLegacy(status string, delivery DeliveryState) (AgentState, DeliveryState) {
	switch status {
	case "working":
		return AgentWorking, delivery
	case "idle":
		return AgentIdle, delivery
	case "needs_input":
		return AgentWaitingInput, delivery
	case "session_ended":
		return AgentExited, delivery
	case "dead", "no_pr":
		return AgentDead, delivery
	case "shell":
		return AgentShell, delivery
	case "orphaned":
		return AgentOrphaned, delivery
	}
	return AgentIdle, delivery
}
