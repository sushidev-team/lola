package daemon

// hubwire.go — the daemon side of lola-hub (see lola-hub's DESIGN.md, "Daemon
// side"). One loop, off by default, that turns a team hub's OFFERS into local
// sessions without ever letting the hub decide anything:
//
//	heartbeat (capacity, opted-in repos, live leases)
//	  → report findings (gh, at most once an hour per repo)
//	  → list offers → per offer: hub.Accept → dispatchHold → health gate →
//	    reserveSlots → count the claim → Claim → spawn → Session.HubItem
//	  → outcomes (complete / fail / release), queued and retried
//	  → policy (verified, stored; a new version reloads to apply it), every 5 min
//
// Rules this file keeps (AGENTS.md, "Hub work is a CLAIM and a candidate"):
//
//   - An offer is a CANDIDATE. It passes exactly the gates a Linear dispatch
//     passes, plus hub.Accept (opt-in, kind, daily cap, sandbox). A hub item
//     never reaches an agent as text: hub.Briefing renders lola's own template
//     from fields hubproto.Validate already checked.
//   - The claim is the in-flight mark. The daily count is persisted BEFORE the
//     claim (a crash cannot exceed max_claims_per_day) and refunded only when
//     the claim itself was refused. A failed SPAWN keeps its count: otherwise a
//     broken project would claim and release the same item every cycle.
//   - The hub never stops a session. A lost lease is logged and recorded on
//     the session; the agent keeps working and lola stops reporting for it.
//   - An offer is the hub's CLAIM about a repository. Before the claim the
//     daemon re-reads it from GitHub (hub.Collector.Verify): a red bump PR
//     must be open, by the named bot, from the same repository and on the
//     named branch; an advisory must match an open Dependabot alert. The
//     session is built from GitHub's answer, never the hub's fields.
//   - Outcomes are one-shot, per CLAIM (item id + claim time): the outcome is
//     persisted in the outbox FIRST, then the guard (HubWork.Reported) is
//     stamped, so a crash neither loses one nor (the outbox dedups by claim)
//     repeats it. A re-claim of the same item purges what an older claim left
//     queued, so a stale release can never free the new lease.
//   - An item this machine ended locally (a person stopped it, the agent gave
//     up) is not claimed again for hubCooldown.
//   - Shutdown-shielded and panic-guarded like the observer: the cycle runs on
//     context.WithoutCancel, every request is bounded by the client's timeout,
//     every gh call by the collector's, a spawn by nativeSpawnTimeout.
//   - Nothing local reaches the hub but what the protocol names: repo slugs,
//     validated findings, item ids, a PR URL and a delivery word. Never a
//     session id, a path or pane text.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

const (
	// hubFindingsEvery rate-limits the gh findings scan per repo.
	hubFindingsEvery = time.Hour
	// hubPolicyEvery rate-limits the policy fetch. Short, because a fetched
	// policy is now APPLIED: an admin's change should reach members within
	// minutes. cmd=hubSync fetches at once.
	hubPolicyEvery = 5 * time.Minute
	// hubOutboxMaxAge drops an outcome the hub has not acknowledged for this
	// long: the lease has long expired and the hub reopened the item anyway.
	hubOutboxMaxAge = 24 * time.Hour
	// hubStateFile is the loop's persisted state under ~/.lola/state. Not
	// hub.json (the identity, owned by internal/hub).
	hubStateFile = "hub-daemon.json"
	// hubMaxOutbox bounds the outbox; the oldest entries go first.
	hubMaxOutbox = 256
	// hubCooldown is how long an item this machine ended without completing
	// it stays unclaimable here, so a person who stopped a chore does not get
	// it back on the next cycle.
	hubCooldown = 24 * time.Hour
)

// Outcome actions in the outbox.
const (
	hubActComplete = "complete"
	hubActFail     = "fail"
	hubActRelease  = "release"
)

// hubAPI is the daemon's seam over *hub.Client: everything the loop sends.
// Tests install a fake through hubWire.newClient.
type hubAPI interface {
	Heartbeat(ctx context.Context, hb hubproto.HeartbeatRequest) (hubproto.HeartbeatResponse, error)
	ReportFindings(ctx context.Context, items []hubproto.Item) (hubproto.FindingsResponse, error)
	Offers(ctx context.Context) ([]hubproto.Item, int, error)
	Claim(ctx context.Context, id string) (hubproto.Lease, error)
	Complete(ctx context.Context, id string, req hubproto.CompleteRequest) error
	Fail(ctx context.Context, id string, reason hubproto.FailReason) error
	Release(ctx context.Context, id string) error
	// Board and Org are the read-only org views (validated whole).
	Board(ctx context.Context, state hubproto.State, limit int) (hubproto.BoardResponse, error)
	Org(ctx context.Context) (hubproto.OrgView, error)
	// FetchPolicy is hub.FetchPolicy bound to this client: verify against the
	// trusted keys, refuse rollback, store. Display only.
	FetchPolicy(ctx context.Context, h config.HubConfig, org, dir string, now time.Time) (hub.StoredPolicy, error)
}

// realHub adapts *hub.Client to hubAPI.
type realHub struct{ *hub.Client }

func (r realHub) FetchPolicy(ctx context.Context, h config.HubConfig, org, dir string, now time.Time) (hub.StoredPolicy, error) {
	return hub.FetchPolicy(ctx, r.Client, h, org, dir, now)
}

// newRealHubClient loads the identity and key from dir and builds a signed
// client for url. It fails CLOSED on every mismatch: an identity for another
// hub, or a key that is not the one that enrolled, sends nothing.
func newRealHubClient(url, dir string) (hubAPI, hub.Identity, error) {
	id, err := hub.LoadIdentity(dir)
	if err != nil {
		return nil, hub.Identity{}, err
	}
	if strings.TrimSuffix(id.URL, "/") != strings.TrimSuffix(url, "/") {
		return nil, id, fmt.Errorf("identity belongs to %s, config names %s: run `lola hub leave` and join again", id.URL, url)
	}
	key, err := hub.LoadKey(dir)
	if err != nil {
		return nil, id, err
	}
	if key.DeviceID() != id.DeviceID {
		return nil, id, errors.New("the device key on disk is not the one that enrolled")
	}
	c, err := hub.NewClient(url, key)
	if err != nil {
		return nil, id, err
	}
	return realHub{c}, id, nil
}

// hubOutcome is one queued report for the hub.
type hubOutcome struct {
	ItemID string `json:"item_id"`
	// ClaimedAt identifies the CLAIM the outcome belongs to: the hub may
	// reopen an item and this daemon claim it again, and the second claim's
	// outcome must not be mistaken for the first's.
	ClaimedAt time.Time `json:"claimed_at,omitzero"`
	Action    string    `json:"action"`
	Reason    string    `json:"reason,omitempty"`
	PRURL     string    `json:"pr_url,omitempty"`
	Delivery  string    `json:"delivery,omitempty"`
	Since     time.Time `json:"since"`
	Tries     int       `json:"tries,omitempty"`
}

// key is the outcome's claim identity: one outcome per claim.
func (o hubOutcome) key() string {
	return fmt.Sprintf("%s@%d", o.ItemID, o.ClaimedAt.UnixNano())
}

// hubPersisted is hub-daemon.json.
type hubPersisted struct {
	// ClaimDay (local YYYY-MM-DD) and Claims count claims for max_claims_per_day.
	ClaimDay string `json:"claim_day,omitempty"`
	Claims   int    `json:"claims,omitempty"`
	// FindingsAt is the last findings scan per lowercased repo slug.
	FindingsAt map[string]time.Time `json:"findings_at,omitempty"`
	// PolicyAt is the last policy fetch attempt.
	PolicyAt time.Time `json:"policy_at,omitzero"`
	// Outbox holds outcomes not yet acknowledged by the hub.
	Outbox []hubOutcome `json:"outbox,omitempty"`
	// Settled are the claim keys (hubOutcome.key) whose outcome left the
	// outbox (sent, refused for good or expired), newest last: a second end
	// signal for the same claim must not report it again.
	Settled []string `json:"settled,omitempty"`
	// Cooldown holds item ids this machine ended without completing, with
	// when; hubTryClaim skips them for hubCooldown.
	Cooldown map[string]time.Time `json:"cooldown,omitempty"`
}

// hubWire is the loop's state. Its own lock; never held while taking d.mu or
// calling the hub.
type hubWire struct {
	// Seams (tests replace them).
	newClient func(url, dir string) (hubAPI, hub.Identity, error)
	collect   func(ctx context.Context, repos []string) hub.Result
	// verify re-reads an offer from GitHub before it is claimed.
	verify func(ctx context.Context, it hubproto.Item) (hub.Verified, error)
	now    func() time.Time
	// enroll builds the client cmd=hubJoin enrolls with (nil = hub.NewClient).
	enroll func(url string, key *hub.Key) (*hub.Client, error)

	// full wakes the loop for a whole cycle (reload, join); outcomes wakes it
	// for the outbox + outcome pass only (a delivery change, a teardown).
	full     chan struct{}
	outcomes chan struct{}

	mu         sync.Mutex
	loaded     bool
	st         hubPersisted
	joined     bool
	device     string
	deviceName string
	org        string
	lastBeat   time.Time
	lastErr    string
	lastErrT   time.Time
	policyV    int
	// policy is the stored, verified bundle (nil = none known).
	policy *hub.StoredPolicy
	// policyRead: the stored policy was read once for status, so a restart
	// shows its version before the next fetch is due.
	policyRead bool
	// policyReloadedV is the policy version a reload was last asked for, so
	// one that cannot apply does not reload on every fetch (hubpolicy.go).
	policyReloadedV int
	// reload is the reload a newly stored policy triggers (nil = handleReload);
	// a test seam.
	reload func(context.Context) error
	// findings is the last findings report (nil = none since start).
	findings *protocol.HubFindingsInfo
	// claiming holds the item ids whose claim is in flight, so the loop and
	// a person's click cannot both claim (and spawn) one item.
	claiming map[string]bool
	// board / org cache the read-only views for the UIs (hubcmds.go).
	board   map[string]hubBoardCache
	orgAt   time.Time
	orgView *hubproto.OrgView
}

func newHubWire() *hubWire {
	col := &hub.Collector{Gh: hub.ExecGh("gh", hub.DefaultGhMaxOutput)}
	return &hubWire{
		newClient: newRealHubClient,
		collect:   col.Collect,
		verify:    col.Verify,
		now:       time.Now,
		full:      make(chan struct{}, 1),
		outcomes:  make(chan struct{}, 1),
	}
}

func poke(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// hubNudge asks for a whole cycle soon (after a reload).
func (d *Daemon) hubNudge() {
	if d.hub != nil {
		poke(d.hub.full)
	}
}

// hubNudgeOutcomes asks for an outcome pass soon. Cheap and non-blocking, so
// the observer may call it from its loop.
func (d *Daemon) hubNudgeOutcomes() {
	if d.hub != nil {
		poke(d.hub.outcomes)
	}
}

func (d *Daemon) hubDir() string { return filepath.Join(d.home, "state") }

// ---- persisted state -------------------------------------------------------

// loadLocked reads hub-daemon.json once. An unreadable file starts empty —
// except that a file that exists but does not parse is logged, because losing
// the claim count would let a restart exceed the daily cap.
func (d *Daemon) hubLoadLocked() {
	w := d.hub
	if w.loaded {
		return
	}
	w.loaded = true
	b, err := os.ReadFile(filepath.Join(d.hubDir(), hubStateFile))
	if err != nil {
		return
	}
	if err := json.Unmarshal(b, &w.st); err != nil {
		d.logf("", "hub: %s does not parse, starting empty: %v", hubStateFile, err)
		w.st = hubPersisted{}
	}
}

func (d *Daemon) hubSaveLocked() error {
	b, err := json.MarshalIndent(d.hub.st, "", "  ")
	if err != nil {
		return err
	}
	dir := d.hubDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, hubStateFile+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, hubStateFile))
}

func hubDay(t time.Time) string { return t.Local().Format("2006-01-02") }

// hubClaimsToday returns today's claim count.
func (d *Daemon) hubClaimsToday(now time.Time) int {
	d.hub.mu.Lock()
	defer d.hub.mu.Unlock()
	d.hubLoadLocked()
	if d.hub.st.ClaimDay != hubDay(now) {
		return 0
	}
	return d.hub.st.Claims
}

// hubCountClaim adds delta to today's count and persists it. It refuses
// (returns an error) when the count cannot be persisted, so a claim is never
// made that a restart would forget.
func (d *Daemon) hubCountClaim(now time.Time, delta int) error {
	w := d.hub
	w.mu.Lock()
	defer w.mu.Unlock()
	d.hubLoadLocked()
	day := hubDay(now)
	if w.st.ClaimDay != day {
		w.st.ClaimDay, w.st.Claims = day, 0
	}
	w.st.Claims = max(0, w.st.Claims+delta)
	return d.hubSaveLocked()
}

// hubEnqueue adds an outcome to the outbox and persists it.
func (d *Daemon) hubEnqueue(o hubOutcome) {
	w := d.hub
	w.mu.Lock()
	defer w.mu.Unlock()
	d.hubLoadLocked()
	k := o.key()
	for _, q := range w.st.Outbox {
		if q.key() == k {
			return // one outcome per claim
		}
	}
	if slices.Contains(w.st.Settled, k) {
		return
	}
	w.st.Outbox = append(w.st.Outbox, o)
	if n := len(w.st.Outbox); n > hubMaxOutbox {
		w.st.Outbox = slices.Clone(w.st.Outbox[n-hubMaxOutbox:])
	}
	if err := d.hubSaveLocked(); err != nil {
		d.logf("", "hub: persist outbox: %v", err)
	}
}

// hubForgetClaims drops everything an OLDER claim of id left behind: a queued
// outcome (a stale release sent now would free the new lease) and its settled
// mark. Called right after a successful claim, before the spawn.
func (d *Daemon) hubForgetClaims(id string, claimedAt time.Time) {
	w := d.hub
	w.mu.Lock()
	defer w.mu.Unlock()
	d.hubLoadLocked()
	keep := w.st.Outbox[:0:0]
	for _, o := range w.st.Outbox {
		if o.ItemID != id || o.ClaimedAt.Equal(claimedAt) {
			keep = append(keep, o)
		}
	}
	dropped := len(w.st.Outbox) - len(keep)
	w.st.Outbox = keep
	if dropped > 0 {
		d.logf("", "hub: re-claimed item %s; dropped %d outcome(s) of an earlier claim", id, dropped)
		if err := d.hubSaveLocked(); err != nil {
			d.logf("", "hub: persist outbox: %v", err)
		}
	}
}

// hubCoolDown marks id as ended here without completion.
func (d *Daemon) hubCoolDown(id string, now time.Time) {
	w := d.hub
	w.mu.Lock()
	defer w.mu.Unlock()
	d.hubLoadLocked()
	if w.st.Cooldown == nil {
		w.st.Cooldown = map[string]time.Time{}
	}
	for k, at := range w.st.Cooldown {
		if now.Sub(at) >= hubCooldown {
			delete(w.st.Cooldown, k)
		}
	}
	w.st.Cooldown[id] = now
	if err := d.hubSaveLocked(); err != nil {
		d.logf("", "hub: persist cooldown: %v", err)
	}
}

// hubCoolingDown reports whether id ended here within hubCooldown.
func (d *Daemon) hubCoolingDown(id string, now time.Time) bool {
	w := d.hub
	w.mu.Lock()
	defer w.mu.Unlock()
	d.hubLoadLocked()
	at, ok := w.st.Cooldown[id]
	return ok && now.Sub(at) < hubCooldown
}

// hubUnsent lists the item ids with an unacknowledged complete/fail in the
// outbox: their leases are still renewed, or a retried outcome would meet an
// expired lease and be refused for good.
func (d *Daemon) hubUnsent() []string {
	w := d.hub
	w.mu.Lock()
	defer w.mu.Unlock()
	d.hubLoadLocked()
	var ids []string
	for _, o := range w.st.Outbox {
		if o.Action != hubActRelease && !slices.Contains(ids, o.ItemID) {
			ids = append(ids, o.ItemID)
		}
	}
	return ids
}

func (d *Daemon) hubSetErr(stage string, err error) {
	w := d.hub
	msg := stage + ": " + err.Error()
	w.mu.Lock()
	w.lastErr, w.lastErrT = msg, w.now()
	w.mu.Unlock()
	d.logf("", "hub: %s", msg)
}

// ---- loop ------------------------------------------------------------------

// hubLoop runs a cycle at startup and then every [hub].poll_seconds (re-read
// each time), plus whenever nudged. It does nothing while the hub is off.
func (d *Daemon) hubLoop(ctx context.Context) {
	defer d.wg.Done()
	d.safeHub(ctx, true)
	for {
		t := time.NewTimer(d.hubInterval())
		full := true
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		case <-d.hub.full:
			t.Stop()
		case <-d.hub.outcomes:
			t.Stop()
			full = false
		}
		d.safeHub(ctx, full)
	}
}

func (d *Daemon) hubInterval() time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	return time.Duration(d.cfg.Hub.EffectivePollSeconds()) * time.Second
}

// safeHub runs one cycle, shielded from shutdown (an in-flight claim must not
// be cut between the claim and the spawn) and from panics.
func (d *Daemon) safeHub(ctx context.Context, full bool) {
	defer func() {
		if r := recover(); r != nil {
			d.logf("", "hub panic (daemon keeps running): %v", r)
		}
	}()
	ctx = context.WithoutCancel(ctx)
	if full {
		d.hubCycle(ctx)
	} else {
		d.hubOutcomeCycle(ctx)
	}
}

// hubSnapshot is the config a cycle needs, cloned under d.mu.
type hubSnapshot struct {
	h         config.HubConfig
	repos     []string
	agents    []string
	globalCap int
}

// hubConfig returns the cycle's config, or ok=false when the hub is off or the
// config failed validation (polls are held then; so is the hub).
func (d *Daemon) hubConfig() (hubSnapshot, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cfg == nil || !d.cfg.Hub.Enabled || d.cfgErr != "" {
		return hubSnapshot{}, false
	}
	h := d.cfg.Hub
	h.AcceptKinds = slices.Clone(h.AcceptKinds)
	h.TrustedPolicyKeys = maps.Clone(h.TrustedPolicyKeys)
	snap := hubSnapshot{h: h, repos: hub.OptedInRepos(d.cfg), globalCap: d.cfg.Defaults.GlobalCap}
	for i := range d.cfg.Projects {
		p := &d.cfg.Projects[i]
		if !p.HubChores || p.IsStack() {
			continue
		}
		name := string(agent.Parse(d.cfg.AgentForProject(p.Name)))
		if !slices.Contains(snap.agents, name) && len(snap.agents) < hubproto.MaxHeartbeatAgents {
			snap.agents = append(snap.agents, name)
		}
	}
	return snap, true
}

// hubConnect builds the client for this cycle, recording who we are.
func (d *Daemon) hubConnect(h config.HubConfig) (hubAPI, hub.Identity, bool) {
	api, id, err := d.hub.newClient(h.URL, d.hubDir())
	w := d.hub
	w.mu.Lock()
	w.joined = err == nil
	if err == nil {
		w.device, w.org, w.deviceName = id.DeviceID, id.Org, id.DeviceName
	}
	w.mu.Unlock()
	if err != nil {
		if errors.Is(err, hub.ErrNotJoined) {
			d.hubSetErr("identity", errors.New("not joined: run `lola hub join <url>` with the token on stdin"))
		} else {
			d.hubSetErr("identity", err)
		}
		return nil, hub.Identity{}, false
	}
	return api, id, true
}

// hubCycle is one whole pass.
func (d *Daemon) hubCycle(ctx context.Context) {
	snap, ok := d.hubConfig()
	if !ok {
		return
	}
	api, id, ok := d.hubConnect(snap.h)
	if !ok {
		return
	}
	// Outcomes first: they free leases the hub is waiting on.
	d.hubOutcomes(ctx, api)
	if !d.hubHeartbeat(ctx, api, snap) {
		return // the hub is unreachable or refuses us: claim nothing
	}
	d.hubFindings(ctx, api, snap)
	d.hubOffers(ctx, api, snap)
	d.hubPolicy(ctx, api, snap, id)
}

// hubOutcomeCycle is the nudged outcome-only pass.
func (d *Daemon) hubOutcomeCycle(ctx context.Context) {
	snap, ok := d.hubConfig()
	if !ok {
		return
	}
	api, _, ok := d.hubConnect(snap.h)
	if !ok {
		return
	}
	d.hubOutcomes(ctx, api)
}

// ---- 1. heartbeat ----------------------------------------------------------

// hubLive reports whether s still holds a lease worth renewing: an open claim
// whose agent has not ended. A dead agent's lease is left to expire, so the
// hub can offer the item again.
func hubLive(s session.Session) bool {
	if !s.HubOpen() {
		return false
	}
	s.EnsureAxes()
	return s.AgentState != state.AgentDead && s.AgentState != state.AgentExited
}

func (d *Daemon) hubHeartbeat(ctx context.Context, api hubAPI, snap hubSnapshot) bool {
	global, _ := d.slotCounts("")
	free := min(max(snap.globalCap-global, 0), hubproto.MaxFreeSlots)
	var live []string
	for _, s := range d.sessions.Snapshot() {
		if !hubLive(s) {
			continue
		}
		for _, w := range s.HubWorks() {
			if w.Open() && !slices.Contains(live, w.ItemID) && len(live) < hubproto.MaxLiveLeases {
				live = append(live, w.ItemID)
			}
		}
	}
	for _, id := range d.hubUnsent() {
		if !slices.Contains(live, id) && len(live) < hubproto.MaxLiveLeases {
			live = append(live, id)
		}
	}
	req := hubproto.HeartbeatRequest{FreeSlots: free, Agents: snap.agents, Repos: snap.repos, LiveLeases: live}
	if req.Agents == nil {
		req.Agents = []string{}
	}
	if req.Repos == nil {
		req.Repos = []string{}
	}
	if req.LiveLeases == nil {
		req.LiveLeases = []string{}
	}
	resp, err := api.Heartbeat(ctx, req)
	if err != nil {
		d.hubSetErr("heartbeat", err)
		return false
	}
	now := d.hub.now()
	d.hub.mu.Lock()
	d.hub.lastBeat = now
	d.hub.mu.Unlock()
	renewed := map[string]time.Time{}
	for _, l := range resp.Renewed {
		renewed[l.ItemID] = l.Expires
	}
	lost := map[string]bool{}
	for _, id := range resp.Lost {
		lost[id] = true
	}
	changed := false
	for _, s := range d.sessions.Snapshot() {
		touches := false
		for _, w := range s.HubWorks() {
			if lost[w.ItemID] || !renewed[w.ItemID].IsZero() {
				touches = true
			}
		}
		if !touches {
			continue
		}
		_, _ = d.sessions.Update(s.ID, func(cur *session.Session) bool {
			wrote := false
			for _, w := range cur.HubWorks() {
				item := w.ItemID
				switch {
				case lost[item] && !w.LeaseLost:
					// The hub took the item back. The session is NOT stopped:
					// the work may still be useful, and the hub never controls
					// a session. lola only stops renewing and reporting for it.
					w.LeaseLost = true
					d.logf("", "hub: lease on item %s lost; session %s keeps running, no outcome will be reported for it", item, cur.ID)
					wrote = true
				case !lost[item] && !renewed[item].IsZero():
					w.LeaseExpires = renewed[item]
					wrote = true
				}
			}
			changed = changed || wrote
			return wrote
		})
	}
	if changed {
		if err := d.sessions.Save(); err != nil {
			d.logf("", "hub: persist sessions: %v", err)
		}
	}
	return true
}

// ---- 2. findings -----------------------------------------------------------

func (d *Daemon) hubFindings(ctx context.Context, api hubAPI, snap hubSnapshot) {
	now := d.hub.now()
	w := d.hub
	w.mu.Lock()
	d.hubLoadLocked()
	var due []string
	for _, r := range snap.repos {
		if at, ok := w.st.FindingsAt[strings.ToLower(r)]; !ok || now.Sub(at) >= hubFindingsEvery {
			due = append(due, r)
		}
	}
	if len(due) > 0 {
		// Stamp before the scan: the limit is on gh traffic, so a scan that
		// fails part-way still waits its hour.
		if w.st.FindingsAt == nil {
			w.st.FindingsAt = map[string]time.Time{}
		}
		for _, r := range due {
			w.st.FindingsAt[strings.ToLower(r)] = now
		}
		for k, at := range w.st.FindingsAt { // forget repos no longer opted in
			if now.Sub(at) > 7*24*time.Hour {
				delete(w.st.FindingsAt, k)
			}
		}
		if err := d.hubSaveLocked(); err != nil {
			d.logf("", "hub: persist findings stamp: %v", err)
		}
	}
	w.mu.Unlock()
	if len(due) == 0 || w.collect == nil {
		return
	}
	res := w.collect(ctx, due)
	for _, err := range res.Errors {
		d.logf("", "hub: findings: %v", err)
	}
	if res.Dropped > 0 {
		d.logf("", "hub: findings: %d candidate(s) did not validate and were dropped", res.Dropped)
	}
	accepted, rejected := 0, 0
	for _, batch := range hub.Chunk(res.Items) {
		r, err := api.ReportFindings(ctx, batch)
		if err != nil {
			d.hubSetErr("report findings", err)
			return
		}
		accepted += r.Accepted
		rejected += len(r.Rejected)
	}
	w.mu.Lock()
	w.findings = &protocol.HubFindingsInfo{At: now, Repos: len(due), Reported: len(res.Items), Accepted: accepted, Rejected: rejected}
	w.mu.Unlock()
	if len(res.Items) > 0 {
		d.logf("", "hub: reported %d finding(s) from %d repo(s): %d accepted, %d rejected", len(res.Items), len(due), accepted, rejected)
	}
}

// ---- 3. offers → claim → spawn --------------------------------------------

func (d *Daemon) hubOffers(ctx context.Context, api hubAPI, snap hubSnapshot) {
	items, dropped, err := api.Offers(ctx)
	if err != nil {
		d.hubSetErr("offers", err)
		return
	}
	if dropped > 0 {
		d.logf("", "hub: %d offered item(s) did not validate and were ignored", dropped)
	}
	groups, single := hubGroupOffers(items, snap.h.GroupsChores())
	for _, g := range groups {
		repo := g[0].DepAdvisory.Repo
		if d.hubRepoBusy(repo) {
			continue // the running session's pull request comes first
		}
		if _, err := d.hubClaimGroup(ctx, api, g, snap, hubClaimReq{}); err != nil && !errors.Is(err, errHubSpawn) {
			d.logf("", "hub: not claiming %d advisories of %s: %s", len(g), repo, err)
		}
	}
	for _, it := range single {
		if ok, why := d.hubTryClaim(ctx, api, it, snap); !ok && why != "" {
			d.logf("", "hub: not claiming %s (%s): %s", it.ID, it.Kind, why)
		}
	}
}

// hubBranch is the branch a hub session works on: a new lola-owned branch for
// an advisory, the bot's own branch for a red bump PR.
func hubBranch(prefix string, it hubproto.Item) string {
	if it.Kind == hubproto.KindRedBumpPR {
		return it.RedBumpPR.Branch
	}
	return prefix + "hub-" + hub.Slug(it)
}

// hubTryClaim runs one offer through every gate and, only then, claims and
// spawns it. ok=false with a reason means "skipped, nothing claimed"; a
// failed spawn (claimed, then released) is logged where it happens and
// reports no reason.
func (d *Daemon) hubTryClaim(ctx context.Context, api hubAPI, it hubproto.Item, snap hubSnapshot) (bool, string) {
	_, err := d.hubClaimItem(ctx, api, it, snap, hubClaimReq{})
	switch {
	case err == nil:
		return true, ""
	case errors.Is(err, errHubSpawn):
		return false, ""
	}
	return false, err.Error()
}

// hubClaimReq says who asks for a claim.
type hubClaimReq struct {
	// human: a person pressed Claim (cmd=hubClaim). The click is the opt-in
	// (no hub_chores needed), it is intent (no concurrency cap, no daily
	// count, no cooldown — like every human open, AGENTS.md), and the session
	// still counts once it exists. require_sandbox, the kind filter,
	// dispatchHold, the health gate and the GitHub re-read all still apply.
	human bool
	// project names the [[project]] when several work the item's repo.
	project string
}

// errHubSpawn marks a claim whose spawn failed after the lease was taken (the
// lease is released through the outbox).
var errHubSpawn = errors.New("spawn failed")

// hubClaimItem is THE claim path, shared by the loop and a person's click:
// gates → GitHub re-read → reserve → count → Claim → spawn → Session.HubItem,
// releasing the lease when the spawn fails. Every refusal before the claim
// mutates nothing. It returns the new session's id.
func (d *Daemon) hubClaimItem(ctx context.Context, api hubAPI, it hubproto.Item, snap hubSnapshot, req hubClaimReq) (string, error) {
	now := d.hub.now()
	if !hubproto.ValidItemID(it.ID) || hubproto.Validate(it) != nil {
		return "", errors.New(hub.ReasonInvalidItem)
	}
	if !d.hubBeginClaim(it.ID) {
		return "", errors.New("a claim for it is already in flight")
	}
	defer d.hubEndClaim(it.ID)
	// Already working this item (a restart, a hub that re-offers)?
	for _, s := range d.sessions.Snapshot() {
		if s.HubWork(it.ID) != nil {
			return "", errors.New("already working it")
		}
	}
	if !req.human && d.hubCoolingDown(it.ID, now) {
		return "", errors.New("ended here recently")
	}

	d.mu.Lock()
	var pp *config.Project
	var pickErr error
	if req.human {
		pp, pickErr = hubPickProject(d.cfg, it, req.project, snap.h)
	} else {
		pp = hub.ProjectFor(d.cfg, it)
	}
	var p config.Project
	if pp != nil {
		p = *pp
		p.PostCreate = slices.Clone(p.PostCreate)
		p.Symlinks = slices.Clone(p.Symlinks)
		p.Env = maps.Clone(p.Env)
	}
	projectCap := 0
	agentBin := ""
	prefix := ""
	if pp != nil {
		projectCap = d.cfg.EffectiveCap(pp)
		agentBin = sandboxedBin(d.cfg, p.Name, agent.Parse(d.cfg.AgentForProject(p.Name)).Binary())
		prefix = d.cfg.BranchPrefixForProject(p.Name)
	}
	nat := d.native
	health := d.runtimeHealth
	d.mu.Unlock()
	if pickErr != nil {
		return "", pickErr
	}

	var accProject *config.Project
	if pp != nil {
		accProject = &p
	}
	if req.human {
		if ok, why := hub.AcceptIntent(it, accProject, snap.h); !ok {
			return "", errors.New(why)
		}
	} else if ok, why := hub.Accept(it, accProject, snap.h, d.hubClaimsToday(now)); !ok {
		return "", errors.New(why)
	}
	branch := hubBranch(prefix, it)
	id := runtime.ManualSessionID(p.Name, branch)
	if _, exists := d.sessions.Get(id); exists {
		return "", errors.New("a session already works " + branch)
	}
	// The same gates as a Linear dispatch, in the same order, all BEFORE the
	// claim: hold, health, slots.
	if msg := d.dispatchHold(ctx, p.Name, now); msg != "" {
		return "", errors.New("dispatch held: " + msg)
	}
	if nat == nil {
		return "", errors.New("native runtime unavailable")
	}
	if err := health(agentBin); err != nil {
		return "", errors.New("runtime unavailable: " + err.Error())
	}
	// The offer is the hub's claim; GitHub's answer is the fact. Everything
	// from here on — branch, briefing, start head — comes from the verified
	// item, and any doubt refuses the offer (nothing claimed).
	if d.hub.verify == nil {
		return "", errors.New("cannot verify the offer on GitHub")
	}
	v, err := d.hub.verify(ctx, it)
	if err != nil {
		// ErrNotConfirmed already says "GitHub does not confirm"; keep only the why.
		why := strings.TrimPrefix(err.Error(), hub.ErrNotConfirmed.Error()+": ")
		return "", errors.New("GitHub does not confirm it: " + why)
	}
	if v.Item.ID != it.ID || v.Item.Kind != it.Kind || hubproto.Validate(v.Item) != nil ||
		(it.Kind == hubproto.KindRedBumpPR && v.Head == "") {
		return "", errors.New("GitHub does not confirm it")
	}
	it = v.Item
	if hubBranch(prefix, it) != branch {
		return "", errors.New("GitHub does not confirm it: the branch differs")
	}
	brief, err := hub.Briefing(it)
	if err != nil {
		return "", errors.New(hub.ReasonInvalidItem)
	}
	// A person's claim checks no cap (it is intent), but it still RESERVES
	// so a loop tick spawning beside it counts it.
	pCap, gCap := projectCap, snap.globalCap
	if req.human {
		pCap, gCap = noProjectCap, math.MaxInt
	}
	release, fits := d.reserveSlots(p.Name, 1, pCap, gCap)
	if !fits {
		return "", errors.New("capped")
	}
	defer release()

	// Count first, then claim: a crash between the two over-counts (safe),
	// never under-counts. A person's claim does not spend the daily cap.
	if !req.human {
		if err := d.hubCountClaim(now, 1); err != nil {
			return "", errors.New("persist claim count: " + err.Error())
		}
	}
	lease, err := api.Claim(ctx, it.ID)
	if err != nil {
		if !req.human {
			if rerr := d.hubCountClaim(now, -1); rerr != nil {
				d.logf("", "hub: refund claim count: %v", rerr)
			}
		}
		return "", errors.New("claim refused: " + err.Error())
	}
	d.hubForgetClaims(it.ID, now)

	cctx, cancel := context.WithTimeout(ctx, nativeSpawnTimeout)
	var sess session.Session
	if it.Kind == hubproto.KindRedBumpPR {
		sess, err = nat.OpenPRAgent(cctx, p, id, branch, brief, "")
	} else {
		sess, err = nat.OpenManualAgent(cctx, p, id, branch, "", brief, "")
	}
	cancel()
	if err != nil {
		d.logf("", "hub: spawn for item %s failed, releasing it: %v", it.ID, err)
		d.hubEnqueue(hubOutcome{ItemID: it.ID, ClaimedAt: now, Action: hubActRelease, Since: now})
		d.hubOutcomes(ctx, api)
		return "", fmt.Errorf("%w: %v", errHubSpawn, err)
	}
	sess.Repo = p.Repo
	sess.Title = "hub: " + hubTitle(it)
	sess.HubItem = session.HubWork{ItemID: it.ID, Kind: string(it.Kind), ClaimedAt: now, LeaseExpires: lease.Expires, StartHead: v.Head, Label: hubLabel(it)}
	d.sessions.Upsert(sess)
	d.recordSessionEvent("", sess)
	if err := d.sessions.Save(); err != nil {
		d.logf("", "hub: persist sessions after spawning %s: %v", id, err)
	}
	who := "the hub loop"
	if req.human {
		who = "a person"
	}
	d.logf("", "hub: claimed item %s (%s) for %s, session %s (%s)", it.ID, it.Kind, p.Name, id, who)
	return id, nil
}

// hubBeginClaim marks id's claim in flight; false when one already is.
func (d *Daemon) hubBeginClaim(id string) bool {
	w := d.hub
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.claiming[id] {
		return false
	}
	if w.claiming == nil {
		w.claiming = map[string]bool{}
	}
	w.claiming[id] = true
	return true
}

func (d *Daemon) hubEndClaim(id string) {
	d.hub.mu.Lock()
	delete(d.hub.claiming, id)
	d.hub.mu.Unlock()
}

// hubPickProject resolves a HUMAN claim's project: every non-stack project
// working the item's repo is a candidate (opted in or not); without a name,
// the one candidate that accepts it is picked and several need a name. The
// caller holds d.mu.
func hubPickProject(cfg *config.Config, it hubproto.Item, name string, h config.HubConfig) (*config.Project, error) {
	cands := hub.ProjectsFor(cfg, it)
	if name != "" {
		for _, p := range cands {
			if p.Name == name {
				return p, nil
			}
		}
		return nil, fmt.Errorf("project %q does not work %s", name, it.Repo())
	}
	if len(cands) == 0 {
		return nil, fmt.Errorf("no local project works %s", it.Repo())
	}
	// Count only the projects that would accept it, the way the board's
	// hubLocal does: a row offered as "claimable (X)" must claim into X when
	// no project is named, not refuse because an unsandboxed sibling exists.
	var ok []*config.Project
	first := ""
	for _, p := range cands {
		if yes, why := hub.AcceptIntent(it, p, h); yes {
			ok = append(ok, p)
		} else if first == "" {
			first = why
		}
	}
	switch len(ok) {
	case 0:
		return nil, errors.New(first)
	case 1:
		return ok[0], nil
	}
	cands = ok
	names := make([]string, len(cands))
	for i, p := range cands {
		names[i] = p.Name
	}
	return nil, fmt.Errorf("several projects work %s (%s): name one", it.Repo(), strings.Join(names, ", "))
}

// hubLabel is lola's own short name for an item, from validated fields only.
func hubLabel(it hubproto.Item) string {
	switch {
	case it.Kind == hubproto.KindDepAdvisory && it.DepAdvisory != nil:
		return it.DepAdvisory.Advisory + " · " + it.DepAdvisory.Package
	case it.Kind == hubproto.KindRedBumpPR && it.RedBumpPR != nil:
		return fmt.Sprintf("PR #%d · %s", it.RedBumpPR.PR, it.RedBumpPR.Package)
	}
	return string(it.Kind)
}

// hubTitle is a list-row title built from validated fields only.
func hubTitle(it hubproto.Item) string {
	switch it.Kind {
	case hubproto.KindDepAdvisory:
		return it.DepAdvisory.Advisory + " in " + it.DepAdvisory.Package
	case hubproto.KindRedBumpPR:
		return fmt.Sprintf("fix #%d (%s)", it.RedBumpPR.PR, it.RedBumpPR.Package)
	}
	return string(it.Kind)
}

// ---- 4. outcomes -----------------------------------------------------------

// hubPRURL is the PR's URL when it is a canonical GitHub PR URL, else one
// rebuilt from the repo slug and number (both validated), else "".
func hubPRURL(s session.Session) string {
	if s.PR == nil || s.PR.Number <= 0 {
		return ""
	}
	if hubproto.ValidatePRURL(s.PR.URL) == nil {
		return s.PR.URL
	}
	u := fmt.Sprintf("https://github.com/%s/pull/%d", s.Repo, s.PR.Number)
	if hubproto.ValidatePRURL(u) == nil {
		return u
	}
	return ""
}

// hubGreen is the delivery a red bump PR must reach to count as fixed.
func hubGreen(d state.DeliveryState) bool {
	switch d {
	case state.DeliveryReviewPending, state.DeliveryApproved, state.DeliveryMerged:
		return true
	}
	return false
}

// hubOutcomeFor decides what to report for s, or ok=false for "nothing yet".
// ended is set when the session is being torn down: then something is always
// reported.
//
//   - a PR that closed unmerged fails the item (pr_closed);
//   - dep_advisory completes once its PR exists;
//   - red_bump_pr completes once the PR head moved past the one first seen and
//     the delivery is green (or merged at all): the bot's PR existed before
//     the agent did anything, so its mere presence proves nothing;
//   - at teardown, a red bump PR the agent pushed to that never went green:
//     ci_failed; one that is green although the agent never moved its head:
//     not_reproducible (the finding no longer holds);
//   - otherwise at teardown: agent_gave_up when the agent had ended, refused
//     when a person stopped a live one (no attempt is counted). An unmoved,
//     still-red bump PR lands here too: closing it as not_reproducible would
//     bury a real failure for good.
func hubOutcomeFor(s session.Session, w session.HubWork, ended bool) (hubOutcome, bool) {
	o := hubOutcome{ItemID: w.ItemID, ClaimedAt: w.ClaimedAt}
	url := hubPRURL(s)
	s.EnsureAxes()
	if url != "" {
		switch {
		case s.Delivery == state.DeliveryClosed:
			o.Action, o.Reason = hubActFail, string(hubproto.FailPRClosed)
			return o, true
		case w.Kind == string(hubproto.KindDepAdvisory),
			s.Delivery == state.DeliveryMerged,
			w.Kind == string(hubproto.KindRedBumpPR) && w.StartHead != "" &&
				s.PR.HeadSHA != "" && s.PR.HeadSHA != w.StartHead && hubGreen(s.Delivery):
			o.Action, o.PRURL, o.Delivery = hubActComplete, url, string(hubproto.DeliveryNone)
			if hubproto.Delivery(s.Delivery).Valid() {
				o.Delivery = string(s.Delivery)
			}
			return o, true
		}
	}
	if !ended {
		return o, false
	}
	o.Action = hubActFail
	moved := url != "" && w.StartHead != "" && s.PR.HeadSHA != "" && s.PR.HeadSHA != w.StartHead
	switch {
	case moved:
		o.Reason = string(hubproto.FailCI)
	case url != "" && hubGreen(s.Delivery):
		o.Reason = string(hubproto.FailObsolete)
	case s.AgentState == state.AgentDead || s.AgentState == state.AgentExited:
		o.Reason = string(hubproto.FailAgent)
	default:
		o.Reason = string(hubproto.FailRefused)
	}
	return o, true
}

// hubOutcomes stamps each live hub session's outcome once it is known, queues
// it, and drains the outbox.
func (d *Daemon) hubOutcomes(ctx context.Context, api hubAPI) {
	now := d.hub.now()
	changed := false
	for _, s := range d.sessions.Snapshot() {
		if !s.HubOpen() {
			continue
		}
		var queued []hubOutcome
		_, _ = d.sessions.Update(s.ID, func(cur *session.Session) bool {
			wrote := false
			for _, w := range cur.HubWorks() {
				if !w.Open() {
					continue
				}
				if w.Kind == string(hubproto.KindRedBumpPR) && w.StartHead == "" &&
					cur.PR != nil && cur.PR.HeadSHA != "" {
					w.StartHead = cur.PR.HeadSHA
					wrote = true
				}
				if o, ok := hubOutcomeFor(*cur, *w, false); ok {
					o.Since = now
					queued = append(queued, o)
				}
			}
			changed = changed || wrote
			return wrote
		})
		// Persist each outcome FIRST, then stamp its one-shot guard: a crash
		// in between re-derives the same outcome, which the outbox dedups by
		// claim. The other order could persist a guard with no outcome.
		for _, o := range queued {
			d.hubEnqueue(o)
			_, _ = d.sessions.Update(s.ID, func(cur *session.Session) bool {
				w := cur.HubWork(o.ItemID)
				if w == nil || !w.Open() || !w.ClaimedAt.Equal(o.ClaimedAt) {
					return false
				}
				w.Reported = o.Action
				if o.Reason != "" {
					w.Reported += ":" + o.Reason
				}
				changed = true
				return true
			})
		}
	}
	if changed {
		if err := d.sessions.Save(); err != nil {
			d.logf("", "hub: persist sessions: %v", err)
		}
	}
	d.hubFlush(ctx, api)
}

// hubSessionEnded is the teardown hook (dropSession): a hub session leaving
// the store reports its outcome once. Local work only — the request goes out
// on the hub loop, nudged here — so teardown never waits on the network.
func (d *Daemon) hubSessionEnded(s session.Session) {
	if d.hub == nil || !s.HubOpen() {
		return
	}
	for _, w := range s.HubWorks() {
		if !w.Open() {
			continue
		}
		o, _ := hubOutcomeFor(s, *w, true)
		o.Since = d.hub.now()
		d.hubEnqueue(o)
		if o.Action != hubActComplete {
			d.hubCoolDown(o.ItemID, o.Since)
		}
		d.logf("", "hub: session %s ended; reporting %s %s for item %s", s.ID, o.Action, o.Reason, o.ItemID)
	}
	d.hubNudgeOutcomes()
}

// hubTerminal reports whether a hub refusal is final for an outcome: the hub
// understood the request and will never accept it (the lease is gone, the item
// is unknown, the request is malformed). Anything else — the network, 5xx,
// rate limiting — is retried.
func hubTerminal(err error) bool {
	var ae *hub.APIError
	if !errors.As(err, &ae) {
		return errors.Is(err, hub.ErrInvalidResponse)
	}
	// 408/429 are retried whatever the code: a proxy in front of the hub
	// sends them with no hubproto code at all.
	if ae.Status == 408 || ae.Status == 429 {
		return false
	}
	return ae.Status >= 400 && ae.Status < 500 && ae.Code != hubproto.CodeRateLimited && ae.Code != hubproto.CodeSkew
}

// hubFlush sends every queued outcome once.
func (d *Daemon) hubFlush(ctx context.Context, api hubAPI) {
	w := d.hub
	w.mu.Lock()
	d.hubLoadLocked()
	queue := slices.Clone(w.st.Outbox)
	w.mu.Unlock()
	if len(queue) == 0 {
		return
	}
	now := w.now()
	done := map[string]bool{}
	tried := map[string]bool{}
	for _, o := range queue {
		var err error
		switch o.Action {
		case hubActComplete:
			err = api.Complete(ctx, o.ItemID, hubproto.CompleteRequest{PRURL: o.PRURL, Delivery: hubproto.Delivery(o.Delivery)})
		case hubActFail:
			err = api.Fail(ctx, o.ItemID, hubproto.FailReason(o.Reason))
		case hubActRelease:
			err = api.Release(ctx, o.ItemID)
		default:
			err = &hub.APIError{Status: 400, Code: hubproto.CodeBadRequest, Message: "unknown outbox action"}
		}
		switch {
		case err == nil:
			done[o.key()] = true
			d.logf("", "hub: reported %s %s for item %s", o.Action, o.Reason, o.ItemID)
		case hubTerminal(err):
			done[o.key()] = true
			d.logf("", "hub: %s for item %s refused, dropping it: %v", o.Action, o.ItemID, err)
		case now.Sub(o.Since) > hubOutboxMaxAge:
			done[o.key()] = true
			d.logf("", "hub: %s for item %s unacknowledged for %s, dropping it: %v", o.Action, o.ItemID, hubOutboxMaxAge, err)
		default:
			tried[o.key()] = true
			d.hubSetErr(o.Action, err)
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	kept := w.st.Outbox[:0:0]
	for _, o := range w.st.Outbox {
		if done[o.key()] {
			w.st.Settled = append(w.st.Settled, o.key())
			continue
		}
		if tried[o.key()] {
			o.Tries++
		}
		kept = append(kept, o)
	}
	w.st.Outbox = kept
	if n := len(w.st.Settled); n > hubMaxOutbox {
		w.st.Settled = slices.Clone(w.st.Settled[n-hubMaxOutbox:])
	}
	if err := d.hubSaveLocked(); err != nil {
		d.logf("", "hub: persist outbox: %v", err)
	}
}

// ---- 5. policy (fetched here, applied by the reload it triggers) -----------

func (d *Daemon) hubPolicy(ctx context.Context, api hubAPI, snap hubSnapshot, id hub.Identity) {
	if len(snap.h.TrustedPolicyKeys) == 0 {
		return // nothing could verify a policy: do not even fetch one
	}
	now := d.hub.now()
	w := d.hub
	w.mu.Lock()
	d.hubReadPolicyLocked(snap.h)
	d.hubLoadLocked()
	due := now.Sub(w.st.PolicyAt) >= hubPolicyEvery
	if due {
		w.st.PolicyAt = now
		if err := d.hubSaveLocked(); err != nil {
			d.logf("", "hub: persist policy stamp: %v", err)
		}
	}
	w.mu.Unlock()
	if !due {
		return
	}
	sp, err := api.FetchPolicy(ctx, snap.h, id.Org, d.hubDir(), now)
	switch {
	case err == nil:
		w.mu.Lock()
		w.policyV = sp.Policy.Version
		w.policy = &sp
		w.mu.Unlock()
		d.hubPolicyReload(ctx, sp.Policy.Version)
	case hub.IsCode(err, hubproto.CodeNoPolicy):
		// The org has not published one; nothing to show.
	default:
		d.hubSetErr("policy", err)
	}
}

// hubPolicyStale drops status's cached policy after a reload: the reload may
// have changed [hub].trusted_policy_keys, so the stored bundle is re-read and
// re-verified against the current keys on the next status, never shown as
// verified from a cache the keys no longer back.
func (d *Daemon) hubPolicyStale() {
	w := d.hub
	if w == nil {
		return
	}
	w.mu.Lock()
	w.policy, w.policyV, w.policyRead = nil, 0, false
	w.mu.Unlock()
}

// hubReadPolicyLocked reads the stored, re-verified policy once, so status
// shows it right after a restart, not nothing until the next fetch is due.
// The caller holds d.hub.mu.
func (d *Daemon) hubReadPolicyLocked(h config.HubConfig) {
	w := d.hub
	if w.policyRead {
		return
	}
	w.policyRead = true
	if trusted, err := hub.TrustedKeys(h); err == nil {
		if sp, err := hub.LoadPolicy(d.hubDir(), trusted); err == nil {
			w.policyV = sp.Policy.Version
			w.policy = &sp
		}
	}
}

// ---- status ----------------------------------------------------------------

// hubStatusData answers cmd=hubStatus from memory, the store and local files;
// it never contacts the hub.
func (d *Daemon) hubStatusData() protocol.HubStatusData {
	d.mu.Lock()
	h := d.cfg.Hub
	h.TrustedPolicyKeys = maps.Clone(h.TrustedPolicyKeys)
	d.mu.Unlock()
	out := protocol.HubStatusData{Enabled: h.Enabled, URL: h.URL, MaxClaimsPerDay: h.ClaimsPerDay()}
	if d.hub != nil {
		now := d.hub.now()
		out.ClaimsToday = d.hubClaimsToday(now)
		w := d.hub
		w.mu.Lock()
		d.hubReadPolicyLocked(h)
		out.Joined, out.Device, out.Org, out.DeviceName = w.joined, w.device, w.org, w.deviceName
		out.LastHeartbeat, out.LastError, out.LastErrorAt = w.lastBeat, w.lastErr, w.lastErrT
		out.PendingOutcomes = len(w.st.Outbox)
		out.PolicyVersion = w.policyV
		if w.findings != nil {
			f := *w.findings
			out.LastFindings = &f
		}
		if sp := w.policy; sp != nil {
			out.Policy = &protocol.HubPolicyInfo{
				Version: sp.Policy.Version, KeyID: sp.Policy.KeyID, IssuedAt: sp.Policy.IssuedAt,
				VerifiedAt: sp.VerifiedAt, Keys: sp.Policy.Settings.Keys(), Summary: hub.PolicySummary(sp.Policy),
			}
			if out.Policy.Keys == nil {
				out.Policy.Keys = []string{}
			}
		}
		w.mu.Unlock()
		if out.Policy != nil {
			d.mu.Lock()
			d.policyInfoLocked(out.Policy)
			d.mu.Unlock()
		}
	}
	if !out.Joined {
		// Before the loop's first connect (or with the hub off) the identity
		// on disk still says who this machine is.
		if id, err := hub.LoadIdentity(d.hubDir()); err == nil && strings.TrimSuffix(id.URL, "/") == strings.TrimSuffix(h.URL, "/") {
			out.Joined, out.Device, out.Org, out.DeviceName = true, id.DeviceID, id.Org, id.DeviceName
		}
	}
	for _, s := range d.sessions.Snapshot() {
		for _, w := range s.HubWorks() {
			summary := w.Label
			if summary == "" {
				summary = hubSessionLabel(s)
			}
			out.Sessions = append(out.Sessions, protocol.HubSessionInfo{
				Session: s.ID, Project: s.Project, ItemID: w.ItemID, Kind: w.Kind,
				ClaimedAt: w.ClaimedAt, LeaseExpires: w.LeaseExpires,
				LeaseLost: w.LeaseLost, Released: w.Released, Reported: w.Reported,
				Summary: summary,
			})
		}
	}
	return out
}

// hubSessionLabel is a hub session's item label; records from before Label
// existed fall back to their title.
func hubSessionLabel(s session.Session) string {
	if n := len(s.HubBatch); n > 0 {
		return fmt.Sprintf("%d advisories", n+1)
	}
	if s.HubItem.Label != "" {
		return s.HubItem.Label
	}
	return strings.TrimPrefix(s.Title, "hub: ")
}

// hubSessionHub is SessionInfo.hub for s (nil for a non-hub session).
func hubSessionHub(s session.Session) *protocol.HubSessionHub {
	if s.HubItem.ItemID == "" {
		return nil
	}
	return &protocol.HubSessionHub{
		ItemID: s.HubItem.ItemID, Kind: s.HubItem.Kind, Label: hubSessionLabel(s),
		LeaseLost: s.HubItem.LeaseLost, Released: s.HubItem.Released,
	}
}
