// Package store holds the hub's state: orgs, devices, enroll tokens, work
// items, policies and a capped event log.
//
// The Store interface is transactional over one in-memory Data value: View
// reads it, Update mutates a private copy and commits only when the callback
// returns nil. The file implementation persists Data as one JSON snapshot
// (atomic temp+rename, 0600). A Postgres implementation can satisfy the same
// interface later.
//
// The package holds NO business rules. Lease transitions live in
// internal/lease, request handling in internal/api.
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hubproto"
)

// MaxEvents caps the event log PER ORG (hub-wide events, Org "", count as
// one more org); an org's oldest events are dropped first. Per org, so a
// busy team cannot push a quiet one's audit trail out of the log.
const MaxEvents = 2000

// MaxReporterIDs caps the distinct reporters remembered per item. The count
// keeps growing past it; only the id list stops.
const MaxReporterIDs = 64

// ErrNotFound is returned by lookups that find nothing.
var ErrNotFound = errors.New("store: not found")

// ErrLimit is returned (wrapped) when a bound on stored tokens or codes is
// reached.
var ErrLimit = errors.New("limit reached")

// Store is the transactional state interface.
type Store interface {
	// View calls fn with the current state. fn must not mutate or retain it.
	View(fn func(d *Data) error) error
	// Update calls fn with a private copy of the state. When fn returns nil
	// the copy is committed (and persisted); otherwise it is discarded.
	Update(fn func(d *Data) error) error
}

// Data is the whole hub state.
type Data struct {
	Orgs    map[string]*Org         `json:"orgs"`
	Devices map[string]*Device      `json:"devices"` // by device id
	Tokens  map[string]*EnrollToken `json:"tokens"`  // by token hash
	Items   map[string]*WorkItem    `json:"items"`   // by item id
	Events  []Event                 `json:"events"`
	// Stats: org -> daily counters, oldest first, the last
	// hubproto.StatsDays days only (see stats.go).
	Stats map[string][]hubproto.StatsDay `json:"stats,omitempty"`
	// CreateCodes: one-time codes that let a member create an org through
	// the API, by code hash (see tokens.go).
	CreateCodes map[string]*CreateCode `json:"create_codes,omitempty"`
}

// Org is a team.
type Org struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	// AdminKeys: policy key id -> public key (hubproto.EncodePublicKey form).
	// A published policy must verify against one of them. They are the
	// hub's copy for a sanity check only; daemons pin their own.
	AdminKeys map[string]string `json:"admin_keys,omitempty"`
	// Policy is the latest published, signed bundle (nil = none).
	Policy *hubproto.Policy `json:"policy,omitempty"`
	// LinearTeam is the Linear team id this org's escalations are filed in
	// ("" = the hub-wide --linear-team, which applies only while the hub
	// holds exactly one org). One org's findings never reach another org's
	// team.
	LinearTeam string `json:"linear_team,omitempty"`
	// LinearKey and DigestWebhook are the org's own secrets, SEALED
	// (internal/secret) under a master key that lives only in the hub's
	// environment. "" = not set. The plaintext is never stored, logged or
	// returned.
	LinearKey     string `json:"linear_key,omitempty"`
	DigestWebhook string `json:"digest_webhook,omitempty"`
	// LinearErr and DigestErr are the class of the latest failure using
	// them ("" = none): a hub constant, never a response body.
	LinearErr string `json:"linear_err,omitempty"`
	DigestErr string `json:"digest_err,omitempty"`
}

// Device is one enrolled lola daemon.
type Device struct {
	ID        string     `json:"id"`
	Org       string     `json:"org"`
	Name      string     `json:"name"`
	PublicKey string     `json:"public_key"` // hubproto.EncodePublicKey form
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	// Reported by the latest heartbeat.
	Repos     []string  `json:"repos,omitempty"`
	FreeSlots int       `json:"free_slots"`
	Agents    []string  `json:"agents,omitempty"`
	LastSeen  time.Time `json:"last_seen,omitzero"`
	// Member is the label of the enroll token the device enrolled with
	// (hubproto.ValidMemberLabel, "" = none). Set once, at enrollment.
	Member string `json:"member,omitempty"`
	// PolicyVersion is the policy version the latest heartbeat said the
	// daemon applies (0 = none or unknown). Display only.
	PolicyVersion int `json:"policy_version,omitempty"`
}

// Revoked reports whether the device was revoked.
func (d *Device) Revoked() bool { return d.RevokedAt != nil }

// HasRepo reports whether repo is among the device's reported repos
// (GitHub slugs compare case-insensitively).
func (d *Device) HasRepo(repo string) bool {
	for _, r := range d.Repos {
		if strings.EqualFold(r, repo) {
			return true
		}
	}
	return false
}

// EnrollToken is a single-use enrollment secret. Only its SHA-256 is stored.
type EnrollToken struct {
	Hash      string     `json:"hash"`
	Org       string     `json:"org"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	UsedBy    string     `json:"used_by,omitempty"` // device id
	// Label is the admin-set member name (hubproto.ValidMemberLabel, "" =
	// none). The device the token enrolls carries it as its member.
	Label string `json:"label,omitempty"`
	// RevokedAt is set when an admin revoked the token before use. Revoking
	// also pulls ExpiresAt back to that moment, so every check that refuses
	// an expired token refuses a revoked one too (see tokens.go).
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// Outcome is what the claiming device reported on completion.
type Outcome struct {
	PRURL    string            `json:"pr_url"`
	Delivery hubproto.Delivery `json:"delivery"`
	Device   string            `json:"device"`
	At       time.Time         `json:"at"`
}

// Escalation tracks the ONE Linear issue an item may get.
type Escalation struct {
	Pending  bool   `json:"pending"`
	Reason   string `json:"reason"` // "critical" | "attempts"
	LinearID string `json:"linear_id,omitempty"`
	// URL is the issue's https://linear.app/... link once it is known
	// (hubproto.ValidateLinearURL; the board drops anything else).
	URL       string    `json:"url,omitempty"`
	Err       string    `json:"err,omitempty"` // a class, never a response body
	Tries     int       `json:"tries,omitempty"`
	NextTry   time.Time `json:"next_try,omitzero"`
	CreatedAt time.Time `json:"created_at"`
	SentAt    time.Time `json:"sent_at,omitzero"`
	// Episode is the item's Reopened count when this escalation opened. An
	// item that went back on offer since (a new episode) and escalates again
	// is announced again, on the same issue (lease.OpenEscalation).
	Episode int `json:"episode,omitempty"`
}

// EscalationClosed is the Escalation.Err value of an issue the hub commented
// on and closed after its items were done (escalate.ClosedMark). It lives
// here so the pure state machine can read it without importing the sender.
const EscalationClosed = "linear: issue closed"

// WorkItem is a deduplicated finding and its lease state.
type WorkItem struct {
	ID          string              `json:"id"`
	Org         string              `json:"org"`
	Key         string              `json:"key"` // hubproto.DedupKey
	Item        hubproto.Item       `json:"item"`
	State       hubproto.State      `json:"state"`
	Attempts    int                 `json:"attempts"`
	Lease       *hubproto.Lease     `json:"lease,omitempty"`
	Outcome     *Outcome            `json:"outcome,omitempty"`
	LastFail    hubproto.FailReason `json:"last_fail,omitempty"`
	Reporters   int                 `json:"reporters"`
	ReporterIDs []string            `json:"reporter_ids,omitempty"`
	Reopened    int                 `json:"reopened,omitempty"`
	FirstSeen   time.Time           `json:"first_seen"`
	LastSeen    time.Time           `json:"last_seen"`
	ClosedAt    time.Time           `json:"closed_at,omitzero"` // entered done/failed
	Escalation  *Escalation         `json:"escalation,omitempty"`
	// ClaimedAt is when the current claim began (claimed, in_review). A
	// renewal or a re-claim inside the grace window keeps it, so the max
	// lease age bounds the whole claim, not one lease.
	ClaimedAt time.Time `json:"claimed_at,omitzero"`
	// Owner is the device that opened the item's PR (in_review), kept on the
	// item once it is done, failed or escalated; "" otherwise.
	Owner string `json:"owner,omitempty"`
	// ProgressAt is the owner's latest progress call (in_review idle clock).
	ProgressAt time.Time `json:"progress_at,omitzero"`
	// Grace holds a lapsed lease's item for its previous holder.
	Grace *Grace `json:"grace,omitempty"`
	// Cooldown: device id -> until when the item is not offered to it.
	Cooldown map[string]time.Time `json:"cooldown,omitempty"`
	// History is the attempt log, newest last, at most
	// hubproto.MaxItemHistory entries.
	History []Attempt `json:"history,omitempty"`
	// FailGroup is the latest fail's group (hubproto.ValidFailGroup): items
	// failed with one group escalate as one issue.
	FailGroup string `json:"fail_group,omitempty"`
}

// NoteReporter records device as a reporter of the item.
func (w *WorkItem) NoteReporter(device string, now time.Time) {
	w.LastSeen = now
	for _, id := range w.ReporterIDs {
		if id == device {
			return
		}
	}
	w.Reporters++
	if len(w.ReporterIDs) < MaxReporterIDs {
		w.ReporterIDs = append(w.ReporterIDs, device)
	}
}

// Event is one audit-log line. Detail is hub-generated text only.
type Event struct {
	At     time.Time `json:"at"`
	Org    string    `json:"org,omitempty"`
	Kind   string    `json:"kind"`
	Device string    `json:"device,omitempty"`
	Item   string    `json:"item,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

// NewData returns an empty state.
func NewData() *Data {
	d := &Data{}
	d.init()
	return d
}

func (d *Data) init() {
	if d.Orgs == nil {
		d.Orgs = map[string]*Org{}
	}
	if d.Devices == nil {
		d.Devices = map[string]*Device{}
	}
	if d.Tokens == nil {
		d.Tokens = map[string]*EnrollToken{}
	}
	if d.Items == nil {
		d.Items = map[string]*WorkItem{}
	}
	if d.CreateCodes == nil {
		d.CreateCodes = map[string]*CreateCode{}
	}
}

// Log appends an event, dropping the oldest event of the same org once that
// org holds more than MaxEvents.
func (d *Data) Log(e Event) {
	d.Events = append(d.Events, e)
	n := 0
	for i := range d.Events {
		if d.Events[i].Org == e.Org {
			n++
		}
	}
	if n <= MaxEvents {
		return
	}
	for i := range d.Events {
		if d.Events[i].Org == e.Org {
			d.Events = append(d.Events[:i:i], d.Events[i+1:]...)
			return
		}
	}
}

// ItemByKey finds an org's item by dedup key.
func (d *Data) ItemByKey(org, key string) *WorkItem {
	for _, w := range d.Items {
		if w.Org == org && w.Key == key {
			return w
		}
	}
	return nil
}

// OrgItems counts an org's items.
func (d *Data) OrgItems(org string) int {
	n := 0
	for _, w := range d.Items {
		if w.Org == org {
			n++
		}
	}
	return n
}

// HashToken is the stored form of an enroll token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewToken returns a fresh enroll token (32 url-safe chars, 192 bits).
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = alphabet[c&63]
	}
	return string(out), nil
}

// NewItemID returns a fresh item id ("i" + 24 hex chars).
func NewItemID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "i" + hex.EncodeToString(b), nil
}

// OrgLiveItems counts an org's non-terminal items (open, claimed,
// escalated): the ones that count against the org's item cap. Done and
// failed items do not, so an org's history can never lock it out.
func (d *Data) OrgLiveItems(org string) int {
	n := 0
	for _, w := range d.Items {
		if w.Org == org && w.State != hubproto.StateDone && w.State != hubproto.StateFailed {
			n++
		}
	}
	return n
}
