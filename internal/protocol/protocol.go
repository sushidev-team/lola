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
type Request struct {
	Cmd    string `json:"cmd"` // stop|status|reload|enable|disable|pollOnce|sessions|hookEvent
	Poll   string `json:"poll,omitempty"`
	DryRun bool   `json:"dryRun,omitempty"`

	// Hook callback fields, set only for cmd=hookEvent.
	Session string `json:"session,omitempty"` // lola session ID ($LOLA_SESSION in the agent's pane)
	Event   string `json:"event,omitempty"`   // normalized: stop|notification|session_end|tool_use
	Detail  string `json:"detail,omitempty"`  // optional: notification_type / stop_reason / end_reason
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
