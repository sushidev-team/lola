package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
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

func TestHandleDevAcquiresMissingLease(t *testing.T) {
	d, tm := parallelDevDaemon(t, nil, devSession("lola-app-eng-1"))
	d.cfg.Projects[0].PortsPerSession = 3
	d.cfg.Projects[0].Env = map[string]string{"APP_URL": "http://localhost:{{.Port}}"}
	_, err := d.handleDev(context.Background(), "lola-app-eng-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if cmd := tm.sessions["lola-app-eng-1-dev-1"]; !strings.Contains(cmd, ".lola/dev-env") {
		t.Fatal(cmd)
	}
	s, _ := d.sessions.Get("lola-app-eng-1")
	env, err := os.ReadFile(filepath.Join(devWorktreeDir(s, "app", d.home), ".lola/dev-env"))
	if err != nil || !strings.Contains(string(env), "4102") || !strings.Contains(string(env), "http://localhost:4100") {
		t.Fatalf("dev env %q: %v", env, err)
	}
}

// Exercise the shell command tmux receives, including a stack lane whose env
// file was written before leasing was enabled, and switching it back off.
func TestHandleDevSwitchesPortModesForAStackLane(t *testing.T) {
	s := devSession("lola-app-stack")
	s.Stack = true
	d, tm := parallelDevDaemon(t, nil, s)
	root := devWorktreeDir(s, s.Project, d.home)
	lane := filepath.Join(root, "web")
	if err := os.MkdirAll(filepath.Join(lane, ".lola"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lane, ".lola/env"), []byte("LOLA_PORT=9999\nLOLA_PORT_2=9998\nAPP_URL=old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s.Apps = []session.App{{Name: "web", Worktree: lane}}
	d.sessions.Upsert(s)
	d.cfg.Projects = append(d.cfg.Projects, config.Project{
		Name: "web", PortRange: "4200-4203", PortsPerSession: 2,
		Env:         map[string]string{"APP_URL": "http://localhost:$LOLA_PORT"},
		DevCommands: []string{`printf '%s|%s|%s' "$LOLA_PORT" "$LOLA_PORT_2" "$APP_URL"`},
	})
	for _, tc := range []struct{ portRange, want string }{
		{"4200-4203", "4200|4201|http://localhost:4200"},
		{"", "||http://localhost:"},
	} {
		d.cfg.Projects[1].PortRange = tc.portRange
		if _, err := d.handleDev(context.Background(), s.ID, true); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", "-c", tm.sessions[s.ID+"-dev-1"])
		cmd.Dir = lane
		if got, err := cmd.CombinedOutput(); err != nil || string(got) != tc.want {
			t.Fatalf("range %q: got %q, %v; want %q", tc.portRange, got, err, tc.want)
		}
	}
	if _, ok := d.ports.Get(session.LaneID(s.ID, "web")); !ok {
		t.Fatal("missing lease for web lane")
	}
}

func TestHandleDevRefusesExhaustedRange(t *testing.T) {
	d, tm := parallelDevDaemon(t, nil, devSession("lola-app-eng-1"))
	d.ports.InUse = func(int) bool { return true }
	_, err := d.handleDev(context.Background(), "lola-app-eng-1", true)
	if err == nil || !strings.Contains(err.Error(), "port range exhausted") {
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
