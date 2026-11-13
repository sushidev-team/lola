package protocol

// Pane lifecycle: closing an auxiliary pane, and pinning one to a phone's size
// while a phone is looking at it.
//
// These live in their own file rather than in protocol.go because that file is
// under concurrent edit; the split is organisational and carries no meaning.

// PaneCloseArgs is the request for cmd=paneClose.
//
// Session names the owning session and Pane the tmux session to close. Both are
// required and both are checked: the pane must belong to that session, which is
// what stops one device closing another session's tab by naming it.
//
// The AGENT pane can never be closed this way, and that is a rule rather than a
// nicety — the agent pane IS the session, so closing it would kill the work and
// leave a record pointing at nothing. Teardown is cmd=kill, which knows how to
// take the worktree and the branch with it.
type PaneCloseArgs struct {
	Session string `json:"session"`
	Pane    string `json:"pane"`
}

// PaneCloseData is Response.Data for cmd=paneClose.
type PaneCloseData struct {
	Session string `json:"session"`
	Pane    string `json:"pane"`
	Closed  bool   `json:"closed"`
}

// PaneResizeArgs is the request for cmd=paneResize: pin a pane's tmux window to
// an explicit size, or release it.
//
// COLS == 0 RELEASES. That is the whole shape of the feature: a phone pins the
// pane it is looking at to its own dimensions and releases it the moment it
// looks away, so the developer's own view is squashed only while somebody is
// actually reading it on a phone.
//
// It is the deliberate opposite of panebus's `-f ignore-size` attach, which
// exists so that a phone joining the byte fan-out CANNOT reshape the desktop's
// window. Both are correct: the fan-out must never resize anything, and this,
// which a human explicitly asked for, must — for as long as they are looking,
// and not one moment longer. A client that pins and then dies leaves the window
// pinned, so the daemon releases on the subscription ending too.
type PaneResizeArgs struct {
	Session string `json:"session"`
	Pane    string `json:"pane"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
}

// PaneResizeData is Response.Data for cmd=paneResize. Pinned reports which way
// the call went, so a client never has to infer it from the arguments it sent.
type PaneResizeData struct {
	Session string `json:"session"`
	Pane    string `json:"pane"`
	Pinned  bool   `json:"pinned"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
}
