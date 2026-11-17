package console

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/admin"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef"

type env struct {
	t   *testing.T
	st  *store.File
	srv *httptest.Server
	c   *http.Client
	con *Console
	now time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open("")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, st: st, now: time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)}
	con, err := New(Config{Store: st, Token: testToken, Now: func() time.Time { return e.now }})
	if err != nil {
		t.Fatal(err)
	}
	e.con = con
	e.srv = httptest.NewServer(con.Handler())
	t.Cleanup(e.srv.Close)
	jar, _ := cookiejar.New(nil)
	e.c = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if err := admin.CreateOrg(st, "acme", e.now); err != nil {
		t.Fatal(err)
	}
	return e
}

type resp struct {
	status int
	body   string
	loc    string
	cookie []*http.Cookie
}

func (e *env) do(method, path string, form url.Values, hdr map[string]string) resp {
	e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	r, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return resp{status: r.StatusCode, body: string(b), loc: r.Header.Get("Location"), cookie: r.Cookies()}
}

func (e *env) origin() map[string]string { return map[string]string{"Origin": e.srv.URL} }

func (e *env) get(path string) resp { return e.do(http.MethodGet, path, nil, nil) }

func (e *env) post(path string, form url.Values) resp {
	return e.do(http.MethodPost, path, form, e.origin())
}

func (e *env) login() {
	e.t.Helper()
	r := e.post("/login", url.Values{"token": {testToken}})
	if r.status != http.StatusSeeOther || r.loc != "/" {
		e.t.Fatalf("login: %d %s", r.status, r.body)
	}
}

var csrfRe = regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`)

func (e *env) csrf() string {
	e.t.Helper()
	m := csrfRe.FindStringSubmatch(e.get("/").body)
	if m == nil {
		e.t.Fatal("no csrf token on the page")
	}
	return m[1]
}

func TestCheckLoopback(t *testing.T) {
	for _, a := range []string{"127.0.0.1:8098", "[::1]:8098", "localhost:8098", "127.0.0.2:1"} {
		if err := CheckLoopback(a); err != nil {
			t.Errorf("%s refused: %v", a, err)
		}
	}
	for _, a := range []string{":8098", "0.0.0.0:8098", "[::]:8098", "192.168.1.5:8098", "example.com:8098", "8098"} {
		if err := CheckLoopback(a); err == nil {
			t.Errorf("%s accepted", a)
		}
	}
	if ln, err := Listen("0.0.0.0:0"); err == nil {
		ln.Close()
		t.Fatal("Listen bound a non-loopback address")
	}
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
}

func TestTokenFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	tok, created, err := LoadOrCreateToken(dir)
	if err != nil || !created || len(tok) != 64 {
		t.Fatalf("create: %q %v %v", tok, created, err)
	}
	fi, _ := os.Stat(filepath.Join(dir, TokenFile))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	again, created, err := LoadOrCreateToken(dir)
	if err != nil || created || again != tok {
		t.Fatalf("reload: %v %v", created, err)
	}
	_ = os.Chmod(filepath.Join(dir, TokenFile), 0o644)
	if _, _, err := LoadOrCreateToken(dir); err == nil {
		t.Fatal("world-readable token accepted")
	}
}

func TestNoSessionRedirectsAndRefuses(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/", "/orgs/acme"} {
		if r := e.get(p); r.status != http.StatusSeeOther || r.loc != "/login" {
			t.Fatalf("%s without a session: %d %q", p, r.status, r.loc)
		}
	}
	if r := e.post("/orgs", url.Values{"name": {"x"}}); r.status != http.StatusForbidden {
		t.Fatalf("post without a session: %d", r.status)
	}
	if r := e.get("/login"); r.status != 200 || !strings.Contains(r.body, `name="token"`) {
		t.Fatalf("login page: %d", r.status)
	}
	if r := e.get("/static/app.css"); r.status != 200 {
		t.Fatalf("static: %d", r.status)
	}
}

func TestLoginCookieAndThrottle(t *testing.T) {
	e := newEnv(t)
	if r := e.post("/login", url.Values{"token": {"wrong"}}); r.status != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", r.status)
	}
	// Cross-origin login is refused before the token is looked at.
	if r := e.do(http.MethodPost, "/login", url.Values{"token": {testToken}}, map[string]string{"Origin": "http://evil.example"}); r.status != http.StatusForbidden {
		t.Fatalf("cross-origin login: %d", r.status)
	}
	r := e.post("/login", url.Values{"token": {testToken}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("login %d", r.status)
	}
	var ck *http.Cookie
	for _, c := range r.cookie {
		if c.Name == cookieName {
			ck = c
		}
	}
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" || len(ck.Value) != 64 || ck.Value == testToken {
		t.Fatalf("cookie %+v", ck)
	}
	if r := e.get("/"); r.status != 200 || !strings.Contains(r.body, "acme") {
		t.Fatalf("overview %d", r.status)
	}
	// Expiry.
	e.now = e.now.Add(DefaultSessionTTL + time.Second)
	if r := e.get("/"); r.status != http.StatusSeeOther {
		t.Fatalf("expired session still valid: %d", r.status)
	}
	// Throttle: after the burst of failures a wrong token is refused
	// outright, but the right one still signs in, so a loopback process
	// spamming failures cannot lock the admin out.
	for i := 0; i < loginBurst; i++ {
		if r := e.post("/login", url.Values{"token": {"wrong"}}); r.status != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d", i, r.status)
		}
	}
	if r := e.post("/login", url.Values{"token": {"wrong"}}); r.status != http.StatusTooManyRequests {
		t.Fatalf("not throttled: %d", r.status)
	}
	e.login()
	e.now = e.now.Add(time.Minute)
	if r := e.post("/login", url.Values{"token": {"wrong"}}); r.status != http.StatusUnauthorized {
		t.Fatalf("throttle never refilled: %d", r.status)
	}
}

func TestMutationsNeedCSRFAndSameOrigin(t *testing.T) {
	e := newEnv(t)
	e.login()
	tok := e.csrf()
	form := func(csrf string) url.Values { return url.Values{"name": {"beta"}, "csrf": {csrf}} }
	if r := e.post("/orgs", form("")); r.status != http.StatusForbidden {
		t.Fatalf("no csrf: %d", r.status)
	}
	if r := e.post("/orgs", form(strings.Repeat("0", 64))); r.status != http.StatusForbidden {
		t.Fatalf("bad csrf: %d", r.status)
	}
	if r := e.do(http.MethodPost, "/orgs", form(tok), map[string]string{"Origin": "http://evil.example"}); r.status != http.StatusForbidden {
		t.Fatalf("cross-origin: %d", r.status)
	}
	if r := e.do(http.MethodPost, "/orgs", form(tok), map[string]string{"Referer": "http://evil.example/x"}); r.status != http.StatusForbidden {
		t.Fatalf("cross-origin referer: %d", r.status)
	}
	if r := e.do(http.MethodPost, "/orgs", form(tok), nil); r.status != http.StatusForbidden {
		t.Fatalf("no origin or referer: %d", r.status)
	}
	if r := e.do(http.MethodGet, "/orgs/acme/enroll", nil, nil); r.status != http.StatusMethodNotAllowed {
		t.Fatalf("GET on a mutation: %d", r.status)
	}
	// Same-origin Referer is enough when no Origin is sent.
	if r := e.do(http.MethodPost, "/orgs", form(tok), map[string]string{"Referer": e.srv.URL + "/"}); r.status != http.StatusSeeOther {
		t.Fatalf("good post: %d %s", r.status, r.body)
	}
	_ = e.st.View(func(d *store.Data) error {
		if d.Orgs["beta"] == nil {
			t.Fatal("org not created")
		}
		return nil
	})
}

func TestForeignHostRefused(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/login", nil)
	req.Host = "rebind.evil.example"
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign host: %d", r.StatusCode)
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	r, err := http.Get(e.srv.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if csp := r.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("csp %q", csp)
	}
	if r.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatal("frameable")
	}
}

func TestTemplatesEscapeStoredData(t *testing.T) {
	e := newEnv(t)
	e.login()
	evil := `<script>alert(1)</script>`
	pub, _, _ := hubproto.GenerateKey()
	dev := hubproto.DeviceID(pub)
	// Written straight into the store, past every validator, to prove the
	// templates alone hold.
	_ = e.st.Update(func(d *store.Data) error {
		d.Devices[dev] = &store.Device{ID: dev, Org: "acme", Name: `<img src=x onerror=alert(2)>`, PublicKey: hubproto.EncodePublicKey(pub)}
		d.Items["ievil"] = &store.WorkItem{ID: "ievil", Org: "acme", State: hubproto.StateClaimed,
			Item: hubproto.Item{Kind: hubproto.KindDepAdvisory, DepAdvisory: &hubproto.DepAdvisory{
				Repo: evil, Ecosystem: evil, Package: evil, Vulnerable: evil, Advisory: evil, Severity: `"><b>x`}},
			Lease:     &hubproto.Lease{ItemID: "ievil", Device: dev, Expires: e.now.Add(time.Hour)},
			Outcome:   &store.Outcome{PRURL: "javascript:alert(3)", Delivery: "x"},
			FirstSeen: e.now, LastSeen: e.now}
		d.Log(store.Event{At: e.now, Org: "acme", Kind: evil, Detail: evil})
		return nil
	})
	for _, p := range []string{"/", "/orgs/acme"} {
		body := e.get(p).body
		for _, bad := range []string{"<script>alert", "<img src=x", `"><b>x`, "javascript:alert"} {
			if strings.Contains(body, bad) {
				t.Fatalf("%s renders %q unescaped", p, bad)
			}
		}
		if !strings.Contains(body, "&lt;script&gt;") {
			t.Fatalf("%s: expected escaped text", p)
		}
	}
}

func TestOrgActionsEndToEnd(t *testing.T) {
	e := newEnv(t)
	e.login()
	tok := e.csrf()
	// Enroll token: shown once, stored hashed.
	// POST-redirect-GET: the POST mints and redirects; the GET it lands
	// on shows the token once. A reload is a GET and mints nothing.
	r := e.post("/orgs/acme/enroll", url.Values{"csrf": {tok}, "ttl": {"24h"}})
	if r.status != http.StatusSeeOther || r.loc != "/orgs/acme" {
		t.Fatalf("enroll: %d %q", r.status, r.loc)
	}
	enrollRe := regexp.MustCompile(`id="enroll-token" class="mono grow" value="([A-Za-z0-9_-]{32})"`)
	if strings.Contains(r.body, "enroll-token") {
		t.Fatal("token on the POST response")
	}
	if enrollRe.MatchString(e.get("/").body) {
		t.Fatal("token revealed on another page")
	}
	m := enrollRe.FindStringSubmatch(e.get("/orgs/acme").body)
	if m == nil {
		t.Fatal("token not shown after the redirect")
	}
	_ = e.st.View(func(d *store.Data) error {
		if n := len(d.Tokens); n != 1 {
			t.Fatalf("%d enroll tokens minted, want 1", n)
		}
		return nil
	})
	_ = e.st.View(func(d *store.Data) error {
		if d.Tokens[store.HashToken(m[1])] == nil {
			t.Fatal("token not stored")
		}
		return nil
	})
	if strings.Contains(e.get("/orgs/acme").body, m[1]) {
		t.Fatal("token shown a second time")
	}

	// Add a chore: invalid is refused with a message, valid is stored.
	bad := url.Values{"csrf": {tok}, "kind": {"dep_advisory"}, "repo": {"acme/web"}, "package": {"lodash"},
		"ecosystem": {"npm"}, "vulnerable": {"< 1.0; rm -rf ~"}, "advisory": {"GHSA-jf85-cpcp-j695"}, "severity": {"high"}}
	if r := e.post("/orgs/acme/items/add", bad); r.status != http.StatusSeeOther {
		t.Fatalf("bad add %d", r.status)
	}
	if body := e.get("/orgs/acme").body; !strings.Contains(body, "flash error") {
		t.Fatal("no error flash for an invalid chore")
	}
	good := url.Values{"csrf": {tok}, "kind": {"dep_advisory"}, "repo": {"acme/web"}, "package": {"lodash"},
		"ecosystem": {"npm"}, "vulnerable": {"< 4.17.21"}, "fixed": {"4.17.21"}, "advisory": {"GHSA-jf85-cpcp-j695"}, "severity": {"high"}}
	e.post("/orgs/acme/items/add", good)
	var id string
	_ = e.st.View(func(d *store.Data) error {
		for k, w := range d.Items {
			if w.ReporterIDs[0] == admin.Reporter {
				id = k
			}
		}
		return nil
	})
	if id == "" {
		t.Fatal("chore not added")
	}
	if body := e.get("/orgs/acme?state=open").body; !strings.Contains(body, "GHSA-jf85-cpcp-j695") {
		t.Fatal("chore not listed")
	}

	// Linear team.
	e.post("/orgs/acme/linear", url.Values{"csrf": {tok}, "team": {"team-123"}})
	e.post("/orgs/acme/linear", url.Values{"csrf": {tok}, "team": {"bad team!"}})
	_ = e.st.View(func(d *store.Data) error {
		if d.Orgs["acme"].LinearTeam != "team-123" {
			t.Fatalf("team %q", d.Orgs["acme"].LinearTeam)
		}
		return nil
	})
	e.post("/orgs/acme/linear", url.Values{"csrf": {tok}, "clear": {"1"}})

	// Revoke.
	pub, _, _ := hubproto.GenerateKey()
	dev := hubproto.DeviceID(pub)
	_ = e.st.Update(func(d *store.Data) error {
		d.Devices[dev] = &store.Device{ID: dev, Org: "acme", Name: "laptop", PublicKey: hubproto.EncodePublicKey(pub)}
		return nil
	})
	e.post("/orgs/acme/devices/"+dev+"/revoke", url.Values{"csrf": {tok}})
	// Purge one, and purge a state.
	if r := e.post("/orgs/acme/items/"+id+"/purge", url.Values{"csrf": {tok}, "return_state": {"open"}}); r.loc != "/orgs/acme?state=open" {
		t.Fatalf("purge redirect %q", r.loc)
	}
	_ = e.st.View(func(d *store.Data) error {
		if !d.Devices[dev].Revoked() || d.Items[id] != nil || d.Orgs["acme"].LinearTeam != "" {
			t.Fatal("revoke / purge / clear did not apply")
		}
		return nil
	})
	if r := e.post("/orgs/acme/items/purge", url.Values{"csrf": {tok}, "state": {"done"}}); r.status != http.StatusSeeOther {
		t.Fatalf("purge state %d", r.status)
	}

	// Policy: register a key, publish a signed bundle.
	apub, apriv, _ := hubproto.GenerateKey()
	kid := hubproto.KeyIDFor(apub)
	e.post("/orgs/acme/policy/keys", url.Values{"csrf": {tok}, "public_key": {hubproto.FormatTrustedKey(kid, apub)}})
	p, _ := hubproto.SignPolicy(apriv, kid, hubproto.Policy{Org: "acme", Version: 2, IssuedAt: e.now, Settings: hubproto.Settings{EscalateAfter: 3}})
	raw, _ := jsonMarshal(p)
	e.post("/orgs/acme/policy/publish", url.Values{"csrf": {tok}, "policy": {string(raw)}})
	body := e.get("/orgs/acme").body
	if !strings.Contains(body, "v2") || !strings.Contains(body, "escalate_after") {
		t.Fatal("policy not shown")
	}
	// Signing out ends the session.
	e.post("/logout", url.Values{"csrf": {tok}})
	if r := e.get("/"); r.status != http.StatusSeeOther {
		t.Fatal("session survived logout")
	}
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
