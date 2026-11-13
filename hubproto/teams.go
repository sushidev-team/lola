package hubproto

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Self-service teams: a member creates an org with a one-time CREATE CODE the
// hub operator minted, and the org's admins change its settings themselves.
//
// An ORG-ADMIN request is an ordinary device-signed request that carries a
// SECOND signature, made with a key registered in the org's admin keys (the
// keys that sign its policy). So it needs both an enrolled, unrevoked device
// of that org and the admin key; the device signature supplies freshness and
// replay protection, the admin signature the authority.
//
// What an org admin can set is a CLOSED set, none of it executing: the Linear
// key and team of the org's escalations, its digest webhook, enroll tokens
// and device revocation. There is deliberately no frame that registers an
// admin key or publishes a policy: a hub cannot be talked into either.

// Paths of the team frames.
const (
	PathOrgs        = "/v1/orgs"               // POST CreateOrgRequest -> CreateOrgResponse (create code)
	PathOrgSettings = "/v1/org/settings"       // GET [?teams=1] -> OrgSettingsView; POST OrgSettingsRequest -> OrgSettingsView
	PathOrgInvites  = "/v1/org/invites"        // POST InviteRequest -> InviteResponse
	PathOrgRevoke   = "/v1/org/devices/revoke" // POST DeviceRevokeRequest -> empty JSON object
)

// PathOrgSettingsTeams is [PathOrgSettings] asking the hub to also list the
// Linear workspace's teams with the org's stored key.
const PathOrgSettingsTeams = PathOrgSettings + "?teams=1"

// Error codes of the team frames.
const (
	// CodeUnavailable: the hub cannot do this right now (it runs without a
	// secret key, or Linear did not answer). Nothing was stored.
	CodeUnavailable = "unavailable"
	// CodeInvalidSecret: Linear refused the key. Nothing was stored.
	CodeInvalidSecret = "invalid_secret"
)

// Admin-signature headers, added to a device-signed request.
const (
	HeaderAdminKey       = "Lola-Admin-Key"
	HeaderAdminSignature = "Lola-Admin-Signature"
)

// adminDomain separates an admin signature from every other signature the
// same key makes: a policy signature can never be replayed as a request, nor
// a request as a policy.
const adminDomain = "lola-hub-admin-v1"

// ErrNotSigned is returned by [SignAdmin] for a request without the device
// signing headers.
var ErrNotSigned = errors.New("hubproto: sign the request with the device key first")

// AdminCanonicalString is the exact byte string an admin signature covers:
// the domain, the org, the signing device and the request's own
// [CanonicalString]. Naming the org means a key registered in two orgs signs
// for one at a time; naming the device means the signature cannot be lifted
// onto another device's request.
func AdminCanonicalString(org, deviceID, method, path, timestamp, nonce string, body []byte) string {
	return strings.Join([]string{adminDomain, org, deviceID, CanonicalString(method, path, timestamp, nonce, body)}, "\n")
}

// SignAdmin adds the admin signature to a request ALREADY signed with the
// device key ([Sign]): it covers that signature's device, timestamp and
// nonce, so it is fresh exactly as long as the device signature is.
func SignAdmin(req *http.Request, org, keyID string, priv ed25519.PrivateKey, body []byte) error {
	if len(priv) != ed25519.PrivateKeySize {
		return errors.New("hubproto: bad private key")
	}
	if !ValidOrg(org) {
		return invalid("org", "bad org")
	}
	if !ValidKeyID(keyID) {
		return invalid("key_id", "bad key id")
	}
	sh, err := ParseHeaders(req.Header)
	if err != nil {
		return ErrNotSigned
	}
	msg := AdminCanonicalString(org, sh.Device, req.Method, req.URL.RequestURI(), sh.RawTime, sh.Nonce, body)
	req.Header.Set(HeaderAdminKey, keyID)
	req.Header.Set(HeaderAdminSignature, base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(msg))))
	return nil
}

// AdminKeyID reads and shape-checks the admin key id header, so a hub knows
// which registered key to verify with. Exactly one value is accepted.
func AdminKeyID(h http.Header) (string, error) {
	v := h.Values(HeaderAdminKey)
	if len(v) != 1 || !ValidKeyID(v[0]) {
		return "", fmt.Errorf("%w: %s", ErrBadHeader, HeaderAdminKey)
	}
	return v[0], nil
}

// VerifyAdmin checks req's admin signature over body against pub for org. It
// does NOT check freshness or replay: call it only after [Verify] accepted
// the same request, whose timestamp and nonce this signature covers.
func VerifyAdmin(req *http.Request, body []byte, org string, pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return ErrBadKey
	}
	sh, err := ParseHeaders(req.Header)
	if err != nil {
		return err
	}
	if _, err := AdminKeyID(req.Header); err != nil {
		return err
	}
	v := req.Header.Values(HeaderAdminSignature)
	if len(v) != 1 || !sigRe.MatchString(v[0]) {
		return fmt.Errorf("%w: %s", ErrBadHeader, HeaderAdminSignature)
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(v[0])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: %s", ErrBadHeader, HeaderAdminSignature)
	}
	msg := AdminCanonicalString(org, sh.Device, req.Method, req.URL.RequestURI(), sh.RawTime, sh.Nonce, body)
	if !ed25519.Verify(pub, []byte(msg), sig) {
		return ErrBadSignature
	}
	return nil
}

// ---- create a team ---------------------------------------------------------

// CreateOrgRequest (POST /v1/orgs) creates an org and enrolls its first
// device in one step. Code is a one-time create code from the hub operator.
// The request carries BOTH signatures: the device's (as an enrollment does)
// and the admin key's over [AdminCanonicalString] with Org, which proves the
// creator holds the admin key it registers.
//
// The admin key is generated on the creator's machine and never leaves it; it
// is the key members pin, and it reaches them only inside an invite the
// admin's own app builds. The hub's copy never reaches a daemon.
type CreateOrgRequest struct {
	Code           string `json:"code"`
	Org            string `json:"org"`
	DeviceName     string `json:"device_name"`
	PublicKey      string `json:"public_key"`       // the device's, EncodePublicKey form
	AdminKeyID     string `json:"admin_key_id"`     // e.g. KeyIDFor(admin public key)
	AdminPublicKey string `json:"admin_public_key"` // EncodePublicKey form
}

// Validate checks the request's shape.
func (r CreateOrgRequest) Validate() error {
	if !tokenRe.MatchString(r.Code) {
		return invalid("code", "bad create code")
	}
	if !ValidOrg(r.Org) {
		return invalid("org", "bad org")
	}
	if !ValidDeviceName(r.DeviceName) {
		return invalid("device_name", "bad device name")
	}
	if _, err := DecodePublicKey(r.PublicKey); err != nil {
		return invalid("public_key", "bad public key")
	}
	if !ValidKeyID(r.AdminKeyID) {
		return invalid("admin_key_id", "bad key id")
	}
	if _, err := DecodePublicKey(r.AdminPublicKey); err != nil {
		return invalid("admin_public_key", "bad public key")
	}
	// One key, one job: a device key signs requests, an admin key signs
	// policy and admin requests.
	if r.AdminPublicKey == r.PublicKey {
		return invalid("admin_public_key", "the admin key must differ from the device key")
	}
	return nil
}

// CreateOrgResponse answers a team creation with the creator's enrollment.
type CreateOrgResponse struct {
	DeviceID   string `json:"device_id"`
	Org        string `json:"org"`
	DeviceName string `json:"device_name"`
}

// Validate checks the response's shape.
func (r CreateOrgResponse) Validate() error {
	if !ValidDeviceID(r.DeviceID) {
		return invalid("device_id", "bad device id")
	}
	if !ValidOrg(r.Org) {
		return invalid("org", "bad org")
	}
	if !ValidDeviceName(r.DeviceName) {
		return invalid("device_name", "bad device name")
	}
	return nil
}

// ---- org settings ----------------------------------------------------------

var (
	// A Linear personal API key. Nothing else is accepted as one, so a
	// pasted webhook or password is refused before it is stored anywhere.
	linearKeyRe = regexp.MustCompile(`^lin_api_[A-Za-z0-9]{16,128}$`)
	// A Linear id (a UUID in practice).
	linearTeamIDRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	// A Linear team KEY ("ENG").
	linearTeamKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,9}$`)
	// A Linear team name as shown to an admin: display data, letters, digits
	// and a little punctuation only.
	linearTeamNameRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ._&/+-]*$`)
	// A Slack incoming webhook, and nothing else: the hub POSTs to this URL
	// on its own, so an org admin must not be able to aim it at an address
	// inside the hub's network.
	digestWebhookRe = regexp.MustCompile(`^https://hooks\.slack\.com/services/[A-Za-z0-9]{1,32}/[A-Za-z0-9]{1,32}/[A-Za-z0-9]{1,64}$`)
	// A hub status class ("linear: unauthorized"): a hub constant, never a
	// response body.
	statusClassRe = regexp.MustCompile(`^[a-z][a-z0-9 :_-]{0,63}$`)
)

// MaxLinearTeams caps the teams one view lists; MaxLinearTeamNameLen a name
// (bytes).
const (
	MaxLinearTeams       = 100
	MaxLinearTeamNameLen = 64
)

// ValidLinearKey reports whether s has the shape of a Linear API key.
func ValidLinearKey(s string) bool { return linearKeyRe.MatchString(s) }

// ValidLinearTeamID reports whether s has the shape of a Linear team id.
func ValidLinearTeamID(s string) bool { return linearTeamIDRe.MatchString(s) }

// ValidLinearTeamKey reports whether s has the shape of a Linear team key.
func ValidLinearTeamKey(s string) bool { return linearTeamKeyRe.MatchString(s) }

// ValidLinearTeamName reports whether s is a team name both sides display.
func ValidLinearTeamName(s string) bool {
	return len(s) <= MaxLinearTeamNameLen && linearTeamNameRe.MatchString(s)
}

// ValidDigestWebhook reports whether s is a Slack incoming-webhook URL.
func ValidDigestWebhook(s string) bool { return len(s) <= 256 && digestWebhookRe.MatchString(s) }

// ValidStatusClass reports whether s has the shape of a hub status class.
func ValidStatusClass(s string) bool { return statusClassRe.MatchString(s) }

// OrgSettingsRequest (POST /v1/org/settings, admin-signed) changes the org's
// settings. A nil field is left alone; a pointer to "" clears it. The two
// secrets are WRITE-ONLY: no frame ever returns them.
//
// A new Linear key is checked against Linear before it is stored, and the
// answer lists the workspace's teams so the admin picks one.
type OrgSettingsRequest struct {
	LinearKey     *string `json:"linear_key,omitempty"`
	LinearTeam    *string `json:"linear_team,omitempty"` // a team ID
	DigestWebhook *string `json:"digest_webhook,omitempty"`
}

// Validate checks the request's shape. A request that changes nothing is
// invalid.
func (r OrgSettingsRequest) Validate() error {
	if r.LinearKey == nil && r.LinearTeam == nil && r.DigestWebhook == nil {
		return invalid("settings", "nothing to change")
	}
	if r.LinearKey != nil && *r.LinearKey != "" && !ValidLinearKey(*r.LinearKey) {
		return invalid("linear_key", "not a Linear API key")
	}
	if r.LinearTeam != nil && *r.LinearTeam != "" && !ValidLinearTeamID(*r.LinearTeam) {
		return invalid("linear_team", "bad team id")
	}
	if r.DigestWebhook != nil && *r.DigestWebhook != "" && !ValidDigestWebhook(*r.DigestWebhook) {
		return invalid("digest_webhook", "not a Slack incoming webhook")
	}
	return nil
}

// LinearTeam is one team of the Linear workspace a key belongs to. Name is
// empty when Linear's name does not pass [ValidLinearTeamName]; the key
// always identifies the team.
type LinearTeam struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name,omitempty"`
}

// OrgSettingsView is the STATUS of an org's settings: whether each secret is
// set and the class of its last error, never a secret. Teams is present only
// in the answer to a request that set a Linear key or asked for the list.
type OrgSettingsView struct {
	Org string `json:"org"`
	// SecretsAvailable: the hub can store secrets (it runs with a secret
	// key). When false, setting a key or webhook answers [CodeUnavailable].
	SecretsAvailable bool         `json:"secrets_available"`
	LinearKeySet     bool         `json:"linear_key_set"`
	LinearTeam       string       `json:"linear_team,omitempty"`
	LinearError      string       `json:"linear_error,omitempty"`
	DigestSet        bool         `json:"digest_set"`
	DigestError      string       `json:"digest_error,omitempty"`
	Teams            []LinearTeam `json:"teams,omitempty"`
}

// Validate checks every field. A daemon drops a view that fails it.
func (v OrgSettingsView) Validate() error {
	if !ValidOrg(v.Org) {
		return invalid("org", "bad org")
	}
	if v.LinearTeam != "" && !ValidLinearTeamID(v.LinearTeam) {
		return invalid("linear_team", "bad team id")
	}
	if v.LinearError != "" && !ValidStatusClass(v.LinearError) {
		return invalid("linear_error", "bad class")
	}
	if v.DigestError != "" && !ValidStatusClass(v.DigestError) {
		return invalid("digest_error", "bad class")
	}
	if len(v.Teams) > MaxLinearTeams {
		return invalid("teams", "too many")
	}
	for _, t := range v.Teams {
		if !ValidLinearTeamID(t.ID) || !ValidLinearTeamKey(t.Key) {
			return invalid("teams", "bad team")
		}
		if t.Name != "" && !ValidLinearTeamName(t.Name) {
			return invalid("teams", "bad team name")
		}
	}
	return nil
}

// ---- invites and devices ---------------------------------------------------

// Invite lifetime bounds (seconds) of [InviteRequest].
const (
	MinInviteTTL     = 60
	MaxInviteTTL     = 30 * 24 * 3600
	DefaultInviteTTL = 7 * 24 * 3600
)

// InviteRequest (POST /v1/org/invites, admin-signed) mints ONE single-use
// enroll token for a named member. TTLSeconds 0 means [DefaultInviteTTL].
type InviteRequest struct {
	Label      string `json:"label"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
}

// Validate checks the request's shape.
func (r InviteRequest) Validate() error {
	if !ValidMemberLabel(r.Label) {
		return invalid("label", "bad member label")
	}
	if r.TTLSeconds != 0 && (r.TTLSeconds < MinInviteTTL || r.TTLSeconds > MaxInviteTTL) {
		return invalid("ttl_seconds", "out of range")
	}
	return nil
}

// InviteResponse carries the token, shown once. The hub sends ONLY the
// token: the admin's app builds the invite ([FormatInvite]) with the hub URL
// it already uses and the admin key it holds, so the hub never chooses the
// key a new member pins.
type InviteResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Validate checks the response's shape.
func (r InviteResponse) Validate() error {
	if !tokenRe.MatchString(r.Token) {
		return invalid("token", "bad token")
	}
	if r.ExpiresAt.IsZero() {
		return invalid("expires_at", "missing")
	}
	return nil
}

// DeviceRevokeRequest (POST /v1/org/devices/revoke, admin-signed) revokes a
// device of the org by its display NAME, the only handle a member ever sees
// (device ids never leave the hub).
type DeviceRevokeRequest struct {
	Device string `json:"device"`
}

// Validate checks the request's shape.
func (r DeviceRevokeRequest) Validate() error {
	if !ValidDeviceName(r.Device) {
		return invalid("device", "bad device name")
	}
	return nil
}

// ---- invite and create-code strings ----------------------------------------

// What an admin hands a person out of band, as one line:
//
//	lola-hub:<url>#<token>[#<key_id>:<public key>]   join a team
//	lola-hub-create:<url>#<code>                      create a team
//
// Both are secrets (they hold a one-time token). No error here echoes any
// part of the input.
const (
	InvitePrefix     = "lola-hub:"
	CreateCodePrefix = "lola-hub-create:"
)

// maxInviteLen bounds what is parsed as an invite or a create code.
const maxInviteLen = 4 << 10

// CheckHubURL accepts a hub URL: https, or plain http to a loopback host
// only, with no user info, query or fragment ('#' separates the parts of an
// invite). It returns the URL without a trailing slash.
func CheckHubURL(raw string) (string, error) {
	bad := errors.New("hubproto: hub url must be an absolute https URL with no query or fragment")
	if raw == "" || len(raw) > 1024 || strings.ContainsAny(raw, "# \t\r\n") {
		return "", bad
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", bad
	}
	switch u.Scheme {
	case "https":
	case "http":
		host := u.Hostname()
		ip := net.ParseIP(host)
		if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			return "", errors.New("hubproto: hub url must be https (plain http only for a loopback hub)")
		}
	default:
		return "", errors.New("hubproto: hub url must be https (plain http only for a loopback hub)")
	}
	return strings.TrimRight(raw, "/"), nil
}

// Invite is a parsed invite. KeyID and PublicKey are both set or both empty.
type Invite struct {
	URL       string
	Token     string
	KeyID     string
	PublicKey string // EncodePublicKey form
}

// Trusted returns the invite's key as a trusted_policy_keys entry ("" when
// the invite carries none).
func (i Invite) Trusted() string {
	if i.KeyID == "" {
		return ""
	}
	return i.KeyID + ":" + i.PublicKey
}

// FormatInvite renders an invite. trusted ("" = none) is the
// "<key_id>:<public key>" entry the ADMIN supplies; it must never be looked
// up from a hub. It returns "" when a part is malformed.
func FormatInvite(hubURL, token, trusted string) string {
	u, err := CheckHubURL(hubURL)
	if err != nil || !tokenRe.MatchString(token) {
		return ""
	}
	s := InvitePrefix + u + "#" + token
	if trusted != "" {
		keys, err := ParseTrustedKeys([]string{trusted})
		if err != nil || len(keys) != 1 {
			return ""
		}
		for id, pub := range keys {
			s += "#" + FormatTrustedKey(id, pub)
		}
	}
	return s
}

// ParseInvite parses an invite and validates every part.
func ParseInvite(s string) (Invite, error) {
	s = strings.TrimSpace(s)
	rest, ok := strings.CutPrefix(s, InvitePrefix)
	if !ok || len(s) > maxInviteLen {
		return Invite{}, errors.New("hubproto: not a lola-hub: invite")
	}
	parts := strings.Split(rest, "#")
	if len(parts) < 2 || len(parts) > 3 {
		return Invite{}, errors.New("hubproto: an invite is lola-hub:<url>#<token>[#<key_id>:<public key>]")
	}
	u, err := CheckHubURL(parts[0])
	if err != nil {
		return Invite{}, errors.New("hubproto: the invite's hub url is not acceptable")
	}
	if !tokenRe.MatchString(parts[1]) {
		return Invite{}, errors.New("hubproto: the invite's token is malformed")
	}
	inv := Invite{URL: u, Token: parts[1]}
	if len(parts) == 3 {
		id, pub, ok := strings.Cut(parts[2], ":")
		if !ok || !ValidKeyID(id) {
			return Invite{}, errors.New("hubproto: the invite's policy key id is malformed")
		}
		if _, err := DecodePublicKey(pub); err != nil {
			return Invite{}, errors.New("hubproto: the invite's policy key is malformed")
		}
		inv.KeyID, inv.PublicKey = id, pub
	}
	return inv, nil
}

// FormatCreateCode renders a create code as one pasteable line. It returns ""
// when a part is malformed.
func FormatCreateCode(hubURL, code string) string {
	u, err := CheckHubURL(hubURL)
	if err != nil || !tokenRe.MatchString(code) {
		return ""
	}
	return CreateCodePrefix + u + "#" + code
}

// ParseCreateCode is the inverse of [FormatCreateCode].
func ParseCreateCode(s string) (hubURL, code string, err error) {
	s = strings.TrimSpace(s)
	rest, ok := strings.CutPrefix(s, CreateCodePrefix)
	if !ok || len(s) > maxInviteLen {
		return "", "", errors.New("hubproto: not a lola-hub-create: code")
	}
	rawURL, code, ok := strings.Cut(rest, "#")
	if !ok || !tokenRe.MatchString(code) {
		return "", "", errors.New("hubproto: a create code is lola-hub-create:<url>#<code>")
	}
	u, err := CheckHubURL(rawURL)
	if err != nil {
		return "", "", errors.New("hubproto: the create code's hub url is not acceptable")
	}
	return u, code, nil
}
