package protocol

import (
	"encoding/json"
	"fmt"
)

// Frame is one message of the REMOTE protocol: the versioned envelope a paired
// mobile client and the daemon exchange over TLS. It is deliberately a separate
// vocabulary from the newline-JSON Request/Response above, which is spoken only
// on the unix socket and has no version field, no peer identity and no
// correlation id. A remote peer never speaks that vocabulary directly — the
// daemon rebuilds a fresh Request from an allowlist of each command's own
// fields and passes THAT to the same handler the unix socket calls.
//
// Everything below lives here, beside Request/Response, rather than in
// internal/remote, and the reason is a dependency cycle rather than taste.
// internal/remote hands out pane subscriptions, so it imports internal/panebus;
// internal/panebus builds the resync frame out of its shadow emulator, so if
// the frame types lived in remote, panebus would have to import remote. A
// dependency-free leaf everybody may import is exactly the shape that resolves
// that, and it is what this package already is. The framing CODEC lives here
// for the same reason and one more: both ends of the wire — the daemon's
// listener and any Go client — must agree on it byte for byte, and neither owns
// the other. See framecodec.go, which is the only I/O in this package.
//
// Every field except V and Type is optional, and the rule that keeps V cheap is:
// V bumps only when the ENVELOPE changes incompatibly. A new Type value, a new
// Cmd, or a new omitempty field on a payload is additive and does not bump it —
// an unknown Type fails closed on either side, which is the correct outcome for
// both directions of skew. Decoding is therefore deliberately tolerant of
// unknown JSON fields (no DisallowUnknownFields): a newer peer's additive field
// must not turn into a parse failure.
type Frame struct {
	V    int    `json:"v"`              // envelope version; see FrameVersionCurrent
	Type string `json:"type"`           // one of the Frame* constants below
	ID   string `json:"id,omitempty"`   // request correlation; echoed on the reply
	Cmd  string `json:"cmd,omitempty"`  // req frames only: the protocol.Request Cmd
	Pane string `json:"pane,omitempty"` // sub/unsub/pty/resync: the tmux session name
	Seq  uint64 `json:"seq,omitempty"`  // per-pane stream ordering, for gap detection

	// Payload is the type-specific body: a protocol.Request on a req frame, a
	// protocol.Response on a resp frame, and one of the *Payload types below on
	// everything else. It is raw so the envelope can be decoded, authorized and
	// routed before anything untrusted is unmarshalled.
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Envelope versions. A daemon accepts V in [FrameVersionMin, FrameVersionCurrent]
// and refuses anything else with an ErrPayload naming both bounds, so a client
// can say WHICH side is behind rather than showing a connect error.
const (
	FrameVersionCurrent = 1
	FrameVersionMin     = 1
)

// Frame types. Direction is fixed per type and is part of the contract: a
// daemon that receives a daemon-to-client type, or a client that receives a
// client-to-daemon one, refuses the frame rather than guessing. FrameDirections
// is the machine-readable form of that table.
const (
	FrameReq    = "req"    // client -> daemon: Payload is a Request, Cmd names it
	FrameResp   = "resp"   // daemon -> client: Payload is a Response, ID echoes the req
	FrameSub    = "sub"    // client -> daemon: subscribe to Pane; Payload is SubPayload
	FrameUnsub  = "unsub"  // client -> daemon: drop the subscription on Pane
	FrameResync = "resync" // daemon -> client: Payload is ResyncPayload
	FramePTY    = "pty"    // BOTH: PTYOutputPayload outbound, PTYInputPayload inbound
	FrameErr    = "err"    // BOTH: Payload is ErrPayload; on ID when a request failed
)

// FrameDir is the set of directions a frame type may legitimately travel in.
// The zero value means "no direction", which is what an UNKNOWN type resolves
// to — so every predicate below refuses it without a special case.
type FrameDir uint8

const (
	// DirToDaemon marks a type a client may SEND and a daemon may RECEIVE.
	DirToDaemon FrameDir = 1 << iota
	// DirToClient marks a type a daemon may SEND and a client may RECEIVE.
	DirToClient
)

// FrameDirections reports the directions declared for a frame type, and 0 for
// any type this build does not know. It is the whole direction table in one
// place so the daemon's dispatch and a client's reader cannot drift apart.
//
// FramePTY is bidirectional because the two directions carry DIFFERENT payloads
// (PTYOutputPayload out, PTYInputPayload in), not because either side may replay
// the other's. FrameErr is bidirectional so a client can report a refusal it
// made locally; the daemon's only correct handling of an inbound err frame is to
// log it and carry on — it never asked the phone a question, so there is nothing
// for it to act on.
func FrameDirections(t string) FrameDir {
	switch t {
	case FrameReq, FrameSub, FrameUnsub:
		return DirToDaemon
	case FrameResp, FrameResync:
		return DirToClient
	case FramePTY, FrameErr:
		return DirToDaemon | DirToClient
	}
	return 0
}

// KnownFrameType reports whether t is a frame type this build understands.
// An unknown type is refused with CodeUnknownType, never guessed at.
func KnownFrameType(t string) bool { return FrameDirections(t) != 0 }

// DaemonAcceptsFrame reports whether the daemon may RECEIVE a frame of type t.
func DaemonAcceptsFrame(t string) bool { return FrameDirections(t)&DirToDaemon != 0 }

// ClientAcceptsFrame reports whether a client may RECEIVE a frame of type t.
func ClientAcceptsFrame(t string) bool { return FrameDirections(t)&DirToClient != 0 }

// SupportedFrameVersion reports whether v is an envelope version this build
// speaks. The window is a closed interval and widening it later is a decision,
// not a default: there is no V0, and a peer outside the window gets one
// UnsupportedVersionFrame and a closed connection.
func SupportedFrameVersion(v int) bool {
	return v >= FrameVersionMin && v <= FrameVersionCurrent
}

// SubPayload is the body of a sub frame (client -> daemon). Cols and Rows are
// the subscriber's own viewport and are ADVISORY: the pane bus attaches once per
// pane at the tmux window size and fans the full untruncated stream out, so a
// subscriber pans client-side. They exist so the daemon can report the window
// size against them and a client can draw its "200 cols - panning" chip.
//
// A sub is acknowledged by the first resync frame carrying the same ID. A
// refusal — an unresolvable pane, a name failing the anchored pattern, a pane
// the device may not see — is an err frame on that ID and nothing else.
type SubPayload struct {
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`
}

// ResyncPayload is a COHERENT snapshot of a pane's screen, rendered from the
// daemon's shadow emulator (internal/vtterm). It is sent on subscribe and again
// on every size change, and it is why the emulator exists: an agent runs on the
// ALTERNATE screen, where a fresh tmux attach replays nothing, so a subscriber
// with no prelude stares at a blank pane until the agent next repaints. A raw
// byte ring cannot replace it — a resumed ring can begin mid-escape-sequence,
// whereas a rendered frame is always parseable from its first byte.
//
// Lines are screen rows with ANSI SGR preserved, TRAILING BLANK ROWS TRIMMED
// (vtterm.Render does the trimming), so len(Lines) may be less than Rows and a
// client pads the remainder with blanks rather than assuming a full grid.
// Cursor is in cells, zero-based, and is the field capture-pane cannot give —
// it is precisely what a human needs in order to answer a prompt.
type ResyncPayload struct {
	Cols      int      `json:"cols"`
	Rows      int      `json:"rows"`
	Lines     []string `json:"lines,omitempty"`
	CursorX   int      `json:"cursorX"`
	CursorY   int      `json:"cursorY"`
	AltScreen bool     `json:"altScreen,omitempty"`
	Exited    bool     `json:"exited,omitempty"` // the pane's child has ended

	// CursorHidden mirrors DECTCEM (?25) and is stated in the NEGATIVE
	// deliberately. A visible caret is the overwhelmingly common case and the
	// safe one to assume, so with omitempty it costs nothing on the wire, and —
	// the load-bearing half — a client that reads this field against a daemon
	// that does not yet write it degrades to "visible" rather than painting no
	// caret at all on every pane. Without it a subscriber attaching mid-session
	// draws a caret over a pane that has hidden one, which reads as the agent
	// waiting for input when it is not.
	CursorHidden bool `json:"cursorHidden,omitempty"`
}

// PTYOutputPayload is raw pane output (daemon -> client), coalesced on a short
// tick so an idle pane costs zero bytes. Data is a []byte, which encoding/json
// marshals as base64 and unmarshals back for free — the wire is a base64 string
// for the Swift and JavaScript sides, and no Go code base64s anything by hand.
// PTY bytes are never logged at any level.
type PTYOutputPayload struct {
	Data []byte `json:"data"`
}

// PTY actions, the closed vocabulary of PTYInputPayload.Action. An unrecognized
// action is refused (CodeUnknownType on the frame's ID), never approximated —
// guessing here would type bytes into a live agent.
const (
	PTYActionWrite  = "write"
	PTYActionScroll = "scroll"
	PTYActionResize = "resize"
)

// PTYInputPayload is a client -> daemon action on a subscribed pane. It is the
// most sensitive frame in the protocol: a write is raw bytes into an interactive
// process on the developer's Mac, so it is authorized on the FRAME TYPE (a Cmd
// tier never reaches it) and it deliberately bypasses the AtPrompt idle gate —
// that gate exists to stop lola's own automation typing mid-turn, not a human.
//
//	"write"  Data is written to the PTY master, preceded by a copy-mode cancel
//	         on the first keystroke after a scroll.
//	"scroll" Lines is handed to tmux.ScrollPane, which decides between the
//	         program's own transcript and copy mode. The client must NEVER
//	         synthesize wheel bytes itself; bounded by tmux.MaxScrollLines.
//	"resize" Cols/Rows, honoured only for a "fit to phone" subscriber when no
//	         other client is attached; otherwise recorded and ignored.
type PTYInputPayload struct {
	Action string `json:"action"` // write|scroll|resize
	Data   []byte `json:"data,omitempty"`
	Lines  int    `json:"lines,omitempty"`
	Cols   int    `json:"cols,omitempty"`
	Rows   int    `json:"rows,omitempty"`
}

// ErrPayload is a machine-readable refusal (both directions). Code is the stable
// discriminator a client branches on; Message is a short human line. Neither
// ever carries the offending payload, a secret, or pane bytes.
//
// MinV/MaxV are set only on CodeUnsupportedVersion, and they are what let the
// app name the side that is behind ("update lola on <host>" versus "update Lola
// from TestFlight") instead of showing a connect error. THIS STRUCT'S FIELD
// LAYOUT IS FROZEN AT V=1 FOREVER: it has to stay decodable by a peer that
// understands nothing else about the version it is talking to, which is the
// whole reason a version mismatch can produce a named screen rather than a
// socket error. Both bounds are >= 1 today, so omitempty never drops them.
type ErrPayload struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	MinV    int    `json:"minV,omitempty"`
	MaxV    int    `json:"maxV,omitempty"`
}

// Refusal codes. This list is closed: an unrecognized code is rendered by the
// client as a generic failure, never interpreted.
const (
	CodeUnsupportedVersion = "unsupported_version"
	CodeUnknownType        = "unknown_type"
	CodeUnknownCmd         = "unknown_cmd" // not in any capability tier, or denied
	CodeDenied             = "denied"      // capability missing for this frame
	CodeUnknownPane        = "unknown_pane"
	CodeFrameTooLarge      = "frame_too_large"
	CodeRateLimited        = "rate_limited"
	CodeInternal           = "internal"
)

// MaxFrameBytes bounds one encoded frame body (the JSON, excluding the length
// prefix) in EITHER direction. It matches the 1 MiB cap internal/daemon's
// unix-socket scanner already applies, so the two transports refuse oversized
// input at the same size. A resync frame of a 200x50 screen with full SGR is
// well under it, and a pty frame is one coalesce window; anything larger is a
// bug or an attack, never a big screen.
const MaxFrameBytes = 1 << 20

// FrameHeaderBytes is the length prefix: a 4-byte big-endian uint32 giving the
// byte count of the JSON body that follows. There is no magic number and no type
// byte — Type lives inside the JSON, so there is exactly one place a frame's
// kind is written down.
const FrameHeaderBytes = 4

// DecodePayload unmarshals the frame's Payload into v. An ABSENT payload leaves
// v untouched and returns nil, so a frame whose body is entirely optional (an
// unsub, a sub taking the defaults) needs no special case at the call site; a
// caller that requires a field must still check it, which every one of them does
// because the fields are what they act on. A malformed payload is
// ErrFrameMalformed — the envelope was fine, so the connection survives and the
// refusal goes back on this frame's ID.
func (f *Frame) DecodePayload(v any) error {
	if len(f.Payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(f.Payload, v); err != nil {
		return fmt.Errorf("%w: %w", ErrFrameMalformed, err)
	}
	return nil
}

// SetPayload marshals v into the frame's Payload, replacing whatever was there.
// A nil v clears it.
func (f *Frame) SetPayload(v any) error {
	if v == nil {
		f.Payload = nil
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("protocol: encode frame payload: %w", err)
	}
	f.Payload = b
	return nil
}

// ErrorFrame builds a refusal on the given correlation id. id may be empty when
// the offending frame carried none. The message is a short human line and must
// never be built from the peer's own payload, a secret, or pane bytes — a codec
// error's text in particular is for the local log, not for the wire.
func ErrorFrame(id, code, message string) Frame {
	f := Frame{V: FrameVersionCurrent, Type: FrameErr, ID: id}
	// Marshalling a struct of strings and ints cannot fail.
	f.Payload, _ = json.Marshal(ErrPayload{Code: code, Message: message})
	return f
}

// UnsupportedVersionFrame is the ONE frame a daemon writes to a peer whose
// envelope version it does not speak, immediately before closing. It carries the
// DAEMON's own V, never the peer's — the daemon never adopts an unknown version
// — and both bounds, which is what lets the client compute the direction of the
// skew instead of showing a connect error.
func UnsupportedVersionFrame(id string) Frame {
	f := Frame{V: FrameVersionCurrent, Type: FrameErr, ID: id}
	f.Payload, _ = json.Marshal(ErrPayload{
		Code:    CodeUnsupportedVersion,
		Message: fmt.Sprintf("daemon speaks envelope v%d", FrameVersionCurrent),
		MinV:    FrameVersionMin,
		MaxV:    FrameVersionCurrent,
	})
	return f
}
