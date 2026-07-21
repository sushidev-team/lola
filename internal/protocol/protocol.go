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
// Cmd "revive" is the inverse of a death: Session names a session whose pane
// died but whose worktree survives, and the daemon relaunches its agent in
// place (Claude resumes via --continue when it has a transcript). The session
// must not already be alive. The reply is ReviveData / an error.
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
// Cmd "review" FORCES a QA review PASS for one session now, ignoring the per-PR
// one-shot guard: Session names the target. Provider optionally selects WHICH
// pass provider kind to force (coderabbit-cli | claude-session); "" forces the
// daemon's primary (first enabled) pass provider. The daemon runs one bounded
// pass against the session's worktree and routes the findings the same way the
// PR-open auto-trigger does (notify + optional GitHub/Linear comment + optional
// sanitized, idle-gated worker hand-off), replying Response.Data = ReviewData
// with a short outcome. No matching provider enabled yields a "skipped"
// ReviewData (not an error); an unknown session, a non-pass Provider, or an exec
// failure is an error.
// Cmd "coderabbit" FORCES the [coderabbit] PR-comment WATCH for one session now,
// ignoring the LastCodeRabbitAt watermark: Session names the target. The daemon
// polls the session's open PR (one `gh pr view`) for CodeRabbit-app comments and
// routes any it finds the same way the observer does (notify + optional Linear
// comment + optional sanitized, idle-gated worker hand-off), replying
// Response.Data = CodeRabbitData with a short outcome. Watch disabled / no open
// PR yields a "skipped" CodeRabbitData (not an error); an unknown session or a gh
// failure is an error.
type Request struct {
	Cmd    string `json:"cmd"` // stop|status|reload|enable|disable|pollOnce|sessions|projects|prs|hookEvent|kill|revive|pane|answer|review|coderabbit|open|renameProject
	Poll   string `json:"poll,omitempty"`
	DryRun bool   `json:"dryRun,omitempty"`

	// Provider optionally selects which review provider kind cmd=review forces
	// (coderabbit-cli | claude-session). "" forces the daemon's primary pass
	// provider. Ignored by every other command.
	Provider string `json:"provider,omitempty"`

	// Open fields, set only for cmd=open: manually check out a branch/PR of a
	// project into a throwaway worktree + shell. Project names the [[project]];
	// Ref is the target — a bare PR number (fetched as refs/pull/<n>/head) or a
	// branch name.
	Project string `json:"project,omitempty"`
	Ref     string `json:"ref,omitempty"`

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

	// Args carries the typed argument payload for the project-centric commands
	// (prs, tickets, openManual, …) whose inputs don't fit the flat fields above.
	// Each such handler unmarshals it into its own <Cmd>Args type.
	Args json.RawMessage `json:"args,omitempty"`
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
	// Events is the daemon's activity feed: recent notable session status
	// transitions, NEWEST FIRST, so the TUI renders a live "what's happening"
	// ticker without deriving transitions itself. Empty when nothing notable has
	// happened since the daemon started (the feed is in-memory, so it starts
	// fresh on a daemon restart but survives TUI restarts).
	Events []Event `json:"events,omitempty"`
}

// Event is one session status transition surfaced in the activity feed,
// flattened to render-ready strings so the TUI needs no scm/session imports.
// From is the prior derived status ("" means the session was just spawned); To
// is the new derived status; Ago is a human duration since the transition
// (e.g. "2m", formatted daemon-side against the request time).
type Event struct {
	ID    string `json:"id"`
	Issue string `json:"issue"`           // Linear identifier, e.g. ENG-123 ("" when unknown)
	Title string `json:"title,omitempty"` // Linear issue title, "" when unknown
	From  string `json:"from"`
	To    string `json:"to"`
	Ago   string `json:"ago"`
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

// ProjectsData is Response.Data for cmd=projects: the daemon's cached view of
// every configured [[project]] decorated with live status. Like cmd=sessions it
// is served from in-memory snapshots (config + status tracker + session store)
// and does not exec gh/tmux/git — the only filesystem touches are cheap
// LookPath (agent health) and os.Stat (path/.git probe), never a subprocess. The
// TUI renders the project list from its OWN config and merely decorates rows
// with these facts, so the home screen stays navigable when the daemon is down.
type ProjectsData struct {
	Projects []ProjectInfo `json:"projects"`
}

// ProjectInfo is one configured project flattened to render-ready fields.
type ProjectInfo struct {
	// Name is the project's ID — what paths, tmux names and every other
	// name-keyed protocol field use. Label is its display string, "" when the
	// project has none (render Name then).
	Name          string `json:"name"`
	Label         string `json:"label,omitempty"`
	Path          string `json:"path"`
	Repo          string `json:"repo"`
	DefaultBranch string `json:"defaultBranch"`

	// Per-PROJECT agent health (not the default agent): AgentBin is the resolved
	// coding-agent binary for this project (its override → [defaults].agent →
	// claude), AgentOK whether it plus tmux+git all resolve on PATH, AgentErr the
	// reason when not. The TUI gates this project's spawn verbs on AgentOK.
	Agent    string `json:"agent"`
	AgentBin string `json:"agentBin"`
	AgentOK  bool   `json:"agentOk"`
	AgentErr string `json:"agentErr,omitempty"`

	// PathOK is whether Path exists and is a git checkout (a .git entry); a
	// runtime probe, deliberately NOT config's job. RepoConfigured is whether a
	// GitHub "owner/name" repo is set (needed by the PR picker).
	PathOK         bool `json:"pathOk"`
	RepoConfigured bool `json:"repoConfigured"`

	// Poll rollup: how many polls this project drives and how many are enabled,
	// their names, and the newest LastRun / first LastError across them.
	PollCount    int       `json:"pollCount"`
	PollsEnabled int       `json:"pollsEnabled"`
	Polls        []string  `json:"polls,omitempty"`
	LastRun      time.Time `json:"lastRun"`
	LastError    string    `json:"lastError,omitempty"`

	// Session rollup for this project (from the observer's snapshot store):
	// Sessions total, LiveCounted occupying a slot, NeedsYou parked on a human,
	// CIRed failing CI, OpenPRs with an open PR.
	Sessions    int `json:"sessions"`
	LiveCounted int `json:"liveCounted"`
	NeedsYou    int `json:"needsYou"`
	CIRed       int `json:"ciRed"`
	OpenPRs     int `json:"openPrs"`
}

// PrsArgs is the argument payload for cmd=prs: which project's open PRs to list.
type PrsArgs struct {
	Project string `json:"project"`
	Refresh bool   `json:"refresh,omitempty"` // bypass the TTL cache and re-exec gh
}

// PrsData is Response.Data for cmd=prs: the open pull requests for a project's
// repo, flattened for the picker. Served from a short-TTL cache (the daemon
// execs `gh pr list` on a miss); AgeSeconds/Stale let the TUI show freshness.
type PrsData struct {
	Repo       string  `json:"repo"`
	PRs        []PrRow `json:"prs"`
	AgeSeconds int     `json:"ageSeconds"` // how old the served snapshot is
	Stale      bool    `json:"stale"`      // served past its TTL (a refresh is running/failed)
}

// PrRow is one open PR for the picker.
type PrRow struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	Branch      string `json:"branch"`
	IsDraft     bool   `json:"isDraft"`
	IsFork      bool   `json:"isFork"`
	Checks      string `json:"checks"` // pass|fail|pending|none
	Review      string `json:"review"`
	URL         string `json:"url"`
	Status      string `json:"status"`      // scm.DeriveStatus vocabulary
	AlreadyOpen bool   `json:"alreadyOpen"` // a lola session already holds this branch
}

// OpenManualArgs is the argument payload for cmd=openManual: create a NEW branch
// (off Base, empty → the project's default branch) in a fresh worktree. With
// Agent set it launches the coding agent (seeded with Prompt); otherwise a plain
// shell. The reply is the shared OpenData.
type OpenManualArgs struct {
	Project string `json:"project"`
	Branch  string `json:"branch"`
	Base    string `json:"base,omitempty"`
	Agent   bool   `json:"agent,omitempty"`  // launch the coding agent instead of a shell
	Prompt  string `json:"prompt,omitempty"` // seed prompt when Agent is set
}

// OpenPrArgs is the argument payload for cmd=openPr: open a PR's head branch as a
// TRACKING worktree and launch the coding agent on it (so it can push back).
// IsFork is set by the client for a fork PR — the daemon refuses those (no
// push-back to a fork). The reply is the shared OpenData.
type OpenPrArgs struct {
	Project string `json:"project"`
	Branch  string `json:"branch"`
	Number  int    `json:"number,omitempty"`
	IsFork  bool   `json:"isFork,omitempty"`
}

// OpenURLArgs is the argument payload for cmd=openURL: open a URL in the user's
// default browser, on the DAEMON side, so the socket client stays exec-free.
type OpenURLArgs struct {
	URL string `json:"url"`
}

// TicketsArgs is the argument payload for cmd=tickets: browse a project's Linear
// team for issues to start. Scope is "mine" (assignee = the API key's viewer,
// default) or "team" (the whole team).
type TicketsArgs struct {
	Project string `json:"project"`
	Scope   string `json:"scope,omitempty"`
}

// TicketsData is Response.Data for cmd=tickets: the browsable issues.
type TicketsData struct {
	Team   string      `json:"team"`
	Issues []TicketRow `json:"issues"`
}

// TicketRow is one Linear issue for the picker.
type TicketRow struct {
	Identifier  string  `json:"identifier"`
	UUID        string  `json:"uuid"`
	Title       string  `json:"title"`
	Branch      string  `json:"branch"`
	Priority    float64 `json:"priority"`
	AlreadyLive bool    `json:"alreadyLive"` // a lola session already holds this issue
}

// OpenTicketArgs is the argument payload for cmd=openTicket: start a Linear issue
// on demand — a worktree + agent, deduped exactly like a poll dispatch so a
// running poll cannot spawn it twice. The reply is the shared OpenData.
type OpenTicketArgs struct {
	Project    string `json:"project"`
	Identifier string `json:"identifier"`
	UUID       string `json:"uuid"`
	Branch     string `json:"branch,omitempty"`
	Title      string `json:"title,omitempty"`
}

// RenameProjectArgs is the argument payload for cmd=renameProject: change a
// [[project]]'s IDENTITY (its name), not its display Label — a label is free
// text the client rewrites itself with an ordinary config save.
//
// The name is a path segment and part of every session ID, so the daemon owns
// this: it moves the runtime state that is keyed by the old name and refuses
// outright when anything live still depends on it (see RenameProjectData).
type RenameProjectArgs struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// RenameProjectData is Response.Data for cmd=renameProject. Message is a short
// human-readable outcome; Blockers names the live sessions that made the daemon
// refuse (empty on success), so the client can tell the human exactly what to
// finish before renaming.
type RenameProjectData struct {
	From     string   `json:"from"`
	To       string   `json:"to"`
	Blockers []string `json:"blockers,omitempty"`
	Message  string   `json:"message,omitempty"`
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

// ReviveData is Response.Data for cmd=revive: a dead session relaunched on its
// kept worktree. Revived is always true on the success path (a failure is
// returned as an error instead). TmuxName is the revived session's tmux target
// and Message is a short human-readable outcome for the CLI/TUI to print.
type ReviveData struct {
	Revived  bool   `json:"revived"`
	TmuxName string `json:"tmuxName,omitempty"`
	Message  string `json:"message,omitempty"`
}

// OpenData is Response.Data for cmd=open: a branch/PR manually checked out into
// a throwaway DETACHED worktree with a plain shell (no coding agent), for running
// and testing a PR. SessionID is the created session's ID (and its tmux target),
// Worktree the checkout directory, Branch the human-readable label opened, and
// Message a short human-readable outcome for the CLI/TUI to print.
type OpenData struct {
	SessionID string `json:"sessionId"`
	Worktree  string `json:"worktree,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Message   string `json:"message,omitempty"`
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

// CodeRabbitData is Response.Data for cmd=coderabbit: the outcome of a forced
// PR-comment watch poll, flattened to render-ready fields for the CLI. Message is
// the short human-readable line the CLI prints. Ran reports whether the poll ran
// (a gh call was made); Found is true when it surfaced at least one comment;
// Skipped names why the poll did not run (watch disabled / no open PR), ""
// otherwise. Comments is the full, size-capped comment text (UNTRUSTED,
// attacker-authorable) when Found — present so a caller can display it; empty on
// none/skipped.
type CodeRabbitData struct {
	Ran      bool   `json:"ran"`
	Found    bool   `json:"found"`
	Skipped  string `json:"skipped,omitempty"`
	Comments string `json:"comments,omitempty"`
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
