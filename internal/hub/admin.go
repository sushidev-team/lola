package hub

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
)

// A TEAM ADMIN KEY is the ed25519 key a team's creator generates on this
// machine. It signs the team's policy and countersigns org-admin requests
// (hubproto.SignAdmin). It is a different key from the device key and lives
// in its own file, state/hub-admin-key (0600), as a record naming the hub and
// the team it belongs to: it is never written to config.toml, never passed in
// argv and never logged, and no socket command returns it.
//
// A leave KEEPS the file: the team still trusts the key, and losing it means
// asking the hub operator for a new one.

// AdminKeyFile is the admin key record under <LOLA_HOME>/state/.
const AdminKeyFile = "hub-admin-key"

var (
	// ErrNoAdminKey: this machine holds no admin key (for this team).
	ErrNoAdminKey = errors.New("hub: this machine holds no admin key for this team")
	// ErrAdminKeyExists: creating a team would replace the admin key of
	// another team.
	ErrAdminKeyExists = errors.New("hub: this machine already holds a team admin key")
	// ErrCodeUsed wraps a failure AFTER the hub accepted the create code: the
	// code is spent, so retrying with it fails.
	ErrCodeUsed = errors.New("hub: the hub accepted the create code (it is now used)")
)

// AdminKey is a team admin key with the team it belongs to. The private half
// is unexported and no formatter or encoder renders it.
type AdminKey struct {
	URL    string
	Org    string
	KeyID  string
	Public ed25519.PublicKey
	// Pending: the key was written before the hub confirmed the team. Such a
	// key administers nothing yet and may be replaced.
	Pending bool
	priv    ed25519.PrivateKey
}

// adminKeyRecord is the file's JSON.
type adminKeyRecord struct {
	URL     string `json:"url"`
	Org     string `json:"org"`
	KeyID   string `json:"key_id"`
	Seed    string `json:"seed"`
	Pending bool   `json:"pending,omitempty"`
}

// PublicString is the public key in hubproto's encoding.
func (k *AdminKey) PublicString() string { return hubproto.EncodePublicKey(k.Public) }

// Trusted is the key as a trusted_policy_keys entry ("<key_id>:<public key>").
func (k *AdminKey) Trusted() string { return hubproto.FormatTrustedKey(k.KeyID, k.Public) }

// Fingerprint is KeyFingerprint of the public key.
func (k *AdminKey) Fingerprint() string { return KeyFingerprint(k.PublicString()) }

// For reports whether the key administers id's team.
func (k *AdminKey) For(id Identity) bool {
	return k != nil && !k.Pending && k.Org == id.Org && strings.TrimSuffix(k.URL, "/") == strings.TrimSuffix(id.URL, "/")
}

func (k *AdminKey) String() string   { return "hub.AdminKey{org:" + k.Org + " key:" + k.KeyID + "}" }
func (k *AdminKey) GoString() string { return k.String() }

// MarshalJSON renders the public half only.
func (k *AdminKey) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"org": k.Org, "key_id": k.KeyID, "public_key": k.PublicString()})
}

func (k *AdminKey) sign(req *http.Request, body []byte) error {
	return hubproto.SignAdmin(req, k.Org, k.KeyID, k.priv, body)
}

// newAdminKey generates a key for org at url, pending.
func newAdminKey(url, org string) (*AdminKey, error) {
	pub, priv, err := hubproto.GenerateKey()
	if err != nil {
		return nil, err
	}
	return &AdminKey{URL: url, Org: org, KeyID: hubproto.KeyIDFor(pub), Public: pub, Pending: true, priv: priv}, nil
}

// LoadAdminKey reads dir/hub-admin-key (ErrNoAdminKey when absent). Like the
// device key, a file other users can access is refused.
func LoadAdminKey(dir string) (*AdminKey, error) {
	path := filepath.Join(dir, AdminKeyFile)
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoAdminKey
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("hub: admin key %s is not a regular file", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("hub: admin key file is accessible by other users (chmod 600 it)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Never echo the file's content: it is the secret.
	malformed := fmt.Errorf("hub: admin key %s is malformed", path)
	var r adminKeyRecord
	if json.Unmarshal(data, &r) != nil {
		return nil, malformed
	}
	seed, err := base64.RawURLEncoding.DecodeString(r.Seed)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, malformed
	}
	if config.ValidateHubURL(r.URL) != nil || !hubproto.ValidOrg(r.Org) || !hubproto.ValidKeyID(r.KeyID) {
		return nil, malformed
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return &AdminKey{URL: r.URL, Org: r.Org, KeyID: r.KeyID, Public: priv.Public().(ed25519.PublicKey), Pending: r.Pending, priv: priv}, nil
}

// saveAdminKey writes the record atomically (0600).
func saveAdminKey(dir string, k *AdminKey) error {
	data, err := json.MarshalIndent(adminKeyRecord{
		URL: k.URL, Org: k.Org, KeyID: k.KeyID, Pending: k.Pending,
		Seed: base64.RawURLEncoding.EncodeToString(k.priv.Seed()),
	}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(dir, AdminKeyFile, append(data, '\n'))
}

// RemoveAdminKey deletes the admin key. Missing is not an error. The team
// keeps trusting the key until the hub operator removes it there.
func RemoveAdminKey(dir string) error { return removeIfExists(filepath.Join(dir, AdminKeyFile)) }

// IsAdmin reports whether dir holds the admin key of id's team. It never
// contacts the hub: the hub decides on every request.
func IsAdmin(dir string, id Identity) bool {
	k, err := LoadAdminKey(dir)
	return err == nil && k.For(id)
}

// ---- client calls ----------------------------------------------------------

// CreateOrg creates a team with a one-time create code and enrolls this
// device as its first member. The request carries both signatures: the
// device's and the admin key's, which proves the creator holds the key the
// hub registers.
func (c *Client) CreateOrg(ctx context.Context, code, deviceName string, admin *AdminKey) (hubproto.CreateOrgResponse, error) {
	if admin == nil {
		return hubproto.CreateOrgResponse{}, ErrNoAdminKey
	}
	req := hubproto.CreateOrgRequest{
		Code: code, Org: admin.Org, DeviceName: deviceName, PublicKey: c.key.PublicString(),
		AdminKeyID: admin.KeyID, AdminPublicKey: admin.PublicString(),
	}
	if err := req.Validate(); err != nil {
		return hubproto.CreateOrgResponse{}, err
	}
	var resp hubproto.CreateOrgResponse
	if err := c.doAdmin(ctx, admin, http.MethodPost, hubproto.PathOrgs, req, &resp); err != nil {
		return hubproto.CreateOrgResponse{}, err
	}
	if err := resp.Validate(); err != nil {
		return hubproto.CreateOrgResponse{}, invalidResponse("create: " + err.Error())
	}
	if resp.DeviceID != c.DeviceID() {
		return hubproto.CreateOrgResponse{}, invalidResponse("device_id does not match this key")
	}
	if resp.Org != admin.Org {
		return hubproto.CreateOrgResponse{}, invalidResponse("the hub created another team than the one asked for")
	}
	return resp, nil
}

// OrgSettings reads the team's settings STATUS (never a secret). teams also
// asks the hub to list the Linear workspace's teams with the stored key.
func (c *Client) OrgSettings(ctx context.Context, admin *AdminKey, teams bool) (hubproto.OrgSettingsView, error) {
	path := hubproto.PathOrgSettings
	if teams {
		path = hubproto.PathOrgSettingsTeams
	}
	return c.settings(ctx, admin, http.MethodGet, path, nil)
}

// SetOrgSettings changes the team's settings (nil field = unchanged, "" =
// cleared) and returns the new status. The secrets in req are sent once, in
// the signed body, and never come back.
func (c *Client) SetOrgSettings(ctx context.Context, admin *AdminKey, req hubproto.OrgSettingsRequest) (hubproto.OrgSettingsView, error) {
	if err := req.Validate(); err != nil {
		return hubproto.OrgSettingsView{}, err
	}
	return c.settings(ctx, admin, http.MethodPost, hubproto.PathOrgSettings, req)
}

func (c *Client) settings(ctx context.Context, admin *AdminKey, method, path string, in any) (hubproto.OrgSettingsView, error) {
	var v hubproto.OrgSettingsView
	if err := c.doAdmin(ctx, admin, method, path, in, &v); err != nil {
		return hubproto.OrgSettingsView{}, err
	}
	if err := v.Validate(); err != nil {
		return hubproto.OrgSettingsView{}, invalidResponse("settings: " + err.Error())
	}
	if v.Org != admin.Org {
		return hubproto.OrgSettingsView{}, invalidResponse("settings of another team")
	}
	return v, nil
}

// CreateInviteToken mints one single-use enroll token for a named member.
// The hub sends only the token: the caller builds the invite itself
// (AdminKey.Invite), so the hub never chooses the key a new member pins.
func (c *Client) CreateInviteToken(ctx context.Context, admin *AdminKey, req hubproto.InviteRequest) (hubproto.InviteResponse, error) {
	if err := req.Validate(); err != nil {
		return hubproto.InviteResponse{}, err
	}
	var resp hubproto.InviteResponse
	if err := c.doAdmin(ctx, admin, http.MethodPost, hubproto.PathOrgInvites, req, &resp); err != nil {
		return hubproto.InviteResponse{}, err
	}
	if err := resp.Validate(); err != nil {
		return hubproto.InviteResponse{}, invalidResponse("invite: " + err.Error())
	}
	return resp, nil
}

// RevokeDevice revokes a device of the team by its display name.
func (c *Client) RevokeDevice(ctx context.Context, admin *AdminKey, name string) error {
	req := hubproto.DeviceRevokeRequest{Device: name}
	if err := req.Validate(); err != nil {
		return err
	}
	return c.doAdmin(ctx, admin, http.MethodPost, hubproto.PathOrgRevoke, req, nil)
}

// doAdmin is do with the admin countersignature.
func (c *Client) doAdmin(ctx context.Context, admin *AdminKey, method, path string, in, out any) error {
	if admin == nil || len(admin.priv) != ed25519.PrivateKeySize {
		return ErrNoAdminKey
	}
	raw, err := c.rawSigned(ctx, method, path, in, hubproto.MaxBody, admin.sign)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := hubproto.DecodeStrict(strings.NewReader(string(raw)), out); err != nil {
		return invalidResponse("body does not decode")
	}
	return nil
}

// Invite renders the invite a new member pastes: this hub's URL, the token
// the hub minted and THIS key as the policy key to pin. "" when a part is
// malformed.
func (k *AdminKey) Invite(token string) string {
	return hubproto.FormatInvite(k.URL, token, k.Trusted())
}

// ---- create a team ---------------------------------------------------------

// CreateTeamOptions configures CreateTeam. ConfigPath and StateDir are
// required.
type CreateTeamOptions struct {
	// Code is the whole create code, lola-hub-create:<url>#<code>.
	Code string
	// Org is the new team's name.
	Org        string
	DeviceName string // "" = the sanitized hostname
	// ReplaceAdminKey allows discarding the admin key of ANOTHER team this
	// machine holds. Without it such a key refuses the create.
	ReplaceAdminKey bool
	ConfigPath      string
	StateDir        string
	// NewClient is the client seam (nil = NewClient).
	NewClient func(url string, key *Key) (*Client, error)
	// Now is the clock seam (nil = time.Now).
	Now func() time.Time
}

// CreateTeamResult reports what CreateTeam did.
type CreateTeamResult struct {
	Identity    Identity
	KeyCreated  bool
	AdminKeyID  string
	Fingerprint string
}

// CreateTeam creates a team on the hub the create code names and makes this
// machine its first member and its admin:
//
//  1. it creates the device key if needed and a fresh team admin key, stored
//     PENDING in state/hub-admin-key before anything is sent;
//  2. it calls POST /v1/orgs, signed with both keys;
//  3. it saves the identity, marks the admin key confirmed, and sets [hub]
//     enabled + url and pins the admin key in trusted_policy_keys. The key is
//     pinned without a question because it was generated here, not received.
//
// As with Join, the create code is single-use, so everything that can fail
// locally is tried before it is sent. A machine that is already joined
// refuses: leave first. The code is never logged and no error echoes it.
func CreateTeam(ctx context.Context, o CreateTeamOptions) (CreateTeamResult, error) {
	if o.ConfigPath == "" || o.StateDir == "" {
		return CreateTeamResult{}, errors.New("hub: creating a team needs a config path and a state dir")
	}
	url, code, err := hubproto.ParseCreateCode(o.Code)
	if err != nil {
		return CreateTeamResult{}, errors.New("hub: that is not a create code (lola-hub-create:<url>#<code>)")
	}
	if err := config.ValidateHubURL(url); err != nil {
		return CreateTeamResult{}, err
	}
	org := strings.TrimSpace(o.Org)
	if !hubproto.ValidOrg(org) {
		return CreateTeamResult{}, errors.New("hub: the team name must be lowercase letters, digits and dashes")
	}
	name := o.DeviceName
	if name == "" {
		name = DefaultDeviceName()
	}
	if !config.ValidHubDeviceName(name) || !hubproto.ValidDeviceName(name) {
		return CreateTeamResult{}, errors.New("hub: device name must be 1-64 letters, digits, spaces, dots, dashes or underscores")
	}
	if _, err := config.Load(o.ConfigPath); err != nil {
		return CreateTeamResult{}, err
	}
	if prev, err := LoadIdentity(o.StateDir); err == nil {
		return CreateTeamResult{}, fmt.Errorf("hub: already joined to team %s at %s; leave it first", prev.Org, prev.URL)
	}
	switch prev, err := LoadAdminKey(o.StateDir); {
	case errors.Is(err, ErrNoAdminKey):
	case err != nil:
		return CreateTeamResult{}, err
	case !prev.Pending && !o.ReplaceAdminKey:
		return CreateTeamResult{}, fmt.Errorf("%w (team %s at %s); creating a team would discard it", ErrAdminKeyExists, prev.Org, prev.URL)
	}
	if err := probeWritable(filepath.Dir(o.ConfigPath)); err != nil {
		return CreateTeamResult{}, fmt.Errorf("hub: the config directory is not writable, so the team could not be saved (the code was not sent): %w", err)
	}

	_, keyErr := LoadKey(o.StateDir)
	key, err := LoadOrCreateKey(o.StateDir)
	if err != nil {
		return CreateTeamResult{}, err
	}
	admin, err := newAdminKey(url, org)
	if err != nil {
		return CreateTeamResult{}, err
	}
	if err := saveAdminKey(o.StateDir, admin); err != nil {
		return CreateTeamResult{}, fmt.Errorf("hub: the state directory is not writable (the code was not sent): %w", err)
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	pending, err := json.Marshal(pendingJoin{URL: url, DeviceID: key.DeviceID(), At: now().UTC()})
	if err != nil {
		return CreateTeamResult{}, err
	}
	// A clean refusal leaves nothing behind: the hub recorded neither the
	// device nor the admin key.
	undo := func() {
		_ = removeIfExists(filepath.Join(o.StateDir, PendingJoinFile))
		_ = RemoveAdminKey(o.StateDir)
	}
	if err := writeAtomic(o.StateDir, PendingJoinFile, append(pending, '\n')); err != nil {
		undo()
		return CreateTeamResult{}, fmt.Errorf("hub: the state directory is not writable (the code was not sent): %w", err)
	}
	newClient := o.NewClient
	if newClient == nil {
		newClient = NewClient
	}
	c, err := newClient(url, key)
	if err != nil {
		undo()
		return CreateTeamResult{}, err
	}
	resp, err := c.CreateOrg(ctx, code, name, admin)
	if errors.Is(err, ErrInvalidResponse) {
		// The hub answered 200 with a reply lola refuses: it has most likely
		// created the team. The pending records stay, so the admin key the
		// hub registered is not lost.
		return CreateTeamResult{}, fmt.Errorf("%w, but its reply did not validate, so this machine is not joined: %v; ask the hub operator", ErrCodeUsed, err)
	}
	if err != nil {
		undo()
		return CreateTeamResult{}, err
	}
	res := CreateTeamResult{KeyCreated: errors.Is(keyErr, ErrNoKey), AdminKeyID: admin.KeyID, Fingerprint: admin.Fingerprint()}
	res.Identity = Identity{URL: url, DeviceID: resp.DeviceID, Org: resp.Org, DeviceName: resp.DeviceName, JoinedAt: now().UTC()}
	if err := SaveIdentity(o.StateDir, res.Identity); err != nil {
		return CreateTeamResult{}, fmt.Errorf("%w, but saving this machine's identity failed, so it is not joined: %v; ask the hub operator", ErrCodeUsed, err)
	}
	admin.Pending = false
	if err := saveAdminKey(o.StateDir, admin); err != nil {
		return res, fmt.Errorf("%w and this machine is joined, but the admin key was not marked confirmed: %v", ErrCodeUsed, err)
	}
	_ = removeIfExists(filepath.Join(o.StateDir, PendingJoinFile))
	_ = ClearRevocation(o.StateDir)
	// Re-read the file right before writing it (see Join).
	cfg, err := config.Load(o.ConfigPath)
	if err == nil {
		cfg.Hub.Enabled = true
		cfg.Hub.URL = url
		if o.DeviceName != "" {
			cfg.Hub.DeviceName = o.DeviceName
		}
		if cur, ok := cfg.Hub.TrustedPolicyKeys[admin.KeyID]; ok && strings.TrimSpace(cur) != admin.PublicString() {
			err = ErrKeyIDTaken
		} else {
			if cfg.Hub.TrustedPolicyKeys == nil {
				cfg.Hub.TrustedPolicyKeys = config.TrustedKeyMap{}
			}
			cfg.Hub.TrustedPolicyKeys[admin.KeyID] = admin.PublicString()
			err = cfg.Save(o.ConfigPath)
		}
	}
	if err != nil {
		return res, fmt.Errorf("%w and this machine is joined as its admin, but config.toml was not updated: %v; set [hub] enabled = true, url = %q and trusted_policy_keys = [%q] by hand", ErrCodeUsed, err, url, admin.Trusted())
	}
	return res, nil
}
