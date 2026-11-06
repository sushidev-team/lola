package protocol

import "time"

// Agent-assisted SSH: the wire contract. docs/ssh-sessions.md is the spec; this
// file is what the daemon, the CLI, the TUI and the app compile against.
//
// Two families of command, and the split is the trust boundary:
//
//   - sshAgent* come from an agent's `lola ssh …`. They always act as
//     Request.Session and can only reach that session's connections and blocks.
//     They are the only ssh commands the sandbox hook bridge admits, and a request
//     over the bridge also carries Request.HookToken.
//   - every other ssh* is a HUMAN's: going, approving, taking the wheel, reading
//     the audit trail. The bridge refuses them, so a sandboxed agent cannot reach
//     them. An UNSANDBOXED agent runs as the user and can reach the unix socket,
//     so for it the gate stops accidents and well-behaved agents, not a determined
//     one. docs/ssh-sessions.md says so and the UIs must not imply otherwise.
//
// Every ssh command is refused over internal/remote for now.
//
// FACTS and CLAIMS are distinct on this wire. In SSHBlock, Command, Cwd, Status,
// ExitCode, timings, Actor and Approval are facts the daemon recorded, and Why is
// a claim. In SSHConnInfo, Why and Scope are claims. All of SSHElement is a claim
// except its Evidence facts and its Outcome, which came from a human. A UI must
// render the two so a human can tell them apart, as text and never as markup.

// Agent-side commands. These alone may cross the sandbox hook bridge.
const (
	CmdSSHAgentHosts   = "sshAgentHosts"   // SSHAgentHostsArgs → SSHAgentHostsData
	CmdSSHAgentRequest = "sshAgentRequest" // SSHAgentRequestArgs → SSHAgentRequestData
	CmdSSHAgentRun     = "sshAgentRun"     // SSHAgentRunArgs → SSHAgentRunData
	CmdSSHAgentPoll    = "sshAgentPoll"    // SSHAgentPollArgs → SSHAgentPollData
	CmdSSHAgentCancel  = "sshAgentCancel"  // SSHAgentCancelArgs → SSHBlock
	CmdSSHAgentPost    = "sshAgentPost"    // SSHAgentPostArgs → SSHAgentPostData
)

// Human-side commands. Never on the bridge, never remote.
const (
	CmdSSHHosts           = "sshHosts"           // no args → SSHHostsData
	CmdSSHOpen            = "sshOpen"            // SSHOpenArgs → SSHOpenData
	CmdSSHConnect         = "sshConnect"         // SSHConnectArgs → SSHConnectData
	CmdSSHHumanRun        = "sshHumanRun"        // SSHHumanRunArgs → SSHBlock
	CmdSSHShareBlock      = "sshShareBlock"      // SSHShareBlockArgs → SSHBlock
	CmdSSHTerminalCd      = "sshTerminalCd"      // SSHConnArgs → SSHConnInfo
	CmdSSHGo              = "sshGo"              // SSHGoArgs → SSHConnInfo
	CmdSSHDeny            = "sshDeny"            // SSHDenyArgs → SSHConnInfo
	CmdSSHApprove         = "sshApprove"         // SSHApproveArgs → SSHBlock
	CmdSSHRevokeGrant     = "sshRevokeGrant"     // SSHConnArgs → SSHConnInfo
	CmdSSHTakeWheel       = "sshTakeWheel"       // SSHTakeWheelArgs → SSHTakeWheelData
	CmdSSHHandbackPreview = "sshHandbackPreview" // SSHConnArgs → SSHHandbackPreviewData
	CmdSSHHandback        = "sshHandback"        // SSHHandbackArgs → SSHHandbackData
	CmdSSHStop            = "sshStop"            // SSHStopArgs → SSHStopData
	CmdSSHClose           = "sshClose"           // SSHConnArgs → SSHCloseData
	CmdSSHTrash           = "sshTrash"           // SSHConnArgs → SSHCloseData
	CmdSSHTerminal        = "sshTerminal"        // SSHConnArgs → SSHConnInfo
	CmdSSHTimeline        = "sshTimeline"        // SSHTimelineArgs → SSHTimelineData
	CmdSSHOutput          = "sshOutput"          // SSHOutputArgs → SSHOutputData
	CmdSSHSay             = "sshSay"             // SSHSayArgs → SSHSayData
	CmdSSHAnswer          = "sshAnswer"          // SSHAnswerArgs → SSHSayData
	CmdSSHProposal        = "sshProposal"        // SSHProposalArgs → SSHProposalData
	CmdSSHRecall          = "sshRecall"          // SSHRecallArgs → SSHRecallData
	CmdSSHResolve         = "sshResolve"         // SSHResolveArgs → SSHResolveData
	CmdSSHWidgetStatus    = "sshWidgetStatus"    // SSHWidgetStatusArgs → SSHWidgetStatusData
	CmdSSHPromptAnswer    = "sshPromptAnswer"    // SSHPromptAnswerArgs → SSHPromptAnswerData. Carries a secret.
)

// SSHAgentCommands are the commands the sandbox hook bridge may admit.
var SSHAgentCommands = []string{
	CmdSSHAgentHosts, CmdSSHAgentRequest, CmdSSHAgentRun, CmdSSHAgentPoll, CmdSSHAgentCancel, CmdSSHAgentPost,
}

// SSHHumanCommands must never cross the bridge.
var SSHHumanCommands = []string{
	CmdSSHHosts, CmdSSHOpen, CmdSSHConnect, CmdSSHHumanRun, CmdSSHShareBlock, CmdSSHTerminalCd, CmdSSHGo, CmdSSHDeny, CmdSSHApprove, CmdSSHRevokeGrant,
	CmdSSHTakeWheel, CmdSSHHandbackPreview, CmdSSHHandback, CmdSSHStop, CmdSSHClose, CmdSSHTrash,
	CmdSSHTerminal, CmdSSHTimeline, CmdSSHOutput, CmdSSHSay, CmdSSHAnswer, CmdSSHProposal,
	CmdSSHRecall, CmdSSHResolve, CmdSSHWidgetStatus, CmdSSHPromptAnswer,
	CmdAgentFeed, CmdAgentFeedOutput, CmdTalkState, CmdAgentSay, CmdAgentStop,
	CmdAgentChoose, CmdAgentQueueRelease, CmdEndedSessions,
}

// Tiers. An empty or unknown tier reads as production: a host nobody classified
// gets the strictest handling.
const (
	SSHTierProduction = "production"
	SSHTierStaging    = "staging"
	SSHTierDev        = "dev"
)

// Connection states. See the state machine in docs/ssh-sessions.md.
const (
	SSHConnRequested      = "requested"      // the agent asked; nothing has connected; waiting for a human's go
	SSHConnDenied         = "denied"         // the human said no. Terminal.
	SSHConnConnecting     = "connecting"     // the tab is up, ssh is running, no master yet
	SSHConnAuthenticating = "authenticating" // still no master after a few seconds: look at the terminal
	SSHConnConnected      = "connected"      // ssh -O check answers
	SSHConnDisconnected   = "disconnected"   // the master went away; a human can reconnect
	SSHConnFailed         = "failed"         // ssh exited before a master came up, or the wait ran out
	SSHConnClosed         = "closed"         // a human or teardown ended it. Terminal.
)

// Wheel states: who may act on a connection. The terminal tab accepts a human's
// keys in both states; the wheel decides whether `lola ssh run` is accepted.
const (
	SSHWheelAgent = "agent"
	SSHWheelHuman = "human"
)

// Entry says how a connection began.
const (
	SSHEntryRequest = "request" // from a running session, lola ssh request
	SSHEntryDirect  = "direct"  // a human started an agent-assisted session
	SSHEntryHuman   = "human"   // a human connected a session that already existed
)

// Block states. Terminal: denied, expired, refused, succeeded, failed, timed_out,
// stopped, lost.
const (
	SSHBlockPendingApproval = "pending_approval"
	SSHBlockQueued          = "queued"
	SSHBlockRunning         = "running"
	SSHBlockDenied          = "denied"    // a human said no
	SSHBlockExpired         = "expired"   // nobody answered within approval_timeout_seconds
	SSHBlockRefused         = "refused"   // never ran: the human had the wheel, or the connection dropped first
	SSHBlockSucceeded       = "succeeded" // ran, exit 0
	SSHBlockFailed          = "failed"    // ran, non-zero exit
	SSHBlockTimedOut        = "timed_out" // ran past its timeout; local ssh killed
	SSHBlockStopped         = "stopped"   // a human or the agent stopped it; local ssh killed
	SSHBlockLost            = "lost"      // the transport died or the daemon restarted mid-run; the result is unknown
)

// SSHOriginTerminal marks a block from the human's long-running shell.
const SSHOriginTerminal = "terminal"

// Actors: who asked for a block.
const (
	SSHActorAgent = "agent"
	SSHActorHuman = "human" // a human ran a command, or an agent's proposal
)

// Approval modes: why a block was allowed to run.
const (
	SSHApprovalPerCommand = "per_command" // a human approved exactly this command
	SSHApprovalGrant      = "grant"       // covered by a time-limited grant
	SSHApprovalHuman      = "human"       // a human ran it themselves
)

// Decisions for SSHApproveArgs.
const (
	SSHDecisionApprove = "approve"
	SSHDecisionDeny    = "deny"
)

// Actions for SSHProposalArgs.
const (
	SSHProposalRun     = "run"
	SSHProposalDecline = "decline"
)

// Streams for SSHOutputArgs.
const (
	SSHStreamStdout = "stdout"
	SSHStreamStderr = "stderr"
)

// Timeline item kinds.
const (
	SSHItemBlock   = "block"
	SSHItemElement = "element"
	SSHItemSystem  = "system"
	SSHItemHuman   = "human" // something the human said to the agent
)

// Human message origins: what a human timeline item came from.
const (
	SSHHumanSay      = "say"      // typed in the composer
	SSHHumanAnswered = "answer"   // answered a question element
	SSHHumanHandback = "handback" // said while handing the wheel back
	SSHHumanInitial  = "initial"  // the brief the session started with
	SSHHumanChoice   = "choice"   // a key a human pressed on a card: "You chose: 1 Yes"
)

// Delivery paths of a human message. Both are lola putting text in front of the
// agent, and neither proves the agent read or heeded it, so a UI says "Delivered".
const (
	SSHViaReply  = "reply"  // printed by the agent's next lola ssh command
	SSHViaPrompt = "prompt" // typed at its resting prompt through the send gate
)

// System event kinds: facts the daemon records about the connection.
const (
	SSHEventRequested    = "requested"
	SSHEventGo           = "go"
	SSHEventDenied       = "denied"
	SSHEventConnected    = "connected"
	SSHEventDisconnected = "disconnected"
	SSHEventFailed       = "failed"
	SSHEventWheelTaken   = "wheel_taken"
	SSHEventHandedBack   = "handed_back"
	SSHEventGrantCreated = "grant_created"
	SSHEventGrantEnded   = "grant_ended"
	SSHEventClosed       = "closed"
)

// System events about the session rather than a connection. All Text is lola's own
// sentence, and Conn is always "".
const (
	SSHEventSessionStarted = "session_started"
	SSHEventSessionEnded   = "session_ended"
	SSHEventAgentExited    = "agent_exited"
	SSHEventStopped        = "stopped"
	SSHEventCheckpoint     = "checkpoint" // a turn's receipt
	SSHEventPR             = "pr"         // opened, merged, closed
	SSHEventCI             = "ci"         // a required check turned red or green
	SSHEventReview         = "review"     // a tally only, never findings
	SSHEventHandoff        = "handoff"    // lola typed something. Says which and why.
)

// Refusal codes for a command that never ran. Stable: the CLI maps them to exit
// codes and an agent's briefing names them.
const (
	SSHRefuseUnknownHost   = "unknown_host"
	SSHRefuseUnsandboxed   = "unsandboxed_production"
	SSHRefuseNoConnection  = "no_connection"
	SSHRefuseAwaitingGo    = "awaiting_go"
	SSHRefuseNotConnected  = "not_connected"
	SSHRefuseConnDenied    = "connection_denied"
	SSHRefuseHumanControl  = "human_has_control"
	SSHRefuseBadCommand    = "bad_command"
	SSHRefuseRateLimited   = "rate_limited"
	SSHRefuseTooManyQueued = "too_many_queued"
	// SSHRefuseStorage: the record of this command could not be written. An
	// unrecorded command breaks the audit guarantee, so it does not run.
	SSHRefuseStorage = "storage_unavailable"
)

// Exit codes of `lola ssh run` for outcomes that are lola's, not the remote
// command's. A remote exit code passes through as itself, including 255, which
// is ssh's own transport failure. stderr always carries a `lola ssh:` line naming
// the reason, so a remote command that happens to exit 240 cannot be mistaken.
const (
	SSHExitDenied        = 240 // a human denied the command or the connection
	SSHExitHumanControl  = 241 // the human has the wheel
	SSHExitNotConnected  = 242 // no live connection; request one first
	SSHExitStillWaiting  = 243 // still waiting for a human; the block lives on, use lola ssh wait
	SSHExitTimedOut      = 244 // the command ran past its timeout
	SSHExitStopped       = 245 // the command was stopped
	SSHExitPolicyRefused = 246 // refused by policy, or a bad or rate-limited request
	SSHExitLost          = 247 // the result is unknown: the connection or the daemon went away
	SSHExitDaemon        = 248 // the daemon could not be reached
)

// Words every surface must say the same way.
const (
	// SSHDataWarning belongs at every go and approval moment.
	SSHDataWarning = "Everything the agent reads here is sent to its model provider."
	// SSHRemoteOutlives belongs next to every stop and close.
	SSHRemoteOutlives = "Stopping ends the local ssh. The remote process may keep running."
	// SSHUserIsTheLimit belongs wherever a reader might expect command filtering.
	SSHUserIsTheLimit = "Lola does not filter commands. The remote user's permissions are the real limit."
	// SSHUnsandboxedNote belongs on an unsandboxed session's approval and in docs.
	SSHUnsandboxedNote = "This agent runs unsandboxed as you. The approval gate stops accidents and well-behaved agents, not a determined one."
	// SSHDrivingNote belongs wherever a human drives or a driving interval is shown.
	SSHDrivingNote = "Your commands appear in the timeline, visible only to you until you share them."
	// SSHTranscriptWarning belongs next to every handback transcript.
	SSHTranscriptWarning = "This text goes to the agent and its model provider. Remove anything you would not paste there."
)

// --- Hosts -----------------------------------------------------------------

// SSHHostInfo is one [[ssh_host]] resolved for display. The Direct* fields answer
// "what would starting an agent-assisted session here do".
type SSHHostInfo struct {
	Name             string `json:"name"`
	Label            string `json:"label,omitempty"`
	Alias            string `json:"alias"`
	User             string `json:"user,omitempty"`
	Tier             string `json:"tier"`
	AllowUnsandboxed bool   `json:"allowUnsandboxed,omitempty"`
	GrantMaxMinutes  int    `json:"grantMaxMinutes"` // 0: per-command approval only
	// DirectSandbox is how the agent of a direct session would run: docker or none.
	DirectSandbox string `json:"directSandbox,omitempty"`
	// DirectAgent is the coding agent a direct session would run by default.
	DirectAgent string `json:"directAgent,omitempty"`
	// DirectRefused is why a direct session cannot start, "" when it can. It also
	// names a host that failed validation.
	DirectRefused string `json:"directRefused,omitempty"`
}

// SSHHostsData is the reply to cmd=sshHosts.
type SSHHostsData struct {
	Hosts []SSHHostInfo `json:"hosts"`
}

// SSHAgentHostsArgs is the request for cmd=sshAgentHosts. Host narrows to one.
type SSHAgentHostsArgs struct {
	Host string `json:"host,omitempty"`
}

// SSHAgentHost is one host as an agent sees it, with this session's standing.
type SSHAgentHost struct {
	Name    string `json:"name"`
	Tier    string `json:"tier"`
	User    string `json:"user,omitempty"`
	Allowed bool   `json:"allowed"`           // false: Refused says why
	Refused string `json:"refused,omitempty"` // e.g. production refuses an unsandboxed session
	Conn    string `json:"conn,omitempty"`
	State   string `json:"state,omitempty"` // connection state, "" when none
	Wheel   string `json:"wheel,omitempty"`
	// Reason is the human's deny reason, when State is denied.
	Reason string `json:"reason,omitempty"`
}

// SSHAgentHostsData is the reply to cmd=sshAgentHosts.
type SSHAgentHostsData struct {
	Hosts []SSHAgentHost `json:"hosts"`
	Inbox []string       `json:"inbox,omitempty"` // see SSHAgentRunData.Inbox
}

// --- Connections -----------------------------------------------------------

// SSHGrantInfo is a live time-limited grant on a connection.
type SSHGrantInfo struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt,omitzero"`
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
}

// SSHConnInfo is one connection.
type SSHConnInfo struct {
	// Cwd is where the next command starts, shared by the agent and the human. TermCwd
	// is where the human's shell is, known only while TermTracked. TermAtPrompt says
	// that shell is at a prompt, so a command can be sent into it. TermElevated is the
	// command that opened a root shell still running there.
	Cwd          string `json:"cwd,omitempty"`
	TermCwd      string `json:"termCwd,omitempty"`
	TermTracked  bool   `json:"termTracked,omitempty"`
	TermAtPrompt bool   `json:"termAtPrompt,omitempty"`
	TermElevated string `json:"termElevated,omitempty"`

	ID      string `json:"id"`
	Session string `json:"session"`
	Host    string `json:"host"`
	Label   string `json:"label,omitempty"`
	Alias   string `json:"alias"`
	Tier    string `json:"tier"`
	// User is the configured restricted user, "" when the ssh config decides.
	User string `json:"user,omitempty"`
	// ResolvedUser and ResolvedHostname come from `ssh -G <alias>`: what the
	// local ssh config says this alias means. "" when unknown. Shown at approval.
	ResolvedUser     string `json:"resolvedUser,omitempty"`
	ResolvedHostname string `json:"resolvedHostname,omitempty"`

	Entry     string `json:"entry"` // request | direct
	Sandboxed bool   `json:"sandboxed,omitempty"`

	State       string    `json:"state"`
	StateDetail string    `json:"stateDetail,omitempty"` // lola's own sentence on the state
	Wheel       string    `json:"wheel"`
	WheelSince  time.Time `json:"wheelSince,omitzero"`
	// Tab is the tmux session of the live terminal ("<session>-ssh-N"), and
	// TabAlive whether its pane process runs. A dead tab with State connected
	// means the human left the shell and the master stays up: offer sshTerminal.
	Tab      string `json:"tab,omitempty"`
	TabAlive bool   `json:"tabAlive,omitempty"`

	Why   string `json:"why,omitempty"`   // CLAIM: the agent's reason for asking
	Scope string `json:"scope,omitempty"` // CLAIM: what the agent says it will do

	RequestedAt time.Time `json:"requestedAt,omitzero"`
	ConnectedAt time.Time `json:"connectedAt,omitzero"`
	ClosedAt    time.Time `json:"closedAt,omitzero"`

	// ConnectDeadline is when a connection still coming up gives up, so a human in
	// the middle of MFA can see the time left. Zero outside connecting/authenticating.
	ConnectDeadline time.Time `json:"connectDeadline,omitzero"`
	// IdleLingerSeconds is control_persist_seconds: how long the master stays up
	// with no clients once the human has left the shell.
	IdleLingerSeconds int `json:"idleLingerSeconds,omitempty"`

	Grant *SSHGrantInfo `json:"grant,omitempty"`
	// GrantMaxMinutes is the longest grant a human may open here, 0 for none. It
	// is on the connection so an approval card needs no second fetch.
	GrantMaxMinutes int `json:"grantMaxMinutes,omitempty"`

	PendingApprovals int `json:"pendingApprovals,omitempty"`
	RunningBlocks    int `json:"runningBlocks,omitempty"`
	// CommandsRun counts blocks that started, so closing can say what may survive.
	CommandsRun int `json:"commandsRun,omitempty"`

	// Prompt is a question the human's shell tab is waiting on, and TermOpenLine says a
	// running command's output stopped mid-line without one lola recognized. Both are
	// filled from the daemon's memory on the human's reads. They are never written to the
	// store, and no agent-side reply carries a connection.
	Prompt       *SSHPromptInfo `json:"prompt,omitempty"`
	TermOpenLine bool           `json:"termOpenLine,omitempty"`
	// AgentRan says the agent has run a command on this connection, so a job of its may
	// still be on the host, able to ask a question on the human's terminal.
	AgentRan bool `json:"agentRan,omitempty"`
}

// Prompt stages and kinds.
const (
	SSHPromptSignIn  = "signin"  // ssh itself, while the connection comes up
	SSHPromptCommand = "command" // a program running in the human's shell

	SSHPromptSecret  = "secret"  // a masked field
	SSHPromptConfirm = "confirm" // yes or no

	SSHPromptSend   = "send"
	SSHPromptYes    = "yes"
	SSHPromptNo     = "no"
	SSHPromptCancel = "cancel"

	SSHPromptSent      = "sent"
	SSHPromptCancelled = "cancelled"

	// SSHMaxPromptSecretBytes bounds one answer.
	SSHMaxPromptSecretBytes = 4096
)

// SSHPromptInfo is what a prompt card shows. Line and Context are text a program printed.
// A remote program can fake them, so a card shows them as they are and says who asks.
type SSHPromptInfo struct {
	ID      string   `json:"id"`
	Stage   string   `json:"stage"`
	Kind    string   `json:"kind"`
	Line    string   `json:"line"`
	Context []string `json:"context,omitempty"`
	// Block and Command name the running command, in the command stage.
	Block   string `json:"block,omitempty"`
	Command string `json:"command,omitempty"`
	// Attempt counts the answers this same question already got and asked again for.
	Attempt int       `json:"attempt,omitempty"`
	Since   time.Time `json:"since,omitzero"`
}

// SSHPromptAnswerArgs answers the prompt named by Prompt. Secret is on the wire to the
// daemon and goes no further than the pane: it is never logged, stored or returned.
type SSHPromptAnswerArgs struct {
	Conn   string `json:"conn"`
	Prompt string `json:"prompt"`
	Action string `json:"action"` // send | yes | no | cancel
	Secret string `json:"secret,omitempty"`
}

// SSHPromptAnswerData is the reply. It never carries the answer.
type SSHPromptAnswerData struct {
	Status string `json:"status"` // sent | cancelled
}

// SSHSessionInfo is the glance form that rides on SessionInfo.SSH. It is built
// from memory with no exec, like the rest of cmd=sessions.
type SSHSessionInfo struct {
	Connections []SSHConnInfo `json:"connections"`
	// NeedsHuman is true only for daemon FACTS: a connection waiting for a go, one
	// waiting on authentication, a command waiting for approval. Never set by
	// anything an agent posted. It does not touch the agent or delivery axes; the
	// clients OR it into their attention view.
	NeedsHuman bool `json:"needsHuman,omitempty"`
	// NeedsHumanWhy is lola's own sentence for the most urgent of those. Most
	// urgent first: authentication (a clock is running), approval, go.
	NeedsHumanWhy string `json:"needsHumanWhy,omitempty"`
	// NextApproval is the oldest block waiting for approval, whole, so a row can
	// show the exact command on hover without fetching a timeline.
	NextApproval *SSHBlock `json:"nextApproval,omitempty"`
	// RunningCommand is the command of the newest running block, clipped to 120
	// runes for a row. A FACT, from memory like the rest of this struct.
	RunningCommand string `json:"runningCommand,omitempty"`
	// OpenItems counts open questions and undecided proposals. Display-only: it is
	// agent-controlled, so it never raises NeedsHuman.
	OpenItems int `json:"openItems,omitempty"`
	// Headline is the agent's latest summary. A CLAIM, shown as one.
	Headline string `json:"headline,omitempty"`
	// LastSeq is the session timeline's latest change: a client refetches the
	// timeline only when this moves.
	LastSeq int64 `json:"lastSeq"`
}

// SSHOpenArgs is the request for cmd=sshOpen: a human starts an agent-assisted
// session on a host. Starting it IS the go for connecting; command approval still
// applies. AgentKind "" uses the host's agent, then [defaults].agent.
//
// Brief is the human's opening words, sent as the agent's first instruction. With
// none the agent is told to wait for the human, so a session never starts doing
// something nobody asked for. Wheel "human" starts with the human driving, for
// someone who wants to look around before asking; "" means agent.
type SSHOpenArgs struct {
	Host      string `json:"host"`
	AgentKind string `json:"agentKind,omitempty"`
	Name      string `json:"name,omitempty"`
	Brief     string `json:"brief,omitempty"`
	Wheel     string `json:"wheel,omitempty"`
}

// SSHOpenData is the reply to cmd=sshOpen.
type SSHOpenData struct {
	Session string      `json:"session"`
	Conn    SSHConnInfo `json:"conn"`
	Sandbox string      `json:"sandbox,omitempty"` // docker, or "" for unsandboxed
	Warning string      `json:"warning,omitempty"` // SSHDataWarning, plus any unsandboxed note
}

// SSHConnectArgs is a human connecting an existing session to a host. The click is
// the go, so it needs no agent request. Wheel is agent (default) or human.
type SSHConnectArgs struct {
	Session string `json:"session"`
	Host    string `json:"host"`
	Wheel   string `json:"wheel,omitempty"`
}

// SSHConnectData is the reply to cmd=sshConnect.
type SSHConnectData struct {
	Conn    SSHConnInfo `json:"conn"`
	Warning string      `json:"warning,omitempty"` // SSHDataWarning, plus any unsandboxed note
}

// SSHHumanRunArgs is a human running a command on a connection from the timeline.
type SSHHumanRunArgs struct {
	Session string `json:"session"`
	Conn    string `json:"conn"`
	Command string `json:"command"`
	Cwd     string `json:"cwd,omitempty"`
}

// SSHShareBlockArgs lets the agent read a command the human ran.
type SSHShareBlockArgs struct {
	Session string `json:"session"`
	Block   string `json:"block"`
}

// SSHConnArgs names one connection.
type SSHConnArgs struct {
	Conn string `json:"conn"`
}

// SSHGoArgs is a human's go for a requested connection, or a reconnect of a
// disconnected or failed one. An agent can never reach this.
type SSHGoArgs struct {
	Conn string `json:"conn"`
}

// SSHDenyArgs is a human's no to a requested connection. Reason goes back to the
// agent through its next poll.
type SSHDenyArgs struct {
	Conn   string `json:"conn"`
	Reason string `json:"reason,omitempty"`
}

// SSHAgentRequestArgs is `lola ssh request <host> --why … [--scope …]`. Requesting
// again while a request is open returns the same connection.
type SSHAgentRequestArgs struct {
	Host  string `json:"host"`
	Why   string `json:"why"`
	Scope string `json:"scope,omitempty"`
}

// SSHAgentRequestData is the reply. Refusal is set when nothing was requested.
// The CLI polls cmd=sshAgentHosts to wait for the human's decision.
type SSHAgentRequestData struct {
	Conn    string      `json:"conn,omitempty"`
	State   string      `json:"state,omitempty"`
	Refusal *SSHRefusal `json:"refusal,omitempty"`
	Inbox   []string    `json:"inbox,omitempty"` // see SSHAgentRunData.Inbox
}

// SSHRefusal says why something did not happen. Message is lola's own sentence,
// written for the agent to read.
type SSHRefusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// --- Blocks ----------------------------------------------------------------

// SSHApprovalInfo records why a block was allowed or refused. Facts.
type SSHApprovalInfo struct {
	Mode    string    `json:"mode,omitempty"` // per_command | grant | human
	Granted bool      `json:"granted"`        // false: denied or still undecided
	At      time.Time `json:"at,omitzero"`
	GrantID string    `json:"grantId,omitempty"`
	Reason  string    `json:"reason,omitempty"` // the human's deny reason
}

// SSHBlock is one command. Output is NOT here: fetch it with cmd=sshOutput.
type SSHBlock struct {
	ID      string `json:"id"` // unique within the session, e.g. "b12"
	Conn    string `json:"conn"`
	Session string `json:"session"`
	Host    string `json:"host"`
	Tier    string `json:"tier"`
	User    string `json:"user,omitempty"` // the connection's resolved user

	Command string `json:"command"` // FACT: exact, and refused if undisplayable
	// Cwd is the EFFECTIVE directory, a FACT: --cwd, else the host's default_cwd.
	// Empty means the remote login directory, and a UI says so.
	Cwd   string `json:"cwd,omitempty"`
	Why   string `json:"why,omitempty"` // CLAIM
	Actor string `json:"actor"`         // agent | human

	Status   string          `json:"status"`
	Approval SSHApprovalInfo `json:"approval"`
	// StatusDetail is lola's own sentence, for example why a block was refused.
	StatusDetail string `json:"statusDetail,omitempty"`
	// RefusalCode is an SSHRefuse* code when Status is refused.
	RefusalCode string `json:"refusalCode,omitempty"`
	ExitCode    *int   `json:"exitCode,omitempty"` // nil until it exits, and when lost

	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
	// ApprovalExpiresAt is when a pending block expires unrun. Zero once decided.
	ApprovalExpiresAt time.Time `json:"approvalExpiresAt,omitzero"`
	CreatedAt         time.Time `json:"createdAt,omitzero"`
	StartedAt         time.Time `json:"startedAt,omitzero"`
	EndedAt           time.Time `json:"endedAt,omitzero"`
	DurationMs        int64     `json:"durationMs,omitempty"`

	// Stored output sizes, and how much was discarded past output_cap_kb.
	StdoutBytes  int64 `json:"stdoutBytes,omitempty"`
	StderrBytes  int64 `json:"stderrBytes,omitempty"`
	DroppedBytes int64 `json:"droppedBytes,omitempty"`
	// Preview is the last lines of the stream that tells the story (stderr when the
	// block failed and has any, else stdout), at most SSHMaxPreviewBytes of
	// sanitized text, so a collapsed row needs no output fetch. Untrusted output,
	// shown as text. Sizes and Preview refresh at most once a second while running.
	Preview       string `json:"preview,omitempty"`
	PreviewStream string `json:"previewStream,omitempty"` // stdout | stderr
	Binary        bool   `json:"binary,omitempty"`        // the output carried NUL bytes
	OutputPruned  bool   `json:"outputPruned,omitempty"`  // retention removed the stored output
	// ProposalElement is the element this block came from, when a human ran one.
	ProposalElement string `json:"proposalElement,omitempty"`
	// Origin says where a human's command came from: "terminal" is the long-running
	// shell, empty is an exec channel. A terminal block has stdout and stderr merged and
	// cannot be stopped from here.
	Origin string `json:"origin,omitempty"`
	// Elevated labels a command that visibly raises its rights (sudo, su, doas). It is
	// for a human to read and never a safety check.
	Elevated bool `json:"elevated,omitempty"`
	// Private is a command a human typed: the agent can neither read nor cite it until
	// the human shares it. What someone looks at while debugging is not automatically
	// sent to a model provider.
	Private bool `json:"private,omitempty"`
}

// SSHMaxPreviewBytes bounds SSHBlock.Preview.
const SSHMaxPreviewBytes = 512

// SSHAgentRunArgs is `lola ssh run <host> --why … [--cwd …] [--timeout …] -- cmd`.
// Command is one string, exactly as the agent wrote it. It returns at once: the
// CLI polls for the rest, so a long command never holds a bridge connection.
type SSHAgentRunArgs struct {
	Host           string `json:"host"`
	Why            string `json:"why"`
	Cwd            string `json:"cwd,omitempty"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"` // 0: the host's default; clamped to its maximum
}

// SSHAgentRunData is the reply. A refusal that the human should see also has a
// Block in the refused state.
//
// Inbox, on every agent-side reply, is what the human said since the last reply:
// lola's own framed text, printed by the CLI to stderr. It is how a human reaches
// an agent that is mid-turn without typing into its pane, which is never safe. An
// entry returned here is marked delivered and leaves the prompt queue.
type SSHAgentRunData struct {
	Block   *SSHBlock   `json:"block,omitempty"`
	Refusal *SSHRefusal `json:"refusal,omitempty"`
	Inbox   []string    `json:"inbox,omitempty"`
}

// SSHAgentPollArgs asks for a block's state and output past the given offsets.
// Offsets are byte positions in the stored, sanitized streams.
type SSHAgentPollArgs struct {
	Block        string `json:"block"`
	StdoutOffset int64  `json:"stdoutOffset,omitempty"`
	StderrOffset int64  `json:"stderrOffset,omitempty"`
}

// SSHMaxPollBytes bounds the output in one poll reply, per stream.
const SSHMaxPollBytes = 256 * 1024

// SSHAgentPollData is the reply. Done means Status is terminal and both offsets
// have reached the end, so the CLI can exit with SSHCLIExit(Block).
type SSHAgentPollData struct {
	Block        SSHBlock `json:"block"`
	Stdout       string   `json:"stdout,omitempty"`
	Stderr       string   `json:"stderr,omitempty"`
	StdoutOffset int64    `json:"stdoutOffset"`
	StderrOffset int64    `json:"stderrOffset"`
	Done         bool     `json:"done"`
	Inbox        []string `json:"inbox,omitempty"` // see SSHAgentRunData.Inbox
}

// SSHAgentCancelArgs lets an agent stop its own block, which is what a CLI does
// on SIGINT or SIGTERM only when it was started with --cancel-on-exit.
type SSHAgentCancelArgs struct {
	Block string `json:"block"`
}

// SSHApproveArgs is a human's decision on a pending block. GrantMinutes > 0 on an
// approve also opens a time-limited grant, refused on a host whose
// GrantMaxMinutes is 0 and clamped to it otherwise.
type SSHApproveArgs struct {
	Conn         string `json:"conn"`
	Block        string `json:"block"`
	Decision     string `json:"decision"` // approve | deny
	Reason       string `json:"reason,omitempty"`
	GrantMinutes int    `json:"grantMinutes,omitempty"`
}

// SSHStopArgs is a human stopping an in-flight or queued block.
type SSHStopArgs struct {
	Conn  string `json:"conn"`
	Block string `json:"block"`
}

// SSHStopData is the reply. Warning is SSHRemoteOutlives when a process had
// actually started.
type SSHStopData struct {
	Block   SSHBlock `json:"block"`
	Warning string   `json:"warning,omitempty"`
}

// --- Output ----------------------------------------------------------------

// SSHOutputArgs fetches stored output lazily. Limit 0 uses SSHMaxPollBytes.
type SSHOutputArgs struct {
	Session string `json:"session"`
	Block   string `json:"block"`
	Stream  string `json:"stream"` // stdout | stderr
	Offset  int64  `json:"offset,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

// SSHOutputData is the reply. Data is sanitized text, safe to render as text.
//
// Past the output cap the head is kept and the middle is dropped, so TotalBytes
// is what is stored and DroppedBytes what was discarded. Tail holds the stream's
// last bytes when anything was dropped, so the end of a long log is never lost.
type SSHOutputData struct {
	Data         string `json:"data"`
	Offset       int64  `json:"offset"`
	NextOffset   int64  `json:"nextOffset"`
	TotalBytes   int64  `json:"totalBytes"`
	EOF          bool   `json:"eof"`
	DroppedBytes int64  `json:"droppedBytes,omitempty"`
	Tail         string `json:"tail,omitempty"`
	Pruned       bool   `json:"pruned,omitempty"`
}

// --- Wheel -----------------------------------------------------------------

// SSHTakeWheelArgs is a human taking control. It takes effect at once. Commands
// awaiting approval are refused. In-flight commands keep running unless
// StopInFlight, and stay visible and stoppable.
type SSHTakeWheelArgs struct {
	Conn         string `json:"conn"`
	StopInFlight bool   `json:"stopInFlight,omitempty"`
	// Interrupt also stops the agent's current turn, as Esc in its pane would.
	Interrupt bool `json:"interrupt,omitempty"`
}

// SSHTakeWheelData is the reply.
type SSHTakeWheelData struct {
	Conn     SSHConnInfo `json:"conn"`
	InFlight []SSHBlock  `json:"inFlight,omitempty"`
	Warning  string      `json:"warning,omitempty"`
}

// SSHHandbackPreviewData is what a handback could send: the pane's text since the
// wheel was taken, sanitized and capped. It is shown to the human for reading and
// editing, because they may have to take a secret back out of it.
type SSHHandbackPreviewData struct {
	Transcript string `json:"transcript"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// SSHMaxHandbackBytes caps a handback transcript. The text is typed into the
// agent's pane, so it must be small.
const SSHMaxHandbackBytes = 8 * 1024

// SSHMaxSayBytes caps one thing a human says to the agent. It goes by paste, not keystrokes, so a
// long log fits. The bound is the socket: one request line is at most 1 MiB, and JSON may double a
// text full of quotes and line breaks.
const SSHMaxSayBytes = 128 * 1024

// SSHHandbackArgs returns the wheel. Note is what the human wants to say.
// Transcript is sent ONLY when non-empty, and it is the text the human saw in the
// preview and may have edited, never a fresh capture. Both go through sanitize +
// the typeToAgent gate, framed to the agent as untrusted content.
type SSHHandbackArgs struct {
	Conn       string `json:"conn"`
	Note       string `json:"note,omitempty"`
	Transcript string `json:"transcript,omitempty"`
}

// SSHHandbackData is the reply. Delivered is false when the text is queued for
// the agent's next resting prompt.
type SSHHandbackData struct {
	Conn      SSHConnInfo `json:"conn"`
	Delivered bool        `json:"delivered"`
}

// SSHCloseData is the reply to cmd=sshClose. Warnings always name what may
// survive, starting with SSHRemoteOutlives when anything ran.
type SSHCloseData struct {
	Conn     SSHConnInfo `json:"conn"`
	Warnings []string    `json:"warnings,omitempty"`
}

// --- Talking to the agent ---------------------------------------------------

// SSHSayArgs is the human talking to the agent from the SSH view. Like diff
// feedback it DEFERS: queued, then typed at the next resting prompt.
type SSHSayArgs struct {
	Session string `json:"session"`
	// Conn is the tab the words were typed in. The agent's next commands and posts go there.
	Conn string `json:"conn,omitempty"`
	Text string `json:"text"`
}

// SSHSayData is the reply to cmd=sshSay and cmd=sshAnswer. Item is the timeline
// item the words became, so the sender can recall them while they are queued.
type SSHSayData struct {
	Item      string `json:"item,omitempty"`
	Delivered bool   `json:"delivered"`
	// AgentGone says the session's agent is not running, so the words stay queued
	// until it comes back. A UI must say so rather than imply they are on the way.
	AgentGone bool `json:"agentGone,omitempty"`
}

// SSHRecallArgs takes back a human message that has not been delivered.
type SSHRecallArgs struct {
	Session string `json:"session"`
	Item    string `json:"item"`
}

// SSHRecallData is the reply. Recalled is false when delivery won the race: the
// agent already has the words, and a UI must say so rather than imply otherwise.
type SSHRecallData struct {
	Recalled bool `json:"recalled"`
}

// SSHResolveArgs asks what an alias means to the local ssh config: `ssh -G`, which
// never connects. Used by host forms before a host is saved.
type SSHResolveArgs struct {
	Port         int    `json:"port,omitempty"`
	IdentityFile string `json:"identityFile,omitempty"`
	Alias        string `json:"alias"`
	User         string `json:"user,omitempty"`
}

// SSHResolveData is the reply. Error is lola's own sentence when ssh could not
// answer; every other field is then empty.
type SSHResolveData struct {
	User      string `json:"user,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
	Port      string `json:"port,omitempty"`
	ProxyJump string `json:"proxyJump,omitempty"`
	Error     string `json:"error,omitempty"`
}

// SSHAnswerArgs is a human answering a question element. Revision is the revision
// the human saw. A mismatch is refused, because the agent can reword an open
// question through --ref and an answer must not land on words nobody read.
type SSHAnswerArgs struct {
	Session  string `json:"session"`
	Element  string `json:"element"`
	Revision int    `json:"revision"`
	Text     string `json:"text"`
}

// What can go wrong with a widget, as the app reports it.
const (
	SSHWidgetScript    = "script"    // the page threw
	SSHWidgetBlocked   = "blocked"   // the page reached for something the sandbox forbids
	SSHWidgetStuck     = "stuck"     // the page never said it finished
	SSHWidgetEmpty     = "empty"     // the page finished and drew nothing
	SSHWidgetNavigated = "navigated" // the page tried to leave, so lola closed it
)

// SSHMaxWidgetError caps the page's own words on their way to the agent.
const SSHMaxWidgetError = 300

// SSHWidgetStatusArgs is the app saying a running widget failed. Message is the page's own
// text, and the page is agent code that may be repeating remote output. So it is untrusted,
// and it goes into one framed line for the agent and nowhere else. Revision is the revision
// on screen: a report on a page that has since been replaced is dropped.
type SSHWidgetStatusArgs struct {
	Session  string `json:"session"`
	Element  string `json:"element"`
	Revision int    `json:"revision"`
	Kind     string `json:"kind"`
	Message  string `json:"message,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// SSHWidgetStatusData says whether the agent has been told about this revision, now or by an
// earlier report.
type SSHWidgetStatusData struct {
	Told bool `json:"told,omitempty"`
}

// SSHProposalArgs is a human acting on a proposal element. Run makes a block with
// actor human and approval mode human, running the element's command exactly.
//
// Revision is the revision the human saw, and it is REQUIRED for run. The agent can
// rewrite an undecided proposal's command through --ref, so without this pin the
// command that runs need not be the one on screen when the human clicked. A
// mismatch is an error and runs nothing.
type SSHProposalArgs struct {
	Session  string `json:"session"`
	Element  string `json:"element"`
	Revision int    `json:"revision"`
	Action   string `json:"action"` // run | decline
	Reason   string `json:"reason,omitempty"`
}

// SSHProposalData is the reply. Block is set when Action was run.
type SSHProposalData struct {
	Element SSHElement `json:"element"`
	Block   *SSHBlock  `json:"block,omitempty"`
}

// --- Elements --------------------------------------------------------------

// SSHElementKinds, SSHSeverities and SSHStepStates mirror internal/sshelement,
// pinned by its test. A UI draws exactly these and renders anything else as a
// plain note.
var (
	SSHElementKinds = []string{"summary", "narration", "hypothesis", "finding", "plan", "metric", "evidence", "recommendation", "proposal", "question", "widget"}
	SSHSeverities   = []string{"info", "low", "medium", "high", "critical"}
	SSHStepStates   = []string{"pending", "active", "done", "skipped", "failed"}
)

// SSHAgentPostArgs is `lola ssh post …`: the argv, parsed DAEMON-side by
// internal/sshelement so the trust boundary is the daemon.
type SSHAgentPostArgs struct {
	Argv []string `json:"argv"`
}

// SSHAgentPostData is the reply: the element's id, and its revision so an agent
// can see an update landed.
type SSHAgentPostData struct {
	ID       string   `json:"id"`
	Revision int      `json:"revision"`
	Inbox    []string `json:"inbox,omitempty"` // see SSHAgentRunData.Inbox
}

// SSHElementStep is one plan step.
type SSHElementStep struct {
	Text  string `json:"text"`
	State string `json:"state"`
}

// SSHElementMetric is one key-value.
type SSHElementMetric struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Unit  string `json:"unit,omitempty"`
}

// SSHElementEvidence links to a block. Verified means the daemon found this block
// in this session and these are its recorded facts. It does NOT mean the agent's
// claim is true. Status and ExitCode were copied at post time: a UI should prefer
// the live block when it has it.
type SSHElementEvidence struct {
	Block    string `json:"block"`
	Verified bool   `json:"verified"`
	Status   string `json:"status,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
}

// SSHElementOutcome is a HUMAN's answer or decision.
type SSHElementOutcome struct {
	Text  string    `json:"text,omitempty"`
	Block string    `json:"block,omitempty"`
	At    time.Time `json:"at,omitzero"`
}

// SSHElement is one agent-published card. A CLAIM, apart from Evidence's facts
// and Outcome. Every string was sanitized daemon-side: render as text only, and
// send any URL in it through cmd=openURL.
type SSHElement struct {
	ID        string               `json:"id"`
	Ref       string               `json:"ref,omitempty"`
	Kind      string               `json:"kind"`
	Conn      string               `json:"conn,omitempty"`
	Title     string               `json:"title,omitempty"`
	Body      string               `json:"body,omitempty"`
	State     string               `json:"state,omitempty"`
	Severity  string               `json:"severity,omitempty"`
	Steps     []SSHElementStep     `json:"steps,omitempty"`
	Metrics   []SSHElementMetric   `json:"metrics,omitempty"`
	Evidence  []SSHElementEvidence `json:"evidence,omitempty"`
	Command   string               `json:"command,omitempty"`
	Cwd       string               `json:"cwd,omitempty"`
	Choices   []string             `json:"choices,omitempty"`
	HTML      string               `json:"html,omitempty"`
	Data      string               `json:"data,omitempty"`
	Height    int                  `json:"height,omitempty"`
	Outcome   *SSHElementOutcome   `json:"outcome,omitempty"`
	Revision  int                  `json:"revision"`
	CreatedAt time.Time            `json:"createdAt,omitzero"`
	UpdatedAt time.Time            `json:"updatedAt,omitzero"`
}

// --- Timeline --------------------------------------------------------------

// SSHSystemEvent is a fact the daemon recorded about a connection.
type SSHSystemEvent struct {
	Kind          string `json:"kind"`
	Text          string `json:"text"`           // lola's own sentence
	Note          string `json:"note,omitempty"` // a human's reason, as given
	Conn          string `json:"conn,omitempty"`
	HasTranscript bool   `json:"hasTranscript,omitempty"` // handed_back only: a transcript was shared
	Seq           int    `json:"seq,omitempty"`           // checkpoint
	Added         int    `json:"added,omitempty"`         // checkpoint
	Removed       int    `json:"removed,omitempty"`       // checkpoint
	Files         int    `json:"files,omitempty"`         // checkpoint
	DurationMs    int64  `json:"durationMs,omitempty"`    // turn time, Stop to Stop
}

// SSHHumanMessage is something the human said to the agent, or a handback's text.
// Text is the human's own words, without lola's framing. Delivered means lola put
// them in front of the agent, not that it read them. Recalled means it never will.
type SSHHumanMessage struct {
	Origin      string    `json:"origin,omitempty"` // say | answer | handback | initial | choice
	Conn        string    `json:"conn,omitempty"`
	Text        string    `json:"text"`
	Transcript  string    `json:"transcript,omitempty"` // handback only: exactly what was sent
	Delivered   bool      `json:"delivered"`
	DeliveredAt time.Time `json:"deliveredAt,omitzero"`
	Via         string    `json:"via,omitempty"` // reply | prompt
	Recalled    bool      `json:"recalled,omitempty"`
	Returned    bool      `json:"returned,omitempty"` // Esc put it back in the agent's prompt as a draft
}

// SSHTimelineItem is one row. Exactly one of Block, Element, System and Human is
// set, as Kind says. ID is stable across changes.
//
// Order is fixed at creation and is the sort key. Seq moves on EVERY change, so a
// client fetching After=<last seq> gets everything that changed, and upserts by
// ID: a block going from pending to running to exit 1 stays one row.
type SSHTimelineItem struct {
	ID      string           `json:"id"`
	Kind    string           `json:"kind"`
	Order   int64            `json:"order"`
	Seq     int64            `json:"seq"`
	At      time.Time        `json:"at,omitzero"`
	Block   *SSHBlock        `json:"block,omitempty"`
	Element *SSHElement      `json:"element,omitempty"`
	System  *SSHSystemEvent  `json:"system,omitempty"`
	Human   *SSHHumanMessage `json:"human,omitempty"`
}

// SSHTimelineArgs fetches a session's timeline. After 0 is the whole of it, Conn
// narrows to one connection, and Limit 0 means 500.
type SSHTimelineArgs struct {
	Session string `json:"session"`
	Conn    string `json:"conn,omitempty"`
	After   int64  `json:"after,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

// SSHTimelineData is the reply. Connections is always current and complete, so a
// client never has to derive connection state from the items.
type SSHTimelineData struct {
	Session     string            `json:"session"`
	Connections []SSHConnInfo     `json:"connections"`
	Items       []SSHTimelineItem `json:"items"`
	LastSeq     int64             `json:"lastSeq"`
	More        bool              `json:"more,omitempty"` // Limit was hit: fetch again from LastSeq
}

// SSHBlockTerminal reports whether a block's status can no longer change.
func SSHBlockTerminal(status string) bool {
	switch status {
	case SSHBlockPendingApproval, SSHBlockQueued, SSHBlockRunning:
		return false
	}
	return true
}

// SSHCLIExit is the exit code `lola ssh run` ends with for a finished block: the
// remote command's own code when it ran to an exit, lola's reserved code when not.
func SSHCLIExit(b SSHBlock) int {
	switch b.Status {
	case SSHBlockSucceeded, SSHBlockFailed:
		if b.ExitCode != nil {
			return *b.ExitCode
		}
		return SSHExitLost
	case SSHBlockDenied, SSHBlockExpired:
		return SSHExitDenied
	case SSHBlockRefused:
		return SSHRefusalExit(b.RefusalCode)
	case SSHBlockTimedOut:
		return SSHExitTimedOut
	case SSHBlockStopped:
		return SSHExitStopped
	case SSHBlockLost:
		return SSHExitLost
	}
	return SSHExitStillWaiting
}

// SSHRefusalExit maps a refusal code to the CLI's exit code.
func SSHRefusalExit(code string) int {
	switch code {
	case SSHRefuseHumanControl:
		return SSHExitHumanControl
	case SSHRefuseNoConnection, SSHRefuseNotConnected, SSHRefuseAwaitingGo:
		return SSHExitNotConnected
	case SSHRefuseConnDenied:
		return SSHExitDenied
	}
	return SSHExitPolicyRefused
}
