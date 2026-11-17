package hubproto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Request-signing headers. Every daemon request carries all four.
const (
	HeaderDevice    = "Lola-Device"
	HeaderTimestamp = "Lola-Timestamp"
	HeaderNonce     = "Lola-Nonce"
	HeaderSignature = "Lola-Signature"
)

// MaxSkew is how far a request's timestamp may be from the verifier's clock,
// in either direction.
const MaxSkew = 5 * time.Minute

// Sentinel errors returned (wrapped) by [Verify] and [ParseHeaders], so a hub
// can map them onto an [ErrorResponse] code.
var (
	ErrMissingHeader = errors.New("hubproto: missing signing header")
	ErrBadHeader     = errors.New("hubproto: malformed signing header")
	ErrSkew          = errors.New("hubproto: timestamp outside the allowed skew")
	ErrWrongDevice   = errors.New("hubproto: device id does not match the key")
	ErrBadSignature  = errors.New("hubproto: signature does not verify")
	ErrBadKey        = errors.New("hubproto: malformed public key")
)

var (
	deviceIDRe  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	timestampRe = regexp.MustCompile(`^[0-9]{1,12}$`)
	// A nonce is 16..64 url-safe characters. NewNonce produces 32 hex chars
	// (128 random bits).
	nonceRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)
	sigRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{86}$`) // 64 bytes, base64 raw-url
)

// GenerateKey creates a fresh ed25519 key pair from crypto/rand. It serves both
// device keys and policy-signing (admin) keys.
func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// EncodePublicKey renders a public key as unpadded base64url — the form used
// on the wire (EnrollRequest.PublicKey) and in config (trusted_policy_keys).
func EncodePublicKey(pub ed25519.PublicKey) string {
	return base64.RawURLEncoding.EncodeToString(pub)
}

// DecodePublicKey is the inverse of [EncodePublicKey]. It rejects padding,
// standard-alphabet characters and any length other than 32 bytes.
func DecodePublicKey(s string) (ed25519.PublicKey, error) {
	if len(s) != 43 {
		return nil, ErrBadKey
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, ErrBadKey
	}
	return ed25519.PublicKey(b), nil
}

// DeviceID derives a device's identifier from its public key: the first 32 hex
// characters (128 bits) of sha256(pub). Being derived, it cannot be chosen, and
// [Verify] checks the header against the key it verifies with.
func DeviceID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])[:32]
}

// ValidDeviceID reports whether s has the shape [DeviceID] produces.
func ValidDeviceID(s string) bool { return deviceIDRe.MatchString(s) }

// KeyIDFor derives a policy key id from an admin public key: "k" followed by
// the first 16 hex characters of sha256(pub). Admins may use any id matching
// the key-id pattern; this is the default the keygen helpers print.
func KeyIDFor(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "k" + hex.EncodeToString(sum[:])[:16]
}

// NewNonce returns 128 random bits as 32 hex characters.
//
// Nonce requirements: a nonce MUST be unique per request for the lifetime of
// the skew window (2×[MaxSkew]); the hub remembers every (device, nonce) pair
// it accepted for at least that long and rejects a repeat. Never derive a nonce
// from the timestamp or a counter that restarts with the process.
func NewNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// CanonicalString is the exact byte string a request signature covers:
//
//	METHOD \n path \n timestamp \n nonce \n hex(sha256(body))
//
// path is the request-target as sent: the escaped path plus "?query" when
// there is one (url.URL.RequestURI), so a query string cannot be altered.
// An empty body hashes as sha256 of zero bytes.
func CanonicalString(method, path, timestamp, nonce string, body []byte) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{method, path, timestamp, nonce, hex.EncodeToString(sum[:])}, "\n")
}

// Sign stamps req with the four signing headers for body (the exact bytes that
// will be sent; nil for no body), using the current time and a fresh nonce.
func Sign(req *http.Request, deviceID string, priv ed25519.PrivateKey, body []byte) error {
	nonce, err := NewNonce()
	if err != nil {
		return err
	}
	return SignAt(req, deviceID, priv, body, time.Now(), nonce)
}

// SignAt is [Sign] with the clock and nonce supplied (tests, deterministic
// replays).
func SignAt(req *http.Request, deviceID string, priv ed25519.PrivateKey, body []byte, now time.Time, nonce string) error {
	if len(priv) != ed25519.PrivateKeySize {
		return errors.New("hubproto: bad private key")
	}
	if !ValidDeviceID(deviceID) {
		return fmt.Errorf("%w: %s", ErrBadHeader, HeaderDevice)
	}
	if !nonceRe.MatchString(nonce) {
		return fmt.Errorf("%w: %s", ErrBadHeader, HeaderNonce)
	}
	ts := strconv.FormatInt(now.Unix(), 10)
	msg := CanonicalString(req.Method, req.URL.RequestURI(), ts, nonce, body)
	sig := ed25519.Sign(priv, []byte(msg))
	req.Header.Set(HeaderDevice, deviceID)
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderNonce, nonce)
	req.Header.Set(HeaderSignature, base64.RawURLEncoding.EncodeToString(sig))
	return nil
}

// SignedHeaders are a request's signing headers, shape-checked.
type SignedHeaders struct {
	Device    string
	Timestamp time.Time
	RawTime   string
	Nonce     string
	Signature []byte
}

// ParseHeaders reads and shape-checks the four signing headers. A hub calls it
// first to learn which device's key to load, then [Verify]. Exactly one value
// per header is accepted.
func ParseHeaders(h http.Header) (SignedHeaders, error) {
	var out SignedHeaders
	get := func(name string) (string, error) {
		v := h.Values(name)
		if len(v) == 0 || v[0] == "" {
			return "", fmt.Errorf("%w: %s", ErrMissingHeader, name)
		}
		if len(v) > 1 {
			return "", fmt.Errorf("%w: %s repeated", ErrBadHeader, name)
		}
		return v[0], nil
	}
	dev, err := get(HeaderDevice)
	if err != nil {
		return out, err
	}
	ts, err := get(HeaderTimestamp)
	if err != nil {
		return out, err
	}
	nonce, err := get(HeaderNonce)
	if err != nil {
		return out, err
	}
	sig, err := get(HeaderSignature)
	if err != nil {
		return out, err
	}
	if !deviceIDRe.MatchString(dev) {
		return out, fmt.Errorf("%w: %s", ErrBadHeader, HeaderDevice)
	}
	if !timestampRe.MatchString(ts) {
		return out, fmt.Errorf("%w: %s", ErrBadHeader, HeaderTimestamp)
	}
	if !nonceRe.MatchString(nonce) {
		return out, fmt.Errorf("%w: %s", ErrBadHeader, HeaderNonce)
	}
	if !sigRe.MatchString(sig) {
		return out, fmt.Errorf("%w: %s", ErrBadHeader, HeaderSignature)
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(sig)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return out, fmt.Errorf("%w: %s", ErrBadHeader, HeaderSignature)
	}
	sec, _ := strconv.ParseInt(ts, 10, 64)
	out = SignedHeaders{Device: dev, Timestamp: time.Unix(sec, 0), RawTime: ts, Nonce: nonce, Signature: raw}
	return out, nil
}

// Verify checks req's signature over body (the exact bytes read from it)
// against pub at time now: headers well formed, the device header equal to
// DeviceID(pub), the timestamp within ±[MaxSkew], and the ed25519 signature
// over [CanonicalString]. It does NOT check the nonce for replay — that is the
// hub's job, using the returned Nonce, after Verify succeeds.
func Verify(req *http.Request, body []byte, pub ed25519.PublicKey, now time.Time) (SignedHeaders, error) {
	sh, err := ParseHeaders(req.Header)
	if err != nil {
		return sh, err
	}
	if len(pub) != ed25519.PublicKeySize {
		return sh, ErrBadKey
	}
	if sh.Device != DeviceID(pub) {
		return sh, ErrWrongDevice
	}
	d := now.Sub(sh.Timestamp)
	if d > MaxSkew || d < -MaxSkew {
		return sh, ErrSkew
	}
	msg := CanonicalString(req.Method, req.URL.RequestURI(), sh.RawTime, sh.Nonce, body)
	if !ed25519.Verify(pub, []byte(msg), sh.Signature) {
		return sh, ErrBadSignature
	}
	return sh, nil
}
