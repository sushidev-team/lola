package protocol

import "time"

// The conversation wire: what the session view reads and sends for every agent
// session, connected to a host or not. Everything here is additive and carried on
// its own commands, so nothing new reaches SessionInfo and a phone sees no change.

// Conversation commands. All are HUMAN side: they go in SSHHumanCommands, which
// refuses them at the hook bridge and at remote. Never add one to SSHAgentCommands.
const (
	CmdAgentFeed         = "agentFeed"         // AgentFeedArgs → AgentFeedData
	CmdAgentFeedOutput   = "agentFeedOutput"   // AgentFeedOutputArgs → AgentFeedOutputData
	CmdTalkState         = "talkState"         // TalkStateArgs → TalkStateData
	CmdAgentSay          = "agentSay"          // AgentSayArgs → AgentSayData
	CmdAgentStop         = "agentStop"         // AgentStopArgs → AgentStopData
	CmdAgentChoose       = "agentChoose"       // AgentChooseArgs → AgentChooseData
	CmdAgentQueueRelease = "agentQueueRelease" // AgentQueueReleaseArgs → AgentSayData
	CmdEndedSessions     = "endedSessions"     // no args → EndedSessionsData
)

// Feed health. Display only.
const (
	FeedOK          = "ok"
	FeedEmpty       = "empty"       // no log yet: not a warning
	FeedPartial     = "partial"     // hook counts and feed counts disagree
	FeedUnreadable  = "unreadable"  // the file grows and yields no items
	FeedUnsupported = "unsupported" // no reader for this agent or version
)

// Feed item kinds. Status values: running | ok | error | interrupted | no_result.
const (
	FeedItemUser    = "user"
	FeedItemText    = "text"
	FeedItemTool    = "tool"
	FeedItemDivider = "divider"
	FeedItemNotice  = "notice"
)

// AgentFeedArgs asks for part of the agent's log. After == 0 and Before == 0 means
// the newest Limit items, in Order, with More true when older ones exist. A client
// never loops on More: older history is paged by Before.
type AgentFeedArgs struct {
	Session string `json:"session"`
	Epoch   string `json:"epoch,omitempty"`
	After   int64  `json:"after,omitempty"`
	Before  int64  `json:"before,omitempty"` // Order, for older history
	Parent  string `json:"parent,omitempty"` // subagent thread
	Limit   int    `json:"limit,omitempty"`  // 0 means 300
}

// AgentFeedData is one page of the agent's log.
type AgentFeedData struct {
	Session string `json:"session"`
	Epoch   string `json:"epoch"` // changed: discard and refetch
	LastSeq int64  `json:"lastSeq"`
	More    bool   `json:"more,omitempty"`
	// Scanning: the scanner has not reached EOF, so these are not yet the tail.
	// Health is not judged while it is true. The client says "Reading this conversation".
	Scanning bool            `json:"scanning,omitempty"`
	Health   string          `json:"health"`
	Items    []AgentFeedItem `json:"items"`
}

// AgentFeedItem is everything lola read from the agent's own log. Every field is a
// CLAIM: the agent can write that file. Echo alone is lola's: this user record
// matches a send lola recorded, so the client hides it and shows the store's row.
type AgentFeedItem struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Order     int64          `json:"order"`
	Seq       int64          `json:"seq"`
	At        time.Time      `json:"at,omitzero"`
	Turn      string         `json:"turn,omitempty"`
	Parent    string         `json:"parent,omitempty"`
	Text      string         `json:"text,omitempty"`
	Truncated bool           `json:"truncated,omitempty"`
	Origin    string         `json:"origin,omitempty"` // terminal | system | task | teammate
	Echo      bool           `json:"echo,omitempty"`
	Tool      *AgentFeedTool `json:"tool,omitempty"`
}

// AgentFeedTool is one tool call as the log tells it. A claim, like the rest.
type AgentFeedTool struct {
	Name       string `json:"name"`
	Category   string `json:"category"` // read | edit | run | agent | other
	Summary    string `json:"summary"`
	Detail     string `json:"detail,omitempty"` // the whole line, when Summary shortened or replaced it
	Status     string `json:"status"`
	DurationMs int64  `json:"durationMs,omitempty"`
	Added      int    `json:"added,omitempty"`
	Removed    int    `json:"removed,omitempty"`
	Preview    string `json:"preview,omitempty"` // 240 chars, sanitized
	HasOutput  bool   `json:"hasOutput,omitempty"`
	Subagent   string `json:"subagent,omitempty"`
}

// AgentFeedOutputArgs asks for one tool call's output, on a human's request.
type AgentFeedOutputArgs struct {
	Session string `json:"session"`
	Item    string `json:"item"`
}

// AgentFeedOutputData is that output, sanitized and capped.
type AgentFeedOutputData struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

// TalkCaps says what lola has measured for this session's agent. Unverified means
// not offered. Why is lola's own sentence when Send is false.
type TalkCaps struct {
	Feed  bool   `json:"feed"`
	Send  bool   `json:"send"`
	Stop  bool   `json:"stop"`
	Cards bool   `json:"cards"`
	Why   string `json:"why,omitempty"`
}

// Composer states, as a fresh pane read says.
const (
	ComposerResting = "resting"
	ComposerWorking = "working"
	ComposerDraft   = "draft"
	ComposerPrompt  = "prompt"
	ComposerModal   = "modal"
	ComposerGone    = "gone"
	ComposerUnknown = "unknown"
)

// AgentPrompt is a read of the pane. Untrusted text, memory only: never written to
// the session snapshot, the store or a log. Hash pins exactly what a human read.
type AgentPrompt struct {
	Kind    string        `json:"kind"` // permission | question | question_multi | modal | background
	Hash    string        `json:"hash"`
	Lines   []string      `json:"lines"`
	Choices []AgentChoice `json:"choices,omitempty"`
	Since   time.Time     `json:"since,omitzero"`
}

// AgentChoice is one option of a prompt, in pane order and in the pane's own words.
type AgentChoice struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"` // a question option's description, under its name in the pane
}

// TalkStateArgs names the session a mounted view is showing.
type TalkStateArgs struct {
	Session string `json:"session"`
}

// TalkStateData is the viewed session's live state. It is on its own command, and
// not on SessionInfo, because handleRemote strips only SSH from that record: a new
// field there reaches phones by default.
type TalkStateData struct {
	Session   string       `json:"session"`
	Caps      TalkCaps     `json:"caps"`
	Composer  string       `json:"composer"`
	Prompt    *AgentPrompt `json:"prompt,omitempty"`
	Deferred  string       `json:"deferred,omitempty"` // AutomationDeferredReason
	QueueHeld bool         `json:"queueHeld,omitempty"`
	FeedSeq   int64        `json:"feedSeq"`
	// FeedEpoch names the reader FeedSeq counts on. A new one starts its count over.
	FeedEpoch string `json:"feedEpoch,omitempty"`
	Health    string `json:"health"`
}

// AgentSayArgs is a human speaking to the agent from the composer.
type AgentSayArgs struct {
	Session   string `json:"session"`
	Text      string `json:"text"`
	Conn      string `json:"conn,omitempty"`      // the host in scope, "" under All
	StopFirst bool   `json:"stopFirst,omitempty"` // Stop & send
}

// AgentSayData says what became of it.
type AgentSayData struct {
	Item      string `json:"item"`
	Delivered bool   `json:"delivered"`
	Why       string `json:"why,omitempty"` // lola's sentence when queued
	AgentGone bool   `json:"agentGone,omitempty"`
}

// AgentStopArgs is a human stopping this turn.
type AgentStopArgs struct {
	Session string `json:"session"`
}

// AgentStopData says whether a turn was stopped, and how many queued messages it held.
type AgentStopData struct {
	Stopped bool   `json:"stopped"`
	Why     string `json:"why,omitempty"` // "Already stopped"
	Held    int    `json:"held,omitempty"`
}

// AgentChooseArgs is a key a human pressed on a card, pinned to the prompt they read.
type AgentChooseArgs struct {
	Session string `json:"session"`
	Hash    string `json:"hash"`
	Key     string `json:"key"`
}

// AgentChooseData says whether the key went, and what the pane asks next.
type AgentChooseData struct {
	Sent   bool         `json:"sent"`
	Why    string       `json:"why,omitempty"`
	Prompt *AgentPrompt `json:"prompt,omitempty"` // the pane's next prompt, if any
}

// AgentQueueReleaseArgs is "Send them". It is a press: it clears QueueHeld and the
// hold, then attempts the head batch through lockHumanPrompt. Answers with AgentSayData.
type AgentQueueReleaseArgs struct {
	Session string `json:"session"`
}

// EndedSession is a session that left the list and whose story is still readable.
type EndedSession struct {
	KeptWorktree string    `json:"keptWorktree,omitempty"` // directory retained when the session ended
	ID           string    `json:"id"`
	Project      string    `json:"project,omitempty"`
	Title        string    `json:"title,omitempty"`
	Issue        string    `json:"issue,omitempty"`
	Agent        string    `json:"agent,omitempty"`
	EndedAt      time.Time `json:"endedAt,omitzero"`
	Reason       string    `json:"reason,omitempty"` // lola's words: "PR #412 merged"
	PRNumber     int       `json:"prNumber,omitempty"`
	PRURL        string    `json:"prUrl,omitempty"`
}

// EndedSessionsData lists them.
type EndedSessionsData struct {
	Sessions []EndedSession `json:"sessions"`
}
