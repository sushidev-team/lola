package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/tmux"
)

// fakeDevTmux is an in-memory tmux server for the dev toggle: it records what
// was started and killed, so a test can assert the take-over ORDER (kill the
// previous holder before starting here) without a tmux binary.
type fakeDevTmux struct {
	mu        sync.Mutex
	sessions  map[string]string // name -> command
	kept      map[string]bool   // name -> remain-on-exit applied
	log       []string          // "start <name>" / "kill <name>", in order
	tree      []string          // names torn down with their process GROUP
	listErr   error
	startErr  map[string]error
	available bool
}

func newFakeDevTmux(names ...string) *fakeDevTmux {
	f := &fakeDevTmux{
		sessions:  map[string]string{},
		kept:      map[string]bool{},
		startErr:  map[string]error{},
		available: true,
	}
	for _, n := range names {
		f.sessions[n] = "pre-existing"
	}
	return f
}

func (f *fakeDevTmux) Available() bool { return f.available }

func (f *fakeDevTmux) ListSessions(context.Context) ([]tmux.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]tmux.Session, 0, len(f.sessions))
	for name := range f.sessions {
		out = append(out, tmux.Session{Name: name})
	}
	return out, nil
}

func (f *fakeDevTmux) Has(_ context.Context, name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.sessions[name]
	return ok
}

func (f *fakeDevTmux) NewSession(_ context.Context, name, _, command string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.startErr[name]; err != nil {
		return err
	}
	f.sessions[name] = command
	f.log = append(f.log, "start "+name)
	return nil
}

func (f *fakeDevTmux) KillSession(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, name)
	f.log = append(f.log, "kill "+name)
	return nil
}

// KillSessionTree is what the dev paths actually call: it stands for killing the
// pane's whole process group, which is what frees the port.
func (f *fakeDevTmux) KillSessionTree(ctx context.Context, name string) error {
	f.mu.Lock()
	f.tree = append(f.tree, name)
	f.mu.Unlock()
	return f.KillSession(ctx, name)
}

func (f *fakeDevTmux) KeepDeadPane(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kept[name] = true
	return nil
}

func (f *fakeDevTmux) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.sessions))
	for n := range f.sessions {
		out = append(out, n)
	}
	return out
}

func (f *fakeDevTmux) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

// devDaemon builds a daemon whose project configures dev_commands, with the
// given sessions in the store and their worktrees on disk (handleDev refuses a
// session whose checkout is gone).
func devDaemon(t *testing.T, commands []string, sessions ...session.Session) (*Daemon, *fakeDevTmux) {
	t.Helper()
	p := config.Project{Name: "app", Path: "/tmp/app", DevCommands: commands}
	d := newTestDaemon(t, testConfig(p), nil, nil)
	tm := newFakeDevTmux()
	d.devTmux = func() devTmux { return tm }
	for _, s := range sessions {
		dir := filepath.Join(d.home, "worktrees", "app", s.ID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("worktree dir: %v", err)
		}
		d.sessions.Upsert(s)
	}
	return d, tm
}

func devSession(id string) session.Session {
	return session.Session{ID: id, Source: "native", Project: "app"}
}

func TestHandleDevStartsOneTabPerCommandAndKeepsTheDeadPane(t *testing.T) {
	d, tm := devDaemon(t, []string{"composer dev", "npm run dev"}, devSession("lola-app-eng-1"))

	got, err := d.handleDev(context.Background(), "lola-app-eng-1", true)
	if err != nil {
		t.Fatalf("handleDev on: %v", err)
	}
	if !got.Active || len(got.Commands) != 2 {
		t.Errorf("DevData = %+v, want active with 2 commands", got)
	}
	for i, want := range []string{"lola-app-eng-1-dev-1", "lola-app-eng-1-dev-2"} {
		cmd, ok := tm.sessions[want]
		if !ok {
			t.Fatalf("dev tab %s was not started (have %v)", want, tm.names())
		}
		// The command runs under the env-exporting wrapper, and it is the
		// COMMAND that is exec'd — see lolaenv.CommandLine.
		if !strings.Contains(cmd, ".lola/env") || !strings.Contains(cmd, "exec "+[]string{"composer dev", "npm run dev"}[i]) {
			t.Errorf("tab %s runs %q, want the env-exporting wrapper around the command", want, cmd)
		}
		if !tm.kept[want] {
			t.Errorf("tab %s did not get remain-on-exit — a crashed dev server would vanish", want)
		}
	}
	// The store reflects the toggle immediately, without waiting for an observe
	// cycle, so the UI that issued it re-renders straight away.
	s, _ := d.sessions.Get("lola-app-eng-1")
	if !s.DevActive || s.DevTabs != 2 {
		t.Errorf("session record = active:%v tabs:%d, want active with 2 tabs", s.DevActive, s.DevTabs)
	}
}

func TestHandleDevMovesTheTabsOffTheProjectsOtherSession(t *testing.T) {
	d, tm := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"), devSession("lola-app-eng-2"))
	if _, err := d.handleDev(context.Background(), "lola-app-eng-1", true); err != nil {
		t.Fatalf("handleDev on first: %v", err)
	}

	got, err := d.handleDev(context.Background(), "lola-app-eng-2", true)
	if err != nil {
		t.Fatalf("handleDev on second: %v", err)
	}
	if got.Stopped != "lola-app-eng-1" {
		t.Errorf("Stopped = %q, want the session it was taken from", got.Stopped)
	}
	if _, still := tm.sessions["lola-app-eng-1-dev-1"]; still {
		t.Error("the previous holder's dev tab is still running — the port is still bound")
	}
	if _, ok := tm.sessions["lola-app-eng-2-dev-1"]; !ok {
		t.Error("the new holder's dev tab was not started")
	}
	// Order is load-bearing: starting before killing leaves the new process
	// dead on arrival with "address already in use".
	calls := tm.calls()
	killAt, startAt := -1, -1
	for i, c := range calls {
		if c == "kill lola-app-eng-1-dev-1" {
			killAt = i
		}
		if c == "start lola-app-eng-2-dev-1" && startAt == -1 {
			startAt = i
		}
	}
	if killAt == -1 || startAt == -1 || killAt > startAt {
		t.Errorf("want the old tab killed BEFORE the new one starts, got %v", calls)
	}
	// The take-over must kill the process GROUP, not just the tmux session: a
	// plain kill-session leaves `php artisan serve` orphaned on the port and the
	// new dev server silently starts on 8001.
	if len(tm.tree) == 0 || tm.tree[0] != "lola-app-eng-1-dev-1" {
		t.Errorf("previous holder was not torn down with its process group, got %v", tm.tree)
	}
	prev, _ := d.sessions.Get("lola-app-eng-1")
	if prev.DevActive {
		t.Error("the previous holder still reads as active")
	}
}

func TestHandleDevOffStopsEveryTabIncludingOnesConfigNoLongerKnows(t *testing.T) {
	d, tm := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	// A tab left over from a longer dev_commands list: discovery is by LISTING,
	// so shortening the config must not strand a running process.
	tm.sessions["lola-app-eng-1-dev-1"] = "composer dev"
	tm.sessions["lola-app-eng-1-dev-2"] = "npm run dev"
	d.markDev("lola-app-eng-1", true, 2)

	got, err := d.handleDev(context.Background(), "lola-app-eng-1", false)
	if err != nil {
		t.Fatalf("handleDev off: %v", err)
	}
	if got.Active {
		t.Error("DevData still reports active after a stop")
	}
	for _, name := range []string{"lola-app-eng-1-dev-1", "lola-app-eng-1-dev-2"} {
		if _, still := tm.sessions[name]; still {
			t.Errorf("%s survived the stop", name)
		}
	}
	s, _ := d.sessions.Get("lola-app-eng-1")
	if s.DevActive || s.DevTabs != 0 {
		t.Errorf("session record = active:%v tabs:%d, want inactive", s.DevActive, s.DevTabs)
	}
}

func TestHandleDevRefusesWithoutConfiguredCommands(t *testing.T) {
	d, tm := devDaemon(t, nil, devSession("lola-app-eng-1"))

	_, err := d.handleDev(context.Background(), "lola-app-eng-1", true)
	if err == nil || !strings.Contains(err.Error(), "dev_commands") {
		t.Fatalf("want a dev_commands error, got %v", err)
	}
	if len(tm.names()) != 0 {
		t.Errorf("nothing should have been started, got %v", tm.names())
	}
}

func TestHandleDevUnknownSession(t *testing.T) {
	d, _ := devDaemon(t, []string{"composer dev"})
	if _, err := d.handleDev(context.Background(), "lola-app-nope", true); err == nil {
		t.Fatal("want an error for an unknown session")
	}
}

func TestReconcileDevTabsDerivesStateFromLiveTabsOnly(t *testing.T) {
	d, _ := devDaemon(t, []string{"composer dev", "npm run dev"}, devSession("lola-app-eng-1"), devSession("lola-app-eng-2"))
	d.markDev("lola-app-eng-2", true, 1) // stale: its tab is gone from tmux
	d.deadPanes = func(context.Context) (map[string]bool, error) {
		// The first tab's command crashed; remain-on-exit kept the session, so
		// only the pane's death says it is no longer serving anything.
		return map[string]bool{"lola-app-eng-1-dev-1": true}, nil
	}
	alive := map[string]bool{
		"lola-app-eng-1":       true,
		"lola-app-eng-1-dev-1": true,
		"lola-app-eng-1-dev-2": true,
		"lola-app-eng-2":       true,
	}

	if !d.reconcileDevTabs(context.Background(), alive) {
		t.Fatal("want reconcile to report a change")
	}
	s1, _ := d.sessions.Get("lola-app-eng-1")
	if !s1.DevActive || s1.DevTabs != 1 {
		t.Errorf("session 1 = active:%v tabs:%d, want active with only the LIVE tab counted", s1.DevActive, s1.DevTabs)
	}
	s2, _ := d.sessions.Get("lola-app-eng-2")
	if s2.DevActive || s2.DevTabs != 0 {
		t.Errorf("session 2 = active:%v tabs:%d, want inactive (its tabs are gone)", s2.DevActive, s2.DevTabs)
	}
}

func TestReconcileDevTabsKeepsLastKnownWhenTheProbeFails(t *testing.T) {
	// A failed probe must not flip the toggle in either direction: a false "off"
	// invites a restart that kills a healthy dev server, a false "on" hides one
	// that is gone.
	d, _ := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	d.markDev("lola-app-eng-1", true, 1)
	d.deadPanes = func(context.Context) (map[string]bool, error) {
		return nil, context.DeadlineExceeded
	}

	if d.reconcileDevTabs(context.Background(), map[string]bool{"lola-app-eng-1-dev-1": true}) {
		t.Error("a failed probe should change nothing")
	}
	s, _ := d.sessions.Get("lola-app-eng-1")
	if !s.DevActive {
		t.Error("last-known state was discarded on a probe failure")
	}
}

func TestReconcileDevTabsNeedsNoProbeWhenNoTabsExist(t *testing.T) {
	d, _ := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	d.markDev("lola-app-eng-1", true, 1)
	probed := false
	d.deadPanes = func(context.Context) (map[string]bool, error) {
		probed = true
		return nil, nil
	}

	if !d.reconcileDevTabs(context.Background(), map[string]bool{"lola-app-eng-1": true}) {
		t.Fatal("want the stale active flag cleared")
	}
	if probed {
		t.Error("a cycle with no dev tabs anywhere must not exec the pane probe")
	}
	s, _ := d.sessions.Get("lola-app-eng-1")
	if s.DevActive {
		t.Error("session still reads as active with no dev tabs on the server")
	}
}
