package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPreviewOpenOnlyForTheSessionsOwnDevURLs(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	s := &PreviewService{devURLs: func(id string) ([]string, error) {
		if id != "s1" {
			return nil, nil
		}
		return []string{up.URL}, nil
	}}
	defer s.ServiceShutdown()
	if addr, err := s.Open("s1", up.URL); err != nil || !strings.HasPrefix(addr, "http://127.0.0.1:") {
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
