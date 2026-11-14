package protocol

import "time"

// CmdHubStatus is the read-only socket command behind `lola hub status`: the
// daemon's hub loop state (internal/daemon/hubwire.go). It changes nothing and
// carries no secret — the device key never leaves internal/hub — so a phone may
// read it.
const CmdHubStatus = "hubStatus"

// The HUMAN hub commands (internal/daemon/hubcmds.go). Every one is a person
// at this machine — the desktop app or the CLI — and none is reachable from a
// phone (internal/remote denies HubHumanCommands) or from the sandbox hook
// bridge (whose allowlist names none of them). The board and org views are in
// the list although they only read: they carry the whole team's work and
// device names, which are not a phone's to browse.
const (
	// CmdHubJoin enrolls this machine (Args: HubJoinArgs). The token is
	// never logged, echoed or returned.
	CmdHubJoin = "hubJoin"
	// CmdHubLeave drops the identity and disables the hub (Args: HubLeaveArgs).
	CmdHubLeave = "hubLeave"
	// CmdHubBoard is the org board (Args: HubBoardArgs → HubBoardData).
	CmdHubBoard = "hubBoard"
	// CmdHubOrg is the org's devices and policy version (→ HubOrgData).
	CmdHubOrg = "hubOrg"
	// CmdHubClaim is a person claiming one item now (Args: HubClaimArgs →
	// HubClaimData).
	CmdHubClaim = "hubClaim"
	// CmdHubRelease gives a claimed item back (Args: HubReleaseArgs →
	// HubReleaseData).
	CmdHubRelease = "hubRelease"
	// CmdHubSync runs one hub loop pass soon (coalesced, non-blocking).
	CmdHubSync = "hubSync"
	// CmdHubStats is the org's daily counters (the hub's GET /v1/stats,
	// signed by the daemon). It answers hubproto.StatsView as the hub sent
	// it (snake_case json), validated whole. Org data, so not a phone's.
	CmdHubStats = "hubStats"
	// CmdHubCreateTeam creates a team with a one-time create code and makes
	// this machine its first member and admin (Args: HubCreateTeamArgs →
	// HubCreateTeamData). The code is never logged, echoed or returned.
	CmdHubCreateTeam = "hubCreateTeam"
	// The TEAM-ADMIN commands. Each needs the team admin key on this machine
	// (state/hub-admin-key) and is countersigned with it; the key itself
	// never crosses the socket.
	//
	// CmdHubInvite mints one invite for a teammate (Args: HubInviteArgs →
	// HubInviteData). The invite is a secret and is returned ONCE.
	CmdHubInvite = "hubInvite"
	// CmdHubTeamSettings reads the team's settings status, or changes them
	// (Args: HubTeamSettingsArgs → HubTeamSettingsData). Secrets go in, never
	// out.
	CmdHubTeamSettings = "hubTeamSettings"
	// CmdHubRevoke revokes a device of the team by its display name (Args:
	// HubRevokeArgs → HubRevokeData).
	CmdHubRevoke = "hubRevoke"
)

// HubHumanCommands are the hub commands no remote peer and no agent may send.
var HubHumanCommands = []string{CmdHubJoin, CmdHubLeave, CmdHubBoard, CmdHubOrg, CmdHubClaim, CmdHubRelease, CmdHubSync, CmdHubStats,
	CmdHubCreateTeam, CmdHubInvite, CmdHubTeamSettings, CmdHubRevoke}

// HubCreateTeamArgs is Request.Args for cmd=hubCreateTeam.
type HubCreateTeamArgs struct {
	// Code is the whole create code from the hub operator
	// (lola-hub-create:<url>#<code>): it names the hub.
	Code string `json:"code"`
	// Team is the new team's name (lowercase letters, digits, dashes).
	Team string `json:"team"`
	// Name is this device's name ("" = the hostname).
	Name string `json:"name,omitempty"`
	// ReplaceAdminKey discards the admin key of ANOTHER team this machine
	// holds. Without it such a key refuses the create.
	ReplaceAdminKey bool `json:"replaceAdminKey,omitempty"`
}

// HubCreateTeamData answers cmd=hubCreateTeam. It never carries the code or
// a private key.
type HubCreateTeamData struct {
	Org        string `json:"org"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	URL        string `json:"url"`
	KeyCreated bool   `json:"keyCreated,omitempty"`
	// AdminKeyID and Fingerprint name the team admin key now on this machine
	// (the fingerprint is what a new member compares).
	AdminKeyID  string `json:"adminKeyId"`
	Fingerprint string `json:"fingerprint"`
}

// HubInviteArgs is Request.Args for cmd=hubInvite. TTLSeconds 0 = 7 days.
type HubInviteArgs struct {
	// Label names the member the invite is for.
	Label      string `json:"label"`
	TTLSeconds int    `json:"ttlSeconds,omitempty"`
}

// HubInviteData answers cmd=hubInvite. Invite is the one line a teammate
// pastes; it holds a one-time token, so it is shown once and stored nowhere.
type HubInviteData struct {
	Invite    string    `json:"invite"`
	ExpiresAt time.Time `json:"expiresAt"`
	// KeyID and Fingerprint are the policy key the invite carries: the admin
	// reads the fingerprint to the teammate over another channel.
	KeyID       string `json:"keyId"`
	Fingerprint string `json:"fingerprint"`
}

// HubTeamSettingsArgs is Request.Args for cmd=hubTeamSettings. With every
// pointer nil it only reads; a pointer to "" clears that setting. LinearKey
// and DigestWebhook are secrets: never logged, never echoed.
type HubTeamSettingsArgs struct {
	LinearKey     *string `json:"linearKey,omitempty"`
	LinearTeam    *string `json:"linearTeam,omitempty"`
	DigestWebhook *string `json:"digestWebhook,omitempty"`
	// Teams also lists the Linear workspace's teams (a read only).
	Teams bool `json:"teams,omitempty"`
}

// HubTeamSettingsData answers cmd=hubTeamSettings: the STATUS of the team's
// settings, validated whole. It carries no secret.
type HubTeamSettingsData struct {
	Org string `json:"org"`
	// SecretsAvailable is false when the hub cannot store a key or webhook.
	SecretsAvailable bool   `json:"secretsAvailable"`
	LinearKeySet     bool   `json:"linearKeySet"`
	LinearTeam       string `json:"linearTeam,omitempty"`
	// LinearError / DigestError are the hub's error classes ("" = none).
	LinearError string          `json:"linearError,omitempty"`
	DigestSet   bool            `json:"digestSet"`
	DigestError string          `json:"digestError,omitempty"`
	Teams       []HubLinearTeam `json:"teams"`
}

// HubLinearTeam is one team of the Linear workspace. Name may be "".
type HubLinearTeam struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name,omitempty"`
}

// HubRevokeArgs is Request.Args for cmd=hubRevoke.
type HubRevokeArgs struct {
	// Device is the device's display name, as the org view lists it.
	Device string `json:"device"`
}

// HubRevokeData answers cmd=hubRevoke.
type HubRevokeData struct {
	Revoked bool `json:"revoked"`
}

// HubJoinArgs is Request.Args for cmd=hubJoin. Name "" = the hostname.
type HubJoinArgs struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	Name  string `json:"name,omitempty"`
}

// HubJoinData answers cmd=hubJoin. It never carries the token.
type HubJoinData struct {
	Org        string `json:"org"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	URL        string `json:"url"`
	KeyCreated bool   `json:"keyCreated,omitempty"`
}

// HubLeaveArgs is Request.Args for cmd=hubLeave.
type HubLeaveArgs struct {
	// ForgetKey also deletes the device key (a later join is a new device).
	ForgetKey bool `json:"forgetKey,omitempty"`
}

// HubLeaveData answers cmd=hubLeave.
type HubLeaveData struct {
	Left       bool `json:"left"`
	KeyDeleted bool `json:"keyDeleted,omitempty"`
	// Released counts the leases the hub took back before the leave; Kept
	// those left to expire (unreachable hub, refused, or out of time). The
	// sessions behind both keep running.
	Released int `json:"released,omitempty"`
	Kept     int `json:"kept,omitempty"`
}

// HubBoardArgs is Request.Args for cmd=hubBoard. State "" = every state
// (open | claimed | in_review | done | failed | escalated); Limit 0 = the hub's default
// (200), at most 500.
type HubBoardArgs struct {
	State string `json:"state,omitempty"`
	Limit int    `json:"limit,omitempty"`
	// Fresh skips the daemon's 20s cache (a refresh button).
	Fresh bool `json:"fresh,omitempty"`
}

// HubBoardData answers cmd=hubBoard. Every row passed hubproto validation as
// part of the whole response; a hub that sent one bad row gets an error, not
// a partial board.
type HubBoardData struct {
	FetchedAt time.Time      `json:"fetchedAt"`
	Items     []HubBoardItem `json:"items"`
}

// HubBoardItem is one board row, flattened for the UIs. Label is lola's own
// short name ("GHSA-xxxx · lodash", "PR #12 · lodash").
type HubBoardItem struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"` // dep_advisory | red_bump_pr
	Repo      string    `json:"repo"`
	Label     string    `json:"label"`
	Package   string    `json:"package"`
	State     string    `json:"state"`
	Attempts  int       `json:"attempts"`
	Reporters int       `json:"reporters"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`

	// dep_advisory fields.
	Advisory   string `json:"advisory,omitempty"`
	Severity   string `json:"severity,omitempty"`
	Ecosystem  string `json:"ecosystem,omitempty"`
	Vulnerable string `json:"vulnerable,omitempty"`
	Fixed      string `json:"fixed,omitempty"`
	// red_bump_pr fields.
	PRNumber int    `json:"prNumber,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Author   string `json:"author,omitempty"`

	Lease      *HubLeaseInfo      `json:"lease,omitempty"`
	Outcome    *HubOutcomeInfo    `json:"outcome,omitempty"`
	Escalation *HubEscalationInfo `json:"escalation,omitempty"`
	// Owner is the device that opened the item's PR (in_review, done, failed,
	// escalated); nil before that.
	Owner *HubOwnerInfo `json:"owner,omitempty"`
	// History is the item's attempts, oldest first (at most 20).
	History []HubAttemptInfo `json:"history,omitempty"`
	// LastFail is the last fail reason ("" = none); Reopened counts how often
	// the hub put the item back on offer.
	LastFail string `json:"lastFail,omitempty"`
	Reopened int    `json:"reopened,omitempty"`
	// ClaimedAt is when the current claim began (claimed / in_review only).
	ClaimedAt time.Time `json:"claimedAt,omitzero"`
	// Local is THIS machine's answer for the row.
	Local HubLocalInfo `json:"local"`
}

// HubLeaseInfo is who holds a claimed item.
type HubLeaseInfo struct {
	Mine       bool      `json:"mine"`
	DeviceName string    `json:"deviceName"`
	Member     string    `json:"member,omitempty"`
	Expires    time.Time `json:"expires"`
}

// HubOwnerInfo is the device (and its member label) that owns an item in
// review or finished.
type HubOwnerInfo struct {
	Mine       bool   `json:"mine"`
	DeviceName string `json:"deviceName"`
	Member     string `json:"member,omitempty"`
}

// HubAttemptInfo is one entry of an item's history. Action is a
// hubproto.AttemptAction word, Reason a hubproto.FailReason ("" = none).
type HubAttemptInfo struct {
	DeviceName string    `json:"deviceName,omitempty"`
	Member     string    `json:"member,omitempty"`
	Action     string    `json:"action"`
	Reason     string    `json:"reason,omitempty"`
	PRURL      string    `json:"prUrl,omitempty"`
	At         time.Time `json:"at"`
}

// HubOutcomeInfo is the PR a device reported. PRURL is a github.com PR URL
// (hubproto.ParsePRURL); open it through cmd=openURL.
type HubOutcomeInfo struct {
	PRURL    string `json:"prUrl"`
	Delivery string `json:"delivery"`
}

// HubEscalationInfo is the Linear issue an escalated item became. URL is
// https://linear.app/... only.
type HubEscalationInfo struct {
	Pending    bool   `json:"pending"`
	Identifier string `json:"identifier,omitempty"`
	URL        string `json:"url,omitempty"`
}

// HubLocalInfo is whether THIS machine could claim the row now. Claimable
// with an empty Project and several Projects means the claim must name one.
// Session is the local session already working the item.
type HubLocalInfo struct {
	Claimable bool     `json:"claimable"`
	Reason    string   `json:"reason,omitempty"`
	Project   string   `json:"project,omitempty"`
	Projects  []string `json:"projects,omitempty"`
	Session   string   `json:"session,omitempty"`
}

// HubOrgData answers cmd=hubOrg.
type HubOrgData struct {
	FetchedAt     time.Time       `json:"fetchedAt"`
	Org           string          `json:"org"`
	PolicyVersion int             `json:"policyVersion"`
	Devices       []HubDeviceInfo `json:"devices"`
}

// HubDeviceInfo is one member device. LastSeen zero = never heartbeat.
type HubDeviceInfo struct {
	Name      string    `json:"name"`
	Me        bool      `json:"me"`
	LastSeen  time.Time `json:"lastSeen,omitzero"`
	FreeSlots int       `json:"freeSlots"`
	Agents    []string  `json:"agents"`
	Active    bool      `json:"active"`
	// Member is the admin-set label of the person the device belongs to;
	// PolicyVersion the team policy version it applies (0 = none / unknown).
	Member        string `json:"member,omitempty"`
	PolicyVersion int    `json:"policyVersion,omitempty"`
}

// HubClaimArgs is Request.Args for cmd=hubClaim. Project names the
// [[project]] when several match the item's repo.
type HubClaimArgs struct {
	ItemID string `json:"itemId"`
	// ItemIDs claims several dependency advisories of one repository for ONE
	// session ("Fix all"). When set, ItemID is ignored.
	ItemIDs []string `json:"itemIds,omitempty"`
	Project string   `json:"project,omitempty"`
	// Scope widens the session beyond the advisories: update the
	// repository's other dependencies too ("Update all dependencies").
	Scope *HubUpdateScope `json:"scope,omitempty"`
}

// HubUpdateScope is what an "update all dependencies" session upgrades
// besides the claimed advisories, and the person's own note for the agent.
type HubUpdateScope struct {
	Patch bool   `json:"patch"`
	Minor bool   `json:"minor"`
	Major bool   `json:"major"`
	Note  string `json:"note,omitempty"`
}

// HubClaimData answers a successful cmd=hubClaim.
type HubClaimData struct {
	Session string `json:"session"`
	Project string `json:"project"`
	ItemID  string `json:"itemId"`
}

// HubReleaseArgs is Request.Args for cmd=hubRelease.
type HubReleaseArgs struct {
	ItemID string `json:"itemId"`
}

// HubReleaseData answers cmd=hubRelease. Session is the local session that
// held the lease: it is NOT stopped (SessionKept), only no longer reported
// to the hub. Queued: the hub could not be reached and the release waits in
// the outbox.
type HubReleaseData struct {
	ItemID      string `json:"itemId"`
	Session     string `json:"session,omitempty"`
	SessionKept bool   `json:"sessionKept,omitempty"`
	Queued      bool   `json:"queued,omitempty"`
	Message     string `json:"message"`
}

// HubSyncData answers cmd=hubSync.
type HubSyncData struct {
	Queued bool `json:"queued"`
}

// HubSessionHub is SessionInfo.hub: what a hub session works on.
type HubSessionHub struct {
	ItemID    string `json:"itemId"`
	Kind      string `json:"kind"`
	Label     string `json:"label,omitempty"`
	LeaseLost bool   `json:"leaseLost,omitempty"`
	// LeaseTaken: the lease was lost AND another device holds the item now
	// (a re-claim was refused). The session keeps running either way.
	LeaseTaken bool `json:"leaseTaken,omitempty"`
	// InReview: the hub took the PR (progress acknowledged); no lease is
	// renewed any more, the item waits for merge or close.
	InReview bool `json:"inReview,omitempty"`
	Released bool `json:"released,omitempty"`
}

// HubFindingsInfo is the last findings report.
type HubFindingsInfo struct {
	At       time.Time `json:"at"`
	Repos    int       `json:"repos"`
	Reported int       `json:"reported"`
	Accepted int       `json:"accepted"`
	Rejected int       `json:"rejected"`
}

// HubRepoHealth is the last findings scan of one opted-in repository.
// Alerts: "ok" | "no_access" (Dependabot alerts disabled or not visible:
// gh answered 403/404) | "error"; PullRequests: "ok" | "error". Error is the
// first gh failure in lola's words plus gh's clipped stderr.
type HubRepoHealth struct {
	Repo         string    `json:"repo"`
	At           time.Time `json:"at"`
	Alerts       string    `json:"alerts"`
	PullRequests string    `json:"pullRequests"`
	Error        string    `json:"error,omitempty"`
}

// HubRepoHealth states.
const (
	HubHealthOK       = "ok"
	HubHealthNoAccess = "no_access"
	HubHealthError    = "error"
)

// HubOfferSkip is one offer the last cycle did not claim, and why. Reason is
// one of the stable hub.Reason* words (internal/hub/accept.go).
type HubOfferSkip struct {
	ItemID string    `json:"itemId"`
	Kind   string    `json:"kind"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// HubPolicyInfo is the verified team policy. Keys lists the settings it
// carries (hubproto.Settings.Keys: every present key is locked). Applied is
// whether the daemon's LIVE config enforces them; AppliedKeys is what it
// enforced (team value beside the local one — a settings UI shows those keys
// as locked), and ApplyNote says why it is not applied ("[hub].apply_policy =
// false", "applies on the next reload", ...). escalate_after is read by the
// hub only, so it is in Keys but never in AppliedKeys.
type HubPolicyInfo struct {
	Version     int             `json:"version"`
	KeyID       string          `json:"keyId"`
	IssuedAt    time.Time       `json:"issuedAt"`
	VerifiedAt  time.Time       `json:"verifiedAt,omitzero"`
	Keys        []string        `json:"keys"`
	Summary     []string        `json:"summary,omitempty"`
	Applied     bool            `json:"applied"`
	AppliedKeys []HubAppliedKey `json:"appliedKeys,omitempty"`
	ApplyNote   string          `json:"applyNote,omitempty"`
}

// HubAppliedKey is one locked key: the team's value and what the local
// config.toml says (both human-readable).
type HubAppliedKey struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Local string `json:"local"`
}

// HubStatusData is Response.Data for cmd=hubStatus.
type HubStatusData struct {
	// Enabled mirrors [hub].enabled; Joined is whether an identity loaded.
	Enabled bool `json:"enabled"`
	Joined  bool `json:"joined"`
	// Membership is the one-word answer to "is this machine in a team":
	// "joined", "revoked" (the hub refused this device's signature; a new
	// token and join are needed) or "not joined" (hub.Status.Membership).
	Membership string `json:"membership,omitempty"`
	URL        string `json:"url,omitempty"`
	Device     string `json:"device,omitempty"`
	// DeviceName is the name this device enrolled with.
	DeviceName string `json:"deviceName,omitempty"`
	Org        string `json:"org,omitempty"`
	// LastHeartbeat is the last SUCCESSFUL heartbeat; LastError the last
	// failure of any stage, in lola's words plus the sanitized hub error.
	LastHeartbeat time.Time `json:"lastHeartbeat,omitzero"`
	LastError     string    `json:"lastError,omitempty"`
	LastErrorAt   time.Time `json:"lastErrorAt,omitzero"`
	// ClaimsToday counts claims on the local day against MaxClaimsPerDay.
	ClaimsToday     int `json:"claimsToday"`
	MaxClaimsPerDay int `json:"maxClaimsPerDay"`
	// PendingOutcomes are outcomes (complete / fail / release) queued for a
	// hub that has not acknowledged them yet.
	PendingOutcomes int `json:"pendingOutcomes"`
	// PolicyVersion is the last verified policy's version (0 = none). The
	// policy is DISPLAY ONLY in this phase.
	PolicyVersion int `json:"policyVersion,omitempty"`
	// Policy is the stored, re-verified policy (nil = none).
	Policy *HubPolicyInfo `json:"policy,omitempty"`
	// LastFindings is the last findings report (nil = none since start).
	LastFindings *HubFindingsInfo `json:"lastFindings,omitempty"`
	// Sessions are the local sessions working a hub item.
	Sessions []HubSessionInfo `json:"sessions,omitempty"`
	// LastSync is the end of the last cycle in which no stage failed;
	// LastError is cleared then.
	LastSync time.Time `json:"lastSync,omitzero"`
	// FindingsHealth is the last scan's answer per repository.
	FindingsHealth []HubRepoHealth `json:"findingsHealth,omitempty"`
	// OfferSkips are the offers the last cycle left unclaimed.
	OfferSkips []HubOfferSkip `json:"offerSkips,omitempty"`
	// LegacyHub: the hub predates the chore lifecycle (no progress frame, no
	// fail pr_url / group, no heartbeat policy_version); lola falls back to
	// the old frames for a while and then tries again.
	LegacyHub bool `json:"legacyHub,omitempty"`
	// Admin: this machine holds the admin key of the joined team
	// (state/hub-admin-key). The hub still decides on every request.
	Admin bool `json:"admin,omitempty"`
}

// HubSessionInfo is one local session working a hub item.
type HubSessionInfo struct {
	Session      string    `json:"session"`
	Project      string    `json:"project"`
	ItemID       string    `json:"itemId"`
	Kind         string    `json:"kind"`
	ClaimedAt    time.Time `json:"claimedAt,omitzero"`
	LeaseExpires time.Time `json:"leaseExpires,omitzero"`
	LeaseLost    bool      `json:"leaseLost,omitempty"`
	LeaseTaken   bool      `json:"leaseTaken,omitempty"`
	InReview     bool      `json:"inReview,omitempty"`
	// Progress is the delivery last reported to the hub for the PR.
	Progress string `json:"progress,omitempty"`
	Released bool   `json:"released,omitempty"`
	Reported string `json:"reported,omitempty"`
	// Summary is lola's own label for the item ("GHSA-xxxx · lodash").
	Summary string `json:"summary,omitempty"`
}
