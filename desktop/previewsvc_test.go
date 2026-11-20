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
	if _, err := s.Open("s1", "http://127.0.0.1:1/"); err == nil {
		t.Error("an address the daemon never derived opened")
	}
	if _, err := s.Open("s1", "http://example.com"); err == nil {
		t.Error("a non-loopback address opened")
	}
}
