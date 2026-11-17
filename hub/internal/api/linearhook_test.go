package api_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
)

const hookSecret = "whsec_0123456789abcdef0123456789"

func sign(body []byte, secret string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// post sends a delivery to org's endpoint (unsigned by any device).
func (e *tenv) post(org string, body []byte, sig string) int {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+hubproto.PathLinearHook+org, bytes.NewReader(body))
	if sig != "" {
		req.Header.Set("Linear-Signature", sig)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (s *signer) doorbell() uint64 { return s.bell("18446744073709551615") }

// bell asks the long-poll with ?since=; a differing value answers at once.
func (s *signer) bell(since string) uint64 {
	s.e.t.Helper()
	r := s.send(http.MethodGet, hubproto.PathDoorbell+"?since="+since, nil, "")
	if r.status != 200 {
		s.e.t.Fatalf("doorbell: %d %s", r.status, r.body)
	}
	var out hubproto.DoorbellResponse
	_ = json.Unmarshal(r.body, &out)
	return out.Doorbell
}

// A verified delivery rings the org's doorbell, which a member's heartbeat
// reports; everything else is refused identically and rings nothing.
func TestLinearHookRingsTheDoorbell(t *testing.T) {
	e := newTeams(t, true)
	a := e.signer()
	a.create("acme")
	body := []byte(fmt.Sprintf(`{"type":"AgentSessionEvent","webhookTimestamp":%d}`, e.now.UnixMilli()))

	if got := e.post("acme", body, sign(body, hookSecret)); got != 401 {
		t.Fatalf("no secret set must refuse, got %d", got)
	}
	if r := a.send(http.MethodPost, hubproto.PathOrgSettings, hubproto.OrgSettingsRequest{LinearWebhookSecret: ptr(hookSecret)}, "acme"); r.status != 200 {
		t.Fatalf("set secret: %d %s", r.status, r.body)
	} else if !strings.Contains(string(r.body), `"linear_webhook_set":true`) || strings.Contains(string(r.body), hookSecret) {
		t.Fatalf("view must say it is set and never echo it: %s", r.body)
	}
	if n := a.doorbell(); n != 0 {
		t.Fatalf("doorbell before any delivery = %d", n)
	}

	for name, c := range map[string]struct {
		org  string
		body []byte
		sig  string
	}{
		"bad signature": {"acme", body, sign(body, "another-secret-another-secret")},
		"no signature":  {"acme", body, ""},
		"unknown org":   {"nobody", body, sign(body, hookSecret)},
		"stale":         {"acme", []byte(`{"webhookTimestamp":1}`), sign([]byte(`{"webhookTimestamp":1}`), hookSecret)},
	} {
		if got := e.post(c.org, c.body, c.sig); got != 401 {
			t.Fatalf("%s: %d, want 401", name, got)
		}
	}
	if n := a.doorbell(); n != 0 {
		t.Fatalf("refused deliveries rang the bell: %d", n)
	}

	if got := e.post("acme", body, sign(body, hookSecret)); got != 200 {
		t.Fatalf("valid delivery: %d", got)
	}
	e.post("acme", body, sign(body, hookSecret))
	if n := a.doorbell(); n != 2 {
		t.Fatalf("doorbell = %d, want 2", n)
	}
	// Another org's members never hear it.
	b := e.signer()
	b.create("beta")
	if n := b.doorbell(); n != 0 {
		t.Fatalf("beta heard acme's bell: %d", n)
	}

	// Clearing the secret closes the endpoint again.
	if r := a.send(http.MethodPost, hubproto.PathOrgSettings, hubproto.OrgSettingsRequest{LinearWebhookSecret: ptr("")}, "acme"); r.status != 200 {
		t.Fatalf("clear: %d %s", r.status, r.body)
	}
	if got := e.post("acme", body, sign(body, hookSecret)); got != 401 {
		t.Fatalf("cleared secret must refuse, got %d", got)
	}
}

func TestLinearHookRefusesOversizeAndWrongMethod(t *testing.T) {
	e := newTeams(t, true)
	a := e.signer()
	a.create("acme")
	a.send(http.MethodPost, hubproto.PathOrgSettings, hubproto.OrgSettingsRequest{LinearWebhookSecret: ptr(hookSecret)}, "acme")
	big := bytes.Repeat([]byte("x"), 70<<10)
	if got := e.post("acme", big, sign(big, hookSecret)); got != 400 {
		t.Fatalf("oversize body: %d", got)
	}
	resp, err := http.Get(e.srv.URL + hubproto.PathLinearHook + "acme")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Fatalf("GET: %d", resp.StatusCode)
	}
}

func TestSettingsLinearWebhookNeedsASecretKeyAndAValidShape(t *testing.T) {
	e := newTeams(t, false)
	a := e.signer()
	a.create("acme")
	if r := a.send(http.MethodPost, hubproto.PathOrgSettings, hubproto.OrgSettingsRequest{LinearWebhookSecret: ptr(hookSecret)}, "acme"); r.status != 503 {
		t.Fatalf("no master key: %d %s", r.status, r.body)
	}
	e2 := newTeams(t, true)
	b := e2.signer()
	b.create("beta")
	if r := b.send(http.MethodPost, hubproto.PathOrgSettings, hubproto.OrgSettingsRequest{LinearWebhookSecret: ptr("short")}, "beta"); r.status != 400 {
		t.Fatalf("short secret: %d %s", r.status, r.body)
	}
}

// A daemon holding the long-poll is woken by a delivery, not by its timeout.
func TestDoorbellLongPollWakesOnDelivery(t *testing.T) {
	e := newTeams(t, true)
	a := e.signer()
	a.create("acme")
	a.send(http.MethodPost, hubproto.PathOrgSettings, hubproto.OrgSettingsRequest{LinearWebhookSecret: ptr(hookSecret)}, "acme")
	body := []byte(fmt.Sprintf(`{"webhookTimestamp":%d}`, e.now.UnixMilli()))

	got := make(chan uint64, 1)
	go func() { got <- a.bell("0") }()
	select {
	case v := <-got:
		t.Fatalf("answered before any delivery: %d", v)
	case <-time.After(300 * time.Millisecond):
	}
	if code := e.post("acme", body, sign(body, hookSecret)); code != 200 {
		t.Fatalf("delivery: %d", code)
	}
	select {
	case v := <-got:
		if v != 1 {
			t.Fatalf("woke with %d, want 1", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the held long-poll was not woken")
	}
	if r := a.send(http.MethodGet, hubproto.PathDoorbell+"?since=x", nil, ""); r.status != 400 {
		t.Fatalf("bad since: %d", r.status)
	}
	if r := a.send(http.MethodGet, hubproto.PathDoorbell+"?other=1", nil, ""); r.status != 400 {
		t.Fatalf("unknown query: %d", r.status)
	}
}
