package preview

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAllowed(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1:8000", "http://localhost:5173/x?y=1", "https://app.test/", "http://[::1]:3000", "http://0.0.0.0:80"} {
		if _, err := Allowed(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "file:///etc/passwd", "javascript:alert(1)", "http://192.168.1.5:3000", "http://example.com", "http://user@127.0.0.1", "ftp://localhost", "http://localhost.evil.com"} {
		if _, err := Allowed(bad); err == nil {
			t.Errorf("%s allowed", bad)
		}
	}
}

func TestInject(t *testing.T) {
	got := string(Inject([]byte("<!doctype html><html><head><title>x</title></head><body></body></html>")))
	if !strings.Contains(got, `<script src="`+ScriptPath+`"></script></head>`) {
		t.Fatalf("not before </head>: %s", got)
	}
	got = string(Inject([]byte("<!DOCTYPE html><p>hi")))
	if !strings.HasPrefix(got, "<!DOCTYPE html><script") {
		t.Fatalf("doctype must stay first: %s", got)
	}
}

// WebKit resolves *.localhost to loopback even on hosts whose system DNS
// does not. Dial the listener directly while preserving the browser Host.
var previewClient = &http.Client{
	Transport: &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
	}},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func get(t *testing.T, url string, mod func(*http.Request)) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if mod != nil {
		mod(req)
	}
	resp, err := previewClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestProxyInjectsAndUnframes(t *testing.T) {
	var sawEnc, sawHost string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawEnc, sawHost = r.Header.Get("Accept-Encoding"), r.Host
		switch r.URL.Path {
		case "/gz":
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			_, _ = zw.Write([]byte("<html><head></head><body>zipped</body></html>"))
			_ = zw.Close()
		case "/api":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"a":1}`))
		case "/go":
			http.Redirect(w, r, "http://"+r.Host+"/dest", http.StatusFound)
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
			_, _ = w.Write([]byte("<html><head></head><body>hello</body></html>"))
		}
	}))
	defer up.Close()
	s := New()
	defer s.Close()
	addr, err := s.Open("session", up.URL+"/start/a%2Fb?q=1#/a%2Fb")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(addr, "/start/a%2Fb?q=1#/a%2Fb") || !strings.Contains(addr, ".localhost:") {
		t.Fatalf("addr = %s", addr)
	}
	base := addr[:strings.Index(addr, "/start")]
	if again, _ := s.Open("session", up.URL); !strings.HasPrefix(again, base) {
		t.Errorf("a second open of one origin must reuse its listener: %s vs %s", again, base)
	}

	resp := get(t, addr, nil)
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), ScriptPath) || !strings.Contains(string(b), "hello") {
		t.Errorf("body = %s", b)
	}
	if resp.Header.Get("X-Frame-Options") != "" {
		t.Error("X-Frame-Options kept")
	}
	if csp := resp.Header.Get("Content-Security-Policy"); strings.Contains(csp, "frame-ancestors") || !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("csp = %q", csp)
	}
	if sawEnc != "identity" || sawHost != strings.TrimPrefix(up.URL, "http://") {
		t.Errorf("upstream saw enc=%q host=%q", sawEnc, sawHost)
	}

	resp = get(t, base+"/gz", nil)
	b, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "zipped") || !strings.Contains(string(b), ScriptPath) {
		t.Errorf("gzip body = %s", b)
	}

	resp = get(t, base+"/api", nil)
	if b, _ = io.ReadAll(resp.Body); string(b) != `{"a":1}` {
		t.Errorf("non-HTML was touched: %s", b)
	}

	resp = get(t, base+ScriptPath, nil)
	if b, _ = io.ReadAll(resp.Body); !strings.Contains(string(b), "__lolaPreview") {
		t.Errorf("picker not served")
	}

	r2 := get(t, base+"/go", nil)
	if loc := r2.Header.Get("Location"); loc != "http://"+strings.TrimPrefix(base, "http://")+"/dest" {
		t.Errorf("redirect left the proxy: %s", loc)
	}
}

func TestProxyRefusesForeignHost(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	s := New()
	defer s.Close()
	addr, _ := s.Open("session", up.URL)
	resp := get(t, addr, func(r *http.Request) { r.Host = "rebind.example.com" })
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestOpenRefusesNonLoopback(t *testing.T) {
	s := New()
	defer s.Close()
	if _, err := s.Open("session", "http://10.0.0.1:3000"); err == nil {
		t.Fatal("LAN address opened")
	}
}

func TestOpenReusesTheProxyAcrossEquivalentSpellings(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer up.Close()
	s := New()
	defer s.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(up.URL, "http://"))
	a, err := s.Open("s", "http://localhost:"+port)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Open("s", "http://LOCALHOST:"+port+"/settings?tab=a")
	if err != nil {
		t.Fatal(err)
	}
	hostOf := func(raw string) string { u, _ := url.Parse(raw); return u.Host }
	if hostOf(a) != hostOf(b) {
		t.Fatalf("one server got two proxies: %s vs %s", a, b)
	}
	if !strings.HasSuffix(b, "/settings?tab=a") {
		t.Fatalf("path and query lost: %s", b)
	}
	// An explicit default port is the same origin as none.
	if x, y := canonicalOrigin(mustURL("http://localhost:80")), canonicalOrigin(mustURL("http://LocalHost")); x.String() != y.String() {
		t.Fatalf("default port differs: %s vs %s", x, y)
	}
	if canonicalOrigin(mustURL("https://localhost:443")).Host != "localhost" || canonicalOrigin(mustURL("http://localhost:443")).Host != "localhost:443" {
		t.Fatal("default port stripped for the wrong scheme")
	}
}

func mustURL(raw string) *url.URL { u, _ := url.Parse(raw); return u }

// The live tab prepares itself through the served script, so the proxy must
// serve the shared prepare script together with the picker, and the picker must
// answer a prepare message by running it.
func TestProxyServesThePrepareScriptWithThePicker(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head></head><body></body></html>"))
	}))
	defer up.Close()
	s := New()
	defer s.Close()
	addr, err := s.Open("session", up.URL)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(addr)
	resp := get(t, "http://"+u.Host+ScriptPath, nil)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	js := string(b)
	for _, want := range []string{"window.__lolaPrepare = function", `e.data.type === "prepare"`, `type: "prepared"`} {
		if !strings.Contains(js, want) {
			t.Errorf("served script lacks %q", want)
		}
	}
	if strings.Index(js, "window.__lolaPrepare = function") > strings.Index(js, `e.data.type === "prepare"`) {
		t.Error("the picker is served before the script it calls")
	}
	if PrepareJS() == "" || !strings.Contains(js, PrepareJS()) {
		t.Error("the screenshot script and the served script differ")
	}
}

func TestRedirectsWithAnExplicitDefaultPortStillRewrite(t *testing.T) {
	h := http.Header{}
	h.Set("Location", "http://LOCALHOST:80/dashboard?x=1")
	from := &url.URL{Scheme: "http", Host: "localhost"}
	to := &url.URL{Scheme: "http", Host: "127.0.0.1:9999"}
	rewriteOrigin(h, "Location", from, to)
	if got := h.Get("Location"); got != "http://127.0.0.1:9999/dashboard?x=1" {
		t.Fatalf("Location = %q", got)
	}
	h.Set("Location", "http://evil.test/x")
	rewriteOrigin(h, "Location", from, to)
	if h.Get("Location") != "http://evil.test/x" {
		t.Fatal("a foreign origin was rewritten")
	}
}

func TestPreviewTransportDialsOnlyLoopback(t *testing.T) {
	if err := checkLoopbackIP("tcp4", "192.0.2.1:80", nil); !errors.Is(err, ErrNotLoopback) {
		t.Fatalf("off-host destination: %v", err)
	}
	up := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer up.Close()
	client := &http.Client{Transport: upstreamTransport}
	resp, err := client.Get(up.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("accepted an untrusted dev certificate")
	}
	var certErr *tls.CertificateVerificationError
	if !errors.As(err, &certErr) {
		t.Fatalf("wanted TLS verification, got %v", err)
	}
}

func TestPreviewWebSocketOrigin(t *testing.T) {
	const host = "preview.localhost:4000"
	handler := hostGuard(host, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, c := range []struct {
		origin string
		status int
	}{{"http://" + host, 204}, {"http://another.localhost", 403}} {
		req := httptest.NewRequest("GET", "http://"+host+"/", nil)
		req.Header.Set("Origin", c.origin)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != c.status {
			t.Errorf("origin %s: status %d", c.origin, w.Code)
		}
	}
}

func TestPreviewStreamsOversizeHTML(t *testing.T) {
	page := strings.Repeat("x", maxHTML) + "<footer>last byte</footer>"
	for _, encoding := range []string{"identity", "gzip"} {
		t.Run(encoding, func(t *testing.T) {
			resp := &http.Response{Header: make(http.Header), ContentLength: -1, Body: io.NopCloser(strings.NewReader(page))}
			resp.Header.Set("Content-Type", "text/html")
			if encoding == "gzip" {
				var compressed bytes.Buffer
				w := gzip.NewWriter(&compressed)
				if _, err := io.WriteString(w, page); err != nil {
					t.Fatal(err)
				}
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				resp.Body = io.NopCloser(&compressed)
				resp.ContentLength = int64(compressed.Len())
				resp.Header.Set("Content-Encoding", "gzip")
			}
			origin, _ := url.Parse("http://localhost")
			if err := rewriteResponse(resp, origin, origin); err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil || string(body) != page || resp.ContentLength != -1 || resp.Header.Get("Content-Encoding") != "" {
				t.Fatalf("incomplete passthrough: %d bytes, length %d, err %v", len(body), resp.ContentLength, err)
			}
		})
	}
}
