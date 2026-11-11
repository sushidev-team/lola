package main

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/protocol"
)

// HubService is the Wails-bound bridge for the team hub: joining and leaving,
// the org board and devices, a person's claim and release, and "sync now".
// Every method is ONE socket round-trip (the same shape as SSHService) and it
// holds no state: the daemon owns the identity, the caches and the lease
// bookkeeping (internal/daemon/hubcmds.go).
//
// Join is the one method that carries a secret, the enrollment token, from the
// form to the daemon and no further. The daemon never logs, echoes or returns
// it, and Wails logs a bound call's arguments only at debug level, which
// desktop/main.go does not enable.
type HubService struct{}

// Timeouts per command class. Join is bounded by the daemon at 60s
// (hubJoinTimeout); the margin covers the socket round-trip. Board and org
// may reach the hub on a cold cache. A claim spawns a session, which the
// daemon bounds itself, so it takes the long class.
const (
	hubJoinTimeout = 90 * time.Second
	hubViewTimeout = 30 * time.Second
)

// hubCall is the seam over one hub round-trip (tests replace it).
var hubCall = func(cmd string, args, out any, timeout time.Duration) error {
	req := protocol.Request{Cmd: cmd}
	if args != nil {
		raw, err := json.Marshal(args)
		if err != nil {
			return err
		}
		req.Args = raw
	}
	return call(req, timeout, out)
}

// Status is the daemon's hub view: identity, connection, policy, sessions.
func (s *HubService) Status() (protocol.HubStatusData, error) {
	var d protocol.HubStatusData
	err := hubCall(protocol.CmdHubStatus, nil, &d, shortTimeout)
	normalizeHubStatus(&d)
	return d, err
}

// normalizeHubStatus keeps every list non-null for the frontend.
func normalizeHubStatus(d *protocol.HubStatusData) {
	if d.Sessions == nil {
		d.Sessions = []protocol.HubSessionInfo{}
	}
	if p := d.Policy; p != nil {
		if p.Keys == nil {
			p.Keys = []string{}
		}
		if p.Summary == nil {
			p.Summary = []string{}
		}
		if p.AppliedKeys == nil {
			p.AppliedKeys = []protocol.HubAppliedKey{}
		}
	}
}

// Join enrolls this machine with a hub. The token is trimmed and sent once.
func (s *HubService) Join(a protocol.HubJoinArgs) (protocol.HubJoinData, error) {
	a.URL = strings.TrimRight(strings.TrimSpace(a.URL), "/")
	a.Token = strings.TrimSpace(a.Token)
	a.Name = strings.TrimSpace(a.Name)
	var d protocol.HubJoinData
	err := hubCall(protocol.CmdHubJoin, a, &d, hubJoinTimeout)
	return d, err
}

// Leave drops the hub identity; forgetKey also deletes the device key.
func (s *HubService) Leave(forgetKey bool) (protocol.HubLeaveData, error) {
	var d protocol.HubLeaveData
	err := hubCall(protocol.CmdHubLeave, protocol.HubLeaveArgs{ForgetKey: forgetKey}, &d, hubViewTimeout)
	return d, err
}

// Board lists the org's chores. State "" = every state; fresh skips the cache.
func (s *HubService) Board(a protocol.HubBoardArgs) (protocol.HubBoardData, error) {
	var d protocol.HubBoardData
	err := hubCall(protocol.CmdHubBoard, a, &d, hubViewTimeout)
	if d.Items == nil {
		d.Items = []protocol.HubBoardItem{}
	}
	return d, err
}

// Org lists the org's devices and the current policy version.
func (s *HubService) Org() (protocol.HubOrgData, error) {
	var d protocol.HubOrgData
	err := hubCall(protocol.CmdHubOrg, nil, &d, hubViewTimeout)
	if d.Devices == nil {
		d.Devices = []protocol.HubDeviceInfo{}
	}
	return d, err
}

// Claim takes one item now and starts its session.
func (s *HubService) Claim(a protocol.HubClaimArgs) (protocol.HubClaimData, error) {
	var d protocol.HubClaimData
	err := hubCall(protocol.CmdHubClaim, a, &d, longTimeout)
	return d, err
}

// Release gives a claimed item back. The session keeps running.
func (s *HubService) Release(itemID string) (protocol.HubReleaseData, error) {
	var d protocol.HubReleaseData
	err := hubCall(protocol.CmdHubRelease, protocol.HubReleaseArgs{ItemID: itemID}, &d, hubViewTimeout)
	return d, err
}

// Sync drops the daemon's hub caches and runs one loop pass soon.
func (s *HubService) Sync() (protocol.HubSyncData, error) {
	var d protocol.HubSyncData
	err := hubCall(protocol.CmdHubSync, nil, &d, shortTimeout)
	return d, err
}

// DefaultDeviceName is the name a join uses when none is typed: this Mac's
// hostname reduced to what enrollment accepts. Local, no daemon needed.
func (s *HubService) DefaultDeviceName() string {
	return hub.DefaultDeviceName()
}

// HubKindDTO is one chore kind for the settings pickers.
type HubKindDTO struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// hubKindLabels are Sentence-case names for hubproto's kinds. A kind without
// one falls back to its wire id, so a new kind is still offered.
var hubKindLabels = map[hubproto.Kind]string{
	hubproto.KindDepAdvisory: "Security advisories",
	hubproto.KindRedBumpPR:   "Failing dependency bumps",
}

// Kinds lists every chore kind this build knows, in hubproto's order.
func (s *HubService) Kinds() []HubKindDTO {
	out := []HubKindDTO{}
	for _, k := range hubproto.AllKinds() {
		label := hubKindLabels[k]
		if label == "" {
			label = string(k)
		}
		out = append(out, HubKindDTO{ID: string(k), Label: label})
	}
	return out
}
