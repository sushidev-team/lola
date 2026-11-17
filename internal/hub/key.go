// Package hub is the daemon-side CLIENT of a lola hub (see lola-hub's
// DESIGN.md): the device key and enrollment identity, the signed HTTP client,
// the per-kind agent briefings, the GitHub findings collector, the signed
// policy store and the pure accept decision.
//
// It is a leaf: it imports hubproto and internal/config, never internal/daemon.
// The daemon loop composes it. Every rule here serves the one the hub design
// is built on — a compromised hub must not mean code execution on this
// machine — so nothing the hub sends is ever interpolated into an agent prompt
// without passing hubproto.Validate, and the device key never leaves the file
// it lives in (no config, no argv, no log line).
package hub

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
)

// File names under <LOLA_HOME>/state/.
const (
	KeyFile      = "hub-key"
	IdentityFile = "hub.json"
	PolicyFile   = "hub-policy.json"
	// RevokedFile records that the hub refused this device's signature (an
	// admin revoked it): the identity is gone, this says why.
	RevokedFile = "hub-revoked.json"
	// LeftFile records the hub this machine last left, so the sessions and
	// state kept after a leave can be traced to the hub they belonged to.
	LeftFile = "hub-left.json"
	// PendingJoinFile is written BEFORE the one-time token is spent and
	// removed once the identity is saved: a leftover one means a join was
	// cut short after the hub may already have used the token.
	PendingJoinFile = "hub-join-pending.json"
)

var (
	// ErrNoKey is returned when no device key exists yet.
	ErrNoKey = errors.New("hub: no device key")
	// ErrNotJoined is returned when no identity file exists (never joined, or left).
	ErrNotJoined = errors.New("hub: not joined to a hub")
	// ErrKeyPerms is returned for a key file group/other can read.
	ErrKeyPerms = errors.New("hub: device key file is accessible by other users (chmod 600 it)")
)

// StateDir is <LOLA_HOME>/state, where every hub file lives.
func StateDir() (string, error) {
	home, err := config.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "state"), nil
}

// Key is this device's ed25519 identity. The private half is unexported and
// the type's String/GoString/MarshalJSON never render it, so a stray %v or a
// JSON dump cannot leak it.
type Key struct {
	Public ed25519.PublicKey
	priv   ed25519.PrivateKey
}

// DeviceID is the hub-side device id derived from the public key.
func (k *Key) DeviceID() string { return hubproto.DeviceID(k.Public) }

// PublicString is the public key in hubproto's encoding.
func (k *Key) PublicString() string { return hubproto.EncodePublicKey(k.Public) }

func (k *Key) String() string   { return "hub.Key{device:" + k.DeviceID() + "}" }
func (k *Key) GoString() string { return k.String() }

// MarshalJSON renders the public half only.
func (k *Key) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"device_id": k.DeviceID(), "public_key": k.PublicString()})
}

// NewKey wraps an existing private key (tests, and LoadKey).
func NewKey(priv ed25519.PrivateKey) *Key {
	return &Key{Public: priv.Public().(ed25519.PublicKey), priv: priv}
}

// LoadKey reads the device key from dir/hub-key. It refuses a file other
// users can access rather than use a key that may already be copied.
func LoadKey(dir string) (*Key, error) {
	path := filepath.Join(dir, KeyFile)
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoKey
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("hub: device key %s is not a regular file", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, ErrKeyPerms
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seed, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(seed) != ed25519.SeedSize {
		// Never echo the file's content: it is the secret.
		return nil, fmt.Errorf("hub: device key %s is malformed", path)
	}
	return NewKey(ed25519.NewKeyFromSeed(seed)), nil
}

// LoadOrCreateKey returns the device key, generating and persisting a new one
// (0600, atomic) when none exists.
func LoadOrCreateKey(dir string) (*Key, error) {
	k, err := LoadKey(dir)
	if !errors.Is(err, ErrNoKey) {
		return k, err
	}
	_, priv, err := hubproto.GenerateKey()
	if err != nil {
		return nil, err
	}
	seed := base64.RawURLEncoding.EncodeToString(priv.Seed())
	if err := writeAtomic(dir, KeyFile, []byte(seed+"\n")); err != nil {
		return nil, err
	}
	return NewKey(priv), nil
}

// RemoveKey deletes the device key. Missing is not an error.
func RemoveKey(dir string) error { return removeIfExists(filepath.Join(dir, KeyFile)) }

// Identity is state/hub.json: what `lola hub join` learned from the hub.
type Identity struct {
	URL        string `json:"url"`
	DeviceID   string `json:"device_id"`
	Org        string `json:"org"`
	DeviceName string `json:"device_name,omitempty"`
	// Member is the admin-set label of the enroll token (C5), "" when the
	// hub predates members or the token carried none.
	Member   string    `json:"member,omitempty"`
	JoinedAt time.Time `json:"joined_at,omitzero"`
}

// Validate checks the identity's shape (it is read back from disk and the org
// is hub-provided, so it is treated as untrusted on every read).
func (id Identity) Validate() error {
	if err := config.ValidateHubURL(id.URL); err != nil {
		return err
	}
	if !hubproto.ValidDeviceID(id.DeviceID) {
		return errors.New("hub: identity device_id is malformed")
	}
	if !hubproto.ValidOrg(id.Org) {
		return errors.New("hub: identity org is malformed")
	}
	if id.Member != "" && !hubproto.ValidMemberLabel(id.Member) {
		return errors.New("hub: identity member is malformed")
	}
	return nil
}

// LoadIdentity reads dir/hub.json (ErrNotJoined when absent).
func LoadIdentity(dir string) (Identity, error) {
	data, err := os.ReadFile(filepath.Join(dir, IdentityFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Identity{}, ErrNotJoined
	}
	if err != nil {
		return Identity{}, err
	}
	var id Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return Identity{}, fmt.Errorf("hub: parse %s: %w", IdentityFile, err)
	}
	if err := id.Validate(); err != nil {
		return Identity{}, err
	}
	return id, nil
}

// SaveIdentity writes dir/hub.json atomically (0600).
func SaveIdentity(dir string, id Identity) error {
	if err := id.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(dir, IdentityFile, append(data, '\n'))
}

// RemoveIdentity deletes dir/hub.json and the stored policy. Missing is fine.
func RemoveIdentity(dir string) error {
	return errors.Join(
		removeIfExists(filepath.Join(dir, IdentityFile)),
		removeIfExists(filepath.Join(dir, PolicyFile)),
	)
}

// Revocation is state/hub-revoked.json: the identity the hub stopped
// accepting, and when lola noticed. Display only; a new join clears it.
type Revocation struct {
	URL        string    `json:"url"`
	Org        string    `json:"org"`
	DeviceID   string    `json:"device_id"`
	DeviceName string    `json:"device_name,omitempty"`
	At         time.Time `json:"at"`
}

// MarkRevoked records that the hub refused this device and removes the
// identity (and with it the stored policy), so nothing is sent as a device
// the hub no longer knows and no team policy stays enforced on its say-so.
// The device key is kept: a new token re-enrolls the same key. Without an
// identity there is nothing to mark and nothing is written.
func MarkRevoked(dir string, at time.Time) (Revocation, error) {
	id, err := LoadIdentity(dir)
	if err != nil {
		return Revocation{}, err
	}
	r := Revocation{URL: id.URL, Org: id.Org, DeviceID: id.DeviceID, DeviceName: id.DeviceName, At: at.UTC()}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return Revocation{}, err
	}
	if err := writeAtomic(dir, RevokedFile, append(data, '\n')); err != nil {
		return Revocation{}, err
	}
	return r, RemoveIdentity(dir)
}

// LoadRevocation reads state/hub-revoked.json; ok=false when there is none or
// it does not parse (it is display only, so an unknown is "not revoked").
func LoadRevocation(dir string) (Revocation, bool) {
	data, err := os.ReadFile(filepath.Join(dir, RevokedFile))
	if err != nil {
		return Revocation{}, false
	}
	var r Revocation
	if json.Unmarshal(data, &r) != nil || config.ValidateHubURL(r.URL) != nil || !hubproto.ValidOrg(r.Org) {
		return Revocation{}, false
	}
	return r, true
}

// ClearRevocation removes the revocation record (a new join, or a leave).
func ClearRevocation(dir string) error { return removeIfExists(filepath.Join(dir, RevokedFile)) }

// LeftRecord is state/hub-left.json: the hub this machine last left.
type LeftRecord struct {
	URL      string    `json:"url"`
	Org      string    `json:"org,omitempty"`
	DeviceID string    `json:"device_id,omitempty"`
	At       time.Time `json:"at"`
}

// LoadLeft reads state/hub-left.json (ok=false when absent or unreadable).
func LoadLeft(dir string) (LeftRecord, bool) {
	data, err := os.ReadFile(filepath.Join(dir, LeftFile))
	if err != nil {
		return LeftRecord{}, false
	}
	var r LeftRecord
	if json.Unmarshal(data, &r) != nil || config.ValidateHubURL(r.URL) != nil {
		return LeftRecord{}, false
	}
	return r, true
}

func saveLeft(dir string, r LeftRecord) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(dir, LeftFile, append(data, '\n'))
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// writeAtomic writes dir/name via a 0600 temp file + rename, creating dir 0700.
func writeAtomic(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+name+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		return err
	}
	ok = true
	return nil
}
