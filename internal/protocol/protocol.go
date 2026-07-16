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
//
// Cmd "pane" is the read-only compact-pane view (PLAN P7): Session names the
// target session and Lines optionally bounds how many trailing rows of its tmux
// pane to capture (0 → the daemon's default, ~40). The daemon captures the pane
// and runs the attention parser over it, replying Response.Data = PaneData (the
// rendered text plus any extracted question). An unknown session is an error.
//
// Cmd "answer" delivers a HUMAN's inline reply to a session that stopped for
// input: Session names the target and Text is the answer typed back into the
// agent's pane (send-keys + Enter). It is REFUSED unless the session's derived
// Status is "needs_input" — the one moment the agent is provably parked at its
// prompt, so typing cannot corrupt a mid-turn agent (the send-keys safety gate,
// PLAN P3/P7). The reply is OK on a delivered answer, an error otherwise.
// Cmd "review" FORCES the P9 QA review pass (CodeRabbit) for one session now,
// ignoring the per-PR one-shot guard: Session names the target. The daemon runs
// one bounded `coderabbit review` against the session's worktree and routes the
// findings the same way the PR-open auto-trigger does (notify + optional Linear
// comment + optional sanitized, idle-gated worker hand-off), replying
// Response.Data = ReviewData with a short outcome. Review disabled / no
// coderabbit yields a "skipped" ReviewData (not an error); an unknown session or
// an exec failure is an error.
type Request struct {
	Cmd    string `json:"cmd"` // stop|status|reload|enable|disable|pollOnce|sessions|hookEvent|kill|pane|answer|review
	Poll   string `json:"poll,omitempty"`
	DryRun bool   `json:"dryRun,omitempty"`

	// Hook callback fields, set only for cmd=hookEvent.
	Session string `json:"session,omitempty"` // lola session ID ($LOLA_SESSION in the agent's pane); also the kill/pane/answer target
	Event   string `json:"event,omitempty"`   // normalized: stop|notification|session_end|tool_use|user_prompt
	Detail  string `json:"detail,omitempty"`  // optional: notification_type / stop_reason / end_reason

	// Force is set only for cmd=kill: remove the worktree even when it has
	// uncommitted changes. Deliberate CLI-only friction (`lola kill <id>
	// --force`); the TUI never sets it.
	Force bool `json:"force,omitempty"`

	// Text is the human's answer for cmd=answer — typed verbatim into the
	// session's pane (send-keys appends Enter).
	Text string `json:"text,omitempty"`

	// Lines optionally bounds cmd=pane's capture to the last N rendered rows of
	// the target pane; 0 means the daemon's default (~40).
	Lines int `json:"lines,omitempty"`
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
	Title    string `json:"title"`  // Linear issue title, "" when unknown (older/adopted records)
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

// PaneData is Response.Data for cmd=pane (PLAN P7): the captured tmux pane text
// plus the attention parser's read of it, flattened so the TUI renders an
// actionable answer card without importing internal/attention (and so protocol
// stays dependency-free). Text is the rendered pane (ANSI preserved, as
// capture-pane -e returns it). HasQuestion reports whether a prompt was
// detected; when true, Prompt is the question line, Choices enumerates any
// pick-one options the agent offered (empty for a pure free-text prompt),
// and FreeForm reports whether a typed reply is expected. Both a choice list and
// FreeForm can be surfaced; the human either picks a Choice.Key or types text,
// and either is delivered back via cmd=answer.
type PaneData struct {
	Text        string       `json:"text"`
	HasQuestion bool         `json:"hasQuestion"`
	Prompt      string       `json:"prompt,omitempty"`
	Choices     []PaneChoice `json:"choices,omitempty"`
	FreeForm    bool         `json:"freeForm,omitempty"`
}

// PaneChoice is one enumerated option the agent offered at its prompt. Key is
// what the human sends to select it (a menu number/letter); Label is the
// human-readable option text.
type PaneChoice struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// ReviewData is Response.Data for cmd=review (PLAN P9): the outcome of a forced
// QA review pass, flattened to render-ready fields for the CLI. Message is the
// short human-readable line the CLI prints. Ran reports whether the review exec
// ran; Clean is true only when it ran and found nothing; Skipped names why the
// pass did not run (review disabled / no project), "" otherwise. Findings is the
// full trimmed, size-capped findings text (UNTRUSTED, diff-derived) when the run
// found issues — present so a caller can display it; empty on clean/skipped.
type ReviewData struct {
	Ran      bool   `json:"ran"`
	Clean    bool   `json:"clean"`
	Skipped  string `json:"skipped,omitempty"`
	Findings string `json:"findings,omitempty"`
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
