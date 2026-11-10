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
//	  → policy (verified, stored, DISPLAY ONLY), at most once an hour
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
//   - Outcomes are one-shot. The guard (HubWork.Reported) is stamped before
//     the request and the request itself rides a persisted outbox, so a crash
//     or an unreachable hub neither repeats nor loses one.
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
	// hubPolicyEvery rate-limits the policy fetch.
	hubPolicyEvery = time.Hour
	// hubOutboxMaxAge drops an outcome the hub has not acknowledged for this
	// long: the lease has long expired and the hub reopened the item anyway.
	hubOutboxMaxAge = 24 * time.Hour
	// hubStateFile is the loop's persisted state under ~/.lola/state. Not
	// hub.json (the identity, owned by internal/hub).
	hubStateFile = "hub-daemon.json"
	// hubMaxOutbox bounds the outbox; the oldest entries go first.
	hubMaxOutbox = 256
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
	ItemID   string    `json:"item_id"`
	Action   string    `json:"action"`
	Reason   string    `json:"reason,omitempty"`
	PRURL    string    `json:"pr_url,omitempty"`
	Delivery string    `json:"delivery,omitempty"`
	Since    time.Time `json:"since"`
	Tries    int       `json:"tries,omitempty"`
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
	// Settled are the item ids whose outcome left the outbox (sent, refused
	// for good or expired), newest last: a second end signal for the same item
	// must not report it again.
	Settled []string `json:"settled,omitempty"`
}

// hubWire is the loop's state. Its own lock; never held while taking d.mu or
// calling the hub.
type hubWire struct {
	// Seams (tests replace them).
	newClient func(url, dir string) (hubAPI, hub.Identity, error)
	collect   func(ctx context.Context, repos []string) hub.Result
	now       func() time.Time

	// full wakes the loop for a whole cycle (reload, join); outcomes wakes it
	// for the outbox + outcome pass only (a delivery change, a teardown).
	full     chan struct{}
	outcomes chan struct{}

	mu       sync.Mutex
	loaded   bool
	st       hubPersisted
	joined   bool
	device   string
	org      string
	lastBeat time.Time
	lastErr  string
	lastErrT time.Time
	policyV  int
}

func newHubWire() *hubWire {
	col := &hub.Collector{Gh: hub.ExecGh("gh", hub.DefaultGhMaxOutput)}
	return &hubWire{
		newClient: newRealHubClient,
		collect:   col.Collect,
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
	for _, q := range w.st.Outbox {
		if q.ItemID == o.ItemID {
			return // one outcome per item
		}
	}
	if slices.Contains(w.st.Settled, o.ItemID) {
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
		w.device, w.org = id.DeviceID, id.Org
	}
	w.mu.Unlock()
	if err != nil {
		if errors.Is(err, hub.ErrNotJoined) {
			d.hubSetErr("identity", errors.New("not joined: run `lola hub join <url> <token>`"))
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
	if !s.HubItem.Open() {
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
		if hubLive(s) && !slices.Contains(live, s.HubItem.ItemID) && len(live) < hubproto.MaxLiveLeases {
			live = append(live, s.HubItem.ItemID)
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
		item := s.HubItem.ItemID
		if item == "" || (!lost[item] && renewed[item].IsZero()) {
			continue
		}
		_, _ = d.sessions.Update(s.ID, func(cur *session.Session) bool {
			if cur.HubItem.ItemID != item {
				return false
			}
			if lost[item] {
				if cur.HubItem.LeaseLost {
					return false
				}
				// The hub took the item back. The session is NOT stopped: the
				// work may still be useful, and the hub never controls a
				// session. lola only stops renewing and reporting for it.
				cur.HubItem.LeaseLost = true
				d.logf("", "hub: lease on item %s lost; session %s keeps running, no outcome will be reported", item, cur.ID)
			} else {
				cur.HubItem.LeaseExpires = renewed[item]
			}
			changed = true
			return true
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
	for _, it := range items {
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
// spawns it. ok=false with a reason means "skipped, nothing claimed".
func (d *Daemon) hubTryClaim(ctx context.Context, api hubAPI, it hubproto.Item, snap hubSnapshot) (bool, string) {
	now := d.hub.now()
	if !hubproto.ValidItemID(it.ID) || hubproto.Validate(it) != nil {
		return false, hub.ReasonInvalidItem
	}
	// Already working this item (a restart, a hub that re-offers)?
	for _, s := range d.sessions.Snapshot() {
		if s.HubItem.ItemID == it.ID {
			return false, "already working it"
		}
	}

	d.mu.Lock()
	var p config.Project
	pp := hub.ProjectFor(d.cfg, it)
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

	var accProject *config.Project
	if pp != nil {
		accProject = &p
	}
	if ok, why := hub.Accept(it, accProject, snap.h, d.hubClaimsToday(now)); !ok {
		return false, why
	}
	branch := hubBranch(prefix, it)
	id := runtime.ManualSessionID(p.Name, branch)
	if _, exists := d.sessions.Get(id); exists {
		return false, "a session already works " + branch
	}
	// The same gates as a Linear dispatch, in the same order, all BEFORE the
	// claim: hold, health, slots.
	if msg := d.dispatchHold(ctx, p.Name, now); msg != "" {
		return false, "dispatch held: " + msg
	}
	if nat == nil {
		return false, "native runtime unavailable"
	}
	if err := health(agentBin); err != nil {
		return false, "runtime unavailable: " + err.Error()
	}
	brief, err := hub.Briefing(it)
	if err != nil {
		return false, hub.ReasonInvalidItem
	}
	release, fits := d.reserveSlots(p.Name, 1, projectCap, snap.globalCap)
	if !fits {
		return false, "capped"
	}
	defer release()

	// Count first, then claim: a crash between the two over-counts (safe),
	// never under-counts.
	if err := d.hubCountClaim(now, 1); err != nil {
		return false, "persist claim count: " + err.Error()
	}
	lease, err := api.Claim(ctx, it.ID)
	if err != nil {
		if rerr := d.hubCountClaim(now, -1); rerr != nil {
			d.logf("", "hub: refund claim count: %v", rerr)
		}
		return false, "claim refused: " + err.Error()
	}

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
		d.hubEnqueue(hubOutcome{ItemID: it.ID, Action: hubActRelease, Since: now})
		d.hubOutcomes(ctx, api)
		return false, ""
	}
	sess.Repo = p.Repo
	sess.Title = "hub: " + hubTitle(it)
	sess.HubItem = session.HubWork{ItemID: it.ID, Kind: string(it.Kind), ClaimedAt: now, LeaseExpires: lease.Expires}
	d.sessions.Upsert(sess)
	d.recordSessionEvent("", sess)
	if err := d.sessions.Save(); err != nil {
		d.logf("", "hub: persist sessions after spawning %s: %v", id, err)
	}
	d.logf("", "hub: claimed item %s (%s) for %s, session %s", it.ID, it.Kind, p.Name, id)
	return true, ""
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
//   - at teardown with no PR: agent_gave_up when the agent had ended,
//     refused when a person stopped a live one (no attempt is counted);
//     a red bump PR that never went green: ci_failed, or not_reproducible
//     when the agent never moved its head.
func hubOutcomeFor(s session.Session, ended bool) (hubOutcome, bool) {
	o := hubOutcome{ItemID: s.HubItem.ItemID}
	url := hubPRURL(s)
	s.EnsureAxes()
	if url != "" {
		switch {
		case s.Delivery == state.DeliveryClosed:
			o.Action, o.Reason = hubActFail, string(hubproto.FailPRClosed)
			return o, true
		case s.HubItem.Kind == string(hubproto.KindDepAdvisory),
			s.Delivery == state.DeliveryMerged,
			s.HubItem.Kind == string(hubproto.KindRedBumpPR) && s.HubItem.StartHead != "" &&
				s.PR.HeadSHA != "" && s.PR.HeadSHA != s.HubItem.StartHead && hubGreen(s.Delivery):
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
	switch {
	case url != "" && (s.HubItem.StartHead == "" || s.PR.HeadSHA == s.HubItem.StartHead):
		o.Reason = string(hubproto.FailObsolete)
	case url != "":
		o.Reason = string(hubproto.FailCI)
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
		if !s.HubItem.Open() {
			continue
		}
		var queued *hubOutcome
		_, _ = d.sessions.Update(s.ID, func(cur *session.Session) bool {
			if !cur.HubItem.Open() {
				return false
			}
			wrote := false
			if cur.HubItem.Kind == string(hubproto.KindRedBumpPR) && cur.HubItem.StartHead == "" &&
				cur.PR != nil && cur.PR.HeadSHA != "" {
				cur.HubItem.StartHead = cur.PR.HeadSHA
				wrote = true
			}
			o, ok := hubOutcomeFor(*cur, false)
			if ok {
				// The one-shot guard, stamped before anything is sent.
				cur.HubItem.Reported = o.Action
				if o.Reason != "" {
					cur.HubItem.Reported += ":" + o.Reason
				}
				o.Since = now
				queued = &o
				wrote = true
			}
			changed = changed || wrote
			return wrote
		})
		if queued != nil {
			d.hubEnqueue(*queued)
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
	if d.hub == nil || !s.HubItem.Open() {
		return
	}
	o, _ := hubOutcomeFor(s, true)
	o.Since = d.hub.now()
	d.hubEnqueue(o)
	d.logf("", "hub: session %s ended; reporting %s %s for item %s", s.ID, o.Action, o.Reason, o.ItemID)
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
			done[o.ItemID] = true
			d.logf("", "hub: reported %s %s for item %s", o.Action, o.Reason, o.ItemID)
		case hubTerminal(err):
			done[o.ItemID] = true
			d.logf("", "hub: %s for item %s refused, dropping it: %v", o.Action, o.ItemID, err)
		case now.Sub(o.Since) > hubOutboxMaxAge:
			done[o.ItemID] = true
			d.logf("", "hub: %s for item %s unacknowledged for %s, dropping it: %v", o.Action, o.ItemID, hubOutboxMaxAge, err)
		default:
			tried[o.ItemID] = true
			d.hubSetErr(o.Action, err)
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	kept := w.st.Outbox[:0:0]
	for _, o := range w.st.Outbox {
		if done[o.ItemID] {
			w.st.Settled = append(w.st.Settled, o.ItemID)
			continue
		}
		if tried[o.ItemID] {
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

// ---- 5. policy (display only) ---------------------------------------------

func (d *Daemon) hubPolicy(ctx context.Context, api hubAPI, snap hubSnapshot, id hub.Identity) {
	if len(snap.h.TrustedPolicyKeys) == 0 {
		return // nothing could verify a policy: do not even fetch one
	}
	now := d.hub.now()
	w := d.hub
	w.mu.Lock()
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
		w.mu.Unlock()
	case hub.IsCode(err, hubproto.CodeNoPolicy):
		// The org has not published one; nothing to show.
	default:
		d.hubSetErr("policy", err)
	}
}

// ---- status ----------------------------------------------------------------

// hubStatusData answers cmd=hubStatus from memory and the store; it never
// contacts the hub.
func (d *Daemon) hubStatusData() protocol.HubStatusData {
	d.mu.Lock()
	h := d.cfg.Hub
	d.mu.Unlock()
	out := protocol.HubStatusData{Enabled: h.Enabled, URL: h.URL, MaxClaimsPerDay: h.ClaimsPerDay()}
	now := time.Now()
	if d.hub != nil {
		now = d.hub.now()
		out.ClaimsToday = d.hubClaimsToday(now)
		w := d.hub
		w.mu.Lock()
		out.Joined, out.Device, out.Org = w.joined, w.device, w.org
		out.LastHeartbeat, out.LastError, out.LastErrorAt = w.lastBeat, w.lastErr, w.lastErrT
		out.PendingOutcomes = len(w.st.Outbox)
		out.PolicyVersion = w.policyV
		w.mu.Unlock()
	}
	for _, s := range d.sessions.Snapshot() {
		if s.HubItem.ItemID == "" {
			continue
		}
		out.Sessions = append(out.Sessions, protocol.HubSessionInfo{
			Session: s.ID, Project: s.Project, ItemID: s.HubItem.ItemID, Kind: s.HubItem.Kind,
			ClaimedAt: s.HubItem.ClaimedAt, LeaseExpires: s.HubItem.LeaseExpires,
			LeaseLost: s.HubItem.LeaseLost, Reported: s.HubItem.Reported,
		})
	}
	return out
}
