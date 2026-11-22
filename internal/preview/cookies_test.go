package preview

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPreviewCookieScopeAndLifecycle(t *testing.T) {
	h := make(http.Header)
	h.Add("Set-Cookie", "session=private; Domain=.app.test; HttpOnly; SameSite=Lax; Path=/dashboard")
	h.Add("Set-Cookie", "XSRF-TOKEN=one%3D; Path=/; Max-Age=3600")
	h.Add("Set-Cookie", "irrelevant=invalid; Domain=other.test")
	h.Add("Set-Cookie", xsrfCopy+"=reserved; Path=/")
	frameCookies(h, "dev.app.test", "p-example.localhost")
	cookies := (&http.Response{Header: h}).Cookies()
	if len(cookies) != 3 {
		t.Fatalf("got %d cookies", len(cookies))
	}
	for _, c := range cookies {
		if !c.Secure || !c.Partitioned || c.Domain != "" || c.SameSite != http.SameSiteNoneMode {
			t.Errorf("wrong scope for %s", c.Name)
		}
	}
	if !cookies[0].HttpOnly || cookies[0].Path != "/dashboard" || cookies[1].HttpOnly || !cookies[2].HttpOnly || cookies[2].Name != xsrfCopy || cookies[2].MaxAge != 3600 || cookies[2].Value != "one%3D" {
		t.Fatal("lost session or token attributes")
	}
	for _, update := range []string{"XSRF-TOKEN=; Path=/; Max-Age=0", "XSRF-TOKEN=private; Path=/; HttpOnly"} {
		h.Set("Set-Cookie", update)
		frameCookies(h, "dev.app.test", "p-example.localhost")
		cs := (&http.Response{Header: h}).Cookies()
		if len(cs) != 2 || cs[1].Name != xsrfCopy || cs[1].MaxAge >= 0 || cs[1].Value != "" {
			t.Fatal("script-readable copy survived deletion or HttpOnly update")
		}
	}
}

func TestPreviewTokenReadIsSameOriginAndCurrent(t *testing.T) {
	for _, tc := range []struct {
		name, site, ref, cookies, body string
		status                         int
	}{
		{"current", "same-origin", "", "session=private; XSRF-TOKEN=now; " + xsrfCopy + "=now", "now", 200},
		{"synchronous WebKit", "", "http://preview.localhost:123/login", "XSRF-TOKEN=now; " + xsrfCopy + "=now", "now", 200},
		{"foreign", "cross-site", "", xsrfCopy + "=now; XSRF-TOKEN=now", "", 403},
		{"foreign referrer", "", "http://other.localhost:123/", xsrfCopy + "=now; XSRF-TOKEN=now", "", 403},
		{"http only", "same-origin", "", "XSRF-TOKEN=private", "", 200},
		{"stale", "same-origin", "", "XSRF-TOKEN=current; " + xsrfCopy + "=old", "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://preview.localhost:123/login", nil)
			r.Header.Set("Sec-Fetch-Site", tc.site)
			r.Header.Set("Referer", tc.ref)
			r.Header.Set("Cookie", tc.cookies)
			w := httptest.NewRecorder()
			readPreviewXSRF(w, r)
			if w.Code != tc.status || w.Body.String() != tc.body || w.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("unexpected status, response, or caching policy: %d", w.Code)
			}
		})
	}
}

func TestPreviewLoginOriginAndIsolation(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Origin") != "http://"+r.Host || r.Referer() != "http://"+r.Host+"/login" || r.Header.Get("Cookie") != "session=private; XSRF-TOKEN=one" || r.Header.Get("X-XSRF-TOKEN") != "one" {
			t.Error("upstream received incorrect credentials or origin")
		}
		w.Header().Set("X-Inertia-Location", "http://"+r.Host+"/home")
		w.WriteHeader(http.StatusConflict)
	}))
	defer up.Close()
	s := New()
	defer s.Close()
	open := func(session, target string) *url.URL {
		t.Helper()
		addr, err := s.Open(session, target)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(addr)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	a := open("one", up.URL)
	if a.Hostname() == open("two", up.URL).Hostname() || a.Hostname() == open("one", "http://localhost:5173").Hostname() || a.Host != open("one", up.URL+"/login").Host {
		t.Fatal("preview cookie host is not isolated or stable")
	}
	base := strings.TrimSuffix(a.String(), "/")
	post := func(origin string) *http.Response {
		t.Helper()
		r, _ := http.NewRequest("POST", base+"/login", nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("Referer", base+"/login")
		r.Header.Set("Cookie", "session=private; "+xsrfCopy+"=one; XSRF-TOKEN=one")
		r.Header.Set("X-XSRF-TOKEN", "one")
		resp, err := previewClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	if resp := post(base); resp.StatusCode != 409 || resp.Header.Get("X-Inertia-Location") != base+"/home" {
		t.Error("Inertia redirect left the preview")
	}
	if resp := post("http://other.localhost"); resp.StatusCode != 403 {
		t.Error("foreign form submission forwarded")
	}
	resp := get(t, base+ScriptPath, func(r *http.Request) { r.AddCookie(&http.Cookie{Name: xsrfCopy, Value: "one"}) })
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "X-Lola-Preview-Read-XSRF") {
		t.Error("missing WebKit XSRF support")
	}
}

// A proxy-aware app (such as Laravel with Ziggy) generates the login target
// inside its page. Rewriting a redirect after the POST is already too late.
func TestPreviewAbsoluteLoginURL(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Header.Get("X-Forwarded-Host")
		if r.Host == host {
			t.Error("lost the upstream virtual host")
		}
		name, port, err := net.SplitHostPort(host)
		if err != nil || r.Header.Get("X-Forwarded-Port") != port {
			t.Error("missing browser-facing port")
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "guest", Domain: name, Path: "/", HttpOnly: true})
		io.WriteString(w, r.Header.Get("X-Forwarded-Proto")+"://"+host+"/login")
	}))
	defer up.Close()
	s := New()
	defer s.Close()
	addr, err := s.Open("app", up.URL+"/login")
	if err != nil {
		t.Fatal(err)
	}
	resp := get(t, addr, func(r *http.Request) {
		r.Header.Set("X-Forwarded-Host", "other.test")
		r.Header.Set("X-Forwarded-Port", "443")
		r.Header.Set("X-Forwarded-Proto", "https")
	})
	defer resp.Body.Close()
	target, err := io.ReadAll(resp.Body)
	if err != nil || string(target) != addr {
		t.Fatalf("login target = %q, want %q: %v", target, addr, err)
	}
	cookies := resp.Cookies()
	if len(cookies) != 1 || cookies[0].Name != "session" || cookies[0].Domain != "" || !cookies[0].HttpOnly || !cookies[0].Partitioned {
		t.Fatal("session cookie issued for the forwarded host must stay scoped to this preview")
	}
}
