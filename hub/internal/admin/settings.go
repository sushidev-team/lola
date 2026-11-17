package admin

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hub/internal/secret"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// Create code lifetime.
const (
	DefaultCreateCodeTTL = 7 * 24 * time.Hour
	// MaxOpenCreateCodes bounds the unused codes a hub holds.
	MaxOpenCreateCodes = 100
	// MaxOpenTokensPerOrg bounds an org's unused enroll tokens, so invites
	// minted through the API cannot grow the store without limit.
	MaxOpenTokensPerOrg = 200
)

// ErrLimit is returned when a bound on stored tokens or codes is reached.
var ErrLimit = store.ErrLimit

// CreateCreateCode stores a fresh single-use create code (hashed) and returns
// the plaintext ONCE. Whoever holds it may create one org through the API.
func CreateCreateCode(st store.Store, ttl time.Duration, now time.Time) (string, time.Time, error) {
	if ttl < MinTokenTTL || ttl > MaxTokenTTL {
		return "", time.Time{}, fmt.Errorf("code lifetime must be between %s and %s", MinTokenTTL, MaxTokenTTL)
	}
	code, err := store.NewToken()
	if err != nil {
		return "", time.Time{}, err
	}
	now = now.UTC()
	exp := now.Add(ttl)
	err = st.Update(func(d *store.Data) error {
		open := 0
		for h, c := range d.CreateCodes {
			if now.Sub(c.ExpiresAt) > 7*24*time.Hour {
				delete(d.CreateCodes, h)
			} else if c.Usable(now) {
				open++
			}
		}
		if open >= MaxOpenCreateCodes {
			return fmt.Errorf("%d unused create codes: %w", open, ErrLimit)
		}
		h := store.HashToken(code)
		d.CreateCodes[h] = &store.CreateCode{Hash: h, CreatedAt: now, ExpiresAt: exp}
		d.Log(store.Event{At: now, Kind: "create_code_created"})
		return nil
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return code, exp, nil
}

// SetLinearKey stores org's Linear API key, sealed with box; "" clears it.
// The key must have the shape of a Linear key. It is never logged.
func SetLinearKey(st store.Store, box *secret.Box, org, key string, now time.Time) error {
	key = strings.TrimSpace(key)
	if key != "" && !hubproto.ValidLinearKey(key) {
		return errors.New("that is not a Linear API key (lin_api_...)")
	}
	return setSecret(st, box, org, secret.FieldLinearKey, key, "org_linear_key", now)
}

// SetDigestWebhook stores org's daily-digest webhook, sealed with box; ""
// clears it. Only a Slack incoming webhook is accepted: the hub posts to the
// URL by itself, so it must not be aimable at the hub's own network.
func SetDigestWebhook(st store.Store, box *secret.Box, org, hook string, now time.Time) error {
	hook = strings.TrimSpace(hook)
	if hook != "" && !hubproto.ValidDigestWebhook(hook) {
		return errors.New("the digest webhook must be a Slack incoming webhook (https://hooks.slack.com/services/...)")
	}
	return setSecret(st, box, org, secret.FieldDigestWebhook, hook, "org_digest", now)
}

func setSecret(st store.Store, box *secret.Box, org, field, plain, event string, now time.Time) error {
	sealed := ""
	if plain != "" {
		var err error
		if sealed, err = box.Seal(org, field, plain); err != nil {
			return err
		}
	}
	return st.Update(func(d *store.Data) error {
		o := d.Orgs[org]
		if o == nil {
			return fmt.Errorf("no org %s", org)
		}
		detail := "set"
		if sealed == "" {
			detail = "cleared"
		}
		switch field {
		case secret.FieldLinearKey:
			o.LinearKey, o.LinearErr = sealed, ""
		case secret.FieldDigestWebhook:
			o.DigestWebhook, o.DigestErr = sealed, ""
		}
		// The event says THAT it changed, never the value.
		d.Log(store.Event{At: now.UTC(), Org: org, Kind: event + "_" + detail})
		return nil
	})
}

// Settings is the status of an org's settings: what is set, never a secret.
func Settings(d *store.Data, org string, box *secret.Box) hubproto.OrgSettingsView {
	v := hubproto.OrgSettingsView{Org: org, SecretsAvailable: box != nil}
	o := d.Orgs[org]
	if o == nil {
		return v
	}
	v.LinearKeySet, v.DigestSet = o.LinearKey != "", o.DigestWebhook != ""
	if hubproto.ValidLinearTeamID(o.LinearTeam) {
		v.LinearTeam = o.LinearTeam
	}
	// Classes are hub constants, but a hand-edited store must not turn one
	// into free text on a member's screen.
	if hubproto.ValidStatusClass(o.LinearErr) {
		v.LinearError = o.LinearErr
	}
	if hubproto.ValidStatusClass(o.DigestErr) {
		v.DigestError = o.DigestErr
	}
	return v
}

// RemovePolicyKey unregisters an admin key of org. The key can no longer
// publish a policy or sign an org-admin request; daemons that pinned it keep
// trusting policies it already signed until they unpin it themselves.
func RemovePolicyKey(st store.Store, org, keyID string, now time.Time) error {
	if !hubproto.ValidKeyID(keyID) {
		return errors.New("key id must match ^[A-Za-z0-9_.-]{1,64}$")
	}
	return st.Update(func(d *store.Data) error {
		o := d.Orgs[org]
		if o == nil {
			return fmt.Errorf("no org %s", org)
		}
		if _, ok := o.AdminKeys[keyID]; !ok {
			return fmt.Errorf("org %s has no key %s", org, keyID)
		}
		delete(o.AdminKeys, keyID)
		d.Log(store.Event{At: now.UTC(), Org: org, Kind: "policy_key_removed", Detail: keyID})
		return nil
	})
}

// LegacyEnv is what a hub older than per-org secrets read from its
// environment: one Linear key, one team and one webhook for the whole hub.
type LegacyEnv struct {
	LinearKey, LinearTeam, DigestWebhook string
}

// AdoptLegacyEnv moves the hub-wide settings of an older deployment into the
// hub's ONLY org, for the fields that org has not set itself, and reports
// what it adopted. With no org, several orgs or no secret key it adopts
// nothing: a hub-wide secret never lands in an org it was not meant for.
func AdoptLegacyEnv(st store.Store, box *secret.Box, env LegacyEnv, now time.Time) ([]string, error) {
	if env == (LegacyEnv{}) {
		return nil, nil
	}
	var org string
	var cur store.Org
	_ = st.View(func(d *store.Data) error {
		if len(d.Orgs) != 1 {
			return nil
		}
		for name, o := range d.Orgs {
			org, cur = name, *o
		}
		return nil
	})
	if org == "" {
		return nil, errors.New("the hub does not hold exactly one org; set each org's Linear key and digest webhook itself")
	}
	var adopted []string
	if env.LinearTeam != "" && cur.LinearTeam == "" {
		if err := SetLinearTeam(st, org, env.LinearTeam, now); err != nil {
			return adopted, err
		}
		adopted = append(adopted, "linear team")
	}
	if env.LinearKey != "" && cur.LinearKey == "" {
		if err := SetLinearKey(st, box, org, env.LinearKey, now); err != nil {
			return adopted, err
		}
		adopted = append(adopted, "linear key")
	}
	if env.DigestWebhook != "" && cur.DigestWebhook == "" {
		if err := SetDigestWebhook(st, box, org, env.DigestWebhook, now); err != nil {
			return adopted, err
		}
		adopted = append(adopted, "digest webhook")
	}
	return adopted, nil
}
