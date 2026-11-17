// Package console is the hub's admin web console: a small server-rendered UI
// over the same operations as the host-side CLI (internal/admin).
//
// It is a deliberate, fenced exception to "no admin HTTP endpoints". The rails,
// each of which a test pins:
//
//   - LOOPBACK ONLY. CheckLoopback refuses any listen address whose host is
//     not a loopback IP or "localhost", and Listen re-checks the address the
//     kernel actually bound. The Host header must name a loopback host too, so
//     a DNS-rebinding page cannot reach it through a foreign name. Remote use
//     goes through an SSH tunnel, never a public bind.
//   - A TOKEN. The admin token lives in <data dir>/admin.token (0600, created
//     on first start). Logging in exchanges it (constant-time compare, failures
//     throttled) for a random session id kept in a server-side map with an
//     expiry; the cookie is HttpOnly, SameSite=Strict, Path=/.
//   - CSRF. Every mutating request must be a POST, carry an Origin (or, when a
//     browser sends none, a Referer) naming this console, and the session's
//     CSRF token in the form. Anything else is 403.
//   - ESCAPING. Every page is html/template; item fields and device names are
//     display data and are never marked safe. A strict CSP allows only the
//     embedded stylesheet and script; there is no third-party asset.
//
// The console never signs a policy (it verifies a pasted, pre-signed bundle,
// as the CLI does), never shows a stored secret (enroll tokens are shown once,
// on the response that created them) and never touches a device.
package console

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/hub/internal/secret"
	"github.com/sushidev-team/lola/hub/internal/store"
)

//go:embed templates/*.html static/*
var assets embed.FS

// Defaults.
const (
	DefaultSessionTTL = 12 * time.Hour
	TokenFile         = "admin.token"
	cookieName        = "lola_hub_admin"
	maxSessions       = 64
	// Login throttle: at most loginBurst failures, refilled one per
	// loginRefill.
	loginBurst  = 5
	loginRefill = 12 * time.Second
)

// Config wires a Console.
type Config struct {
	Store      store.Store
	Token      string           // the admin token (required)
	Now        func() time.Time // nil = time.Now
	SessionTTL time.Duration    // 0 = DefaultSessionTTL
	Logf       func(format string, args ...any)
	// Box seals an org's Linear key and digest webhook when the operator
	// sets one here (nil = the hub runs without a secret key and stores
	// none). The console only ever WRITES a secret: no page shows one.
	Box *secret.Box
	// LinearTeam is the hub-wide fallback team (--linear-team), to tell an
	// org whose escalations wait for want of a team.
	LinearTeam string
}

type session struct {
	csrf    string
	expires time.Time
	flash   *flash
	reveal  *reveal
}

// reveal is a freshly minted enroll token waiting to be shown ONCE on the
// org page the create redirect lands on (POST-redirect-GET, so a reload
// cannot mint another). Memory only, never logged. With org "" it is a
// create code, shown once on the overview.
type reveal struct {
	org    string
	token  string
	label  string
	expiry time.Time
}

type flash struct {
	Error bool
	Text  string
}

// Console serves the admin UI.
type Console struct {
	cfg   Config
	pages map[string]*template.Template

	mu       sync.Mutex
	sessions map[string]*session
	failures float64
	failedAt time.Time
}

// New builds a Console.
func New(cfg Config) (*Console, error) {
	if cfg.Token == "" {
		return nil, errors.New("console: no admin token")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = DefaultSessionTTL
	}
	c := &Console{cfg: cfg, sessions: map[string]*session{}, pages: map[string]*template.Template{}}
	for _, p := range []string{"login", "overview", "org", "item", "events"} {
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(assets, "templates/layout.html", "templates/"+p+".html")
		if err != nil {
			return nil, fmt.Errorf("console: template %s: %w", p, err)
		}
		c.pages[p] = t
	}
	return c, nil
}

func (c *Console) logf(format string, args ...any) {
	if c.cfg.Logf != nil {
		c.cfg.Logf(format, args...)
	}
}

// ---- loopback -----------------------------------------------------------

// ErrNotLoopback is returned for a listen address that is not loopback.
var ErrNotLoopback = errors.New("console: the admin console listens on a loopback address only (127.0.0.1, ::1 or localhost)")

// CheckLoopback refuses any address whose host is not a loopback IP or
// "localhost". An empty host (":8098", all interfaces) is refused.
func CheckLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("console: bad address: %w", err)
	}
	if !loopbackHost(host) {
		return ErrNotLoopback
	}
	return nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Listen checks addr, listens, and re-checks the address actually bound
// ("localhost" may resolve to anything on a misconfigured host).
func Listen(addr string) (net.Listener, error) {
	if err := CheckLoopback(addr); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	if ta, ok := ln.Addr().(*net.TCPAddr); !ok || !ta.IP.IsLoopback() {
		ln.Close()
		return nil, ErrNotLoopback
	}
	return ln, nil
}

// ---- token file -----------------------------------------------------------

// LoadOrCreateToken reads <dir>/admin.token, creating it (0600, 256 random
// bits) when absent. created reports a fresh token. A token file readable by
// group or others is refused.
func LoadOrCreateToken(dir string) (token string, created bool, err error) {
	path := filepath.Join(dir, TokenFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false, err
	}
	if fi, err := os.Stat(path); err == nil {
		if fi.Mode().Perm()&0o077 != 0 {
			return "", false, fmt.Errorf("console: %s is readable by others; chmod 600 it", path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", false, err
		}
		tok := strings.TrimSpace(string(raw))
		if len(tok) < 32 {
			return "", false, fmt.Errorf("console: %s holds no usable token", path)
		}
		return tok, false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", false, err
	}
	tok := randHex(32)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", false, err
	}
	if _, err := f.WriteString(tok + "\n"); err != nil {
		f.Close()
		return "", false, err
	}
	return tok, true, f.Close()
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return hex.EncodeToString(b)
}

// ---- sessions ---------------------------------------------------------------

func (c *Console) newSession() (string, *session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.cfg.Now()
	for id, s := range c.sessions {
		if !now.Before(s.expires) {
			delete(c.sessions, id)
		}
	}
	for len(c.sessions) >= maxSessions {
		var oldest string
		for id, s := range c.sessions {
			if oldest == "" || s.expires.Before(c.sessions[oldest].expires) {
				oldest = id
			}
		}
		delete(c.sessions, oldest)
	}
	id := randHex(32)
	s := &session{csrf: randHex(32), expires: now.Add(c.cfg.SessionTTL)}
	c.sessions[id] = s
	return id, s
}

func (c *Console) session(r *http.Request) (string, *session) {
	ck, err := r.Cookie(cookieName)
	if err != nil || ck.Value == "" {
		return "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.sessions[ck.Value]
	if s == nil {
		return "", nil
	}
	if !c.cfg.Now().Before(s.expires) {
		delete(c.sessions, ck.Value)
		return "", nil
	}
	return ck.Value, s
}

func (c *Console) setFlash(s *session, isErr bool, format string, args ...any) {
	c.mu.Lock()
	s.flash = &flash{Error: isErr, Text: fmt.Sprintf(format, args...)}
	c.mu.Unlock()
}

func (c *Console) setReveal(s *session, rv *reveal) {
	c.mu.Lock()
	s.reveal = rv
	c.mu.Unlock()
}

// takeReveal returns and clears the pending token when it belongs to org.
func (c *Console) takeReveal(s *session, org string) *reveal {
	c.mu.Lock()
	defer c.mu.Unlock()
	rv := s.reveal
	if rv == nil || rv.org != org {
		return nil
	}
	s.reveal = nil
	return rv
}

func (c *Console) takeFlash(s *session) *flash {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := s.flash
	s.flash = nil
	return f
}

// loginAllowed spends one failure token; false = throttled.
func (c *Console) loginThrottled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.cfg.Now()
	c.failures -= now.Sub(c.failedAt).Seconds() / loginRefill.Seconds()
	if c.failures < 0 {
		c.failures = 0
	}
	c.failedAt = now
	return c.failures >= loginBurst
}

func (c *Console) noteFailure() {
	c.mu.Lock()
	c.failures++
	c.mu.Unlock()
}

// ---- middleware -------------------------------------------------------------

func securityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Cache-Control", "no-store")
}

// hostOK refuses a Host header that does not name a loopback host: a page on
// a rebinding domain reaches 127.0.0.1 under its own name, and is refused.
func hostOK(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return loopbackHost(strings.Trim(host, "[]"))
}

// sameOrigin requires an Origin, or failing that a Referer, naming this
// console. A request carrying neither is refused.
func sameOrigin(r *http.Request) bool {
	self := "http://" + r.Host
	if o := r.Header.Get("Origin"); o != "" {
		return o == self
	}
	ref := r.Header.Get("Referer")
	if ref == "" {
		return false
	}
	u, err := url.Parse(ref)
	return err == nil && u.Scheme == "http" && u.Host == r.Host
}

type ctxHandler func(w http.ResponseWriter, r *http.Request, s *session)

// authed requires a live session; a POST also needs same-origin and the
// session's CSRF token.
func (c *Console) authed(h ctxHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, s := c.session(r)
		if s == nil {
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			if !sameOrigin(r) {
				http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxForm)
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.PostFormValue("csrf")), []byte(s.csrf)) != 1 {
				http.Error(w, "forbidden: bad CSRF token", http.StatusForbidden)
				return
			}
		}
		h(w, r, s)
	}
}

// maxForm bounds a form body (a pasted policy is the largest).
const maxForm = 256 << 10

// Handler returns the routed console.
func (c *Console) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /login", c.loginPage)
	mux.HandleFunc("POST /login", c.login)
	mux.HandleFunc("POST /logout", c.authed(c.logout))
	mux.HandleFunc("GET /{$}", c.authed(c.overview))
	mux.HandleFunc("POST /orgs", c.authed(c.createOrg))
	mux.HandleFunc("GET /orgs/{org}", c.authed(c.orgPage))
	mux.HandleFunc("POST /orgs/{org}/enroll", c.authed(c.createEnroll))
	mux.HandleFunc("POST /orgs/{org}/enroll/{id}/revoke", c.authed(c.revokeEnroll))
	mux.HandleFunc("GET /orgs/{org}/events", c.authed(c.eventsPage))
	mux.HandleFunc("GET /orgs/{org}/items/{id}", c.authed(c.itemPage))
	mux.HandleFunc("POST /orgs/{org}/items/{id}/resolve", c.authed(c.resolveItem))
	mux.HandleFunc("POST /orgs/{org}/items/{id}/reopen", c.authed(c.reopenItem))
	mux.HandleFunc("POST /orgs/{org}/devices/{id}/revoke", c.authed(c.revokeDevice))
	mux.HandleFunc("POST /orgs/{org}/items/add", c.authed(c.addItem))
	mux.HandleFunc("POST /orgs/{org}/items/purge", c.authed(c.purgeState))
	mux.HandleFunc("POST /orgs/{org}/items/{id}/purge", c.authed(c.purgeItem))
	mux.HandleFunc("POST /orgs/{org}/linear", c.authed(c.setLinear))
	mux.HandleFunc("POST /orgs/{org}/linear-key", c.authed(c.setLinearKey))
	mux.HandleFunc("POST /orgs/{org}/digest", c.authed(c.setDigest))
	mux.HandleFunc("POST /orgs/{org}/policy/keys/{id}/remove", c.authed(c.removeKey))
	mux.HandleFunc("POST /create-codes", c.authed(c.createCode))
	mux.HandleFunc("POST /orgs/{org}/policy/keys", c.authed(c.addKey))
	mux.HandleFunc("POST /orgs/{org}/policy/publish", c.authed(c.publish))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w)
		if !hostOK(r) {
			http.Error(w, "forbidden: not a loopback host", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// ---- login --------------------------------------------------------------------

func (c *Console) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, s := c.session(r); s != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	c.render(w, http.StatusOK, "login", nil, pageData{Title: "Sign in"})
}

func (c *Console) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// Compare first: the throttle only slows WRONG guesses, so a loopback
	// process spamming failures cannot lock the real admin out. The token
	// is 256 bits of randomness; the throttle is defence in depth.
	got := strings.TrimSpace(r.PostFormValue("token"))
	if subtle.ConstantTimeCompare([]byte(got), []byte(c.cfg.Token)) != 1 {
		if c.loginThrottled() {
			c.render(w, http.StatusTooManyRequests, "login", nil, pageData{Title: "Sign in", Flash: &flash{Error: true, Text: "Too many failed attempts. Wait a minute and try again."}})
			return
		}
		c.noteFailure()
		c.logf("console: failed login")
		c.render(w, http.StatusUnauthorized, "login", nil, pageData{Title: "Sign in", Flash: &flash{Error: true, Text: "That is not the admin token."}})
		return
	}
	id, _ := c.newSession()
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: id, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: int(c.cfg.SessionTTL.Seconds()),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (c *Console) logout(w http.ResponseWriter, r *http.Request, _ *session) {
	if id, _ := c.session(r); id != "" {
		c.mu.Lock()
		delete(c.sessions, id)
		c.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
