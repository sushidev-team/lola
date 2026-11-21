package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/preview"
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
	s := &PreviewService{sessions: (&DaemonService{}).Sessions, srv: preview.New()}
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

func TestPreviewPollRetiresOnlyLostSessionOrigins(t *testing.T) {
	const first, second = "http://localhost:3000", "http://localhost:3001"
	data := protocol.SessionsData{Sessions: []protocol.SessionInfo{
		{ID: "stack", Apps: []protocol.SessionApp{{DevURLs: []string{first, second}}}},
		{ID: "other", DevURLs: []string{first}},
	}}
	var snapshotErr error
	s := &PreviewService{srv: preview.New(), sessions: func() (protocol.SessionsData, error) { return data, snapshotErr }}
	defer s.ServiceShutdown()
	open := func(id, target string) string {
		t.Helper()
		addr, err := s.Open(id, target)
		if err != nil {
			t.Fatal(err)
		}
		return addr
	}
	a, b, other := open("stack", first), open("stack", second), open("other", first)
	snapshotErr = errors.New("daemon unavailable")
	if _, err := s.refreshSessions(); err == nil {
		t.Fatal("expected snapshot error")
	}
	snapshotErr = nil
	if open("stack", first) != a {
		t.Fatal("failed read retired a live origin")
	}

	data.Sessions = data.Sessions[:1]
	data.Sessions[0].Apps[0].DevURLs = []string{second + "/app"}
	if _, err := s.refreshSessions(); err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{a, other} {
		u, err := url.Parse(addr)
		if err != nil {
			t.Fatal(err)
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", u.Port()), time.Second)
		if err == nil {
			conn.Close()
			t.Fatalf("retired preview still listening: %s", addr)
		}
	}
	if open("stack", second) != b {
		t.Fatal("retained stack page lost its cookie host")
	}
	data.Sessions[0].Apps[0].DevURLs = append(data.Sessions[0].Apps[0].DevURLs, first)
	if open("stack", first) == a {
		t.Fatal("new dev server inherited retired preview")
	}
}
