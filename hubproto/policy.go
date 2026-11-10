package hubproto

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Policy is a team settings bundle, signed by an admin key that each daemon
// pins locally ([hub].trusted_policy_keys). The hub only stores and routes it;
// it holds no signing key, so it cannot forge or alter one.
type Policy struct {
	Org       string    `json:"org"`
	Version   int       `json:"version"` // monotonically increasing per org
	IssuedAt  time.Time `json:"issued_at"`
	Settings  Settings  `json:"settings"`
	KeyID     string    `json:"key_id"`
	Signature string    `json:"signature,omitempty"` // base64 raw-url ed25519
}

// Settings is the CLOSED set of team-settable keys. Every one is
// NON-executing: nothing here is a shell line, a command, an image or a path.
// post_create, pre_remove, dev_commands, custom-cli commands and sandbox images
// must never be added — a team-pushed shell line is remote code execution on
// every member. Unknown keys fail [DecodePolicy], so an executing key cannot
// ride along unnoticed either.
//
// Pointer and omitempty fields mean "the team sets nothing here".
type Settings struct {
	// ReviewProviders: the review provider kinds the team enables. custom-cli
	// is deliberately not settable — its meaning is a local shell command.
	ReviewProviders []string `json:"review_providers,omitempty"`
	// BudgetDailyWeightedTokens: daily spend cap in weighted tokens; 0 = unset.
	BudgetDailyWeightedTokens int64 `json:"budget_daily_weighted_tokens,omitempty"`
	// MergeQueueEnabled: [merge_queue].enabled; nil = unset.
	MergeQueueEnabled *bool `json:"merge_queue_enabled,omitempty"`
	// MaxConcurrencyHint: suggested global_cap; 0 = unset.
	MaxConcurrencyHint int `json:"max_concurrency_hint,omitempty"`
	// AcceptKinds: chore kinds members should accept.
	AcceptKinds []Kind `json:"accept_kinds,omitempty"`
	// EscalateAfter: failed attempts before an item escalates; 0 = unset.
	EscalateAfter int `json:"escalate_after,omitempty"`
}

// PolicyReviewProviders are the review provider kinds a policy may enable.
func PolicyReviewProviders() []string {
	return []string{"claude-session", "codex-session", "opencode-session", "coderabbit-cli", "coderabbit-watch", "bot-watch"}
}

// Bounds on numeric settings.
const (
	MaxPolicyConcurrency   = 64
	MaxPolicyEscalateAfter = 20
	MaxPolicyBudget        = int64(1) << 40
)

// Validate checks every settings value.
func (s Settings) Validate() error {
	allowed := map[string]bool{}
	for _, k := range PolicyReviewProviders() {
		allowed[k] = true
	}
	seen := map[string]bool{}
	for _, k := range s.ReviewProviders {
		if !allowed[k] {
			return invalid("settings.review_providers", "kind not settable by policy")
		}
		if seen[k] {
			return invalid("settings.review_providers", "duplicate")
		}
		seen[k] = true
	}
	if s.BudgetDailyWeightedTokens < 0 || s.BudgetDailyWeightedTokens > MaxPolicyBudget {
		return invalid("settings.budget_daily_weighted_tokens", "out of range")
	}
	if s.MaxConcurrencyHint < 0 || s.MaxConcurrencyHint > MaxPolicyConcurrency {
		return invalid("settings.max_concurrency_hint", "out of range")
	}
	seenK := map[Kind]bool{}
	for _, k := range s.AcceptKinds {
		if !k.Valid() {
			return invalid("settings.accept_kinds", "unknown kind")
		}
		if seenK[k] {
			return invalid("settings.accept_kinds", "duplicate")
		}
		seenK[k] = true
	}
	if s.EscalateAfter < 0 || s.EscalateAfter > MaxPolicyEscalateAfter {
		return invalid("settings.escalate_after", "out of range")
	}
	return nil
}

var keyIDRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// ValidKeyID reports whether s is an acceptable policy key id.
func ValidKeyID(s string) bool { return keyIDRe.MatchString(s) }

// Policy verification errors.
var (
	ErrUntrustedKey     = errors.New("hubproto: policy key id is not trusted")
	ErrPolicySignature  = errors.New("hubproto: policy signature does not verify")
	ErrPolicyMalformed  = errors.New("hubproto: malformed policy")
	errPolicyNoTrustSet = errors.New("hubproto: no trusted policy keys")
)

// policyDomain separates policy signatures from request signatures, so a
// signature made for one can never verify as the other.
const policyDomain = "lola-policy-v1\n"

// validateHeader checks everything but the signature and settings.
func (p Policy) validateHeader() error {
	if !ValidOrg(p.Org) {
		return invalid("org", "bad org")
	}
	if p.Version <= 0 {
		return invalid("version", "must be positive")
	}
	if p.IssuedAt.IsZero() {
		return invalid("issued_at", "missing")
	}
	if !ValidKeyID(p.KeyID) {
		return invalid("key_id", "bad key id")
	}
	return nil
}

// CanonicalPolicyBytes is what a policy signature covers: a domain prefix
// followed by the policy's JSON with Signature empty and IssuedAt in UTC.
// encoding/json writes struct fields in declaration order and Settings holds
// no maps, so the encoding is deterministic and survives a decode/encode round
// trip (time.Time is RFC 3339 with nanoseconds, trailing zeros trimmed).
func CanonicalPolicyBytes(p Policy) ([]byte, error) {
	p.Signature = ""
	p.IssuedAt = p.IssuedAt.UTC()
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return append([]byte(policyDomain), b...), nil
}

// SignPolicy returns p signed with priv under keyID. It validates the policy
// first, so an admin cannot sign something a daemon would refuse.
func SignPolicy(priv ed25519.PrivateKey, keyID string, p Policy) (Policy, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return Policy{}, errors.New("hubproto: bad private key")
	}
	p.KeyID = keyID
	p.IssuedAt = p.IssuedAt.UTC()
	if err := p.validateHeader(); err != nil {
		return Policy{}, err
	}
	if err := p.Settings.Validate(); err != nil {
		return Policy{}, err
	}
	msg, err := CanonicalPolicyBytes(p)
	if err != nil {
		return Policy{}, err
	}
	p.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, msg))
	return p, nil
}

// VerifyPolicy checks p against the locally trusted keys (key id -> public
// key): a trusted key id, a valid ed25519 signature over
// [CanonicalPolicyBytes], and valid header and settings values. It does not
// judge freshness: a daemon must also refuse a Version lower than the one it
// already holds (rollback).
func VerifyPolicy(p Policy, trusted map[string]ed25519.PublicKey) error {
	if len(trusted) == 0 {
		return errPolicyNoTrustSet
	}
	if err := p.validateHeader(); err != nil {
		return err
	}
	pub, ok := trusted[p.KeyID]
	if !ok || len(pub) != ed25519.PublicKeySize {
		return ErrUntrustedKey
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(p.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return ErrPolicySignature
	}
	msg, err := CanonicalPolicyBytes(p)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, msg, sig) {
		return ErrPolicySignature
	}
	return p.Settings.Validate()
}

// DecodePolicy decodes a policy bundle strictly: unknown keys anywhere —
// including an executing key such as "post_create" inside settings — fail.
func DecodePolicy(data []byte) (Policy, error) {
	if len(data) > MaxBody {
		return Policy{}, ErrTooLarge
	}
	var p Policy
	if err := decodeStrictBytes(data, &p); err != nil {
		return Policy{}, fmt.Errorf("%w: %v", ErrPolicyMalformed, err)
	}
	return p, nil
}

// DecodeAndVerifyPolicy is [DecodePolicy] followed by [VerifyPolicy]: the one
// call a daemon should make on a fetched bundle.
func DecodeAndVerifyPolicy(data []byte, trusted map[string]ed25519.PublicKey) (Policy, error) {
	p, err := DecodePolicy(data)
	if err != nil {
		return Policy{}, err
	}
	if err := VerifyPolicy(p, trusted); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// ParseTrustedKeys parses config entries of the form "<key_id>:<public key>"
// (public key in [EncodePublicKey] form) into the map [VerifyPolicy] takes.
// A malformed or duplicate entry fails the whole set.
func ParseTrustedKeys(entries []string) (map[string]ed25519.PublicKey, error) {
	out := make(map[string]ed25519.PublicKey, len(entries))
	for i, e := range entries {
		id, enc, ok := strings.Cut(e, ":")
		if !ok || !ValidKeyID(id) {
			return nil, fmt.Errorf("%w: trusted_policy_keys[%d]: bad key id", ErrInvalid, i)
		}
		pub, err := DecodePublicKey(enc)
		if err != nil {
			return nil, fmt.Errorf("%w: trusted_policy_keys[%d]: bad public key", ErrInvalid, i)
		}
		if _, dup := out[id]; dup {
			return nil, fmt.Errorf("%w: trusted_policy_keys[%d]: duplicate key id", ErrInvalid, i)
		}
		out[id] = pub
	}
	return out, nil
}

// FormatTrustedKey renders the "<key_id>:<public key>" config entry.
func FormatTrustedKey(keyID string, pub ed25519.PublicKey) string {
	return keyID + ":" + EncodePublicKey(pub)
}
