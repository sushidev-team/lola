package daemon

// hubadmin.go — creating a team and the TEAM-ADMIN commands the desktop app
// sends (protocol.HubHumanCommands): invite a teammate, read or change the
// team's Linear and digest settings, revoke a device. Rules this file keeps:
//
//   - Every command is a person at this machine; a phone and the sandbox
//     hook bridge reach none of them.
//   - The team admin key (state/hub-admin-key) is loaded for the one request
//     it countersigns and never crosses the socket, a log line or an error.
//   - What goes IN may be a secret (a create code, a Linear key, a webhook):
//     it is never logged and is scrubbed from every error. What comes OUT is
//     status only, validated whole by internal/hub; the one exception is the
//     invite a person asked for, returned once and stored nowhere.
//   - The invite is built HERE from the hub URL this machine uses and the
//     admin key it holds. The hub contributes the token only, so it never
//     chooses the key a new member pins.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/protocol"
)

const (
	// hubAdminTimeout bounds one team-admin request (the hub may ask Linear).
	hubAdminTimeout = 45 * time.Second
	// hubSecretMax bounds a secret a client may send.
	hubSecretMax = 4 << 10
)

// hubScrub removes every secret from an error text.
func hubScrub(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, s := range secrets {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "[secret]")
		}
	}
	return errors.New(msg)
}

func (d *Daemon) handleHubCreateTeam(ctx context.Context, a protocol.HubCreateTeamArgs) (protocol.HubCreateTeamData, error) {
	var none protocol.HubCreateTeamData
	code := strings.TrimSpace(a.Code)
	switch {
	case code == "":
		return none, errors.New("hub: the create code is required")
	case len(code) > hubTokenMax || strings.ContainsAny(code, " \t\r\n"):
		return none, errors.New("hub: the create code must be a single line with no spaces")
	}
	// Parsed here too, so the secret part is known and can be scrubbed.
	_, secret, err := hubproto.ParseCreateCode(code)
	if err != nil {
		return none, errors.New("hub: that is not a create code (lola-hub-create:<url>#<code>)")
	}
	path, err := config.DefaultPath()
	if err != nil {
		return none, err
	}
	// Shielded like a join: a client that hangs up mid-request must not leave
	// a team on the hub with no identity and no admin key written here.
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hubJoinTimeout)
	defer cancel()
	res, err := hub.CreateTeam(cctx, hub.CreateTeamOptions{
		Code: code, Org: strings.TrimSpace(a.Team), DeviceName: strings.TrimSpace(a.Name),
		ReplaceAdminKey: a.ReplaceAdminKey,
		ConfigPath:      path, StateDir: d.hubDir(), NewClient: d.hub.enroll, Now: d.hub.now,
	})
	if err != nil {
		if errors.Is(err, hub.ErrCodeUsed) && res.Identity.Org != "" {
			// The identity is saved; the loop must still learn it.
			d.hubResetIdentity()
		}
		return none, hubScrub(err, code, secret)
	}
	d.hubResetIdentity()
	d.logf("", "hub: created team %s at %s as %q (device %s, admin key %s)", res.Identity.Org, res.Identity.URL, res.Identity.DeviceName, res.Identity.DeviceID, res.AdminKeyID)
	out := protocol.HubCreateTeamData{
		Org: res.Identity.Org, DeviceID: res.Identity.DeviceID, DeviceName: res.Identity.DeviceName, URL: res.Identity.URL,
		KeyCreated: res.KeyCreated, AdminKeyID: res.AdminKeyID, Fingerprint: res.Fingerprint,
	}
	if err := d.handleReload(ctx); err != nil {
		return out, fmt.Errorf("created team %s, but the daemon did not reload: %v", res.Identity.Org, err)
	}
	return out, nil
}

// hubAdmin loads what a team-admin request needs: the identity, the device
// key that enrolled and the admin key of THAT team. It fails closed on every
// mismatch, before anything is sent.
func (d *Daemon) hubAdmin() (*hub.Client, *hub.AdminKey, error) {
	dir := d.hubDir()
	id, err := hub.LoadIdentity(dir)
	if errors.Is(err, hub.ErrNotJoined) {
		return nil, nil, errors.New("hub: not joined (join or create a team first)")
	}
	if err != nil {
		return nil, nil, err
	}
	admin, err := hub.LoadAdminKey(dir)
	if err != nil {
		return nil, nil, err
	}
	if !admin.For(id) {
		return nil, nil, hub.ErrNoAdminKey
	}
	key, err := hub.LoadKey(dir)
	if err != nil {
		return nil, nil, err
	}
	if key.DeviceID() != id.DeviceID {
		return nil, nil, errors.New("hub: the device key is not the one that enrolled; leave and join again")
	}
	newClient := d.hub.enroll
	if newClient == nil {
		newClient = hub.NewClient
	}
	c, err := newClient(id.URL, key)
	if err != nil {
		return nil, nil, err
	}
	return c, admin, nil
}

func (d *Daemon) handleHubInvite(ctx context.Context, a protocol.HubInviteArgs) (protocol.HubInviteData, error) {
	var none protocol.HubInviteData
	req := hubproto.InviteRequest{Label: strings.TrimSpace(a.Label), TTLSeconds: a.TTLSeconds}
	if req.Validate() != nil {
		return none, errors.New("hub: name the teammate (letters, digits, spaces, dots, dashes; up to 40) and keep the lifetime between a minute and 30 days")
	}
	c, admin, err := d.hubAdmin()
	if err != nil {
		return none, err
	}
	ictx, cancel := context.WithTimeout(ctx, hubAdminTimeout)
	defer cancel()
	resp, err := c.CreateInviteToken(ictx, admin, req)
	if err != nil {
		return none, err
	}
	invite := admin.Invite(resp.Token)
	if invite == "" {
		return none, errors.New("hub: the invite could not be built from this machine's hub url")
	}
	// Who was invited is logged; the invite never is.
	d.logf("", "hub: minted an invite for %q in team %s", req.Label, admin.Org)
	return protocol.HubInviteData{Invite: invite, ExpiresAt: resp.ExpiresAt, KeyID: admin.KeyID, Fingerprint: admin.Fingerprint()}, nil
}

func (d *Daemon) handleHubTeamSettings(ctx context.Context, a protocol.HubTeamSettingsArgs) (protocol.HubTeamSettingsData, error) {
	var none protocol.HubTeamSettingsData
	trim := func(p *string) *string {
		if p == nil {
			return nil
		}
		s := strings.TrimSpace(*p)
		return &s
	}
	req := hubproto.OrgSettingsRequest{LinearKey: trim(a.LinearKey), LinearTeam: trim(a.LinearTeam), DigestWebhook: trim(a.DigestWebhook)}
	var secrets []string
	for _, p := range []*string{req.LinearKey, req.DigestWebhook} {
		if p != nil {
			if len(*p) > hubSecretMax {
				return none, errors.New("hub: that value is too long")
			}
			secrets = append(secrets, *p)
		}
	}
	write := req.LinearKey != nil || req.LinearTeam != nil || req.DigestWebhook != nil
	if write {
		// Fixed sentences: a validation error must not quote a secret.
		switch {
		case req.LinearKey != nil && *req.LinearKey != "" && !hubproto.ValidLinearKey(*req.LinearKey):
			return none, errors.New("hub: that is not a Linear API key (it starts with lin_api_)")
		case req.DigestWebhook != nil && *req.DigestWebhook != "" && !hubproto.ValidDigestWebhook(*req.DigestWebhook):
			return none, errors.New("hub: the digest webhook must be a Slack incoming webhook (https://hooks.slack.com/services/...)")
		case req.Validate() != nil:
			return none, errors.New("hub: that Linear team is not valid")
		}
	}
	c, admin, err := d.hubAdmin()
	if err != nil {
		return none, err
	}
	sctx, cancel := context.WithTimeout(ctx, hubAdminTimeout)
	defer cancel()
	var v hubproto.OrgSettingsView
	if write {
		v, err = c.SetOrgSettings(sctx, admin, req)
	} else {
		v, err = c.OrgSettings(sctx, admin, a.Teams)
	}
	if err != nil {
		switch {
		case hub.IsCode(err, hubproto.CodeInvalidSecret):
			return none, errors.New("hub: Linear refused that key; nothing was changed")
		case hub.IsCode(err, hubproto.CodeUnavailable):
			return none, errors.New("hub: the hub cannot do this right now (it stores no secrets, or Linear did not answer); nothing was changed")
		}
		return none, hubScrub(err, secrets...)
	}
	if write {
		// THAT a setting changed is logged, never its value.
		var what []string
		if req.LinearKey != nil {
			what = append(what, "linear key")
		}
		if req.LinearTeam != nil {
			what = append(what, "linear team")
		}
		if req.DigestWebhook != nil {
			what = append(what, "digest webhook")
		}
		d.logf("", "hub: changed team %s settings: %s", admin.Org, strings.Join(what, ", "))
	}
	out := protocol.HubTeamSettingsData{
		Org: v.Org, SecretsAvailable: v.SecretsAvailable, LinearKeySet: v.LinearKeySet, LinearTeam: v.LinearTeam,
		LinearError: v.LinearError, DigestSet: v.DigestSet, DigestError: v.DigestError, Teams: []protocol.HubLinearTeam{},
	}
	for _, t := range v.Teams {
		out.Teams = append(out.Teams, protocol.HubLinearTeam{ID: t.ID, Key: t.Key, Name: t.Name})
	}
	return out, nil
}

func (d *Daemon) handleHubRevoke(ctx context.Context, a protocol.HubRevokeArgs) (protocol.HubRevokeData, error) {
	name := strings.TrimSpace(a.Device)
	if !hubproto.ValidDeviceName(name) {
		return protocol.HubRevokeData{}, errors.New("hub: name the device to revoke")
	}
	c, admin, err := d.hubAdmin()
	if err != nil {
		return protocol.HubRevokeData{}, err
	}
	rctx, cancel := context.WithTimeout(ctx, hubAdminTimeout)
	defer cancel()
	if err := c.RevokeDevice(rctx, admin, name); err != nil {
		return protocol.HubRevokeData{}, err
	}
	d.logf("", "hub: revoked device %q of team %s", name, admin.Org)
	// The org view lists devices: the next read must show the change.
	d.hubDropCaches()
	return protocol.HubRevokeData{Revoked: true}, nil
}
