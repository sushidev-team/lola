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
)

// HubHumanCommands are the hub commands no remote peer and no agent may send.
var HubHumanCommands = []string{CmdHubJoin, CmdHubLeave, CmdHubBoard, CmdHubOrg, CmdHubClaim, CmdHubRelease, CmdHubSync}

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
}

// HubBoardArgs is Request.Args for cmd=hubBoard. State "" = every state
// (open | claimed | done | failed | escalated); Limit 0 = the hub's default
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
	// Local is THIS machine's answer for the row.
	Local HubLocalInfo `json:"local"`
}

// HubLeaseInfo is who holds a claimed item.
type HubLeaseInfo struct {
	Mine       bool      `json:"mine"`
	DeviceName string    `json:"deviceName"`
	Expires    time.Time `json:"expires"`
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
}

// HubClaimArgs is Request.Args for cmd=hubClaim. Project names the
// [[project]] when several match the item's repo.
type HubClaimArgs struct {
	ItemID  string `json:"itemId"`
	Project string `json:"project,omitempty"`
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
	Released  bool   `json:"released,omitempty"`
}

// HubFindingsInfo is the last findings report.
type HubFindingsInfo struct {
	At       time.Time `json:"at"`
	Repos    int       `json:"repos"`
	Reported int       `json:"reported"`
	Accepted int       `json:"accepted"`
	Rejected int       `json:"rejected"`
}

// HubPolicyInfo is the verified team policy. Keys lists the settings it
// carries (hubproto.Settings.Keys: every present key is locked). Applied is
// whether this daemon enforces them; until the policy layer lands it is
// false and the policy is display-only.
type HubPolicyInfo struct {
	Version    int       `json:"version"`
	KeyID      string    `json:"keyId"`
	IssuedAt   time.Time `json:"issuedAt"`
	VerifiedAt time.Time `json:"verifiedAt,omitzero"`
	Keys       []string  `json:"keys"`
	Summary    []string  `json:"summary,omitempty"`
	Applied    bool      `json:"applied"`
}

// HubStatusData is Response.Data for cmd=hubStatus.
type HubStatusData struct {
	// Enabled mirrors [hub].enabled; Joined is whether an identity loaded.
	Enabled bool   `json:"enabled"`
	Joined  bool   `json:"joined"`
	URL     string `json:"url,omitempty"`
	Device  string `json:"device,omitempty"`
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
	Released     bool      `json:"released,omitempty"`
	Reported     string    `json:"reported,omitempty"`
	// Summary is lola's own label for the item ("GHSA-xxxx · lodash").
	Summary string `json:"summary,omitempty"`
}
