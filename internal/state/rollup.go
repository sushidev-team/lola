package state

// Rollup composes the two axes into the legacy one-string status. It is the
// ONLY producer of that vocabulary — nothing else may mint a status string.
//
// LEGACY WIRE SHIM. Rollup no longer describes how lola thinks about a
// session: collapsing the agent axis into the delivery axis is exactly what
// display.go exists to undo (see the measurements at the top of that file —
// 90% of the needs_input population was a 60s idle nudge, and post-PR the
// agent axis vanished entirely). Its ONE remaining job is
// protocol.SessionInfo.status, which mobile/ still reads and which therefore
// must keep carrying the historical 16-word vocabulary byte for byte. The
// in-repo consumers move to the pair-based tables in display.go; this function
// stays until the wire field can be retired, and its output must not change.
//
// Priority order, chosen to be behavior-compatible with the old
// nativeStatus + DeriveStatus composition (each rule cites the old behavior
// it preserves):
//
//  1. merged wins over everything, including a dead pane — a merged PR is
//     the one legitimate way for a session to end.
//  2. a dead pane forces "dead" unless merged.
//  3. waiting_input outranks every PR state except merged (the old
//     needs_input rescue): a human is being waited on, whatever CI says.
//  4. closed, then any other delivery state: PR facts own the rolled-up
//     status while a PR exists (the agent axis stays visible separately on
//     the wire — that is the point of the split).
//  5. pre-PR the agent axis is the status: orphaned/shell/exited pass
//     through, starting rolls up as "working" (a spawned session occupies
//     its slot immediately), idle is idle.
func Rollup(a AgentState, d DeliveryState) string {
	switch {
	case d == DeliveryMerged:
		return "merged"
	case a == AgentDead:
		return "dead"
	case a == AgentWaitingInput:
		return "needs_input"
	case d == DeliveryClosed:
		return "closed"
	case d != DeliveryNone && d != "":
		return string(d)
	case a == AgentOrphaned:
		return "orphaned"
	case a == AgentShell:
		return "shell"
	case a == AgentExited:
		return "session_ended"
	case a == AgentIdle:
		return "idle"
	default: // starting, working, or an unknown live state
		return "working"
	}
}
