package hubproto

import (
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The org board: two READ-ONLY, device-facing views a member's desktop app
// draws. Everything in them is DISPLAY data from the hub, so the daemon treats
// it as untrusted: it decodes with [DecodeStrictLimit] and calls Validate on
// the whole response before any of it reaches a client. A response that fails
// validation is dropped whole, so a compromised hub can show nothing rather
// than junk — no foreign URL scheme, no control character, no unbounded list.
// Nothing here is ever acted on: a claim still goes through the offer path and
// its own re-read from GitHub.
const (
	PathBoard = "/v1/board" // GET ?state=&limit=  -> BoardResponse
	PathOrg   = "/v1/org"   // GET                 -> OrgView
	PathStats = "/v1/stats" // GET                 -> StatsView
)

// Caps on the board views.
const (
	MaxBoardItems     = 500
	DefaultBoardLimit = 200
	MaxOrgDevices     = 200
	MaxItemAttempts   = 1000
	MaxItemReporters  = 100_000
	MaxIdentifierLen  = 32
	MaxLinearURLLen   = 512
	// MaxViewBody bounds a board, org or stats response. It is larger than
	// [MaxBody] because a full board is MaxBoardItems items, each with up to
	// [MaxItemHistory] attempts (about 2.5 MiB at worst); requests stay at
	// MaxBody.
	MaxViewBody = 4 << 20
)

var (
	// A Linear issue identifier: team key, dash, number ("ENG-123").
	identifierRe = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,9}-[1-9][0-9]{0,8}$`)
	// https://linear.app/<path> and nothing else: no userinfo, port, query or
	// fragment, and only path characters that cannot leave the host.
	linearURLRe = regexp.MustCompile(`^https://linear\.app/[A-Za-z0-9_-][A-Za-z0-9_./-]*$`)
)

// ValidDeviceName reports whether s is an acceptable device display name.
func ValidDeviceName(s string) bool {
	return len(s) > 0 && len(s) <= maxDeviceNameLen && deviceNameRe.MatchString(s)
}

// ValidIssueIdentifier reports whether s is a Linear issue identifier.
func ValidIssueIdentifier(s string) bool {
	return len(s) <= MaxIdentifierLen && identifierRe.MatchString(s)
}

// ValidateLinearURL accepts ONLY https://linear.app/... with a plain path:
// no other scheme or host, no userinfo, port, query, fragment or "..".
func ValidateLinearURL(s string) error {
	if len(s) > MaxLinearURLLen {
		return invalid("url", "too long")
	}
	path := strings.TrimPrefix(s, "https://linear.app")
	if !linearURLRe.MatchString(s) || strings.Contains(path, "..") || strings.Contains(path, "//") {
		return invalid("url", "not a linear.app URL")
	}
	return nil
}

// BoardPath builds /v1/board with its query. An empty state lists every
// state; limit <= 0 leaves the hub's default ([DefaultBoardLimit]). It returns
// "" for an unknown state or a limit over [MaxBoardItems].
func BoardPath(state State, limit int) string {
	if (state != "" && !state.Valid()) || limit > MaxBoardItems {
		return ""
	}
	q := url.Values{}
	if state != "" {
		q.Set("state", string(state))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if len(q) == 0 {
		return PathBoard
	}
	return PathBoard + "?" + q.Encode()
}

// ParseBoardQuery is the hub-side inverse of [BoardPath]: an empty or known
// state, and a limit in 1..MaxBoardItems (absent = [DefaultBoardLimit]).
// Unknown parameters and repeated ones are refused.
func ParseBoardQuery(q url.Values) (state State, limit int, err error) {
	for k, v := range q {
		if (k != "state" && k != "limit") || len(v) != 1 {
			return "", 0, invalid("query", "unknown or repeated parameter")
		}
	}
	state = State(q.Get("state"))
	if state != "" && !state.Valid() {
		return "", 0, invalid("state", "unknown")
	}
	limit = DefaultBoardLimit
	if s := q.Get("limit"); s != "" {
		n, perr := strconv.Atoi(s)
		if perr != nil || n <= 0 || n > MaxBoardItems || strconv.Itoa(n) != s {
			return "", 0, invalid("limit", "out of range")
		}
		limit = n
	}
	return state, limit, nil
}

// LeaseView is who holds a claimed item. Mine is the hub's answer for the
// asking device; DeviceName is the holder's display name; Member the label of
// the token it enrolled with (empty if none).
type LeaseView struct {
	Mine       bool      `json:"mine"`
	DeviceName string    `json:"device_name"`
	Member     string    `json:"member,omitempty"`
	Expires    time.Time `json:"expires"`
}

// OwnerView is the device that opened the item's PR ([StateInReview]) and
// stays recorded on it afterwards (done, failed, escalated). Same fields and
// rules as the lease holder, minus the expiry: ownership does not lapse on a
// clock the board shows.
type OwnerView struct {
	Mine       bool   `json:"mine"`
	DeviceName string `json:"device_name"`
	Member     string `json:"member,omitempty"`
}

// AttemptAction is the closed enum of an item's history entries.
type AttemptAction string

const (
	AttemptClaimed     AttemptAction = "claimed"      // a device took the lease
	AttemptInReview    AttemptAction = "in_review"    // the holder opened a PR
	AttemptCompleted   AttemptAction = "completed"    // the item is done
	AttemptFailed      AttemptAction = "failed"       // the device reported a failure
	AttemptReleased    AttemptAction = "released"     // the device gave the item back
	AttemptExpired     AttemptAction = "expired"      // the lease lapsed past grace, or hit max age
	AttemptReopened    AttemptAction = "reopened"     // the hub or an admin reopened it
	AttemptClosedStale AttemptAction = "closed_stale" // no longer reported; closed as failed
)

// AllAttemptActions lists every valid attempt action, in a stable order.
func AllAttemptActions() []AttemptAction {
	return []AttemptAction{AttemptClaimed, AttemptInReview, AttemptCompleted, AttemptFailed,
		AttemptReleased, AttemptExpired, AttemptReopened, AttemptClosedStale}
}

// Valid reports whether a is a known attempt action.
func (a AttemptAction) Valid() bool {
	switch a {
	case AttemptClaimed, AttemptInReview, AttemptCompleted, AttemptFailed,
		AttemptReleased, AttemptExpired, AttemptReopened, AttemptClosedStale:
		return true
	}
	return false
}

// byDevice reports whether a device did the action. reopened and closed_stale
// are the hub's (or an admin's) and name no device.
func (a AttemptAction) byDevice() bool {
	return a != AttemptReopened && a != AttemptClosedStale
}

// carriesReason reports whether a may carry a fail reason: a failure, a lease
// that ran out (timeout), or a stale close.
func (a AttemptAction) carriesReason() bool {
	return a == AttemptFailed || a == AttemptExpired || a == AttemptClosedStale
}

// MaxItemHistory caps an item's history; the hub keeps the newest entries.
const MaxItemHistory = 20

// Attempt is one entry of an item's history, newest last. Every field is
// display data; Reason is the closed [FailReason] enum, never prose.
type Attempt struct {
	DeviceName string        `json:"device_name,omitempty"`
	Member     string        `json:"member,omitempty"`
	Action     AttemptAction `json:"action"`
	Reason     FailReason    `json:"reason,omitempty"`
	PRURL      string        `json:"pr_url,omitempty"`
	At         time.Time     `json:"at"`
}

// validate checks one history entry; field prefixes the error's field name.
// A device-made action names its device; a hub-made one (reopened,
// closed_stale) may not name a member without a device.
func (a Attempt) validate(field string) error {
	if !a.Action.Valid() {
		return invalid(field+".action", "unknown")
	}
	switch {
	case a.DeviceName != "" && !ValidDeviceName(a.DeviceName):
		return invalid(field+".device_name", "bad device name")
	case a.DeviceName == "" && a.Action.byDevice():
		return invalid(field+".device_name", "missing")
	case a.DeviceName == "" && a.Member != "":
		return invalid(field+".member", "member without device")
	}
	if a.Member != "" && !ValidMemberLabel(a.Member) {
		return invalid(field+".member", "bad member label")
	}
	if a.Reason != "" && (!a.Reason.Valid() || !a.Action.carriesReason()) {
		return invalid(field+".reason", "unknown or not allowed for action")
	}
	if a.PRURL != "" && ValidatePRURL(a.PRURL) != nil {
		return invalid(field+".pr_url", "not a github.com pull request URL")
	}
	if a.At.IsZero() {
		return invalid(field+".at", "missing")
	}
	return nil
}

// Validate checks one history entry.
func (a Attempt) Validate() error { return a.validate("attempt") }

// OutcomeView is the PR a device reported for an item.
type OutcomeView struct {
	PRURL    string   `json:"pr_url"`
	Delivery Delivery `json:"delivery"`
}

// EscalationView is the Linear side of an escalated item. Pending means the
// issue has not been created yet; Identifier and URL are empty until it has.
type EscalationView struct {
	Pending    bool   `json:"pending"`
	Identifier string `json:"identifier,omitempty"` // e.g. "ENG-123"
	URL        string `json:"url,omitempty"`        // https://linear.app/... only
}

// ItemView is one board row: the item plus what the hub knows about it.
//
// The fields after Escalation are additive (a hub that predates them omits
// them): History is the attempt log (at most [MaxItemHistory], newest last);
// LastFail the reason of the newest failure; Reopened how many times the item
// went back to open; ClaimedAt when the current claim began (claimed or
// in_review); Owner the device that opened its PR.
type ItemView struct {
	Item       Item            `json:"item"`
	State      State           `json:"state"`
	Attempts   int             `json:"attempts"`
	Reporters  int             `json:"reporters"`
	FirstSeen  time.Time       `json:"first_seen"`
	LastSeen   time.Time       `json:"last_seen"`
	Lease      *LeaseView      `json:"lease,omitempty"`
	Outcome    *OutcomeView    `json:"outcome,omitempty"`
	Escalation *EscalationView `json:"escalation,omitempty"`
	History    []Attempt       `json:"history,omitempty"`
	LastFail   FailReason      `json:"last_fail,omitempty"`
	Reopened   int             `json:"reopened,omitempty"`
	ClaimedAt  time.Time       `json:"claimed_at,omitzero"`
	Owner      *OwnerView      `json:"owner,omitempty"`
}

// Validate checks the row: a valid item WITH an id, a known state, bounded
// counts, ordered timestamps, a lease only on a claimed item, an owner never
// on an open or claimed one, a bounded and ordered history, and every URL and
// name against its pattern.
func (v ItemView) Validate() error {
	if v.Item.ID == "" {
		return invalid("item.id", "missing")
	}
	if err := Validate(v.Item); err != nil {
		return err
	}
	if !v.State.Valid() {
		return invalid("state", "unknown")
	}
	if v.Attempts < 0 || v.Attempts > MaxItemAttempts {
		return invalid("attempts", "out of range")
	}
	if v.Reporters < 0 || v.Reporters > MaxItemReporters {
		return invalid("reporters", "out of range")
	}
	if v.FirstSeen.IsZero() || v.LastSeen.IsZero() {
		return invalid("first_seen", "missing")
	}
	if v.LastSeen.Before(v.FirstSeen) {
		return invalid("last_seen", "before first_seen")
	}
	if l := v.Lease; l != nil {
		if v.State != StateClaimed {
			return invalid("lease", "only a claimed item has a lease")
		}
		if !ValidDeviceName(l.DeviceName) {
			return invalid("lease.device_name", "bad device name")
		}
		if l.Member != "" && !ValidMemberLabel(l.Member) {
			return invalid("lease.member", "bad member label")
		}
		if l.Expires.IsZero() {
			return invalid("lease.expires", "missing")
		}
	}
	if o := v.Owner; o != nil {
		if v.State == StateOpen || v.State == StateClaimed {
			return invalid("owner", "an open or claimed item has no owner")
		}
		if !ValidDeviceName(o.DeviceName) {
			return invalid("owner.device_name", "bad device name")
		}
		if o.Member != "" && !ValidMemberLabel(o.Member) {
			return invalid("owner.member", "bad member label")
		}
	}
	if !v.ClaimedAt.IsZero() && v.State != StateClaimed && v.State != StateInReview {
		return invalid("claimed_at", "only a claimed or in_review item has one")
	}
	if v.LastFail != "" && !v.LastFail.Valid() {
		return invalid("last_fail", "unknown")
	}
	if v.Reopened < 0 || v.Reopened > MaxItemAttempts {
		return invalid("reopened", "out of range")
	}
	if len(v.History) > MaxItemHistory {
		return invalid("history", "too many")
	}
	for i, a := range v.History {
		if err := a.validate("history[" + strconv.Itoa(i) + "]"); err != nil {
			return err
		}
		if i > 0 && a.At.Before(v.History[i-1].At) {
			return invalid("history["+strconv.Itoa(i)+"].at", "not newest last")
		}
	}
	if o := v.Outcome; o != nil {
		if err := ValidatePRURL(o.PRURL); err != nil {
			return err
		}
		if !o.Delivery.Valid() {
			return invalid("outcome.delivery", "unknown")
		}
	}
	if e := v.Escalation; e != nil {
		if e.Identifier != "" && !ValidIssueIdentifier(e.Identifier) {
			return invalid("escalation.identifier", "not a Linear identifier")
		}
		if e.URL != "" {
			if err := ValidateLinearURL(e.URL); err != nil {
				return invalid("escalation.url", "not a linear.app URL")
			}
		}
	}
	return nil
}

// BoardResponse (GET /v1/board) lists the org's items, newest activity first.
type BoardResponse struct {
	Items []ItemView `json:"items"`
}

// Validate checks the list size, every row, and that no id repeats.
func (r BoardResponse) Validate() error {
	if len(r.Items) > MaxBoardItems {
		return invalid("items", "too many")
	}
	seen := make(map[string]bool, len(r.Items))
	for i, it := range r.Items {
		if err := it.Validate(); err != nil {
			return fmt.Errorf("items[%d]: %w", i, err)
		}
		if seen[it.Item.ID] {
			return invalid("items["+strconv.Itoa(i)+"].id", "duplicate")
		}
		seen[it.Item.ID] = true
	}
	return nil
}

// DeviceView is one member device as the org sees it. Me marks the asking
// device; Active means it heartbeat recently. Member is the label of the token
// it enrolled with; PolicyVersion the policy version it last reported applying
// (0 = none or unknown). Both are omitted by a hub that predates them.
type DeviceView struct {
	Name          string    `json:"name"`
	Me            bool      `json:"me"`
	LastSeen      time.Time `json:"last_seen"` // zero = never heartbeat
	FreeSlots     int       `json:"free_slots"`
	Agents        []string  `json:"agents"`
	Active        bool      `json:"active"`
	Member        string    `json:"member,omitempty"`
	PolicyVersion int       `json:"policy_version,omitempty"`
}

// OrgView (GET /v1/org) is the org, its devices and the stored policy's
// version (0 = no policy). The policy itself travels on [PathPolicy], signed.
type OrgView struct {
	Org           string       `json:"org"`
	Devices       []DeviceView `json:"devices"`
	PolicyVersion int          `json:"policy_version"`
}

// Validate checks the org slug, the device list and every device, and that at
// most one device is "me".
func (o OrgView) Validate() error {
	if !ValidOrg(o.Org) {
		return invalid("org", "bad org")
	}
	if o.PolicyVersion < 0 {
		return invalid("policy_version", "negative")
	}
	if len(o.Devices) > MaxOrgDevices {
		return invalid("devices", "too many")
	}
	me := 0
	for i, d := range o.Devices {
		field := "devices[" + strconv.Itoa(i) + "]"
		if !ValidDeviceName(d.Name) {
			return invalid(field+".name", "bad device name")
		}
		if d.FreeSlots < 0 || d.FreeSlots > MaxFreeSlots {
			return invalid(field+".free_slots", "out of range")
		}
		if d.Member != "" && !ValidMemberLabel(d.Member) {
			return invalid(field+".member", "bad member label")
		}
		if d.PolicyVersion < 0 {
			return invalid(field+".policy_version", "negative")
		}
		if len(d.Agents) > MaxHeartbeatAgents {
			return invalid(field+".agents", "too many")
		}
		for _, a := range d.Agents {
			if !agentNameRe.MatchString(a) {
				return invalid(field+".agents", "bad agent name")
			}
		}
		if d.Me {
			me++
		}
	}
	if me > 1 {
		return invalid("devices", "more than one is me")
	}
	return nil
}

// DecodeStrictLimit is [DecodeStrict] with a caller-chosen size bound, for
// the board views ([MaxViewBody]). Every other rule is the same: unknown
// fields and trailing data are rejected.
func DecodeStrictLimit(r io.Reader, limit int64, v any) error {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return ErrTooLarge
	}
	return decodeStrictBytes(data, v)
}
