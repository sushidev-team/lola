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
type Request struct {
	Cmd    string `json:"cmd"` // stop|status|reload|enable|disable|pollOnce|sessions
	Poll   string `json:"poll,omitempty"`
	DryRun bool   `json:"dryRun,omitempty"`
}

// Response is one line of JSON sent back by the daemon.
type Response struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// StatusData is Response.Data for cmd=status.
type StatusData struct {
	AORunning bool         `json:"aoRunning"`
	LinearOK  bool         `json:"linearOk"`
	Polls     []PollStatus `json:"polls"`
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
	Status   string `json:"status"` // derived (scm.DeriveStatus / AO attention states)
	PRURL    string `json:"prUrl"`
	PRNumber int    `json:"prNumber"` // 0 when no PR observed
	Checks   string `json:"checks"`   // pass|fail|pending|none, "" when no PR
	Review   string `json:"review"`   // APPROVED|CHANGES_REQUESTED|REVIEW_REQUIRED, "" otherwise
	TmuxName string `json:"tmuxName"` // "" when no tmux session correlates
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
