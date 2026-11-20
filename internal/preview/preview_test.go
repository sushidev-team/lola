package preview

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
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

func get(t *testing.T, url string, mod func(*http.Request)) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if mod != nil {
		mod(req)
	}
	resp, err := http.DefaultClient.Do(req)
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
	addr, err := s.Open(up.URL + "/start?q=1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(addr, "/start?q=1") || !strings.HasPrefix(addr, "http://127.0.0.1:") {
		t.Fatalf("addr = %s", addr)
	}
	base := addr[:strings.Index(addr, "/start")]
	if again, _ := s.Open(up.URL); !strings.HasPrefix(again, base) {
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

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r2, _ := client.Get(base + "/go")
	if loc := r2.Header.Get("Location"); loc != "http://"+strings.TrimPrefix(base, "http://")+"/dest" {
		t.Errorf("redirect left the proxy: %s", loc)
	}
}

func TestProxyRefusesForeignHost(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	s := New()
	defer s.Close()
	addr, _ := s.Open(up.URL)
	resp := get(t, addr, func(r *http.Request) { r.Host = "rebind.example.com" })
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestOpenRefusesNonLoopback(t *testing.T) {
	s := New()
	defer s.Close()
	if _, err := s.Open("http://10.0.0.1:3000"); err == nil {
		t.Fatal("LAN address opened")
	}
}
