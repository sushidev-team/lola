package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
)

// The daemon's relay leg to the team hub (docs/mobile/03-hub-relay.md §10).
//
// The hub forwards an opaque stream: everything a phone says travels inside a
// TLS session this daemon terminates (remote.Server.ServeRelayed), so nothing
// the hub sends is ever a command. What the hub may steer is limited to "a
// phone is dialling rendezvous X": the daemon accepts only rendezvous ids it
// registered itself, with its own slot counts, its own ping interval and its
// own rate limit. A hello that names other values changes nothing.
//
// Lock order: this file never holds two locks. hubRelay.mu, syncMu and credMu
// are leaves; d.mu and remoteMu are read in copies and released first
// (remoteMu → d.mu is the daemon's order and the loop never takes both).

// Relay-loop constants (§10.2). The hub's limits live in hubproto.
const (
	relayIncomingPerSec    = 1.0
	relayIncomingBurst     = 8
	relayAcceptConcurrency = 2
	relayPairOfferWait     = protocol.PairBeginRelayWait
	relayHelloTimeout      = 10 * time.Second
	relayDirtyRetry        = 60 * time.Second
	relaySyncRepeatMax     = 3 // re-sends when the rendezvous set changed during a sync
	relayRejectWrite       = 5 * time.Second
	relayErrMax            = 160
	// relayConflictRotateEvery bounds conflict-driven rv rotations: at most one
	// per device per window, whatever the hub's conflicts list says. A real
	// conflict is a squatted rv, which one fresh rv resolves; a hub naming the
	// same device again at once is broken or hostile, and believing it would
	// rewrite the registry and re-sync in a loop as fast as it answers.
	relayConflictRotateEvery = time.Hour
)

// hubRelay is the loop's state. Created empty by newDaemon and initialised on
// first use (relay()).
type hubRelay struct {
	once sync.Once

	mu        sync.Mutex
	state     string // protocol.HubRelayState*
	reason    string
	since     time.Time
	lastErr   string
	lastErrAt time.Time
	active    int             // live relayed remote conns
	reg       hub.RelayStatus // Registered* fields, persisted with the state
	relDone   bool            // the give-up of hubRelayRelease was logged
	kick      chan struct{}   // cap 1; hubRelayNudge
	dirty     chan struct{}   // cap 1; hubRelayDirty
	gen       atomic.Uint64   // bumped by hubRelayDirty
	syncMu    sync.Mutex      // serializes EVERY SyncRendezvous
	synced    chan struct{}   // closed and replaced after each successful sync
	syncedGen uint64          // gen read before the last successful sync's POST
	conflicts map[string]bool // rvs the last successful sync reported as conflicts
	credMu    sync.Mutex      // serializes relay-record mutations
	incoming  *relayBucket
	accepting chan struct{} // cap relayAcceptConcurrency
	// windowID/windowRV name the pairing the open window's relay belongs to, so
	// a conflict on the window rv can take the relay off that pairing.
	windowID string
	windowRV string
	inflight map[string]context.CancelFunc // accept dials by conn id
	// conflictAt is the last conflict-driven rv rotation per device id, under
	// mu (relayConflictRotateEvery).
	conflictAt map[string]relayConflictMark
	// unsupportedNudged is when a heartbeat last ended the unsupported hold
	// (relayAfterHeartbeat), under mu.
	unsupportedNudged time.Time

	// Seams (tests replace them).
	newClient func(url, dir string) (*hub.Client, *hub.RelayClient, hub.Identity, error)
	// delay is the backoff: full jitter below min(Max, Min<<fails), floored.
	delay func(fails int, floor time.Duration) time.Duration
	// unsupportedRecheck is how long a 404 from the hub is believed.
	unsupportedRecheck time.Duration
	dirtyRetry         time.Duration
	pairWait           time.Duration
}

var relayInitMu sync.Mutex

// relay returns the daemon's relay state, creating and initialising it on
// first use (a Daemon built without newDaemon has none).
func (d *Daemon) relay() *hubRelay {
	relayInitMu.Lock()
	if d.hubRelay == nil {
		d.hubRelay = &hubRelay{}
	}
	r := d.hubRelay
	relayInitMu.Unlock()
	r.once.Do(func() {
		r.state, r.since = protocol.HubRelayStateOff, time.Now()
		r.kick = make(chan struct{}, 1)
		r.dirty = make(chan struct{}, 1)
		r.synced = make(chan struct{})
		r.accepting = make(chan struct{}, relayAcceptConcurrency)
		r.incoming = newRelayBucket(relayIncomingPerSec, relayIncomingBurst)
		r.inflight = map[string]context.CancelFunc{}
		if r.newClient == nil {
			r.newClient = newRealRelayClients
		}
		if r.delay == nil {
			r.delay = relayBackoffDelay
		}
		if r.unsupportedRecheck == 0 {
			r.unsupportedRecheck = hubproto.RelayUnsupportedRecheck
		}
		if r.dirtyRetry == 0 {
			r.dirtyRetry = relayDirtyRetry
		}
		if r.pairWait == 0 {
			r.pairWait = relayPairOfferWait
		}
		if st, ok := hub.LoadRelayStatus(d.hubDir()); ok {
			r.reg = hub.RelayStatus{Registered: st.Registered, RegisteredURL: st.RegisteredURL, RegisteredDevice: st.RegisteredDevice}
		}
	})
	return r
}

// newRealRelayClients loads the identity and key from dir and builds the
// signed clients for url, failing closed on every mismatch exactly as
// newRealHubClient does.
func newRealRelayClients(url, dir string) (*hub.Client, *hub.RelayClient, hub.Identity, error) {
	id, key, err := relayIdentity(url, dir)
	if err != nil {
		return nil, nil, hub.Identity{}, err
	}
	c, err := hub.NewClient(url, key)
	if err != nil {
		return nil, nil, id, err
	}
	return c, &hub.RelayClient{BaseURL: url, Key: key}, id, nil
}

// relayIdentity is the joined check: an identity for url whose device key is
// the one on disk.
func relayIdentity(url, dir string) (hub.Identity, *hub.Key, error) {
	id, err := hub.LoadIdentity(dir)
	if err != nil {
		return hub.Identity{}, nil, err
	}
	if strings.TrimSuffix(id.URL, "/") != strings.TrimSuffix(url, "/") {
		return id, nil, errors.New("the hub identity belongs to another hub")
	}
	key, err := hub.LoadKey(dir)
	if err != nil {
		return id, nil, err
	}
	if key.DeviceID() != id.DeviceID {
		return id, nil, errors.New("the device key on disk is not the one that enrolled")
	}
	return id, key, nil
}

// relayBucket is a small token bucket (the remote package's is unexported).
type relayBucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newRelayBucket(rate, burst float64) *relayBucket {
	return &relayBucket{rate: rate, burst: burst, tokens: burst, last: time.Now()}
}

func (b *relayBucket) take() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// relayBackoffDelay is full jitter in [0, min(RelayBackoffMax, RelayBackoffMin<<fails)),
// with a floor (a 429's).
func relayBackoffDelay(fails int, floor time.Duration) time.Duration {
	ceil := hubproto.RelayBackoffMax
	if fails < 20 {
		ceil = min(ceil, hubproto.RelayBackoffMin<<fails)
	}
	d := time.Duration(rand.Int64N(int64(max(ceil, 1))))
	return min(max(d, floor), hubproto.RelayBackoffMax)
}

// relayUnsupportedNudgeEvery bounds how often a successful heartbeat ends
// the "unsupported" hold early. A hub that answered 404 to the relay may
// be upgraded at any time; waiting out RelayUnsupportedRecheck (6 h)
// would leave phones without the relay for most of a day, and one
// rendezvous POST per interval to a hub that still says 404 costs nothing.
const relayUnsupportedNudgeEvery = 15 * time.Minute

// ---- nudges ----------------------------------------------------------------

// hubRelayNudge asks the loop to re-evaluate now (reload, remote start/stop).
func (d *Daemon) hubRelayNudge() { poke(d.relay().kick) }

// relayAfterHeartbeat runs after every heartbeat the hub accepted. While the
// relay sits in its "unsupported" hold it nudges the loop, at most once per
// relayUnsupportedNudgeEvery: the hub answering again is the moment an
// upgrade may have landed. In any other state it does nothing.
func (d *Daemon) relayAfterHeartbeat(now time.Time) {
	r := d.relay()
	r.mu.Lock()
	due := r.state == protocol.HubRelayStateUnsupported &&
		(r.unsupportedNudged.IsZero() || now.Sub(r.unsupportedNudged) >= relayUnsupportedNudgeEvery)
	if due {
		r.unsupportedNudged = now
	}
	r.mu.Unlock()
	if due {
		d.hubRelayNudge()
	}
}

// hubRelayDirty says the rendezvous set changed (a device paired, revoked or
// rotated; a window opened or closed). The generation counter lets a sync that
// is already in flight notice and repeat.
func (d *Daemon) hubRelayDirty() {
	r := d.relay()
	r.gen.Add(1)
	poke(r.dirty)
}

// ---- facts -----------------------------------------------------------------

// hubRelayJoined reports whether this machine is joined to the hub h names:
// the hub is on, an identity loads for [hub].url, and the key on disk is the
// one that enrolled. It reads files; call it with no lock held.
func (d *Daemon) hubRelayJoined(h config.HubConfig) bool {
	ok, _ := d.relayJoinedWhy(h)
	return ok
}

func (d *Daemon) relayJoinedWhy(h config.HubConfig) (bool, string) {
	if !h.Enabled {
		return false, "hub_off"
	}
	if _, _, err := relayIdentity(h.URL, d.hubDir()); err != nil {
		if _, ok := hub.LoadRevocation(d.hubDir()); ok && errors.Is(err, hub.ErrNotJoined) {
			return false, "revoked"
		}
		return false, "not_joined"
	}
	return true, ""
}

// relayInputs are the facts one evaluation uses, gathered without ever holding
// two locks.
type relayInputs struct {
	remote    config.RemoteConfig
	hub       config.HubConfig
	hasServer bool
	wanted    bool
	reason    string // why not wanted, or "no_phones"
	entries   []hubproto.RelayRendezvous
}

func (d *Daemon) relayGather(now time.Time) relayInputs {
	d.mu.Lock()
	in := relayInputs{remote: d.cfg.Remote, hub: d.cfg.Hub}
	cfgErr := d.cfgErr
	d.mu.Unlock()
	d.remoteMu.Lock()
	in.hasServer = d.remote != nil
	d.remoteMu.Unlock()
	joined, why := d.relayJoinedWhy(in.hub)
	switch {
	case cfgErr != "":
		// Its own word, so a UI can tell "config.toml has an error" from
		// "turned off". It comes first: the running config is the last valid
		// one, so its relay switch may not be what the file now says.
		in.reason = "config_error"
	case !in.remote.RelayOn():
		in.reason = "disabled"
	case !in.hub.Enabled:
		in.reason = "hub_off"
	case !joined:
		in.reason = why
	case !in.hasServer:
		in.reason = "remote_off"
	default:
		in.wanted = true
	}
	if in.wanted {
		in.entries = d.relayEntries(now)
		if len(in.entries) == 0 {
			in.reason = "no_phones"
		}
	}
	return in
}

// relayEntries is the rendezvous set this daemon wants registered, sorted by
// rv: every paired device's record (the previous bearer only while it is live)
// and the open pairing window (only while it is waiting and unexpired).
func (d *Daemon) relayEntries(now time.Time) []hubproto.RelayRendezvous {
	var out []hubproto.RelayRendezvous
	for _, dev := range d.devices.List() {
		rl := dev.Relay
		if rl == nil {
			continue
		}
		e := hubproto.RelayRendezvous{RV: rl.RV, BearerSHA256: rl.BearerHash}
		if rl.PrevHash != "" && rl.PrevUntil.After(now) {
			e.PrevSHA256, e.PrevUntil = rl.PrevHash, rl.PrevUntil
		}
		out = append(out, e)
	}
	if rv, tokHash, exp, ok := d.pairer.Window(); ok && exp.After(now) {
		out = append(out, hubproto.RelayRendezvous{RV: rv, BearerSHA256: tokHash, ExpiresAt: exp})
	}
	slices.SortFunc(out, func(a, b hubproto.RelayRendezvous) int { return strings.Compare(a.RV, b.RV) })
	return out
}

func (d *Daemon) relayNow() time.Time {
	if d.hub != nil && d.hub.now != nil {
		return d.hub.now()
	}
	return time.Now()
}

// relayEligible is the pairing/credential gate, from facts (no live channel
// needed): relay on, hub joined, config valid, a server to serve with, and the
// hub not known to lack the relay or to have refused this device.
func (d *Daemon) relayEligible() bool {
	in := d.relayGather(time.Time{})
	if !in.wanted {
		return false
	}
	r := d.relay()
	r.mu.Lock()
	st := r.state
	r.mu.Unlock()
	return st != protocol.HubRelayStateUnsupported && st != protocol.HubRelayStateRefused
}

// ---- status ----------------------------------------------------------------

// relaySet moves the state and persists it. A move to the state it is already
// in with the same reason writes nothing.
func (d *Daemon) relaySet(state, reason string) {
	r := d.relay()
	r.mu.Lock()
	changed := r.state != state || r.reason != reason
	if changed {
		r.state, r.reason, r.since = state, reason, time.Now()
	}
	st := r.statusLocked()
	r.mu.Unlock()
	if changed {
		if reason != "" {
			d.logf("", "hub relay: %s (%s)", state, reason)
		} else {
			d.logf("", "hub relay: %s", state)
		}
		d.relaySave(st)
	}
}

func (r *hubRelay) statusLocked() hub.RelayStatus {
	st := r.reg
	st.State, st.Reason, st.Since = r.state, r.reason, r.since
	st.LastError, st.LastErrAt = r.lastErr, r.lastErrAt
	return st
}

func (d *Daemon) relaySave(st hub.RelayStatus) {
	dir := d.hubDir()
	if err := os.MkdirAll(dir, 0o700); err == nil {
		if err := hub.SaveRelayStatus(dir, st); err != nil {
			d.logf("", "hub relay: write status: %v", err)
		}
	}
}

// relayNoteErr records a failure for status. The text is built here from the
// client's already-filtered APIError or a fixed phrase: nothing a hub chose.
func (d *Daemon) relayNoteErr(err error) {
	r := d.relay()
	txt := relayErrText(err)
	r.mu.Lock()
	r.lastErr, r.lastErrAt = txt, time.Now()
	st := r.statusLocked()
	r.mu.Unlock()
	d.logf("", "hub relay: %s", txt)
	d.relaySave(st)
}

func relayErrText(err error) string {
	var ae *hub.APIError
	var s string
	switch {
	case errors.As(err, &ae):
		s = ae.Error()
	case errors.Is(err, context.DeadlineExceeded):
		s = "timed out"
	case errors.Is(err, context.Canceled):
		s = "cancelled"
	default:
		s = "connection failed"
	}
	if r := []rune(s); len(r) > relayErrMax {
		s = string(r[:relayErrMax])
	}
	return s
}

// relayStatus is the hubStatus view; nil while [remote] is disabled. It carries
// no rendezvous id, bearer, hash or connection id.
func (d *Daemon) relayStatus() *protocol.HubRelayInfo {
	d.mu.Lock()
	on := d.cfg.Remote.Enabled
	d.mu.Unlock()
	if !on {
		return nil
	}
	r := d.relay()
	n := len(d.relayEntries(d.relayNow()))
	r.mu.Lock()
	defer r.mu.Unlock()
	return &protocol.HubRelayInfo{
		State: r.state, Reason: r.reason, Since: r.since,
		LastError: r.lastErr, LastErrorAt: r.lastErrAt,
		ActiveConns: r.active, Rendezvous: n,
	}
}

// ---- the loop --------------------------------------------------------------

// relayHold is a wait the loop owes before it tries the hub again.
type relayHold struct {
	until time.Time
	// hard holds (unsupported, refused) end on a kick; a backoff does not, so a
	// reload cannot bypass a 429 floor.
	hard bool
}

// hubRelayLoop runs on the daemon's run context, started beside hubLoop. It
// never blocks shutdown: every dial and wait derives from ctx, and nothing is
// shielded from its cancellation (the rendezvous POST is an idempotent
// replace-all, so a cut leaves nothing half-written). Entries stay registered
// across a restart.
func (d *Daemon) hubRelayLoop(ctx context.Context) {
	defer d.wg.Done()
	d.relay()
	defer d.relaySet(protocol.HubRelayStateOff, "stopped")
	var fails int
	var hold relayHold
	for ctx.Err() == nil {
		d.safeRelayStep(ctx, &fails, &hold)
	}
}

func (d *Daemon) safeRelayStep(ctx context.Context, fails *int, hold *relayHold) {
	defer func() {
		if p := recover(); p != nil {
			d.logf("", "hub relay: step panicked: %v", p)
			d.relaySet(protocol.HubRelayStateBackoff, "internal")
			*fails++
			*hold = relayHold{until: time.Now().Add(d.relay().delay(*fails, 0))}
			d.relay().wait(ctx, time.Until(hold.until), false)
		}
	}()
	d.relayStep(ctx, fails, hold)
}

type relayWake int

const (
	wakeTimer relayWake = iota
	wakeKick
	wakeDirty
	wakeCtx
)

// wait blocks until d elapses (d < 0 = forever), a kick, optionally a dirty
// signal, or ctx ends.
func (r *hubRelay) wait(ctx context.Context, d time.Duration, onDirty bool) relayWake {
	var tc <-chan time.Time
	if d >= 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		tc = t.C
	}
	var dc <-chan struct{}
	if onDirty {
		dc = r.dirty
	}
	select {
	case <-ctx.Done():
		return wakeCtx
	case <-tc:
		return wakeTimer
	case <-r.kick:
		return wakeKick
	case <-dc:
		return wakeDirty
	}
}

func (d *Daemon) relayStep(ctx context.Context, fails *int, hold *relayHold) {
	r := d.relay()
	in := d.relayGather(d.relayNow())
	if !in.wanted || len(in.entries) == 0 {
		d.relayToOff(ctx, in)
		*hold = relayHold{}
		r.wait(ctx, -1, true)
		return
	}
	d.relayReleaseStale(ctx, in)
	if w := time.Until(hold.until); w > 0 {
		if r.wait(ctx, w, false) == wakeKick && hold.hard {
			*hold = relayHold{}
		}
		return
	}
	out := d.relayAttempt(ctx, in)
	if ctx.Err() != nil {
		return
	}
	if out.resetFails {
		*fails = 0
	}
	switch out.state {
	case protocol.HubRelayStateUnsupported:
		d.relaySet(out.state, out.reason)
		*hold = relayHold{until: time.Now().Add(r.unsupportedRecheck), hard: true}
	case protocol.HubRelayStateRefused:
		d.relaySet(out.state, out.reason)
		*hold = relayHold{until: time.Now().Add(r.unsupportedRecheck), hard: true}
	case protocol.HubRelayStateOff:
		// Re-evaluate at once: the inputs changed under the channel.
		*hold = relayHold{}
	default:
		d.relaySet(protocol.HubRelayStateBackoff, out.reason)
		*fails = min(*fails+1, 30)
		*hold = relayHold{until: time.Now().Add(r.delay(*fails-1, out.floor))}
	}
}

// relayToOff moves to off for the reason in, first taking the registrations
// off the hub when there are any (a revoked device cannot sign, and the hub
// sweeps its entries).
func (d *Daemon) relayToOff(ctx context.Context, in relayInputs) {
	reason := in.reason
	if reason == "" {
		reason = "disabled"
	}
	r := d.relay()
	if reason == "revoked" {
		r.mu.Lock()
		r.reg = hub.RelayStatus{}
		r.mu.Unlock()
	} else {
		d.hubRelayRelease(ctx)
	}
	d.relaySet(protocol.HubRelayStateOff, reason)
}

// relayReleaseStale clears registrations that live on another hub or were made
// by another device identity than the one this evaluation would use.
func (d *Daemon) relayReleaseStale(ctx context.Context, in relayInputs) {
	r := d.relay()
	r.mu.Lock()
	reg := r.reg
	r.mu.Unlock()
	if reg.Registered > 0 && strings.TrimSuffix(reg.RegisteredURL, "/") != strings.TrimSuffix(in.hub.URL, "/") {
		d.hubRelayRelease(ctx)
	}
}

// relayOutcome is how one attempt ended.
type relayOutcome struct {
	state      string
	reason     string
	floor      time.Duration
	resetFails bool
}

func relayBackoff(reason string) relayOutcome {
	return relayOutcome{state: protocol.HubRelayStateBackoff, reason: reason}
}

// relayClassify turns a failed signed call into the state it implies. A 401
// goes through hubRevokedIf, which drops the identity and the policy overlay;
// its reload kicks the loop, which then finds "revoked".
func (d *Daemon) relayClassify(ctx context.Context, err error) relayOutcome {
	d.relayNoteErr(err)
	var ae *hub.APIError
	if errors.As(err, &ae) {
		switch {
		case ae.Status == 404:
			return relayOutcome{state: protocol.HubRelayStateUnsupported, reason: "no_relay"}
		case d.hubRevokedIf(ctx, err):
			return relayOutcome{state: protocol.HubRelayStateRefused, reason: "revoked"}
		case ae.Status == 429:
			floor := max(hubproto.RelayRateLimitedFloor, ae.RetryAfter)
			return relayOutcome{state: protocol.HubRelayStateBackoff, reason: "rate_limited", floor: min(floor, hubproto.RelayBackoffMax)}
		}
	}
	return relayBackoff("error")
}

// relayAttempt runs sync → control dial → hello → serve until the channel ends.
func (d *Daemon) relayAttempt(ctx context.Context, in relayInputs) relayOutcome {
	r := d.relay()
	d.relaySet(protocol.HubRelayStateSyncing, "")
	api, rc, id, err := r.newClient(in.hub.URL, d.hubDir())
	if err != nil {
		d.relayNoteErr(err)
		return relayBackoff("identity")
	}
	if err := d.relaySync(ctx, api, id, in.hub.URL); err != nil {
		return d.relayClassify(ctx, err)
	}
	if ctx.Err() != nil {
		return relayBackoff("stopped")
	}
	d.relaySet(protocol.HubRelayStateConnecting, "")
	cctx, cancel := context.WithTimeout(ctx, hubproto.WSHandshakeTimeout)
	ws, err := rc.DialControl(cctx)
	cancel()
	if err != nil {
		return d.relayClassify(ctx, err)
	}
	stop := context.AfterFunc(ctx, func() { _ = ws.CloseNow(hubproto.WSCloseGoingAway) })
	defer stop()

	_ = ws.SetReadDeadline(time.Now().Add(relayHelloTimeout))
	raw, err := ws.ReadMessage(hubproto.RelayMaxControlMessage)
	if err != nil {
		_ = ws.CloseNow(hubproto.WSCloseGoingAway)
		d.relayNoteErr(err)
		return relayBackoff("hello")
	}
	_ = ws.SetReadDeadline(time.Time{})
	var hello hubproto.RelayMsg
	if json.Unmarshal(raw, &hello) != nil || hello.T != "hello" {
		_ = ws.CloseWithCode(hubproto.WSCloseProtocol)
		return relayBackoff("protocol")
	}
	if hello.V != hubproto.RelayControlVersion {
		_ = ws.CloseWithCode(hubproto.WSCloseNormal)
		return relayOutcome{state: protocol.HubRelayStateUnsupported, reason: "version"}
	}
	// hello's ping_s and max_conns are display values; they steer nothing.
	d.relaySet(protocol.HubRelayStateConnected, "")
	return d.relayServe(ctx, in, rc, id, ws)
}

type relayRead struct {
	raw []byte
	err error
}

// relayServe runs the connected state (§10.4): it reads control messages,
// answers incoming phones, and re-syncs when the rendezvous set changes.
func (d *Daemon) relayServe(ctx context.Context, in relayInputs, rc *hub.RelayClient, id hub.Identity, ws *hubproto.WSConn) relayOutcome {
	r := d.relay()
	connectedAt := time.Now()
	msgs := make(chan relayRead, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			raw, err := ws.ReadMessage(hubproto.RelayMaxControlMessage)
			select {
			case msgs <- relayRead{raw, err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()

	var (
		syncing  bool
		pending  bool
		syncDone = make(chan error, 1)
		retry    = time.NewTimer(time.Hour)
		bye      string
	)
	retry.Stop()
	defer retry.Stop()
	startSync := func() {
		if syncing {
			pending = true
			return
		}
		syncing, pending = true, false
		go func() {
			api, _, _, err := r.newClient(in.hub.URL, d.hubDir())
			if err == nil {
				err = d.relaySync(ctx, api, id, in.hub.URL)
			}
			syncDone <- err
		}()
	}
	finish := func() relayOutcome {
		out := relayBackoff("closed")
		out.resetFails = time.Since(connectedAt) >= hubproto.RelayBackoffResetAfter
		switch bye {
		case "replaced":
			d.logf("", "hub relay: another process with this device key holds the control channel")
			out.reason = "replaced"
		case "revoked":
			out.reason = "revoked_by_hub" // confirmed by the next signed call
		case "shutdown":
			out.resetFails = true
		}
		return out
	}

	for {
		select {
		case <-ctx.Done():
			_ = ws.CloseNow(hubproto.WSCloseGoingAway)
			return relayBackoff("stopped")
		case rd := <-msgs:
			if rd.err != nil {
				if code, ok := ws.PeerCloseCode(); ok {
					switch code {
					case hubproto.RelayCloseReplaced:
						bye = "replaced"
					case hubproto.RelayCloseRevoked:
						bye = "revoked"
					}
				}
				return finish()
			}
			var m hubproto.RelayMsg
			if json.Unmarshal(rd.raw, &m) != nil || m.Validate() != nil {
				_ = ws.CloseWithCode(hubproto.WSCloseProtocol)
				return relayBackoff("protocol")
			}
			switch m.T {
			case "incoming":
				d.relayIncoming(ctx, rc, ws, m)
			case "cancel":
				r.mu.Lock()
				cancel := r.inflight[m.Conn]
				r.mu.Unlock()
				if cancel != nil {
					cancel()
				}
			case "bye":
				switch m.Reason {
				case "replaced", "revoked", "shutdown":
					bye = m.Reason
				default:
					bye = "shutdown"
				}
			}
		case <-r.kick:
			cur := d.relayGather(d.relayNow())
			if !cur.wanted || len(cur.entries) == 0 || !sameHub(cur.hub.URL, in.hub.URL) {
				_ = ws.CloseWithCode(hubproto.WSCloseNormal)
				return relayOutcome{state: protocol.HubRelayStateOff}
			}
			if d.relayIdentityChanged(in.hub.URL, id) {
				_ = ws.CloseWithCode(hubproto.WSCloseNormal)
				return relayOutcome{state: protocol.HubRelayStateOff}
			}
		case <-r.dirty:
			// A sync that started at this generation already covers it.
			r.mu.Lock()
			covered := r.syncedGen == r.gen.Load() && !syncing
			r.mu.Unlock()
			if !covered {
				startSync()
			}
		case err := <-syncDone:
			syncing = false
			switch {
			case err != nil:
				d.relayNoteErr(err)
				if o := d.relayClassifyQuiet(ctx, err); o.state == protocol.HubRelayStateRefused || o.state == protocol.HubRelayStateUnsupported {
					_ = ws.CloseWithCode(hubproto.WSCloseNormal)
					return o
				}
				retry.Reset(r.dirtyRetry)
			case pending:
				startSync()
			}
		case <-retry.C:
			startSync()
		}
	}
}

// relayClassifyQuiet is relayClassify for a failure already noted.
func (d *Daemon) relayClassifyQuiet(ctx context.Context, err error) relayOutcome {
	var ae *hub.APIError
	if errors.As(err, &ae) {
		switch {
		case ae.Status == 404:
			return relayOutcome{state: protocol.HubRelayStateUnsupported, reason: "no_relay"}
		case d.hubRevokedIf(ctx, err):
			return relayOutcome{state: protocol.HubRelayStateRefused, reason: "revoked"}
		}
	}
	return relayBackoff("error")
}

func sameHub(a, b string) bool { return strings.TrimSuffix(a, "/") == strings.TrimSuffix(b, "/") }

func (d *Daemon) relayIdentityChanged(url string, was hub.Identity) bool {
	id, _, err := relayIdentity(url, d.hubDir())
	return err != nil || id.DeviceID != was.DeviceID
}

// relayIncoming answers one `incoming` message (§10.4). Every refusal is a
// `reject` to the hub with a closed reason; no dial happens until all gates
// passed.
func (d *Daemon) relayIncoming(ctx context.Context, rc *hub.RelayClient, ws *hubproto.WSConn, m hubproto.RelayMsg) {
	r := d.relay()
	reject := func(reason string) {
		b, _ := json.Marshal(hubproto.RelayMsg{T: "reject", Conn: m.Conn, Reason: reason})
		_ = ws.SetWriteDeadline(time.Now().Add(relayRejectWrite))
		_ = ws.WriteMessage(b)
		_ = ws.SetWriteDeadline(time.Time{})
	}
	known := false
	for _, e := range d.relayEntries(d.relayNow()) {
		if e.RV == m.RV {
			known = true
			break
		}
	}
	if !known {
		reject("unknown")
		return
	}
	if !r.incoming.take() {
		reject("busy")
		return
	}
	d.remoteMu.Lock()
	srv := d.remote
	d.remoteMu.Unlock()
	if srv == nil {
		reject("off")
		return
	}
	slot, ok := srv.ReserveRelay(m.RV)
	if !ok {
		reject("busy")
		return
	}
	select {
	case r.accepting <- struct{}{}:
	default:
		slot.Release()
		reject("busy")
		return
	}
	go func() {
		defer func() { <-r.accepting }()
		defer func() {
			if p := recover(); p != nil {
				slot.Release()
				d.logf("", "hub relay: accept panicked: %v", p)
			}
		}()
		actx, cancel := context.WithTimeout(ctx, hubproto.RelayAcceptTimeout)
		r.mu.Lock()
		r.inflight[m.Conn] = cancel
		r.mu.Unlock()
		defer func() {
			cancel()
			r.mu.Lock()
			delete(r.inflight, m.Conn)
			r.mu.Unlock()
		}()
		aws, err := rc.DialAccept(actx, m.Conn)
		if err != nil {
			slot.Release()
			if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
				d.logf("", "hub relay: accept leg failed: %s", relayErrText(err))
			}
			d.hubRevokedIf(ctx, err)
			return
		}
		stop := context.AfterFunc(ctx, func() { _ = aws.CloseNow(hubproto.WSCloseGoingAway) })
		defer stop()
		if err := srv.ServeRelayed(r.count(aws), remote.RelayMeta{RV: m.RV}, slot); err != nil {
			d.logf("", "hub relay: serve relayed connection: %v", err)
		}
	}()
}

// countedConn counts a relayed connection while it lives. relayNetConn closes
// through CloseNow when the server is closing, so both are forwarded.
type countedConn struct {
	net.Conn
	r    *hubRelay
	once sync.Once
}

func (r *hubRelay) count(ws *hubproto.WSConn) net.Conn {
	r.mu.Lock()
	r.active++
	r.mu.Unlock()
	return &countedConn{Conn: ws, r: r}
}

func (c *countedConn) release() {
	c.once.Do(func() {
		c.r.mu.Lock()
		c.r.active--
		c.r.mu.Unlock()
	})
}

func (c *countedConn) Close() error {
	defer c.release()
	return c.Conn.Close()
}

func (c *countedConn) CloseNow(code uint16) error {
	defer c.release()
	if cn, ok := c.Conn.(interface{ CloseNow(uint16) error }); ok {
		return cn.CloseNow(code)
	}
	return c.Conn.Close()
}

// ---- sync and release ------------------------------------------------------

// relaySync replaces the hub's rendezvous set with the daemon's. EVERY sync
// goes through syncMu, so an older replace-all can never land after a newer
// one and bring back a revoked phone's rv; the set is rebuilt after the
// generation moves during a POST.
func (d *Daemon) relaySync(ctx context.Context, c *hub.Client, id hub.Identity, hubURL string) error {
	r := d.relay()
	r.syncMu.Lock()
	defer r.syncMu.Unlock()
	for range relaySyncRepeatMax {
		g := r.gen.Load()
		entries := d.relayEntries(d.relayNow())
		sctx, cancel := context.WithTimeout(ctx, hub.DefaultRequestTimeout)
		resp, err := c.SyncRendezvous(sctx, entries)
		cancel()
		if err != nil {
			return err
		}
		d.relayConflicts(resp.Conflicts)
		r.mu.Lock()
		r.reg.Registered = resp.Registered
		if len(entries) == 0 {
			r.reg.RegisteredURL, r.reg.RegisteredDevice = "", ""
		} else {
			r.reg.RegisteredURL, r.reg.RegisteredDevice = strings.TrimSuffix(hubURL, "/"), id.DeviceID
		}
		r.syncedGen = g
		r.conflicts = map[string]bool{}
		for _, rv := range resp.Conflicts {
			r.conflicts[rv] = true
		}
		close(r.synced)
		r.synced = make(chan struct{})
		st := r.statusLocked()
		r.mu.Unlock()
		d.relaySave(st)
		if r.gen.Load() == g {
			return nil
		}
	}
	return nil
}

// relayConflicts reacts to rendezvous ids the hub refused because another
// device owns them. A window loses its relay; a device gets a fresh rv (its
// bearer is kept) and learns it from its next relayCred.
//
// The hub's list is a claim, so it moves nothing unbounded: an rv that names
// neither the window nor a device changes nothing, and a device's rv rotates
// at most once per relayConflictRotateEvery. Only a real change dirties the
// set (the registry write does that through devicesChanged), so a conflict
// the daemon declined to act on never starts another sync.
func (d *Daemon) relayConflicts(rvs []string) {
	if len(rvs) == 0 {
		return
	}
	r := d.relay()
	for _, rv := range rvs {
		r.mu.Lock()
		wID, wRV := r.windowID, r.windowRV
		r.mu.Unlock()
		if rv == wRV && wRV != "" {
			if d.pairer.SetWindowRelay(wID, "", "") {
				d.hubRelayDirty()
			}
			continue
		}
		dev, ok := d.devices.LookupRV(rv)
		if !ok || dev.Relay == nil || !d.relayConflictMayRotate(dev.ID) {
			continue
		}
		r.credMu.Lock()
		dev, ok = d.devices.LookupRV(rv)
		if ok && dev.Relay != nil {
			fresh, err := hubproto.NewRelayID()
			if err == nil {
				rl := *dev.Relay
				rl.RV = fresh
				if _, err := d.devices.SetRelay(dev.ID, &rl); err == nil {
					d.logf("", "hub relay: rendezvous conflict for device %s; new rendezvous id issued", dev.ID)
				}
			}
		}
		r.credMu.Unlock()
	}
}

// relayConflictMark is one device's last conflict-driven rotation.
type relayConflictMark struct {
	at     time.Time
	repeat bool // a further conflict inside the window was logged
}

// relayConflictMayRotate reports whether a conflict may rotate device id's rv
// now, and records the rotation when it may. A refused one is logged once per
// window.
func (d *Daemon) relayConflictMayRotate(id string) bool {
	r := d.relay()
	now := d.relayNow()
	r.mu.Lock()
	if r.conflictAt == nil {
		r.conflictAt = map[string]relayConflictMark{}
	}
	for k, m := range r.conflictAt {
		if now.Sub(m.at) >= relayConflictRotateEvery {
			delete(r.conflictAt, k)
		}
	}
	m, seen := r.conflictAt[id]
	if !seen {
		r.conflictAt[id] = relayConflictMark{at: now}
		r.mu.Unlock()
		return true
	}
	first := !m.repeat
	m.repeat = true
	r.conflictAt[id] = m
	r.mu.Unlock()
	if first {
		d.logf("", "hub relay: the hub reports another rendezvous conflict for device %s within %s; its rendezvous id is kept",
			id, relayConflictRotateEvery)
	}
	return false
}

// hubRelayRelease takes every registration off the hub the registrations live
// on (not necessarily [hub].url now). Best effort: a failure is logged and the
// persisted Registered count makes the next evaluation, or the next start,
// try again. hubLeave calls it before the identity goes.
func (d *Daemon) hubRelayRelease(ctx context.Context) {
	r := d.relay()
	// Under syncMu from the start: a sync in flight may be the very POST that
	// empties the set, and the count is only meaningful after it.
	r.syncMu.Lock()
	defer r.syncMu.Unlock()
	r.mu.Lock()
	reg := r.reg
	r.mu.Unlock()
	if reg.Registered == 0 {
		return
	}
	giveUp := func(why string) {
		r.mu.Lock()
		first := !r.relDone
		r.relDone = true
		r.mu.Unlock()
		if first {
			d.logf("", "hub relay: cannot release registrations (%s); they expire with the hub's own sweep", why)
		}
	}
	if reg.RegisteredURL == "" {
		giveUp("no hub recorded")
		return
	}
	api, _, id, err := r.newClient(reg.RegisteredURL, d.hubDir())
	if err != nil || id.DeviceID != reg.RegisteredDevice {
		giveUp("this device no longer holds that identity")
		return
	}
	rctx, cancel := context.WithTimeout(ctx, hub.DefaultRequestTimeout)
	defer cancel()
	if _, err := api.SyncRendezvous(rctx, nil); err != nil {
		d.hubRevokedIf(ctx, err)
		d.logf("", "hub relay: release registrations: %s (retried later)", relayErrText(err))
		return
	}
	r.mu.Lock()
	r.reg = hub.RelayStatus{}
	r.relDone = false
	st := r.statusLocked()
	r.mu.Unlock()
	d.relaySave(st)
}

// ---- pairing and credentials -----------------------------------------------

// relayPairOffer gives a new pairing window a relay rendezvous (§10.6): a
// fresh rv and window token registered with the hub BEFORE the QR is shown.
// nil means the QR carries no relay (not eligible, hub too old or refusing,
// a conflict, or no sync within relayPairOfferWait).
func (d *Daemon) relayPairOffer(ctx context.Context, t remote.Ticket) *remote.PairRelayQR {
	if !d.relayEligible() {
		return nil
	}
	r := d.relay()
	rv, err := hubproto.NewRelayID()
	if err != nil {
		return nil
	}
	tok, tokHash, err := hubproto.NewRelayBearer()
	if err != nil {
		return nil
	}
	if !d.pairer.SetWindowRelay(t.ID, rv, tokHash) {
		return nil
	}
	r.mu.Lock()
	r.windowID, r.windowRV = t.ID, rv
	r.mu.Unlock()
	d.hubRelayDirty()
	want := r.gen.Load()
	d.hubRelayNudge()

	fail := func() *remote.PairRelayQR {
		d.pairer.SetWindowRelay(t.ID, "", "")
		d.hubRelayDirty()
		return nil
	}
	d.mu.Lock()
	url := strings.TrimSuffix(d.cfg.Hub.URL, "/")
	d.mu.Unlock()
	timer := time.NewTimer(r.pairWait)
	defer timer.Stop()
	for {
		r.mu.Lock()
		ch, ok, conflict, st := r.synced, r.syncedGen >= want, r.conflicts[rv], r.state
		r.mu.Unlock()
		if conflict || st == protocol.HubRelayStateUnsupported || st == protocol.HubRelayStateRefused {
			return fail()
		}
		if ok {
			if _, _, _, live := d.pairer.Window(); !live {
				return nil
			}
			return &remote.PairRelayQR{URL: url, RV: rv, Tok: tok}
		}
		select {
		case <-ch:
		case <-timer.C:
			return fail()
		case <-ctx.Done():
			return fail()
		}
	}
}

// relayMint makes a newly confirmed phone's relay credential with a FRESH rv:
// the QR's rv and token die with the window, so a photographed QR is useless
// after pairing. nil when relay is not eligible (the phone asks later through
// relayCred).
func (d *Daemon) relayMint() *remote.ConfirmRelay {
	if !d.relayEligible() {
		return nil
	}
	rv, err := hubproto.NewRelayID()
	if err != nil {
		return nil
	}
	bearer, hash, err := hubproto.NewRelayBearer()
	if err != nil {
		return nil
	}
	return &remote.ConfirmRelay{Bearer: bearer, Record: devices.DeviceRelay{RV: rv, BearerHash: hash, RotatedAt: d.relayNow()}}
}

// handleRelayCred answers a paired phone's credential refresh (§10.6). It is
// self-only: the device comes from the connection's authenticated peer, never
// from the request. The reply (a plaintext bearer at most) is a response on
// the caller's own connection.
func (d *Daemon) handleRelayCred(ctx context.Context, a protocol.RelayCredArgs) (protocol.RelayCredData, error) {
	peer, ok := remote.PeerFromContext(ctx)
	if !ok {
		return protocol.RelayCredData{}, errors.New("relay credentials need a paired phone connection")
	}
	if a.Holds != "" && !hubproto.ValidRelayHash(a.Holds) {
		return protocol.RelayCredData{}, errors.New("holds is not a bearer hash")
	}
	if !d.relayEligible() {
		return protocol.RelayCredData{Available: false}, nil
	}
	d.mu.Lock()
	url := strings.TrimSuffix(d.cfg.Hub.URL, "/")
	d.mu.Unlock()

	r := d.relay()
	r.credMu.Lock()
	defer r.credMu.Unlock()
	dev, ok := d.devices.Get(peer.DeviceID)
	if !ok {
		return protocol.RelayCredData{}, errors.New("this device is not paired")
	}
	now := d.relayNow()
	out := protocol.RelayCredData{Available: true, URL: url}
	var action string
	var next *devices.DeviceRelay
	rl := dev.Relay
	holds := func(h string) bool {
		return a.Holds != "" && h != "" && subtle.ConstantTimeCompare([]byte(a.Holds), []byte(h)) == 1
	}
	mint := func(prev string, prevUntil time.Time, rv string) (*devices.DeviceRelay, error) {
		bearer, hash, err := hubproto.NewRelayBearer()
		if err != nil {
			return nil, err
		}
		out.Bearer = bearer
		return &devices.DeviceRelay{RV: rv, BearerHash: hash, PrevHash: prev, PrevUntil: prevUntil, RotatedAt: now}, nil
	}
	var err error
	switch {
	case rl == nil:
		var rv string
		if rv, err = hubproto.NewRelayID(); err == nil {
			next, err = mint("", time.Time{}, rv)
		}
		action = "issued"
	case holds(rl.BearerHash) && now.Sub(rl.RotatedAt) < hubproto.RelayBearerMaxAge:
		out.RV = rl.RV
		action = "current"
		if rl.PrevHash != "" {
			c := *rl
			c.PrevHash, c.PrevUntil = "", time.Time{}
			next, action = &c, "ack"
		}
	case holds(rl.BearerHash):
		next, err = mint(rl.BearerHash, now.Add(hubproto.RelayPrevBearerGrace), rl.RV)
		action = "rotated"
	case holds(rl.PrevHash) && rl.PrevUntil.After(now):
		// The phone never stored the last rotation: it still holds the previous
		// bearer, which stays live.
		next, err = mint(rl.PrevHash, now.Add(hubproto.RelayPrevBearerGrace), rl.RV)
		action = "reissued"
	default:
		next, err = mint("", time.Time{}, rl.RV)
		action = "reissued"
	}
	if err != nil {
		return protocol.RelayCredData{}, errors.New("could not mint a relay credential")
	}
	if next != nil {
		if _, err := d.devices.SetRelay(dev.ID, next); err != nil {
			return protocol.RelayCredData{}, errors.New("could not save the relay credential")
		}
		out.RV = next.RV
	}
	// Never a bearer or hash in the log.
	d.logf("", "hub relay: cred device=%s action=%s via=%s", dev.ID, action, relayVia(peer))
	return out, nil
}

func relayVia(p remote.Peer) string {
	if p.Via == remote.ViaRelay {
		return remote.ViaRelay
	}
	return remote.ViaLAN
}
