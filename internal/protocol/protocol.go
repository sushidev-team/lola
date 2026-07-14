// Package protocol defines the newline-delimited JSON messages exchanged
// over the unix socket ~/.lola/lola.sock between the daemon (server) and
// CLI/TUI clients. This file is the contract between internal/daemon and
// internal/tui — keep it dependency-free.
package protocol

import (
	"encoding/json"
	"time"
)

// Request is one line of JSON sent by a client.
//
// Cmd "hookEvent" is the Claude Code → daemon callback path: `lola hook
// <event>` (see internal/hook) runs inside a Claude Code hook and reports
// what just happened in the agent session identified by $LOLA_SESSION.
// Session carries that ID, Event one of the normalized event names below,
// Detail an optional short reason string (notification_type, stop_reason,
// end_reason from the hook's stdin payload). The daemon handler lives in
// internal/daemon: it records the event against the session for state
// derivation and replies Response{OK:true}; an unknown session yields
// OK:false with Error. Hook clients treat any reply — or none — as success:
// a hook must never block or fail an agent's turn.
//
// Normalized Event values:
//
//	"stop"         Stop hook — the agent finished a turn
//	"notification" Notification hook — needs input / permission prompt
//	"session_end"  SessionEnd hook — the session terminated
//	"tool_use"     PostToolUse hook — liveness heartbeat after a tool call
//	"user_prompt"  UserPromptSubmit hook — turn start (clears the AtPrompt gate)
//
// Cmd "kill" tears a session down: Session names the target session ID and
// Force selects whether a dirty worktree (uncommitted changes) is removed
// anyway. The daemon always terminates the agent's tmux session first; a clean
// worktree is then removed and the store entry dropped, while a dirty one is
// kept unless Force is set (the reply is KillData / an error either way).
type Request struct {
	Cmd    string `json:"cmd"` // stop|status|reload|enable|disable|pollOnce|sessions|hookEvent|kill
	Poll   string `json:"poll,omitempty"`
	DryRun bool   `json:"dryRun,omitempty"`

	// Hook callback fields, set only for cmd=hookEvent.
	Session string `json:"session,omitempty"` // lola session ID ($LOLA_SESSION in the agent's pane); also the kill target
	Event   string `json:"event,omitempty"`   // normalized: stop|notification|session_end|tool_use|user_prompt
	Detail  string `json:"detail,omitempty"`  // optional: notification_type / stop_reason / end_reason

	// Force is set only for cmd=kill: remove the worktree even when it has
	// uncommitted changes. Deliberate CLI-only friction (`lola kill <id>
	// --force`); the TUI never sets it.
	Force bool `json:"force,omitempty"`
}

// Response is one line of JSON sent back by the daemon.
type Response struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// StatusData is Response.Data for cmd=status. RuntimeOK reports whether the
// native runtime's external tools (tmux, git, claude) are all resolvable;
// RuntimeErr names what is missing ("" when ok).
type StatusData struct {
	RuntimeOK  bool         `json:"runtimeOk"`
	RuntimeErr string       `json:"runtimeErr,omitempty"`
	LinearOK   bool         `json:"linearOk"`
	Polls      []PollStatus `json:"polls"`
}

type PollStatus struct {
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	LastRun   time.Time `json:"lastRun"`
	LastSpawn time.Time `json:"lastSpawn"`
	Running   bool      `json:"running"` // tick currently executing
	LastError string    `json:"lastError,omitempty"`
}

// SessionsData is Response.Data for cmd=sessions: the daemon's cached view
// of observed agent sessions (PLAN P1). Served from the observer's snapshot
// store — a sessions request never execs ao/gh/tmux.
type SessionsData struct {
	Sessions []SessionInfo `json:"sessions"`
}

// SessionInfo is one observed session, flattened to render-ready strings and
// ints so the TUI never needs scm/session imports or re-derivation.
type SessionInfo struct {
	ID       string `json:"id"`
	Project  string `json:"project"`
	Issue    string `json:"issue"`  // Linear identifier, e.g. ENG-123
	Branch   string `json:"branch"` // "" when unknown
	Status   string `json:"status"` // derived (scm.DeriveStatus / hook-driven states)
	PRURL    string `json:"prUrl"`
	PRNumber int    `json:"prNumber"` // 0 when no PR observed
	Checks   string `json:"checks"`   // pass|fail|pending|none, "" when no PR
	Review   string `json:"review"`   // APPROVED|CHANGES_REQUESTED|REVIEW_REQUIRED, "" otherwise
	TmuxName string `json:"tmuxName"` // "" when no tmux session correlates
	Source   string `json:"source"`   // always "native"; kept for wire compat with pre-P3 clients
	Worktree string `json:"worktree"` // native runtime: the session's git worktree dir; "" otherwise
	Age      string `json:"age"`      // human duration since first observed, e.g. "2h05m"

	// Reaction-engine posture (PLAN P3), flattened so the TUI renders reaction
	// state without importing internal/session or re-deriving it.
	CIRetries int  `json:"ciRetries"` // ci_failed recovery attempts already spent on the current failing streak
	Escalated bool `json:"escalated"` // ci retries exhausted; the session was handed off to a human
	// Reacting is a short human label of the current reaction posture, derived
	// from Status + CIRetries + Escalated: "" (nothing worth surfacing beyond
	// STATUS) | "ci retry 1/2" | "escalated" | "awaiting review" |
	// "addressing review" | "rebasing" | "ready to merge".
	Reacting string `json:"reacting"`
}

// KillData is Response.Data for cmd=kill. Removed reports whether the worktree
// was actually removed (false when the project is gone from config so there was
// nothing safe to target, or on a dirty-refused kill — but a dirty refusal is
// returned as an error, not a KillData). Worktree is the worktree dir the kill
// targeted (removed or kept), "" when none applied. Message is a short
// human-readable outcome for the CLI/TUI to print.
type KillData struct {
	Removed  bool   `json:"removed"`
	Worktree string `json:"worktree,omitempty"`
	Message  string `json:"message,omitempty"`
}

// PollOnceData is Response.Data for cmd=pollOnce.
type PollOnceData struct {
	Poll    string  `json:"poll"`
	DryRun  bool    `json:"dryRun"`
	Matches []Match `json:"matches"`
}

// Match describes one matched issue and what the tick did (or would do) with it.
type Match struct {
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	Action     string `json:"action"`           // spawned|would-spawn|skipped
	Reason     string `json:"reason,omitempty"` // dedup-label|dedup-seen|in-flight|capped|error
}
