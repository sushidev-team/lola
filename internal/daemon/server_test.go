package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/linear"
)

// writeAOYaml writes an agent-orchestrator.yaml fixture listing the given
// project names and returns its path.
func writeAOYaml(t *testing.T, projects ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("projects:\n")
	for _, p := range projects {
		b.WriteString("  " + p + ":\n    repo: git@example.com:acme/" + p + ".git\n")
	}
	path := filepath.Join(t.TempDir(), "agent-orchestrator.yaml")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// newEnableTestDaemon builds a daemon with one disabled poll "p1"
// (ao_project "proj") ready for handleEnable tests.
func newEnableTestDaemon(t *testing.T, aoc AOAPI, aoConfigPath string) *Daemon {
	t.Helper()
	p := labelPoll("p1")
	p.Enabled = false
	cfg := testConfig(p)
	cfg.AO.ConfigPath = aoConfigPath
	d := newTestDaemon(t, cfg, &linear.Fake{}, aoc)
	t.Cleanup(d.stopAllWorkers) // handleEnable success starts a worker
	return d
}

func TestEnableRegistryListsProject(t *testing.T) {
	d := newEnableTestDaemon(t, &fakeAO{projects: []string{"other", "proj"}}, "")

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

func TestEnableRegistryRejectsUnknownProject(t *testing.T) {
	// Non-empty registry without the project is authoritative: no yaml
	// fallback even though the yaml lists it.
	yaml := writeAOYaml(t, "proj")
	d := newEnableTestDaemon(t, &fakeAO{projects: []string{"other"}}, yaml)

	err := d.handleEnable(context.Background(), "p1", true)
	if err == nil || !strings.Contains(err.Error(), "not registered in AO") {
		t.Fatalf("handleEnable error = %v, want registry rejection", err)
	}
	if d.cfg.PollByName("p1").Enabled {
		t.Error("poll enabled despite rejection")
	}
}

func TestEnableEmptyRegistryFallsBackToYaml(t *testing.T) {
	// A successful but EMPTY `ao project ls` (fresh install, registry
	// migration, or an unexpected envelope key) must not be authoritative:
	// the yaml fallback validates the project.
	yaml := writeAOYaml(t, "proj")
	d := newEnableTestDaemon(t, &fakeAO{projects: []string{}}, yaml)

	if err := d.handleEnable(context.Background(), "p1", true); err != nil {
		t.Fatalf("handleEnable: %v (empty registry must fall back to yaml)", err)
	}
	if !d.cfg.PollByName("p1").Enabled {
		t.Error("poll not enabled")
	}
}

func TestEnableRegistryErrorFallsBackToYaml(t *testing.T) {
	yaml := writeAOYaml(t, "someone-else")
	d := newEnableTestDaemon(t, &fakeAO{projects: nil}, yaml) // nil = registry unavailable

	err := d.handleEnable(context.Background(), "p1", true)
	if err == nil || !strings.Contains(err.Error(), "not found in") {
		t.Fatalf("handleEnable error = %v, want yaml-fallback rejection", err)
	}
}

func TestEnableNoRegistryNoYamlOnlyRequiresNonEmpty(t *testing.T) {
	d := newEnableTestDaemon(t, &fakeAO{projects: nil}, "")

	if err := d.handleEnable(context.Background(), "p1", true); err != nil {
		t.Fatalf("handleEnable: %v", err)
	}
}

func TestEnableDoesNotHoldLockDuringAOCheck(t *testing.T) {
	// A wedged ao binary must not freeze the daemon: while the project check
	// blocks, d.mu (taken by every tick, status, and reconcile) stays free,
	// and the exec context carries a deadline.
	aoc := &fakeAO{projects: []string{"proj"}}
	entered := make(chan struct{})
	release := make(chan struct{})
	var hadDeadline bool
	aoc.onProjects = func(ctx context.Context) {
		_, hadDeadline = ctx.Deadline()
		close(entered)
		<-release
	}
	d := newEnableTestDaemon(t, aoc, "")

	errCh := make(chan error, 1)
	go func() { errCh <- d.handleEnable(context.Background(), "p1", true) }()
	<-entered

	locked := make(chan struct{})
	go func() {
		d.mu.Lock()
		d.mu.Unlock() //nolint:staticcheck // empty critical section probes availability
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(2 * time.Second):
		t.Fatal("d.mu held while the ao project check was blocked")
	}

	close(release)
	if err := <-errCh; err != nil {
		t.Fatalf("handleEnable: %v", err)
	}
	if !hadDeadline {
		t.Error("Projects ran on a context without a deadline; a hung ao exec would never be killed")
	}
}

func TestEnableWedgedAOTimesOutAndFallsBack(t *testing.T) {
	prev := aoProjectCheckTimeout
	aoProjectCheckTimeout = 50 * time.Millisecond
	t.Cleanup(func() { aoProjectCheckTimeout = prev })

	// The fake blocks until its context expires, like an ao binary stuck on
	// IPC to a hung desktop app — exactly the condition the yaml fallback
	// was designed for.
	yaml := writeAOYaml(t, "proj")
	aoc := &fakeAO{projects: []string{"proj"}}
	aoc.onProjects = func(ctx context.Context) { <-ctx.Done() }
	d := newEnableTestDaemon(t, aoc, yaml)

	done := make(chan error, 1)
	go func() { done <- d.handleEnable(context.Background(), "p1", true) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("handleEnable: %v (timeout must fall back to yaml)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handleEnable hung on a wedged ao binary")
	}
}

func TestDisableSkipsAOCheck(t *testing.T) {
	// Disabling never touches AO: a wedged/absent registry must not block it.
	aoc := &fakeAO{projects: nil}
	aoc.onProjects = func(context.Context) { t.Error("Projects called on disable") }
	p := labelPoll("p1")
	cfg := testConfig(p) // enabled
	d := newTestDaemon(t, cfg, &linear.Fake{}, aoc)
	t.Cleanup(d.stopAllWorkers)

	if err := d.handleEnable(context.Background(), "p1", false); err != nil {
		t.Fatalf("handleEnable(disable): %v", err)
	}
	if d.cfg.PollByName("p1").Enabled {
		t.Error("poll still enabled")
	}
}
