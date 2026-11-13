package config

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sshErrs(c *Config) string {
	var b strings.Builder
	for _, e := range c.validateSSHHosts() {
		b.WriteString(e.Error() + "\n")
	}
	return b.String()
}

func TestSSHHostRoundTripsAndAbsentMeansOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.SSHHosts) != 0 {
		t.Fatalf("no table, hosts = %+v", c.SSHHosts)
	}
	c.SSHHosts = []SSHHost{
		{Alias: "web-1"},
		{Name: "prod-db", Alias: "db.internal", Label: "Prod DB", User: "lola-ro", Port: 2222, IdentityFile: "~/.ssh/production key", Tier: SSHTierProduction, AllowUnsandboxed: true,
			Sandbox: SandboxNone, Agent: "codex", DefaultCwd: "/var/log", CommandTimeoutSeconds: 30, MaxCommandTimeoutSeconds: 60,
			OutputCapKB: 64, ControlPersistSeconds: 120, ConnectWaitSeconds: 90, ApprovalTimeoutSeconds: 600},
		{Name: "stage", Alias: "stage", Tier: SSHTierStaging, GrantMaxMinutes: -1},
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.SSHHosts, c.SSHHosts) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got.SSHHosts, c.SSHHosts)
	}
}

func TestSSHDefaultsFailClosed(t *testing.T) {
	h := SSHHost{Alias: "x"}
	if h.ID() != "x" || h.DisplayName() != "x" {
		t.Fatalf("id: %q", h.ID())
	}
	if !h.IsProduction() || h.EffectiveTier() != SSHTierProduction {
		t.Fatal("an unclassified host must read as production")
	}
	if !(SSHHost{Alias: "x", Tier: "prod"}).IsProduction() {
		t.Fatal("an unknown tier must read as production")
	}
	if h.EffectiveGrantMaxMinutes() != 0 {
		t.Fatal("production must have no grants")
	}
	if (SSHHost{Alias: "x", GrantMaxMinutes: 60}).EffectiveGrantMaxMinutes() != 0 {
		t.Fatal("production must have no grants, whatever the file says")
	}
	if got := (SSHHost{Alias: "x", Tier: SSHTierStaging}).EffectiveGrantMaxMinutes(); got != DefaultSSHGrantMinutesStaging {
		t.Fatalf("staging default = %d", got)
	}
	if got := (SSHHost{Alias: "x", Tier: SSHTierDev}).EffectiveGrantMaxMinutes(); got != DefaultSSHGrantMinutesDev {
		t.Fatalf("dev default = %d", got)
	}
	if got := (SSHHost{Alias: "x", Tier: SSHTierDev, GrantMaxMinutes: -1}).EffectiveGrantMaxMinutes(); got != 0 {
		t.Fatalf("-1 = %d", got)
	}
	if got := (SSHHost{Alias: "x", Tier: SSHTierDev, GrantMaxMinutes: 5}).EffectiveGrantMaxMinutes(); got != 5 {
		t.Fatalf("explicit = %d", got)
	}
}

func TestSSHKnobDefaults(t *testing.T) {
	h := SSHHost{Alias: "x"}
	if h.CommandTimeout(0) != DefaultSSHCommandTimeoutSeconds || h.CommandTimeout(30) != 30 ||
		h.CommandTimeout(1<<20) != DefaultSSHMaxCommandTimeoutSeconds {
		t.Fatalf("timeouts: %d %d %d", h.CommandTimeout(0), h.CommandTimeout(30), h.CommandTimeout(1<<20))
	}
	if h.OutputCapBytes() != DefaultSSHOutputCapKB*1024 || h.EffectiveControlPersist() != DefaultSSHControlPersistSeconds ||
		h.EffectiveConnectWait() != DefaultSSHConnectWaitSeconds || h.EffectiveApprovalTimeout() != DefaultSSHApprovalTimeoutSeconds {
		t.Fatalf("defaults: %+v", h)
	}
	set := SSHHost{Alias: "x", CommandTimeoutSeconds: 5, MaxCommandTimeoutSeconds: 10, OutputCapKB: 2}
	if set.CommandTimeout(0) != 5 || set.CommandTimeout(11) != 10 || set.OutputCapBytes() != 2048 {
		t.Fatalf("explicit: %+v", set)
	}
}

func TestSSHAdmitIsTheProductionRule(t *testing.T) {
	prod, stage := SSHHost{Alias: "p"}, SSHHost{Alias: "s", Tier: SSHTierStaging}
	err := prod.SSHAdmit(false)
	if !errors.Is(err, ErrSSHUnsandboxedProduction) {
		t.Fatalf("unsandboxed on production: %v", err)
	}
	for _, want := range []string{"sandbox = \"docker\"", "allow_unsandboxed = true", "p is tier production"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message should name %q: %v", want, err)
		}
	}
	if err := prod.SSHAdmit(true); err != nil {
		t.Errorf("sandboxed on production: %v", err)
	}
	if err := stage.SSHAdmit(false); err != nil {
		t.Errorf("unsandboxed on staging: %v", err)
	}
	if err := (SSHHost{Alias: "p", AllowUnsandboxed: true}).SSHAdmit(false); err != nil {
		t.Errorf("explicit override: %v", err)
	}
}

func TestSSHDirectSandbox(t *testing.T) {
	ready := &Config{Sandbox: SandboxConfig{Image: "img", LolaBin: "/l"}}
	bare := &Config{} // the default image needs no [sandbox] table
	cases := []struct {
		name    string
		c       *Config
		h       SSHHost
		want    string
		refused bool
	}{
		{"production with sandbox configured", ready, SSHHost{Alias: "p"}, SandboxDocker, false},
		{"production prefers sandbox even when allowed out", ready, SSHHost{Alias: "p", AllowUnsandboxed: true}, SandboxDocker, false},
		{"production with zero config is sandboxed", bare, SSHHost{Alias: "p"}, SandboxDocker, false},
		{"production with zero config, override still sandboxes", bare, SSHHost{Alias: "p", AllowUnsandboxed: true}, SandboxDocker, false},
		{"production leaves the sandbox only by saying none", bare, SSHHost{Alias: "p", Sandbox: SandboxNone, AllowUnsandboxed: true}, SandboxNone, false},
		{"production forced none needs override", ready, SSHHost{Alias: "p", Sandbox: SandboxNone}, "", true},
		{"production forced none with override", ready, SSHHost{Alias: "p", Sandbox: SandboxNone, AllowUnsandboxed: true}, SandboxNone, false},
		{"docker without sandbox config", bare, SSHHost{Alias: "p", Sandbox: SandboxDocker}, SandboxDocker, false},
		{"staging runs on the host", ready, SSHHost{Alias: "s", Tier: SSHTierStaging}, SandboxNone, false},
		{"staging can opt in", ready, SSHHost{Alias: "s", Tier: SSHTierStaging, Sandbox: SandboxDocker}, SandboxDocker, false},
	}
	for _, c := range cases {
		got, err := c.c.SSHDirectSandbox(c.h)
		if (err != nil) != c.refused || got != c.want {
			t.Errorf("%s: got %q, %v", c.name, got, err)
		}
	}
	if !(&Config{SSHHosts: []SSHHost{{Alias: "p"}}}).AnySSHSandboxed() {
		t.Error("a sandboxed direct session needs the hook bridge")
	}
	if (&Config{SSHHosts: []SSHHost{{Alias: "s", Tier: SSHTierDev}}}).AnySSHSandboxed() {
		t.Error("nothing sandboxed, no bridge")
	}
}

func TestSSHHostValidation(t *testing.T) {
	good := &Config{
		Sandbox: SandboxConfig{Image: "img", LolaBin: "/l"},
		SSHHosts: []SSHHost{
			{Alias: "web-1"},
			{Name: "db", Alias: "db.internal_2", User: "lola-ro", Tier: SSHTierStaging, GrantMaxMinutes: 60, Sandbox: SandboxDocker, Agent: "opencode", DefaultCwd: "/srv/app"},
			{Name: "dev", Alias: "dev", Tier: SSHTierDev, Sandbox: SandboxNone},
			{Name: "ack", Alias: "ack", AllowUnsandboxed: true, Sandbox: SandboxNone},
		},
	}
	if msg := sshErrs(good); msg != "" {
		t.Fatalf("good config rejected:\n%s", msg)
	}

	bad := []struct {
		name string
		h    SSHHost
		want string
	}{
		{"no alias", SSHHost{Name: "x"}, "alias is required"},
		{"dash alias reads as an option", SSHHost{Alias: "-oProxyCommand=evil"}, "alias must be"},
		{"alias with user", SSHHost{Alias: "root@host"}, "alias must be"},
		{"alias with space", SSHHost{Alias: "a b"}, "alias must be"},
		{"alias token", SSHHost{Alias: "a%h"}, "alias must be"},
		{"bad name", SSHHost{Name: "-x", Alias: "a"}, "name must be"},
		{"bad user", SSHHost{Alias: "a", User: "-l root"}, "not a valid user"},
		{"unknown tier", SSHHost{Alias: "a", Tier: "prod"}, "tier must be"},
		{"unknown sandbox", SSHHost{Alias: "a", Sandbox: "vm"}, "sandbox must be"},
		{"none on production", SSHHost{Alias: "a", Sandbox: SandboxNone}, "allow_unsandboxed"},
		{"unknown agent", SSHHost{Alias: "a", Agent: "gpt"}, "agent must be"},
		{"relative cwd", SSHHost{Alias: "a", DefaultCwd: "var/log"}, "default_cwd"},
		{"grants on production", SSHHost{Alias: "a", GrantMaxMinutes: 10}, "cannot be set"},
		{"grants on empty tier", SSHHost{Alias: "a", Tier: "", GrantMaxMinutes: 10}, "cannot be set"},
		{"grant below -1", SSHHost{Alias: "a", Tier: SSHTierDev, GrantMaxMinutes: -2}, "grant_max_minutes"},
		{"negative timeout", SSHHost{Alias: "a", CommandTimeoutSeconds: -1}, "command_timeout_seconds"},
		{"output cap too big", SSHHost{Alias: "a", OutputCapKB: MaxSSHOutputCapKB + 1}, "output_cap_kb"},
		{"timeout over max", SSHHost{Alias: "a", CommandTimeoutSeconds: 100, MaxCommandTimeoutSeconds: 50}, "above max_command_timeout_seconds"},
		{"label control", SSHHost{Alias: "a", Label: "x\x1b[2J"}, "control characters"},
	}
	for _, c := range bad {
		msg := sshErrs(&Config{SSHHosts: []SSHHost{c.h}})
		if !strings.Contains(msg, c.want) {
			t.Errorf("%s: want %q, got %q", c.name, c.want, msg)
		}
	}
	if msg := sshErrs(&Config{SSHHosts: []SSHHost{{Alias: "a"}, {Name: "a", Alias: "b"}}}); !strings.Contains(msg, "duplicate") {
		t.Errorf("duplicate: %q", msg)
	}
	if msg := sshErrs(&Config{SSHHosts: []SSHHost{{Alias: "s", Tier: SSHTierDev, Sandbox: SandboxDocker}}}); msg != "" {
		t.Errorf("docker needs no [sandbox] table: %q", msg)
	}
	// Reaches Validate at all.
	full := &Config{SSHHosts: []SSHHost{{Alias: "-bad"}}}
	if err := full.Validate(); err == nil || !strings.Contains(err.Error(), "alias must be") {
		t.Errorf("Validate does not run the catalog check: %v", err)
	}
	if p := (&Config{SSHHosts: []SSHHost{{Alias: "a"}, {Name: "b", Alias: "c"}}}); p.SSHHostByName("b") == nil || p.SSHHostByName("c") != nil || p.SSHHostByName("") != nil {
		t.Error("SSHHostByName goes by catalog id")
	}
}

func TestSSHCustomConnectionValidation(t *testing.T) {
	for _, host := range []string{"2001:db8::1", "192.0.2.1", strings.Repeat("a", 64) + ".example.com"} {
		if got := sshErrs(&Config{SSHHosts: []SSHHost{{Name: "custom", Alias: host, Port: 2222, IdentityFile: "~/.ssh/custom key"}}}); got != "" {
			t.Errorf("%s: %s", host, got)
		}
	}
	for _, h := range []SSHHost{{Alias: "host", Port: 65536}, {Alias: "host", Port: -1}, {Alias: "host", IdentityFile: "-oBad"}, {Alias: "host", IdentityFile: "/tmp/key\n"}, {Alias: "host:2222"}} {
		if got := sshErrs(&Config{SSHHosts: []SSHHost{h}}); got == "" {
			t.Errorf("accepted %+v", h)
		}
	}
}
