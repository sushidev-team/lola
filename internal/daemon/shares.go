package daemon

// shares.go — team session sharing, owner side (docs/team-sessions.md). A person
// shares a session; a pinned teammate attaches to its agent pane through the hub
// relay. Rules this file keeps:
//
//   - The hub routes and lists, it never grants. Who may attach is decided HERE,
//     on every frame, from the live session store and the members registry
//     (shareGate), so un-sharing a session or unpinning a teammate takes effect
//     at once.
//   - A teammate is pinned by a person who compared the key's fingerprint, and
//     the daemon re-reads the key from the hub at that moment: it refuses unless
//     the fingerprint the person saw is the one the hub names now. A pin is one
//     direction: allowing a teammate to attach to us grants nothing in reverse.
//   - Every command here is a human act (protocol.ShareHumanCommands): tier
//     never for a phone, absent from the sandbox hook bridge.
//   - Only the share id, the owner's title, the agent kind, the level and the
//     audience leave the machine. Never a session id, a path or pane text.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/session"
)

const (
	// shareRetry is how long a failed publish waits before trying again.
	shareRetry = 60 * time.Second
	// shareCallTimeout bounds one hub call made for a person's click.
	shareCallTimeout = 30 * time.Second
)

// shareState is the owner-side publish state, created on first use.
type shareState struct {
	once sync.Once
	kick chan struct{}

	mu        sync.Mutex
	published string // JSON of the last set the hub accepted
	identity  string // spki the hub last accepted
	err       string // class of the last failure, "" = none
}

var shareInitMu sync.Mutex

func (d *Daemon) shareSt() *shareState {
	shareInitMu.Lock()
	if d.shares == nil {
		d.shares = &shareState{}
	}
	s := d.shares
	shareInitMu.Unlock()
	s.once.Do(func() { s.kick = make(chan struct{}, 1) })
	return s
}

// shareNudge asks the publish loop for a pass.
func (d *Daemon) shareNudge() { poke(d.shareSt().kick) }

// ---- the gate -----------------------------------------------------------------

// shareGate is the daemon's per-frame policy for member peers.
type shareGate struct{ d *Daemon }

var _ remote.MemberGate = shareGate{}

// sharedSessions are the sessions currently shared, in a stable order.
func (d *Daemon) sharedSessions() []session.Session {
	var out []session.Session
	for _, s := range d.sessions.Snapshot() {
		if s.Share.On() && shareable(s) == nil {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// shareable is nil when a session may be shared at all: a session this daemon
// owns, not a borrowed directory and not a stack (one agent in many repos).
func shareable(s session.Session) error {
	switch {
	case s.EffectiveKind() == session.KindLocal:
		return errors.New("a session in a borrowed directory cannot be shared")
	case s.Stack || s.Lane != "":
		return errors.New("a stack session cannot be shared")
	case s.Agentless:
		return errors.New("a plain shell cannot be shared")
	}
	return nil
}

func (g shareGate) KnownRV(rv string) bool {
	if !hubproto.ValidRelayID(rv) {
		return false
	}
	for _, s := range g.d.sharedSessions() {
		if s.Share.RV == rv {
			return true
		}
	}
	return false
}

// memberHubID is the hub device id the owner pinned for a member entry.
func (d *Daemon) memberHubID(memberID string) string {
	if d.members == nil {
		return ""
	}
	dev, ok := d.members.Get(memberID)
	if !ok || dev.Hub == nil {
		return ""
	}
	return dev.Hub.DeviceID
}

func (g shareGate) PaneAccess(memberID, pane string) string {
	hubID := g.d.memberHubID(memberID)
	if hubID == "" || pane == "" {
		return remote.LevelNone
	}
	for _, s := range g.d.sharedSessions() {
		if paneTarget(s) != pane {
			continue
		}
		if len(s.Share.With) > 0 && !slices.Contains(s.Share.With, hubID) {
			return remote.LevelNone
		}
		if s.Share.Level == hubproto.LevelType {
			return remote.LevelType
		}
		return remote.LevelView
	}
	return remote.LevelNone
}

func (g shareGate) Roster(memberID string, att []remote.Attachment) (protocol.ShareSessionsData, error) {
	out := protocol.ShareSessionsData{Sessions: []protocol.SharedSession{}}
	for _, s := range g.d.sharedSessions() {
		pane := paneTarget(s)
		if g.PaneAccess(memberID, pane) == remote.LevelNone {
			continue
		}
		ss := protocol.SharedSession{
			ID: s.Share.ID, Title: s.Share.Title, Agent: s.Agent, Level: s.Share.Level, Pane: pane,
		}
		for _, a := range att {
			if a.Pane != pane {
				continue
			}
			lvl := g.PaneAccess(a.MemberID, pane)
			ss.Attached = append(ss.Attached, protocol.ShareViewer{
				Name: a.Name, Me: a.MemberID == memberID, Since: a.Since.Unix(), Typed: lvl == remote.LevelType,
			})
		}
		sort.Slice(ss.Attached, func(i, j int) bool { return ss.Attached[i].Since < ss.Attached[j].Since })
		out.Sessions = append(out.Sessions, ss)
	}
	return out, nil
}

// shareActive reports whether the daemon needs a serving remote.Server and a
// relay control channel for sharing: a hub is joined and something is shared
// or a teammate is pinned.
func (d *Daemon) shareActive(joined bool) bool {
	if !joined {
		return false
	}
	if d.members != nil && len(d.members.List()) > 0 && len(d.sharedSessions()) > 0 {
		return true
	}
	return false
}

// shareKnownRV reports whether rv is a published share's, for the relay's own
// "unknown rendezvous" refusal.
func (d *Daemon) shareKnownRV(rv string) bool { return shareGate{d}.KnownRV(rv) }

// membersChanged is the registry subscriber: the member set decides who may
// attach, so the server rechecks its live connections and the relay and the
// published set are refreshed.
func (d *Daemon) membersChanged() {
	d.shareRecheck()
	d.hubRelayDirty()
	d.shareNudge()
}

// shareRecheck ends member subscriptions the gate no longer allows.
func (d *Daemon) shareRecheck() {
	d.remoteMu.Lock()
	srv := d.remote
	d.remoteMu.Unlock()
	if srv != nil {
		srv.RecheckMembers()
	}
}

// ---- cmd=shareSession ---------------------------------------------------------

func (d *Daemon) handleShareSession(ctx context.Context, a protocol.ShareSessionArgs) (protocol.ShareSessionData, error) {
	s, ok := d.sessions.Get(a.Session)
	if !ok {
		return protocol.ShareSessionData{}, errors.New("share: no such session")
	}
	if !a.On {
		if _, ok := d.sessions.Update(a.Session, func(s *session.Session) bool {
			if !s.Share.On() {
				return false
			}
			s.Share = session.Share{}
			return true
		}); !ok {
			return protocol.ShareSessionData{Session: a.Session}, nil
		}
		d.afterShareChange(ctx)
		return protocol.ShareSessionData{Session: a.Session}, nil
	}
	if err := shareable(s); err != nil {
		return protocol.ShareSessionData{}, fmt.Errorf("share: %w", err)
	}
	if snap, ok := d.hubConfig(); !ok || !d.hubRelayJoined(snap.h) {
		return protocol.ShareSessionData{}, errors.New("share: join a team hub first")
	}
	level := a.Level
	if level == "" {
		level = hubproto.LevelView
	}
	if level != hubproto.LevelView && level != hubproto.LevelType {
		return protocol.ShareSessionData{}, errors.New("share: level must be view or type")
	}
	if level == hubproto.LevelType && s.Sandbox == "" && !a.ConfirmUnsandboxed {
		return protocol.ShareSessionData{}, errors.New("share: typing into a session that runs on this machine needs an explicit confirmation")
	}
	title := strings.TrimSpace(a.Title)
	if title == "" {
		title = s.DisplayTitle()
	}
	if title == "" {
		title = "Session"
	}
	if r := []rune(title); len(r) > hubproto.MaxShareTitleRunes {
		title = string(r[:hubproto.MaxShareTitleRunes])
	}
	if !hubproto.ValidShareTitle(title) {
		return protocol.ShareSessionData{}, errors.New("share: the title has characters that cannot be shared")
	}
	with := slices.Clone(a.With)
	if len(with) > hubproto.MaxShareAudience {
		return protocol.ShareSessionData{}, errors.New("share: too many members")
	}
	for _, w := range with {
		if !hubproto.ValidDeviceID(w) {
			return protocol.ShareSessionData{}, errors.New("share: invalid member")
		}
	}
	id, err := hubproto.NewShareID()
	if err != nil {
		return protocol.ShareSessionData{}, err
	}
	rv, err := hubproto.NewRelayID()
	if err != nil {
		return protocol.ShareSessionData{}, err
	}
	var data protocol.ShareSessionData
	if _, ok := d.sessions.Update(a.Session, func(s *session.Session) bool {
		// Editing a share keeps its id and rendezvous: a teammate attached now
		// stays attached through a title or audience change.
		if s.Share.ID == "" {
			s.Share.ID, s.Share.RV = id, rv
		}
		s.Share.Title, s.Share.Level, s.Share.With = title, level, with
		data = protocol.ShareSessionData{Session: s.ID, On: true, ShareID: s.Share.ID, Level: level}
		return true
	}); !ok {
		return protocol.ShareSessionData{}, errors.New("share: no such session")
	}
	d.afterShareChange(ctx)
	return data, nil
}

// afterShareChange applies a change of share state: members lose what the gate
// no longer allows at once, the server and relay follow the new need, and the
// hub's list is refreshed.
func (d *Daemon) afterShareChange(ctx context.Context) {
	d.shareRecheck()
	d.reloadRemoteIfShareChanged()
	d.hubRelayDirty()
	d.shareNudge()
	d.watch.PokeGlobal(pokeSessionsAll)
}

// reloadRemoteIfShareChanged rebuilds the server when sharing changed whether
// one should exist. A change inside a running server needs nothing: the gate
// is read live.
func (d *Daemon) reloadRemoteIfShareChanged() {
	d.mu.Lock()
	rc, hubCfg := d.cfg.Remote, d.cfg.Hub
	d.mu.Unlock()
	serves := rc.Serves(d.hubRelayJoined(hubCfg)) || d.shareActive(d.hubRelayJoined(hubCfg))
	if serves != d.remoteServes.Load() {
		d.reloadRemote()
	}
}

// ---- members --------------------------------------------------------------------

func fingerprintOf(pubKey string) string {
	pub, err := hubproto.DecodePublicKey(pubKey)
	if err != nil {
		return ""
	}
	return hubproto.Fingerprint(pub)
}

func (d *Daemon) shareClients() (*hub.Client, error) {
	snap, ok := d.hubConfig()
	if !ok {
		return nil, errors.New("share: join a team hub first")
	}
	c, _, _, err := d.relay().newClient(snap.h.URL, d.hubDir())
	if err != nil {
		if errors.Is(err, hub.ErrNotJoined) {
			return nil, errors.New("share: join a team hub first")
		}
		return nil, err
	}
	return c, nil
}

func (d *Daemon) handleShareMembers(ctx context.Context) (protocol.ShareMembersData, error) {
	out := protocol.ShareMembersData{Members: []protocol.ShareMemberInfo{}}
	pinnedBy := func(reg *devices.Registry) map[string]bool {
		m := map[string]bool{}
		if reg != nil {
			for _, dev := range reg.List() {
				if dev.Hub != nil {
					m[dev.Hub.DeviceID] = true
				}
			}
		}
		return m
	}
	allowed, trusted := pinnedBy(d.members), pinnedBy(d.owners)
	att := map[string]int{}
	d.remoteMu.Lock()
	srv := d.remote
	d.remoteMu.Unlock()
	if srv != nil {
		for _, a := range srv.MemberAttachments() {
			att[d.memberHubID(a.MemberID)]++
		}
	}
	seen := map[string]bool{}
	c, err := d.shareClients()
	if err == nil {
		cctx, cancel := context.WithTimeout(ctx, shareCallTimeout)
		defer cancel()
		var list hubproto.MemberList
		if list, err = c.Members(cctx); err == nil {
			for _, m := range list.Members {
				seen[m.DeviceID] = true
				name := m.Name
				if m.Member != "" {
					name = m.Member + " · " + m.Name
				}
				out.Members = append(out.Members, protocol.ShareMemberInfo{
					DeviceID: m.DeviceID, Name: name, Member: m.Member,
					Fingerprint: fingerprintOf(m.PublicKey),
					Allowed:     allowed[m.DeviceID], Trusted: trusted[m.DeviceID], Attached: att[m.DeviceID],
				})
			}
		}
	}
	if err != nil {
		out.Offline = true
	}
	// A pinned teammate the hub no longer lists is still shown, so a person can
	// remove it.
	for _, reg := range []*devices.Registry{d.members, d.owners} {
		if reg == nil {
			continue
		}
		for _, dev := range reg.List() {
			if dev.Hub == nil || seen[dev.Hub.DeviceID] {
				continue
			}
			seen[dev.Hub.DeviceID] = true
			out.Members = append(out.Members, protocol.ShareMemberInfo{
				DeviceID: dev.Hub.DeviceID, Name: dev.Name, Member: dev.Hub.Member, Fingerprint: dev.Hub.Fingerprint,
				Allowed: allowed[dev.Hub.DeviceID], Trusted: trusted[dev.Hub.DeviceID], Attached: att[dev.Hub.DeviceID],
			})
		}
	}
	sort.Slice(out.Members, func(i, j int) bool { return out.Members[i].Name < out.Members[j].Name })
	return out, nil
}

// handleShareTrust pins a teammate. The hub is asked again NOW and the pin is
// refused unless the key it names has the fingerprint the person compared.
func (d *Daemon) handleShareTrust(ctx context.Context, a protocol.ShareTrustArgs) (protocol.ShareMembersData, error) {
	if !hubproto.ValidDeviceID(a.DeviceID) || a.Fingerprint == "" {
		return protocol.ShareMembersData{}, errors.New("share: name the teammate and the fingerprint you compared")
	}
	if !a.Allow && !a.Trust {
		return protocol.ShareMembersData{}, errors.New("share: choose what to allow")
	}
	if d.members == nil || d.owners == nil {
		return protocol.ShareMembersData{}, errors.New("share: not available in this daemon")
	}
	c, err := d.shareClients()
	if err != nil {
		return protocol.ShareMembersData{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, shareCallTimeout)
	defer cancel()
	list, err := c.Members(cctx)
	if err != nil {
		return protocol.ShareMembersData{}, errors.New("share: the hub could not be asked right now")
	}
	var row *hubproto.MemberView
	for i := range list.Members {
		if list.Members[i].DeviceID == a.DeviceID {
			row = &list.Members[i]
		}
	}
	if row == nil {
		return protocol.ShareMembersData{}, errors.New("share: the hub does not list that teammate")
	}
	pub, err := hubproto.DecodePublicKey(row.PublicKey)
	if err != nil {
		return protocol.ShareMembersData{}, errors.New("share: the teammate's key is invalid")
	}
	if strings.Join(strings.Fields(a.Fingerprint), " ") != hubproto.Fingerprint(pub) {
		return protocol.ShareMembersData{}, errors.New("share: the fingerprint changed since you looked; compare it again")
	}
	raw, err := base64.StdEncoding.DecodeString(row.Identity.SPKI)
	if err != nil || len(raw) != 32 {
		return protocol.ShareMembersData{}, errors.New("share: the teammate's connection key is invalid")
	}
	var spki [32]byte
	copy(spki[:], raw)
	name := row.Name
	if row.Member != "" {
		name = row.Member + " · " + row.Name
	}
	if len([]rune(name)) > devices.MaxNameRunes {
		name = string([]rune(name)[:devices.MaxNameRunes])
	}
	hid := devices.HubIdentity{DeviceID: row.DeviceID, PublicKey: row.PublicKey, Fingerprint: hubproto.Fingerprint(pub), Member: row.Member}
	if a.Allow {
		if _, err := d.members.AddMember(name, spki, hid); err != nil {
			return protocol.ShareMembersData{}, fmt.Errorf("share: %w", err)
		}
	}
	if a.Trust {
		if _, err := d.owners.AddMember(name, spki, hid); err != nil {
			return protocol.ShareMembersData{}, fmt.Errorf("share: %w", err)
		}
	}
	d.logf("", "share: pinned teammate device=%s allow=%t trust=%t", short8(row.DeviceID), a.Allow, a.Trust)
	return d.handleShareMembers(ctx)
}

func short8(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func (d *Daemon) handleShareUntrust(ctx context.Context, a protocol.ShareUntrustArgs) (protocol.ShareMembersData, error) {
	if !hubproto.ValidDeviceID(a.DeviceID) {
		return protocol.ShareMembersData{}, errors.New("share: invalid teammate")
	}
	var firstErr error
	for _, reg := range []*devices.Registry{d.members, d.owners} {
		if reg == nil {
			continue
		}
		for _, dev := range reg.List() {
			if dev.Hub != nil && dev.Hub.DeviceID == a.DeviceID {
				if _, err := reg.Revoke(dev.ID); err != nil && firstErr == nil {
					firstErr = err
				}
			}
		}
	}
	d.shareRecheck()
	out, err := d.handleShareMembers(ctx)
	if err == nil && firstErr != nil {
		err = fmt.Errorf("share: removed here, but it could not be saved: %w", firstErr)
	}
	return out, err
}

// ---- cmd dispatch -----------------------------------------------------------------

func (d *Daemon) handleShare(ctx context.Context, req protocol.Request) protocol.Response {
	var (
		data any
		err  error
	)
	bad := errors.New("share: bad args")
	switch req.Cmd {
	case protocol.CmdShareSession:
		var a protocol.ShareSessionArgs
		if err = decodeArgs(req.Args, &a); err != nil {
			err = bad
		} else {
			data, err = d.handleShareSession(ctx, a)
		}
	case protocol.CmdShareMembers:
		data, err = d.handleShareMembers(ctx)
	case protocol.CmdShareTrust:
		var a protocol.ShareTrustArgs
		if err = decodeArgs(req.Args, &a); err != nil {
			err = bad
		} else {
			data, err = d.handleShareTrust(ctx, a)
		}
	case protocol.CmdShareUntrust:
		var a protocol.ShareUntrustArgs
		if err = decodeArgs(req.Args, &a); err != nil {
			err = bad
		} else {
			data, err = d.handleShareUntrust(ctx, a)
		}
	case protocol.CmdShareOffered:
		data, err = d.handleShareOffered(ctx)
	default:
		err = fmt.Errorf("unknown cmd %q", req.Cmd)
	}
	if err != nil {
		return protocol.Response{OK: false, Error: err.Error()}
	}
	return dataResponse(data)
}

func (d *Daemon) handleShareOffered(ctx context.Context) (protocol.ShareOfferedData, error) {
	out := protocol.ShareOfferedData{Offers: []protocol.ShareOffer{}}
	c, err := d.shareClients()
	if err != nil {
		out.Offline = true
		return out, nil
	}
	cctx, cancel := context.WithTimeout(ctx, shareCallTimeout)
	defer cancel()
	list, err := c.ListShares(cctx)
	if err != nil {
		out.Offline = true
		return out, nil
	}
	trusted := map[string]bool{}
	if d.owners != nil {
		for _, dev := range d.owners.List() {
			if dev.Hub != nil {
				trusted[dev.Hub.DeviceID] = true
			}
		}
	}
	for _, v := range list.Shares {
		out.Offers = append(out.Offers, protocol.ShareOffer{
			ID: v.ID, Title: v.Title, Agent: v.Agent, Level: v.Level,
			OwnerDevice: v.OwnerDevice, OwnerName: v.OwnerName, OwnerMember: v.OwnerMember,
			Online: v.Online, Trusted: trusted[v.OwnerDevice],
		})
	}
	return out, nil
}

// ---- publishing ---------------------------------------------------------------------

// shareSet is the share set this daemon wants the hub to hold.
func (d *Daemon) shareSet() hubproto.ShareSet {
	set := hubproto.ShareSet{Shares: []hubproto.ShareEntry{}}
	// Only while a teammate is pinned: a share nobody may attach is not offered.
	if d.members == nil || len(d.members.List()) == 0 {
		return set
	}
	pinned := map[string]bool{}
	for _, dev := range d.members.List() {
		if dev.Hub != nil {
			pinned[dev.Hub.DeviceID] = true
		}
	}
	for _, s := range d.sharedSessions() {
		var with []string
		for _, w := range s.Share.With {
			if pinned[w] {
				with = append(with, w)
			}
		}
		if len(s.Share.With) > 0 && len(with) == 0 {
			continue // all its members were unpinned
		}
		if len(set.Shares) >= hubproto.MaxSharesPerDevice {
			break
		}
		set.Shares = append(set.Shares, hubproto.ShareEntry{
			ID: s.Share.ID, RV: s.Share.RV, Title: s.Share.Title, Agent: s.Agent,
			Level: s.Share.Level, Members: with,
		})
	}
	return set
}

// sharePass publishes what changed. It never runs on the observer loop.
func (d *Daemon) sharePass(ctx context.Context) {
	st := d.shareSt()
	snap, ok := d.hubConfig()
	if !ok || !d.hubRelayJoined(snap.h) {
		return
	}
	c, err := d.shareClients()
	if err != nil {
		return
	}
	setFail := func(err error) {
		st.mu.Lock()
		st.err = relayErrText(err)
		st.mu.Unlock()
	}
	// The identity binding, once per key: members can only pin what the device
	// itself signed.
	if key, kerr := remote.LoadOrCreateDeviceKey(d.home, nil); kerr == nil {
		spki := base64.StdEncoding.EncodeToString(func() []byte { h := key.SPKISHA256(); return h[:] }())
		st.mu.Lock()
		done := st.identity == spki
		st.mu.Unlock()
		if !done {
			ictx, cancel := context.WithTimeout(ctx, hub.DefaultRequestTimeout)
			err := c.PublishIdentity(ictx, spki)
			cancel()
			if err != nil {
				if !hub.IsCode(err, hubproto.CodeNotFound) {
					setFail(err)
				}
				return
			}
			st.mu.Lock()
			st.identity = spki
			st.mu.Unlock()
		}
	}
	set := d.shareSet()
	raw, _ := json.Marshal(set)
	st.mu.Lock()
	same := st.published == string(raw)
	st.mu.Unlock()
	if same {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, hub.DefaultRequestTimeout)
	err = c.PublishShares(pctx, set)
	cancel()
	if err != nil {
		// An older hub has no sharing: nothing to publish, nothing to retry.
		if !hub.IsCode(err, hubproto.CodeNotFound) {
			setFail(err)
		}
		return
	}
	st.mu.Lock()
	st.published, st.err = string(raw), ""
	st.mu.Unlock()
	d.hubRelayDirty()
}

// shareLoop publishes the share set when it changes, on its own goroutine and
// the daemon's run context.
func (d *Daemon) shareLoop(ctx context.Context) {
	st := d.shareSt()
	t := time.NewTimer(shareRetry)
	defer t.Stop()
	for {
		func() {
			defer func() {
				if p := recover(); p != nil {
					d.logf("", "share: publish pass panicked: %v", p)
				}
			}()
			d.sharePass(context.WithoutCancel(ctx))
		}()
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
		t.Reset(shareRetry)
		select {
		case <-ctx.Done():
			return
		case <-st.kick:
		case <-t.C:
		}
	}
}
