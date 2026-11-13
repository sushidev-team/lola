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
	// MaxViewBody bounds a board or org response. It is larger than [MaxBody]
	// because a full board is MaxBoardItems items; requests stay at MaxBody.
	MaxViewBody = 1 << 20
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
// asking device; DeviceName is the holder's display name.
type LeaseView struct {
	Mine       bool      `json:"mine"`
	DeviceName string    `json:"device_name"`
	Expires    time.Time `json:"expires"`
}

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
}

// Validate checks the row: a valid item WITH an id, a known state, bounded
// counts, ordered timestamps, a lease only on a claimed item, and every URL
// and name against its pattern.
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
		if l.Expires.IsZero() {
			return invalid("lease.expires", "missing")
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
// device; Active means it heartbeat recently.
type DeviceView struct {
	Name      string    `json:"name"`
	Me        bool      `json:"me"`
	LastSeen  time.Time `json:"last_seen"` // zero = never heartbeat
	FreeSlots int       `json:"free_slots"`
	Agents    []string  `json:"agents"`
	Active    bool      `json:"active"`
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
