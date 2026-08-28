//go:build lola_insecure

package remote

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"os"

	"github.com/sushidev-team/lola/internal/protocol"
)

// M1's authentication, and everything about it is temporary by construction.
//
// There is no cryptography in M1: the peer proves itself with a bearer key read
// from LOLA_REMOTE_INSECURE_KEY, which the operator also configures on the
// phone. "Deleted, not disabled, in M2" is a promise enforced by memory, so it
// is enforced by the compiler instead — this file only exists under
// //go:build lola_insecure, and insecure_off.go is what a release binary gets.
//
// Two rails hold while the tag is active, and neither is optional:
//
//   - Listen FORCES the bind to loopback whatever [remote].bind says. A shared
//     bearer secret must never reach a network interface, and M1's stated goal
//     — one phone on the same WiFi — is satisfied by a loopback bind plus an
//     SSH forward, with no secret on the wire and no dependence on the network
//     the laptop happens to be on.
//   - Every accept logs a warning. A daemon running this path should be
//     impossible to forget about.

// InsecureKeyEnv names the environment variable holding M1's bearer key. It is
// read once at startup and never appears in argv (ps reads argv), in a log
// line, or in an error.
const InsecureKeyEnv = "LOLA_REMOTE_INSECURE_KEY"

// insecureMinKeyLen is the shortest key this path accepts. There is no key
// derivation and no rate limit on the handshake, so the whole of the secret's
// strength is its length; refusing a short one is cheaper than explaining later
// why it did not matter.
const insecureMinKeyLen = 16

// helloCmd is the req frame that carries the bearer key. It is a command in the
// remote.* namespace, which policy.go denies unconditionally, so one arriving
// after the handshake can never be forwarded to the daemon's dispatcher.
const helloCmd = remoteCmdPrefix + "hello"

// insecureHello is the hello frame's payload. It is deliberately its own shape
// rather than a protocol.Request field: the key is not a command argument, and
// putting it in one would make it something a future handler could read.
type insecureHello struct {
	Key string `json:"key"`
}

// Listen starts the phone listener.
//
// This is the lola_insecure build. It chooses the bearer-key authorizer, and it
// overrides any non-loopback bind mode before a socket is opened — the override
// is logged as a warning rather than made fatal, because refusing to start
// would leave an operator whose config says "lan" with a daemon that is simply
// gone.
func Listen(ctx context.Context, opts Options) (*Server, error) {
	logf := opts.logf()
	auth, err := newAuthorizer(logf)
	if err != nil {
		return nil, err
	}
	if mode := opts.Bind; mode != "" && mode != "off" && mode != "localhost" {
		logf("remote: WARNING bind %q is overridden to localhost: this build carries the insecure "+
			"M1 bearer-key path (-tags lola_insecure) and must not put a shared secret on a network interface",
			logSafe(mode))
		opts.Bind = "localhost"
	}
	logf("remote: WARNING this daemon authenticates phones with a shared %s bearer key and no cryptography", InsecureKeyEnv)
	return listen(ctx, opts, auth)
}

// newAuthorizer builds M1's bearer-key authorizer, or refuses when the
// environment does not carry a usable key. The listener does not start without
// it: an empty key that authenticated everyone would be strictly worse than no
// listener at all.
func newAuthorizer(logf func(string, ...any)) (Authorizer, error) {
	key := os.Getenv(InsecureKeyEnv)
	switch {
	case key == "":
		return nil, fmt.Errorf("%w: set %s to a random secret of at least %d characters",
			ErrNoAuthorizer, InsecureKeyEnv, insecureMinKeyLen)
	case len(key) < insecureMinKeyLen:
		// The length, never the value.
		return nil, fmt.Errorf("%w: %s is %d characters; at least %d are required",
			ErrNoAuthorizer, InsecureKeyEnv, len(key), insecureMinKeyLen)
	}
	return &insecureAuthorizer{key: []byte(key), logf: logf}, nil
}

// insecureAuthorizer authenticates one in-band hello per connection. It is
// stateless and therefore safe for concurrent use.
type insecureAuthorizer struct {
	key  []byte
	logf func(string, ...any)
}

// Authenticate reads exactly one frame and requires it to be the hello.
//
// The comparison is constant time. The refusal says nothing about which part
// was wrong, and the key is never logged, never echoed and never included in an
// error: everything a failed attempt learns is that it failed.
func (a *insecureAuthorizer) Authenticate(ctx context.Context, hs *Handshake) (Peer, error) {
	a.logf("remote: WARNING accepting %s over the insecure M1 bearer-key path", hs.RemoteAddr)

	f, err := hs.NextFrame()
	if err != nil {
		return Peer{}, err
	}
	if f.Type != protocol.FrameReq || f.Cmd != helloCmd {
		ref := protocol.ErrorFrame(f.ID, protocol.CodeDenied, "authenticate first")
		_ = hs.Send(&ref)
		return Peer{}, ErrUnauthenticated
	}
	var hello insecureHello
	if err := f.DecodePayload(&hello); err != nil {
		ref := protocol.ErrorFrame(f.ID, protocol.CodeDenied, "authenticate first")
		_ = hs.Send(&ref)
		return Peer{}, ErrUnauthenticated
	}
	if subtle.ConstantTimeCompare([]byte(hello.Key), a.key) != 1 {
		ref := protocol.ErrorFrame(f.ID, protocol.CodeDenied, "authenticate first")
		_ = hs.Send(&ref)
		return Peer{}, fmt.Errorf("%w: bad %s", ErrUnauthenticated, InsecureKeyEnv)
	}

	// The acknowledgement is an ordinary resp on the hello's id, so the client
	// codec needs no special case and no frame type had to be invented for a
	// path that is being deleted in M2.
	ack := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameResp, ID: f.ID}
	ack.Payload, _ = json.Marshal(protocol.Response{OK: true})
	if err := hs.Send(&ack); err != nil {
		return Peer{}, err
	}
	return Peer{DeviceID: "insecure", Label: "insecure M1 client", ConnectedAt: hs.At, Insecure: true}, nil
}

// AuthorizeFrame permits everything the unconditional denials already let
// through. M1 has no device registry and therefore no capability tiers; M2
// replaces this whole file with the registry check, and the frame-by-frame
// shape is kept here so that the seam cannot rot before there is something to
// enforce.
func (a *insecureAuthorizer) AuthorizeFrame(context.Context, Peer, *protocol.Frame) error {
	return nil
}
