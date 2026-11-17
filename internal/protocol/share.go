package protocol

// Team session sharing: the one request a team member may send an owner's
// daemon besides attaching to a shared pane. A member peer is not a phone: it
// holds no tier, reaches no other command, and the daemon names which panes it
// may attach per shared session (docs/team-sessions.md).

// CmdShareSessions asks the owner's daemon for the sessions shared with the
// asking member and who is attached to each. Member connections only.
const CmdShareSessions = "shareSessions"

// Share levels, as hubproto spells them.
const (
	ShareLevelView = "view"
	ShareLevelType = "type"
)

// SharedSession is one session as a member sees it. ID is the share id (random,
// not the lola session id); Pane is the name to subscribe to.
type SharedSession struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Agent    string        `json:"agent,omitempty"`
	Level    string        `json:"level"`
	Pane     string        `json:"pane"`
	Attached []ShareViewer `json:"attached,omitempty"`
}

// ShareViewer is one member attached to a shared session. Name is the member
// device's label as the owner pinned it; Me marks the asking member.
type ShareViewer struct {
	Name  string `json:"name"`
	Me    bool   `json:"me,omitempty"`
	Since int64  `json:"since"`           // unix seconds
	Typed bool   `json:"typed,omitempty"` // holds a type level
}

// ShareSessionsData answers CmdShareSessions.
type ShareSessionsData struct {
	Host     string          `json:"host,omitempty"`
	Sessions []SharedSession `json:"sessions"`
}
