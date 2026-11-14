package api

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/lease"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// clock is a settable test clock.
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

type env struct {
	t     *testing.T
	st    *store.File
	clk   *clock
	srv   *httptest.Server
	hub   *Server
	nonce int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open("")
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)}
	hub := New(Config{Store: st, Now: clk.Now, LeaseTTL: 30 * time.Minute})
	srv := httptest.NewServer(hub.Handler())
	t.Cleanup(srv.Close)
	if err := st.Update(func(d *store.Data) error {
		d.Orgs["acme"] = &store.Org{Name: "acme"}
		d.Orgs["beta"] = &store.Org{Name: "beta"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return &env{t: t, st: st, clk: clk, srv: srv, hub: hub}
}

func (e *env) token(org string, ttl time.Duration) string {
	tok, _ := store.NewToken()
	if err := e.st.Update(func(d *store.Data) error {
		d.Tokens[store.HashToken(tok)] = &store.EnrollToken{Hash: store.HashToken(tok), Org: org, CreatedAt: e.clk.Now(), ExpiresAt: e.clk.Now().Add(ttl)}
		return nil
	}); err != nil {
		e.t.Fatal(err)
	}
	return tok
}

// daemon simulates a lola daemon signing with hubproto.
type daemon struct {
	e    *env
	id   string
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func (e *env) newDaemon() *daemon {
	pub, priv, err := hubproto.GenerateKey()
	if err != nil {
		e.t.Fatal(err)
	}
	return &daemon{e: e, id: hubproto.DeviceID(pub), pub: pub, priv: priv}
}

type result struct {
	status int
	body   []byte
	code   string
}

func (r result) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

func (e *env) nextNonce() string {
	e.nonce++
	return fmt.Sprintf("testnonce-%08d", e.nonce)
}

// raw sends a signed request with full control over clock skew and nonce.
func (d *daemon) raw(method, path string, payload any, at time.Time, nonce string) result {
	var body []byte
	if payload != nil {
		var err error
		if body, err = json.Marshal(payload); err != nil {
			d.e.t.Fatal(err)
		}
	}
	req, _ := http.NewRequest(method, d.e.srv.URL+path, bytes.NewReader(body))
	if body == nil {
		req.Body = http.NoBody
	}
	if err := hubproto.SignAt(req, d.id, d.priv, body, at, nonce); err != nil {
		d.e.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		d.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	var er hubproto.ErrorResponse
	_ = json.Unmarshal(rb, &er)
	return result{status: resp.StatusCode, body: rb, code: er.Code}
}

func (d *daemon) do(method, path string, payload any) result {
	return d.raw(method, path, payload, d.e.clk.Now(), d.e.nextNonce())
}

func (d *daemon) enroll(tok string) result {
	return d.do(http.MethodPost, hubproto.PathEnroll, hubproto.EnrollRequest{
		Token: tok, DeviceName: "laptop", PublicKey: hubproto.EncodePublicKey(d.pub),
	})
}

func (d *daemon) mustEnroll(org string) {
	d.e.t.Helper()
	if r := d.enroll(d.e.token(org, time.Hour)); r.status != 200 {
		d.e.t.Fatalf("enroll: %d %s", r.status, r.body)
	}
}

func (d *daemon) heartbeat(repos []string, leases ...string) hubproto.HeartbeatResponse {
	d.e.t.Helper()
	r := d.do(http.MethodPost, hubproto.PathHeartbeat, hubproto.HeartbeatRequest{
		FreeSlots: 2, Agents: []string{"claude"}, Repos: repos, LiveLeases: leases,
	})
	if r.status != 200 {
		d.e.t.Fatalf("heartbeat: %d %s", r.status, r.body)
	}
	var out hubproto.HeartbeatResponse
	r.decode(d.e.t, &out)
	return out
}

func (d *daemon) offers(query string) []hubproto.Item {
	d.e.t.Helper()
	r := d.do(http.MethodGet, hubproto.PathOffers+query, nil)
	if r.status != 200 {
		d.e.t.Fatalf("offers: %d %s", r.status, r.body)
	}
	var out hubproto.OffersResponse
	r.decode(d.e.t, &out)
	return out.Items
}

func advisory(repo string, sev hubproto.Severity) hubproto.Item {
	return hubproto.Item{Kind: hubproto.KindDepAdvisory, DepAdvisory: &hubproto.DepAdvisory{
		Repo: repo, Ecosystem: "npm", Package: "lodash", Vulnerable: "< 4.17.21",
		Fixed: "4.17.21", Advisory: "GHSA-jf85-cpcp-j695", Severity: sev,
	}}
}

func redBump(repo string, pr int) hubproto.Item {
	return hubproto.Item{Kind: hubproto.KindRedBumpPR, RedBumpPR: &hubproto.RedBumpPR{
		Repo: repo, PR: pr, Branch: "dependabot/npm/lodash-4.17.21", Package: "lodash", Author: hubproto.AuthorDependabot,
	}}
}

func (d *daemon) report(items ...hubproto.Item) hubproto.FindingsResponse {
	d.e.t.Helper()
	r := d.do(http.MethodPost, hubproto.PathFindings, hubproto.FindingsRequest{Items: items})
	if r.status != 200 {
		d.e.t.Fatalf("findings: %d %s", r.status, r.body)
	}
	var out hubproto.FindingsResponse
	r.decode(d.e.t, &out)
	return out
}

func (e *env) item(id string) store.WorkItem {
	var w store.WorkItem
	_ = e.st.View(func(d *store.Data) error {
		if p := d.Items[id]; p != nil {
			w = *p
		}
		return nil
	})
	return w
}

// ---- the happy path -------------------------------------------------------

func TestFullFlow(t *testing.T) {
	e := newEnv(t)
	a := e.newDaemon()
	a.mustEnroll("acme")

	hb := a.heartbeat([]string{"acme/web"})
	if len(hb.Renewed) != 0 || len(hb.Lost) != 0 {
		t.Fatalf("heartbeat %+v", hb)
	}
	fr := a.report(advisory("acme/web", hubproto.SeverityHigh), redBump("acme/web", 9))
	if fr.Accepted != 2 || len(fr.Rejected) != 0 {
		t.Fatalf("findings %+v", fr)
	}
	offers := a.offers("")
	if len(offers) != 2 || offers[0].Kind != hubproto.KindDepAdvisory {
		t.Fatalf("offers %+v", offers)
	}
	for _, it := range offers {
		if err := hubproto.Validate(it); err != nil || it.ID == "" {
			t.Fatalf("offer invalid: %v", err)
		}
	}
	if got := a.offers("?kind=red_bump_pr"); len(got) != 1 || got[0].Kind != hubproto.KindRedBumpPR {
		t.Fatalf("kind filter %+v", got)
	}

	id := offers[0].ID
	r := a.do(http.MethodPost, hubproto.ClaimPath(id), nil)
	if r.status != 200 {
		t.Fatalf("claim %d %s", r.status, r.body)
	}
	var l hubproto.Lease
	r.decode(t, &l)
	if l.ItemID != id || l.Device != a.id || l.Validate() != nil {
		t.Fatalf("lease %+v", l)
	}
	if got := a.offers(""); len(got) != 1 {
		t.Fatal("claimed item still offered")
	}

	e.clk.Add(20 * time.Minute)
	hb = a.heartbeat([]string{"acme/web"}, id, "inosuchitem")
	if len(hb.Renewed) != 1 || !hb.Renewed[0].Expires.Equal(e.clk.Now().Add(30*time.Minute)) || len(hb.Lost) != 1 || hb.Lost[0] != "inosuchitem" {
		t.Fatalf("renew %+v", hb)
	}

	r = a.do(http.MethodPost, hubproto.CompletePath(id), hubproto.CompleteRequest{
		PRURL: "https://github.com/acme/web/pull/77", Delivery: hubproto.DeliveryCIPending,
	})
	if r.status != 200 {
		t.Fatalf("complete %d %s", r.status, r.body)
	}
	w := e.item(id)
	if w.State != hubproto.StateDone || w.Outcome == nil || w.Outcome.PRURL != "https://github.com/acme/web/pull/77" {
		t.Fatalf("item %+v", w)
	}
	// A re-report inside 24h keeps it done; after 24h it reopens.
	a.report(advisory("acme/web", hubproto.SeverityHigh))
	if e.item(id).State != hubproto.StateDone {
		t.Fatal("reopened too soon")
	}
	e.clk.Add(25 * time.Hour)
	a.report(advisory("acme/web", hubproto.SeverityHigh))
	if w := e.item(id); w.State != hubproto.StateOpen || w.Attempts != 0 || w.Reopened != 1 {
		t.Fatalf("not reopened: %+v", w)
	}
}

func TestDedupAcrossDevices(t *testing.T) {
	e := newEnv(t)
	a, b := e.newDaemon(), e.newDaemon()
	a.mustEnroll("acme")
	b.mustEnroll("acme")
	a.heartbeat([]string{"acme/web"})
	b.heartbeat([]string{"ACME/web"})
	a.report(advisory("acme/web", hubproto.SeverityHigh))
	b.report(advisory("Acme/Web", hubproto.SeverityHigh))
	var n, reporters int
	_ = e.st.View(func(d *store.Data) error {
		for _, w := range d.Items {
			n++
			reporters = w.Reporters
		}
		return nil
	})
	if n != 1 || reporters != 2 {
		t.Fatalf("items=%d reporters=%d", n, reporters)
	}
}

// ---- rejections -----------------------------------------------------------

func TestEnrollRejections(t *testing.T) {
	e := newEnv(t)
	a := e.newDaemon()
	tok := e.token("acme", time.Hour)
	if r := a.enroll(tok); r.status != 200 {
		t.Fatalf("enroll %d", r.status)
	}
	// Single use.
	b := e.newDaemon()
	if r := b.enroll(tok); r.status != 401 || r.code != hubproto.CodeUnauthorized {
		t.Fatalf("reuse %d %s", r.status, r.code)
	}
	// Expired.
	exp := e.token("acme", time.Minute)
	e.clk.Add(2 * time.Minute)
	if r := b.enroll(exp); r.status != 401 {
		t.Fatalf("expired %d", r.status)
	}
	// Signed by another key than the body's public key.
	c := e.newDaemon()
	tok2 := e.token("acme", time.Hour)
	body, _ := json.Marshal(hubproto.EnrollRequest{Token: tok2, DeviceName: "x", PublicKey: hubproto.EncodePublicKey(c.pub)})
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+hubproto.PathEnroll, bytes.NewReader(body))
	_ = hubproto.SignAt(req, b.id, b.priv, body, e.clk.Now(), e.nextNonce())
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("key mismatch %d", resp.StatusCode)
	}
	// The token was not burnt by the failed attempt.
	if r := c.enroll(tok2); r.status != 200 {
		t.Fatalf("token burnt: %d", r.status)
	}
	// Unknown field.
	r := b.do(http.MethodPost, hubproto.PathEnroll, map[string]string{"token": tok2, "device_name": "x", "public_key": hubproto.EncodePublicKey(b.pub), "org": "beta"})
	if r.status != 400 {
		t.Fatalf("unknown field %d", r.status)
	}
}

func TestReplayRejected(t *testing.T) {
	e := newEnv(t)
	a := e.newDaemon()
	a.mustEnroll("acme")
	nonce := "replaynonce-0000001"
	if r := a.raw(http.MethodGet, hubproto.PathOffers, nil, e.clk.Now(), nonce); r.status != 200 {
		t.Fatalf("first %d %s", r.status, r.body)
	}
	if r := a.raw(http.MethodGet, hubproto.PathOffers, nil, e.clk.Now(), nonce); r.status != 401 || r.code != hubproto.CodeReplay {
		t.Fatalf("replay %d %s", r.status, r.code)
	}
}

func TestSkewRejected(t *testing.T) {
	e := newEnv(t)
	a := e.newDaemon()
	a.mustEnroll("acme")
	for _, d := range []time.Duration{-6 * time.Minute, 6 * time.Minute} {
		r := a.raw(http.MethodGet, hubproto.PathOffers, nil, e.clk.Now().Add(d), e.nextNonce())
		if r.status != 401 || r.code != hubproto.CodeSkew {
			t.Fatalf("skew %v: %d %s", d, r.status, r.code)
		}
	}
}

func TestBadSignatureAndUnknownDevice(t *testing.T) {
	e := newEnv(t)
	a := e.newDaemon()
	a.mustEnroll("acme")
	// Tampered body after signing.
	body := []byte(`{"free_slots":1,"agents":[],"repos":[],"live_leases":[]}`)
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+hubproto.PathHeartbeat, bytes.NewReader([]byte(`{"free_slots":9,"agents":[],"repos":[],"live_leases":[]}`)))
	_ = hubproto.SignAt(req, a.id, a.priv, body, e.clk.Now(), e.nextNonce())
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("tampered %d", resp.StatusCode)
	}
	// Never enrolled.
	if r := e.newDaemon().do(http.MethodGet, hubproto.PathOffers, nil); r.status != 401 || r.code != hubproto.CodeUnauthorized {
		t.Fatalf("unknown %d %s", r.status, r.code)
	}
	// Unsigned.
	resp, _ = http.Get(e.srv.URL + hubproto.PathOffers)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("unsigned %d", resp.StatusCode)
	}
}

func TestRevokedRejected(t *testing.T) {
	e := newEnv(t)
	a := e.newDaemon()
	a.mustEnroll("acme")
	a.heartbeat([]string{"acme/web"})
	a.report(advisory("acme/web", hubproto.SeverityHigh))
	id := a.offers("")[0].ID
	if r := a.do(http.MethodPost, hubproto.ClaimPath(id), nil); r.status != 200 {
		t.Fatal("claim")
	}
	_ = e.st.Update(func(d *store.Data) error {
		now := e.clk.Now()
		d.Devices[a.id].RevokedAt = &now
		for _, w := range d.Items {
			lease.Revoke(w, a.id)
		}
		return nil
	})
	if r := a.do(http.MethodGet, hubproto.PathOffers, nil); r.status != 401 {
		t.Fatalf("revoked offers %d", r.status)
	}
	if r := a.do(http.MethodPost, hubproto.CompletePath(id), hubproto.CompleteRequest{PRURL: "https://github.com/acme/web/pull/1", Delivery: hubproto.DeliveryDraft}); r.status != 401 {
		t.Fatalf("revoked complete %d", r.status)
	}
	if e.item(id).State != hubproto.StateOpen {
		t.Fatal("revoked device's lease not reopened")
	}
	// Re-enrolling the revoked key is refused.
	if r := a.enroll(e.token("acme", time.Hour)); r.status != 409 {
		t.Fatalf("re-enroll %d", r.status)
	}
}

func TestInvalidFindingRejected(t *testing.T) {
	e := newEnv(t)
	a := e.newDaemon()
	a.mustEnroll("acme")
	a.heartbeat([]string{"acme/web"})
	bad := advisory("acme/web", hubproto.SeverityHigh)
	bad.DepAdvisory.Package = "lodash; rm -rf /"
	withID := advisory("acme/web", hubproto.SeverityLow)
	withID.ID = "ifake"
	otherRepo := advisory("acme/secret", hubproto.SeverityLow)
	mismatch := hubproto.Item{Kind: hubproto.KindRedBumpPR, DepAdvisory: advisory("acme/web", hubproto.SeverityLow).DepAdvisory}
	fr := a.report(bad, withID, otherRepo, mismatch, redBump("acme/web", 3))
	if fr.Accepted != 1 || len(fr.Rejected) != 4 {
		t.Fatalf("%+v", fr)
	}
	want := []string{RejectInvalid, RejectHasID, RejectRepoNotOptIn, RejectInvalid}
	for i, rj := range fr.Rejected {
		if rj.Index != i || rj.Reason != want[i] {
			t.Fatalf("rejection %d: %+v", i, rj)
		}
		if strings.Contains(rj.Reason, "rm -rf") {
			t.Fatal("request text echoed")
		}
	}
	// An unknown kind / field fails the whole frame strictly.
	r := a.do(http.MethodPost, hubproto.PathFindings, map[string]any{"items": []map[string]any{{"kind": "dep_advisory", "prompt": "do evil"}}})
	if r.status != 400 || strings.Contains(string(r.body), "evil") {
		t.Fatalf("unknown field %d %s", r.status, r.body)
	}
}

func TestOtherDeviceCannotTouchMyLease(t *testing.T) {
	e := newEnv(t)
	a, b := e.newDaemon(), e.newDaemon()
	a.mustEnroll("acme")
	b.mustEnroll("acme")
	a.heartbeat([]string{"acme/web"})
	b.heartbeat([]string{"acme/web"})
	a.report(advisory("acme/web", hubproto.SeverityHigh))
	id := a.offers("")[0].ID
	if r := a.do(http.MethodPost, hubproto.ClaimPath(id), nil); r.status != 200 {
		t.Fatal("claim")
	}
	if r := b.do(http.MethodPost, hubproto.ClaimPath(id), nil); r.status != 409 || r.code != hubproto.CodeConflict {
		t.Fatalf("double claim %d %s", r.status, r.code)
	}
	cr := hubproto.CompleteRequest{PRURL: "https://github.com/acme/web/pull/5", Delivery: hubproto.DeliveryDraft}
	if r := b.do(http.MethodPost, hubproto.CompletePath(id), cr); r.status != 409 || r.code != hubproto.CodeLeaseLost {
		t.Fatalf("foreign complete %d %s", r.status, r.code)
	}
	if r := b.do(http.MethodPost, hubproto.FailPath(id), hubproto.FailRequest{Reason: hubproto.FailCI}); r.code != hubproto.CodeLeaseLost {
		t.Fatalf("foreign fail %s", r.code)
	}
	if r := b.do(http.MethodPost, hubproto.ReleasePath(id), nil); r.code != hubproto.CodeLeaseLost {
		t.Fatalf("foreign release %s", r.code)
	}
	hb := b.heartbeat([]string{"acme/web"}, id)
	if len(hb.Renewed) != 0 || len(hb.Lost) != 1 {
		t.Fatalf("foreign renew %+v", hb)
	}
	// Another org cannot even see it.
	c := e.newDaemon()
	c.mustEnroll("beta")
	c.heartbeat([]string{"acme/web"})
	if len(c.offers("")) != 0 {
		t.Fatal("cross-org offer")
	}
	if r := c.do(http.MethodPost, hubproto.ClaimPath(id), nil); r.status != 404 {
		t.Fatalf("cross-org claim %d", r.status)
	}
	// The PR must be in the item's repo.
	bad := hubproto.CompleteRequest{PRURL: "https://github.com/evil/web/pull/5", Delivery: hubproto.DeliveryDraft}
	if r := a.do(http.MethodPost, hubproto.CompletePath(id), bad); r.status != 400 {
		t.Fatalf("wrong-repo PR %d", r.status)
	}
	if r := a.do(http.MethodPost, hubproto.CompletePath(id), cr); r.status != 200 {
		t.Fatalf("own complete %d %s", r.status, r.body)
	}
}

func TestExpiryReopens(t *testing.T) {
	e := newEnv(t)
	a, b := e.newDaemon(), e.newDaemon()
	a.mustEnroll("acme")
	b.mustEnroll("acme")
	a.heartbeat([]string{"acme/web"})
	b.heartbeat([]string{"acme/web"})
	a.report(advisory("acme/web", hubproto.SeverityHigh))
	id := a.offers("")[0].ID
	a.do(http.MethodPost, hubproto.ClaimPath(id), nil)
	if len(b.offers("")) != 0 {
		t.Fatal("claimed item offered")
	}
	e.clk.Add(31 * time.Minute)
	if n, err := e.hub.Sweep(); err != nil || n != 1 {
		t.Fatalf("sweep %d %v", n, err)
	}
	// Inside the holder's grace window (2 x TTL) nobody else gets it.
	if got := b.offers(""); len(got) != 0 {
		t.Fatal("item offered inside the holder's grace window")
	}
	if r := b.do(http.MethodPost, hubproto.ClaimPath(id), nil); r.status != http.StatusConflict {
		t.Fatalf("claim inside another device's grace: %d", r.status)
	}
	e.clk.Add(time.Hour)
	if _, err := e.hub.Sweep(); err != nil {
		t.Fatal(err)
	}
	if got := b.offers(""); len(got) != 1 {
		t.Fatal("expired item not reoffered after the grace window")
	}
	hb := a.heartbeat([]string{"acme/web"}, id)
	if len(hb.Lost) != 1 || hb.Lost[0] != id {
		t.Fatalf("old holder not told: %+v", hb)
	}
	if r := b.do(http.MethodPost, hubproto.ClaimPath(id), nil); r.status != 200 {
		t.Fatalf("reclaim %d", r.status)
	}
	if w := e.item(id); w.Attempts != 0 {
		t.Fatal("expiry counted an attempt")
	}
}

func TestFailEscalates(t *testing.T) {
	e := newEnv(t)
	a := e.newDaemon()
	a.mustEnroll("acme")
	a.heartbeat([]string{"acme/web"})
	a.report(redBump("acme/web", 4))
	id := a.offers("")[0].ID
	for i := 0; i < 2; i++ {
		if r := a.do(http.MethodPost, hubproto.ClaimPath(id), nil); r.status != 200 {
			t.Fatalf("claim %d: %d", i, r.status)
		}
		if r := a.do(http.MethodPost, hubproto.FailPath(id), hubproto.FailRequest{Reason: hubproto.FailAgent}); r.status != 200 {
			t.Fatalf("fail %d: %d", i, r.status)
		}
	}
	w := e.item(id)
	if w.State != hubproto.StateEscalated || w.Escalation == nil || !w.Escalation.Pending {
		t.Fatalf("%+v", w)
	}
	if len(a.offers("")) != 0 {
		t.Fatal("escalated item offered")
	}
	// Bad fail reason.
	if r := a.do(http.MethodPost, hubproto.FailPath(id), map[string]string{"reason": "because"}); r.status != 400 {
		t.Fatalf("bad reason %d", r.status)
	}
}

func TestClaimRequiresReportedRepo(t *testing.T) {
	e := newEnv(t)
	a, b := e.newDaemon(), e.newDaemon()
	a.mustEnroll("acme")
	b.mustEnroll("acme")
	a.heartbeat([]string{"acme/web"})
	b.heartbeat([]string{"acme/api"})
	a.report(advisory("acme/web", hubproto.SeverityHigh))
	id := a.offers("")[0].ID
	if len(b.offers("")) != 0 {
		t.Fatal("offered outside device repos")
	}
	if r := b.do(http.MethodPost, hubproto.ClaimPath(id), nil); r.status != 403 {
		t.Fatalf("claim outside repos %d", r.status)
	}
}

func TestPolicyEndpoint(t *testing.T) {
	e := newEnv(t)
	a := e.newDaemon()
	a.mustEnroll("acme")
	if r := a.do(http.MethodGet, hubproto.PathPolicy, nil); r.status != 404 || r.code != hubproto.CodeNoPolicy {
		t.Fatalf("no policy %d %s", r.status, r.code)
	}
	apub, apriv, _ := hubproto.GenerateKey()
	kid := hubproto.KeyIDFor(apub)
	p, err := hubproto.SignPolicy(apriv, kid, hubproto.Policy{Org: "acme", Version: 3, IssuedAt: e.clk.Now(),
		Settings: hubproto.Settings{EscalateAfter: 1, ReviewProviders: []string{"claude-session"}}})
	if err != nil {
		t.Fatal(err)
	}
	_ = e.st.Update(func(d *store.Data) error { d.Orgs["acme"].Policy = &p; return nil })
	r := a.do(http.MethodGet, hubproto.PathPolicy, nil)
	if r.status != 200 {
		t.Fatalf("policy %d", r.status)
	}
	got, err := hubproto.DecodeAndVerifyPolicy(r.body, map[string]ed25519.PublicKey{kid: apub})
	if err != nil || got.Version != 3 {
		t.Fatalf("daemon-side verify: %v", err)
	}
	// The org policy's escalate_after applies to leases.
	a.heartbeat([]string{"acme/web"})
	a.report(advisory("acme/web", hubproto.SeverityLow))
	id := a.offers("")[0].ID
	a.do(http.MethodPost, hubproto.ClaimPath(id), nil)
	a.do(http.MethodPost, hubproto.FailPath(id), hubproto.FailRequest{Reason: hubproto.FailCI})
	if e.item(id).State != hubproto.StateEscalated {
		t.Fatal("policy escalate_after ignored")
	}
}

func TestCriticalEscalatesAndIsOffered(t *testing.T) {
	e := newEnv(t)
	a := e.newDaemon()
	a.mustEnroll("acme")
	a.heartbeat([]string{"acme/web"})
	a.report(advisory("acme/web", hubproto.SeverityCritical))
	got := a.offers("")
	if len(got) != 1 {
		t.Fatal("critical not offered")
	}
	if w := e.item(got[0].ID); w.Escalation == nil || !w.Escalation.Pending {
		t.Fatal("critical not escalated")
	}
}

func TestBodyLimitsAndRouting(t *testing.T) {
	e := newEnv(t)
	a := e.newDaemon()
	a.mustEnroll("acme")
	big := map[string]string{"x": strings.Repeat("a", hubproto.MaxBody+10)}
	if r := a.do(http.MethodPost, hubproto.PathHeartbeat, big); r.status != 413 || r.code != hubproto.CodeTooLarge {
		t.Fatalf("too large %d %s", r.status, r.code)
	}
	if r := a.do(http.MethodPost, hubproto.PathOffers, nil); r.status != 405 {
		t.Fatalf("wrong method %d", r.status)
	}
	if r := a.do(http.MethodGet, hubproto.PathOffers+"?evil=1", nil); r.status != 400 {
		t.Fatalf("unknown query %d", r.status)
	}
	if r := a.do(http.MethodPost, "/v1/items/x/explode", nil); r.status != 404 {
		t.Fatalf("bad item path %d", r.status)
	}
	if r := a.do(http.MethodGet, "/v1/admin", nil); r.status != 404 {
		t.Fatalf("admin path %d", r.status)
	}
	resp, err := http.Get(e.srv.URL + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("healthz %v", err)
	}
	resp.Body.Close()
}

func TestRateLimited(t *testing.T) {
	e := newEnv(t)
	e.hub = New(Config{Store: e.st, Now: e.clk.Now, RatePerSec: 0.001, Burst: 3})
	srv := httptest.NewServer(e.hub.Handler())
	defer srv.Close()
	e.srv = srv
	a := e.newDaemon()
	a.mustEnroll("acme")
	var last result
	for i := 0; i < 4; i++ {
		last = a.do(http.MethodGet, hubproto.PathOffers, nil)
	}
	if last.status != 429 || last.code != hubproto.CodeRateLimited {
		t.Fatalf("rate %d %s", last.status, last.code)
	}
}

// ---- review fixes ---------------------------------------------------------

// withServer rebuilds the hub over the same store with cfg tweaks.
func (e *env) withServer(mod func(c *Config)) {
	cfg := Config{Store: e.st, Now: e.clk.Now, LeaseTTL: 30 * time.Minute}
	mod(&cfg)
	e.hub = New(cfg)
	srv := httptest.NewServer(e.hub.Handler())
	e.t.Cleanup(srv.Close)
	e.srv = srv
}

func pkgAdvisory(repo, pkg string, sev hubproto.Severity) hubproto.Item {
	it := advisory(repo, sev)
	it.DepAdvisory.Package = pkg
	return it
}

func TestThrottledRequestInsertsNoNonce(t *testing.T) {
	e := newEnv(t)
	e.withServer(func(c *Config) { c.RatePerSec, c.Burst = 0.001, 3 })
	a := e.newDaemon()
	a.mustEnroll("acme")
	for i := 0; i < 10; i++ {
		a.do(http.MethodGet, hubproto.PathOffers, nil)
	}
	e.hub.replay.mu.Lock()
	n := e.hub.replay.count[a.id]
	e.hub.replay.mu.Unlock()
	// One from enroll plus the burst of 3; the 7 throttled requests none.
	if n != 4 {
		t.Fatalf("live nonces for a throttled device: %d, want 4", n)
	}
}

func TestReplayCachePerDeviceCapAndExpiry(t *testing.T) {
	c := newReplayCache(100, 3)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if err := c.check("dev-a", fmt.Sprint(i), now); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.check("dev-a", "x", now); err == nil {
		t.Fatal("per-device cap ignored")
	}
	// Another device is unaffected.
	if err := c.check("dev-b", "x", now); err != nil {
		t.Fatalf("other device refused: %v", err)
	}
	if err := c.check("dev-a", "0", now); err == nil {
		t.Fatal("replay accepted")
	}
	// After the window everything expires and is pruned.
	later := now.Add(replayWindow)
	if err := c.check("dev-a", "0", later); err != nil {
		t.Fatalf("expired nonce refused: %v", err)
	}
	if len(c.seen) != 1 || c.count["dev-a"] != 1 || c.count["dev-b"] != 0 {
		t.Fatalf("not pruned: seen=%d count=%v", len(c.seen), c.count)
	}
	// The global cap holds too.
	g := newReplayCache(2, 10)
	_ = g.check("a", "1", now)
	_ = g.check("b", "1", now)
	if err := g.check("c", "1", now); err == nil {
		t.Fatal("global cap ignored")
	}
}

func TestJunkEnrollsDoNotDrainTheBucket(t *testing.T) {
	e := newEnv(t)
	junk := e.newDaemon()
	for i := 0; i < 3*enrollBurst; i++ {
		if r := junk.enroll(fmt.Sprintf("not-a-token-%04d-xxxxxxxxxxxxxxxxxxxx", i)); r.status != 401 {
			t.Fatalf("junk %d: %d %s", i, r.status, r.body)
		}
	}
	a := e.newDaemon()
	if r := a.enroll(e.token("acme", time.Hour)); r.status != 200 {
		t.Fatalf("legit enroll blocked: %d %s", r.status, r.body)
	}
}

func TestNewItemQuotaPerDevice(t *testing.T) {
	e := newEnv(t)
	e.withServer(func(c *Config) { c.NewItemsPerHour = 2 })
	a, b := e.newDaemon(), e.newDaemon()
	a.mustEnroll("acme")
	b.mustEnroll("acme")
	a.heartbeat([]string{"acme/web"})
	b.heartbeat([]string{"acme/web"})
	out := a.report(pkgAdvisory("acme/web", "p1", hubproto.SeverityHigh), pkgAdvisory("acme/web", "p2", hubproto.SeverityHigh), pkgAdvisory("acme/web", "p3", hubproto.SeverityHigh))
	if out.Accepted != 2 || len(out.Rejected) != 1 || out.Rejected[0].Reason != RejectQuota {
		t.Fatalf("%+v", out)
	}
	// Re-reports are free; another device has its own quota.
	if out := a.report(pkgAdvisory("acme/web", "p1", hubproto.SeverityHigh)); out.Accepted != 1 {
		t.Fatalf("re-report charged: %+v", out)
	}
	if out := b.report(pkgAdvisory("acme/web", "p3", hubproto.SeverityHigh)); out.Accepted != 1 {
		t.Fatalf("other device: %+v", out)
	}
}

func TestClosedItemsDoNotFillTheOrg(t *testing.T) {
	e := newEnv(t)
	e.withServer(func(c *Config) { c.MaxItemsPerOrg = 1 })
	a := e.newDaemon()
	a.mustEnroll("acme")
	a.heartbeat([]string{"acme/web"})
	a.report(pkgAdvisory("acme/web", "p1", hubproto.SeverityHigh))
	if out := a.report(pkgAdvisory("acme/web", "p2", hubproto.SeverityHigh)); len(out.Rejected) != 1 || out.Rejected[0].Reason != RejectOrgFull {
		t.Fatalf("cap not enforced: %+v", out)
	}
	_ = e.st.Update(func(d *store.Data) error {
		for _, w := range d.Items {
			w.State, w.ClosedAt = hubproto.StateDone, e.clk.Now()
		}
		return nil
	})
	if out := a.report(pkgAdvisory("acme/web", "p2", hubproto.SeverityHigh)); out.Accepted != 1 || len(out.Rejected) != 0 {
		t.Fatalf("closed item counted: %+v", out)
	}
}

func TestSweepPrunesOldClosedItems(t *testing.T) {
	e := newEnv(t)
	now := e.clk.Now()
	_ = e.st.Update(func(d *store.Data) error {
		mk := func(id string, st hubproto.State, closed time.Time, pending bool) {
			w := lease.NewItem(id, "acme", pkgAdvisory("acme/web", id, hubproto.SeverityHigh), "d", now)
			w.State, w.ClosedAt = st, closed
			if pending {
				w.Escalation = &store.Escalation{Pending: true}
			}
			d.Items[id] = w
		}
		mk("old-done", hubproto.StateDone, now.Add(-8*24*time.Hour), false)
		mk("old-failed", hubproto.StateFailed, now.Add(-8*24*time.Hour), false)
		mk("old-pending", hubproto.StateDone, now.Add(-8*24*time.Hour), true)
		mk("recent", hubproto.StateDone, now.Add(-2*24*time.Hour), false)
		mk("open", hubproto.StateOpen, time.Time{}, false)
		return nil
	})
	if _, err := e.hub.Sweep(); err != nil {
		t.Fatal(err)
	}
	_ = e.st.View(func(d *store.Data) error {
		for _, id := range []string{"old-done", "old-failed"} {
			if d.Items[id] != nil {
				t.Errorf("%s kept", id)
			}
		}
		for _, id := range []string{"old-pending", "recent", "open"} {
			if d.Items[id] == nil {
				t.Errorf("%s pruned", id)
			}
		}
		return nil
	})
}

// countingStore counts committed-or-not Update calls.
type countingStore struct {
	store.Store
	mu      sync.Mutex
	updates int
}

func (c *countingStore) Update(fn func(d *store.Data) error) error {
	c.mu.Lock()
	c.updates++
	c.mu.Unlock()
	return c.Store.Update(fn)
}

func TestOffersIsARead(t *testing.T) {
	e := newEnv(t)
	cs := &countingStore{Store: e.st}
	e.withServer(func(c *Config) { c.Store = cs })
	a, b := e.newDaemon(), e.newDaemon()
	a.mustEnroll("acme")
	b.mustEnroll("acme")
	a.heartbeat([]string{"acme/web"})
	b.heartbeat([]string{"acme/web"})
	a.report(advisory("acme/web", hubproto.SeverityHigh))
	before := cs.updates
	for i := 0; i < 5; i++ {
		if len(a.offers("")) != 1 {
			t.Fatal("offer missing")
		}
	}
	if cs.updates != before {
		t.Fatalf("offers wrote the store %d times", cs.updates-before)
	}
	// A lapsed lease is still reopened by the offers call.
	id := a.offers("")[0].ID
	a.do(http.MethodPost, hubproto.ClaimPath(id), nil)
	e.clk.Add(31*time.Minute + 2*30*time.Minute) // past the lease and its grace
	if got := b.offers(""); len(got) != 1 {
		t.Fatal("lapsed lease not reopened by offers")
	}
	if e.item(id).State != hubproto.StateOpen {
		t.Fatal("expiry not persisted")
	}
}

func TestReopeningReportRefreshesTheItem(t *testing.T) {
	e := newEnv(t)
	a := e.newDaemon()
	a.mustEnroll("acme")
	a.heartbeat([]string{"acme/web"})
	a.report(advisory("acme/web", hubproto.SeverityHigh))
	id := a.offers("")[0].ID
	_ = e.st.Update(func(d *store.Data) error {
		w := d.Items[id]
		w.State, w.ClosedAt = hubproto.StateDone, e.clk.Now()
		return nil
	})
	e.clk.Add(lease.ReopenAfter + time.Hour)
	it := advisory("acme/web", hubproto.SeverityCritical)
	it.DepAdvisory.Fixed = "4.17.22"
	a.report(it)
	w := e.item(id)
	if w.State != hubproto.StateOpen || w.Item.DepAdvisory.Fixed != "4.17.22" || w.Item.DepAdvisory.Severity != hubproto.SeverityCritical {
		t.Fatalf("stale reopen: %+v %+v", w.State, w.Item.DepAdvisory)
	}
	if w.Escalation == nil || !w.Escalation.Pending || w.Escalation.Reason != lease.ReasonCritical {
		t.Fatalf("critical reopen not escalated: %+v", w.Escalation)
	}
}
