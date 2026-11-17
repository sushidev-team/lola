package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The transport this guard was read against. See runtimeCallGuard before changing it.
const reviewedWails = "v3.0.0-alpha2.117"

func TestRuntimeGuardWasReviewedForThisWails(t *testing.T) {
	mod, err := os.ReadFile("../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`github\.com/wailsapp/wails/v3 (\S+)`).FindSubmatch(mod)
	if m == nil {
		t.Fatal("wails is not in go.mod")
	}
	if got := string(m[1]); got != reviewedWails {
		t.Fatalf("wails is %s, and runtimeCallGuard was read against %s. Read pkg/application/transport_http.go again: does a bodyless request still take its call from the query string, and do the runtime's calls still carry x-wails-client-id? Then update reviewedWails", got, reviewedWails)
	}
}

func TestRuntimeGuard(t *testing.T) {
	const call = "/wails/runtime?object=0&method=2564028932&args=%7B%7D"
	cases := []struct {
		name    string
		method  string
		target  string
		headers map[string]string
		reached bool
	}{
		{"a frame navigating itself", http.MethodGet, call, nil, false},
		{"a navigation claiming to be the client", http.MethodGet, call, map[string]string{"x-wails-client-id": "x"}, false},
		{"a form post", http.MethodPost, call, nil, false},
		{"a preflight", http.MethodOptions, call, map[string]string{"x-wails-client-id": "x"}, false},
		{"a sandboxed fetch", http.MethodPost, "/wails/runtime", map[string]string{"x-wails-client-id": "x", "Origin": "null"}, false},
		{"the runtime's own call", http.MethodPost, "/wails/runtime", map[string]string{"x-wails-client-id": "abc", "Origin": "wails://localhost"}, true},
		{"the runtime's call with no origin", http.MethodPost, "/wails/runtime", map[string]string{"x-wails-client-id": "abc"}, true},
		{"the runtime script", http.MethodGet, "/wails/runtime.js", nil, true},
		{"the app's page", http.MethodGet, "/", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reached := false
			h := runtimeCallGuard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
			r := httptest.NewRequest(c.method, c.target, strings.NewReader(""))
			for k, v := range c.headers {
				r.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if reached != c.reached {
				t.Fatalf("reached = %v, want %v", reached, c.reached)
			}
			if !c.reached && w.Code != http.StatusForbidden {
				t.Fatalf("code = %d", w.Code)
			}
		})
	}
}

// The runtime's calls must keep carrying the header the guard asks for, or every button in
// the app stops working.
func TestTheRuntimeSendsWhatTheGuardAsksFor(t *testing.T) {
	src, err := os.ReadFile("frontend/node_modules/@wailsio/runtime/dist/runtime.js")
	if err != nil {
		t.Skip("frontend dependencies are not installed")
	}
	if !strings.Contains(string(src), "x-wails-client-id") || !strings.Contains(string(src), "POST") {
		t.Fatal("the runtime no longer posts with x-wails-client-id")
	}
}
