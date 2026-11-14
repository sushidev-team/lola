package store

import (
	"strings"
	"time"
)

// Enroll token states, as listed to an admin.
const (
	TokenUnused  = "unused"
	TokenUsed    = "used"
	TokenRevoked = "revoked"
	TokenExpired = "expired"
)

// TokenIDLen is the length of a token's public id: a prefix of its hash. It
// names the token in lists and revoke commands; it is not the secret and
// cannot be turned back into it.
const TokenIDLen = 12

// ID is the token's public id (a hash prefix).
func (t *EnrollToken) ID() string {
	if len(t.Hash) < TokenIDLen {
		return t.Hash
	}
	return t.Hash[:TokenIDLen]
}

// Status is the token's state at now: used wins over revoked (a used token
// cannot be revoked), revoked over expired.
func (t *EnrollToken) Status(now time.Time) string {
	switch {
	case t.UsedAt != nil:
		return TokenUsed
	case t.RevokedAt != nil:
		return TokenRevoked
	case !now.Before(t.ExpiresAt):
		return TokenExpired
	}
	return TokenUnused
}

// Revoke revokes an unused token at now and reports whether it changed t. It
// also pulls ExpiresAt back to now: enrollment refuses an expired token, so a
// revoked one is refused by the same check without a second rule to forget.
func (t *EnrollToken) Revoke(now time.Time) bool {
	if t.UsedAt != nil || t.RevokedAt != nil {
		return false
	}
	at := now.UTC()
	t.RevokedAt = &at
	if at.Before(t.ExpiresAt) {
		t.ExpiresAt = at
	}
	return true
}

// TokenByID finds a token by its public id (or any longer hash prefix). It
// returns nil when none or more than one match, or the id is too short.
func (d *Data) TokenByID(id string) *EnrollToken {
	if len(id) < TokenIDLen {
		return nil
	}
	var found *EnrollToken
	for h, t := range d.Tokens {
		if strings.HasPrefix(h, id) {
			if found != nil {
				return nil
			}
			found = t
		}
	}
	return found
}

// CreateCode is a single-use secret the hub operator mints: whoever holds it
// may create ONE org through the API. Only its SHA-256 is stored.
type CreateCode struct {
	Hash      string     `json:"hash"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	UsedBy    string     `json:"used_by,omitempty"` // the org it created
}

// Usable reports whether the code can still create an org at now.
func (c *CreateCode) Usable(now time.Time) bool {
	return c != nil && c.UsedAt == nil && now.Before(c.ExpiresAt)
}

// OpenTokens counts an org's unused, unexpired enroll tokens.
func (d *Data) OpenTokens(org string, now time.Time) int {
	n := 0
	for _, t := range d.Tokens {
		if t.Org == org && t.Status(now) == TokenUnused {
			n++
		}
	}
	return n
}
