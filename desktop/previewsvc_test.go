package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

func TestPreviewOpenOnlyForTheSessionsOwnDevURLs(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	startFakeDaemon(t, func(req protocol.Request) protocol.Response {
		data, _ := json.Marshal(protocol.SessionsData{Sessions: []protocol.SessionInfo{
			{ID: "s1", Apps: []protocol.SessionApp{{Name: "web", DevURLs: []string{up.URL}}}},
			{ID: "s2", DevURLs: []string{"http://localhost:3000"}},
		}})
		return protocol.Response{OK: true, Data: data}
	})
	s := &PreviewService{}
	defer s.ServiceShutdown()
	if addr, err := s.Open("s1", up.URL); err != nil || !strings.Contains(addr, ".localhost:") {
		t.Fatalf("own dev url: %q, %v", addr, err)
	}
	if _, err := s.Open("s2", up.URL); err == nil {
		t.Error("another session's address opened")
	}
	if addr, err := s.Open("s1", up.URL+"/settings?tab=a"); err != nil || !strings.HasSuffix(addr, "/settings?tab=a") {
		t.Errorf("a path on the session's own dev server: %q, %v", addr, err)
	}
	if _, err := s.Open("s1", "http://127.0.0.1:1/"); err == nil {
		t.Error("an address the daemon never derived opened")
	}
	if _, err := s.Open("s1", "http://example.com"); err == nil {
		t.Error("a non-loopback address opened")
	}
}

func TestSameOriginIgnoresHostCaseAndDefaultPorts(t *testing.T) {
	for _, c := range []struct {
		owned, asked string
		want         bool
	}{
		{"http://localhost:3000", "http://LOCALHOST:3000/settings", true},
		{"http://localhost", "http://localhost:80/x", true},
		{"https://localhost:443", "https://localhost/x", true},
		{"http://localhost:3000", "http://localhost:3001/", false},
		{"http://localhost", "https://localhost/", false},
		{"http://localhost:3000", "http://127.0.0.1:3000/", false},
	} {
		if got := sameOrigin(c.owned, c.asked); got != c.want {
			t.Errorf("sameOrigin(%q, %q) = %v", c.owned, c.asked, got)
		}
	}
}
