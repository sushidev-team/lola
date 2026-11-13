package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/session"
)

// parallelDevDaemon is devDaemon for a project that leases ports, with a lease
// recorded for every session in leased.
func parallelDevDaemon(t *testing.T, leased []string, sessions ...session.Session) (*Daemon, *fakeDevTmux) {
	t.Helper()
	d, tm := devDaemon(t, []string{"composer dev"}, sessions...)
	d.mu.Lock()
	d.cfg.Projects[0].PortRange = "4100-4199"
	d.mu.Unlock()
	d.ports.InUse = func(int) bool { return false }
	for _, id := range leased {
		if _, err := d.ports.Acquire(id, "app", 4100, 4199, 1); err != nil {
			t.Fatal(err)
		}
	}
	return d, tm
}

func TestHandleDevRunsSideBySideWhenThePortsAreLeased(t *testing.T) {
	d, tm := parallelDevDaemon(t, []string{"lola-app-eng-1", "lola-app-eng-2"},
		devSession("lola-app-eng-1"), devSession("lola-app-eng-2"))
	if _, err := d.handleDev(context.Background(), "lola-app-eng-1", true); err != nil {
		t.Fatalf("first: %v", err)
	}
	got, err := d.handleDev(context.Background(), "lola-app-eng-2", true)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if got.Stopped != "" {
		t.Errorf("Stopped = %q: a multi-active start must not take another session's stack", got.Stopped)
	}
	for _, name := range []string{"lola-app-eng-1-dev-1", "lola-app-eng-2-dev-1"} {
		if _, ok := tm.sessions[name]; !ok {
			t.Errorf("%s is not running (have %v)", name, tm.names())
		}
	}
	first, _ := d.sessions.Get("lola-app-eng-1")
	if !first.DevActive {
		t.Error("the first session lost its active state")
	}

	// The wire carries the lease and the mode.
	var ports []int
	var parallel bool
	for _, si := range d.sessionsData().Sessions {
		if si.ID == "lola-app-eng-2" {
			ports, parallel = si.Ports, si.DevParallel
		}
	}
	if len(ports) != 1 || ports[0] != 4101 || !parallel {
		t.Errorf("wire = ports %v parallel %v", ports, parallel)
	}
}

func TestHandleDevRefusesAParallelStartWithoutALease(t *testing.T) {
	d, tm := parallelDevDaemon(t, nil, devSession("lola-app-eng-1"))
	_, err := d.handleDev(context.Background(), "lola-app-eng-1", true)
	if err == nil || !strings.Contains(err.Error(), "no port lease") {
		t.Fatalf("err = %v, want a refusal naming the missing lease", err)
	}
	if len(tm.names()) != 0 {
		t.Errorf("tabs started anyway: %v", tm.names())
	}
}

func TestPruneLeasesDropsSessionsGoneFromTheStore(t *testing.T) {
	d, _ := parallelDevDaemon(t, []string{"lola-app-eng-1", "lola-app-gone"}, devSession("lola-app-eng-1"))
	// Inside the grace a lease survives even without a session (spawn in flight).
	d.pruneLeases()
	if _, ok := d.ports.Get("lola-app-gone"); !ok {
		t.Fatal("pruned inside the grace window")
	}
	d.ports.Now = func() time.Time { return time.Now().Add(2 * orphanTimeout) }
	d.pruneLeases()
	if _, ok := d.ports.Get("lola-app-gone"); ok {
		t.Error("the lease of a session gone from the store was kept")
	}
	if _, ok := d.ports.Get("lola-app-eng-1"); !ok {
		t.Error("a live session's lease was pruned")
	}
}
