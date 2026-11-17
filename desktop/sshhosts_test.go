package main

import (
	"os"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
)

const sandboxConfig = minimalConfig + "[sandbox]\nimage = \"lola:latest\"\nlola_bin = \"/tmp/lola-linux\"\n"

func TestSSHHostSaveEditRemove(t *testing.T) {
	path := writeTestConfig(t, minimalConfig)
	svc := &ConfigService{}

	if err := svc.SaveSSHHost("", SSHHostDTO{Alias: "web-1", Tier: "dev", User: "ops"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	hosts, err := svc.SSHHostForms()
	if err != nil || len(hosts) != 1 || hosts[0].Alias != "web-1" || hosts[0].User != "ops" {
		t.Fatalf("after add: %+v, %v", hosts, err)
	}

	// The id follows the alias while no name was written.
	if err := svc.SaveSSHHost("web-1", SSHHostDTO{Alias: "web-2", Tier: "dev"}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if hosts, _ = svc.SSHHostForms(); len(hosts) != 1 || hosts[0].Alias != "web-2" || hosts[0].Name != "" {
		t.Fatalf("after edit: %+v", hosts)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `alias = "web-2"`) {
		t.Fatalf("not written: %s", raw)
	}

	if err := svc.RemoveSSHHost("web-2"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if hosts, _ = svc.SSHHostForms(); len(hosts) != 0 {
		t.Fatalf("after remove: %+v", hosts)
	}
}

func TestSSHHostRefusals(t *testing.T) {
	writeTestConfig(t, minimalConfig)
	svc := &ConfigService{}
	if err := svc.SaveSSHHost("", SSHHostDTO{}); err == nil {
		t.Error("an empty alias must be refused")
	}
	if err := svc.SaveSSHHost("", SSHHostDTO{Alias: "-oProxyCommand=x"}); err == nil {
		t.Error("an alias ssh would read as an option must be refused")
	}
	if err := svc.SaveSSHHost("", SSHHostDTO{Alias: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveSSHHost("", SSHHostDTO{Alias: "a"}); err == nil {
		t.Error("a duplicate id must be refused")
	}
	if err := svc.SaveSSHHost("gone", SSHHostDTO{Alias: "b"}); err == nil {
		t.Error("editing a host no longer in config must be refused")
	}
}

// A grant field is hidden on production, so a value left over from before the tier
// changed must not make the form unsaveable, and it must not reach the file.
func TestSSHHostDropsAStaleGrantOnProduction(t *testing.T) {
	writeTestConfig(t, minimalConfig)
	svc := &ConfigService{}
	if err := svc.SaveSSHHost("", SSHHostDTO{Alias: "a", Tier: "production", GrantMaxMinutes: 60}); err != nil {
		t.Fatalf("save: %v", err)
	}
	hosts, _ := svc.SSHHostForms()
	if hosts[0].GrantMaxMinutes != 0 {
		t.Fatalf("grant survived: %+v", hosts[0])
	}
}

// The preview decides with config.SSHDirectSandbox, the daemon's own rule.
func TestSSHHostPreviewMatchesTheDaemonsRule(t *testing.T) {
	for _, c := range []struct {
		name, cfg string
		host      SSHHostDTO
		sandbox   string
		refused   bool
		line      string
	}{
		{"production with a sandbox", sandboxConfig, SSHHostDTO{Alias: "p"}, config.SandboxDocker, false, "default on production"},
		{"production with zero config", minimalConfig, SSHHostDTO{Alias: "p"}, config.SandboxDocker, false, "default on production"},
		{"production allowed still defaults to the container", minimalConfig, SSHHostDTO{Alias: "p", AllowUnsandboxed: true}, config.SandboxDocker, false, "default on production"},
		{"production opted out", minimalConfig, SSHHostDTO{Alias: "p", AllowUnsandboxed: true, Sandbox: "none"}, config.SandboxNone, false, "this host asks for it"},
		{"production none without override", minimalConfig, SSHHostDTO{Alias: "p", Sandbox: "none"}, "", true, "cannot start"},
		{"below production", minimalConfig, SSHHostDTO{Alias: "d", Tier: "dev"}, config.SandboxNone, false, "default below production"},
		{"explicit container", sandboxConfig, SSHHostDTO{Alias: "d", Tier: "dev", Sandbox: "docker"}, config.SandboxDocker, false, "this host asks for one"},
		{"explicit none", minimalConfig, SSHHostDTO{Alias: "d", Tier: "dev", Sandbox: "none"}, config.SandboxNone, false, "this host asks for it"},
	} {
		t.Run(c.name, func(t *testing.T) {
			writeTestConfig(t, c.cfg)
			p, err := (&ConfigService{}).SSHHostPreview(c.host)
			if err != nil {
				t.Fatal(err)
			}
			if p.DirectSandbox != c.sandbox || (p.DirectRefused != "") != c.refused || !strings.Contains(p.DirectLine, c.line) {
				t.Fatalf("%+v", p)
			}
		})
	}
}

func TestSSHHostPreviewShowsEffectiveDefaults(t *testing.T) {
	writeTestConfig(t, minimalConfig)
	p, err := (&ConfigService{}).SSHHostPreview(SSHHostDTO{Alias: "d", Tier: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	if p.GrantMaxMinutes != config.DefaultSSHGrantMinutesStaging ||
		p.CommandTimeoutSeconds != config.DefaultSSHCommandTimeoutSeconds ||
		p.OutputCapKB != config.DefaultSSHOutputCapKB ||
		p.ConnectWaitSeconds != config.DefaultSSHConnectWaitSeconds {
		t.Fatalf("%+v", p)
	}
}

func TestSSHCustomHostSaveAndEdit(t *testing.T) {
	writeTestConfig(t, minimalConfig)
	svc := &ConfigService{}
	want := SSHHostDTO{Name: "custom", Alias: "2001:db8::1", User: "deploy", Port: 2222, IdentityFile: "~/.ssh/custom key", Tier: "dev"}
	if err := svc.SaveSSHHost("", want); err != nil {
		t.Fatal(err)
	}
	hosts, err := svc.SSHHostForms()
	if err != nil || len(hosts) != 1 || hosts[0] != want {
		t.Fatalf("got %+v, %v", hosts, err)
	}
	hosts[0].Label = "Renamed"
	if err := svc.SaveSSHHost("custom", hosts[0]); err != nil {
		t.Fatal(err)
	}
	hosts, err = svc.SSHHostForms()
	if err != nil || hosts[0].Port != 2222 || hosts[0].IdentityFile != want.IdentityFile {
		t.Fatalf("lost overrides: %+v %v", hosts, err)
	}
}
