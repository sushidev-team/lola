package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/doctor"
	"github.com/sushidev-team/lola/internal/secrets"
)

func stubSandboxSvc(t *testing.T) (stored *string, deleted *bool) {
	t.Helper()
	st, del, look, chk := storeSandboxToken, deleteSandboxToken, lookupSandboxToken, checkSandbox
	t.Cleanup(func() { storeSandboxToken, deleteSandboxToken, lookupSandboxToken, checkSandbox = st, del, look, chk })
	var s string
	var d bool
	storeSandboxToken = func(service, v string) error {
		if service != secrets.SandboxClaudeService {
			t.Errorf("service %q", service)
		}
		s = v
		return nil
	}
	deleteSandboxToken = func(string) error { d = true; return nil }
	lookupSandboxToken = func(string) (string, bool, error) { return "sk-ant-oat-SECRET", true, nil }
	checkSandbox = func(_ context.Context, cfg *config.Config) doctor.SandboxStatus {
		return doctor.SandboxStatus{Docker: "docker", DockerOK: true, DefaultImage: config.DefaultSandboxImage(), ClaudeSource: secrets.SourceKeychain}
	}
	return &s, &d
}

func TestSandboxTokenIsWriteOnly(t *testing.T) {
	writeTestConfig(t, minimalConfig)
	stored, deleted := stubSandboxSvc(t)
	s := &ConfigService{}
	if err := s.SetSandboxToken("  sk-ant-oat01-x \n"); err != nil || *stored != "sk-ant-oat01-x" {
		t.Fatalf("%v %q", err, *stored)
	}
	for _, bad := range []string{"", "a b"} {
		if err := s.SetSandboxToken(bad); err == nil || (bad != "" && strings.Contains(err.Error(), bad)) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	st, err := s.SandboxStatus()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(st)
	if !st.TokenStored || !st.DockerOK || strings.Contains(string(b), "SECRET") {
		t.Fatalf("status %s", b)
	}
	if err := s.ClearSandboxToken(); err != nil || !*deleted {
		t.Fatalf("clear: %v", err)
	}
}

func TestSetSandboxImage(t *testing.T) {
	writeTestConfig(t, minimalConfig)
	stubSandboxSvc(t)
	s := &ConfigService{}
	if got := s.SandboxImageDefault(); got != config.DefaultSandboxImage() {
		t.Fatalf("default = %q", got)
	}
	if err := s.SetSandboxImage(" acme/base:1 "); err != nil {
		t.Fatal(err)
	}
	if got := s.SandboxImageDefault(); got != "acme/base:1" {
		t.Fatalf("after set = %q", got)
	}
	if err := s.SetSandboxImage("a b"); err == nil {
		t.Fatal("accepted a space")
	}
	if err := s.SetSandboxImage(""); err != nil || s.SandboxImageDefault() != config.DefaultSandboxImage() {
		t.Fatalf("reset: %v", err)
	}
}
