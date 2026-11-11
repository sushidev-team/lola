package daemon

// hubcmds.go — the HUMAN hub commands the desktop app and the CLI send
// (protocol.HubHumanCommands): join, leave, the read-only board and org views,
// a person's claim and release, and "sync now". Rules this file keeps
// (AGENTS.md, "Hub work is a CLAIM and a candidate"):
//
//   - Every command here is a person at this machine. internal/remote denies
//     the whole list (a phone gets none of it) and the sandbox hook bridge's
//     allowlist names none of it.
//   - The board and org views are DISPLAY data from the hub: decoded with
//     hubproto.MaxViewBody and validated whole by internal/hub, so a bad row
//     refuses the response rather than reaching a client. They are cached
//     (hubBoardTTL / hubOrgTTL) so a UI polling them cannot flood the hub.
//   - A person's claim goes through hubClaimItem, the loop's own path, with
//     the human rules: the click is the opt-in (no hub_chores) and intent (no
//     concurrency cap, no daily count), while require_sandbox, the kind
//     filter, dispatchHold, the health gate and the GitHub re-read all hold.
//   - A release never stops a session: the session is marked Released and
//     lola stops renewing and reporting for it.
//   - The enroll token is never logged, echoed or returned; an error that
//     happens to contain it has it scrubbed.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

const (
	// hubBoardTTL / hubOrgTTL bound how often a UI's polling reaches the hub.
	hubBoardTTL = 20 * time.Second
	hubOrgTTL   = 60 * time.Second
	// hubJoinTimeout bounds enrollment end to end.
	hubJoinTimeout = 60 * time.Second
	// hubTokenMax bounds the enroll token a client may send.
	hubTokenMax = 4 << 10
)

// hubBoardCache is one cached board response.
type hubBoardCache struct {
	at   time.Time
	resp hubproto.BoardResponse
}

// handleHub dispatches the human hub commands.
func (d *Daemon) handleHub(ctx context.Context, req protocol.Request) protocol.Response {
	if d.hub == nil {
		return protocol.Response{OK: false, Error: "hub: not available in this daemon"}
	}
	var (
		data any
		err  error
	)
	switch req.Cmd {
	case protocol.CmdHubJoin:
		var a protocol.HubJoinArgs
		if err = hubArgs(req, &a); err == nil {
			data, err = d.handleHubJoin(ctx, a)
		}
	case protocol.CmdHubLeave:
		var a protocol.HubLeaveArgs
		if err = hubArgs(req, &a); err == nil {
			data, err = d.handleHubLeave(ctx, a)
		}
	case protocol.CmdHubBoard:
		var a protocol.HubBoardArgs
		if err = hubArgs(req, &a); err == nil {
			data, err = d.handleHubBoard(ctx, a)
		}
	case protocol.CmdHubOrg:
		data, err = d.handleHubOrg(ctx)
	case protocol.CmdHubClaim:
		var a protocol.HubClaimArgs
		if err = hubArgs(req, &a); err == nil {
			data, err = d.handleHubClaim(ctx, a)
		}
	case protocol.CmdHubRelease:
		var a protocol.HubReleaseArgs
		if err = hubArgs(req, &a); err == nil {
			data, err = d.handleHubRelease(ctx, a)
		}
	case protocol.CmdHubSync:
		d.hubDropCaches()
		d.hubPolicyDue()
		d.hubNudge()
		data = protocol.HubSyncData{Queued: true}
	default:
		err = fmt.Errorf("unknown cmd %q", req.Cmd)
	}
	if err != nil {
		return protocol.Response{OK: false, Error: err.Error()}
	}
	return dataResponse(data)
}

// hubArgs decodes Request.Args (absent = zero value). The error never quotes
// the payload: it may carry an enroll token.
func hubArgs(req protocol.Request, v any) error {
	if len(req.Args) == 0 {
		return nil
	}
	if err := json.Unmarshal(req.Args, v); err != nil {
		return errors.New("hub: bad args")
	}
	return nil
}

// hubHumanClient is the client a human command uses. Unlike hubConnect it
// does not touch the loop's error line: a person's failed click is answered
// to the person.
func (d *Daemon) hubHumanClient() (hubAPI, hubSnapshot, error) {
	snap, ok := d.hubConfig()
	if !ok {
		d.mu.Lock()
		cfgErr := d.cfgErr
		d.mu.Unlock()
		if cfgErr != "" {
			return nil, hubSnapshot{}, errors.New("hub: the config does not validate: " + cfgErr)
		}
		return nil, hubSnapshot{}, errors.New("hub: not enabled (join a hub first)")
	}
	api, _, err := d.hub.newClient(snap.h.URL, d.hubDir())
	if err != nil {
		if errors.Is(err, hub.ErrNotJoined) {
			return nil, snap, errors.New("hub: not joined (join a hub first)")
		}
		return nil, snap, err
	}
	return api, snap, nil
}

// ---- join / leave ----------------------------------------------------------

func (d *Daemon) handleHubJoin(ctx context.Context, a protocol.HubJoinArgs) (protocol.HubJoinData, error) {
	token := strings.TrimSpace(a.Token)
	switch {
	case token == "":
		return protocol.HubJoinData{}, errors.New("hub: the one-time token is required")
	case len(token) > hubTokenMax || strings.ContainsAny(token, " \t\r\n"):
		return protocol.HubJoinData{}, errors.New("hub: the token must be a single line with no spaces")
	}
	path, err := config.DefaultPath()
	if err != nil {
		return protocol.HubJoinData{}, err
	}
	// Shielded: a client that hangs up mid-enroll must not leave a key
	// enrolled on the hub with no identity written here.
	jctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hubJoinTimeout)
	defer cancel()
	res, err := hub.Join(jctx, hub.JoinOptions{
		URL: a.URL, Token: token, DeviceName: strings.TrimSpace(a.Name),
		ConfigPath: path, StateDir: d.hubDir(), NewClient: d.hub.enroll,
	})
	if err != nil {
		return protocol.HubJoinData{}, errors.New(strings.ReplaceAll(err.Error(), token, "[token]"))
	}
	d.hubResetIdentity()
	d.logf("", "hub: joined org %s as %q (device %s) at %s", res.Identity.Org, res.Identity.DeviceName, res.Identity.DeviceID, res.Identity.URL)
	out := protocol.HubJoinData{Org: res.Identity.Org, DeviceID: res.Identity.DeviceID, DeviceName: res.Identity.DeviceName, URL: res.Identity.URL, KeyCreated: res.KeyCreated}
	if err := d.handleReload(ctx); err != nil {
		return out, fmt.Errorf("joined org %s, but the daemon did not reload: %v", res.Identity.Org, err)
	}
	return out, nil
}

func (d *Daemon) handleHubLeave(ctx context.Context, a protocol.HubLeaveArgs) (protocol.HubLeaveData, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return protocol.HubLeaveData{}, err
	}
	if err := hub.Leave(hub.LeaveOptions{ConfigPath: path, StateDir: d.hubDir(), ForgetKey: a.ForgetKey}); err != nil {
		return protocol.HubLeaveData{}, err
	}
	d.hubResetIdentity()
	d.logf("", "hub: left the hub (forget key: %v); sessions keep running, their leases expire", a.ForgetKey)
	out := protocol.HubLeaveData{Left: true, KeyDeleted: a.ForgetKey}
	if err := d.handleReload(ctx); err != nil {
		return out, fmt.Errorf("left the hub, but the daemon did not reload: %v", err)
	}
	return out, nil
}

// hubResetIdentity forgets everything the loop learned about the previous
// identity: the next connect re-reads it.
func (d *Daemon) hubResetIdentity() {
	w := d.hub
	w.mu.Lock()
	w.joined, w.device, w.deviceName, w.org = false, "", "", ""
	w.lastErr, w.lastErrT, w.lastBeat = "", time.Time{}, time.Time{}
	w.policy, w.policyV, w.policyRead = nil, 0, false
	w.findings = nil
	w.mu.Unlock()
	d.hubDropCaches()
}

// hubPolicyDue makes the next cycle fetch the policy whatever its cadence
// says (a person asked to sync).
func (d *Daemon) hubPolicyDue() {
	w := d.hub
	w.mu.Lock()
	d.hubLoadLocked()
	w.st.PolicyAt = time.Time{}
	w.mu.Unlock()
}

func (d *Daemon) hubDropCaches() {
	w := d.hub
	w.mu.Lock()
	w.board, w.orgView, w.orgAt = nil, nil, time.Time{}
	w.mu.Unlock()
}

// ---- board / org -----------------------------------------------------------

func (d *Daemon) handleHubBoard(ctx context.Context, a protocol.HubBoardArgs) (protocol.HubBoardData, error) {
	st := hubproto.State(a.State)
	if st != "" && !st.Valid() {
		return protocol.HubBoardData{}, errors.New("hub: unknown state")
	}
	if a.Limit < 0 || a.Limit > hubproto.MaxBoardItems {
		return protocol.HubBoardData{}, fmt.Errorf("hub: limit must be 0..%d", hubproto.MaxBoardItems)
	}
	resp, at, err := d.hubBoardFetch(ctx, st, a.Limit, a.Fresh)
	if err != nil {
		return protocol.HubBoardData{}, err
	}
	snap, _ := d.hubConfig()
	return protocol.HubBoardData{FetchedAt: at, Items: d.hubAnnotate(resp.Items, snap.h)}, nil
}

// hubBoardFetch answers from the cache when it is fresh enough.
func (d *Daemon) hubBoardFetch(ctx context.Context, st hubproto.State, limit int, fresh bool) (hubproto.BoardResponse, time.Time, error) {
	key := fmt.Sprintf("%s|%d", st, limit)
	w := d.hub
	now := w.now()
	if !fresh {
		w.mu.Lock()
		c, ok := w.board[key]
		w.mu.Unlock()
		if ok && now.Sub(c.at) < hubBoardTTL {
			return c.resp, c.at, nil
		}
	}
	api, _, err := d.hubHumanClient()
	if err != nil {
		return hubproto.BoardResponse{}, time.Time{}, err
	}
	resp, err := api.Board(ctx, st, limit)
	if err != nil {
		return hubproto.BoardResponse{}, time.Time{}, err
	}
	w.mu.Lock()
	if w.board == nil {
		w.board = map[string]hubBoardCache{}
	}
	w.board[key] = hubBoardCache{at: now, resp: resp}
	w.mu.Unlock()
	return resp, now, nil
}

// hubAnnotate flattens the rows and adds THIS machine's answer to each:
// whether a person here could claim it now, and with which project.
func (d *Daemon) hubAnnotate(rows []hubproto.ItemView, h config.HubConfig) []protocol.HubBoardItem {
	working := map[string]string{}
	for _, s := range d.sessions.Snapshot() {
		if s.HubItem.ItemID != "" {
			working[s.HubItem.ItemID] = s.ID
		}
	}
	out := make([]protocol.HubBoardItem, 0, len(rows))
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, v := range rows {
		bi := hubBoardItem(v)
		bi.Local = hubLocal(d.cfg, h, v, working[v.Item.ID])
		out = append(out, bi)
	}
	return out
}

// hubLocal decides one row's local answer. The caller holds d.mu.
func hubLocal(cfg *config.Config, h config.HubConfig, v hubproto.ItemView, sess string) protocol.HubLocalInfo {
	if sess != "" {
		return protocol.HubLocalInfo{Reason: "working it here", Session: sess}
	}
	switch v.State {
	case hubproto.StateOpen:
	case hubproto.StateClaimed:
		if v.Lease != nil && v.Lease.Mine {
			return protocol.HubLocalInfo{Reason: "claimed by this machine"}
		}
		if v.Lease != nil {
			return protocol.HubLocalInfo{Reason: "claimed by " + v.Lease.DeviceName}
		}
		return protocol.HubLocalInfo{Reason: "claimed"}
	default:
		return protocol.HubLocalInfo{Reason: "item is " + string(v.State)}
	}
	cands := hub.ProjectsFor(cfg, v.Item)
	if len(cands) == 0 {
		return protocol.HubLocalInfo{Reason: "no local project works " + v.Item.Repo()}
	}
	var ok []string
	first := ""
	for _, p := range cands {
		if yes, why := hub.AcceptIntent(v.Item, p, h); yes {
			ok = append(ok, p.Name)
		} else if first == "" {
			first = why
		}
	}
	switch len(ok) {
	case 0:
		return protocol.HubLocalInfo{Reason: first}
	case 1:
		return protocol.HubLocalInfo{Claimable: true, Project: ok[0]}
	}
	return protocol.HubLocalInfo{Claimable: true, Projects: ok}
}

// hubBoardItem flattens a validated row for the UIs.
func hubBoardItem(v hubproto.ItemView) protocol.HubBoardItem {
	it := v.Item
	bi := protocol.HubBoardItem{
		ID: it.ID, Kind: string(it.Kind), Repo: it.Repo(), Label: hubLabel(it), State: string(v.State),
		Attempts: v.Attempts, Reporters: v.Reporters, FirstSeen: v.FirstSeen, LastSeen: v.LastSeen,
	}
	switch {
	case it.DepAdvisory != nil:
		a := it.DepAdvisory
		bi.Package, bi.Advisory, bi.Severity, bi.Ecosystem, bi.Vulnerable, bi.Fixed = a.Package, a.Advisory, string(a.Severity), a.Ecosystem, a.Vulnerable, a.Fixed
	case it.RedBumpPR != nil:
		r := it.RedBumpPR
		bi.Package, bi.PRNumber, bi.Branch, bi.Author = r.Package, r.PR, r.Branch, r.Author
	}
	if l := v.Lease; l != nil {
		bi.Lease = &protocol.HubLeaseInfo{Mine: l.Mine, DeviceName: l.DeviceName, Expires: l.Expires}
	}
	if o := v.Outcome; o != nil {
		bi.Outcome = &protocol.HubOutcomeInfo{PRURL: o.PRURL, Delivery: string(o.Delivery)}
	}
	if e := v.Escalation; e != nil {
		bi.Escalation = &protocol.HubEscalationInfo{Pending: e.Pending, Identifier: e.Identifier, URL: e.URL}
	}
	return bi
}

func (d *Daemon) handleHubOrg(ctx context.Context) (protocol.HubOrgData, error) {
	w := d.hub
	now := w.now()
	w.mu.Lock()
	cached, at := w.orgView, w.orgAt
	w.mu.Unlock()
	var o hubproto.OrgView
	if cached != nil && now.Sub(at) < hubOrgTTL {
		o = *cached
	} else {
		api, _, err := d.hubHumanClient()
		if err != nil {
			return protocol.HubOrgData{}, err
		}
		if o, err = api.Org(ctx); err != nil {
			return protocol.HubOrgData{}, err
		}
		at = now
		w.mu.Lock()
		w.orgView, w.orgAt = &o, now
		w.mu.Unlock()
	}
	out := protocol.HubOrgData{FetchedAt: at, Org: o.Org, PolicyVersion: o.PolicyVersion, Devices: make([]protocol.HubDeviceInfo, 0, len(o.Devices))}
	for _, dv := range o.Devices {
		agents := dv.Agents
		if agents == nil {
			agents = []string{}
		}
		out.Devices = append(out.Devices, protocol.HubDeviceInfo{Name: dv.Name, Me: dv.Me, LastSeen: dv.LastSeen, FreeSlots: dv.FreeSlots, Agents: agents, Active: dv.Active})
	}
	return out, nil
}

// ---- claim / release -------------------------------------------------------

func (d *Daemon) handleHubClaim(ctx context.Context, a protocol.HubClaimArgs) (protocol.HubClaimData, error) {
	if !hubproto.ValidItemID(a.ItemID) {
		return protocol.HubClaimData{}, errors.New("hub: invalid item id")
	}
	api, snap, err := d.hubHumanClient()
	if err != nil {
		return protocol.HubClaimData{}, err
	}
	// Shielded: the claim and the spawn must not be cut apart by a client
	// that hangs up.
	ctx = context.WithoutCancel(ctx)
	it, err := d.hubFindItem(ctx, api, a.ItemID)
	if err != nil {
		return protocol.HubClaimData{}, err
	}
	id, err := d.hubClaimItem(ctx, api, it, snap, hubClaimReq{human: true, project: a.Project})
	d.hubDropCaches()
	if err != nil {
		return protocol.HubClaimData{}, fmt.Errorf("hub: not claimed: %w", err)
	}
	out := protocol.HubClaimData{Session: id, ItemID: a.ItemID}
	if s, ok := d.sessions.Get(id); ok {
		out.Project = s.Project
	}
	return out, nil
}

// hubFindItem finds an item a person may claim: among this device's offers,
// else among the board's open items. Either way it passed validation; the
// claim path re-reads it from GitHub before anything is claimed.
func (d *Daemon) hubFindItem(ctx context.Context, api hubAPI, id string) (hubproto.Item, error) {
	if items, _, err := api.Offers(ctx); err == nil {
		for _, it := range items {
			if it.ID == id {
				return it, nil
			}
		}
	}
	resp, err := api.Board(ctx, "", hubproto.MaxBoardItems)
	if err != nil {
		return hubproto.Item{}, err
	}
	for _, v := range resp.Items {
		if v.Item.ID != id {
			continue
		}
		if v.State != hubproto.StateOpen {
			return hubproto.Item{}, fmt.Errorf("hub: item is %s on the hub", v.State)
		}
		return v.Item, nil
	}
	return hubproto.Item{}, errors.New("hub: item is not on the board")
}

func (d *Daemon) handleHubRelease(ctx context.Context, a protocol.HubReleaseArgs) (protocol.HubReleaseData, error) {
	if !hubproto.ValidItemID(a.ItemID) {
		return protocol.HubReleaseData{}, errors.New("hub: invalid item id")
	}
	var holder *session.Session
	for _, s := range d.sessions.Snapshot() {
		if s.HubItem.ItemID != a.ItemID {
			continue
		}
		switch {
		case s.HubItem.Released:
			return protocol.HubReleaseData{}, fmt.Errorf("hub: session %s already released item %s", s.ID, a.ItemID)
		case s.HubItem.LeaseLost:
			return protocol.HubReleaseData{}, fmt.Errorf("hub: session %s already lost the lease on item %s", s.ID, a.ItemID)
		case s.HubItem.Reported != "":
			return protocol.HubReleaseData{}, fmt.Errorf("hub: session %s already reported an outcome for item %s", s.ID, a.ItemID)
		}
		holder = &s
		break
	}
	api, _, err := d.hubHumanClient()
	if err != nil {
		return protocol.HubReleaseData{}, err
	}
	ctx = context.WithoutCancel(ctx)
	out := protocol.HubReleaseData{ItemID: a.ItemID}
	now := d.hub.now()
	err = api.Release(ctx, a.ItemID)
	switch {
	case err == nil:
	case hubTerminal(err):
		// The hub understood and refused (not our lease, unknown item):
		// nothing changes here.
		return protocol.HubReleaseData{}, err
	case holder != nil:
		// The hub is unreachable: the release waits in the outbox, one per
		// claim, and the session stops renewing the lease meanwhile.
		d.hubEnqueue(hubOutcome{ItemID: a.ItemID, ClaimedAt: holder.HubItem.ClaimedAt, Action: hubActRelease, Since: now})
		d.hubNudgeOutcomes()
		out.Queued = true
	default:
		return protocol.HubReleaseData{}, err
	}
	d.hubCoolDown(a.ItemID, now)
	d.hubDropCaches()
	if holder == nil {
		out.Message = "released item " + a.ItemID
		d.logf("", "hub: a person released item %s", a.ItemID)
		return out, nil
	}
	claimedAt := holder.HubItem.ClaimedAt
	_, _ = d.sessions.Update(holder.ID, func(cur *session.Session) bool {
		if cur.HubItem.ItemID != a.ItemID || !cur.HubItem.ClaimedAt.Equal(claimedAt) {
			return false
		}
		cur.HubItem.Released = true
		return true
	})
	if err := d.sessions.Save(); err != nil {
		d.logf("", "hub: persist sessions: %v", err)
	}
	out.Session, out.SessionKept = holder.ID, true
	out.Message = fmt.Sprintf("released item %s; session %s keeps running and is no longer reported to the hub", a.ItemID, holder.ID)
	if out.Queued {
		out.Message = fmt.Sprintf("the hub is unreachable: the release of item %s is queued; session %s keeps running and is no longer reported to the hub", a.ItemID, holder.ID)
	}
	d.logf("", "hub: a person released item %s; session %s keeps running", a.ItemID, holder.ID)
	return out, nil
}
