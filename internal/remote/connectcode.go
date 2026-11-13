package remote

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The M1 connect code: the string a desktop renders as a QR and a phone scans
// instead of a human copying four values by hand.
//
// This file is deliberately UNTAGGED even though the only thing that can fill a
// ConnectCode is the lola_insecure build. The FORMAT is pure text with no
// secret in it — a struct, a JSON encoding and a prefix — and keeping the codec
// out of the tag means its tests run in every build and the golden vector below
// cannot rot in a build nobody compiles. What is tag-gated is the KEY, which
// lives in insecure.go and has no way in here except through a caller.
//
// # Why this is a token and not a URL
//
// mobile/PLAN.md settles it for M2's pairing payload and the argument transfers
// to M1 unchanged, so it is restated rather than re-derived: a custom URL scheme
// cannot be claimed exclusively on either platform, and people routinely scan a
// QR with the SYSTEM CAMERA rather than an in-app scanner — at which point the
// OS hands the payload to whichever app registered the scheme. M1's bearer key
// is a WORSE thing to hand over than the M2 secret that argument was written
// about: it has no TTL, it is not single-use, and it is not zeroed after one
// handshake. So the code is an opaque token, the system camera produces nothing
// actionable from it, and the key is never given to the OS URL router.
//
// # The prefix is the version, and that is what keeps M1 and M2 apart
//
// M2's pairing token is a different blob behind the same scheme: it carries a
// qr_secret and a pairing id and no bearer key at all. Two payloads behind one
// undifferentiated prefix is how a scanner ends up decoding a blob whose fields
// it does not have — an M2 client would find no "s", an M1 client no "key" —
// and "this is the old code" would be indistinguishable from a corrupt scan.
// The digit answers that: the mobile decoder matches lola(\d+)\. and refuses
// anything but its own, so M2 becomes "lola2." and each side can say which
// milestone it is looking at. The version is ALSO inside the blob, so a decoder
// handed the bytes without the prefix still knows what it is holding.
const connectCodePrefix = "lola1."

// ConnectCodeVersion is the payload version inside the token. It is carried in
// the blob as well as implied by the prefix, so a decoder that was handed the
// bytes without the prefix still knows what it is holding.
const ConnectCodeVersion = 1

// maxConnectCodeBytes bounds a token this package will DECODE. The encoder
// cannot produce anything near it; the cap exists because Decode is the half a
// phone runs over camera input, and an unbounded base64 body is an unbounded
// allocation driven by whatever was pointed at the lens. The real ceiling is
// optical anyway: a QR that carries more than this is not one a phone reads.
const maxConnectCodeBytes = 4096

// ErrNotConnectCode is returned by DecodeConnectCode for a string that is not
// one of these tokens at all — the case a scanner reports as "that is not a
// Lola code" rather than as a corrupt one.
var ErrNotConnectCode = errors.New("remote: not a lola connect code")

// ConnectCode is what a phone needs to reach this daemon, and nothing else.
//
// Field names are short because every byte is optical bandwidth, and they are
// FROZEN: the mobile decoder pins them, so renaming one is a wire break.
//
// Pin is standard base64 WITH padding — 44 characters ending in "=" — because
// that is what DeviceKey.SPKIPin already returns, what the daemon already logs
// at startup, and what the mobile connect form's own validator already accepts.
// mobile/PLAN.md's M2 payload specifies base64url instead; that is a different
// encoding of the same 32 bytes and M2 may have it, but M1 must be
// byte-identical to the value already in circulation or a code and a log line
// would disagree about the same daemon.
type ConnectCode struct {
	V int `json:"v"`

	// Addrs is every address the listener actually bound, in bind order. The
	// FIRST is the one to dial; the rest are mobile/PLAN.md's address book,
	// which exists because a single frozen address bricks the app the first
	// time a DHCP lease changes.
	//
	// A list rather than one host even when the daemon binds one interface,
	// because a decoder that has to guess whether it was handed a string or an
	// array is a decoder with two paths through it.
	Addrs []string `json:"addrs"`

	Port int `json:"port"`

	// Pin is DeviceKey.SPKIPin: the value the client pins the TLS key to.
	Pin string `json:"pin"`

	// Key is M1's shared bearer secret. It is the reason everything that
	// touches this struct is handled as a secret: it is never logged, never put
	// in an error, and never written anywhere but the response that was asked
	// for it.
	Key string `json:"key"`
}

// EncodeConnectCode renders a code as the scannable token.
//
// The body is base64url WITHOUT padding: a QR's byte mode encodes any byte, so
// the alphabet is not chosen for the code — it is chosen so the token survives
// being pasted through a URL, a shell argument or a log-scraping README without
// a character being eaten, which is exactly what a human debugging a failed
// scan will do with it.
func EncodeConnectCode(c ConnectCode) (string, error) {
	if c.V == 0 {
		c.V = ConnectCodeVersion
	}
	switch {
	case len(c.Addrs) == 0 || c.Addrs[0] == "":
		return "", errors.New("remote: connect code needs at least one address")
	case c.Port <= 0 || c.Port > 65535:
		return "", fmt.Errorf("remote: connect code port %d is out of range", c.Port)
	case c.Pin == "":
		return "", errors.New("remote: connect code needs an SPKI pin")
	case c.Key == "":
		// Named without the value, like every other refusal in this package.
		return "", errors.New("remote: connect code needs a bearer key")
	}
	blob, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("remote: encoding connect code: %w", err)
	}
	return connectCodePrefix + base64.RawURLEncoding.EncodeToString(blob), nil
}

// DecodeConnectCode parses a token. It exists so the format has exactly one
// definition that a test can round-trip and a golden vector can pin — the phone
// implements this in TypeScript, and a second implementation with no shared
// fixture is how two encodings of the same thing diverge.
//
// It fails closed on everything: a wrong prefix, a body that is not base64url,
// a blob that is not this shape, a version it does not know, and a field that
// is missing or out of range. A half-valid code that connected somewhere
// unintended is strictly worse than one the scanner refuses.
func DecodeConnectCode(s string) (ConnectCode, error) {
	s = strings.TrimSpace(s)
	if len(s) > maxConnectCodeBytes {
		return ConnectCode{}, fmt.Errorf("%w: %d bytes is longer than any code this daemon writes", ErrNotConnectCode, len(s))
	}
	body, ok := strings.CutPrefix(s, connectCodePrefix)
	if !ok {
		return ConnectCode{}, ErrNotConnectCode
	}
	blob, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return ConnectCode{}, fmt.Errorf("%w: body is not base64url", ErrNotConnectCode)
	}
	var c ConnectCode
	dec := json.NewDecoder(strings.NewReader(string(blob)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return ConnectCode{}, fmt.Errorf("%w: body is not a connect code", ErrNotConnectCode)
	}
	switch {
	case c.V != ConnectCodeVersion:
		return ConnectCode{}, fmt.Errorf("%w: version %d, this build speaks %d", ErrNotConnectCode, c.V, ConnectCodeVersion)
	case len(c.Addrs) == 0 || c.Addrs[0] == "":
		return ConnectCode{}, fmt.Errorf("%w: no address", ErrNotConnectCode)
	case c.Port <= 0 || c.Port > 65535:
		return ConnectCode{}, fmt.Errorf("%w: port out of range", ErrNotConnectCode)
	case c.Pin == "":
		return ConnectCode{}, fmt.Errorf("%w: no pin", ErrNotConnectCode)
	case c.Key == "":
		return ConnectCode{}, fmt.Errorf("%w: no key", ErrNotConnectCode)
	}
	return c, nil
}
