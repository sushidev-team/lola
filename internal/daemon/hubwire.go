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
//   - The hub never stops a session. A lost lease is claimed back once (the
//     hub holds a lapsed lease for its previous holder for a grace window);
//     only when another device holds it is it recorded on the session, and
//     the agent keeps working while lola stops reporting for it.
//   - A dependency advisory's PR moves the item to in_review (progress), not
//     to done: the work stays open here, so the repository keeps its one PR
//     at a time, until the PR merges (complete) or closes (fail).
//   - Lease renewal runs on its own timer (at most a third of the lease's
//     life), so a long poll interval never loses a lease.
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
	"github.com/sushidev-team/lola/internal/notify"
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
	// hubLegacyRetry is how long lola speaks the old frames after a hub showed
	// it predates the chore lifecycle (404 on progress, 400 on a new field),
	// before it tries the new ones again (the hub may have been upgraded).
	hubLegacyRetry = 6 * time.Hour
	// hubDefaultLeaseTTL is assumed for renewal until a lease shows the hub's
	// own TTL.
	hubDefaultLeaseTTL = 15 * time.Minute
	// hubMinRenew bounds how often the renewal timer may fire.
	hubMinRenew = 10 * time.Second
	// hubBackoffMax caps the retry spacing of an outcome a proxy refused.
	hubBackoffMax = time.Hour
	// hubProgressRefresh is how often an in-review item whose delivery has not
	// changed is reported again: well inside hubproto.InReviewIdleReopen, so a
	// slow review is not taken for an abandoned PR, even across a few missed
	// passes or a hub outage of days.
	hubProgressRefresh = hubproto.InReviewIdleReopen / 4
	// hubMaxSkips bounds the offer skip reasons kept for status.
	hubMaxSkips = 100
)

// Outcome actions in the outbox.
const (
	hubActComplete = "complete"
	hubActFail     = "fail"
	hubActRelease  = "release"
	// hubActProgress reports the PR of a dep_advisory (claimed -> in_review,
	// or a new delivery while in review). Not one-shot: a newer delivery
	// replaces a queued one, and it never settles the claim.
	hubActProgress = "progress"
)

// hubAPI is the daemon's seam over *hub.Client: everything the loop sends.
// Tests install a fake through hubWire.newClient.
type hubAPI interface {
	Heartbeat(ctx context.Context, hb hubproto.HeartbeatRequest) (hubproto.HeartbeatResponse, error)
	ReportFindings(ctx context.Context, items []hubproto.Item) (hubproto.FindingsResponse, error)
	Offers(ctx context.Context) ([]hubproto.Item, int, error)
	Claim(ctx context.Context, id string) (hubproto.Lease, error)
	Complete(ctx context.Context, id string, req hubproto.CompleteRequest) error
	Progress(ctx context.Context, id string, req hubproto.ProgressRequest) error
	Fail(ctx context.Context, id string, req hubproto.FailRequest) error
	Release(ctx context.Context, id string) error
	// Board and Org are the read-only org views (validated whole).
	Board(ctx context.Context, state hubproto.State, limit int) (hubproto.BoardResponse, error)
	Org(ctx context.Context) (hubproto.OrgView, error)
	// Stats is the org's daily counters (validated whole). Display only.
	Stats(ctx context.Context) (hubproto.StatsView, error)
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
	// Group is the grouped session's fail group (fail only).
	Group string    `json:"group,omitempty"`
	Since time.Time `json:"since"`
	Tries int       `json:"tries,omitempty"`
	// NoLease: the item is in review, owned by this device without a lease;
	// its outcome is sent as the owner's and keeps no lease renewed.
	NoLease bool `json:"no_lease,omitempty"`
	// NextTry spaces the retries of an outcome a proxy refused with a bare
	// 4xx (no hubproto code): zero = retry on the next pass.
	NextTry time.Time `json:"next_try,omitzero"`
}

// key is the outcome's claim identity: one terminal outcome per claim, and
// at most one queued progress beside it.
func (o hubOutcome) key() string {
	k := fmt.Sprintf("%s@%d", o.ItemID, o.ClaimedAt.UnixNano())
	if o.Action == hubActProgress {
		k += "/progress"
	}
	return k
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
	// when the cooldown counts from; hubTryClaim skips them for hubCooldown
	// from then. A shorter cooldown (hubCoolDownFor) is stored as a start
	// moved back, so one map and one rule serve both.
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
	// beatTried is the last heartbeat ATTEMPT and beatFails the failures in
	// a row since the last success: the renewal timer backs off from them
	// (hubRenewIn), since lastBeat moves only on success.
	beatTried time.Time
	beatFails int
	lastErr   string
	lastErrT  time.Time
	policyV   int
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
	// board / org / stats cache the read-only views for the UIs (hubcmds.go).
	board     map[string]hubBoardCache
	orgAt     time.Time
	orgView   *hubproto.OrgView
	statsAt   time.Time
	statsView *hubproto.StatsView

	// beginPass starts a cycle's GitHub reads afresh (the collector's
	// per-cycle alert cache); nil = nothing to reset.
	beginPass func()
	// leaseTTL is the hub's lease life as the last claim or renewal showed
	// it; renewal runs at a third of it.
	leaseTTL time.Duration
	// legacyUntil: until then the hub is treated as predating the chore
	// lifecycle (no progress, no fail pr_url / group, no policy_version).
	legacyUntil time.Time
	// cycleFailed is set by hubSetErr during a cycle; a cycle that ends
	// without it clears lastErr and stamps lastSync.
	cycleFailed bool
	lastSync    time.Time
	// health is the last findings scan per repository; skips the offers the
	// last cycle left unclaimed, with the stable reason.
	health []protocol.HubRepoHealth
	skips  []protocol.HubOfferSkip
}

func newHubWire() *hubWire {
	col := &hub.Collector{Gh: hub.ExecGh("gh", hub.DefaultGhMaxOutput)}
	return &hubWire{
		newClient: newRealHubClient,
		collect:   col.Collect,
		verify:    col.Verify,
		beginPass: col.BeginPass,
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
	if o.Action == hubActProgress {
		// The newest delivery is the one worth sending: it replaces a
		// queued progress of the same claim instead of queueing behind it.
		for i, q := range w.st.Outbox {
			if q.key() == k {
				w.st.Outbox[i] = o
				if err := d.hubSaveLocked(); err != nil {
					d.logf("", "hub: persist outbox: %v", err)
				}
				return
			}
		}
	} else {
		for _, q := range w.st.Outbox {
			if q.key() == k {
				return // one outcome per claim
			}
		}
		if slices.Contains(w.st.Settled, k) {
			return
		}
		// A terminal outcome supersedes a progress of the same claim still
		// queued: the hub takes complete / fail from claimed as well.
		pk := hubOutcome{ItemID: o.ItemID, ClaimedAt: o.ClaimedAt, Action: hubActProgress}.key()
		w.st.Outbox = slices.DeleteFunc(w.st.Outbox, func(q hubOutcome) bool { return q.key() == pk })
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

// hubCoolDown marks id as ended here without completion, for hubCooldown.
func (d *Daemon) hubCoolDown(id string, now time.Time) {
	d.hubCoolDownFor(id, now, hubCooldown)
}

// hubCoolDownFor keeps id unclaimable here for dur (at most hubCooldown). A
// longer cooldown already running is never shortened.
func (d *Daemon) hubCoolDownFor(id string, now time.Time, dur time.Duration) {
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
	from := now.Add(min(dur, hubCooldown) - hubCooldown)
	if at, ok := w.st.Cooldown[id]; !ok || from.After(at) {
		w.st.Cooldown[id] = from
	}
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
		// A progress belongs to a work still open (renewed from the store),
		// and an in-review item holds no lease to renew.
		if o.Action == hubActRelease || o.Action == hubActProgress || o.NoLease {
			continue
		}
		if !slices.Contains(ids, o.ItemID) {
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
	w.cycleFailed = true
	w.mu.Unlock()
	d.logf("", "hub: %s", msg)
}

// ---- loop ------------------------------------------------------------------

// hubLoop runs a cycle at startup and then every [hub].poll_seconds (re-read
// after each whole cycle), plus whenever nudged. Between cycles a renewal
// pass runs when a live lease would otherwise go a third of its life
// unrenewed. The whole cycle keeps its OWN deadline: a renewal pass never
// pushes it back, or a poll_seconds of at least a third of the lease life
// would starve findings, offers and the policy fetch for as long as any
// lease is live. It does nothing while the hub is off.
func (d *Daemon) hubLoop(ctx context.Context) {
	defer d.wg.Done()
	d.safeHub(ctx, true)
	nextFull := time.Now().Add(d.hubInterval())
	for {
		r, ok := d.hubRenewIn()
		wait, renew := hubNextWake(time.Now(), nextFull, r, ok)
		t := time.NewTimer(wait)
		full := true
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
			if renew {
				d.safeHubRenew(ctx)
				continue
			}
		case <-d.hub.full:
			t.Stop()
		case <-d.hub.outcomes:
			t.Stop()
			full = false
		}
		d.safeHub(ctx, full)
		if full {
			nextFull = time.Now().Add(d.hubInterval())
		}
	}
}

// hubNextWake is how long the loop sleeps and whether it then runs a renewal
// pass (renew) rather than the whole cycle: whichever of the two deadlines
// comes first. A whole cycle due now runs at once; it renews the leases too.
func hubNextWake(now, nextFull time.Time, renewIn time.Duration, renewOK bool) (time.Duration, bool) {
	wait := max(nextFull.Sub(now), 0)
	if renewOK && renewIn < wait {
		return renewIn, true
	}
	return wait, false
}

// hubRenewIn is how long until the live leases need renewing: a third of the
// lease life after the last heartbeat, never sooner than hubMinRenew. After
// failed heartbeats it also waits out hubRenewBackoff from the last ATTEMPT:
// lastBeat moves only on success, so a hub that is down or rate-limiting
// would otherwise be asked again every hubMinRenew. ok=false when nothing
// holds a lease.
func (d *Daemon) hubRenewIn() (time.Duration, bool) {
	if len(d.hubLiveLeases()) == 0 {
		return 0, false
	}
	w := d.hub
	w.mu.Lock()
	ttl, last, tried, fails := w.leaseTTL, w.lastBeat, w.beatTried, w.beatFails
	w.mu.Unlock()
	if ttl <= 0 {
		ttl = hubDefaultLeaseTTL
	}
	due := last.Add(ttl / 3)
	if fails > 0 {
		due = maxTime(due, tried.Add(hubRenewBackoff(fails, ttl)))
	}
	return max(time.Until(due), hubMinRenew), true
}

// hubRenewBackoff spaces the renewal retries after fails failed heartbeats in
// a row: 30s, 1m, 2m … at most a third of the lease life (the normal renewal
// spacing), so a short outage still renews before the lease lapses.
func hubRenewBackoff(fails int, ttl time.Duration) time.Duration {
	limit := max(ttl/3, hubMinRenew)
	if fails > 10 {
		return limit
	}
	return min(30*time.Second<<max(fails-1, 0), limit)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// safeHubRenew is the lightweight pass the renewal timer runs: outcomes (an
// agent that died is reported without waiting for the poll) and a heartbeat.
// No findings, no offers, no policy.
func (d *Daemon) safeHubRenew(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			d.logf("", "hub panic (daemon keeps running): %v", r)
		}
	}()
	d.hubRenewCycle(context.WithoutCancel(ctx))
}

func (d *Daemon) hubRenewCycle(ctx context.Context) {
	snap, ok := d.hubConfig()
	if !ok {
		return
	}
	api, _, ok := d.hubConnect(snap.h)
	if !ok {
		return
	}
	d.hubOutcomes(ctx, api)
	d.hubHeartbeat(ctx, api, snap)
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
			// A revocation record outlives the identity it replaced: keep
			// saying "revoked" until a new join, not "not joined".
			if _, revoked := hub.LoadRevocation(d.hubDir()); revoked {
				d.hubSetErr("identity", errHubRevoked)
			} else {
				d.hubSetErr("identity", errors.New("not joined: run `lola hub join <url>` with the token on stdin"))
			}
		} else {
			d.hubSetErr("identity", err)
		}
		return nil, hub.Identity{}, false
	}
	return api, id, true
}

// hubCycle is one whole pass. A pass in which no stage failed clears the
// last error and stamps the last sync.
func (d *Daemon) hubCycle(ctx context.Context) {
	snap, ok := d.hubConfig()
	if !ok {
		return
	}
	w := d.hub
	w.mu.Lock()
	w.cycleFailed = false
	w.mu.Unlock()
	api, id, ok := d.hubConnect(snap.h)
	if !ok {
		return
	}
	if w.beginPass != nil {
		w.beginPass() // GitHub is read afresh, once per repo, this cycle
	}
	// Outcomes first: they free leases the hub is waiting on.
	d.hubOutcomes(ctx, api)
	if !d.hubHeartbeat(ctx, api, snap) {
		return // the hub is unreachable or refuses us: claim nothing
	}
	d.hubFindings(ctx, api, snap)
	d.hubOffers(ctx, api, snap)
	d.hubPolicy(ctx, api, snap, id)
	w.mu.Lock()
	if !w.cycleFailed {
		w.lastErr, w.lastErrT, w.lastSync = "", time.Time{}, w.now()
	}
	w.mu.Unlock()
}

// hubLegacy reports whether the hub is treated as predating the chore
// lifecycle now.
func (d *Daemon) hubLegacy(now time.Time) bool {
	d.hub.mu.Lock()
	defer d.hub.mu.Unlock()
	return now.Before(d.hub.legacyUntil)
}

func (d *Daemon) hubMarkLegacy(why string) {
	w := d.hub
	w.mu.Lock()
	first := !w.now().Before(w.legacyUntil)
	w.legacyUntil = w.now().Add(hubLegacyRetry)
	w.mu.Unlock()
	if first {
		d.logf("", "hub: the hub predates the chore lifecycle (%s); using the old frames for %s", why, hubLegacyRetry)
	}
}

// hubNotify sends one of lola's own fixed notes (best effort).
func (d *Daemon) hubNotify(ctx context.Context, title, body string, p notify.Priority) {
	d.mu.Lock()
	n := d.notifier
	d.mu.Unlock()
	if n != nil {
		n.Notify(ctx, notify.Note{Title: title, Body: body, Priority: p})
	}
}

// hubStatusIs reports whether err is a hub answer with HTTP status st.
func hubStatusIs(err error, st int) bool {
	var ae *hub.APIError
	return errors.As(err, &ae) && ae.Status == st
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

// hubLeased reports whether w holds a lease to renew: open and not in review
// (an in-review item has an owner, not a lease).
func hubLeased(w *session.HubWork) bool { return w.Open() && !w.InReview }

// hubLiveLeases lists the item ids whose leases the heartbeat renews: every
// leased work of a live session, plus the items whose complete / fail still
// waits in the outbox.
func (d *Daemon) hubLiveLeases() []string {
	var live []string
	for _, s := range d.sessions.Snapshot() {
		if !hubLive(s) {
			continue
		}
		for _, w := range s.HubWorks() {
			if hubLeased(w) && !slices.Contains(live, w.ItemID) && len(live) < hubproto.MaxLiveLeases {
				live = append(live, w.ItemID)
			}
		}
	}
	for _, id := range d.hubUnsent() {
		if !slices.Contains(live, id) && len(live) < hubproto.MaxLiveLeases {
			live = append(live, id)
		}
	}
	return live
}

// hubAppliedPolicy is the team policy version the live config enforces (0 =
// none), for the heartbeat.
func (d *Daemon) hubAppliedPolicy() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.teamPolicy.applied {
		return 0
	}
	return d.teamPolicy.version
}

func (d *Daemon) hubHeartbeat(ctx context.Context, api hubAPI, snap hubSnapshot) bool {
	global, _ := d.slotCounts("")
	free := min(max(snap.globalCap-global, 0), hubproto.MaxFreeSlots)
	live := d.hubLiveLeases()
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
	if !d.hubLegacy(d.hub.now()) {
		req.PolicyVersion = d.hubAppliedPolicy()
	}
	resp, err := api.Heartbeat(ctx, req)
	if err != nil && req.PolicyVersion > 0 && hubStatusIs(err, 400) {
		// A hub that predates policy_version refuses the unknown field:
		// heartbeat without it rather than go dark. A current hub answers
		// the same 400 bad_request for any other refusal (too many leases),
		// so the hub is taken as old only when the retry WITHOUT the field
		// is accepted: the field alone was the difference.
		req.PolicyVersion = 0
		resp, err = api.Heartbeat(ctx, req)
		if err == nil {
			d.hubMarkLegacy("heartbeat policy_version refused")
		}
	}
	d.hub.mu.Lock()
	d.hub.beatTried = d.hub.now()
	if err != nil {
		d.hub.beatFails++
	} else {
		d.hub.beatFails = 0
	}
	d.hub.mu.Unlock()
	if err != nil {
		if d.hubRevokedIf(ctx, err) {
			return false // the device is gone: claim nothing, say "revoked"
		}
		d.hubSetErr("heartbeat", err)
		return false
	}
	now := d.hub.now()
	d.hub.mu.Lock()
	d.hub.lastBeat = now
	for _, l := range resp.Renewed {
		if ttl := l.Expires.Sub(now); ttl > 0 {
			d.hub.leaseTTL = ttl
		}
	}
	d.hub.mu.Unlock()
	renewed := map[string]time.Time{}
	for _, l := range resp.Renewed {
		renewed[l.ItemID] = l.Expires
	}
	lost := map[string]bool{}
	for _, id := range resp.Lost {
		lost[id] = true
	}
	// A lost lease is claimed back first: the hub holds a lapsed lease for
	// its previous holder for a grace window, and that claim is no new
	// attempt (nor a daily claim here). Only a refusal marks it lost.
	back := map[string]hubproto.Lease{}
	taken := map[string]bool{}
	gone := map[string]bool{}
	for _, s := range d.sessions.Snapshot() {
		for _, w := range s.HubWorks() {
			if !lost[w.ItemID] || !hubLeased(w) {
				continue
			}
			if _, done := back[w.ItemID]; done || taken[w.ItemID] || gone[w.ItemID] {
				continue
			}
			if !d.hubBeginClaim(w.ItemID) {
				continue // a claim is in flight; the next heartbeat decides
			}
			l, err := api.Claim(ctx, w.ItemID)
			d.hubEndClaim(w.ItemID)
			switch {
			case err == nil:
				back[w.ItemID] = l
				d.logf("", "hub: lease on item %s had lapsed; claimed it back for session %s", w.ItemID, s.ID)
			case hubStatusIs(err, 409):
				taken[w.ItemID] = true
			case hubTerminal(err):
				gone[w.ItemID] = true
			default:
				d.hubSetErr("re-claim", err) // unknown: keep it, ask again next time
			}
		}
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
		var takenNow []string
		_, _ = d.sessions.Update(s.ID, func(cur *session.Session) bool {
			wrote := false
			for _, w := range cur.HubWorks() {
				item := w.ItemID
				switch {
				case !hubLeased(w):
					// In review, reported, released or already lost: the
					// heartbeat has nothing to say about it.
				case lost[item]:
					if l, ok := back[item]; ok {
						w.LeaseExpires = l.Expires
						wrote = true
						continue
					}
					if !taken[item] && !gone[item] {
						continue
					}
					// The hub gave the item to another device, or it is no
					// longer there. The session is NOT stopped: the work may
					// still be useful, and the hub never controls a session.
					// lola only stops renewing and reporting for it.
					w.LeaseLost, w.LeaseTaken = true, taken[item]
					if taken[item] {
						takenNow = append(takenNow, item)
					}
					d.logf("", "hub: lease on item %s lost; session %s keeps running, no outcome will be reported for it", item, cur.ID)
					wrote = true
				case !renewed[item].IsZero():
					w.LeaseExpires = renewed[item]
					wrote = true
				}
			}
			changed = changed || wrote
			return wrote
		})
		for range takenNow {
			d.hubNotify(ctx, "Hub chore taken by another device",
				"Session "+s.ID+" keeps running; lola no longer reports it to the hub.", notify.Action)
		}
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
	d.hubNoteHealth(now, res.Health)
	if res.Dropped > 0 {
		d.logf("", "hub: findings: %d candidate(s) did not validate and were dropped", res.Dropped)
	}
	accepted, rejected := 0, 0
	for _, batch := range hub.Chunk(res.Items) {
		r, err := api.ReportFindings(ctx, batch)
		if err != nil {
			if !d.hubRevokedIf(ctx, err) {
				d.hubSetErr("report findings", err)
			}
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

// hubNoteHealth merges a scan's per-repo answers into status: a repo scanned
// now replaces its row, the others keep theirs until they are due again, and
// repos no longer opted in age out with the findings stamps.
func (d *Daemon) hubNoteHealth(now time.Time, rows []hub.RepoHealth) {
	w := d.hub
	w.mu.Lock()
	defer w.mu.Unlock()
	byRepo := map[string]protocol.HubRepoHealth{}
	for _, h := range w.health {
		if now.Sub(h.At) <= 7*24*time.Hour {
			byRepo[strings.ToLower(h.Repo)] = h
		}
	}
	for _, r := range rows {
		byRepo[strings.ToLower(r.Repo)] = protocol.HubRepoHealth{Repo: r.Repo, At: now, Alerts: r.Alerts, PullRequests: r.PullRequests, Error: r.Err}
	}
	w.health = slices.SortedFunc(maps.Values(byRepo), func(a, b protocol.HubRepoHealth) int { return strings.Compare(a.Repo, b.Repo) })
}

// ---- 3. offers → claim → spawn --------------------------------------------

// hubRefusal is a claim refused before anything was claimed (or a spawn that
// failed after): Reason is a stable hub.Reason* word for status, the message
// is what the log and a person's click show.
type hubRefusal struct{ reason, msg string }

func (r *hubRefusal) Error() string { return r.msg }

// hubRefuse builds a refusal; msg "" = the reason itself.
func hubRefuse(reason, msg string) error {
	if msg == "" {
		msg = reason
	}
	return &hubRefusal{reason: reason, msg: msg}
}

// hubReasonOf is err's stable reason (hub.ReasonOther when it carries none).
func hubReasonOf(err error) string {
	var r *hubRefusal
	if errors.As(err, &r) {
		return r.reason
	}
	if errors.Is(err, errHubSpawn) {
		return hub.ReasonSpawnFailed
	}
	return hub.ReasonOther
}

// hubNoteSkips replaces status's offer skip list with this cycle's.
func (d *Daemon) hubNoteSkips(skips []protocol.HubOfferSkip) {
	if len(skips) > hubMaxSkips {
		skips = skips[:hubMaxSkips]
	}
	d.hub.mu.Lock()
	d.hub.skips = skips
	d.hub.mu.Unlock()
}

func (d *Daemon) hubOffers(ctx context.Context, api hubAPI, snap hubSnapshot) {
	items, dropped, err := api.Offers(ctx)
	if err != nil {
		if !d.hubRevokedIf(ctx, err) {
			d.hubSetErr("offers", err)
		}
		return
	}
	if dropped > 0 {
		d.logf("", "hub: %d offered item(s) did not validate and were ignored", dropped)
	}
	now := d.hub.now()
	var skips []protocol.HubOfferSkip
	skip := func(its []hubproto.Item, reason string) {
		for _, it := range its {
			skips = append(skips, protocol.HubOfferSkip{ItemID: it.ID, Kind: string(it.Kind), Reason: reason, At: now})
		}
	}
	defer func() { d.hubNoteSkips(skips) }()
	groups, single := hubGroupOffers(items, snap.h.GroupsChores())
	for _, g := range groups {
		repo := g[0].DepAdvisory.Repo
		if d.hubRepoBusy(repo) {
			skip(g, hub.ReasonRepoBusy)
			continue // the running session's pull request comes first
		}
		claimed, err := d.hubClaimGroupItems(ctx, api, g, snap, hubClaimReq{})
		if err != nil {
			if !errors.Is(err, errHubSpawn) {
				d.logf("", "hub: not claiming %d advisories of %s: %s", len(g), repo, err)
			}
			skip(g, hubReasonOf(err))
			continue
		}
		var left []hubproto.Item
		for _, it := range g {
			if !slices.Contains(claimed, it.ID) {
				left = append(left, it)
			}
		}
		skip(left, hub.ReasonOther)
	}
	for _, it := range single {
		_, err := d.hubClaimItem(ctx, api, it, snap, hubClaimReq{})
		if err == nil {
			continue
		}
		if !errors.Is(err, errHubSpawn) {
			d.logf("", "hub: not claiming %s (%s): %s", it.ID, it.Kind, err)
		}
		skip([]hubproto.Item{it}, hubReasonOf(err))
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
	// scope, from a person's "update all dependencies", widens the briefing.
	scope *protocol.HubUpdateScope
}

// withScope appends a person's update scope to a briefing.
func withScope(brief string, scope *protocol.HubUpdateScope) string {
	if scope == nil {
		return brief
	}
	return brief + hub.UpdateBriefing(scope.Patch, scope.Minor, scope.Major, scope.Note)
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
		return "", hubRefuse(hub.ReasonInvalidItem, "")
	}
	if !d.hubBeginClaim(it.ID) {
		return "", hubRefuse(hub.ReasonInFlight, "")
	}
	defer d.hubEndClaim(it.ID)
	// Already working this item (a restart, a hub that re-offers)?
	for _, s := range d.sessions.Snapshot() {
		if s.HubWork(it.ID) != nil {
			return "", hubRefuse(hub.ReasonWorking, "")
		}
	}
	if !req.human && d.hubCoolingDown(it.ID, now) {
		return "", hubRefuse(hub.ReasonCooldown, "")
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
	var kind agent.Kind
	if pp != nil {
		projectCap = d.cfg.EffectiveCap(pp)
		kind = agent.Parse(d.cfg.AgentForProject(p.Name))
		agentBin = sandboxedBin(d.cfg, p.Name, kind.Binary())
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
			return "", hubRefuse(why, "")
		}
	} else if ok, why := hub.Accept(it, accProject, snap.h, d.hubClaimsToday(now)); !ok {
		return "", hubRefuse(why, "")
	}
	branch := hubBranch(prefix, it)
	id := runtime.ManualSessionID(p.Name, branch)
	if _, exists := d.sessions.Get(id); exists {
		return "", hubRefuse(hub.ReasonBranchTaken, "a session already works "+branch)
	}
	// The same gates as a Linear dispatch, in the same order, all BEFORE the
	// claim: hold, health, login, slots.
	if err := d.hubGates(ctx, p, kind, agentBin, nat, health, now); err != nil {
		return "", err
	}
	// A person's claim checks no cap (it is intent), but it still RESERVES
	// so a loop tick spawning beside it counts it. The slot is taken before
	// GitHub is asked: a capped machine spends no gh call on an offer it
	// could not run anyway.
	pCap, gCap := projectCap, snap.globalCap
	if req.human {
		pCap, gCap = noProjectCap, math.MaxInt
	}
	release, fits := d.reserveSlots(p.Name, 1, pCap, gCap)
	if !fits {
		return "", hubRefuse(hub.ReasonCapped, "")
	}
	defer release()
	// The offer is the hub's claim; GitHub's answer is the fact. Everything
	// from here on — branch, briefing, start head — comes from the verified
	// item, and any doubt refuses the offer (nothing claimed).
	if d.hub.verify == nil {
		return "", hubRefuse(hub.ReasonNotConfirmed, "cannot verify the offer on GitHub")
	}
	v, err := d.hub.verify(ctx, it)
	if err != nil {
		// ErrNotConfirmed already says "GitHub does not confirm"; keep only the why.
		why := strings.TrimPrefix(err.Error(), hub.ErrNotConfirmed.Error()+": ")
		return "", hubRefuse(hub.ReasonNotConfirmed, "GitHub does not confirm it: "+why)
	}
	if v.Item.ID != it.ID || v.Item.Kind != it.Kind || hubproto.Validate(v.Item) != nil ||
		(it.Kind == hubproto.KindRedBumpPR && v.Head == "") {
		return "", hubRefuse(hub.ReasonNotConfirmed, "")
	}
	it = v.Item
	if hubBranch(prefix, it) != branch {
		return "", hubRefuse(hub.ReasonNotConfirmed, "GitHub does not confirm it: the branch differs")
	}
	brief, err := hub.Briefing(it)
	if err != nil {
		return "", hubRefuse(hub.ReasonInvalidItem, "")
	}
	brief = withScope(brief, req.scope)

	// Count first, then claim: a crash between the two over-counts (safe),
	// never under-counts. A person's claim does not spend the daily cap.
	if !req.human {
		if err := d.hubCountClaim(now, 1); err != nil {
			return "", hubRefuse(hub.ReasonPersist, "persist claim count: "+err.Error())
		}
	}
	lease, err := api.Claim(ctx, it.ID)
	if err != nil {
		if !req.human {
			if rerr := d.hubCountClaim(now, -1); rerr != nil {
				d.logf("", "hub: refund claim count: %v", rerr)
			}
		}
		return "", hubRefuse(hub.ReasonClaimRefused, "claim refused: "+err.Error())
	}
	d.hubNoteLease(lease, now)
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
		d.hubSpawnFailed(ctx, api, []string{it.ID}, now, "", err)
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
	if !req.human {
		d.hubNotify(ctx, "Hub chore claimed", "Session "+id+" works "+sess.HubItem.Label+".", notify.Info)
	}
	return id, nil
}

// hubGates are the gates a Linear dispatch passes, in its order, for one hub
// claim: dispatch hold, runtime health, and — for a sandboxed project — a
// login the container can use (an agent without one would print "Not logged
// in" and park forever, holding the lease). Nothing is mutated.
func (d *Daemon) hubGates(ctx context.Context, p config.Project, kind agent.Kind, agentBin string, nat NativeAPI, health func(string) error, now time.Time) error {
	if msg := d.dispatchHold(ctx, p.Name, now); msg != "" {
		return hubRefuse(hub.ReasonHeld, "dispatch held: "+msg)
	}
	if nat == nil {
		return hubRefuse(hub.ReasonRuntime, "native runtime unavailable")
	}
	if err := health(agentBin); err != nil {
		return hubRefuse(hub.ReasonRuntime, "runtime unavailable: "+err.Error())
	}
	if p.Sandboxed() && !d.sandboxLoginReady(kind) {
		return hubRefuse(hub.ReasonNoSandboxLogin, sandboxLoginMissing(kind))
	}
	return nil
}

// hubNoteLease records the hub's lease life from a fresh lease, for the
// renewal timer.
func (d *Daemon) hubNoteLease(l hubproto.Lease, now time.Time) {
	if ttl := l.Expires.Sub(now); ttl > 0 {
		d.hub.mu.Lock()
		d.hub.leaseTTL = ttl
		d.hub.mu.Unlock()
	}
}

// hubSpawnFailed reports a spawn that failed after the claim: fail
// spawn_failed (an attempt the hub counts, so a broken project escalates
// instead of looping), and a local cooldown so this machine does not claim
// the item again — and spend max_claims_per_day on it — for
// hubproto.DeviceCooldown. The daily count stays spent.
func (d *Daemon) hubSpawnFailed(ctx context.Context, api hubAPI, ids []string, claimedAt time.Time, group string, err error) {
	d.logf("", "hub: spawn for %d item(s) failed, reporting spawn_failed: %v", len(ids), err)
	for _, id := range ids {
		d.hubEnqueue(hubOutcome{ItemID: id, ClaimedAt: claimedAt, Action: hubActFail, Reason: string(hubproto.FailSpawn), Group: group, Since: claimedAt})
		d.hubCoolDownFor(id, claimedAt, hubproto.DeviceCooldown)
	}
	d.hubNotify(ctx, "Hub chore failed", "A claimed chore's session could not start; lola reported it to the hub.", notify.Action)
	d.hubOutcomes(ctx, api)
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
		return fmt.Sprintf("PR #%d · %s", it.RedBumpPR.PR, hubPackageWord(it.RedBumpPR.Package))
	}
	return string(it.Kind)
}

// hubTitle is a list-row title built from validated fields only.
func hubTitle(it hubproto.Item) string {
	switch it.Kind {
	case hubproto.KindDepAdvisory:
		return it.DepAdvisory.Advisory + " in " + it.DepAdvisory.Package
	case hubproto.KindRedBumpPR:
		return fmt.Sprintf("fix #%d (%s)", it.RedBumpPR.PR, hubPackageWord(it.RedBumpPR.Package))
	}
	return string(it.Kind)
}

// hubPackageWord names a red bump PR's package; a grouped update names none.
func hubPackageWord(pkg string) string {
	if pkg == "" {
		return "grouped update"
	}
	return pkg
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

// hubOutcomeFor decides the TERMINAL outcome to report for s, or ok=false for
// "nothing yet". ended is set when the session is being torn down: then
// something is always reported. legacy is a hub that predates progress.
//
//   - a PR that closed unmerged fails the item (pr_closed) — except a red bump
//     PR whose head the agent never moved: the bot or a person closed it, the
//     finding no longer holds (not_reproducible);
//   - a merged PR completes it;
//   - dep_advisory: an open PR is progress (hubProgressFor), not an outcome:
//     the item is in review until the PR merges or closes. It completes on the
//     open PR only for a legacy hub, or at teardown (the PR stands, nothing
//     here will report it later);
//   - red_bump_pr completes once the PR head moved past the one first seen and
//     the delivery is green: the bot's PR existed before the agent did
//     anything, so its mere presence proves nothing;
//   - an agent that ended on its own (dead / exited) fails agent_gave_up as
//     soon as the observer sees it, when it left no PR (or an unmoved, still
//     red bump PR); a PR that stands is left to its own facts;
//   - at teardown, a red bump PR the agent pushed to that never went green:
//     ci_failed; one that is green although the agent never moved its head:
//     not_reproducible (the finding no longer holds);
//   - otherwise at teardown: agent_gave_up when the agent had ended, refused
//     when a person stopped a live one (no attempt is counted). An unmoved,
//     still-red bump PR lands here too: closing it as not_reproducible would
//     bury a real failure for good.
//
// A fail carries the PR URL when a PR exists, and the grouped session's fail
// group.
func hubOutcomeFor(s session.Session, w session.HubWork, ended, legacy bool) (hubOutcome, bool) {
	o := hubOutcome{ItemID: w.ItemID, ClaimedAt: w.ClaimedAt, NoLease: w.InReview}
	url := hubPRURL(s)
	s.EnsureAxes()
	redBump := w.Kind == string(hubproto.KindRedBumpPR)
	moved := url != "" && w.StartHead != "" && s.PR.HeadSHA != "" && s.PR.HeadSHA != w.StartHead
	fail := func(r hubproto.FailReason) (hubOutcome, bool) {
		o.Action, o.Reason, o.PRURL, o.Group = hubActFail, string(r), url, w.Group
		return o, true
	}
	if url != "" {
		switch {
		case s.Delivery == state.DeliveryClosed:
			if redBump && w.StartHead != "" && s.PR.HeadSHA == w.StartHead {
				return fail(hubproto.FailObsolete)
			}
			return fail(hubproto.FailPRClosed)
		case s.Delivery == state.DeliveryMerged,
			w.Kind == string(hubproto.KindDepAdvisory) && (legacy || ended),
			redBump && moved && hubGreen(s.Delivery):
			o.Action, o.PRURL, o.Delivery = hubActComplete, url, string(hubproto.DeliveryNone)
			if hubproto.Delivery(s.Delivery).Valid() {
				o.Delivery = string(s.Delivery)
			}
			return o, true
		}
	}
	agentEnded := s.AgentState == state.AgentDead || s.AgentState == state.AgentExited
	if !ended {
		switch {
		case !agentEnded:
			return o, false
		case url == "", redBump && !moved && !hubGreen(s.Delivery):
			return fail(hubproto.FailAgent)
		}
		return o, false
	}
	switch {
	case moved:
		return fail(hubproto.FailCI)
	case url != "" && hubGreen(s.Delivery):
		return fail(hubproto.FailObsolete)
	case agentEnded:
		return fail(hubproto.FailAgent)
	}
	return fail(hubproto.FailRefused)
}

// hubProgressFor is the progress to report for a dep_advisory whose PR is
// open: the PR and its delivery, once per delivery change — and again every
// hubProgressRefresh while the item is in review with nothing new to say.
// An in-review item holds no lease, so no heartbeat touches it: the hub
// reopens one with no progress call for hubproto.InReviewIdleReopen and
// offers it to other devices, which would open a second PR for a review that
// is merely slow. ok=false when there is none (no PR, not an advisory, a
// legacy hub, or nothing changed and nothing is due).
func hubProgressFor(s session.Session, w session.HubWork, legacy bool, now time.Time) (hubOutcome, bool) {
	if legacy || w.Kind != string(hubproto.KindDepAdvisory) {
		return hubOutcome{}, false
	}
	url := hubPRURL(s)
	s.EnsureAxes()
	del := hubproto.Delivery(s.Delivery)
	if url == "" || !del.Valid() || del == hubproto.DeliveryMerged || del == hubproto.DeliveryClosed {
		return hubOutcome{}, false
	}
	// A work stamped before ProgressAt existed (zero) is refreshed once now.
	if string(del) == w.Progress && (!w.InReview || now.Sub(w.ProgressAt) < hubProgressRefresh) {
		return hubOutcome{}, false
	}
	return hubOutcome{ItemID: w.ItemID, ClaimedAt: w.ClaimedAt, Action: hubActProgress, PRURL: url, Delivery: string(del), NoLease: w.InReview}, true
}

// hubTimedOut: a claim older than hubproto.DefaultMaxLeaseAge that has not
// reached review. The hub stops renewing such a lease; lola reports timeout
// once and stops renewing it too. The session is never stopped.
func hubTimedOut(w session.HubWork, now time.Time) bool {
	return !w.InReview && w.Progress == "" && !w.ClaimedAt.IsZero() && now.Sub(w.ClaimedAt) >= hubproto.DefaultMaxLeaseAge
}

// hubOutcomes stamps each live hub session's outcome (or progress) once it is
// known, queues it, and drains the outbox.
func (d *Daemon) hubOutcomes(ctx context.Context, api hubAPI) {
	now := d.hub.now()
	legacy := d.hubLegacy(now)
	changed := false
	failed := 0
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
				if o, ok := hubOutcomeFor(*cur, *w, false, legacy); ok {
					queued = append(queued, o)
					continue
				}
				if hubTimedOut(*w, now) {
					o := hubOutcome{ItemID: w.ItemID, ClaimedAt: w.ClaimedAt, Action: hubActFail,
						Reason: string(hubproto.FailTimeout), PRURL: hubPRURL(*cur), Group: w.Group}
					queued = append(queued, o)
					continue
				}
				if o, ok := hubProgressFor(*cur, *w, legacy, now); ok {
					queued = append(queued, o)
				}
			}
			changed = changed || wrote
			return wrote
		})
		// Persist each outcome FIRST, then stamp its guard: a crash in
		// between re-derives the same outcome, which the outbox dedups by
		// claim. The other order could persist a guard with no outcome.
		for _, o := range queued {
			o.Since = now
			d.hubEnqueue(o)
			_, _ = d.sessions.Update(s.ID, func(cur *session.Session) bool {
				w := cur.HubWork(o.ItemID)
				if w == nil || !w.Open() || !w.ClaimedAt.Equal(o.ClaimedAt) {
					return false
				}
				if o.Action == hubActProgress {
					// Stamped at queue time, so a refresh is queued once
					// per hubProgressRefresh even while the hub is down.
					w.Progress, w.ProgressAt = o.Delivery, now
				} else {
					w.Reported = o.Action
					if o.Reason != "" {
						w.Reported += ":" + o.Reason
					}
				}
				changed = true
				return true
			})
			if o.Action == hubActFail {
				// The hub keeps it from this device for a while too; the
				// local mark holds even against an older hub.
				d.hubCoolDownFor(o.ItemID, now, hubproto.DeviceCooldown)
				failed++
				d.logf("", "hub: session %s: reporting fail %s for item %s", s.ID, o.Reason, o.ItemID)
			}
		}
	}
	if changed {
		if err := d.sessions.Save(); err != nil {
			d.logf("", "hub: persist sessions: %v", err)
		}
	}
	if failed > 0 {
		d.hubNotify(ctx, "Hub chore failed", fmt.Sprintf("lola reported %d hub chore(s) as failed; the sessions keep running.", failed), notify.Action)
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
	legacy := d.hubLegacy(d.hub.now())
	for _, w := range s.HubWorks() {
		if !w.Open() {
			continue
		}
		o, _ := hubOutcomeFor(s, *w, true, legacy)
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
// rate limiting, and a 4xx with no hubproto code at all (a proxy's 403/404,
// which says nothing about the hub's answer) — is retried.
func hubTerminal(err error) bool {
	var ae *hub.APIError
	if !errors.As(err, &ae) {
		return errors.Is(err, hub.ErrInvalidResponse)
	}
	// 408/429 are retried whatever the code: a proxy in front of the hub
	// sends them with no hubproto code at all.
	if ae.Status == 408 || ae.Status == 429 || ae.Code == "" {
		return false
	}
	return ae.Status >= 400 && ae.Status < 500 && ae.Code != hubproto.CodeRateLimited && ae.Code != hubproto.CodeSkew
}

// hubProxyRefusal: a 4xx without a hubproto code. Retried, but spaced out.
func hubProxyRefusal(err error) bool {
	var ae *hub.APIError
	return errors.As(err, &ae) && ae.Code == "" && ae.Status >= 400 && ae.Status < 500 && ae.Status != 408 && ae.Status != 429
}

// hubBackoff is the wait before the tries-th retry of a proxy-refused
// outcome: 1, 2, 4 … minutes, at most hubBackoffMax.
func hubBackoff(tries int) time.Duration {
	if tries > 6 {
		return hubBackoffMax
	}
	return min(time.Minute<<max(tries-1, 0), hubBackoffMax)
}

// hubSend sends one queued outcome, with the compatibility and recovery
// steps the protocol allows, and returns the outcome as last sent:
//
//   - progress answered 404 by a hub that predates it becomes the old
//     complete-on-PR (C12);
//   - a fail whose pr_url / group an old hub refuses (400) is sent again with
//     the reason alone;
//   - 409 lease_lost is not final at once: the lease may have lapsed into the
//     hub's grace window, so the item is claimed back and the outcome sent
//     once more (a release is never re-claimed: it would take the item again).
func (d *Daemon) hubSend(ctx context.Context, api hubAPI, o hubOutcome) (hubOutcome, error) {
	send := func(o hubOutcome) error {
		switch o.Action {
		case hubActComplete:
			return api.Complete(ctx, o.ItemID, hubproto.CompleteRequest{PRURL: o.PRURL, Delivery: hubproto.Delivery(o.Delivery)})
		case hubActProgress:
			return api.Progress(ctx, o.ItemID, hubproto.ProgressRequest{PRURL: o.PRURL, Delivery: hubproto.Delivery(o.Delivery)})
		case hubActFail:
			req := hubproto.FailRequest{Reason: hubproto.FailReason(o.Reason), PRURL: o.PRURL, Group: o.Group}
			if hubproto.ValidatePRURL(req.PRURL) != nil {
				req.PRURL = ""
			}
			if !hubproto.ValidFailGroup(req.Group) {
				req.Group = ""
			}
			extra := req.PRURL != "" || req.Group != ""
			if extra && d.hubLegacy(d.hub.now()) {
				req.PRURL, req.Group, extra = "", "", false
			}
			err := api.Fail(ctx, o.ItemID, req)
			if extra && hubStatusIs(err, 400) {
				// An old hub refuses the unknown fields; a current one
				// answers the same 400 bad_request to a pr_url that is
				// not the item's PR. Either way the reason alone goes
				// through. Only a refused GROUP alone (a valid one: it
				// passed ValidFailGroup, as the hub checks it) proves an
				// old hub; a refused pr_url proves nothing, so it never
				// downgrades the whole hub.
				groupOnly := req.PRURL == ""
				req.PRURL, req.Group = "", ""
				err = api.Fail(ctx, o.ItemID, req)
				if err == nil && groupOnly {
					d.hubMarkLegacy("fail group refused")
				}
			}
			return err
		case hubActRelease:
			return api.Release(ctx, o.ItemID)
		}
		return &hub.APIError{Status: 400, Code: hubproto.CodeBadRequest, Message: "unknown outbox action"}
	}
	err := send(o)
	if o.Action == hubActProgress && hubStatusIs(err, 404) {
		// An old hub has no progress route; a current one answers the
		// same 404 not_found for an item it does not know (purged). The
		// old complete-on-PR goes out either way, and the hub is taken as
		// old only when that complete was NOT also a 404: the hub knows
		// the item, so it was the route that was missing. Otherwise one
		// purged item would downgrade the hub for hubLegacyRetry, and every
		// dep_advisory would complete on an open PR instead of entering
		// review.
		o.Action = hubActComplete
		err = send(o)
		if !hubStatusIs(err, 404) {
			d.hubMarkLegacy("no progress frame")
		}
	}
	if o.Action != hubActRelease && hub.IsCode(err, hubproto.CodeLeaseLost) {
		if l, cerr := api.Claim(ctx, o.ItemID); cerr == nil {
			d.hubNoteLease(l, d.hub.now())
			d.logf("", "hub: %s for item %s met a lapsed lease; claimed it back and sending once more", o.Action, o.ItemID)
			err = send(o)
		}
	}
	return o, err
}

// hubStampWork applies fn to the session work that holds the claim
// (itemID, claimedAt), if one is still in the store.
func (d *Daemon) hubStampWork(itemID string, claimedAt time.Time, fn func(*session.HubWork)) {
	for _, s := range d.sessions.Snapshot() {
		if w := s.HubWork(itemID); w == nil || !w.ClaimedAt.Equal(claimedAt) {
			continue
		}
		_, _ = d.sessions.Update(s.ID, func(cur *session.Session) bool {
			w := cur.HubWork(itemID)
			if w == nil || !w.ClaimedAt.Equal(claimedAt) {
				return false
			}
			fn(w)
			return true
		})
		if err := d.sessions.Save(); err != nil {
			d.logf("", "hub: persist sessions: %v", err)
		}
		return
	}
}

// hubFlush sends every queued outcome that is due once.
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
	type result struct {
		done bool
		next hubOutcome // the entry to keep when not done
	}
	results := map[string]result{}
	var settle []string
	for _, o := range queue {
		if !o.NextTry.IsZero() && now.Before(o.NextTry) {
			continue // a proxy refused it: spaced out
		}
		sent, err := d.hubSend(ctx, api, o)
		if d.hubRevokedIf(ctx, err) {
			// A refused signature is the device being revoked, not this
			// outcome being refused: keep the queue as it is (no 4xx drop)
			// and send nothing more as a device the hub no longer knows.
			break
		}
		if sent.Action != o.Action {
			// A legacy hub: the progress went out as the old complete, which
			// is one-shot. Stamp the guard whatever its fate, so the next
			// pass does not derive it again.
			d.hubStampWork(o.ItemID, o.ClaimedAt, func(w *session.HubWork) { w.Reported = hubActComplete })
		}
		switch {
		case err == nil:
			results[o.key()] = result{done: true}
			if sent.Action == hubActProgress {
				d.hubStampWork(o.ItemID, o.ClaimedAt, func(w *session.HubWork) { w.InReview = true })
			} else {
				settle = append(settle, sent.key())
			}
			d.logf("", "hub: reported %s %s for item %s", sent.Action, sent.Reason, sent.ItemID)
		case hubTerminal(err):
			results[o.key()] = result{done: true}
			if sent.Action != hubActProgress {
				settle = append(settle, sent.key())
			}
			d.logf("", "hub: %s for item %s refused, dropping it: %v", sent.Action, sent.ItemID, err)
		case now.Sub(o.Since) > hubOutboxMaxAge:
			results[o.key()] = result{done: true}
			if sent.Action != hubActProgress {
				settle = append(settle, sent.key())
			}
			d.logf("", "hub: %s for item %s unacknowledged for %s, dropping it: %v", sent.Action, sent.ItemID, hubOutboxMaxAge, err)
		default:
			sent.Tries++
			sent.NextTry = time.Time{}
			if hubProxyRefusal(err) {
				sent.NextTry = now.Add(hubBackoff(sent.Tries))
			}
			results[o.key()] = result{next: sent}
			d.hubSetErr(sent.Action, err)
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	kept := w.st.Outbox[:0:0]
	seen := map[string]bool{}
	for _, o := range w.st.Outbox {
		if r, ok := results[o.key()]; ok {
			if r.done {
				continue
			}
			o = r.next
		}
		if seen[o.key()] {
			continue // a converted entry met its twin
		}
		seen[o.key()] = true
		kept = append(kept, o)
	}
	w.st.Outbox = kept
	for _, k := range settle {
		if !slices.Contains(w.st.Settled, k) {
			w.st.Settled = append(w.st.Settled, k)
		}
	}
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
		// The org has not published one, or removed it: an applied policy
		// is marked withdrawn and its overlay dropped (nothing happens when
		// none was applied).
		d.hubPolicyWithdrawn(ctx, snap.h)
	case d.hubRevokedIf(ctx, err):
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
		out.LastSync = w.lastSync
		out.FindingsHealth = slices.Clone(w.health)
		out.OfferSkips = slices.Clone(w.skips)
		out.LegacyHub = now.Before(w.legacyUntil)
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
	// Membership in the same words as `lola hub status` (hub.Status): a
	// revocation record outlives the identity it replaced until a new join.
	ms := hub.Status{Joined: out.Joined}
	if r, ok := hub.LoadRevocation(d.hubDir()); ok && !out.Joined {
		ms.Revoked = &r
	}
	out.Membership = ms.Membership()
	for _, s := range d.sessions.Snapshot() {
		for _, w := range s.HubWorks() {
			summary := w.Label
			if summary == "" {
				summary = hubSessionLabel(s)
			}
			out.Sessions = append(out.Sessions, protocol.HubSessionInfo{
				Session: s.ID, Project: s.Project, ItemID: w.ItemID, Kind: w.Kind,
				ClaimedAt: w.ClaimedAt, LeaseExpires: w.LeaseExpires,
				LeaseLost: w.LeaseLost, LeaseTaken: w.LeaseTaken, InReview: w.InReview, Progress: w.Progress,
				Released: w.Released, Reported: w.Reported,
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
		LeaseLost: s.HubItem.LeaseLost, LeaseTaken: s.HubItem.LeaseTaken,
		InReview: s.HubItem.InReview, Released: s.HubItem.Released,
	}
}
