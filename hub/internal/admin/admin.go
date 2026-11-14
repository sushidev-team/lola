// Package admin holds the hub's administrative operations: the ones the
// host-side CLI (cmd/lola-hub admin ...) and the loopback-only web console
// (internal/console) both perform. One implementation, so the two surfaces
// cannot drift: same validation, same events, same refusals.
//
// Every operation is one store transaction. Errors are written for an
// operator; they may name what the operator typed (an org, an item id) but
// never a secret.
package admin

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hub/internal/api"
	"github.com/sushidev-team/lola/hub/internal/lease"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// Enroll token lifetime bounds.
const (
	MinTokenTTL     = time.Minute
	MaxTokenTTL     = 30 * 24 * time.Hour
	DefaultTokenTTL = 24 * time.Hour
)

// Reporter is the reporter id recorded for an item an admin added by hand. It
// is not a device id (those are 32 hex characters), so it never collides
// with one.
const Reporter = "admin"

// MaxLiveItemsPerOrg mirrors the API's default org cap, so a hand-added item
// cannot push an org past what devices may fill.
const MaxLiveItemsPerOrg = 10000

// ErrExists is returned when the thing being created already exists.
var ErrExists = errors.New("already exists")

// CreateOrg creates an org.
func CreateOrg(st store.Store, name string, now time.Time) error {
	if !hubproto.ValidOrg(name) {
		return errors.New("org name must match ^[a-z0-9][a-z0-9_-]{0,63}$")
	}
	return st.Update(func(d *store.Data) error {
		if d.Orgs[name] != nil {
			return fmt.Errorf("org %s %w", name, ErrExists)
		}
		d.Orgs[name] = &store.Org{Name: name, CreatedAt: now.UTC()}
		d.Log(store.Event{At: now.UTC(), Org: name, Kind: "org_created"})
		return nil
	})
}

// CreateEnrollToken stores a fresh single-use token for org (hashed) and
// returns the plaintext ONCE. label ("" = none) is the member name the
// enrolled device carries; it must pass hubproto.ValidMemberLabel. Tokens
// that expired over a week ago are dropped.
func CreateEnrollToken(st store.Store, org, label string, ttl time.Duration, now time.Time) (string, time.Time, error) {
	toks, exp, err := CreateEnrollTokens(st, org, label, ttl, 1, now)
	if err != nil {
		return "", time.Time{}, err
	}
	return toks[0], exp, nil
}

// MaxTokensPerCall bounds one CreateEnrollTokens call (an invite batch).
const MaxTokensPerCall = 50

// CreateEnrollTokens is CreateEnrollToken for n tokens with the same label,
// in one transaction: all of them exist or none does.
func CreateEnrollTokens(st store.Store, org, label string, ttl time.Duration, n int, now time.Time) ([]string, time.Time, error) {
	if ttl < MinTokenTTL || ttl > MaxTokenTTL {
		return nil, time.Time{}, fmt.Errorf("token lifetime must be between %s and %s", MinTokenTTL, MaxTokenTTL)
	}
	if n < 1 || n > MaxTokensPerCall {
		return nil, time.Time{}, fmt.Errorf("token count must be between 1 and %d", MaxTokensPerCall)
	}
	if label != "" && !hubproto.ValidMemberLabel(label) {
		return nil, time.Time{}, errors.New("label must be 1-40 letters, digits, spaces, dots, dashes or underscores, starting with a letter or digit")
	}
	toks := make([]string, n)
	for i := range toks {
		tok, err := store.NewToken()
		if err != nil {
			return nil, time.Time{}, err
		}
		toks[i] = tok
	}
	now = now.UTC()
	exp := now.Add(ttl)
	err := st.Update(func(d *store.Data) error {
		if d.Orgs[org] == nil {
			return fmt.Errorf("no org %s", org)
		}
		if open := d.OpenTokens(org, now); open+n > MaxOpenTokensPerOrg {
			return fmt.Errorf("org %s has %d unused enroll tokens (revoke some): %w", org, open, ErrLimit)
		}
		for _, tok := range toks {
			h := store.HashToken(tok)
			d.Tokens[h] = &store.EnrollToken{Hash: h, Org: org, CreatedAt: now, ExpiresAt: exp, Label: label}
			// The label is admin-typed display data that passed the member
			// pattern; the token itself never reaches the log.
			d.Log(store.Event{At: now, Org: org, Kind: "enroll_token_created", Detail: label})
		}
		for k, t := range d.Tokens {
			if now.Sub(t.ExpiresAt) > 7*24*time.Hour {
				delete(d.Tokens, k)
			}
		}
		return nil
	})
	if err != nil {
		return nil, time.Time{}, err
	}
	return toks, exp, nil
}

// ValidLinearTeam bounds a Linear team id (a UUID in practice).
func ValidLinearTeam(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// SetLinearTeam sets the org's Linear team; "" clears it.
func SetLinearTeam(st store.Store, org, team string, now time.Time) error {
	if team != "" && !ValidLinearTeam(team) {
		return errors.New("team must be 1-64 letters, digits or dashes")
	}
	return st.Update(func(d *store.Data) error {
		o := d.Orgs[org]
		if o == nil {
			return fmt.Errorf("no org %s", org)
		}
		o.LinearTeam = team
		d.Log(store.Event{At: now.UTC(), Org: org, Kind: "org_linear_set"})
		return nil
	})
}

// RevokeDevice revokes a device and reopens the items it held. It returns how
// many it reopened. When org is not "", the device must belong to it.
func RevokeDevice(st store.Store, org, id string, now time.Time) (int, error) {
	if !hubproto.ValidDeviceID(id) {
		return 0, errors.New("device id must be 32 lowercase hex characters")
	}
	reopened := 0
	err := st.Update(func(d *store.Data) error {
		dv := d.Devices[id]
		if dv == nil || (org != "" && dv.Org != org) {
			return fmt.Errorf("no device %s", id)
		}
		if dv.Revoked() {
			return fmt.Errorf("device %s is already revoked", id)
		}
		at := now.UTC()
		dv.RevokedAt = &at
		for _, w := range d.Items {
			if lease.Revoke(w, id) {
				reopened++
			}
		}
		d.Log(store.Event{At: at, Org: dv.Org, Kind: "device_revoked", Device: id})
		return nil
	})
	return reopened, err
}

// PurgeItem deletes one item. When org is not "", it must belong to it; when
// state is not "", it must be in it.
func PurgeItem(st store.Store, org, id string, state hubproto.State, now time.Time) error {
	return st.Update(func(d *store.Data) error {
		w := d.Items[id]
		if w == nil || (org != "" && w.Org != org) || (state != "" && w.State != state) {
			return fmt.Errorf("no item %s", id)
		}
		delete(d.Items, id)
		d.Log(store.Event{At: now.UTC(), Org: w.Org, Kind: "item_purged", Item: id})
		return nil
	})
}

// PurgeState deletes every item of org in state and returns how many.
func PurgeState(st store.Store, org string, state hubproto.State, now time.Time) (int, error) {
	if !state.Valid() {
		return 0, fmt.Errorf("unknown state %q", state)
	}
	n := 0
	err := st.Update(func(d *store.Data) error {
		for id, w := range d.Items {
			if w.Org == org && w.State == state {
				delete(d.Items, id)
				n++
				d.Log(store.Event{At: now.UTC(), Org: w.Org, Kind: "item_purged", Item: id})
			}
		}
		return nil
	})
	return n, err
}

// AddPolicyKey registers an admin public key for org and returns the
// trusted_policy_keys entry daemons pin. Re-adding the same key is a no-op.
func AddPolicyKey(st store.Store, org, keyID, pubStr string, now time.Time) (string, error) {
	if !hubproto.ValidKeyID(keyID) {
		return "", errors.New("key id must match ^[A-Za-z0-9_.-]{1,64}$")
	}
	pub, err := hubproto.DecodePublicKey(strings.TrimSpace(pubStr))
	if err != nil {
		return "", errors.New("public key must be a base64url ed25519 key (43 characters)")
	}
	err = st.Update(func(d *store.Data) error { return addKey(d, org, keyID, pub, now) })
	if err != nil {
		return "", err
	}
	return hubproto.FormatTrustedKey(keyID, pub), nil
}

// addKey registers pub under keyID for org inside a transaction.
func addKey(d *store.Data, org, keyID string, pub ed25519.PublicKey, now time.Time) error {
	enc := hubproto.EncodePublicKey(pub)
	o := d.Orgs[org]
	if o == nil {
		return fmt.Errorf("no org %s", org)
	}
	if old, ok := o.AdminKeys[keyID]; ok {
		if old == enc {
			return nil
		}
		return fmt.Errorf("key id %s is already registered with a different key", keyID)
	}
	if o.AdminKeys == nil {
		o.AdminKeys = map[string]string{}
	}
	o.AdminKeys[keyID] = enc
	d.Log(store.Event{At: now.UTC(), Org: org, Kind: "policy_key_added", Detail: keyID})
	return nil
}

// PublishPolicy verifies a signed bundle against its org's registered keys
// and stores it. The hub only VERIFIES: it never signs. When org is not "",
// the bundle must name it.
func PublishPolicy(st store.Store, org string, raw []byte, now time.Time) (hubproto.Policy, error) {
	return PublishPolicyWithKey(st, org, raw, "", now)
}

// PublishPolicyWithKey is PublishPolicy that first registers register (a
// "<key_id>:<public key>" entry, "" = none) as an admin key of the bundle's
// org, in the SAME transaction: when the bundle does not verify against the
// org's keys including that one, neither the key nor the bundle is stored.
// The key id must be the one that signed the bundle. Registering the key
// does not let the hub sign anything: the signature still has to verify,
// and daemons verify again against the keys they pin themselves.
func PublishPolicyWithKey(st store.Store, org string, raw []byte, register string, now time.Time) (hubproto.Policy, error) {
	p, err := hubproto.DecodePolicy(raw)
	if err != nil {
		return hubproto.Policy{}, err
	}
	if org != "" && p.Org != org {
		return hubproto.Policy{}, errors.New("the policy names a different org")
	}
	var regID string
	var regPub ed25519.PublicKey
	if register != "" {
		keys, err := hubproto.ParseTrustedKeys([]string{strings.TrimSpace(register)})
		if err != nil || len(keys) != 1 {
			return hubproto.Policy{}, errors.New("the key to register must be <key_id>:<public key>")
		}
		for id, pub := range keys {
			regID, regPub = id, pub
		}
		if regID != p.KeyID {
			return hubproto.Policy{}, errors.New("the key to register is not the key that signed the policy")
		}
	}
	err = st.Update(func(d *store.Data) error {
		o := d.Orgs[p.Org]
		if o == nil {
			return errors.New("the policy names an org this hub does not have")
		}
		if regPub != nil {
			if err := addKey(d, p.Org, regID, regPub, now); err != nil {
				return err
			}
		}
		entries := make([]string, 0, len(o.AdminKeys))
		for id, enc := range o.AdminKeys {
			entries = append(entries, id+":"+enc)
		}
		trusted, err := hubproto.ParseTrustedKeys(entries)
		if err != nil {
			return err
		}
		if len(trusted) == 0 {
			return fmt.Errorf("org %s has no policy keys; register one first", p.Org)
		}
		if err := hubproto.VerifyPolicy(p, trusted); err != nil {
			return fmt.Errorf("policy does not verify against org %s's keys: %w", p.Org, err)
		}
		if o.Policy != nil && p.Version <= o.Policy.Version {
			return fmt.Errorf("version %d is not newer than the published v%d", p.Version, o.Policy.Version)
		}
		cp := p
		o.Policy = &cp
		d.Log(store.Event{At: now.UTC(), Org: p.Org, Kind: "policy_published", Detail: "v" + strconv.Itoa(p.Version) + " " + p.KeyID})
		return nil
	})
	return p, err
}

// AddItem stores item for org as if a device had reported it, with
// [Reporter] as the reporter. The item must pass hubproto.Validate and carry
// no id. A known item (same dedup key) is re-reported, exactly as the
// findings endpoint does; it returns the item id and whether it was created.
func AddItem(st store.Store, org string, item hubproto.Item, now time.Time) (string, bool, error) {
	if item.ID != "" {
		return "", false, errors.New("a new item carries no id")
	}
	if err := hubproto.Validate(item); err != nil {
		return "", false, err
	}
	var id string
	var created bool
	err := st.Update(func(d *store.Data) error {
		if d.Orgs[org] == nil {
			return fmt.Errorf("no org %s", org)
		}
		at := now.UTC()
		if w := d.ItemByKey(org, hubproto.DedupKey(item)); w != nil {
			if lease.Observe(w, Reporter, now) {
				d.Log(store.Event{At: at, Org: org, Kind: "item_reopened", Item: w.ID, Detail: Reporter})
			}
			if w.State == hubproto.StateOpen {
				it := item
				it.ID = w.ID
				w.Item = it
				if it.Severity() == hubproto.SeverityCritical {
					// Counted and logged exactly as the findings
					// endpoint does (api.Tally).
					before := api.Snap(w)
					lease.OpenEscalation(w, lease.ReasonCritical, now)
					api.Tally(d, w, before, now)
				}
			}
			id = w.ID
			return nil
		}
		if d.OrgLiveItems(org) >= MaxLiveItemsPerOrg {
			return errors.New("the org is at its live item cap")
		}
		nid, err := store.NewItemID()
		if err != nil {
			return err
		}
		nw := lease.NewItem(nid, org, item, Reporter, now)
		d.Items[nid] = nw
		id, created = nid, true
		d.Log(store.Event{At: at, Org: org, Kind: "item_created", Item: nid, Detail: string(item.Kind) + " by " + Reporter})
		api.TallyOpened(d, org, now)
		api.Tally(d, nw, api.Snapshot{State: nw.State}, now)
		return nil
	})
	return id, created, err
}

// ItemFields are the operator-typed fields of a hand-added item. Kind
// selects which ones apply.
type ItemFields struct {
	Kind       string
	Repo       string
	Ecosystem  string
	Package    string
	Vulnerable string
	Fixed      string
	Advisory   string
	Severity   string
	PR         string
	Branch     string
	Author     string
}

// BuildItem turns typed fields into an item and validates it.
func BuildItem(f ItemFields) (hubproto.Item, error) {
	var it hubproto.Item
	switch hubproto.Kind(f.Kind) {
	case hubproto.KindDepAdvisory:
		it = hubproto.Item{Kind: hubproto.KindDepAdvisory, DepAdvisory: &hubproto.DepAdvisory{
			Repo: f.Repo, Ecosystem: f.Ecosystem, Package: f.Package, Vulnerable: f.Vulnerable,
			Fixed: f.Fixed, Advisory: f.Advisory, Severity: hubproto.Severity(f.Severity),
		}}
	case hubproto.KindRedBumpPR:
		n, err := strconv.Atoi(strings.TrimSpace(f.PR))
		if err != nil {
			return it, errors.New("pr must be a number")
		}
		it = hubproto.Item{Kind: hubproto.KindRedBumpPR, RedBumpPR: &hubproto.RedBumpPR{
			Repo: f.Repo, PR: n, Branch: f.Branch, Package: f.Package, Author: f.Author,
		}}
	default:
		return it, fmt.Errorf("unknown kind %q (dep_advisory or red_bump_pr)", f.Kind)
	}
	if err := hubproto.Validate(it); err != nil {
		return it, err
	}
	return it, nil
}
