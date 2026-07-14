package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
)

// newEnableTestDaemon builds a daemon whose config (poll p1 + [[project]]
// proj1) is saved to <home>/config.toml, ready for handleEnable/handleReload
// tests (both persist via config.DefaultPath under LOLA_HOME).
func newEnableTestDaemon(t *testing.T, poll config.Poll) *Daemon {
	t.Helper()
	cfg := testConfig(poll)
	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.stopAllWorkers) // a successful enable starts a worker
	return d
}

// Enabling validates the whole config (which resolves the poll's [[project]]),
// flips the flag, and persists.
func TestEnableValidatesConfigAndPersists(t *testing.T) {
	p := labelPoll("p1")
	p.Enabled = false
	d := newEnableTestDaemon(t, p)

	if err := d.handleEnable(context.Background(), "p1", true); err != nil {
		t.Fatalf("handleEnable: %v", err)
	}
	if !d.cfg.PollByName("p1").Enabled {
		t.Error("poll not enabled")
	}
	if _, err := os.Stat(filepath.Join(d.home, "config.toml")); err != nil {
		t.Errorf("config not saved: %v", err)
	}
}

// The enable-time validation now just runs Validate: a poll whose [[project]]
// reference does not resolve is rejected and its flag is rolled back.
func TestEnableRejectsPollWithUnknownProject(t *testing.T) {
	p := labelPoll("p1")
	p.Enabled = false
	p.Project = "no-such-project"
	d := newEnableTestDaemon(t, p)

	err := d.handleEnable(context.Background(), "p1", true)
	if err == nil {
		t.Fatal("handleEnable must reject a poll whose project is undefined")
	}
	if d.cfg.PollByName("p1").Enabled {
		t.Error("poll enabled despite validation failure")
	}
}

func TestDisableStopsPoll(t *testing.T) {
	d := newEnableTestDaemon(t, labelPoll("p1")) // enabled

	if err := d.handleEnable(context.Background(), "p1", false); err != nil {
		t.Fatalf("handleEnable(disable): %v", err)
	}
	if d.cfg.PollByName("p1").Enabled {
		t.Error("poll still enabled")
	}
}

func TestEnableUnknownPollErrors(t *testing.T) {
	d := newEnableTestDaemon(t, labelPoll("p1"))
	if err := d.handleEnable(context.Background(), "ghost", true); err == nil {
		t.Fatal("handleEnable must error for an unknown poll")
	}
}

// Reload rejects an invalid on-disk config and keeps the previous one live.
func TestReloadRejectsInvalidConfigKeepsPrevious(t *testing.T) {
	d := newEnableTestDaemon(t, labelPoll("p1"))
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	bad := testConfig(labelPoll("p1"))
	bad.Defaults.GlobalCap = 0 // invalid
	if err := bad.Save(path); err != nil {
		t.Fatal(err)
	}

	if err := d.handleReload(context.Background()); err == nil {
		t.Fatal("reload must reject an invalid config")
	}
	if d.cfg.Defaults.GlobalCap != 10 {
		t.Errorf("reload must keep the previous config, global_cap = %d", d.cfg.Defaults.GlobalCap)
	}
}
