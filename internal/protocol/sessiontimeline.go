package protocol

// CmdSessionTimeline is the phone-safe read of a session's Conversation timeline.
// Its reply is SSHTimelineData, the same type the desktop mounts, with host
// identity, private blocks, widget documents and handback transcripts removed by an
// allowlist copy in the daemon (docs/mobile/01 §8.6).
const CmdSessionTimeline = "sessionTimeline"

// SessionTimelineArgs is Request.Args for cmd=sessionTimeline.
type SessionTimelineArgs struct {
	Session string `json:"session"`
	Conn    string `json:"conn,omitempty"`
	After   int64  `json:"after,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}
