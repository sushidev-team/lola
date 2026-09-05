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
	// It is distinct from the unconditional denials in policy.go, which no
	// authorizer is consulted about at all.
	ErrDenied = errors.New("remote: frame denied for this peer")

	// ErrNoAuthorizer is returned by newAuthorizer in a build without the
	// lola_insecure tag: M1 has no cryptography, so a release binary has no way
	// to authenticate a peer and therefore refuses to listen. M2 replaces this
	// with the device registry and mutual TLS.
	ErrNoAuthorizer = errors.New("remote: no authorizer in this build")
)

// Peer is the identity a connection was authenticated as. It is built by the
// authorizer and is READ-ONLY to everything downstream: nothing on the wire may
// set any of it, which is the whole reason the envelope carries no device id.
// A peer-asserted identity field would be a field the server must remember to
// ignore, and that is the shape of mistake this design removes rather than
// documents.
type Peer struct {
	// DeviceID names the peer in the audit line. In M1 there is exactly one
	// possible value; in M2 it is the registry key.
	DeviceID string

	// Label is a human-facing name for a log line ("Martin's iPhone"). It is
	// never used for a decision.
	Label string

	// RemoteAddr is the peer's address as the listener saw it.
	RemoteAddr string

	// ConnectedAt stamps the accept, from Options.Now.
	ConnectedAt time.Time

	// Insecure records that this peer was authenticated by the M1 bearer-key
	// path rather than by a device key. It exists so the audit line can say so
	// on every mutating frame; nothing grants or withholds anything on it.
	Insecure bool
}

// Handshake is the pre-dispatch view of a newly accepted connection, handed to
// Authorizer.Authenticate before a single frame is routed.
//
// It carries both a read and a write closure because the two authentication
// shapes this design has to accommodate are genuinely different, and the seam
// would leak one of them if it only carried the other. M2 authenticates from
// the TLS handshake — a pinned client certificate checked against the device
// registry — and ignores NextFrame entirely. M1 has no cryptography, so its
// bearer key has to arrive IN BAND over the already-encrypted connection, which
// means reading exactly one frame and answering it. Keeping that difference
// inside the authorizer is what lets the whole frame loop below stay tag-free.
type Handshake struct {
	// RemoteAddr is the peer's address.
	RemoteAddr string

	// TLS is the completed handshake state, or nil when the connection is not
	// a TLS connection (an in-memory pipe in a test). An authorizer that needs
	// it must check for nil and refuse, never assume.
	TLS *tls.ConnectionState

	// At is the accept time, from Options.Now.
	At time.Time

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
// is consulted AFTER the unconditional denials in policy.go, never before, so
// there is no implementation that can grant stop, hookEvent or a forced kill.
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
	// revocation in M2: it takes effect on the device's next frame rather than
	// whenever it next reconnects. In M1 it is a formality, and it is still
	// called on every frame so that the shape cannot rot before there is
	// something to enforce.
	AuthorizeFrame(ctx context.Context, peer Peer, f *protocol.Frame) error
}
