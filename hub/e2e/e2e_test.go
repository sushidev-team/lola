// Package e2e drives the whole hub — the real HTTP handler, the real file
// store on disk and the real escalator — with two simulated lola daemons that
// sign every request through hubproto, exactly as a daemon does. Nothing
// leaves the process: the server is an httptest listener, and Linear is a fake
// Creator that counts what it was asked to file.
package e2e

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/api"
	"github.com/sushidev-team/lola/hub/internal/escalate"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fakeLinear records every issue it is asked to create.
type fakeLinear struct {
	mu      sync.Mutex
	created map[string]escalate.Issue // issue id -> issue
	calls   int
}

func (f *fakeLinear) CreateIssue(_ context.Context, _, issueID string, iss escalate.Issue) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.created == nil {
		f.created = map[string]escalate.Issue{}
	}
	f.created[issueID] = iss
	return fmt.Sprintf("OPS-%d", f.calls), nil
}

func (f *fakeLinear) FindIssue(_ context.Context, issueID string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.created[issueID]
	return "", ok, nil
}

func (f *fakeLinear) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

type hubEnv struct {
	t      *testing.T
	path   string
	st     *store.File
	clk    *clock
	srv    *httptest.Server
	linear *fakeLinear
	esc    *escalate.Escalator
	nonce  int
}

func newHub(t *testing.T) *hubEnv {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hub.json")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)}
	srv := httptest.NewServer(api.New(api.Config{Store: st, Now: clk.Now, LeaseTTL: 30 * time.Minute}).Handler())
	t.Cleanup(srv.Close)
	// `lola-hub admin org create acme` + `admin org linear acme --team T`.
	if err := st.Update(func(d *store.Data) error {
		d.Orgs["acme"] = &store.Org{Name: "acme", LinearTeam: "team-ops"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lin := &fakeLinear{}
	return &hubEnv{
		t: t, path: path, st: st, clk: clk, srv: srv, linear: lin,
		esc: &escalate.Escalator{Store: st, Creator: lin, Now: clk.Now},
	}
}

// enrollToken is `lola-hub admin enroll create --org`.
func (h *hubEnv) enrollToken(org string) string {
	h.t.Helper()
	tok, err := store.NewToken()
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.st.Update(func(d *store.Data) error {
		d.Tokens[store.HashToken(tok)] = &store.EnrollToken{
			Hash: store.HashToken(tok), Org: org, CreatedAt: h.clk.Now(), ExpiresAt: h.clk.Now().Add(24 * time.Hour),
		}
		return nil
	}); err != nil {
		h.t.Fatal(err)
	}
	return tok
}

func (h *hubEnv) item(id string) store.WorkItem {
	var w store.WorkItem
	_ = h.st.View(func(d *store.Data) error {
		if p := d.Items[id]; p != nil {
			w = *p
		}
		return nil
	})
	return w
}

// escalateTwice drains the escalation queue twice: a second pass must never
// file a second issue for the same item.
func (h *hubEnv) escalateTwice() {
	h.esc.RunOnce(context.Background())
	h.esc.RunOnce(context.Background())
}

// device is one simulated lola daemon.
type device struct {
	h    *hubEnv
	name string
	id   string
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func (h *hubEnv) join(name string) *device {
	h.t.Helper()
	pub, priv, err := hubproto.GenerateKey()
	if err != nil {
		h.t.Fatal(err)
	}
	d := &device{h: h, name: name, id: hubproto.DeviceID(pub), priv: priv, pub: pub}
	var out hubproto.EnrollResponse
	d.must(http.StatusOK, http.MethodPost, hubproto.PathEnroll, hubproto.EnrollRequest{
		Token: h.enrollToken("acme"), DeviceName: name, PublicKey: hubproto.EncodePublicKey(pub),
	}, &out)
	if out.DeviceID != d.id || out.Org != "acme" {
		h.t.Fatalf("enroll %s: %+v", name, out)
	}
	return d
}

// call signs and sends one request and returns the status and error code.
func (d *device) call(method, path string, payload, out any) (int, string) {
	d.h.t.Helper()
	var body []byte
	if payload != nil {
		var err error
		if body, err = json.Marshal(payload); err != nil {
			d.h.t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, d.h.srv.URL+path, bytes.NewReader(body))
	if err != nil {
		d.h.t.Fatal(err)
	}
	d.h.nonce++
	if err := hubproto.SignAt(req, d.id, d.priv, body, d.h.clk.Now(), fmt.Sprintf("e2e-nonce-%08d", d.h.nonce)); err != nil {
		d.h.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		d.h.t.Fatal(err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var er hubproto.ErrorResponse
		_ = json.Unmarshal(rb, &er)
		return resp.StatusCode, er.Code
	}
	if out != nil {
		if err := json.Unmarshal(rb, out); err != nil {
			d.h.t.Fatalf("%s %s: decode %s: %v", method, path, rb, err)
		}
	}
	return resp.StatusCode, ""
}

func (d *device) must(status int, method, path string, payload, out any) {
	d.h.t.Helper()
	if got, code := d.call(method, path, payload, out); got != status {
		d.h.t.Fatalf("%s %s %s: got %d %s, want %d", d.name, method, path, got, code, status)
	}
}

func (d *device) heartbeat(leases ...string) hubproto.HeartbeatResponse {
	d.h.t.Helper()
	var out hubproto.HeartbeatResponse
	d.must(http.StatusOK, http.MethodPost, hubproto.PathHeartbeat, hubproto.HeartbeatRequest{
		FreeSlots: 2, Agents: []string{"claude"}, Repos: []string{"acme/web"}, LiveLeases: leases,
	}, &out)
	return out
}

func (d *device) report(items ...hubproto.Item) hubproto.FindingsResponse {
	d.h.t.Helper()
	var out hubproto.FindingsResponse
	d.must(http.StatusOK, http.MethodPost, hubproto.PathFindings, hubproto.FindingsRequest{Items: items}, &out)
	return out
}

func (d *device) offers() []hubproto.Item {
	d.h.t.Helper()
	var out hubproto.OffersResponse
	d.must(http.StatusOK, http.MethodGet, hubproto.PathOffers, nil, &out)
	for _, it := range out.Items {
		// A daemon validates every offer before it may claim it.
		if err := hubproto.Validate(it); err != nil {
			d.h.t.Fatalf("offer does not validate: %v", err)
		}
	}
	return out.Items
}

func (d *device) claim(id string) (int, string) {
	return d.call(http.MethodPost, hubproto.ClaimPath(id), nil, nil)
}

func (d *device) fail(id string, reason hubproto.FailReason) (int, string) {
	return d.call(http.MethodPost, hubproto.FailPath(id), hubproto.FailRequest{Reason: reason}, nil)
}

func (d *device) progress(id string) (int, string) {
	return d.call(http.MethodPost, hubproto.ProgressPath(id), hubproto.ProgressRequest{
		PRURL: "https://github.com/acme/web/pull/42", Delivery: hubproto.DeliveryCIPending,
	}, nil)
}

func (d *device) board(state hubproto.State) []hubproto.ItemView {
	d.h.t.Helper()
	var out hubproto.BoardResponse
	d.must(http.StatusOK, http.MethodGet, hubproto.BoardPath(state, 0), nil, &out)
	if err := out.Validate(); err != nil {
		d.h.t.Fatalf("board does not validate: %v", err)
	}
	return out.Items
}

func (d *device) complete(id string) (int, string) {
	return d.call(http.MethodPost, hubproto.CompletePath(id), hubproto.CompleteRequest{
		PRURL: "https://github.com/acme/web/pull/42", Delivery: hubproto.DeliveryCIPending,
	}, nil)
}

func advisory(pkg, ghsa string, sev hubproto.Severity) hubproto.Item {
	return hubproto.Item{Kind: hubproto.KindDepAdvisory, DepAdvisory: &hubproto.DepAdvisory{
		Repo: "acme/web", Ecosystem: "npm", Package: pkg, Vulnerable: "< 4.17.21",
		Fixed: "4.17.21", Advisory: ghsa, Severity: sev,
	}}
}

func onlyOffer(t *testing.T, items []hubproto.Item) string {
	t.Helper()
	if len(items) != 1 {
		t.Fatalf("want exactly one offer, got %d: %+v", len(items), items)
	}
	return items[0].ID
}

// TestTwoDaemonsOneOrg is the chore lane end to end: dedup across members, a
// lease nobody else can touch, failures that escalate exactly once, a
// critical advisory that escalates on sight, and a lapsed lease that hands
// the item to the other member.
func TestTwoDaemonsOneOrg(t *testing.T) {
	h := newHub(t)
	a, b := h.join("dev-a"), h.join("dev-b")
	a.heartbeat()
	b.heartbeat()

	// Both members' daemons see the same Dependabot alert: ONE item.
	high := advisory("lodash", "GHSA-jf85-cpcp-j695", hubproto.SeverityHigh)
	if r := a.report(high); r.Accepted != 1 || len(r.Rejected) != 0 {
		t.Fatalf("A report: %+v", r)
	}
	if r := b.report(high); r.Accepted != 1 || len(r.Rejected) != 0 {
		t.Fatalf("B report: %+v", r)
	}
	id := onlyOffer(t, a.offers())
	if bid := onlyOffer(t, b.offers()); bid != id {
		t.Fatalf("A and B were offered different items: %s vs %s", id, bid)
	}
	if w := h.item(id); w.Reporters != 2 {
		t.Fatalf("reporters = %d, want 2", w.Reporters)
	}

	// A claims; B can neither claim nor finish it, nor fail or release it.
	if st, code := a.claim(id); st != http.StatusOK {
		t.Fatalf("A claim: %d %s", st, code)
	}
	if st, code := b.claim(id); st != http.StatusConflict || code != hubproto.CodeConflict {
		t.Fatalf("B claim of A's item: %d %s", st, code)
	}
	if st, code := b.complete(id); st != http.StatusConflict || code != hubproto.CodeLeaseLost {
		t.Fatalf("B complete of A's item: %d %s", st, code)
	}
	if st, _ := b.fail(id, hubproto.FailAgent); st == http.StatusOK {
		t.Fatal("B failed A's item")
	}
	if st, _ := b.call(http.MethodPost, hubproto.ReleasePath(id), nil, nil); st == http.StatusOK {
		t.Fatal("B released A's item")
	}
	if len(b.offers()) != 0 {
		t.Fatal("a claimed item is still offered to B")
	}
	if w := h.item(id); w.State != hubproto.StateClaimed || w.Attempts != 0 {
		t.Fatalf("B's attempts changed the item: %+v", w)
	}

	// A fails twice (escalate_after defaults to 2): escalated, exactly one
	// Linear issue however often the escalator runs.
	if st, code := a.fail(id, hubproto.FailCI); st != http.StatusOK {
		t.Fatalf("A fail #1: %d %s", st, code)
	}
	if w := h.item(id); w.State != hubproto.StateOpen || w.Attempts != 1 {
		t.Fatalf("after one failure: %+v", w)
	}
	h.escalateTwice()
	if n := h.linear.count(); n != 0 {
		t.Fatalf("escalated after one failure: %d issue(s)", n)
	}
	if st, code := a.claim(id); st != http.StatusOK {
		t.Fatalf("A re-claim: %d %s", st, code)
	}
	if st, code := a.fail(id, hubproto.FailAgent); st != http.StatusOK {
		t.Fatalf("A fail #2: %d %s", st, code)
	}
	if w := h.item(id); w.State != hubproto.StateEscalated || w.Escalation == nil {
		t.Fatalf("after two failures: %+v", w)
	}
	h.escalateTwice()
	h.clk.Add(48 * time.Hour) // past every retry backoff
	h.escalateTwice()
	if n := h.linear.count(); n != 1 {
		t.Fatalf("Linear issues for the failed item = %d, want 1", n)
	}
	if w := h.item(id); w.Escalation.Pending || w.Escalation.LinearID != "OPS-1" {
		t.Fatalf("escalation not recorded: %+v", w.Escalation)
	}
	if len(a.offers()) != 0 || len(b.offers()) != 0 {
		t.Fatal("an escalated item is still offered")
	}

	// A critical advisory escalates as soon as it is seen and stays on offer.
	crit := advisory("minimist", "GHSA-xvch-5gv4-984h", hubproto.SeverityCritical)
	b.report(crit)
	cid := onlyOffer(t, b.offers())
	if w := h.item(cid); w.Escalation == nil || !w.Escalation.Pending {
		t.Fatalf("critical not escalated on sight: %+v", w)
	}
	h.escalateTwice()
	if n := h.linear.count(); n != 2 {
		t.Fatalf("Linear issues after the critical = %d, want 2", n)
	}

	// A claims the critical one and goes silent: once the lease lapses the
	// item is held for A's grace window (2 x TTL), then reopens for B without
	// counting an attempt, and A's lease is gone.
	if st, code := a.claim(cid); st != http.StatusOK {
		t.Fatalf("A claim critical: %d %s", st, code)
	}
	if st, _ := b.claim(cid); st != http.StatusConflict {
		t.Fatalf("B claimed a live lease: %d", st)
	}
	h.clk.Add(20 * time.Minute)
	if hb := a.heartbeat(cid); len(hb.Renewed) != 1 {
		t.Fatalf("A renew: %+v", hb)
	}
	h.clk.Add(31 * time.Minute) // past the renewed 30m lease, inside the grace
	if len(b.offers()) != 0 {
		t.Fatal("a lapsed lease was offered to B inside A's grace window")
	}
	if st, _ := b.claim(cid); st != http.StatusConflict {
		t.Fatalf("B claimed inside A's grace window: %d", st)
	}
	h.clk.Add(time.Hour) // past the grace window
	if got := onlyOffer(t, b.offers()); got != cid {
		t.Fatalf("lapsed lease not reoffered to B: %s", got)
	}
	if st, code := b.claim(cid); st != http.StatusOK {
		t.Fatalf("B claim after lapse: %d %s", st, code)
	}
	if hb := a.heartbeat(cid); len(hb.Lost) != 1 || hb.Lost[0] != cid {
		t.Fatalf("A must learn it lost the lease: %+v", hb)
	}
	if st, code := a.complete(cid); st != http.StatusConflict || code != hubproto.CodeLeaseLost {
		t.Fatalf("A completed B's item: %d %s", st, code)
	}
	if st, code := b.complete(cid); st != http.StatusOK {
		t.Fatalf("B complete: %d %s", st, code)
	}
	if w := h.item(cid); w.State != hubproto.StateDone || w.Attempts != 0 || w.Outcome == nil {
		t.Fatalf("critical item after B: %+v", w)
	}
	if n := h.linear.count(); n != 2 {
		t.Fatalf("the lapse filed another issue: %d", n)
	}

	// Everything above survived on disk: a second process (the admin CLI)
	// opening the same file sees the same items.
	again, err := store.Open(h.path)
	if err != nil {
		t.Fatal(err)
	}
	var states []hubproto.State
	_ = again.View(func(d *store.Data) error {
		for _, k := range []string{id, cid} {
			if w := d.Items[k]; w != nil {
				states = append(states, w.State)
			}
		}
		return nil
	})
	if len(states) != 2 || states[0] != hubproto.StateEscalated || states[1] != hubproto.StateDone {
		t.Fatalf("on-disk states = %v", states)
	}
}

// TestItemLifecycle walks the newer lifecycle end to end: a PR moves an item
// to in_review where nobody is offered it; a laptop that slept through its
// lease gets the item back inside the grace window; and a finding nobody
// reports any more closes as stale while a member is still active.
func TestItemLifecycle(t *testing.T) {
	h := newHub(t)
	a, b := h.join("dev-a"), h.join("dev-b")
	a.heartbeat()
	b.heartbeat()

	// PR opened -> in_review, not re-offered, not re-opened by re-reports.
	high := advisory("lodash", "GHSA-jf85-cpcp-j695", hubproto.SeverityHigh)
	a.report(high)
	id := onlyOffer(t, a.offers())
	if st, code := a.claim(id); st != http.StatusOK {
		t.Fatalf("A claim: %d %s", st, code)
	}
	if st, code := a.progress(id); st != http.StatusOK {
		t.Fatalf("A progress: %d %s", st, code)
	}
	b.report(high)
	if len(a.offers()) != 0 || len(b.offers()) != 0 {
		t.Fatal("an in_review item is offered")
	}
	if st, _ := b.claim(id); st != http.StatusConflict {
		t.Fatalf("B claimed an in_review item: %d", st)
	}
	rows := b.board(hubproto.StateInReview)
	if len(rows) != 1 || rows[0].Owner == nil || rows[0].Owner.DeviceName != "dev-a" || rows[0].Owner.Mine {
		t.Fatalf("in_review row for B: %+v", rows)
	}
	if st, code := a.complete(id); st != http.StatusOK {
		t.Fatalf("A complete from in_review: %d %s", st, code)
	}

	// Lease expiry, then the previous holder re-claims inside its grace.
	mod := advisory("minimist", "GHSA-xvch-5gv4-984h", hubproto.SeverityModerate)
	b.report(mod)
	mid := onlyOffer(t, b.offers())
	if st, code := b.claim(mid); st != http.StatusOK {
		t.Fatalf("B claim: %d %s", st, code)
	}
	h.clk.Add(45 * time.Minute) // lease lapsed 15m ago; grace is 60m
	a.heartbeat()
	if len(a.offers()) != 0 {
		t.Fatal("A was offered B's item inside B's grace window")
	}
	if st, code := b.claim(mid); st != http.StatusOK {
		t.Fatalf("B re-claim inside grace: %d %s", st, code)
	}
	if w := h.item(mid); w.State != hubproto.StateClaimed || w.Attempts != 0 || w.Lease.Device != b.id {
		t.Fatalf("after re-claim: %+v", w)
	}
	if st, code := b.complete(mid); st != http.StatusOK {
		t.Fatalf("B complete: %d %s", st, code)
	}

	// A stale finding: reported once, never again, while A stays active.
	stale := advisory("axios", "GHSA-wf5p-g6vw-rhxx", hubproto.SeverityLow)
	a.report(stale)
	sid := onlyOffer(t, a.offers())
	srv := api.New(api.Config{Store: h.st, Now: h.clk.Now, LeaseTTL: 30 * time.Minute})
	h.clk.Add(hubproto.DefaultStaleAfter)
	a.heartbeat()
	if _, err := srv.Sweep(); err != nil {
		t.Fatal(err)
	}
	w := h.item(sid)
	if w.State != hubproto.StateFailed || len(w.History) == 0 || w.History[len(w.History)-1].Action != hubproto.AttemptClosedStale {
		t.Fatalf("stale item: %+v", w)
	}
	if len(a.offers()) != 0 {
		t.Fatal("a stale-closed item is offered")
	}
}
