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

// Human commands (desktop app and CLI): a person at this machine decides who
// may attach to what. All are tier never for a phone and absent from the sandbox
// hook bridge, so neither a phone nor an agent can share a session or pin a
// teammate.
const (
	// CmdShareSession turns sharing of one session on or off (Args: ShareSessionArgs).
	CmdShareSession = "shareSession"
	// CmdShareMembers lists the teammates this machine can pin or has pinned
	// (answers ShareMembersData).
	CmdShareMembers = "shareMembers"
	// CmdShareTrust pins a teammate's key after the person compared its
	// fingerprint (Args: ShareTrustArgs).
	CmdShareTrust = "shareTrust"
	// CmdShareUntrust removes a pinned teammate (Args: ShareUntrustArgs).
	CmdShareUntrust = "shareUntrust"
	// CmdShareOffered lists the shares teammates offer this machine
	// (answers ShareOfferedData).
	CmdShareOffered = "shareOffered"
)

// ShareHumanCommands is the whole set; tiers.go classifies it never.
var ShareHumanCommands = []string{CmdShareSession, CmdShareMembers, CmdShareTrust, CmdShareUntrust, CmdShareOffered}

// ShareSessionArgs is Request.Args for cmd=shareSession.
type ShareSessionArgs struct {
	Session string `json:"session"`
	On      bool   `json:"on"`
	// Title defaults to the session's display title.
	Title string `json:"title,omitempty"`
	// Level is "view" (default) or "type".
	Level string `json:"level,omitempty"`
	// With are hub device ids of pinned members; empty = every pinned member.
	With []string `json:"with,omitempty"`
	// ConfirmUnsandboxed must be true to give "type" access to a session that
	// runs on the host rather than in the sandbox.
	ConfirmUnsandboxed bool `json:"confirmUnsandboxed,omitempty"`
}

// ShareSessionData answers cmd=shareSession.
type ShareSessionData struct {
	Session string `json:"session"`
	On      bool   `json:"on"`
	ShareID string `json:"shareId,omitempty"`
	Level   string `json:"level,omitempty"`
}

// ShareMemberInfo is one teammate device.
type ShareMemberInfo struct {
	DeviceID    string `json:"deviceId"` // the hub's device id
	Name        string `json:"name"`
	Member      string `json:"member,omitempty"`
	Fingerprint string `json:"fingerprint"` // what the person compares before pinning
	// Allowed: this teammate may attach to sessions shared from this machine.
	Allowed bool `json:"allowed"`
	// Trusted: this machine will attach to the sessions this teammate shares.
	Trusted bool `json:"trusted"`
	// Attached counts this member's live attachments to this machine.
	Attached int `json:"attached,omitempty"`
}

// ShareMembersData answers cmd=shareMembers.
type ShareMembersData struct {
	Members []ShareMemberInfo `json:"members"`
	// Offline is true when the hub could not be asked; Members then holds only
	// the pinned ones.
	Offline bool `json:"offline,omitempty"`
}

// ShareTrustArgs is Request.Args for cmd=shareTrust.
type ShareTrustArgs struct {
	DeviceID string `json:"deviceId"`
	// Fingerprint is the one the person compared. The daemon refuses unless it
	// equals the fingerprint of the key the hub names NOW.
	Fingerprint string `json:"fingerprint"`
	// Allow lets the teammate attach to sessions shared from this machine;
	// Trust lets this machine attach to the sessions the teammate shares. Each
	// is a separate decision: pinning one direction grants nothing in the other.
	Allow bool `json:"allow,omitempty"`
	Trust bool `json:"trust,omitempty"`
}

// ShareUntrustArgs is Request.Args for cmd=shareUntrust. It ends the teammate's
// live attachments. Allow and Trust name the directions to remove; neither set
// removes both.
type ShareUntrustArgs struct {
	DeviceID string `json:"deviceId"`
	Allow    bool   `json:"allow,omitempty"`
	Trust    bool   `json:"trust,omitempty"`
}

// ShareOffer is one session a teammate offers this machine.
type ShareOffer struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Agent       string `json:"agent,omitempty"`
	Level       string `json:"level"`
	OwnerDevice string `json:"ownerDevice"`
	OwnerName   string `json:"ownerName"`
	OwnerMember string `json:"ownerMember,omitempty"`
	Online      bool   `json:"online"`
	// Trusted is whether this machine pinned the owner's key (it must, to attach).
	Trusted bool `json:"trusted"`
}

// ShareOfferedData answers cmd=shareOffered.
type ShareOfferedData struct {
	Offers  []ShareOffer `json:"offers"`
	Offline bool         `json:"offline,omitempty"`
}

// SessionShare is a session's sharing state on the wire (SessionInfo.Share).
type SessionShare struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Level string `json:"level"`
	// With are the hub device ids the share is limited to (empty = everyone pinned).
	With []string `json:"with,omitempty"`
	// Attached are the teammates attached to it right now.
	Attached []ShareViewer `json:"attached,omitempty"`
}
