package remote

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// The pairing QR payload: "lola2." + base64url(JSON), no padding.
//
// The prefix carries the version so a scanner can say "that is not a Lola
// pairing code" instead of "corrupt"; the version is repeated inside the blob.
// The secret in the payload is single-use and burned on the first handshake; it
// never appears in an error, so a failed decode of a camera frame cannot leak
// it into a log.
const (
	PairPayloadPrefix   = "lola2."
	PairPayloadVersion  = 2
	MaxPairPayloadBytes = 1024 // whole string, prefix included
	maxPayloadAddrs     = 8
)

// ErrNotPairPayload marks a string that is not a lola2 pairing payload at all
// (including the retired "lola1." connect code).
var ErrNotPairPayload = errors.New("remote: not a lola pairing code")

// PairPayload is what a phone needs to start a pairing, and nothing else.
// Field names are short (optical bandwidth) and frozen: the phone pins them.
type PairPayload struct {
	V      int          `json:"v"`               // 2
	Pin    string       `json:"pin"`             // daemon SPKI SHA-256, std base64 (44 chars)
	Addrs  []string     `json:"addrs,omitempty"` // IP literals, no zone ids, at most 8
	Port   int          `json:"port,omitempty"`  // 1..65535 when Addrs is non-empty
	Secret string       `json:"secret"`          // base64url (no pad) of 32 random bytes
	Relay  *PairRelayQR `json:"relay,omitempty"`
}

// PairRelayQR is the hub path of a payload. Tok is a window token: the hub
// holds only its hash and it proves nothing to the daemon.
type PairRelayQR struct {
	URL string `json:"url"` // hub base URL, https
	RV  string `json:"rv"`  // rendezvous id, 32 lowercase hex
	Tok string `json:"tok"` // 43 base64url chars (256 random bits)
}

var (
	payloadRVRe  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	payloadTokRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

// EncodePairPayload validates p and renders it. Errors never carry p.
func EncodePairPayload(p PairPayload) (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", errors.New("remote: pairing payload cannot be encoded")
	}
	s := PairPayloadPrefix + base64.RawURLEncoding.EncodeToString(raw)
	if len(s) > MaxPairPayloadBytes {
		return "", fmt.Errorf("remote: pairing payload is %d bytes, limit %d", len(s), MaxPairPayloadBytes)
	}
	return s, nil
}

// DecodePairPayload parses and validates a scanned string. Unknown JSON fields
// are ignored so a newer daemon can add one without breaking an older phone.
func DecodePairPayload(s string) (PairPayload, error) {
	if !strings.HasPrefix(s, PairPayloadPrefix) {
		return PairPayload{}, ErrNotPairPayload
	}
	if len(s) > MaxPairPayloadBytes {
		return PairPayload{}, fmt.Errorf("remote: pairing payload is %d bytes, limit %d", len(s), MaxPairPayloadBytes)
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(s[len(PairPayloadPrefix):])
	if err != nil {
		return PairPayload{}, errors.New("remote: pairing payload is not valid base64url")
	}
	var p PairPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return PairPayload{}, errors.New("remote: pairing payload is not valid JSON")
	}
	if err := p.validate(); err != nil {
		return PairPayload{}, err
	}
	return p, nil
}

func (p PairPayload) validate() error {
	if p.V != PairPayloadVersion {
		return fmt.Errorf("remote: pairing payload version %d is not %d", p.V, PairPayloadVersion)
	}
	if b, err := base64.StdEncoding.DecodeString(p.Pin); err != nil || len(b) != 32 {
		return errors.New("remote: pairing payload pin must be a base64 SHA-256")
	}
	if b, err := base64.RawURLEncoding.Strict().DecodeString(p.Secret); err != nil || len(b) != 32 {
		return errors.New("remote: pairing payload secret must be 32 base64url bytes")
	}
	if len(p.Addrs) == 0 && p.Relay == nil {
		return errors.New("remote: pairing payload names no address and no relay")
	}
	if len(p.Addrs) > maxPayloadAddrs {
		return fmt.Errorf("remote: pairing payload has more than %d addresses", maxPayloadAddrs)
	}
	for _, a := range p.Addrs {
		if strings.Contains(a, "%") || net.ParseIP(a) == nil {
			return errors.New("remote: pairing payload address is not an IP literal")
		}
	}
	if len(p.Addrs) > 0 && (p.Port < 1 || p.Port > 65535) {
		return errors.New("remote: pairing payload port is out of range")
	}
	if len(p.Addrs) == 0 && p.Port != 0 {
		return errors.New("remote: pairing payload has a port but no address")
	}
	if r := p.Relay; r != nil {
		u, err := url.Parse(r.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return errors.New("remote: pairing payload relay url must be https")
		}
		if !payloadRVRe.MatchString(r.RV) {
			return errors.New("remote: pairing payload relay rv is malformed")
		}
		if !payloadTokRe.MatchString(r.Tok) {
			return errors.New("remote: pairing payload relay tok is malformed")
		}
	}
	return nil
}
