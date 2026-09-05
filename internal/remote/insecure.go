//go:build lola_insecure

package remote

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sushidev-team/lola/internal/protocol"
)

// M1's authentication, and everything about it is temporary by construction.
//
// There is no cryptography in M1: the peer proves itself with a bearer key.
// "Deleted, not disabled, in M2" is a promise enforced by memory, so it is
// enforced by the compiler instead — this file only exists under
// //go:build lola_insecure, and insecure_off.go is what a release binary gets.
//
// The key is GENERATED AND PERSISTED rather than demanded from the environment.
// An env-var-only key made the listener silently disappear on any restart that
// did not carry it — which is every restart the TUI's ^r and the desktop app's
// restart button perform, since neither inherits the shell that started the
// first one. The failure then looked like a bind problem and was not one. So
// the key lives at <Dir>/remote.key beside device.key, survives a restart, and
// InsecureKeyEnv remains as an override for whoever wants to supply their own.
//
// Two rails hold while the tag is active:
//
//   - Listen FORCES the bind to loopback whatever [remote].bind says, UNLESS
//     the operator ALSO sets LOLA_REMOTE_INSECURE_LAN — see InsecureLANEnv,
//     which exists for physical-device testing and is the one documented hole
//     in this rail. A shared secret must never reach a network interface by
//     ACCIDENT; for the Simulator it never has to, since a Simulator shares the
//     Mac's loopback and 127.0.0.1 puts nothing on the wire at all.
//   - Every accept logs a warning. A daemon running this path should be
//     impossible to forget about.

// InsecureKeyEnv names an environment variable that OVERRIDES the generated
// key. It is read once at startup and never appears in argv (ps reads argv), in
// a log line, or in an error.
const InsecureKeyEnv = "LOLA_REMOTE_INSECURE_KEY"

// InsecureLANEnv names an environment variable that opens the bind rail for one
// run, without touching config. [remote].insecure_lan is the normal way — see
// config.RemoteConfig.InsecureLAN for why the permission has to persist — and
// this remains for a one-off: a single `lola run` bound to the LAN, gone the
// moment that process exits and leaving nothing behind to forget about.
//
// Either source opens it, and both still require a [remote].bind naming
// something other than loopback. Neither alone changes anything.
const InsecureLANEnv = "LOLA_REMOTE_INSECURE_LAN"

// insecureKeyFile is the generated key's name, alongside device.key in the same
// 0700 directory and with the same 0600 mode.
const insecureKeyFile = "remote.key"

// insecureLANAllowed reports whether the bind rail was opened, by config or for
// this run. Anything but an explicit affirmative in the environment reads as
// "no": a variable that is merely PRESENT — exported empty by a shell profile,
// say — must not open a listener.
func insecureLANAllowed(opts Options) bool {
	if opts.InsecureLAN {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(InsecureLANEnv))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

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
	auth, err := newAuthorizer(opts.Dir, logf)
	if err != nil {
		return nil, err
	}
	if mode := opts.Bind; mode != "" && mode != "off" && mode != "localhost" {
		if insecureLANAllowed(opts) {
			// Both halves were deliberate: the config names a non-loopback bind
			// AND the opt-in is set. Neither alone reaches here.
			logf("remote: WARNING bind %q is honoured because remote.insecure_lan is set. The shared "+
				"bearer key now crosses your network in the clear, and anything that can reach this "+
				"port and guess the key can type into a running coding agent. Use it on a network you "+
				"control, while you are testing, and not longer.", logSafe(mode))
		} else {
			logf("remote: WARNING bind %q is overridden to localhost: this build carries the insecure "+
				"M1 bearer-key path (-tags lola_insecure) and must not put a shared secret on a network "+
				"interface by accident. Set remote.insecure_lan = true to allow it, which is how a "+
				"physical phone reaches this daemon.", logSafe(mode))
			opts.Bind = "localhost"
		}
	}
	logf("remote: WARNING this daemon authenticates phones with a shared bearer key and no cryptography")
	return listen(ctx, opts, auth)
}

// newAuthorizer builds M1's bearer-key authorizer.
//
// The key is resolved in one order and the order matters: an explicit
// InsecureKeyEnv wins, so an operator who wants to supply their own still can
// and nothing on disk overrides them; otherwise the generated key at
// <dir>/remote.key is loaded, and if there is none, one is created. The
// listener no longer refuses to start merely because nobody exported a
// variable — that failure was silent, looked like a bind problem, and cost more
// than it protected.
func newAuthorizer(dir string, logf func(string, ...any)) (Authorizer, error) {
	if key := os.Getenv(InsecureKeyEnv); key != "" {
		if len(key) < insecureMinKeyLen {
			// The length, never the value.
			return nil, fmt.Errorf("%w: %s is %d characters; at least %d are required",
				ErrNoAuthorizer, InsecureKeyEnv, len(key), insecureMinKeyLen)
		}
		return &insecureAuthorizer{key: []byte(key), logf: logf}, nil
	}
	key, err := loadOrCreateInsecureKey(dir, logf)
	if err != nil {
		return nil, err
	}
	return &insecureAuthorizer{key: []byte(key), logf: logf}, nil
}

// loadOrCreateInsecureKey reads <dir>/remote.key, creating it when absent.
//
// A key already on disk is returned whatever its length: it was generated here
// at the right size, and refusing to start over a file an operator has edited
// is worse than honouring it — the minimum exists to stop a WEAK key being
// chosen deliberately, and this path chooses nothing.
//
// The write is temp+rename at 0600 inside the 0700 home, the same discipline
// session.Store uses, so a crash mid-write cannot leave a truncated key that
// authenticates nobody and cannot be told from a corrupted one.
func loadOrCreateInsecureKey(dir string, logf func(string, ...any)) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("%w: no lola home directory to keep a key in", ErrNoAuthorizer)
	}
	path := filepath.Join(dir, insecureKeyFile)
	switch b, err := os.ReadFile(path); {
	case err == nil:
		if key := strings.TrimSpace(string(b)); key != "" {
			return key, nil
		}
		// An empty file is not a key. Fall through and write a real one rather
		// than authenticating everyone with "".
	case !os.IsNotExist(err):
		return "", fmt.Errorf("%w: reading %s: %w", ErrNoAuthorizer, insecureKeyFile, err)
	}

	raw := make([]byte, 16) // 32 hex characters, comfortably over the minimum
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("%w: generating a key: %w", ErrNoAuthorizer, err)
	}
	key := hex.EncodeToString(raw)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoAuthorizer, err)
	}
	tmp, err := os.CreateTemp(dir, insecureKeyFile+".*")
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoAuthorizer, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", fmt.Errorf("%w: %w", ErrNoAuthorizer, err)
	}
	if _, err := tmp.WriteString(key); err != nil {
		tmp.Close()
		return "", fmt.Errorf("%w: %w", ErrNoAuthorizer, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoAuthorizer, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoAuthorizer, err)
	}
	// The path, never the value.
	logf("remote: generated a new phone bearer key at %s", path)
	return key, nil
}

// RegenerateInsecureKey replaces the stored key, invalidating every phone that
// holds the old one. It is the closest thing M1 has to revocation, which is why
// it exists before M2 brings the real one: a key that can never be rolled is a
// key that is shared forever, including with whoever once borrowed the QR.
//
// It does NOT touch a listener that is already running — the caller restarts
// the daemon, and the reload path rebuilds the authorizer — so a phone stays
// connected until then. Saying otherwise would promise a cut-off this cannot
// deliver.
func RegenerateInsecureKey(dir string, logf func(string, ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if dir == "" {
		return fmt.Errorf("%w: no lola home directory", ErrNoAuthorizer)
	}
	if err := os.Remove(filepath.Join(dir, insecureKeyFile)); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, err := loadOrCreateInsecureKey(dir, logf)
	return err
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

// InsecureKey returns M1's shared bearer key, or "" when this listener is not
// authenticating with one.
//
// It exists for exactly one caller: the daemon's pairBegin handler, which puts
// the key into a connect code so a phone can scan it instead of a human copying
// it. That is a deliberate exception to this package's rule that the key never
// leaves it, and it is bounded three ways. The method is tag-split, so an
// untagged binary returns "" and physically cannot answer. The reply travels
// over ~/.lola/lola.sock, which is srw------- inside a 0700 directory, and
// anything that can read it already reaches cmd=answer — a strict superset of
// what the key grants — so handing it back adds no privilege to that caller.
// And the value still never reaches a log line, an error or argv: the rule that
// changed is "no caller", not "no discipline".
//
// The type assertion is the honest test. Whether this listener authenticates
// with a bearer key is a property of the authorizer it was built with, not of
// the build tag alone, and a Server assembled some other way must answer "no".
func (s *Server) InsecureKey() string {
	a, ok := s.auth.(*insecureAuthorizer)
	if !ok {
		return ""
	}
	return string(a.key)
}
