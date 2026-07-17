package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/runtime"
)

// newEnableTestDaemon builds a daemon whose config (poll p1 + [[project]]
// proj1) is saved to <home>/config.toml, ready for handleEnable/handleReload
// tests (both persist via config.DefaultPath under LOLA_HOME).
func newEnableTestDaemon(t *testing.T, poll config.Project) *Daemon {
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

// The enable-time validation runs Validate: a project with an invalid polling
// config (here a bad match_mode) is rejected and its enabled flag rolled back.
func TestEnableRejectsInvalidPollingConfig(t *testing.T) {
	p := labelPoll("p1")
	p.Enabled = false
	p.MatchMode = "bogus" // fails Validate
	d := newEnableTestDaemon(t, p)

	err := d.handleEnable(context.Background(), "p1", true)
	if err == nil {
		t.Fatal("handleEnable must reject an invalid polling config")
	}
	if d.cfg.PollByName("p1").Enabled {
		t.Error("polling enabled despite validation failure")
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

// Finding 4: a reload that changes [tmux].socket_name (but not [[project]]) must
// rebuild the native runtime so its Alive/Adopt/Kill/Spawn land on the SAME
// server as d.tmuxClient's live send-keys/capture. Without the rebuild the
// observer would read the OLD server while keys go to the NEW one.
func TestReloadRebuildsNativeOnSocketChange(t *testing.T) {
	d := newEnableTestDaemon(t, labelPoll("p1"))
	// Stand in a real native runtime on the default "lola" socket and mark it
	// owned so the realNative-gated rebuild path is exercised.
	d.native = newNativeRuntime(d.cfg, d.home, d.lolaBin, d.linearKey, d.nativeLogf)
	d.realNative = true
	if got := d.native.(*runtime.Native).Tmux.SocketName; got != config.DefaultTmuxSocketName {
		t.Fatalf("precondition: native socket = %q, want %q", got, config.DefaultTmuxSocketName)
	}

	// Persist a config identical except for the tmux socket name.
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	nc := testConfig(labelPoll("p1"))
	nc.Tmux = config.TmuxConfig{SocketName: "team-lola"}
	if err := nc.Save(path); err != nil {
		t.Fatal(err)
	}

	if err := d.handleReload(context.Background()); err != nil {
		t.Fatalf("handleReload: %v", err)
	}

	nat, ok := d.native.(*runtime.Native)
	if !ok {
		t.Fatalf("native is %T, want *runtime.Native", d.native)
	}
	if got := nat.Tmux.SocketName; got != "team-lola" {
		t.Fatalf("native tmux socket = %q, want team-lola (runtime must be rebuilt on socket change)", got)
	}
	if got := d.tmuxClient().SocketName; got != "team-lola" {
		t.Fatalf("tmuxClient socket = %q, want team-lola (both must target the same server)", got)
	}
}
