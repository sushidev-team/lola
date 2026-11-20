package hubproto

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"unicode/utf8"
)

// Team session sharing (lola's docs/team-sessions.md). A member publishes which
// of its sessions teammates may attach to; a teammate lists them and dials the
// owner through the blind relay. The hub routes and displays; it never grants.
// Authority is the owner's daemon, which pins the teammate's key locally, so
// nothing here names a lola session id, a path or a pane.

const (
	PathShares        = "/v1/shares"          // POST ShareSet (replace-all) -> empty; GET -> ShareList
	PathShareIdentity = "/v1/shares/identity" // POST MemberIdentity         -> empty
	PathShareMembers  = "/v1/shares/members"  // GET                         -> MemberList
	PathRelayMember   = "/v1/relay/member"    // GET ?share=<id>, WS upgrade, signed by the member
)

// QueryShare is the member dial's query key (in the query so Sign covers it).
const QueryShare = "share"

// Caps.
const (
	MaxSharesPerDevice  = 32
	MaxShareAudience    = 32
	MaxShareTitleRunes  = 80
	MaxMemberIdentities = MaxOrgDevices
)

// Share levels. A "type" share lets the teammate send keystrokes; it is the
// shell tier on that one pane and the UI says so.
const (
	LevelView = "view"
	LevelType = "type"
)

// ValidShareID reports whether s is a share id: 128 random bits, lowercase hex.
// It is unrelated to the lola session id, which embeds Linear identifiers.
func ValidShareID(s string) bool { return len(s) == 32 && isLowerHex(s) }

// NewShareID returns a fresh share id.
func NewShareID() (string, error) { return NewRelayID() }

var shareTitleRe = regexp.MustCompile(`^[^\x00-\x1f\x7f]+$`)

// ValidShareTitle reports whether s is an acceptable share title: the owner's
// own words, printable, at most MaxShareTitleRunes.
func ValidShareTitle(s string) bool {
	n := utf8.RuneCountInString(s)
	return n > 0 && n <= MaxShareTitleRunes && utf8.ValidString(s) && shareTitleRe.MatchString(s)
}

// ShareEntry is one shared session as the owner publishes it.
type ShareEntry struct {
	ID    string `json:"id"`
	RV    string `json:"rv"`              // the rendezvous the owner's daemon dials for; secret from nobody but the hub
	Title string `json:"title"`           // the owner's words; never a session id
	Agent string `json:"agent,omitempty"` // claude | codex | opencode
	Level string `json:"level"`           // view | type
	// Members are the audience as device ids. Nil = every other device of the
	// org. The owner's daemon re-checks the audience at attach: this list only
	// decides what the hub lists and admits.
	Members []string `json:"members,omitempty"`
}

// ShareSet is the owner's WHOLE set; shares the hub holds that are not in it
// are removed. It may be empty.
type ShareSet struct {
	Shares []ShareEntry `json:"shares"`
}

// Validate checks shapes and bounds. Errors name the field, never the value.
func (s ShareSet) Validate() error {
	if len(s.Shares) > MaxSharesPerDevice {
		return errors.New("hubproto: shares: too many")
	}
	ids := map[string]struct{}{}
	rvs := map[string]struct{}{}
	for _, e := range s.Shares {
		if !ValidShareID(e.ID) {
			return errors.New("hubproto: share id: invalid")
		}
		if _, dup := ids[e.ID]; dup {
			return errors.New("hubproto: share id: duplicate")
		}
		ids[e.ID] = struct{}{}
		if !ValidRelayID(e.RV) {
			return errors.New("hubproto: share rv: invalid")
		}
		if _, dup := rvs[e.RV]; dup {
			return errors.New("hubproto: share rv: duplicate")
		}
		rvs[e.RV] = struct{}{}
		if !ValidShareTitle(e.Title) {
			return errors.New("hubproto: share title: invalid")
		}
		if e.Agent != "" && !agentNameRe.MatchString(e.Agent) {
			return errors.New("hubproto: share agent: invalid")
		}
		if e.Level != LevelView && e.Level != LevelType {
			return errors.New("hubproto: share level: invalid")
		}
		if len(e.Members) > MaxShareAudience {
			return errors.New("hubproto: share members: too many")
		}
		seen := map[string]struct{}{}
		for _, m := range e.Members {
			if !ValidDeviceID(m) {
				return errors.New("hubproto: share member: invalid")
			}
			if _, dup := seen[m]; dup {
				return errors.New("hubproto: share member: duplicate")
			}
			seen[m] = struct{}{}
		}
	}
	return nil
}

// ShareView is a share as the asking teammate sees it. It carries no rv: only
// the hub and the owner hold it.
type ShareView struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Agent       string `json:"agent,omitempty"`
	Level       string `json:"level"`
	OwnerDevice string `json:"owner_device"` // device id
	OwnerName   string `json:"owner_name"`   // device display name
	OwnerMember string `json:"owner_member,omitempty"`
	Online      bool   `json:"online"` // the owner's relay control channel is up
}

// ShareList answers GET /v1/shares.
type ShareList struct {
	Shares []ShareView `json:"shares"`
}

// MaxShareViews bounds a listing.
const MaxShareViews = MaxOrgDevices * MaxSharesPerDevice

// Validate checks a listing whole: one bad row refuses it.
func (l ShareList) Validate() error {
	if len(l.Shares) > MaxShareViews {
		return errors.New("hubproto: shares: too many")
	}
	for _, v := range l.Shares {
		if !ValidShareID(v.ID) || !ValidShareTitle(v.Title) ||
			(v.Level != LevelView && v.Level != LevelType) ||
			!ValidDeviceID(v.OwnerDevice) || !ValidDeviceName(v.OwnerName) ||
			(v.OwnerMember != "" && !ValidMemberLabel(v.OwnerMember)) ||
			(v.Agent != "" && !agentNameRe.MatchString(v.Agent)) {
			return errors.New("hubproto: share view: invalid")
		}
	}
	return nil
}

// MemberIdentity binds a device's hub key to the TLS key its daemon presents
// on the inner session. The device signs the binding with its ed25519 hub key,
// so the hub can carry it but cannot forge one: an owner who pinned the
// ed25519 key (after seeing its fingerprint) trusts exactly the TLS key the
// device itself named.
type MemberIdentity struct {
	SPKI string `json:"spki"` // std base64 of SHA-256(SubjectPublicKeyInfo), as devices.Device.SPKI
	Sig  string `json:"sig"`  // base64url (unpadded) ed25519 signature over BindingMessage
}

var spkiRe = regexp.MustCompile(`^[A-Za-z0-9+/]{43}=$`)

// ValidSPKI reports whether s is a std-base64 SHA-256 digest.
func ValidSPKI(s string) bool { return spkiRe.MatchString(s) }

// BindingMessage is what a MemberIdentity signs.
func BindingMessage(deviceID, spki string) []byte {
	return []byte("lola-member-identity-v1\n" + deviceID + "\n" + spki)
}

// SignBinding returns the identity a device publishes.
func SignBinding(priv ed25519.PrivateKey, spki string) (MemberIdentity, error) {
	if !ValidSPKI(spki) {
		return MemberIdentity{}, errors.New("hubproto: spki: invalid")
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return MemberIdentity{}, ErrBadKey
	}
	sig := ed25519.Sign(priv, BindingMessage(DeviceID(pub), spki))
	return MemberIdentity{SPKI: spki, Sig: base64.RawURLEncoding.EncodeToString(sig)}, nil
}

// Verify checks the binding against the device's public key.
func (m MemberIdentity) Verify(pub ed25519.PublicKey) error {
	if !ValidSPKI(m.SPKI) {
		return errors.New("hubproto: spki: invalid")
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(m.Sig)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("hubproto: binding signature: invalid")
	}
	if !ed25519.Verify(pub, BindingMessage(DeviceID(pub), m.SPKI), sig) {
		return errors.New("hubproto: binding signature: wrong")
	}
	return nil
}

// Validate checks the shape only; the hub cannot check the signature without
// the device's key, which it has, so it does that too (see the API).
func (m MemberIdentity) Validate() error {
	if !ValidSPKI(m.SPKI) {
		return errors.New("hubproto: spki: invalid")
	}
	if len(m.Sig) != 86 {
		return errors.New("hubproto: binding signature: invalid")
	}
	return nil
}

// MemberView is one teammate device an owner may pick as audience.
type MemberView struct {
	DeviceID  string         `json:"device_id"`
	Name      string         `json:"name"`
	Member    string         `json:"member,omitempty"`
	PublicKey string         `json:"public_key"` // EncodePublicKey form; the owner confirms its fingerprint
	Identity  MemberIdentity `json:"identity"`
}

// MemberList answers GET /v1/shares/members: the org's devices that published
// an identity, the asking device excluded.
type MemberList struct {
	Members []MemberView `json:"members"`
}

// Validate checks a listing whole, binding signatures included: a row whose
// binding does not verify against its own key refuses the listing.
func (l MemberList) Validate() error {
	if len(l.Members) > MaxMemberIdentities {
		return errors.New("hubproto: members: too many")
	}
	for _, m := range l.Members {
		pub, err := DecodePublicKey(m.PublicKey)
		if err != nil || DeviceID(pub) != m.DeviceID || !ValidDeviceName(m.Name) ||
			(m.Member != "" && !ValidMemberLabel(m.Member)) || m.Identity.Verify(pub) != nil {
			return errors.New("hubproto: member: invalid")
		}
	}
	return nil
}

// Fingerprint renders a public key as groups of four hex characters over the
// first 16 bytes of its SHA-256: what an owner compares before pinning.
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	h := hex.EncodeToString(sum[:16])
	out := make([]byte, 0, len(h)+len(h)/4)
	for i := 0; i < len(h); i += 4 {
		if i > 0 {
			out = append(out, ' ')
		}
		out = append(out, h[i:i+4]...)
	}
	return string(out)
}
