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
	// A team-admin request is bounded by the daemon at 45s (the hub may ask
	// Linear); the margin covers the socket round-trip.
	hubAdminTimeout = 60 * time.Second
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
	if d.FindingsHealth == nil {
		d.FindingsHealth = []protocol.HubRepoHealth{}
	}
	if d.OfferSkips == nil {
		d.OfferSkips = []protocol.HubOfferSkip{}
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

// CreateTeam creates a team with the operator's one-time create code and
// makes this Mac its first member and admin. Like Join, the code is a secret
// carried from the form to the daemon and no further.
func (s *HubService) CreateTeam(a protocol.HubCreateTeamArgs) (protocol.HubCreateTeamData, error) {
	a.Code = strings.TrimSpace(a.Code)
	a.Team = strings.TrimSpace(a.Team)
	a.Name = strings.TrimSpace(a.Name)
	var d protocol.HubCreateTeamData
	err := hubCall(protocol.CmdHubCreateTeam, a, &d, hubJoinTimeout)
	return d, err
}

// Invite mints one invite for a teammate (team admins only). The invite holds
// a one-time token: the frontend shows it once and keeps it nowhere.
func (s *HubService) Invite(a protocol.HubInviteArgs) (protocol.HubInviteData, error) {
	a.Label = strings.TrimSpace(a.Label)
	var d protocol.HubInviteData
	err := hubCall(protocol.CmdHubInvite, a, &d, hubAdminTimeout)
	return d, err
}

// TeamSettings reads the team's settings status, or changes them when a
// field is set (team admins only). Secrets go in and never come back.
func (s *HubService) TeamSettings(a protocol.HubTeamSettingsArgs) (protocol.HubTeamSettingsData, error) {
	var d protocol.HubTeamSettingsData
	err := hubCall(protocol.CmdHubTeamSettings, a, &d, hubAdminTimeout)
	if d.Teams == nil {
		d.Teams = []protocol.HubLinearTeam{}
	}
	return d, err
}

// RevokeDevice revokes a device of the team by its display name (team admins
// only).
func (s *HubService) RevokeDevice(name string) (protocol.HubRevokeData, error) {
	var d protocol.HubRevokeData
	err := hubCall(protocol.CmdHubRevoke, protocol.HubRevokeArgs{Device: strings.TrimSpace(name)}, &d, hubAdminTimeout)
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

// HubStatsDTO is the Team view's one stats line, summed here so the frontend
// does no date arithmetic: the last seven UTC days, today included.
// Available is false when the hub sent no days at all.
type HubStatsDTO struct {
	Available bool `json:"available"`
	// FixedThisWeek counts items done in the window; FailedThisWeek and
	// OpenedThisWeek the same for failed and newly reported items.
	FixedThisWeek  int `json:"fixedThisWeek"`
	FailedThisWeek int `json:"failedThisWeek"`
	OpenedThisWeek int `json:"openedThisWeek"`
	// AvgFixSeconds is the mean time from first report to done over the
	// window's fixes (0 = no fix in the window).
	AvgFixSeconds int64 `json:"avgFixSeconds"`
}

// hubStatsWeek is how many UTC days the stats line sums, today included.
const hubStatsWeek = 7

// Stats reads the org's daily counters and sums the last seven days.
func (s *HubService) Stats() (HubStatsDTO, error) {
	var v hubproto.StatsView
	// protocol.CmdHubStats answers hubproto.StatsView (the hub's GET
	// /v1/stats, signed by the daemon). A daemon that predates it answers
	// `unknown cmd`, which the Team view reads as "no stats line", not a fault.
	if err := hubCall(protocol.CmdHubStats, nil, &v, hubViewTimeout); err != nil {
		return HubStatsDTO{}, err
	}
	return hubStatsSummary(v, time.Now()), nil
}

// hubStatsSummary sums the days of v that fall in the week ending at now
// (UTC). A day that does not parse is skipped, never guessed at; the view was
// validated by the daemon, so that is a belt over the braces.
func hubStatsSummary(v hubproto.StatsView, now time.Time) HubStatsDTO {
	out := HubStatsDTO{Available: len(v.Days) > 0}
	today := now.UTC().Truncate(24 * time.Hour)
	from := today.AddDate(0, 0, -(hubStatsWeek - 1))
	var fixSecs int64
	for _, d := range v.Days {
		t, err := time.Parse(hubproto.StatsDateLayout, d.Date)
		if err != nil || t.Before(from) || t.After(today) {
			continue
		}
		out.FixedThisWeek += d.Done
		out.FailedThisWeek += d.Failed
		out.OpenedThisWeek += d.Opened
		fixSecs += d.FixSecondsSum
	}
	if out.FixedThisWeek > 0 {
		out.AvgFixSeconds = fixSecs / int64(out.FixedThisWeek)
	}
	return out
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
