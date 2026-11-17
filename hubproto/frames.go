package hubproto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// MaxBody is the largest request or response body either side accepts.
const MaxBody = 64 << 10

// Endpoint paths, versioned by the /v1 prefix.
const (
	PathEnroll      = "/v1/enroll"    // POST EnrollRequest  -> EnrollResponse
	PathHeartbeat   = "/v1/heartbeat" // POST HeartbeatRequest -> HeartbeatResponse
	PathFindings    = "/v1/findings"  // POST FindingsRequest -> FindingsResponse
	PathOffers      = "/v1/offers"    // GET                  -> OffersResponse
	PathPolicy      = "/v1/policy"    // GET                  -> Policy
	PathItemsPrefix = "/v1/items/"    // POST /v1/items/{id}/{action}
)

// ItemAction is the last segment of an item path.
type ItemAction string

const (
	ActionClaim    ItemAction = "claim"    // POST, no body      -> Lease
	ActionComplete ItemAction = "complete" // POST CompleteRequest -> empty JSON object
	ActionFail     ItemAction = "fail"     // POST FailRequest     -> empty JSON object
	ActionRelease  ItemAction = "release"  // POST, no body      -> empty JSON object
	// ActionProgress reports a PR opened for the item. Allowed for the lease
	// holder while claimed (claimed -> in_review: lease dropped, the device
	// recorded as owner) and for the owner while in_review (updates delivery).
	// A hub that predates it answers 404; the daemon then falls back to
	// complete-on-PR.
	ActionProgress ItemAction = "progress" // POST ProgressRequest -> empty JSON object
)

// Valid reports whether a is a known item action.
func (a ItemAction) Valid() bool {
	switch a {
	case ActionClaim, ActionComplete, ActionFail, ActionRelease, ActionProgress:
		return true
	}
	return false
}

// ItemPath builds /v1/items/{id}/{action}. It returns "" for an invalid id or
// action, so a malformed id can never be spliced into a path.
func ItemPath(id string, action ItemAction) string {
	if !ValidItemID(id) || !action.Valid() {
		return ""
	}
	return PathItemsPrefix + id + "/" + string(action)
}

// ClaimPath, CompletePath, FailPath, ReleasePath and ProgressPath are
// [ItemPath] shorthands.
func ClaimPath(id string) string    { return ItemPath(id, ActionClaim) }
func CompletePath(id string) string { return ItemPath(id, ActionComplete) }
func FailPath(id string) string     { return ItemPath(id, ActionFail) }
func ReleasePath(id string) string  { return ItemPath(id, ActionRelease) }
func ProgressPath(id string) string { return ItemPath(id, ActionProgress) }

// ParseItemPath is the hub-side inverse of [ItemPath].
func ParseItemPath(path string) (id string, action ItemAction, ok bool) {
	rest, found := strings.CutPrefix(path, PathItemsPrefix)
	if !found {
		return "", "", false
	}
	id, act, found := strings.Cut(rest, "/")
	if !found || !ValidItemID(id) || !ItemAction(act).Valid() {
		return "", "", false
	}
	return id, ItemAction(act), true
}

// Error codes carried in ErrorResponse.Code.
const (
	CodeBadRequest   = "bad_request"
	CodeUnauthorized = "unauthorized" // missing/bad signature, unknown device
	CodeSkew         = "stale_timestamp"
	CodeReplay       = "replayed_nonce"
	CodeForbidden    = "forbidden"
	CodeNotFound     = "not_found"
	CodeConflict     = "conflict" // e.g. item already claimed
	CodeLeaseLost    = "lease_lost"
	CodeTooLarge     = "too_large"
	CodeRateLimited  = "rate_limited"
	CodeInvalidItem  = "invalid_item"
	CodeNoPolicy     = "no_policy"
	CodeInternal     = "internal"
)

// ErrorResponse is the body of every non-2xx response.
type ErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

var (
	tokenRe      = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
	deviceNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]*$`)
	orgRe        = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	agentNameRe  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	// A member label: set by an admin on the enroll token, carried by every
	// device enrolled with it. Display data, so the device-name alphabet.
	memberLabelRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}$`)
	// A fail group: an opaque id a daemon picks to say "these failures are
	// one problem". Lowercase alnum only; it never reaches an agent.
	failGroupRe = regexp.MustCompile(`^[a-z0-9]{8,32}$`)
)

// MaxMemberLabelLen caps a member label (bytes).
const MaxMemberLabelLen = 40

// ValidMemberLabel reports whether s is an acceptable member label.
func ValidMemberLabel(s string) bool { return memberLabelRe.MatchString(s) }

// ValidFailGroup reports whether s is an acceptable fail group.
func ValidFailGroup(s string) bool { return failGroupRe.MatchString(s) }

// UniqueDeviceName returns name, or name with the first free "-2", "-3", …
// suffix when taken reports it in use, so device display names are unique per
// org. The base is truncated so the result stays within the device-name length
// cap. name must already be valid ([ValidDeviceName]); "" is returned for an
// invalid one, or when no suffix up to 1000 is free.
func UniqueDeviceName(name string, taken func(string) bool) string {
	if !ValidDeviceName(name) {
		return ""
	}
	if !taken(name) {
		return name
	}
	for n := 2; n <= 1000; n++ {
		suffix := "-" + strconv.Itoa(n)
		base := name
		if len(base)+len(suffix) > maxDeviceNameLen {
			base = base[:maxDeviceNameLen-len(suffix)]
		}
		if c := base + suffix; !taken(c) {
			return c
		}
	}
	return ""
}

// Caps on heartbeat list sizes and slot counts.
const (
	MaxHeartbeatAgents = 8
	MaxHeartbeatRepos  = 500
	MaxLiveLeases      = 256
	MaxFreeSlots       = 1024
	MaxFindingsItems   = 200
	MaxOfferItems      = 200
)

// ValidOrg reports whether s is an acceptable org slug.
func ValidOrg(s string) bool { return orgRe.MatchString(s) }

// EnrollRequest (POST /v1/enroll) trades a one-time enroll token for device
// membership. It is signed like every other request, with the key being
// enrolled: the hub verifies it against PublicKey, which proves possession.
type EnrollRequest struct {
	Token      string `json:"token"`
	DeviceName string `json:"device_name"`
	PublicKey  string `json:"public_key"` // EncodePublicKey form
}

// Validate checks the request's shape.
func (r EnrollRequest) Validate() error {
	if !tokenRe.MatchString(r.Token) {
		return invalid("token", "bad token")
	}
	if len(r.DeviceName) == 0 || len(r.DeviceName) > maxDeviceNameLen || !deviceNameRe.MatchString(r.DeviceName) {
		return invalid("device_name", "bad device name")
	}
	if _, err := DecodePublicKey(r.PublicKey); err != nil {
		return invalid("public_key", "bad public key")
	}
	return nil
}

// EnrollResponse answers an enrollment. Member is the label the admin set on
// the enroll token (empty for an unlabelled token, or a hub that predates
// members). DeviceName is the display name the hub stored, which may carry a
// "-2" style suffix ([UniqueDeviceName]); empty from an older hub.
type EnrollResponse struct {
	DeviceID   string `json:"device_id"`
	Org        string `json:"org"`
	Member     string `json:"member,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
}

// Validate checks the response's shape.
func (r EnrollResponse) Validate() error {
	if !ValidDeviceID(r.DeviceID) {
		return invalid("device_id", "bad device id")
	}
	if !ValidOrg(r.Org) {
		return invalid("org", "bad org")
	}
	if r.Member != "" && !ValidMemberLabel(r.Member) {
		return invalid("member", "bad member label")
	}
	if r.DeviceName != "" && !ValidDeviceName(r.DeviceName) {
		return invalid("device_name", "bad device name")
	}
	return nil
}

// HeartbeatRequest (POST /v1/heartbeat) reports capacity and renews leases.
// PolicyVersion is the policy version the daemon currently APPLIES (0 = none),
// so the org view can show who lags; omitted at 0, so a heartbeat without a
// policy is byte-identical to the old frame.
type HeartbeatRequest struct {
	FreeSlots     int      `json:"free_slots"`
	Agents        []string `json:"agents"`      // installed coding agents: claude, codex, …
	Repos         []string `json:"repos"`       // owner/name slugs the member opted in
	LiveLeases    []string `json:"live_leases"` // item ids this device still works
	PolicyVersion int      `json:"policy_version,omitempty"`
}

// Validate checks the request's shape.
func (r HeartbeatRequest) Validate() error {
	if r.FreeSlots < 0 || r.FreeSlots > MaxFreeSlots {
		return invalid("free_slots", "out of range")
	}
	if r.PolicyVersion < 0 {
		return invalid("policy_version", "negative")
	}
	if len(r.Agents) > MaxHeartbeatAgents {
		return invalid("agents", "too many")
	}
	for _, a := range r.Agents {
		if !agentNameRe.MatchString(a) {
			return invalid("agents", "bad agent name")
		}
	}
	if len(r.Repos) > MaxHeartbeatRepos {
		return invalid("repos", "too many")
	}
	for _, repo := range r.Repos {
		if err := ValidateRepo(repo); err != nil {
			return err
		}
	}
	if len(r.LiveLeases) > MaxLiveLeases {
		return invalid("live_leases", "too many")
	}
	for _, id := range r.LiveLeases {
		if !ValidItemID(id) {
			return invalid("live_leases", "bad item id")
		}
	}
	return nil
}

// HeartbeatResponse: Renewed carries the leases with their new expiry; Lost
// names live_leases the hub no longer holds for this device (expired and
// reclaimed, or finished elsewhere) — the daemon should stop that work.
type HeartbeatResponse struct {
	Renewed []Lease  `json:"renewed"`
	Lost    []string `json:"lost"`
}

// FindingsRequest (POST /v1/findings) reports validated findings.
type FindingsRequest struct {
	Items []Item `json:"items"`
}

// Validate checks the list size and every item. Items must carry no ID.
func (r FindingsRequest) Validate() error {
	if len(r.Items) > MaxFindingsItems {
		return invalid("items", "too many")
	}
	for i, it := range r.Items {
		if it.ID != "" {
			return invalid("items["+strconv.Itoa(i)+"].id", "findings carry no id")
		}
		if err := Validate(it); err != nil {
			return fmt.Errorf("items[%d]: %w", i, err)
		}
	}
	return nil
}

// Rejection names one refused finding by its index in the request.
type Rejection struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

// FindingsResponse counts accepted findings (new or deduplicated) and lists
// the rejected ones.
type FindingsResponse struct {
	Accepted int         `json:"accepted"`
	Rejected []Rejection `json:"rejected"`
}

// OffersResponse (GET /v1/offers) lists open items this device may claim.
// A daemon MUST [Validate] each one and skip any that fails.
type OffersResponse struct {
	Items []Item `json:"items"`
}

// Delivery mirrors lola's delivery axis (internal/state.DeliveryState): where
// the PR stands when the outcome is reported.
type Delivery string

const (
	DeliveryNone             Delivery = "none"
	DeliveryDraft            Delivery = "draft"
	DeliveryCIPending        Delivery = "ci_pending"
	DeliveryCIFailed         Delivery = "ci_failed"
	DeliveryMergeConflict    Delivery = "merge_conflict"
	DeliveryChangesRequested Delivery = "changes_requested"
	DeliveryReviewPending    Delivery = "review_pending"
	DeliveryApproved         Delivery = "approved"
	DeliveryMerged           Delivery = "merged"
	DeliveryClosed           Delivery = "closed"
)

// Valid reports whether d is a known delivery state.
func (d Delivery) Valid() bool {
	switch d {
	case DeliveryNone, DeliveryDraft, DeliveryCIPending, DeliveryCIFailed, DeliveryMergeConflict,
		DeliveryChangesRequested, DeliveryReviewPending, DeliveryApproved, DeliveryMerged, DeliveryClosed:
		return true
	}
	return false
}

// CompleteRequest (POST /v1/items/{id}/complete) reports the outcome PR.
type CompleteRequest struct {
	PRURL    string   `json:"pr_url"`
	Delivery Delivery `json:"delivery"`
}

// Validate checks the PR URL shape and the delivery state.
func (r CompleteRequest) Validate() error {
	if _, _, err := ParsePRURL(r.PRURL); err != nil {
		return err
	}
	if !r.Delivery.Valid() {
		return invalid("delivery", "unknown")
	}
	return nil
}

// ProgressRequest (POST /v1/items/{id}/progress) reports the PR opened for an
// item and where it stands. Same shape and rules as [CompleteRequest]; the
// difference is the state transition (claimed -> in_review, or an update
// while in_review), not the payload.
type ProgressRequest struct {
	PRURL    string   `json:"pr_url"`
	Delivery Delivery `json:"delivery"`
}

// Validate checks the PR URL shape and the delivery state.
func (r ProgressRequest) Validate() error {
	return CompleteRequest(r).Validate()
}

// FailReason is the closed enum of failure reasons.
type FailReason string

const (
	FailSpawn    FailReason = "spawn_failed"     // the session could not start
	FailAgent    FailReason = "agent_gave_up"    // the agent ended without a PR
	FailCI       FailReason = "ci_failed"        // the PR stayed red
	FailPRClosed FailReason = "pr_closed"        // the PR was closed unmerged
	FailTimeout  FailReason = "timeout"          // no outcome in time
	FailRefused  FailReason = "refused"          // local policy refused after claim
	FailSandbox  FailReason = "sandbox_required" // project not sandboxed
	FailObsolete FailReason = "not_reproducible" // finding no longer holds
)

// Valid reports whether f is a known fail reason.
func (f FailReason) Valid() bool {
	switch f {
	case FailSpawn, FailAgent, FailCI, FailPRClosed, FailTimeout, FailRefused, FailSandbox, FailObsolete:
		return true
	}
	return false
}

// FailRequest (POST /v1/items/{id}/fail). PRURL optionally names the PR the
// attempt left behind. Group ([ValidFailGroup]) optionally ties failures
// together: items failed with the same group escalate as ONE Linear issue
// listing them ([GroupKey]).
type FailRequest struct {
	Reason FailReason `json:"reason"`
	PRURL  string     `json:"pr_url,omitempty"`
	Group  string     `json:"group,omitempty"`
}

// Validate checks the reason, and the PR URL and group when present.
func (r FailRequest) Validate() error {
	if !r.Reason.Valid() {
		return invalid("reason", "unknown")
	}
	if r.PRURL != "" {
		if err := ValidatePRURL(r.PRURL); err != nil {
			return err
		}
	}
	if r.Group != "" && !ValidFailGroup(r.Group) {
		return invalid("group", "bad group")
	}
	return nil
}

var prURLRe = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/pull/([1-9][0-9]{0,9})$`)

// MaxPRURLLen caps a PR URL.
const MaxPRURLLen = 256

// ParsePRURL accepts ONLY https://github.com/<owner>/<repo>/pull/<n> — no
// query, fragment, trailing slash, userinfo, port or other host — and returns
// the repo slug and PR number.
func ParsePRURL(s string) (repo string, number int, err error) {
	if len(s) > MaxPRURLLen {
		return "", 0, invalid("pr_url", "too long")
	}
	m := prURLRe.FindStringSubmatch(s)
	if m == nil {
		return "", 0, invalid("pr_url", "not a github.com pull request URL")
	}
	repo = m[1] + "/" + m[2]
	if err := ValidateRepo(repo); err != nil {
		return "", 0, invalid("pr_url", "bad repo")
	}
	n, err := strconv.Atoi(m[3])
	if err != nil || n <= 0 || n > MaxPRNumber {
		return "", 0, invalid("pr_url", "bad PR number")
	}
	return repo, n, nil
}

// ValidatePRURL is [ParsePRURL] without the results.
func ValidatePRURL(s string) error { _, _, err := ParsePRURL(s); return err }

// ErrTooLarge is returned by [DecodeStrict] for a body over [MaxBody].
var ErrTooLarge = errors.New("hubproto: body too large")

// DecodeStrict decodes exactly one JSON value from r into v: at most
// [MaxBody] bytes, unknown fields rejected (at every depth), trailing data
// rejected. Both sides decode every frame with it.
func DecodeStrict(r io.Reader, v any) error {
	data, err := io.ReadAll(io.LimitReader(r, MaxBody+1))
	if err != nil {
		return err
	}
	if len(data) > MaxBody {
		return ErrTooLarge
	}
	return decodeStrictBytes(data, v)
}

func decodeStrictBytes(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("hubproto: trailing data after JSON value")
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("hubproto: trailing data after JSON value")
	}
	return nil
}
