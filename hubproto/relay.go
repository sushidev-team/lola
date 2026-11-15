package hubproto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// Relay wire: the paths, headers, identifiers, rendezvous registration and
// control-channel messages the hub relay, the daemon and the phone share. The
// relay carries an inner TLS stream it cannot read; nothing here names a
// session, path, device name or command.

const (
	PathRelayDaemon     = "/v1/relay/daemon"     // GET ?v=1, WS upgrade, signed    -> control channel
	PathRelayAccept     = "/v1/relay/accept"     // GET ?conn=<id>, WS upgrade, signed -> daemon data leg
	PathRelayPhone      = "/v1/relay/phone"      // GET, WS upgrade, bearer           -> phone data leg
	PathRelayRendezvous = "/v1/relay/rendezvous" // POST RendezvousRequest, signed    -> RendezvousResponse

	// The rendezvous id and bearer ride custom headers, never the query
	// (proxies log the request line) and never Authorization (the URL loading
	// system may replace it).
	HeaderRelayRV     = "Lola-Relay-Rv"     // phone leg: rendezvous id
	HeaderRelayBearer = "Lola-Relay-Bearer" // phone leg: bearer (or pairing token)
	HeaderRelayCode   = "Lola-Relay-Code"   // every non-101 phone-leg answer: the hubproto Code
	QueryRelayConn    = "conn"              // accept leg; in the query so Sign covers it
	QueryRelayVersion = "v"                 // control leg; "1"
)

// Error codes the relay adds to the hub's message map.
const (
	CodeOffline      = "offline"       // 503 "daemon not connected"
	CodeBusy         = "busy"          // 503 "relay busy"
	CodeRelayTimeout = "relay_timeout" // 504 "daemon did not answer"
)

// WebSocket close codes the hub sends on a relay leg.
const (
	RelayCloseReplaced = 4000 // a newer control channel for this device
	RelayCloseRevoked  = 4001 // device revoked or a paired rendezvous removed
	RelayCloseIdle     = 4002 // RelayIdleTimeout with no payload either way
	RelayCloseQuota    = 4003 // daily byte quota (device or rv) exhausted
	RelayClosePeerGone = 4004 // the other leg ended, or this leg arrived after the phone left
)

// Limits. Each names its enforcer in the relay spec (03 section 3.6).
const (
	RelayControlVersion = 1

	RelayMaxControlMessage = 4 << 10

	RelayPingInterval = 25 * time.Second
	RelayDeadAfter    = 60 * time.Second
	RelayWriteStall   = 30 * time.Second

	RelayAcceptTimeout       = 6 * time.Second
	RelayPhoneAttemptTimeout = 8 * time.Second // must exceed RelayAcceptTimeout
	// A 401 within this long after the phone stored a new bearer is a backoff,
	// not a dead credential: the hub may not have the new hash yet.
	RelayFreshCredentialGrace = 60 * time.Second

	RelayIdleTimeout    = 30 * time.Minute
	RelayHalfCloseGrace = 2 * time.Second

	RelaySpliceBytesPerSec   = 2 << 20
	RelaySpliceBurst         = 4 << 20
	RelayDeviceBytesPerSec   = 4 << 20
	RelayOrgBytesPerSec      = 16 << 20
	RelayDailyBytesPerDevice = 4 << 30
	RelayDailyBytesPerRV     = 1 << 30

	RelayMaxConnsPerDevice = 4
	RelayMaxConnsPerRV     = 2
	RelayMaxConnsPerOrg    = 64
	RelayMaxConnsHub       = 512

	// 16 phones (devices.MaxDevices) plus one open pairing window.
	RelayMaxRendezvousPerDevice = 17

	RelayDialRatePerRV   = 0.2
	RelayDialBurstPerRV  = 3
	RelayDialRateAuth    = 100
	RelayDialBurstAuth   = 200
	RelayDialRateUnauth  = 20
	RelayDialBurstUnauth = 50
	RelayDialRatePerIP   = 1
	RelayDialBurstPerIP  = 20

	RelayPairingTTLMax      = 10 * time.Minute
	RelayPrevBearerGraceMax = 7 * 24 * time.Hour
	RelayPrevBearerGrace    = 6 * 24 * time.Hour
	RelayBearerMaxAge       = 30 * 24 * time.Hour

	RelayRecheckInterval   = 60 * time.Second
	RelayBackoffMin        = 1 * time.Second
	RelayBackoffMax        = 5 * time.Minute
	RelayBackoffResetAfter = 60 * time.Second
	RelayRateLimitedFloor  = 60 * time.Second

	RelayUnsupportedRecheck     = 6 * time.Hour
	RelayPhoneUnauthorizedPause = 1 * time.Hour
)

// Identifiers.

// NewRelayID returns 128 random bits as 32 lowercase hex characters.
func NewRelayID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// NewRelayBearer returns 256 random bits as 43 base64url characters plus the
// hash the hub stores. It also mints a pairing window's token.
func NewRelayBearer() (bearer, hash string, err error) {
	var b [32]byte
	if _, err = rand.Read(b[:]); err != nil {
		return "", "", err
	}
	bearer = base64.RawURLEncoding.EncodeToString(b[:])
	return bearer, RelayBearerHash(bearer), nil
}

// RelayBearerHash is hex(sha256(bearer ASCII)), the only form the hub stores.
func RelayBearerHash(bearer string) string {
	sum := sha256.Sum256([]byte(bearer))
	return hex.EncodeToString(sum[:])
}

// ValidRelayID reports whether s is 32 lowercase hex characters.
func ValidRelayID(s string) bool { return len(s) == 32 && isLowerHex(s) }

// ValidRelayHash reports whether s is 64 lowercase hex characters.
func ValidRelayHash(s string) bool { return len(s) == 64 && isLowerHex(s) }

// ValidRelayBearer reports whether s is 43 base64url characters.
func ValidRelayBearer(s string) bool {
	if len(s) != 43 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Rendezvous registration (signed POST, replace-all).

// RelayRendezvous is one entry of the device's rendezvous set.
type RelayRendezvous struct {
	RV           string    `json:"rv"`
	BearerSHA256 string    `json:"bearer_sha256"`
	PrevSHA256   string    `json:"prev_sha256,omitempty"` // previous bearer during rotation
	PrevUntil    time.Time `json:"prev_until,omitzero"`   // required iff PrevSHA256 != ""
	ExpiresAt    time.Time `json:"expires_at,omitzero"`   // pairing windows only; marks the entry a WINDOW
}

// RendezvousRequest is the device's WHOLE set: entries the hub holds for the
// device that are not in it are deleted. It may be empty (drop all).
type RendezvousRequest struct {
	Entries []RelayRendezvous `json:"entries"`
}

// RendezvousResponse answers a registration.
type RendezvousResponse struct {
	Registered int      `json:"registered"`          // entries stored
	Conflicts  []string `json:"conflicts,omitempty"` // rvs of THIS request refused: owned by another device
}

// Validate checks shapes and bounds. There is no lower bound on PrevUntil or
// ExpiresAt: an already-past value stores an entry that is simply not live.
// Errors name the field, never the value.
func (r RendezvousRequest) Validate(now time.Time) error {
	if len(r.Entries) > RelayMaxRendezvousPerDevice {
		return errors.New("hubproto: entries: too many")
	}
	seen := make(map[string]struct{}, len(r.Entries))
	for _, e := range r.Entries {
		if !ValidRelayID(e.RV) {
			return errors.New("hubproto: rv: invalid")
		}
		if _, dup := seen[e.RV]; dup {
			return errors.New("hubproto: rv: duplicate")
		}
		seen[e.RV] = struct{}{}
		if !ValidRelayHash(e.BearerSHA256) {
			return errors.New("hubproto: bearer_sha256: invalid")
		}
		if e.PrevSHA256 != "" {
			if !ValidRelayHash(e.PrevSHA256) {
				return errors.New("hubproto: prev_sha256: invalid")
			}
			if e.PrevSHA256 == e.BearerSHA256 {
				return errors.New("hubproto: prev_sha256: equals bearer_sha256")
			}
			if e.PrevUntil.IsZero() {
				return errors.New("hubproto: prev_until: required with prev_sha256")
			}
			if e.PrevUntil.After(now.Add(RelayPrevBearerGraceMax + MaxSkew)) {
				return errors.New("hubproto: prev_until: too far ahead")
			}
		} else if !e.PrevUntil.IsZero() {
			return errors.New("hubproto: prev_until: set without prev_sha256")
		}
		if !e.ExpiresAt.IsZero() && e.ExpiresAt.After(now.Add(RelayPairingTTLMax+MaxSkew)) {
			return errors.New("hubproto: expires_at: too far ahead")
		}
	}
	return nil
}

// Control-channel messages: one FIN binary frame, one JSON object, at most
// RelayMaxControlMessage. Decode leniently with encoding/json (unknown fields
// ignored), then switch on T. An unknown T is ignored; a known T whose known
// fields fail Validate is a protocol error (close 1002).
type RelayMsg struct {
	T      string `json:"t"`
	V      int    `json:"v,omitempty"`         // hello
	PingS  int    `json:"ping_s,omitempty"`    // hello: display only
	Max    int    `json:"max_conns,omitempty"` // hello: display only
	Conn   string `json:"conn,omitempty"`      // incoming, cancel, reject
	RV     string `json:"rv,omitempty"`        // incoming
	Reason string `json:"reason,omitempty"`    // reject, bye
}

// ErrRelayMsg is returned by RelayMsg.Validate for a known type whose known
// fields are invalid.
var ErrRelayMsg = errors.New("hubproto: invalid relay message")

// Validate applies the per-type rules. An unknown T passes (it is ignored by
// the receiver). A hello with another version is reported as an error too: the
// receiver closes and records "unsupported".
func (m RelayMsg) Validate() error {
	switch m.T {
	case "hello":
		if m.V != RelayControlVersion {
			return ErrRelayMsg
		}
	case "incoming":
		if !ValidRelayID(m.Conn) || !ValidRelayID(m.RV) {
			return ErrRelayMsg
		}
	case "cancel":
		if !ValidRelayID(m.Conn) {
			return ErrRelayMsg
		}
	case "reject":
		if !ValidRelayID(m.Conn) {
			return ErrRelayMsg
		}
		switch m.Reason {
		case "busy", "unknown", "off":
		default:
			return ErrRelayMsg
		}
	}
	return nil
}
