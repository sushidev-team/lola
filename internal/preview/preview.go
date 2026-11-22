// Package preview serves a session's dev server to the desktop app's preview
// pane, with an element picker injected into its pages.
//
// WHY A PROXY. The pane is an iframe, and a page on another origin cannot be
// read or scripted from the app, so "click an element, get its selector and
// HTML" is impossible against the dev server directly. Each target instead gets
// its own listener on a random loopback port that forwards to it, strips the
// headers that forbid framing (X-Frame-Options, CSP frame-ancestors) and adds
// one script to HTML responses. The script talks to its parent frame by
// postMessage; nothing is added to the dev server or the project.
//
// THE RAIL. Allowed is the only way a target gets in: http(s) and a loopback
// host, the same hosts internal/devurl will ever return. The CALLER must also
// match the URL against the session's own derived dev URLs — without that a
// client picks its own port and this becomes a proxy into everything on the
// Mac's loopback (the same caveat as internal/devforward).
//
// Stdlib only.
package preview

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	_ "embed"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed picker.js
var pickerJS string

//go:embed prepare.js
var prepareJS string

// PrepareJS is the script that prepares a reviewer's view (scroll, outline,
// wait). The screenshot capture injects the SAME source, so a screenshot and the
// live tab show one view.
func PrepareJS() string { return prepareJS }

//go:embed xsrf.js
var xsrfJS string

// ScriptPath is where the proxy serves the picker. A script tag with a src is
// allowed by a dev server's `script-src 'self'`, which an inline one is not.
const ScriptPath = "/__lola_preview/picker.js"

// maxHTML bounds a page held in memory to inject the script. A larger HTML
// response streams through unmodified (the picker is then simply absent).
const maxHTML = 8 << 20

// ErrNotLoopback is returned for a URL the preview must not proxy.
var ErrNotLoopback = errors.New("preview: only http(s) loopback addresses can be previewed")

// Allowed parses raw and reports it only when it is http(s) with a loopback
// host (localhost, 127.0.0.0/8, ::1, 0.0.0.0, or a local development TLD).
func Allowed(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil {
		return nil, ErrNotLoopback
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrNotLoopback
	}
	if !loopbackHost(u.Hostname()) {
		return nil, ErrNotLoopback
	}
	return u, nil
}

func loopbackHost(h string) bool {
	h = strings.ToLower(h)
	switch h {
	case "localhost", "0.0.0.0", "host.docker.internal":
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	for _, sfx := range []string{".test", ".localhost", ".local"} {
		if strings.HasSuffix(h, sfx) {
			return true
		}
	}
	return false
}

type previewKey struct {
	session, origin string
}

// Server owns a separate browser cookie host per session and upstream origin.
type Server struct {
	mu     sync.Mutex
	byHost map[previewKey]*proxy
	closed bool
}

type proxy struct {
	addr   string
	srv    *http.Server
	cancel context.CancelFunc
}

// New returns an empty Server.
func New() *Server { return &Server{byHost: map[previewKey]*proxy{}} }

// Open returns the proxied address for target, starting a listener for its
// origin when there is none. The path and query are kept, so the pane opens
// the page the dev server printed.
func (s *Server) Open(session, target string) (string, error) {
	u, err := Allowed(target)
	if err != nil {
		return "", err
	}
	origin := canonicalOrigin(u)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", errors.New("preview: closed")
	}
	key := previewKey{session, origin.String()}
	p := s.byHost[key]
	if p == nil {
		if p, err = s.start(origin); err != nil {
			return "", err
		}
		s.byHost[key] = p
	}
	out := *u
	out.Scheme, out.Host = "http", p.addr
	if out.Path == "" {
		out.Path = "/"
	}
	return out.String(), nil
}

// Retain closes listeners whose upstream origin the daemon no longer reports.
func (s *Server) Retain(owns func(session, origin string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, p := range s.byHost {
		if !owns(key.session, key.origin) {
			p.close()
			delete(s.byHost, key)
		}
	}
}

func (p *proxy) close() {
	// ReverseProxy also needs cancellation for hijacked WebSocket connections.
	p.cancel()
	_ = p.srv.Close()
}

// Close stops every listener.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for k, p := range s.byHost {
		p.close()
		delete(s.byHost, k)
	}
}

func (s *Server) start(origin *url.URL) (*proxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	// Cookies ignore port numbers. A distinct .localhost name keeps even
	// identically named session cookies apart without leaving loopback.
	addr := net.JoinHostPort("p-"+strings.ToLower(rand.Text())+".localhost", strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	ctx, cancel := context.WithCancel(context.Background())
	p := &proxy{addr: addr, cancel: cancel}
	self := &url.URL{Scheme: "http", Host: addr}
	rp := &httputil.ReverseProxy{
		Transport: upstreamTransport,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(origin)
			r.Out.Host = origin.Host
			// Keep virtual-host routing upstream, but let proxy-aware apps
			// generate absolute links and form actions on the browser's origin.
			// The listener owns these values, including its non-default port.
			r.Out.Header.Set("X-Forwarded-Host", self.Host)
			r.Out.Header.Set("X-Forwarded-Proto", self.Scheme)
			r.Out.Header.Set("X-Forwarded-Port", self.Port())
			// An identity body is the only one the script can be injected into.
			r.Out.Header.Set("Accept-Encoding", "identity")
			// Preserve foreign origins so upstream CSRF checks see the actual caller.
			for _, key := range []string{"Origin", "Referer"} {
				rewriteOrigin(r.Out.Header, key, self, origin)
			}
			stripPreviewCookie(r.Out)
		},
		ModifyResponse: func(resp *http.Response) error { return rewriteResponse(resp, origin, self) },
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "The dev server at "+origin.Host+" is not answering.", http.StatusBadGateway)
		},
	}
	p.srv = &http.Server{
		Handler:           hostGuard(addr, rp),
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = p.srv.Serve(ln) }()
	return p, nil
}

// The dialer checks each resolved IP before connect, including fallback attempts.
// Keep the hostname for TLS and HTTP; an environment proxy would bypass this check.
var upstreamTransport = func() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: checkLoopbackIP}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
			host = "::1"
			if ip.To4() != nil {
				host = "127.0.0.1"
			}
			addr = net.JoinHostPort(host, port)
		}
		return dialer.DialContext(ctx, network, addr)
	}
	return transport
}()

func checkLoopbackIP(_, addr string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if !net.ParseIP(host).IsLoopback() {
		return ErrNotLoopback
	}
	return nil
}

// hostGuard refuses a request whose Host is not the listener's own address: a
// DNS-rebinding page would otherwise reach a dev server through this port.
func hostGuard(addr string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != addr {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		// SameSite=None is needed in the iframe. Reject cross-origin writes
		// before giving the upstream any of the preview's credentials.
		if r.Header.Get("Upgrade") != "" || (r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions) {
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+addr {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		if r.Header.Get("X-Lola-Preview-Read-XSRF") != "" {
			readPreviewXSRF(w, r)
			return
		}
		if r.URL.Path == ScriptPath {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			// Install the WebKit fallback only for apps that actually issued
			// a script-readable XSRF cookie.
			if _, err := r.Cookie(xsrfCopy); err == nil {
				_, _ = io.WriteString(w, xsrfJS)
			}
			_, _ = io.WriteString(w, prepareJS)
			_, _ = io.WriteString(w, pickerJS)
			return
		}
		next.ServeHTTP(w, r)
	})
}

var frameAncestorsRe = regexp.MustCompile(`(?i)(^|;)\s*frame-ancestors[^;]*`)

func rewriteOrigin(h http.Header, key string, from, to *url.URL) {
	value := h.Get(key)
	if value == "" {
		return
	}
	u, err := url.Parse(value)
	if err == nil && u.User == nil && u.Scheme == from.Scheme && strings.EqualFold(canonicalOrigin(u).Host, from.Host) {
		u.Scheme, u.Host = to.Scheme, to.Host
		h.Set(key, u.String())
	}
}

func rewriteResponse(resp *http.Response, origin, self *url.URL) error {
	h := resp.Header
	h.Del("X-Frame-Options")
	for _, key := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		vals := h.Values(key)
		if len(vals) == 0 {
			continue
		}
		h.Del(key)
		for _, v := range vals {
			if v = strings.Trim(strings.TrimSpace(frameAncestorsRe.ReplaceAllString(v, "$1")), "; "); v != "" {
				h.Add(key, v)
			}
		}
	}
	rewriteOrigin(h, "Location", origin, self)
	rewriteOrigin(h, "X-Inertia-Location", origin, self)
	frameCookies(h, origin.Hostname(), self.Hostname())
	if !strings.HasPrefix(strings.ToLower(h.Get("Content-Type")), "text/html") ||
		resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified ||
		h.Get("Content-Encoding") != "" && !strings.EqualFold(h.Get("Content-Encoding"), "gzip") {
		return nil
	}
	if resp.ContentLength > maxHTML {
		return nil
	}
	var rd io.Reader = resp.Body
	if strings.EqualFold(h.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil
		}
		rd = zr
	}
	body, err := io.ReadAll(io.LimitReader(rd, maxHTML+1))
	if err != nil {
		_ = resp.Body.Close()
		return err
	}
	if len(body) > maxHTML {
		// The length was unknown, or the page expanded beyond the limit.
		// Replay the decoded prefix and stream the rest from the same reader.
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), rd), resp.Body}
		h.Del("Content-Length")
		h.Del("Content-Encoding")
		resp.ContentLength = -1
		return nil
	}
	_ = resp.Body.Close()
	body = Inject(body)
	h.Del("Content-Encoding")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	resp.ContentLength = int64(len(body))
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return nil
}

var headCloseRe = regexp.MustCompile(`(?i)</head\s*>`)

// Inject adds the picker script to an HTML document: before </head> when
// there is one, else at the very start (after a doctype, which must stay first).
func Inject(html []byte) []byte {
	tag := []byte(`<script src="` + ScriptPath + `"></script>`)
	if loc := headCloseRe.FindIndex(html); loc != nil {
		return concat(html[:loc[0]], tag, html[loc[0]:])
	}
	at := 0
	if len(html) > 2 && html[0] == '<' && html[1] == '!' {
		if i := bytes.IndexByte(html, '>'); i >= 0 {
			at = i + 1
		}
	}
	return concat(html[:at], tag, html[at:])
}

func concat(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}

// canonicalOrigin is the proxy key and upstream for a URL: lowercase host, and
// no port when it is the scheme's default. Two spellings of one server must share
// a proxy, or each gets its own cookie host and a login does not carry over.
func canonicalOrigin(u *url.URL) *url.URL {
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return &url.URL{Scheme: u.Scheme, Host: host}
}
