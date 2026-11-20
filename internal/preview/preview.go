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
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed picker.js
var pickerJS string

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

// Server owns the listeners, one per target origin.
type Server struct {
	mu     sync.Mutex
	byHost map[string]*proxy // target scheme://host → listener
	closed bool
}

type proxy struct {
	origin *url.URL
	ln     net.Listener
	srv    *http.Server
}

// New returns an empty Server.
func New() *Server { return &Server{byHost: map[string]*proxy{}} }

// Open returns the proxied address for target, starting a listener for its
// origin when there is none. The path and query are kept, so the pane opens
// the page the dev server printed.
func (s *Server) Open(target string) (string, error) {
	u, err := Allowed(target)
	if err != nil {
		return "", err
	}
	origin := &url.URL{Scheme: u.Scheme, Host: u.Host}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", errors.New("preview: closed")
	}
	p := s.byHost[origin.String()]
	if p == nil {
		if p, err = s.start(origin); err != nil {
			return "", err
		}
		s.byHost[origin.String()] = p
	}
	out := url.URL{Scheme: "http", Host: p.ln.Addr().String(), Path: u.Path, RawQuery: u.RawQuery}
	if out.Path == "" {
		out.Path = "/"
	}
	return out.String(), nil
}

// Close stops every listener.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for k, p := range s.byHost {
		_ = p.srv.Close()
		delete(s.byHost, k)
	}
}

func (s *Server) start(origin *url.URL) (*proxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &proxy{origin: origin, ln: ln}
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(origin)
			r.Out.Host = origin.Host
			// An identity body is the only one the script can be injected into.
			r.Out.Header.Set("Accept-Encoding", "identity")
			// The page believes it lives at the dev server.
			r.Out.Header.Del("Origin")
			if ref := r.In.Header.Get("Referer"); ref != "" {
				r.Out.Header.Set("Referer", strings.Replace(ref, "http://"+ln.Addr().String(), origin.String(), 1))
			}
		},
		ModifyResponse: func(resp *http.Response) error { return rewriteResponse(resp, origin, ln.Addr().String()) },
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "The dev server at "+origin.Host+" is not answering.", http.StatusBadGateway)
		},
	}
	p.srv = &http.Server{
		Handler:           hostGuard(ln.Addr().String(), rp),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = p.srv.Serve(ln) }()
	return p, nil
}

// hostGuard refuses a request whose Host is not the listener's own address: a
// DNS-rebinding page would otherwise reach a dev server through this port.
func hostGuard(addr string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != addr {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.URL.Path == ScriptPath {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = io.WriteString(w, pickerJS)
			return
		}
		next.ServeHTTP(w, r)
	})
}

var frameAncestorsRe = regexp.MustCompile(`(?i)(^|;)\s*frame-ancestors[^;]*`)

func rewriteResponse(resp *http.Response, origin *url.URL, self string) error {
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
	if loc := h.Get("Location"); loc != "" {
		if lu, err := url.Parse(loc); err == nil && lu.IsAbs() && strings.EqualFold(lu.Host, origin.Host) && lu.Scheme == origin.Scheme {
			lu.Scheme, lu.Host = "http", self
			h.Set("Location", lu.String())
		}
	}
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
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	if len(body) > maxHTML {
		return fmt.Errorf("preview: page larger than %d bytes", maxHTML)
	}
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
