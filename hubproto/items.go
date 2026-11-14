package hubproto

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrInvalid wraps every validation failure in this package.
var ErrInvalid = errors.New("hubproto: invalid")

func invalid(field, why string) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, field, why)
}

// Kind is the closed enum of work-item kinds. A kind not listed here is
// invalid; a daemon never claims one.
type Kind string

const (
	// KindDepAdvisory: a security advisory affecting a dependency in a repo.
	// The daemon opens a NEW branch and asks the agent to upgrade to a fixed
	// version, adapt the code and make CI green.
	KindDepAdvisory Kind = "dep_advisory"
	// KindRedBumpPR: an open Dependabot/Renovate PR whose required checks
	// fail. The daemon opens a session ON that branch to fix the fallout.
	KindRedBumpPR Kind = "red_bump_pr"
)

// AllKinds lists every valid kind, in a stable order.
func AllKinds() []Kind { return []Kind{KindDepAdvisory, KindRedBumpPR} }

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return k == KindDepAdvisory || k == KindRedBumpPR }

// Severity of an advisory (GitHub's vocabulary).
type Severity string

const (
	SeverityLow      Severity = "low"
	SeverityModerate Severity = "moderate"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// Valid reports whether s is a known severity.
func (s Severity) Valid() bool {
	switch s {
	case SeverityLow, SeverityModerate, SeverityHigh, SeverityCritical:
		return true
	}
	return false
}

// Ecosystems a dep_advisory may name (GitHub advisory database ecosystems).
var ecosystems = map[string]bool{
	"actions": true, "composer": true, "erlang": true, "go": true, "maven": true,
	"npm": true, "nuget": true, "pip": true, "pub": true, "rubygems": true,
	"rust": true, "swift": true,
}

// ValidEcosystem reports whether s is an accepted ecosystem name.
func ValidEcosystem(s string) bool { return ecosystems[s] }

// Bot authors a red_bump_pr may name. Anything else is invalid: the kind is
// for dependency-bump PRs only, never a human's branch.
const (
	AuthorDependabot = "dependabot[bot]"
	AuthorRenovate   = "renovate[bot]"
)

// Length caps (bytes) per field.
const (
	MaxItemIDLen     = 64
	MaxRepoLen       = 140
	MaxPackageLen    = 214
	MaxVersionLen    = 64
	MaxRangeLen      = 128
	MaxAdvisoryLen   = 64
	MaxBranchLen     = 255
	MaxPRNumber      = 1_000_000_000
	maxRepoOwnerLen  = 39
	maxRepoNameLen   = 100
	maxEcosystemLen  = 16
	maxDeliveryLen   = 32
	maxDeviceNameLen = 64
)

var (
	itemIDRe  = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	repoRe    = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	advisoryR = regexp.MustCompile(`^(GHSA(-[23456789cfghjmpqrvwx]{4}){3}|CVE-\d{4}-\d{4,}|[A-Z]+-\d{4}-[A-Za-z0-9-]+)$`)
	ghsaRe    = regexp.MustCompile(`^GHSA(-[23456789cfghjmpqrvwx]{4}){3}$`)
	cveRe     = regexp.MustCompile(`^CVE-\d{4}-\d{4,}$`)
	// npm "@scope/name", maven "group:artifact", go "github.com/x/y", pip
	// "zope.interface", rust "serde_json", nuget "Newtonsoft.Json".
	packageRe = regexp.MustCompile(`^[A-Za-z0-9@][A-Za-z0-9@/._+:-]*$`)
	versionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]*$`)
	// A range is TOKENS, never prose (see ValidRange): an operator, a version
	// that starts with a digit (optionally "v"), or the two glued together.
	rangeOpRe      = regexp.MustCompile(`^(<=|>=|==|!=|~>|~=|<|>|=|~|\^|\*)$`)
	rangeVersionRe = regexp.MustCompile(`^[vV]?[0-9][A-Za-z0-9.+_*-]*$`)
	branchRe       = regexp.MustCompile(`^[A-Za-z0-9@._/+-]+$`)
)

// ValidItemID reports whether s is an acceptable hub item id.
func ValidItemID(s string) bool {
	return len(s) > 0 && len(s) <= MaxItemIDLen && itemIDRe.MatchString(s)
}

// ValidateRepo checks an "owner/name" GitHub slug. Beyond the pattern it
// refuses segments made only of dots ("../..") and segments starting with "-",
// because the slug reaches a gh argv and an API path on the daemon.
func ValidateRepo(s string) error {
	if len(s) == 0 || len(s) > MaxRepoLen || !repoRe.MatchString(s) {
		return invalid("repo", "must be owner/name")
	}
	owner, name, _ := strings.Cut(s, "/")
	if len(owner) > maxRepoOwnerLen || len(name) > maxRepoNameLen {
		return invalid("repo", "too long")
	}
	for _, seg := range []string{owner, name} {
		if strings.Trim(seg, ".") == "" || strings.HasPrefix(seg, "-") {
			return invalid("repo", "bad segment")
		}
	}
	return nil
}

// DepAdvisory are the fields of a KindDepAdvisory item.
type DepAdvisory struct {
	Repo       string   `json:"repo"`
	Ecosystem  string   `json:"ecosystem"`
	Package    string   `json:"package"`
	Vulnerable string   `json:"vulnerable"`
	Fixed      string   `json:"fixed,omitempty"`
	Advisory   string   `json:"advisory"`
	Severity   Severity `json:"severity"`
}

// Validate checks every field of d.
func (d DepAdvisory) Validate() error {
	if err := ValidateRepo(d.Repo); err != nil {
		return err
	}
	if len(d.Ecosystem) > maxEcosystemLen || !ValidEcosystem(d.Ecosystem) {
		return invalid("ecosystem", "unknown")
	}
	if err := validatePackage(d.Package); err != nil {
		return err
	}
	if !ValidRange(d.Vulnerable) {
		return invalid("vulnerable", "bad version range")
	}
	if d.Fixed != "" && (len(d.Fixed) > MaxVersionLen || !versionRe.MatchString(d.Fixed)) {
		return invalid("fixed", "bad version")
	}
	if err := validateAdvisory(d.Advisory); err != nil {
		return err
	}
	if !d.Severity.Valid() {
		return invalid("severity", "unknown")
	}
	return nil
}

// ValidRange reports whether s is a version range made only of range tokens:
// ">= 1.0.0, < 1.2.3", "< 4.17.21", "^2.0 || ~3.1", "= 1.0.0-rc.1", "*".
// The range reaches an agent's briefing, so it must not be able to carry a
// word: s is split on ",", "||" and spaces, and every token must be an
// operator (< <= > >= = == != ~ ~> ~= ^ *), a version starting with a digit
// (or "v" and a digit), or an operator glued to such a version. Anything
// else — a letter-led word, a pipe, a URL — fails.
func ValidRange(s string) bool {
	if len(s) == 0 || len(s) > MaxRangeLen || strings.TrimSpace(s) != s {
		return false
	}
	tokens := 0
	for _, clause := range strings.Split(s, "||") {
		for _, part := range strings.Split(clause, ",") {
			for _, tok := range strings.Fields(part) {
				if !validRangeToken(tok) {
					return false
				}
				tokens++
			}
		}
	}
	return tokens > 0
}

func validRangeToken(tok string) bool {
	if rangeOpRe.MatchString(tok) || rangeVersionRe.MatchString(tok) {
		return true
	}
	// An operator glued to its version: the longest operator prefix first.
	for _, op := range []string{"<=", ">=", "==", "!=", "~>", "~=", "<", ">", "=", "~", "^"} {
		if rest, ok := strings.CutPrefix(tok, op); ok {
			return rangeVersionRe.MatchString(rest)
		}
	}
	return false
}

// validateAdvisory applies the design's advisory pattern, plus one rule the
// pattern cannot express (RE2 has no lookahead): an id that STARTS like a GHSA
// or a CVE must be a well-formed one, so "GHSA-2024-x" or "CVE-2024-1" cannot
// pass as an OSV-style id.
func validateAdvisory(s string) error {
	if len(s) == 0 || len(s) > MaxAdvisoryLen || !advisoryR.MatchString(s) {
		return invalid("advisory", "not a GHSA/CVE/OSV id")
	}
	if (strings.HasPrefix(s, "GHSA-") && !ghsaRe.MatchString(s)) ||
		(strings.HasPrefix(s, "CVE-") && !cveRe.MatchString(s)) {
		return invalid("advisory", "malformed GHSA/CVE id")
	}
	return nil
}

func validatePackage(s string) error {
	if len(s) == 0 || len(s) > MaxPackageLen || !packageRe.MatchString(s) {
		return invalid("package", "bad package name")
	}
	return nil
}

// RedBumpPR are the fields of a KindRedBumpPR item. Package may be empty: a
// Dependabot GROUPED PR ("bump the npm_and_yarn group …") names no single
// package. The dedup key never used it, so it is unchanged.
type RedBumpPR struct {
	Repo    string `json:"repo"`
	PR      int    `json:"pr"`
	Branch  string `json:"branch"`
	Package string `json:"package"`
	Author  string `json:"author"`
}

// Validate checks every field of r.
func (r RedBumpPR) Validate() error {
	if err := ValidateRepo(r.Repo); err != nil {
		return err
	}
	if r.PR <= 0 || r.PR > MaxPRNumber {
		return invalid("pr", "must be a positive PR number")
	}
	if err := validateBranch(r.Branch); err != nil {
		return err
	}
	if r.Package != "" {
		if err := validatePackage(r.Package); err != nil {
			return err
		}
	}
	if r.Author != AuthorDependabot && r.Author != AuthorRenovate {
		return invalid("author", "not an allowed bot")
	}
	return nil
}

// validateBranch is a conservative subset of git's ref-name rules: no leading
// "-" (it reaches a git argv), no "..", no "//", no leading/trailing "/" or
// ".", no "@{", no ".lock" suffix.
func validateBranch(s string) error {
	bad := len(s) == 0 || len(s) > MaxBranchLen || !branchRe.MatchString(s) ||
		strings.HasPrefix(s, "-") || strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") ||
		strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") ||
		strings.Contains(s, "..") || strings.Contains(s, "//") || strings.Contains(s, "/.") ||
		strings.Contains(s, "@{") || s == "@" || strings.HasSuffix(s, ".lock")
	if bad {
		return invalid("branch", "bad branch name")
	}
	return nil
}

// Item is one unit of chore work: a tagged union. Kind names the variant, and
// exactly the matching payload field is set:
//
//	{"id":"…","kind":"dep_advisory","dep_advisory":{…}}
//	{"id":"…","kind":"red_bump_pr","red_bump_pr":{…}}
//
// ID is assigned by the hub; a finding a daemon reports has none.
type Item struct {
	ID          string       `json:"id,omitempty"`
	Kind        Kind         `json:"kind"`
	DepAdvisory *DepAdvisory `json:"dep_advisory,omitempty"`
	RedBumpPR   *RedBumpPR   `json:"red_bump_pr,omitempty"`
}

// Validate checks item: a known kind, exactly the matching payload, every
// field valid, and the ID (when present) well formed. A daemon calls it on
// every offer before claiming; a hub on every finding before storing.
func Validate(item Item) error {
	if item.ID != "" && !ValidItemID(item.ID) {
		return invalid("id", "bad item id")
	}
	switch item.Kind {
	case KindDepAdvisory:
		if item.DepAdvisory == nil || item.RedBumpPR != nil {
			return invalid("kind", "payload does not match kind")
		}
		return item.DepAdvisory.Validate()
	case KindRedBumpPR:
		if item.RedBumpPR == nil || item.DepAdvisory != nil {
			return invalid("kind", "payload does not match kind")
		}
		return item.RedBumpPR.Validate()
	}
	return invalid("kind", "unknown kind")
}

// Repo returns the item's repository slug, or "" for an invalid item.
func (item Item) Repo() string {
	switch {
	case item.Kind == KindDepAdvisory && item.DepAdvisory != nil:
		return item.DepAdvisory.Repo
	case item.Kind == KindRedBumpPR && item.RedBumpPR != nil:
		return item.RedBumpPR.Repo
	}
	return ""
}

// Severity returns the item's severity; only dep_advisory carries one.
func (item Item) Severity() Severity {
	if item.Kind == KindDepAdvisory && item.DepAdvisory != nil {
		return item.DepAdvisory.Severity
	}
	return ""
}

// DedupKey is the team-wide identity of a finding, prefixed with its kind:
//
//	dep_advisory|<repo>|<advisory>|<package>
//	red_bump_pr|<repo>|<pr>
//
// The repo is lowercased (GitHub slugs are case-insensitive). Call it on a
// validated item; an item whose kind/payload do not match yields "".
func DedupKey(item Item) string {
	switch {
	case item.Kind == KindDepAdvisory && item.DepAdvisory != nil:
		d := item.DepAdvisory
		return strings.Join([]string{string(item.Kind), strings.ToLower(d.Repo), d.Advisory, d.Package}, "|")
	case item.Kind == KindRedBumpPR && item.RedBumpPR != nil:
		r := item.RedBumpPR
		return strings.Join([]string{string(item.Kind), strings.ToLower(r.Repo), strconv.Itoa(r.PR)}, "|")
	}
	return ""
}

// GroupKey is the escalation key of a fail GROUP ([FailRequest.Group]): items
// failed with the same group escalate as ONE Linear issue listing them. It
// returns "" for an invalid group.
func GroupKey(group string) string {
	if !ValidFailGroup(group) {
		return ""
	}
	return "group|" + group
}

// EscalationIssueID is the stable Linear issue id (a UUID, RFC 9562 version 8)
// for escalating key in org. key is a [DedupKey] or a [GroupKey], NEVER an item
// id: an item that is reopened, re-found or re-created must land on the same
// Linear issue instead of filing a duplicate, and two orgs never collide. It
// returns "" for an invalid org or an empty key.
func EscalationIssueID(org, key string) string {
	if !ValidOrg(org) || key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("lola-hub escalation v1\x00" + org + "\x00" + key))
	u := sum[:16]
	u[6] = (u[6] & 0x0f) | 0x80 // version 8: custom, name-derived
	u[8] = (u[8] & 0x3f) | 0x80 // RFC 9562 variant
	h := hex.EncodeToString(u)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// State of an item on the hub.
type State string

const (
	StateOpen    State = "open"
	StateClaimed State = "claimed"
	// StateInReview: the device that held the lease opened a PR for the item
	// ([ActionProgress]). The lease is dropped and that device is recorded as
	// the item's OWNER; the item is not offered, and a re-report of the same
	// finding does not reopen it. Only the owner may move it on (progress,
	// complete, fail). An in_review item with no progress call for
	// [InReviewIdleReopen] reopens.
	StateInReview  State = "in_review"
	StateDone      State = "done"
	StateFailed    State = "failed"
	StateEscalated State = "escalated"
)

// AllStates lists every valid state, in lifecycle order.
func AllStates() []State {
	return []State{StateOpen, StateClaimed, StateInReview, StateDone, StateFailed, StateEscalated}
}

// Valid reports whether s is a known state.
func (s State) Valid() bool {
	switch s {
	case StateOpen, StateClaimed, StateInReview, StateDone, StateFailed, StateEscalated:
		return true
	}
	return false
}

// Lifecycle timings both sides agree on. The hub enforces them; a daemon reads
// them only to explain what it sees (a renew refused past MaxLeaseAge, an item
// it cannot claim during someone else's grace window).
const (
	// LeaseGraceFactor: when a lease expires the item is held back for its
	// previous holder for LeaseGraceFactor x the lease TTL. It is not offered
	// to any other device, and the previous holder's claim succeeds without
	// counting a new attempt (a laptop that slept is not a failure).
	LeaseGraceFactor = 2
	// DefaultMaxLeaseAge bounds how long renewals may keep one claim alive
	// (hub flag --max-lease-age). Past it a renew fails and the hub counts a
	// [FailTimeout] attempt.
	DefaultMaxLeaseAge = 24 * time.Hour
	// InReviewIdleReopen: an in_review item with no progress call for this long
	// reopens.
	InReviewIdleReopen = 14 * 24 * time.Hour
	// DefaultStaleAfter: an open item not re-reported for this long, while some
	// active device still reports its repo, is closed as failed with history
	// action [AttemptClosedStale] (hub flag --stale-after). Hub-side only.
	DefaultStaleAfter = 48 * time.Hour
	// DeviceCooldown: after a device fails, releases, refuses, or lets an item
	// expire past its grace window, that item is not offered to that device for
	// this long.
	DeviceCooldown = 6 * time.Hour
)

// Lease is a device's time-bounded claim on an item. It is renewed by the
// heartbeat; once Expires passes without renewal the item is held for the
// holder for the grace window ([LeaseGraceFactor]) and then reopens.
// ClaimedAt is when the claim began (renewals keep it); a hub that predates
// it omits it.
type Lease struct {
	ItemID    string    `json:"item_id"`
	Device    string    `json:"device"`
	Expires   time.Time `json:"expires"`
	ClaimedAt time.Time `json:"claimed_at,omitzero"`
}

// Validate checks the lease's shape.
func (l Lease) Validate() error {
	if !ValidItemID(l.ItemID) {
		return invalid("item_id", "bad item id")
	}
	if !ValidDeviceID(l.Device) {
		return invalid("device", "bad device id")
	}
	if l.Expires.IsZero() {
		return invalid("expires", "missing")
	}
	if !l.ClaimedAt.IsZero() && l.ClaimedAt.After(l.Expires) {
		return invalid("claimed_at", "after expires")
	}
	return nil
}
