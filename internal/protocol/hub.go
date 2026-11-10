package protocol

import "time"

// CmdHubStatus is the read-only socket command behind `lola hub status`: the
// daemon's hub loop state (internal/daemon/hubwire.go). It changes nothing and
// carries no secret — the device key never leaves internal/hub — so a phone may
// read it. Every hub WRITE (join, leave) is a local CLI edit of config followed
// by `reload`, which internal/remote already denies.
const CmdHubStatus = "hubStatus"

// HubStatusData is Response.Data for cmd=hubStatus.
type HubStatusData struct {
	// Enabled mirrors [hub].enabled; Joined is whether an identity loaded.
	Enabled bool   `json:"enabled"`
	Joined  bool   `json:"joined"`
	URL     string `json:"url,omitempty"`
	Device  string `json:"device,omitempty"`
	Org     string `json:"org,omitempty"`
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
	Reported     string    `json:"reported,omitempty"`
}
