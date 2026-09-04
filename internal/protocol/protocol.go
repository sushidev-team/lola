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
// pass provider kind to force (any pass kind — cli or agent); "" forces the
// daemon's primary (first enabled) pass provider. The daemon runs one bounded
// pass against the session's worktree and routes the findings the same way the
// PR-open auto-trigger does (notify + optional GitHub/Linear comment + optional
// sanitized, idle-gated worker hand-off), replying Response.Data = ReviewData
// with a short outcome. No matching provider enabled yields a "skipped"
// ReviewData (not an error); an unknown session, a non-pass Provider, or an exec
// failure is an error.
// Cmd "dev" moves the project's DEV PROCESSES onto one session, or stops them:
// Args is a DevArgs naming the session and whether to switch it on. Activating
// runs every [[project]].dev_commands entry in its own "<id>-dev-N" tmux tab
// rooted in that session's worktree, after killing the tabs of whichever session
// of the SAME project held them — only one session per project may run them,
// because they bind ports. Switching off kills this session's tabs. The reply is
// DevData. A session whose project configures no dev_commands is an error, as is
// an unknown session.
//
// Cmd "devFreePort" resolves a PORT CLASH a dev tab died on: Args is a
// DevFreePortArgs naming the session plus the port and pid the client was shown.
// The daemon kills that process's group tree and restarts the session's dev
// tabs, replying DevFreePortData. It is the one path that signals a process
// lola did not start and does not own, so it is deliberately narrow: it is only
// ever reached by a human answering a dialog, the port/pid must match the clash
// the daemon has on record (SessionInfo.DevClash), that pid must STILL hold that
// port when the request arrives (pids are reused), and a process group owning a
// live tmux pane is refused outright. Anything else is an error and nothing is
// signalled.
//
// Cmd "resolveConflict" asks a CONFLICTING session's coding agent to merge the
// project's default_branch into its branch and resolve the conflicts: Session
// names the target. It is the manual trigger for what [reactions].merge_conflict
// does on its own, so it obeys the same send-keys safety rules — the session's
// DELIVERY axis must be merge_conflict, its project must still be in config
// (its default_branch is the whole content of the instruction), and the agent
// must be provably resting at its prompt right now. Unlike the reaction it does
// NOT defer: a mid-turn agent is an error, because the caller is a human
// watching a button. The reply is ResolveConflictData naming the branch asked
// for.
//
// Cmd "coderabbit" FORCES the [coderabbit] PR-comment WATCH for one session now,
// ignoring the LastCodeRabbitAt watermark: Session names the target. The daemon
// polls the session's open PR (one `gh pr view`) for CodeRabbit-app comments and
// routes any it finds the same way the observer does (notify + optional Linear
// comment + optional sanitized, idle-gated worker hand-off), replying
// Response.Data = CodeRabbitData with a short outcome. Watch disabled / no open
// PR yields a "skipped" CodeRabbitData (not an error); an unknown session or a gh
// failure is an error.
//
// Cmd "pairBegin" asks for everything a phone needs to reach this daemon's
// remote listener — addresses, port, SPKI pin and, in an M1 build, the bearer
// key — as PairBeginData. It takes no arguments: the answer describes the
// LISTENER THAT IS RUNNING, which is the whole point of asking the daemon
// rather than reading a file. ~/.lola/remote-dev-key is only the live key when
// the script that wrote it also started the daemon, and a desktop that rendered
// a code from a stale file would produce a scan the daemon refuses with
// "authenticate first" — indistinguishable from a bad camera read.
//
// It is refused for every REMOTE peer unconditionally (see internal/remote's
// deniedCommands, which has listed it since before it existed): enrolment is a
// local operation at the machine, and a phone that could ask for the key could
// enrol a second device that survives revoking the first. Over the unix socket
// it is answered, because anything that can open ~/.lola/lola.sock already
// reaches cmd=answer and therefore already has more than the key grants.
//
// The handler is TAG-SPLIT. A release binary has no bearer-key path at all, so
// it answers with an error naming that rather than an empty code; only a
// -tags lola_insecure daemon can fill PairBeginData.Key.
type Request struct {
	Cmd    string `json:"cmd"` // stop|status|reload|enable|disable|pollOnce|sessions|projects|prs|hookEvent|kill|revive|pane|answer|review|coderabbit|resolveConflict|switchAgent|dev|devFreePort|open|renameProject|pairBegin
	Poll   string `json:"poll,omitempty"`
	DryRun bool   `json:"dryRun,omitempty"`

	// Provider optionally selects which review provider kind cmd=review forces
	// (any pass kind — cli or agent). "" forces the daemon's primary pass
	// provider. Ignored by every other command.
	Provider string `json:"provider,omitempty"`

	// Open fields, set only for cmd=open: manually check out a branch/PR of a
	// project into a throwaway worktree + shell. Project names the [[project]];
	// Ref is the target — a bare PR number (fetched as refs/pull/<n>/head) or a
	// branch name.
	Project string `json:"project,omitempty"`
	Ref     string `json:"ref,omitempty"`

	// Hook callback fields, set only for cmd=hookEvent.
	Session string       `json:"session,omitempty"` // lola session ID ($LOLA_SESSION in the agent's pane); also the kill/pane/answer target
	Event   string       `json:"event,omitempty"`   // normalized: stop|notification|session_end|tool_use|user_prompt
	Detail  string       `json:"detail,omitempty"`  // optional: notification_type / stop_reason / end_reason
	Hook    *HookPayload `json:"hook,omitempty"`    // structured payload fields; nil from pre-payload hook binaries

	// Force is set only for cmd=kill: remove the worktree even when it has
	// uncommitted changes. Deliberate CLI-only friction (`lola kill <id>
	// --force`); the TUI never sets it.
	Force bool `json:"force,omitempty"`

	// Text is the human's answer for cmd=answer — typed verbatim into the
	// session's pane (send-keys appends Enter).
	Text string `json:"text,omitempty"`

	// Cols and Rows optionally state the size the CLIENT can show, for
	// cmd=shellCreate: the tmux session is created at that size instead of
	// tmux's default.
	//
	// It exists so a phone's shell tab does not have to be REFLOWED into shape
	// after the fact. Created at tmux's own size a shell is typically 157x37,
	// so a phone pinned it a moment later and the tab visibly redrew itself
	// line by line for several seconds. Born at the right size there is nothing
	// to redraw. Both are ignored unless positive, and clamped like every other
	// dimension on this surface, so a client that does not send them (or sends
	// nonsense) gets exactly the old behaviour.
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`

	// Lines optionally bounds cmd=pane's capture to the last N rendered rows of
	// the target pane; 0 means the daemon's default (~40).
	Lines int `json:"lines,omitempty"`

	// Args carries the typed argument payload for the project-centric commands
	// (prs, tickets, openManual, …) whose inputs don't fit the flat fields above.
	// Each such handler unmarshals it into its own <Cmd>Args type.
	Args json.RawMessage `json:"args,omitempty"`
}

// HookPayload carries the structured fields the `lola hook` CLI extracts from
// a lifecycle hook's stdin (Claude Code writes a JSON event payload there) or
// from a codex notify argv payload. Every field is optional and size-capped by
// the CLI before it touches the wire. Message and Prompt are rendered
// agent/user text: DISPLAY-ONLY on the daemon side — never executed, never
// send-keys'd, never fed back to any agent.
type HookPayload struct {
	ToolName       string `json:"toolName,omitempty"`       // PostToolUse: which tool ran
	Message        string `json:"message,omitempty"`        // Notification text / codex last-assistant-message
	Prompt         string `json:"prompt,omitempty"`         // UserPromptSubmit: the submitted prompt
	TranscriptPath string `json:"transcriptPath,omitempty"` // the agent's own transcript file
	AgentSessionID string `json:"agentSessionId,omitempty"` // the agent's internal conversation id
	Reason         string `json:"reason,omitempty"`         // notification_type / stop_reason / end_reason
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

	// Host is this machine's name, for a client that has to say WHICH daemon it
	// is talking about. A phone reaches the same Mac on a different address at
	// home and at the office — that is the point of discovery — so an address
	// is a poor name for it and a stale one is worse: "connecting to
	// 192.168.10.160" describes a network, not the machine somebody left work
	// running on.
	//
	// It travels on an AUTHENTICATED answer, never in the mDNS advertisement,
	// and the difference is deliberate. A hostname in a TXT record is a stable
	// cross-network correlator broadcast to every peer (internal/mdns says so
	// at length); telling an already-paired device the name of the machine it
	// is holding a session list from discloses nothing it does not have.
	Host string `json:"host,omitempty"`
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
	Agent    string `json:"agent"`  // coding-agent kind driving the pane: claude|codex|opencode ("" = legacy claude)
	Status   string `json:"status"` // the rolled-up status (state.Rollup vocabulary)
	PRURL    string `json:"prUrl"`
	PRNumber int    `json:"prNumber"` // 0 when no PR observed
	Checks   string `json:"checks"`   // pass|fail|pending|none, "" when no PR
	Review   string `json:"review"`   // APPROVED|CHANGES_REQUESTED|REVIEW_REQUIRED, "" otherwise
	TmuxName string `json:"tmuxName"` // "" when no tmux session correlates
	Source   string `json:"source"`   // always "native"; kept for wire compat with pre-P3 clients
	Worktree string `json:"worktree"` // native runtime: the session's git worktree dir; "" otherwise
	Age      string `json:"age"`      // human duration since first observed, e.g. "2h05m"

	// The two state axes underneath Status (see internal/state), with raw
	// freshness timestamps so a client can render a live "ago" between
	// refreshes. All omitempty: absent on an older daemon.
	AgentState       string    `json:"agentState,omitempty"`       // starting|working|waiting_input|idle|exited|dead|shell|orphaned
	Delivery         string    `json:"delivery,omitempty"`         // none|draft|ci_pending|…|merged|closed
	StatusSince      time.Time `json:"statusSince,omitzero"`       // when the rolled-up Status last changed
	AgentStateSince  time.Time `json:"agentStateSince,omitzero"`   // when the agent axis last changed
	LastActivityAt   time.Time `json:"lastActivityAt,omitzero"`    // last POSITIVE evidence of work
	ActivitySource   string    `json:"activitySource,omitempty"`   // hook|pane|tmux_activity|transcript
	PRObservedAt     time.Time `json:"prObservedAt,omitzero"`      // last successful gh PR fetch
	PRStale          bool      `json:"prStale,omitempty"`          // PR facts are ≥3 failed fetches old
	AtPrompt         bool      `json:"atPrompt,omitempty"`         // agent idle at its prompt (send-keys gate open)
	InputReason      string    `json:"inputReason,omitempty"`      // why waiting_input: permission_prompt|question|idle_notification|dialog
	CurrentTool      string    `json:"currentTool,omitempty"`      // tool the in-flight turn runs right now (PostToolUse)
	LastNotification string    `json:"lastNotification,omitempty"` // last Notification message (display-only text)

	// [statusagent] interpreter overlay — untrusted LLM text, DISPLAY ONLY,
	// pre-gated daemon-side (confidence, freshness, supersession): a client
	// renders it verbatim or not at all. InterpretedState is set ONLY when the
	// interpreter DISAGREES with agentState (render with an "approx" marker);
	// Headline ships whenever a valid judgement exists.
	InterpretedState string `json:"interpretedState,omitempty"` // working|waiting_input|idle|stuck; "" = no override
	Headline         string `json:"headline,omitempty"`         // one line: what the agent is doing right now
	WaitingOn        string `json:"waitingOn,omitempty"`        // what the agent needs, when blocked
	HeadlineAgo      string `json:"headlineAgo,omitempty"`      // formatted age of the judgement, e.g. "2m"

	// Dev processes ([[project]].dev_commands). DevActive is true while this
	// session holds them — derived from live tmux facts each observe cycle, so a
	// closed tab or a crashed command reads as inactive within one cycle.
	// DevCommands is its project's configured list (in tab order, so index N-1
	// labels tab "<id>-dev-N"); a client renders the Active toggle only when it
	// is non-empty.
	// DevURLs are the local testing addresses those tabs printed
	// ("http://127.0.0.1:8001"), best first, scraped from the panes by
	// internal/devurl — lola cannot know what port the command picked, and a
	// client should offer the link rather than make a human read it out of a
	// scrolling log. Derived like DevActive: empty the moment the tabs stop.
	// Only http(s) on a loopback host ever appears here, because a client hands
	// it to an opener and pane text is untrusted.
	// DevClash is set while a dev tab of this session is dead BECAUSE another
	// process holds the port it wanted — the one dev failure lola can name and
	// offer to undo (cmd=devFreePort). nil whenever the tabs are healthy.
	// DevForwards are those same servers republished on the local network, so a
	// phone can open them: the loopback addresses above are unreachable from
	// anything but this machine. Present only while the session is ACTIVE and
	// only when [remote].dev_forward is set, and each one ends with the tabs it
	// belongs to. Empty is the normal state.
	DevActive   bool          `json:"devActive,omitempty"`
	DevCommands []string      `json:"devCommands,omitempty"`
	DevURLs     []string      `json:"devUrls,omitempty"`
	DevForwards []DevForward  `json:"devForwards,omitempty"`
	DevClash    *DevClashInfo `json:"devClash,omitempty"`

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
	// Groups is the configured [[group]] table in FILE ORDER — the UI folders
	// projects are filed under. It ships beside the projects rather than being
	// derived from them because an EMPTY group is a real, renderable thing: the
	// app creates the folder first and lets projects be dragged into it after.
	Groups []GroupInfo `json:"groups,omitempty"`
}

// GroupInfo is one configured [[group]] flattened for rendering. It carries no
// live facts because a group has none — it is arrangement only, and nothing in
// the daemon's control loop reads it.
type GroupInfo struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
	// Position is the group's index among the TOP-LEVEL rows — the sidebar draws
	// folders beside the ungrouped projects, not in a section below them, so a
	// group carries its own place in that list. See config.Group.
	Position  int  `json:"position"`
	Collapsed bool `json:"collapsed,omitempty"`
}

// ProjectInfo is one configured project flattened to render-ready fields.
type ProjectInfo struct {
	// Name is the project's ID — what paths, tmux names and every other
	// name-keyed protocol field use. Label is its display string, "" when the
	// project has none (render Name then).
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
	// Group is the [[group]] Name this project is filed under, "" for the top
	// level. Always resolves to a group present in ProjectsData.Groups — config
	// repairs a dangling reference to "" on load.
	Group         string `json:"group,omitempty"`
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
	Status      string `json:"status"`      // state.DeliveryState vocabulary (daemon.openPRStatus)
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
	// AgentKind optionally overrides WHICH coding agent runs (claude|codex|
	// opencode) instead of the project's configured default. "" = configured.
	AgentKind string `json:"agentKind,omitempty"`
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

// TicketsData is Response.Data for cmd=tickets: the browsable issues. Team is
// the UUID config keys by; TeamName/TeamKey are its resolved display identity —
// both may be empty (the lookup fails open), so a client renders the UUID only
// as a last resort.
type TicketsData struct {
	Team     string      `json:"team"`
	TeamName string      `json:"teamName,omitempty"`
	TeamKey  string      `json:"teamKey,omitempty"`
	Issues   []TicketRow `json:"issues"`
}

// TicketRow is one Linear issue for the picker. Everything past Branch is
// DISPLAY: it tells a human which issue to pick (what state it is in, who holds
// it, how stale it is) and is never read back on the openTicket path.
type TicketRow struct {
	Identifier string  `json:"identifier"`
	UUID       string  `json:"uuid"`
	Title      string  `json:"title"`
	Branch     string  `json:"branch"`
	Priority   float64 `json:"priority"`
	// State is the team's own workflow-state name ("In Progress"); StateType the
	// stable enum behind it (triage|backlog|unstarted|started|completed|canceled),
	// which is what a client colours and sorts by — names are per-team text.
	State     string   `json:"state,omitempty"`
	StateType string   `json:"stateType,omitempty"`
	Assignee  string   `json:"assignee,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	Estimate  float64  `json:"estimate,omitempty"`
	// Updated is a pre-formatted age since the issue last changed ("2h05m"),
	// formatted daemon-side exactly like SessionInfo.Age so both surfaces read
	// the same and neither has to parse a timestamp.
	Updated     string `json:"updated,omitempty"`
	AlreadyLive bool   `json:"alreadyLive"` // a lola session already holds this issue
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
	// AgentKind optionally overrides WHICH coding agent runs (claude|codex|
	// opencode) instead of the project's configured default. "" = configured.
	AgentKind string `json:"agentKind,omitempty"`
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

// DevArgs is Request.Args for cmd=dev: Session names the target session and On
// selects activation (true) or teardown (false).
type DevArgs struct {
	Session string `json:"session"`
	On      bool   `json:"on"`
}

// DevData is Response.Data for cmd=dev. Active mirrors the resulting state
// (true when tabs are running), Commands are the dev_commands the tabs run in
// order, Stopped names the session whose tabs were taken over ("" when none
// were), and Message is the short human-readable outcome for the CLI/TUI.
type DevData struct {
	Active   bool     `json:"active"`
	Commands []string `json:"commands,omitempty"`
	Stopped  string   `json:"stopped,omitempty"`
	Message  string   `json:"message,omitempty"`
}

// DevForward is one dev server republished on the local network: where a phone
// goes, and which loopback address it is.
//
// BOTH, because the forward's port is allocated by the kernel and means nothing
// to anybody — "192.168.20.3:65497" identifies no application. The address the
// developer knows is the ORIGINAL: 8000 is the Laravel app, 5175 is vite. A
// client showing only the forward makes its user guess which link is which,
// which with an app and a bundler is a coin flip.
type DevForward struct {
	// URL is the address to open, on the network the daemon is on.
	URL string `json:"url"`
	// From is the loopback address it publishes ("127.0.0.1:8000").
	From string `json:"from"`
}

// DevClashInfo is why a dev tab is dead when the reason is a port another
// process already holds — the flattened session.DevClash, rendered by both UIs
// beside the dev toggle.
//
// It is EVIDENCE for a question, not a verdict: a client shows it and may offer
// cmd=devFreePort, which re-checks that this pid still holds this port before
// signalling anything. Port is the only value that came out of the terminal (an
// integer; see internal/portclash) — Proc and Dir come from lsof, Command from
// config.
type DevClashInfo struct {
	Tab     string `json:"tab"`
	Command string `json:"command,omitempty"`
	Port    int    `json:"port"`
	PID     int    `json:"pid"`
	Proc    string `json:"proc,omitempty"`
	Dir     string `json:"dir,omitempty"`
	// Ours reports whether the holder is listening from inside lola's worktrees
	// for this project (a stray server of an earlier session) rather than from
	// the user's own checkout. A client should word its confirmation
	// differently for the two: reclaiming lola's own leftover is routine,
	// killing the user's process is not.
	Ours bool `json:"ours,omitempty"`
}

// DevFreePortArgs is the argument payload for cmd=devFreePort: kill the process
// holding the port a session's dev tab died on, then start that session's dev
// tabs again. Session names the session, and Port/PID must MATCH the clash the
// daemon currently has on record — a stale dialog (the holder exited, another
// process took the port, the tabs were restarted meanwhile) is refused rather
// than applied to whatever is there now.
type DevFreePortArgs struct {
	Session string `json:"session"`
	Port    int    `json:"port"`
	PID     int    `json:"pid"`
}

// DevFreePortData is Response.Data for cmd=devFreePort. Freed reports whether
// the holder was signalled, Dev the outcome of the restart that followed, and
// Message the short human-readable summary for the CLI/TUI.
type DevFreePortData struct {
	Freed   bool    `json:"freed"`
	Port    int     `json:"port"`
	Dev     DevData `json:"dev"`
	Message string  `json:"message,omitempty"`
}

// ResolveConflictData is Response.Data for cmd=resolveConflict. Branch is the
// project's default_branch the agent was asked to merge (so a client can say
// which branch it named rather than repeating its own guess), and Message is the
// short human-readable outcome.
type ResolveConflictData struct {
	Branch  string `json:"branch"`
	Message string `json:"message,omitempty"`
}

// SwitchAgentArgs is the argument payload for cmd=switchAgent: replace the
// session's coding agent with a DIFFERENT kind on the same worktree and branch
// (SUSHI-585). Session names the target, Agent the new kind (claude|codex|
// opencode). The old pane is stopped, a .lola/handoff.md briefing is written,
// and the new agent launches fresh on the kept checkout. Refused for an
// unknown session, an agentless shell, an invalid kind, the kind already
// running, or a kind whose binary is not on PATH.
type SwitchAgentArgs struct {
	Session string `json:"session"`
	Agent   string `json:"agent"`
}

// SwitchAgentData is Response.Data for cmd=switchAgent. Agent is the kind now
// running; Message is the short human-readable outcome.
type SwitchAgentData struct {
	Agent   string `json:"agent"`
	Message string `json:"message,omitempty"`
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

// PaneInfo is one pane of a session, as cmd=panes reports it.
//
// Kind is the ROLE — "agent", "shell", "dev" or "review" — so a client can group
// and label a tab strip without re-deriving lola's naming convention, which
// lives in internal/runtime and internal/devtab and is not a client's business.
// Index is the N of a numbered tab and 0 for the agent and review panes.
type PaneInfo struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Index int    `json:"index,omitempty"`
}

// PanesData is Response.Data for cmd=panes: which panes EXIST for a session,
// in the order a tab strip should draw them.
//
// It is derived from tmux on every call rather than read from the session
// record, for the same reason DevActive and DevURLs are: Session.DevTabs is a
// cache the observer overwrites, shell tabs are recorded nowhere at all, and a
// strip drawn from a stale cache offers tabs that are gone while hiding ones
// that are there.
//
// CanCreateShell is answered here rather than left for the client to infer from
// a count whose cap it would have to know.
type PanesData struct {
	Session        string     `json:"session"`
	Panes          []PaneInfo `json:"panes"`
	Review         PaneInfo   `json:"review,omitempty"`
	CanCreateShell bool       `json:"canCreateShell"`
}

// ShellCreateData is Response.Data for cmd=shellCreate: the pane that was
// started, which the caller subscribes to like any other.
//
// The INDEX is allocated by the daemon, not asked for by the caller. The desktop
// lets its own frontend own the name because both run in one process on one
// machine; two phones and a desktop racing for "-shell-2" do not have that
// luxury, and only the daemon sees all of them.
type ShellCreateData struct {
	Session string `json:"session"`
	Pane    string `json:"pane"`
	Index   int    `json:"index"`
}

// PairBeginData is Response.Data for cmd=pairBegin: how to reach this daemon's
// phone listener, as facts rather than as a picture.
//
// Two shapes of the same thing, deliberately. Code is the opaque token a client
// renders as a QR and the mobile app scans; the loose fields are the same
// values as text, so a scan is a convenience and never the only way in — a
// camera that will not focus, a phone with the permission denied, or a
// Simulator with no camera at all must still be able to connect. Rendering is
// the CLIENT's job (the desktop draws an SVG, `lola pair` prints half-blocks),
// so the daemon ships a string and takes no QR dependency.
//
// EVERY field of this is a secret while Key is set, and the token especially:
// it CONTAINS the key. It is never logged by the daemon, never placed in an
// error, and a client must treat it the way it treats a password — revealed on
// an explicit action, not left on a screen.
type PairBeginData struct {
	// Code is the scannable token (internal/remote.EncodeConnectCode). Empty
	// when the daemon cannot answer, in which case Problem says why.
	Code string `json:"code,omitempty"`

	// Hosts is every address the listener actually bound, in bind order, and
	// Port the port it took. Under -tags lola_insecure the bind is forced to
	// loopback, so this is 127.0.0.1 and ::1 and a phone reaches them through a
	// forward — a LAN address must never be printed here, because it is one the
	// daemon cannot deliver.
	Hosts []string `json:"hosts,omitempty"`
	Port  int      `json:"port,omitempty"`

	// Pin is the listener's SPKI pin: standard base64 with padding, byte for
	// byte the value the startup log line carries.
	Pin string `json:"pin,omitempty"`

	// Key is M1's shared bearer key. Empty in any build without the
	// lola_insecure path, where Problem names that as the reason.
	Key string `json:"key,omitempty"`

	// Insecure records that this code carries a shared bearer key with no
	// device identity and no cryptography, so a client can say so beside it
	// instead of a reader having to know which build produced it.
	Insecure bool `json:"insecure,omitempty"`

	// Problem names why there is no code, in one human sentence, when the
	// daemon can answer the request but not fill it: the listener is not
	// running, or this build has no way to authenticate a phone. It is a
	// RENDERED STATE rather than an error so a client shows the reason in place
	// of the code instead of a failed call with nothing to act on.
	Problem string `json:"problem,omitempty"`
}
