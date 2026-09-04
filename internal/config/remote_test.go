package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const remoteBaseToml = `
[defaults]
global_cap = 2

[[project]]
name = "p1"
path = "/tmp/p1"
`

// An absent [remote] table resolves to the zero config — disabled, zero
// behavior change — and Save must not grow the table on a config that never
// mentioned it. This is the property that keeps every existing install from
// suddenly carrying a listener block after its next settings save.
func TestRemoteAbsentIsDisabled(t *testing.T) {
	c := loadToml(t, remoteBaseToml)
	if c.Remote != (RemoteConfig{}) {
		t.Fatalf("absent table = %+v, want zero", c.Remote)
	}
	if c.Remote.Listens() {
		t.Fatal("an absent [remote] table must not listen")
	}
	out := filepath.Join(t.TempDir(), "out.toml")
	if err := c.Save(out); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	if strings.Contains(string(data), "remote") {
		t.Errorf("Save wrote a [remote] table for the zero config:\n%s", data)
	}
}

// `enabled = true` alone resolves a complete, working configuration.
func TestRemoteEnabledAloneResolvesDefaults(t *testing.T) {
	c := loadToml(t, remoteBaseToml+"\n[remote]\nenabled = true\n")
	r := c.Remote
	if !r.Enabled || r.Bind != DefaultRemoteBind || r.Port != DefaultRemotePort {
		t.Fatalf("enabled-alone = %+v, want bind %q port %d", r, DefaultRemoteBind, DefaultRemotePort)
	}
	if !r.Listens() {
		t.Fatal("an enabled localhost listener must listen")
	}
}

// A present table that only turns the feature OFF is still a present table: it
// keeps the operator's port and bind, and it round-trips as one rather than
// collapsing back to "never configured".
func TestRemotePresentButDisabledSurvivesSave(t *testing.T) {
	c := loadToml(t, remoteBaseToml+"\n[remote]\nenabled = false\nport = 9100\n")
	if c.Remote.Enabled || c.Remote.Port != 9100 {
		t.Fatalf("loaded = %+v", c.Remote)
	}
	out := filepath.Join(t.TempDir(), "out.toml")
	if err := c.Save(out); err != nil {
		t.Fatal(err)
	}
	c2, err := Load(out)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Remote != c.Remote {
		t.Fatalf("round trip changed the table:\nbefore %+v\nafter  %+v", c.Remote, c2.Remote)
	}
}

// The two effective-value accessors are the only place the fallbacks live, so
// no caller has to repeat them and none can disagree about "off".
func TestRemoteEffectiveValues(t *testing.T) {
	tests := []struct {
		name     string
		in       RemoteConfig
		wantBind string
		wantPort int
		listens  bool
	}{
		{"zero", RemoteConfig{}, DefaultRemoteBind, DefaultRemotePort, false},
		{"enabled, defaults", RemoteConfig{Enabled: true}, DefaultRemoteBind, DefaultRemotePort, true},
		{"enabled but bound off", RemoteConfig{Enabled: true, Bind: "off", Port: 9100}, "off", 9100, false},
		{"disabled but bound lan", RemoteConfig{Bind: "lan"}, "lan", DefaultRemotePort, false},
		{"explicit ip", RemoteConfig{Enabled: true, Bind: "192.168.1.20", Port: 1}, "192.168.1.20", 1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.BindMode(); got != tc.wantBind {
				t.Errorf("BindMode() = %q, want %q", got, tc.wantBind)
			}
			if got := tc.in.ListenPort(); got != tc.wantPort {
				t.Errorf("ListenPort() = %d, want %d", got, tc.wantPort)
			}
			if got := tc.in.Listens(); got != tc.listens {
				t.Errorf("Listens() = %v, want %v", got, tc.listens)
			}
		})
	}
}

// Explicit values — including the ones a naive resolve would mistake for
// "unset" — survive save/load unchanged.
func TestRemoteRoundTripPreservesExplicitValues(t *testing.T) {
	c := loadToml(t, remoteBaseToml+`
[remote]
enabled = true
bind = "off"
port = 65535
`)
	r := c.Remote
	if !r.Enabled || r.Bind != "off" || r.Port != 65535 {
		t.Fatalf("explicit values mangled on load: %+v", r)
	}
	out := filepath.Join(t.TempDir(), "out.toml")
	if err := c.Save(out); err != nil {
		t.Fatal(err)
	}
	c2, err := Load(out)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Remote != r {
		t.Fatalf("round-trip changed the table:\nbefore %+v\nafter  %+v", r, c2.Remote)
	}
	// Saving the reloaded config again has to be a fixed point, or a second
	// settings save would keep rewriting the file.
	out2 := filepath.Join(t.TempDir(), "out2.toml")
	if err := c2.Save(out2); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(out)
	b, _ := os.ReadFile(out2)
	if string(a) != string(b) {
		t.Fatalf("save is not idempotent:\nfirst:\n%s\nsecond:\n%s", a, b)
	}
}

func TestRemoteValidation(t *testing.T) {
	bad := []struct {
		name string
		body string
	}{
		{"negative port", "[remote]\nport = -1\n"},
		{"port past the range", "[remote]\nport = 65536\n"},
		{"a hostname is not resolved", "[remote]\nbind = \"myhost.local\"\n"},
		{"an unknown keyword", "[remote]\nbind = \"lan0\"\n"},
		{"a host:port pair", "[remote]\nbind = \"127.0.0.1:7717\"\n"},
		{"a cidr block", "[remote]\nbind = \"192.168.1.0/24\"\n"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			c := loadToml(t, remoteBaseToml+tc.body)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), "remote.") {
				t.Fatalf("Validate() = %v, want a remote error", err)
			}
		})
	}

	good := []struct {
		name string
		body string
	}{
		{"every keyword", "[remote]\nenabled = true\nbind = \"lan\"\n"},
		{"bind all", "[remote]\nenabled = true\nbind = \"all\"\n"},
		// enabled + off is "keep my settings, stop listening" and must not be
		// an error: rejecting it would force an operator to delete the table to
		// pause the feature.
		{"enabled but bound off", "[remote]\nenabled = true\nbind = \"off\"\n"},
		{"port 0 means the default", "[remote]\nenabled = true\nport = 0\n"},
		{"an ipv4 literal", "[remote]\nbind = \"192.168.1.20\"\n"},
		{"an ipv6 literal", "[remote]\nbind = \"fd00::1\"\n"},
		{"an unset bind", "[remote]\nenabled = true\n"},
	}
	for _, tc := range good {
		t.Run(tc.name, func(t *testing.T) {
			c := loadToml(t, remoteBaseToml+tc.body)
			if err := c.Validate(); err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

// Validation is a property of the CONFIG, never of the binary reading it: the
// TUI and the desktop app are built without the lola_insecure tag and must load
// exactly the configs the daemon does. A `lan` bind is therefore valid
// everywhere; the M1 forcing to localhost is a runtime decision in
// internal/remote, which logs the override.
func TestRemoteValidationIsBuildTagIndependent(t *testing.T) {
	for _, bind := range RemoteBinds {
		c := loadToml(t, remoteBaseToml+"[remote]\nenabled = true\nbind = \""+bind+"\"\n")
		if err := c.Validate(); err != nil {
			t.Errorf("bind %q rejected: %v", bind, err)
		}
	}
}

// A key that is not in the on-disk mirror does not survive a save, and nothing
// says so: Load reads it, the daemon uses it, Save drops it, and the next Load
// reads the default back. That is how `advertise` shipped — settable in both
// settings forms, gone by the next reload.
func TestRemoteAdvertiseRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	cfg := Config{Remote: RemoteConfig{Enabled: true, Bind: "lan", Port: 7717, Advertise: true}}
	if err := cfg.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got.Remote.Advertise {
		t.Fatal("advertise did not survive save/load")
	}

	// And an explicit false is preserved rather than being read as "unset".
	cfg.Remote.Advertise = false
	if err := cfg.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err = Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Remote.Advertise {
		t.Error("advertise came back on after being turned off")
	}
}

// The default is OFF, and it is a decision rather than a reflex: the service
// announces that this machine runs coding agents and accepts remote control to
// every peer on the network. A [remote] table that never mentions the key must
// therefore resolve to false.
func TestRemoteAdvertiseDefaultsOff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[remote]\n  enabled = true\n  bind = \"lan\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Remote.Advertise {
		t.Error("a table that never mentions advertise resolved to on")
	}
}
