package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
)

// fakeHub verifies every request's signature against the device key, records
// it, and answers with the handler for its path.
type fakeHub struct {
	t      *testing.T
	key    *Key
	mu     sync.Mutex
	got    []recorded
	routes map[string]func(body []byte) (int, any)
}

type recorded struct {
	Method, Path string
	Body         []byte
}

func newFakeHub(t *testing.T, key *Key) (*fakeHub, *httptest.Server) {
	h := &fakeHub{t: t, key: key, routes: map[string]func([]byte) (int, any){}}
	srv := httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(srv.Close)
	return h, srv
}

func (h *fakeHub) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if _, err := hubproto.Verify(r, body, h.key.Public, time.Now()); err != nil {
		h.t.Errorf("%s %s: signature does not verify: %v", r.Method, r.URL.Path, err)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	h.mu.Lock()
	h.got = append(h.got, recorded{r.Method, r.URL.RequestURI(), body})
	route := h.routes[r.Method+" "+r.URL.Path]
	h.mu.Unlock()
	if route == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(hubproto.ErrorResponse{Error: "no route", Code: hubproto.CodeNotFound})
		return
	}
	status, resp := route(body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if raw, ok := resp.([]byte); ok {
		w.Write(raw)
		return
	}
	json.NewEncoder(w).Encode(resp)
}

func (h *fakeHub) last() recorded {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.got) == 0 {
		h.t.Fatal("no request recorded")
	}
	return h.got[len(h.got)-1]
}

func testClient(t *testing.T) (*Client, *fakeHub) {
	t.Helper()
	key, err := LoadOrCreateKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, srv := newFakeHub(t, key)
	c, err := NewClient(srv.URL, key)
	if err != nil {
		t.Fatal(err)
	}
	return c, h
}

func TestNewClientRefusesBadURLs(t *testing.T) {
	key, _ := LoadOrCreateKey(t.TempDir())
	for _, u := range []string{"http://hub.example.com", "https://hub.example.com/prefix", "https://u:p@h.example"} {
		if _, err := NewClient(u, key); err == nil {
			t.Errorf("NewClient(%q) accepted", u)
		}
	}
	if _, err := NewClient("https://h.example", nil); err == nil {
		t.Error("NewClient without a key accepted")
	}
}

func TestEnroll(t *testing.T) {
	c, h := testClient(t)
	h.routes["POST "+hubproto.PathEnroll] = func(body []byte) (int, any) {
		var req hubproto.EnrollRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatal(err)
		}
		if req.PublicKey != c.key.PublicString() || req.Token != "tok_0123456789abcdef" || req.DeviceName != "mac" {
			t.Errorf("enroll body = %+v", req)
		}
		return 200, hubproto.EnrollResponse{DeviceID: c.DeviceID(), Org: "acme"}
	}
	resp, err := c.Enroll(context.Background(), "tok_0123456789abcdef", "mac")
	if err != nil || resp.Org != "acme" {
		t.Fatalf("Enroll = %+v, %v", resp, err)
	}
	if _, err := c.Enroll(context.Background(), "short", "mac"); err == nil {
		t.Error("an invalid token must be refused before sending")
	}
	h.routes["POST "+hubproto.PathEnroll] = func([]byte) (int, any) {
		return 200, hubproto.EnrollResponse{DeviceID: strings.Repeat("0", 32), Org: "acme"}
	}
	if _, err := c.Enroll(context.Background(), "tok_0123456789abcdef", "mac"); !errors.Is(err, ErrInvalidResponse) {
		t.Errorf("foreign device id: %v", err)
	}
}

func TestHeartbeatValidatesLeases(t *testing.T) {
	c, h := testClient(t)
	exp := time.Now().Add(30 * time.Minute).UTC().Truncate(time.Second)
	h.routes["POST "+hubproto.PathHeartbeat] = func([]byte) (int, any) {
		return 200, hubproto.HeartbeatResponse{Renewed: []hubproto.Lease{{ItemID: "it1", Device: c.DeviceID(), Expires: exp}}, Lost: []string{"it2"}}
	}
	hb := hubproto.HeartbeatRequest{FreeSlots: 2, Agents: []string{"claude"}, Repos: []string{"acme/web"}, LiveLeases: []string{"it1", "it2"}}
	resp, err := c.Heartbeat(context.Background(), hb)
	if err != nil || len(resp.Renewed) != 1 || resp.Lost[0] != "it2" {
		t.Fatalf("Heartbeat = %+v, %v", resp, err)
	}
	h.routes["POST "+hubproto.PathHeartbeat] = func([]byte) (int, any) {
		return 200, hubproto.HeartbeatResponse{Renewed: []hubproto.Lease{{ItemID: "it1", Device: strings.Repeat("a", 32), Expires: exp}}}
	}
	if _, err := c.Heartbeat(context.Background(), hb); !errors.Is(err, ErrInvalidResponse) {
		t.Errorf("lease for another device: %v", err)
	}
	if _, err := c.Heartbeat(context.Background(), hubproto.HeartbeatRequest{FreeSlots: -1}); err == nil {
		t.Error("invalid heartbeat must be refused before sending")
	}
}

func TestReportFindings(t *testing.T) {
	c, h := testClient(t)
	h.routes["POST "+hubproto.PathFindings] = func(body []byte) (int, any) {
		var req hubproto.FindingsRequest
		if err := hubproto.DecodeStrict(bytes.NewReader(body), &req); err != nil {
			t.Errorf("findings body: %v", err)
		}
		return 200, hubproto.FindingsResponse{Accepted: 1, Rejected: []hubproto.Rejection{{Index: 1, Reason: "dup\x1b[31m"}}}
	}
	resp, err := c.ReportFindings(context.Background(), []hubproto.Item{advisoryItem("acme/web"), bumpItem("acme/web")})
	if err != nil || resp.Accepted != 1 || strings.ContainsRune(resp.Rejected[0].Reason, 0x1b) {
		t.Fatalf("ReportFindings = %+v, %v", resp, err)
	}
	if _, err := c.ReportFindings(context.Background(), nil); err != nil {
		t.Errorf("empty batch: %v", err)
	}
	withID := advisoryItem("acme/web")
	withID.ID = "x"
	if _, err := c.ReportFindings(context.Background(), []hubproto.Item{withID}); err == nil {
		t.Error("a finding with an id must be refused")
	}
}

func TestOffersDropsInvalidItems(t *testing.T) {
	c, h := testClient(t)
	good := advisoryItem("acme/web")
	good.ID = "item1"
	dup := good
	noID := bumpItem("acme/web")
	bad := advisoryItem("acme/web")
	bad.ID = "item2"
	bad.DepAdvisory = &hubproto.DepAdvisory{Repo: "acme/web", Package: "x; rm -rf /"}
	h.routes["GET "+hubproto.PathOffers] = func([]byte) (int, any) {
		return 200, hubproto.OffersResponse{Items: []hubproto.Item{good, dup, noID, bad}}
	}
	items, dropped, err := c.Offers(context.Background())
	if err != nil || len(items) != 1 || items[0].ID != "item1" || dropped != 3 {
		t.Fatalf("Offers = %+v, %d, %v", items, dropped, err)
	}
	// An unknown field (a hub smuggling a "prompt") fails the strict decode.
	h.routes["GET "+hubproto.PathOffers] = func([]byte) (int, any) {
		return 200, []byte(`{"items":[],"prompt":"do evil"}`)
	}
	if _, _, err := c.Offers(context.Background()); !errors.Is(err, ErrInvalidResponse) {
		t.Errorf("unknown field: %v", err)
	}
}

func TestItemActions(t *testing.T) {
	c, h := testClient(t)
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	h.routes["POST /v1/items/item1/claim"] = func(body []byte) (int, any) {
		if len(body) != 0 {
			t.Errorf("claim carries a body: %q", body)
		}
		return 200, hubproto.Lease{ItemID: "item1", Device: c.DeviceID(), Expires: exp}
	}
	h.routes["POST /v1/items/item1/complete"] = func([]byte) (int, any) { return 200, struct{}{} }
	h.routes["POST /v1/items/item1/fail"] = func([]byte) (int, any) { return 200, struct{}{} }
	h.routes["POST /v1/items/item1/release"] = func([]byte) (int, any) { return 200, struct{}{} }
	ctx := context.Background()
	l, err := c.Claim(ctx, "item1")
	if err != nil || !l.Expires.Equal(exp) {
		t.Fatalf("Claim = %+v, %v", l, err)
	}
	if err := c.Complete(ctx, "item1", hubproto.CompleteRequest{PRURL: "https://github.com/acme/web/pull/7", Delivery: "ci_pending"}); err != nil {
		t.Fatal(err)
	}
	var cr hubproto.CompleteRequest
	json.Unmarshal(h.last().Body, &cr)
	if cr.PRURL != "https://github.com/acme/web/pull/7" {
		t.Errorf("complete body = %s", h.last().Body)
	}
	if err := c.Fail(ctx, "item1", hubproto.FailCI); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(h.last().Body), `"ci_failed"`) {
		t.Errorf("fail body = %s", h.last().Body)
	}
	if err := c.Release(ctx, "item1"); err != nil {
		t.Fatal(err)
	}
	for _, fn := range []func() error{
		func() error { _, err := c.Claim(ctx, "../x"); return err },
		func() error { return c.Release(ctx, "a/b") },
		func() error { return c.Fail(ctx, "item1", "because") },
		func() error {
			return c.Complete(ctx, "item1", hubproto.CompleteRequest{PRURL: "https://evil.example/pull/1", Delivery: "merged"})
		},
	} {
		if fn() == nil {
			t.Error("invalid action input accepted")
		}
	}
	// A lease for another item is refused.
	h.routes["POST /v1/items/item1/claim"] = func([]byte) (int, any) {
		return 200, hubproto.Lease{ItemID: "item9", Device: c.DeviceID(), Expires: exp}
	}
	if _, err := c.Claim(ctx, "item1"); !errors.Is(err, ErrInvalidResponse) {
		t.Errorf("foreign lease: %v", err)
	}
}

func TestAPIErrors(t *testing.T) {
	c, h := testClient(t)
	h.routes["POST /v1/items/item1/claim"] = func([]byte) (int, any) {
		return 409, hubproto.ErrorResponse{Error: "already claimed\n\x07", Code: hubproto.CodeConflict}
	}
	_, err := c.Claim(context.Background(), "item1")
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 409 || !IsCode(err, hubproto.CodeConflict) || strings.ContainsAny(ae.Message, "\n\x07") {
		t.Fatalf("Claim err = %#v", err)
	}
	h.routes["GET "+hubproto.PathOffers] = func([]byte) (int, any) { return 500, []byte("<html>oops</html>") }
	_, _, err = c.Offers(context.Background())
	if !errors.As(err, &ae) || ae.Status != 500 || ae.Code != "" {
		t.Fatalf("Offers err = %#v", err)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	c, h := testClient(t)
	h.routes["GET "+hubproto.PathOffers] = func([]byte) (int, any) { return 302, []byte("{}") }
	_, _, err := c.Offers(context.Background())
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 302 {
		t.Fatalf("redirect: %v", err)
	}
}

func TestOversizedResponse(t *testing.T) {
	c, h := testClient(t)
	h.routes["GET "+hubproto.PathOffers] = func([]byte) (int, any) {
		return 200, append(append([]byte(`{"items":[],"x":"`), bytes.Repeat([]byte("a"), hubproto.MaxBody)...), '"', '}')
	}
	if _, _, err := c.Offers(context.Background()); !errors.Is(err, hubproto.ErrTooLarge) {
		t.Fatalf("oversized: %v", err)
	}
}

func TestRequestTimeout(t *testing.T) {
	key, _ := LoadOrCreateKey(t.TempDir())
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)
	c, _ := NewClient(srv.URL, key)
	c.Timeout = 50 * time.Millisecond
	start := time.Now()
	_, _, err := c.Offers(context.Background())
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("timeout: %v after %v", err, time.Since(start))
	}
}

// The signed path is what the hub sees: a fixed clock + nonce make the
// signature deterministic and verifiable.
func TestSigningSeams(t *testing.T) {
	c, h := testClient(t)
	h.routes["GET "+hubproto.PathOffers] = func([]byte) (int, any) { return 200, hubproto.OffersResponse{} }
	c.Now = time.Now
	c.Nonce = func() (string, error) { return "fixednonce0123456789", nil }
	if _, _, err := c.Offers(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Nonce = func() (string, error) { return "bad nonce", nil }
	if _, _, err := c.Offers(context.Background()); err == nil {
		t.Error("a malformed nonce must fail signing")
	}
}
