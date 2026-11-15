package doctor

import (
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
)

// Every row of the relay table, first match wins, including a stale
// "connected" file with no daemon behind it.
func TestRelayDoctorRows(t *testing.T) {
	joined := func(t *testing.T, dir string) {
		t.Helper()
		key, err := hub.LoadOrCreateKey(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := hub.SaveIdentity(dir, hub.Identity{URL: "https://hub.example.com", DeviceID: key.DeviceID(), Org: "acme"}); err != nil {
			t.Fatal(err)
		}
	}
	status := func(t *testing.T, dir, state, reason string) {
		t.Helper()
		if err := hub.SaveRelayStatus(dir, hub.RelayStatus{State: state, Reason: reason}); err != nil {
			t.Fatal(err)
		}
	}
	on := &config.Config{Remote: config.RemoteConfig{Enabled: true}}

	if _, ok := RelayResult(&config.Config{}, t.TempDir(), true); ok {
		t.Error("no row while [remote] is disabled")
	}
	if _, ok := RelayResult(nil, t.TempDir(), true); ok {
		t.Error("no row without a config")
	}

	cases := []struct {
		name   string
		cfg    *config.Config
		setup  func(t *testing.T, dir string)
		up     bool
		ok     bool
		detail string
		fix    FixID
	}{
		{"relay off", &config.Config{Remote: config.RemoteConfig{Enabled: true, RelayOff: true}}, nil, false, true, "off: phones connect on LAN/VPN only", FixNone},
		{"stale connected, no daemon", on, func(t *testing.T, d string) { joined(t, d); status(t, d, "connected", "") }, false, false, "daemon not running; relay state unknown", FixStartDaemon},
		{"not joined", on, nil, true, false, "join a hub to reach phones off-network", FixJoinHub},
		{"refused", on, func(t *testing.T, d string) { joined(t, d); status(t, d, "refused", "") }, true, false, "the hub refused this device", FixRejoinHub},
		{"unsupported", on, func(t *testing.T, d string) { joined(t, d); status(t, d, "unsupported", "") }, true, false, "this hub has no relay; it needs an upgrade", FixRelayHubUpgrade},
		{"backoff", on, func(t *testing.T, d string) { joined(t, d); status(t, d, "backoff", "dial_failed") }, true, false, "cannot reach the hub relay: dial_failed", FixNone},
		{"no phones", on, func(t *testing.T, d string) { joined(t, d); status(t, d, "off", "no_phones") }, true, true, "no paired phone", FixNone},
		{"config error", on, func(t *testing.T, d string) { joined(t, d); status(t, d, "off", "config_error") }, true, true, "off: config.toml has an error", FixNone},
		{"connected", on, func(t *testing.T, d string) { joined(t, d); status(t, d, "connected", "") }, true, true, "connected", FixNone},
		{"syncing", on, func(t *testing.T, d string) { joined(t, d); status(t, d, "syncing", "") }, true, true, "syncing", FixNone},
		{"connecting", on, func(t *testing.T, d string) { joined(t, d); status(t, d, "connecting", "") }, true, true, "connecting", FixNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if c.setup != nil {
				c.setup(t, dir)
			}
			r, ok := RelayResult(c.cfg, dir, c.up)
			if !ok {
				t.Fatal("row expected")
			}
			if r.Name != "phone relay" || r.Critical || r.OK != c.ok || r.Detail != c.detail || r.Fix != c.fix {
				t.Fatalf("got %+v", r)
			}
		})
	}

	t.Run("status free text never shown", func(t *testing.T) {
		dir := t.TempDir()
		joined(t, dir)
		if err := hub.SaveRelayStatus(dir, hub.RelayStatus{State: "connected", LastError: "bearer-SECRET"}); err != nil {
			t.Fatal(err)
		}
		r, _ := RelayResult(on, dir, true)
		if strings.Contains(r.Detail, "SECRET") {
			t.Fatal(r.Detail)
		}
	})
}
