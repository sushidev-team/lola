package admin

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// TokenInfo is an enroll token as an admin may see it: never the secret,
// only its public id (a hash prefix), its label and its state.
type TokenInfo struct {
	ID        string
	Org       string
	Label     string
	CreatedAt time.Time
	ExpiresAt time.Time
	Status    string // store.TokenUnused | TokenUsed | TokenRevoked | TokenExpired
	UsedAt    time.Time
	// UsedBy is the enrolled device's NAME ("" while unused, or when the
	// device is gone); UsedByID its id.
	UsedBy   string
	UsedByID string
}

// ListEnrollTokens lists org's enroll tokens ("" = every org), newest
// first. Tokens that expired over a week ago were dropped on creation of a
// later token and do not appear.
func ListEnrollTokens(st store.Store, org string, now time.Time) ([]TokenInfo, error) {
	var out []TokenInfo
	err := st.View(func(d *store.Data) error {
		for _, t := range d.Tokens {
			if org != "" && t.Org != org {
				continue
			}
			ti := TokenInfo{ID: t.ID(), Org: t.Org, Label: t.Label, CreatedAt: t.CreatedAt,
				ExpiresAt: t.ExpiresAt, Status: t.Status(now), UsedByID: t.UsedBy}
			if t.UsedAt != nil {
				ti.UsedAt = *t.UsedAt
			}
			if dv := d.Devices[t.UsedBy]; dv != nil {
				ti.UsedBy = dv.Name
			}
			out = append(out, ti)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, err
}

// RevokeEnrollToken revokes an unused token by its public id. When org is
// not "", the token must belong to it. A used token cannot be revoked:
// revoke the device it enrolled instead.
func RevokeEnrollToken(st store.Store, org, id string, now time.Time) error {
	id = strings.ToLower(strings.TrimSpace(id))
	if len(id) < store.TokenIDLen || len(id) > 64 || strings.Trim(id, "0123456789abcdef") != "" {
		return fmt.Errorf("token id must be %d to 64 lowercase hex characters", store.TokenIDLen)
	}
	return st.Update(func(d *store.Data) error {
		t := d.TokenByID(id)
		if t == nil || (org != "" && t.Org != org) {
			return fmt.Errorf("no enroll token %s", id)
		}
		switch t.Status(now) {
		case store.TokenUsed:
			return errors.New("the token was already used; revoke the device it enrolled instead")
		case store.TokenRevoked:
			return errors.New("the token is already revoked")
		}
		t.Revoke(now)
		d.Log(store.Event{At: now.UTC(), Org: t.Org, Kind: "enroll_token_revoked", Detail: t.ID()})
		return nil
	})
}

// InvitePrefix starts every invite string (the format is hubproto's).
const InvitePrefix = hubproto.InvitePrefix

// CheckHubURL accepts the hub URL an invite names: https, or plain http to
// a loopback host only (what lola itself accepts), with no user info, query
// or fragment ('#' separates the invite's parts).
func CheckHubURL(raw string) (string, error) {
	u, err := hubproto.CheckHubURL(raw)
	if err != nil {
		return "", errors.New(strings.TrimPrefix(err.Error(), "hubproto: "))
	}
	return u, nil
}

// CheckTrusted parses a "<key_id>:<public key>" policy key entry and returns
// it normalized.
func CheckTrusted(entry string) (string, error) {
	keys, err := hubproto.ParseTrustedKeys([]string{strings.TrimSpace(entry)})
	if err != nil || len(keys) != 1 {
		return "", errors.New("trusted key must be <key_id>:<public key>, as printed by lola-hub policy init or keygen")
	}
	for id, pub := range keys {
		return hubproto.FormatTrustedKey(id, pub), nil
	}
	return "", errors.New("trusted key missing")
}

// FormatInvite renders one invite string:
//
//	lola-hub:<url>#<token>[#<key_id>:<pub>]
//
// trusted ("" = none) is the policy key the ADMIN passed on the command
// line: it is never looked up from the store, so a compromised hub cannot
// choose the key a new member pins.
func FormatInvite(hubURL, token, trusted string) string {
	return hubproto.FormatInvite(hubURL, token, trusted)
}

// InitOrg creates org and, when team is not "", sets its Linear team, in one
// transaction. An org that already exists is an error wrapping ErrExists, so
// running init twice never changes an org silently.
func InitOrg(st store.Store, org, team string, now time.Time) error {
	if !hubproto.ValidOrg(org) {
		return errors.New("org name must match ^[a-z0-9][a-z0-9_-]{0,63}$")
	}
	if team != "" && !ValidLinearTeam(team) {
		return errors.New("team must be 1-64 letters, digits or dashes")
	}
	return st.Update(func(d *store.Data) error {
		if d.Orgs[org] != nil {
			return fmt.Errorf("org %s %w", org, ErrExists)
		}
		at := now.UTC()
		d.Orgs[org] = &store.Org{Name: org, CreatedAt: at, LinearTeam: team}
		d.Log(store.Event{At: at, Org: org, Kind: "org_created"})
		if team != "" {
			d.Log(store.Event{At: at, Org: org, Kind: "org_linear_set"})
		}
		return nil
	})
}
