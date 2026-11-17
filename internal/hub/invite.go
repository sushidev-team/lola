package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
)

// An INVITE is what an admin hands a new member out of band:
//
//	lola-hub:<url>#<token>[#<key_id>:<public key>]
//
// It carries the hub's URL, the one-time enroll token and, optionally, the
// admin key that signs the team's policy. The key in an invite is the ONLY
// way a policy key reaches this machine besides typing it into config.toml:
// it never comes from a hub response. Even so it is pinned only after the
// person confirmed its fingerprint (`lola hub join` prints it and asks, or
// takes --trust-key), because an invite is just text someone pasted.
//
// The whole invite is a secret (it holds the token): it is read from stdin
// or a file, never argv, and no error here echoes any part of it.

// InvitePrefix starts every invite string.
const InvitePrefix = "lola-hub:"

// maxInvite bounds what is parsed as an invite.
const maxInvite = 4 << 10

// Invite is a parsed invite. KeyID and PublicKey are both set or both empty.
type Invite struct {
	URL       string
	Token     string
	KeyID     string
	PublicKey string
}

// ParseInvite parses an invite, or a bare token (URL then ""). Every part is
// validated: the URL by config.ValidateHubURL, the token as one line with no
// spaces, the key id and key by hubproto. Errors are fixed strings.
func ParseInvite(s string) (Invite, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return Invite{}, errors.New("hub: no enroll token or invite")
	case len(s) > maxInvite:
		return Invite{}, errors.New("hub: the invite is too long")
	case strings.ContainsAny(s, " \t\r\n"):
		return Invite{}, errors.New("hub: the enroll token or invite must be a single line with no spaces")
	}
	if !strings.HasPrefix(s, InvitePrefix) {
		if strings.Contains(s, "#") {
			return Invite{}, errors.New("hub: this is not a lola-hub: invite or a bare token")
		}
		return Invite{Token: s}, nil
	}
	parts := strings.Split(strings.TrimPrefix(s, InvitePrefix), "#")
	if len(parts) < 2 || len(parts) > 3 {
		return Invite{}, errors.New("hub: an invite is lola-hub:<url>#<token>[#<key_id>:<public key>]")
	}
	inv := Invite{URL: strings.TrimSuffix(parts[0], "/"), Token: parts[1]}
	if err := config.ValidateHubURL(inv.URL); err != nil {
		return Invite{}, fmt.Errorf("hub: the invite's url: %w", err)
	}
	if inv.Token == "" {
		return Invite{}, errors.New("hub: the invite carries no token")
	}
	if len(parts) == 3 {
		id, pub, ok := strings.Cut(parts[2], ":")
		if !ok || !hubproto.ValidKeyID(id) {
			return Invite{}, errors.New("hub: the invite's policy key id is malformed")
		}
		if _, err := hubproto.DecodePublicKey(pub); err != nil {
			return Invite{}, errors.New("hub: the invite's policy key is not a base64url ed25519 public key")
		}
		inv.KeyID, inv.PublicKey = id, pub
	}
	return inv, nil
}

// KeyFingerprint is a short, readable fingerprint of a public key in
// hubproto's encoding: the first 8 bytes of its sha256, in four groups
// ("ab12 cd34 ef56 7890"). "" for a malformed key. An admin reads the same
// fingerprint to the member over another channel.
func KeyFingerprint(pub string) string {
	k, err := hubproto.DecodePublicKey(pub)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(k)
	h := hex.EncodeToString(sum[:8])
	return h[0:4] + " " + h[4:8] + " " + h[8:12] + " " + h[12:16]
}

// ErrKeyIDTaken: [hub].trusted_policy_keys already pins a DIFFERENT key under
// the invite's key id. An invite never replaces a pinned key.
var ErrKeyIDTaken = errors.New("hub: trusted_policy_keys already pins a different key under that id; remove it by hand first")

// PinTrustedKey adds keyID → pub to [hub].trusted_policy_keys in the config
// file. It loads the file fresh, refuses to replace a different key under
// the same id, and is a no-op for a key already pinned. Call it only after
// the person confirmed the key; the overlay never reaches the file, because
// this edits the file's own config.
func PinTrustedKey(configPath, keyID, pub string) (added bool, err error) {
	if !hubproto.ValidKeyID(keyID) {
		return false, errors.New("hub: the policy key id is malformed")
	}
	if _, err := hubproto.DecodePublicKey(pub); err != nil {
		return false, errors.New("hub: the policy key is not a base64url ed25519 public key")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return false, err
	}
	if cur, ok := cfg.Hub.TrustedPolicyKeys[keyID]; ok {
		if strings.TrimSpace(cur) == pub {
			return false, nil
		}
		return false, ErrKeyIDTaken
	}
	if cfg.Hub.TrustedPolicyKeys == nil {
		cfg.Hub.TrustedPolicyKeys = config.TrustedKeyMap{}
	}
	cfg.Hub.TrustedPolicyKeys[keyID] = pub
	if err := cfg.Save(configPath); err != nil {
		return false, err
	}
	return true, nil
}
