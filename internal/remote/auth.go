package remote

import (
	"context"
	"crypto/tls"
	"errors"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// Errors an authorizer or its constructor returns. They are compared with
// errors.Is by the server, which turns each into a refusal and a log line; none
// of them is ever echoed to the peer, because an error's text is for the local
// log and a refusal on the wire is a closed code from internal/protocol.
var (
	// ErrUnauthenticated is the authorizer's answer to a peer it cannot
	// identify. The connection is closed without a frame reaching Handle.
	ErrUnauthenticated = errors.New("remote: unauthenticated peer")

	// ErrDenied is the authorizer's answer to a frame this peer may not send.
	// It is distinct from tier never, which conn.authorize refuses before any
	// authorizer is consulted, so no implementation can grant one.
	ErrDenied = errors.New("remote: frame denied for this peer")

	// ErrPairingDone ends a pairing connection after its outcome frame was
	// written. It is not a rejection: the phone reconnects as a paired device
	// (or the pairing failed and was already audited), so it logs at info.
	ErrPairingDone = errors.New("remote: pairing connection finished")
)

// The path a connection arrived on. The value is wire-visible (ReadyData.Via,
// PairStatusData.Via) and a log field (via=).
const (
	ViaLAN   = "lan"   // a socket accepted by a listener (LAN, VPN, loopback)
	ViaRelay = "relay" // a spliced hub stream handed in by ServeRelayed
)

// relayed is implemented by the relay's net.Conn. A conn whose TLS layer sits on
// one gets Handshake.Via = ViaRelay and Handshake.RelayRV = relayRV(); any other
// conn is ViaLAN.
type relayed interface{ relayRV() string }

// Peer is the identity a connection was authenticated as. It is built by the
// authorizer and is READ-ONLY to everything downstream: nothing on the wire may
// set any of it, which is the whole reason the envelope carries no device id.
// A peer-asserted identity field would be a field the server must remember to
// ignore, and that is the shape of mistake this design removes rather than
// documents.
type Peer struct {
	// DeviceID is the registry id; it names the peer in the audit line.
	DeviceID string

	// Label is a human-facing name for a log line ("Martin's iPhone"). It is
	// never used for a decision.
	Label string

	// RemoteAddr is the peer's address as the listener saw it.
	RemoteAddr string

	// Epoch is the registry epoch of the device entry this connection
	// authenticated under. AuthorizeFrame ends the connection when the entry
	// was revoked or replaced since.
	Epoch uint64

	// Via is the path the connection arrived on: ViaLAN or ViaRelay. It is
	// copied from Handshake.Via and never set from anything the peer sends.
	Via string

	// Member marks a team member (docs/team-sessions.md): a teammate's daemon
	// the owner pinned by key. It is set only by the authorizer, holds no tier
	// and reaches only what MemberGate allows.
	Member bool

	// ConnectedAt stamps the accept, from Options.Now.
	ConnectedAt time.Time
}

// Handshake is the pre-dispatch view of a newly accepted connection, handed to
// Authorizer.Authenticate before a single frame is routed.
//
// Authentication is by the pinned client certificate in TLS; a pairing
// candidate is the exception and talks in band, which is why the handshake also
// carries a read and a write closure. Keeping that difference inside the
// authorizer is what lets the whole frame loop stay ignorant of it.
type Handshake struct {
	// RemoteAddr is the peer's address.
	RemoteAddr string

	// TLS is the completed handshake state, or nil when the connection is not
	// a TLS connection (an in-memory pipe in a test). An authorizer that needs
	// it must check for nil and refuse, never assume.
	TLS *tls.ConnectionState

	// At is the accept time, from Options.Now.
	At time.Time

	// Via is ViaLAN or ViaRelay; "" is read as ViaLAN.
	Via string

	// RelayRV is the rendezvous id the hub matched; "" unless Via == ViaRelay.
	RelayRV string

	// SetReadDeadline lets an authorizer that holds the connection longer than
	// the handshake (pairing waits for a human) manage its own read deadline.
	SetReadDeadline func(time.Time) error

	// NextFrame reads one more frame from the connection. It has already
	// passed the envelope checks — a known version, a known type, travelling
	// towards the daemon — so an authorizer only has to judge the payload. The
	// caller has set a bounded read deadline for the whole handshake, so this
	// cannot block forever; the error it returns on expiry closes the
	// connection.
	NextFrame func() (*protocol.Frame, error)

	// Send writes one frame back, for an acknowledgement or a refusal. A
	// refusal written here is best effort: the connection is closing either
	// way.
	Send func(f *protocol.Frame) error
}

// Authorizer decides who may connect and what each frame of theirs may do. It
// is consulted AFTER tier never is refused, never before, so there is no
// implementation that can grant stop, hookEvent or a forced kill.
//
// Both methods must be safe for concurrent use: AuthorizeFrame is called from
// the connection's reader goroutine, and one server serves many connections.
type Authorizer interface {
	// Authenticate establishes the peer identity for one connection. It is
	// called exactly once, after the TLS handshake and before any frame is
	// dispatched. An error — ErrUnauthenticated or anything else — closes the
	// connection with nothing having reached Handle.
	Authenticate(ctx context.Context, hs *Handshake) (Peer, error)

	// AuthorizeFrame is called for EVERY inbound frame, not once per
	// connection. That is what makes a capability downgrade behave like a
	// revocation: it takes effect on the device's next frame rather than
	// whenever it next reconnects. It must have no side effects, because the
	// event pump calls it before every pushed event too.
	AuthorizeFrame(ctx context.Context, peer Peer, f *protocol.Frame) error
}
