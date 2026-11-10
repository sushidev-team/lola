package daemon

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/state"
)

// sharedHub is ONE org's chore lane as two daemons see it: a minimal model of
// lola-hub's lease rules (claim an open item, renew by heartbeat, a lapsed
// lease reopens, only the holder may finish). lola-hub's own e2e test runs the
// same two-member scenario against the real server; this one runs it against
// two real daemon loops, which cannot import the hub module.
type sharedHub struct {
	mu    sync.Mutex
	now   time.Time
	ttl   time.Duration
	items map[string]*sharedItem
	order []string
}

type sharedItem struct {
	item     hubproto.Item
	holder   string
	expires  time.Time
	attempts int
	done     bool
}

func newSharedHub(items ...hubproto.Item) *sharedHub {
	h := &sharedHub{now: time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC), ttl: 30 * time.Minute, items: map[string]*sharedItem{}}
	for _, it := range items {
		h.items[it.ID] = &sharedItem{item: it}
		h.order = append(h.order, it.ID)
	}
	return h
}

func (h *sharedHub) advance(d time.Duration) { h.mu.Lock(); h.now = h.now.Add(d); h.mu.Unlock() }

// expireLocked reopens every lapsed lease, as the hub does on each request.
func (h *sharedHub) expireLocked() {
	for _, w := range h.items {
		if w.holder != "" && !h.now.Before(w.expires) {
			w.holder = ""
		}
	}
}

func (h *sharedHub) get(id string) sharedItem {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expireLocked()
	return *h.items[id]
}

// member is one device's view of the shared hub: the daemon's hubAPI.
type member struct {
	h      *sharedHub
	device string
	// staleOffers lists every unfinished item, held or not: an offer list read
	// just before another member's claim landed.
	staleOffers bool
	claims      []string
	fails       []hubproto.FailReason
	completes   int
}

var _ hubAPI = (*member)(nil)

func (m *member) Heartbeat(_ context.Context, hb hubproto.HeartbeatRequest) (hubproto.HeartbeatResponse, error) {
	m.h.mu.Lock()
	defer m.h.mu.Unlock()
	m.h.expireLocked()
	var resp hubproto.HeartbeatResponse
	for _, id := range hb.LiveLeases {
		w := m.h.items[id]
		if w == nil || w.holder != m.device {
			resp.Lost = append(resp.Lost, id)
			continue
		}
		w.expires = m.h.now.Add(m.h.ttl)
		resp.Renewed = append(resp.Renewed, hubproto.Lease{ItemID: id, Device: m.device, Expires: w.expires})
	}
	return resp, nil
}

func (m *member) ReportFindings(_ context.Context, items []hubproto.Item) (hubproto.FindingsResponse, error) {
	return hubproto.FindingsResponse{Accepted: len(items)}, nil
}

func (m *member) Offers(context.Context) ([]hubproto.Item, int, error) {
	m.h.mu.Lock()
	defer m.h.mu.Unlock()
	m.h.expireLocked()
	var out []hubproto.Item
	for _, id := range m.h.order {
		w := m.h.items[id]
		if !w.done && (w.holder == "" || m.staleOffers) {
			out = append(out, w.item)
		}
	}
	return out, 0, nil
}

func (m *member) Claim(_ context.Context, id string) (hubproto.Lease, error) {
	m.h.mu.Lock()
	defer m.h.mu.Unlock()
	m.h.expireLocked()
	m.claims = append(m.claims, id)
	w := m.h.items[id]
	if w == nil || w.done || w.holder != "" {
		return hubproto.Lease{}, &hub.APIError{Status: 409, Code: hubproto.CodeConflict}
	}
	w.holder, w.expires = m.device, m.h.now.Add(m.h.ttl)
	return hubproto.Lease{ItemID: id, Device: m.device, Expires: w.expires}, nil
}

// holdsLocked is the hub's "only the lease holder may finish" rule.
func (m *member) holdsLocked(id string) (*sharedItem, error) {
	m.h.expireLocked()
	w := m.h.items[id]
	if w == nil || w.holder != m.device {
		return nil, &hub.APIError{Status: 409, Code: hubproto.CodeLeaseLost}
	}
	return w, nil
}

func (m *member) Complete(_ context.Context, id string, _ hubproto.CompleteRequest) error {
	m.h.mu.Lock()
	defer m.h.mu.Unlock()
	w, err := m.holdsLocked(id)
	if err != nil {
		return err
	}
	m.completes++
	w.holder, w.done = "", true
	return nil
}

func (m *member) Fail(_ context.Context, id string, r hubproto.FailReason) error {
	m.h.mu.Lock()
	defer m.h.mu.Unlock()
	w, err := m.holdsLocked(id)
	if err != nil {
		return err
	}
	m.fails = append(m.fails, r)
	w.holder = ""
	w.attempts++
	return nil
}

func (m *member) Release(_ context.Context, id string) error {
	m.h.mu.Lock()
	defer m.h.mu.Unlock()
	w, err := m.holdsLocked(id)
	if err != nil {
		return err
	}
	w.holder = ""
	return nil
}

func (m *member) FetchPolicy(context.Context, config.HubConfig, string, string, time.Time) (hub.StoredPolicy, error) {
	return hub.StoredPolicy{}, &hub.APIError{Status: 404, Code: hubproto.CodeNoPolicy}
}

func (m *member) snapshot() (claims []string, fails []hubproto.FailReason, completes int) {
	m.h.mu.Lock()
	defer m.h.mu.Unlock()
	return slices.Clone(m.claims), slices.Clone(m.fails), m.completes
}

// memberDaemon is hubFixture wired to one member of a shared hub.
func memberDaemon(t *testing.T, h *sharedHub, device string) (*Daemon, *member, *fakeNative) {
	t.Helper()
	d, _, nat := hubFixture(t)
	m := &member{h: h, device: device}
	d.hub.newClient = func(url, _ string) (hubAPI, hub.Identity, error) {
		return m, hub.Identity{URL: url, DeviceID: device, Org: "acme"}, nil
	}
	return d, m, nat
}

// TestHubTwoDaemonsShareOneLease runs two daemon loops against one org's
// lease rules: a member that loses the claim race spawns nothing and is not
// charged for it, a lapsed lease moves the item to the other member, the
// member that lost it keeps its session (never killed) but stops renewing and
// reports nothing at teardown, and only the holder's outcome reaches the hub.
func TestHubTwoDaemonsShareOneLease(t *testing.T) {
	h := newSharedHub(advisoryItem("it1"))
	a, ma, natA := memberDaemon(t, h, "dev-a")
	b, mb, natB := memberDaemon(t, h, "dev-b")
	ctx := context.Background()

	// A claims and spawns.
	a.hubCycle(ctx)
	if len(natA.manualAgents) != 1 || h.get("it1").holder != "dev-a" {
		t.Fatalf("A did not take it1: spawns=%d holder=%q", len(natA.manualAgents), h.get("it1").holder)
	}

	// B read its offers before A's claim landed: the claim is refused, B
	// spawns nothing and its daily claim count is refunded.
	mb.staleOffers = true
	b.hubCycle(ctx)
	if claims, _, _ := mb.snapshot(); !slices.Equal(claims, []string{"it1"}) {
		t.Fatalf("B claims = %v", claims)
	}
	if len(natB.manualAgents) != 0 || len(hubSessions(b)) != 0 {
		t.Fatal("B spawned an item A holds")
	}
	if n := b.hubClaimsToday(time.Now()); n != 0 {
		t.Errorf("B charged for a lost race: %d", n)
	}
	mb.staleOffers = false

	// A keeps renewing while its heartbeats arrive.
	h.advance(20 * time.Minute)
	a.hubCycle(ctx)
	h.advance(20 * time.Minute)
	if w := h.get("it1"); w.holder != "dev-a" {
		t.Fatalf("A's renewed lease lapsed early: %+v", w)
	}

	// A goes silent past its lease: the item reopens and B takes it.
	h.advance(31 * time.Minute)
	b.hubCycle(ctx)
	if len(natB.manualAgents) != 1 || h.get("it1").holder != "dev-b" {
		t.Fatalf("B did not take the lapsed item: spawns=%d holder=%q", len(natB.manualAgents), h.get("it1").holder)
	}

	// A learns it lost the lease: session kept and marked, never killed.
	a.hubCycle(ctx)
	sa := hubSessions(a)
	if len(sa) != 1 || !sa[0].HubItem.LeaseLost {
		t.Fatalf("A's session = %+v", sa)
	}
	if len(natA.killCalls()) != 0 {
		t.Fatal("a lost lease killed A's session")
	}

	// A's teardown reports nothing for an item B now holds.
	a.dropSession(sa[0])
	a.hubOutcomeCycle(ctx)
	if _, fails, completes := ma.snapshot(); len(fails)+completes != 0 {
		t.Fatalf("A reported an outcome for B's item: fails=%v completes=%d", fails, completes)
	}
	if w := h.get("it1"); w.holder != "dev-b" || w.attempts != 0 {
		t.Fatalf("A's teardown touched B's lease: %+v", w)
	}

	// B's agent ends without a PR: ONE failure, from the holder, and the item
	// reopens with the attempt counted.
	sb := hubSessions(b)[0]
	sb.EnsureAxes()
	sb.SetAgentState(state.AgentDead, "", time.Now())
	b.dropSession(sb)
	b.hubOutcomeCycle(ctx)
	b.hubOutcomeCycle(ctx)
	if _, fails, _ := mb.snapshot(); !slices.Equal(fails, []hubproto.FailReason{hubproto.FailAgent}) {
		t.Fatalf("B fails = %v", fails)
	}
	if w := h.get("it1"); w.holder != "" || w.attempts != 1 || w.done {
		t.Fatalf("after B's failure: %+v", w)
	}
}
