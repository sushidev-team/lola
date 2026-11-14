package api_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/admin"
	"github.com/sushidev-team/lola/hub/internal/api"
	"github.com/sushidev-team/lola/hub/internal/escalate"
	"github.com/sushidev-team/lola/hub/internal/secret"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// fakeLinear answers Teams without the network: the key decides the answer.
type fakeLinear struct{ key string }

const goodKey = "lin_api_goodgoodgoodgoodgoodgood"

func (f fakeLinear) Teams(context.Context) ([]hubproto.LinearTeam, error) {
	switch f.key {
	case goodKey:
		return []hubproto.LinearTeam{{ID: "team-eng", Key: "ENG", Name: "Engineering"}, {ID: "team-ops", Key: "OPS"}}, nil
	case "lin_api_downdowndowndowndowndown":
		return nil, escalate.ErrNetwork
	}
	return nil, escalate.ErrAuth
}

type tenv struct {
	t     *testing.T
	st    *store.File
	now   time.Time
	srv   *httptest.Server
	box   *secret.Box
	nonce int
}

func newTeams(t *testing.T, withBox bool) *tenv {
	t.Helper()
	return newTeamsCfg(t, withBox, nil)
}

// newTeamsCfg is newTeams with the API config adjusted by tune.
func newTeamsCfg(t *testing.T, withBox bool, tune func(*api.Config)) *tenv {
	t.Helper()
	st, err := store.Open("")
	if err != nil {
		t.Fatal(err)
	}
	e := &tenv{t: t, st: st, now: time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)}
	if withBox {
		k, _ := secret.NewKey()
		e.box, _ = secret.New(k)
	}
	ops := &admin.Ops{Store: st, Box: e.box, Linear: func(key string) admin.TeamLister { return fakeLinear{key} }}
	cfg := api.Config{Store: st, Now: func() time.Time { return e.now }, Admin: ops}
	if tune != nil {
		tune(&cfg)
	}
	hub := api.New(cfg)
	e.srv = httptest.NewServer(hub.Handler())
	t.Cleanup(e.srv.Close)
	return e
}

type signer struct {
	e          *tenv
	pub, apub  ed25519.PublicKey
	priv, apri ed25519.PrivateKey
	org        string
}

func (e *tenv) signer() *signer {
	pub, priv, _ := hubproto.GenerateKey()
	apub, apri, _ := hubproto.GenerateKey()
	return &signer{e: e, pub: pub, priv: priv, apub: apub, apri: apri}
}

type res struct {
	status int
	code   string
	body   []byte
}

// send signs with the device key and, when asAdmin, with the admin key for
// adminOrg.
func (s *signer) send(method, path string, payload any, adminOrg string) res {
	s.e.t.Helper()
	var body []byte
	if payload != nil {
		body, _ = json.Marshal(payload)
	}
	req, _ := http.NewRequest(method, s.e.srv.URL+path, bytes.NewReader(body))
	if body == nil {
		req.Body = http.NoBody
	}
	s.e.nonce++
	if err := hubproto.SignAt(req, hubproto.DeviceID(s.pub), s.priv, body, s.e.now, fmt.Sprintf("teamsnonce-%08d", s.e.nonce)); err != nil {
		s.e.t.Fatal(err)
	}
	if adminOrg != "" {
		if err := hubproto.SignAdmin(req, adminOrg, hubproto.KeyIDFor(s.apub), s.apri, body); err != nil {
			s.e.t.Fatal(err)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	var er hubproto.ErrorResponse
	_ = json.Unmarshal(rb, &er)
	return res{resp.StatusCode, er.Code, rb}
}

func (e *tenv) code() string {
	e.t.Helper()
	c, _, err := admin.CreateCreateCode(e.st, time.Hour, e.now)
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (s *signer) createReq(code, org string) hubproto.CreateOrgRequest {
	return hubproto.CreateOrgRequest{Code: code, Org: org, DeviceName: "laptop",
		PublicKey: hubproto.EncodePublicKey(s.pub), AdminKeyID: hubproto.KeyIDFor(s.apub), AdminPublicKey: hubproto.EncodePublicKey(s.apub)}
}

// create makes org with s as its first device and admin.
func (s *signer) create(org string) {
	s.e.t.Helper()
	if r := s.send(http.MethodPost, hubproto.PathOrgs, s.createReq(s.e.code(), org), org); r.status != 200 {
		s.e.t.Fatalf("create %s: %d %s", org, r.status, r.body)
	}
	s.org = org
}

// member enrolls a plain member (no admin key registered) into org.
func (e *tenv) member(org string) *signer {
	e.t.Helper()
	m := e.signer()
	tok, _, err := admin.CreateEnrollToken(e.st, org, "Member", time.Hour, e.now)
	if err != nil {
		e.t.Fatal(err)
	}
	r := m.send(http.MethodPost, hubproto.PathEnroll, hubproto.EnrollRequest{Token: tok, DeviceName: "member-box", PublicKey: hubproto.EncodePublicKey(m.pub)}, "")
	if r.status != 200 {
		e.t.Fatalf("enroll: %d %s", r.status, r.body)
	}
	m.org = org
	return m
}

func ptr(s string) *string { return &s }

func TestCreateOrg(t *testing.T) {
	e := newTeams(t, true)
	a := e.signer()
	code := e.code()
	r := a.send(http.MethodPost, hubproto.PathOrgs, a.createReq(code, "acme"), "acme")
	if r.status != 200 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	var out hubproto.CreateOrgResponse
	_ = json.Unmarshal(r.body, &out)
	if out.Validate() != nil || out.Org != "acme" || out.DeviceID != hubproto.DeviceID(a.pub) {
		t.Fatalf("response %+v", out)
	}
	_ = e.st.View(func(d *store.Data) error {
		o := d.Orgs["acme"]
		if o == nil || o.AdminKeys[hubproto.KeyIDFor(a.apub)] != hubproto.EncodePublicKey(a.apub) {
			t.Fatalf("org %+v", o)
		}
		if dv := d.Devices[out.DeviceID]; dv == nil || dv.Org != "acme" {
			t.Fatalf("device %+v", dv)
		}
		return nil
	})
	// The code is single use.
	b := e.signer()
	if r := b.send(http.MethodPost, hubproto.PathOrgs, b.createReq(code, "beta"), "beta"); r.status != 401 {
		t.Fatalf("reused code: %d %s", r.status, r.body)
	}
	// The creator is an enrolled device and an admin at once.
	if r := a.send(http.MethodGet, hubproto.PathOrgSettings, nil, "acme"); r.status != 200 {
		t.Fatalf("settings after create: %d %s", r.status, r.body)
	}
}

func TestCreateOrgRefusals(t *testing.T) {
	e := newTeams(t, true)
	first := e.signer()
	first.create("acme")
	a := e.signer()
	// No code, a wrong code, an expired code.
	if r := a.send(http.MethodPost, hubproto.PathOrgs, a.createReq("", "beta"), "beta"); r.status != 401 {
		t.Fatalf("no code on a closed hub: %d", r.status)
	}
	if r := a.send(http.MethodPost, hubproto.PathOrgs, a.createReq(strings.Repeat("x", 32), "beta"), "beta"); r.status != 401 {
		t.Fatalf("unknown code: %d", r.status)
	}
	old, _, _ := admin.CreateCreateCode(e.st, time.Minute, e.now.Add(-time.Hour))
	if r := a.send(http.MethodPost, hubproto.PathOrgs, a.createReq(old, "beta"), "beta"); r.status != 401 {
		t.Fatalf("expired code: %d", r.status)
	}
	code := e.code()
	// No admin signature: possession of the admin key is not proven.
	if r := a.send(http.MethodPost, hubproto.PathOrgs, a.createReq(code, "beta"), ""); r.status != 401 {
		t.Fatalf("no admin signature: %d", r.status)
	}
	// An admin signature for another org name.
	if r := a.send(http.MethodPost, hubproto.PathOrgs, a.createReq(code, "beta"), "gamma"); r.status != 401 {
		t.Fatalf("admin signature for another org: %d", r.status)
	}
	// Somebody else's admin PUBLIC key, signed with our own.
	req := a.createReq(code, "beta")
	req.AdminPublicKey = hubproto.EncodePublicKey(first.apub)
	if r := a.send(http.MethodPost, hubproto.PathOrgs, req, "beta"); r.status != 401 {
		t.Fatalf("foreign admin key: %d", r.status)
	}
	// A taken org name conflicts and leaves the code usable.
	if r := a.send(http.MethodPost, hubproto.PathOrgs, a.createReq(code, "acme"), "acme"); r.status != 409 {
		t.Fatalf("taken org: %d", r.status)
	}
	_ = e.st.View(func(d *store.Data) error {
		if len(d.Orgs["acme"].AdminKeys) != 1 || d.Orgs["acme"].AdminKeys[hubproto.KeyIDFor(a.apub)] != "" {
			t.Fatal("a refused create changed the existing org's keys")
		}
		return nil
	})
	if r := a.send(http.MethodPost, hubproto.PathOrgs, a.createReq(code, "beta"), "beta"); r.status != 200 {
		t.Fatalf("code after a conflict: %d %s", r.status, r.body)
	}
}

// GET /v1/hub is unsigned and says only whether a team can be created without
// a code right now.
func TestHubInfo(t *testing.T) {
	get := func(e *tenv, method, path string) (int, hubproto.HubInfo) {
		req, _ := http.NewRequest(method, e.srv.URL+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var info hubproto.HubInfo
		_ = json.NewDecoder(resp.Body).Decode(&info)
		return resp.StatusCode, info
	}
	closed := newTeams(t, true)
	if s, info := get(closed, http.MethodGet, hubproto.PathHub); s != 200 || info.OpenTeams {
		t.Fatalf("closed hub: %d %+v", s, info)
	}
	if s, _ := get(closed, http.MethodPost, hubproto.PathHub); s != 405 {
		t.Fatalf("POST: %d", s)
	}
	if s, _ := get(closed, http.MethodGet, hubproto.PathHub+"?x=1"); s != 405 {
		t.Fatalf("query: %d", s)
	}
	open := newTeamsCfg(t, true, func(c *api.Config) { c.OpenTeams, c.MaxOrgs = true, 1 })
	if s, info := get(open, http.MethodGet, hubproto.PathHub); s != 200 || !info.OpenTeams {
		t.Fatalf("open hub: %d %+v", s, info)
	}
	// At the cap a code is what creates a team: the hub says so.
	a := open.signer()
	if r := a.send(http.MethodPost, hubproto.PathOrgs, a.createReq("", "acme"), "acme"); r.status != 200 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	if s, info := get(open, http.MethodGet, hubproto.PathHub); s != 200 || info.OpenTeams {
		t.Fatalf("full hub: %d %+v", s, info)
	}
}

// --open-teams: a team is created without a code, both signatures are still
// required, a code that IS sent is still checked, and the hub-wide bucket and
// the team cap bound it.
func TestCreateOrgOpen(t *testing.T) {
	e := newTeamsCfg(t, true, func(c *api.Config) { c.OpenTeams, c.MaxOrgs = true, 7 })
	a := e.signer()
	// No admin signature: still refused.
	if r := a.send(http.MethodPost, hubproto.PathOrgs, a.createReq("", "acme"), ""); r.status != 401 {
		t.Fatalf("no admin signature: %d", r.status)
	}
	// A wrong code is not waved through because the hub is open.
	if r := a.send(http.MethodPost, hubproto.PathOrgs, a.createReq(strings.Repeat("x", 32), "acme"), "acme"); r.status != 401 {
		t.Fatalf("unknown code on an open hub: %d", r.status)
	}
	if r := a.send(http.MethodPost, hubproto.PathOrgs, a.createReq("", "acme"), "acme"); r.status != 200 {
		t.Fatalf("open create: %d %s", r.status, r.body)
	}
	a.org = "acme"
	if r := a.send(http.MethodGet, hubproto.PathOrgSettings, nil, "acme"); r.status != 200 {
		t.Fatalf("settings after an open create: %d %s", r.status, r.body)
	}
	_ = e.st.View(func(d *store.Data) error {
		for _, ev := range d.Events {
			if ev.Kind == "org_created" && ev.Detail != "open creation" {
				t.Fatalf("event %+v", ev)
			}
		}
		return nil
	})
	// A taken name conflicts.
	b := e.signer()
	if r := b.send(http.MethodPost, hubproto.PathOrgs, b.createReq("", "acme"), "acme"); r.status != 409 {
		t.Fatalf("taken org: %d", r.status)
	}
	// The burst is 5 hub-wide (two are spent): three more pass, then 429.
	for i, org := range []string{"b1", "b2", "b3"} {
		s := e.signer()
		if r := s.send(http.MethodPost, hubproto.PathOrgs, s.createReq("", org), org); r.status != 200 {
			t.Fatalf("create %d: %d %s", i, r.status, r.body)
		}
	}
	s := e.signer()
	if r := s.send(http.MethodPost, hubproto.PathOrgs, s.createReq("", "b4"), "b4"); r.status != 429 {
		t.Fatalf("over the burst: %d", r.status)
	}
	// A drained open bucket does not block a creation that carries a code.
	c := e.signer()
	c.create("coded")
	// Ten minutes refill one. Teams: acme, b1-b3, coded = 5 of 7.
	for _, org := range []string{"b5", "b6"} {
		e.now = e.now.Add(10 * time.Minute)
		s := e.signer()
		if r := s.send(http.MethodPost, hubproto.PathOrgs, s.createReq("", org), org); r.status != 200 {
			t.Fatalf("create %s after a refill: %d %s", org, r.status, r.body)
		}
	}
	// The hub is full for code-less creation, not for the operator's code.
	e.now = e.now.Add(10 * time.Minute)
	s = e.signer()
	if r := s.send(http.MethodPost, hubproto.PathOrgs, s.createReq("", "b7"), "b7"); r.status != 503 {
		t.Fatalf("over the team cap: %d", r.status)
	}
	s.create("b7")
}

// An org-admin request needs BOTH an enrolled device of the org and a key
// registered in that org.
func TestOrgAdminAuth(t *testing.T) {
	e := newTeams(t, true)
	a := e.signer()
	a.create("acme")
	b := e.signer()
	b.create("beta")
	body := hubproto.OrgSettingsRequest{LinearTeam: ptr("team-eng")}

	// A member without the admin key, with no admin headers and with a key
	// the org does not know.
	m := e.member("acme")
	if r := m.send(http.MethodPost, hubproto.PathOrgSettings, body, ""); r.status != 403 {
		t.Fatalf("member, no admin signature: %d", r.status)
	}
	if r := m.send(http.MethodPost, hubproto.PathOrgSettings, body, "acme"); r.status != 403 {
		t.Fatalf("member, unregistered key: %d", r.status)
	}
	// beta's admin signing for acme: its device is in beta, so the request
	// is about beta, and a signature naming acme does not verify there.
	if r := b.send(http.MethodPost, hubproto.PathOrgSettings, body, "acme"); r.status != 403 {
		t.Fatalf("cross-org admin signature: %d", r.status)
	}
	// beta's admin acting properly changes beta only.
	if r := b.send(http.MethodPost, hubproto.PathOrgSettings, body, "beta"); r.status != 200 {
		t.Fatalf("beta admin: %d %s", r.status, r.body)
	}
	_ = e.st.View(func(d *store.Data) error {
		if d.Orgs["acme"].LinearTeam != "" || d.Orgs["beta"].LinearTeam != "team-eng" {
			t.Fatalf("teams: acme %q beta %q", d.Orgs["acme"].LinearTeam, d.Orgs["beta"].LinearTeam)
		}
		return nil
	})
	// An operator removes the key: the admin is a plain member again.
	if err := admin.RemovePolicyKey(e.st, "beta", hubproto.KeyIDFor(b.apub), e.now); err != nil {
		t.Fatal(err)
	}
	if r := b.send(http.MethodPost, hubproto.PathOrgSettings, body, "beta"); r.status != 403 {
		t.Fatalf("removed key: %d", r.status)
	}
	// A revoked device loses admin access even with the key.
	if _, err := admin.RevokeDevice(e.st, "acme", hubproto.DeviceID(a.pub), e.now); err != nil {
		t.Fatal(err)
	}
	if r := a.send(http.MethodGet, hubproto.PathOrgSettings, nil, "acme"); r.status != 401 {
		t.Fatalf("revoked device: %d", r.status)
	}
}

func TestSettingsLinearKey(t *testing.T) {
	e := newTeams(t, true)
	a := e.signer()
	a.create("acme")
	// Linear refuses the key: nothing is stored.
	r := a.send(http.MethodPost, hubproto.PathOrgSettings, hubproto.OrgSettingsRequest{LinearKey: ptr("lin_api_badbadbadbadbadbadbad")}, "acme")
	if r.status != 422 || r.code != hubproto.CodeInvalidSecret {
		t.Fatalf("bad key: %d %s", r.status, r.code)
	}
	// Linear does not answer: nothing is stored either.
	r = a.send(http.MethodPost, hubproto.PathOrgSettings, hubproto.OrgSettingsRequest{LinearKey: ptr("lin_api_downdowndowndowndowndown")}, "acme")
	if r.status != 503 || r.code != hubproto.CodeUnavailable {
		t.Fatalf("linear down: %d %s", r.status, r.code)
	}
	// A team that is not in the key's workspace.
	r = a.send(http.MethodPost, hubproto.PathOrgSettings, hubproto.OrgSettingsRequest{LinearKey: ptr(goodKey), LinearTeam: ptr("team-elsewhere")}, "acme")
	if r.status != 400 {
		t.Fatalf("foreign team: %d", r.status)
	}
	_ = e.st.View(func(d *store.Data) error {
		if d.Orgs["acme"].LinearKey != "" {
			t.Fatal("a refused key was stored")
		}
		return nil
	})
	r = a.send(http.MethodPost, hubproto.PathOrgSettings, hubproto.OrgSettingsRequest{LinearKey: ptr(goodKey)}, "acme")
	if r.status != 200 {
		t.Fatalf("good key: %d %s", r.status, r.body)
	}
	var v hubproto.OrgSettingsView
	_ = json.Unmarshal(r.body, &v)
	if v.Validate() != nil || !v.LinearKeySet || !v.SecretsAvailable || len(v.Teams) != 2 || v.Teams[0].Key != "ENG" {
		t.Fatalf("view %+v", v)
	}
	// The key is write-only: no answer and no stored byte holds it.
	if bytes.Contains(r.body, []byte(goodKey)) {
		t.Fatal("the answer carries the key")
	}
	_ = e.st.View(func(d *store.Data) error {
		raw, _ := json.Marshal(d)
		if bytes.Contains(raw, []byte(goodKey)) {
			t.Fatal("the store holds the plaintext key")
		}
		got, err := e.box.Open("acme", secret.FieldLinearKey, d.Orgs["acme"].LinearKey)
		if err != nil || got != goodKey {
			t.Fatalf("sealed key opens to %q, %v", got, err)
		}
		for _, ev := range d.Events {
			if strings.Contains(ev.Detail, "lin_api_") {
				t.Fatalf("event leaks the key: %+v", ev)
			}
		}
		return nil
	})
	// The team list again, with the stored key; then the team.
	r = a.send(http.MethodGet, hubproto.PathOrgSettingsTeams, nil, "acme")
	_ = json.Unmarshal(r.body, &v)
	if r.status != 200 || len(v.Teams) != 2 {
		t.Fatalf("teams: %d %s", r.status, r.body)
	}
	r = a.send(http.MethodPost, hubproto.PathOrgSettings, hubproto.OrgSettingsRequest{LinearTeam: ptr("team-eng")}, "acme")
	v = hubproto.OrgSettingsView{}
	_ = json.Unmarshal(r.body, &v)
	if r.status != 200 || v.LinearTeam != "team-eng" || len(v.Teams) != 0 {
		t.Fatalf("set team: %d %s", r.status, r.body)
	}
	// Clear the key.
	r = a.send(http.MethodPost, hubproto.PathOrgSettings, hubproto.OrgSettingsRequest{LinearKey: ptr("")}, "acme")
	v = hubproto.OrgSettingsView{}
	_ = json.Unmarshal(r.body, &v)
	if r.status != 200 || v.LinearKeySet {
		t.Fatalf("clear: %d %s", r.status, r.body)
	}
	if r := a.send(http.MethodGet, hubproto.PathOrgSettingsTeams, nil, "acme"); r.status != 409 {
		t.Fatalf("teams without a key: %d", r.status)
	}
}

func TestSettingsDigestWebhook(t *testing.T) {
	e := newTeams(t, true)
	a := e.signer()
	a.create("acme")
	const hook = "https://hooks.slack.com/services/T0001/B0001/abcdefSECRET"
	// The hub posts to this URL itself: anything but Slack is refused.
	for _, bad := range []string{"https://169.254.169.254/latest/meta-data", "http://lola-hub.lola-hub.svc/", "https://example.com/services/T/B/x"} {
		if r := a.send(http.MethodPost, hubproto.PathOrgSettings, hubproto.OrgSettingsRequest{DigestWebhook: ptr(bad)}, "acme"); r.status != 400 {
			t.Fatalf("%s: %d", bad, r.status)
		}
	}
	r := a.send(http.MethodPost, hubproto.PathOrgSettings, hubproto.OrgSettingsRequest{DigestWebhook: ptr(hook)}, "acme")
	var v hubproto.OrgSettingsView
	_ = json.Unmarshal(r.body, &v)
	if r.status != 200 || !v.DigestSet || bytes.Contains(r.body, []byte("SECRET")) {
		t.Fatalf("set webhook: %d %s", r.status, r.body)
	}
	_ = e.st.View(func(d *store.Data) error {
		raw, _ := json.Marshal(d)
		if bytes.Contains(raw, []byte("SECRET")) {
			t.Fatal("the store holds the plaintext webhook")
		}
		return nil
	})
}

// Without a secret key the hub stores no secret; the rest still works.
func TestSettingsWithoutSecretKey(t *testing.T) {
	e := newTeams(t, false)
	a := e.signer()
	a.create("acme")
	r := a.send(http.MethodPost, hubproto.PathOrgSettings, hubproto.OrgSettingsRequest{LinearKey: ptr(goodKey)}, "acme")
	if r.status != 503 || r.code != hubproto.CodeUnavailable {
		t.Fatalf("key without a box: %d %s", r.status, r.code)
	}
	r = a.send(http.MethodPost, hubproto.PathOrgSettings, hubproto.OrgSettingsRequest{LinearTeam: ptr("team-eng")}, "acme")
	var v hubproto.OrgSettingsView
	_ = json.Unmarshal(r.body, &v)
	if r.status != 200 || v.SecretsAvailable || v.LinearTeam != "team-eng" {
		t.Fatalf("team without a box: %d %s", r.status, r.body)
	}
}

func TestInviteAndRevoke(t *testing.T) {
	e := newTeams(t, true)
	a := e.signer()
	a.create("acme")
	r := a.send(http.MethodPost, hubproto.PathOrgInvites, hubproto.InviteRequest{Label: "Ada"}, "acme")
	var inv hubproto.InviteResponse
	_ = json.Unmarshal(r.body, &inv)
	if r.status != 200 || inv.Validate() != nil || !inv.ExpiresAt.Equal(e.now.Add(hubproto.DefaultInviteTTL*time.Second)) {
		t.Fatalf("invite: %d %s", r.status, r.body)
	}
	// The answer is the token and its expiry, nothing a member would pin.
	var raw map[string]any
	_ = json.Unmarshal(r.body, &raw)
	if len(raw) != 2 {
		t.Fatalf("invite answer carries more than the token: %v", raw)
	}
	// The token enrolls a device into the admin's org, labelled.
	m := e.signer()
	er := m.send(http.MethodPost, hubproto.PathEnroll, hubproto.EnrollRequest{Token: inv.Token, DeviceName: "ada-box", PublicKey: hubproto.EncodePublicKey(m.pub)}, "")
	var en hubproto.EnrollResponse
	_ = json.Unmarshal(er.body, &en)
	if er.status != 200 || en.Org != "acme" || en.Member != "Ada" {
		t.Fatalf("enroll with the invite: %d %s", er.status, er.body)
	}
	// A member cannot invite or revoke.
	if r := m.send(http.MethodPost, hubproto.PathOrgInvites, hubproto.InviteRequest{Label: "Eve"}, "acme"); r.status != 403 {
		t.Fatalf("member invite: %d", r.status)
	}
	if r := m.send(http.MethodPost, hubproto.PathOrgRevoke, hubproto.DeviceRevokeRequest{Device: "laptop"}, "acme"); r.status != 403 {
		t.Fatalf("member revoke: %d", r.status)
	}
	// The admin revokes by display name; another org's device is not found.
	b := e.signer()
	b.create("beta")
	if r := b.send(http.MethodPost, hubproto.PathOrgRevoke, hubproto.DeviceRevokeRequest{Device: "ada-box"}, "beta"); r.status != 404 {
		t.Fatalf("revoke across orgs: %d", r.status)
	}
	if r := a.send(http.MethodPost, hubproto.PathOrgRevoke, hubproto.DeviceRevokeRequest{Device: "ada-box"}, "acme"); r.status != 200 {
		t.Fatalf("revoke: %d %s", r.status, r.body)
	}
	if r := m.send(http.MethodGet, hubproto.PathOrg, nil, ""); r.status != 401 {
		t.Fatalf("revoked device still served: %d", r.status)
	}
	if r := a.send(http.MethodPost, hubproto.PathOrgRevoke, hubproto.DeviceRevokeRequest{Device: "ada-box"}, "acme"); r.status != 404 {
		t.Fatalf("revoke twice: %d", r.status)
	}
}

// Unused invites are bounded per org.
func TestInviteLimit(t *testing.T) {
	e := newTeams(t, true)
	a := e.signer()
	a.create("acme")
	if _, _, err := admin.CreateEnrollTokens(e.st, "acme", "Bulk", time.Hour, 50, e.now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, _, err := admin.CreateEnrollTokens(e.st, "acme", "Bulk", time.Hour, 50, e.now); err != nil {
			t.Fatal(err)
		}
	}
	if r := a.send(http.MethodPost, hubproto.PathOrgInvites, hubproto.InviteRequest{Label: "Ada"}, "acme"); r.status != 409 {
		t.Fatalf("invite past the limit: %d %s", r.status, r.body)
	}
}

// The org-admin bucket is per org and charged only for verified admins.
func TestOrgAdminRateLimit(t *testing.T) {
	e := newTeams(t, true)
	a := e.signer()
	a.create("acme")
	m := e.member("acme")
	for i := 0; i < 30; i++ {
		m.send(http.MethodGet, hubproto.PathOrgSettings, nil, "acme")
	}
	last := 0
	for i := 0; i < 25; i++ {
		last = a.send(http.MethodGet, hubproto.PathOrgSettings, nil, "acme").status
		if i == 0 && last != 200 {
			t.Fatalf("a member's refused requests spent the admin budget: %d", last)
		}
	}
	if last != 429 {
		t.Fatalf("admin requests are not rate limited: %d", last)
	}
}
