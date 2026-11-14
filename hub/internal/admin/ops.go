package admin

import (
	"context"
	"errors"
	"time"

	"github.com/sushidev-team/lola/hub/internal/escalate"
	"github.com/sushidev-team/lola/hub/internal/secret"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// TeamLister lists the teams of the Linear workspace a key belongs to. It is
// also the check that Linear accepts the key.
type TeamLister interface {
	Teams(ctx context.Context) ([]hubproto.LinearTeam, error)
}

// Ops binds the org-scoped operations to a store and the hub's secret box.
// It is what the API hands an org admin (api.OrgAdmin): the same functions
// the CLI and the console call, with the org fixed by the caller's
// signature, never by the request.
type Ops struct {
	Store store.Store
	Box   *secret.Box // nil = the hub stores no secrets
	// Linear builds the client for a key (nil = the Linear API). Tests
	// replace it; nothing here touches the network otherwise.
	Linear func(key string) TeamLister
}

func (o *Ops) lister(key string) TeamLister {
	if o.Linear != nil {
		return o.Linear(key)
	}
	return &escalate.Linear{Key: key}
}

// Settings is the org's settings status.
func (o *Ops) Settings(d *store.Data, org string) hubproto.OrgSettingsView {
	return Settings(d, org, o.Box)
}

// LinearTeams asks Linear for the teams of key's workspace.
func (o *Ops) LinearTeams(ctx context.Context, key string) ([]hubproto.LinearTeam, error) {
	if !hubproto.ValidLinearKey(key) {
		return nil, escalate.ErrAuth
	}
	return o.lister(key).Teams(ctx)
}

// StoredLinearTeams asks Linear for the teams of org's stored key.
func (o *Ops) StoredLinearTeams(ctx context.Context, org string) ([]hubproto.LinearTeam, error) {
	var sealed string
	_ = o.Store.View(func(d *store.Data) error {
		if og := d.Orgs[org]; og != nil {
			sealed = og.LinearKey
		}
		return nil
	})
	if sealed == "" {
		return nil, errors.New("the org has no Linear key")
	}
	key, err := o.Box.Open(org, secret.FieldLinearKey, sealed)
	if err != nil {
		return nil, err
	}
	return o.LinearTeams(ctx, key)
}

// SetLinearKey is [SetLinearKey] with the hub's box.
func (o *Ops) SetLinearKey(org, key string, now time.Time) error {
	return SetLinearKey(o.Store, o.Box, org, key, now)
}

// SetLinearTeam is [SetLinearTeam].
func (o *Ops) SetLinearTeam(org, team string, now time.Time) error {
	return SetLinearTeam(o.Store, org, team, now)
}

// SetDigestWebhook is [SetDigestWebhook] with the hub's box.
func (o *Ops) SetDigestWebhook(org, hook string, now time.Time) error {
	return SetDigestWebhook(o.Store, o.Box, org, hook, now)
}

// CreateInvite mints one labelled enroll token for org.
func (o *Ops) CreateInvite(org, label string, ttl time.Duration, now time.Time) (string, time.Time, error) {
	if label == "" {
		return "", time.Time{}, errors.New("an invite needs a member label")
	}
	return CreateEnrollToken(o.Store, org, label, ttl, now)
}

// RevokeDeviceNamed revokes org's unrevoked device with the display name
// (names are unique per org). found=false means the org has no such device.
func (o *Ops) RevokeDeviceNamed(org, name string, now time.Time) (found bool, err error) {
	var id string
	_ = o.Store.View(func(d *store.Data) error {
		for _, dv := range d.Devices {
			if dv.Org == org && !dv.Revoked() && dv.Name == name {
				id = dv.ID
			}
		}
		return nil
	})
	if id == "" {
		return false, nil
	}
	_, err = RevokeDevice(o.Store, org, id, now)
	return err == nil, err
}
