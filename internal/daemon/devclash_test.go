package daemon

// Detection half of the port-clash explanation (internal/daemon/devclash.go):
// reading a DEAD dev tab once, deciding whether it lost a port race, and naming
// the process that won it. No signals are sent here — the kill half lives in
// devclash_unix_test.go.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sushidev-team/lola/internal/portproc"
	"github.com/sushidev-team/lola/internal/session"
)

// deadDevTabs wires a daemon whose single dev tab exists but whose pane is dead
// (remain-on-exit), which is exactly what a command that exited instantly leaves
// behind.
func deadDevTabs(d *Daemon, pane string, reads *int) map[string]bool {
	d.deadPanes = func(context.Context) (map[string]bool, error) {
		return map[string]bool{"lola-app-eng-1-dev-1": true}, nil
	}
	// Counted by the clash scan's own tail length: the URL scan shares this seam
	// and reads the LIVE tabs with a much longer tail.
	d.paneTail = func(_ context.Context, _ string, lines int) (string, error) {
		if reads != nil && lines == devClashScanLines {
			*reads++
		}
		return pane, nil
	}
	return map[string]bool{"lola-app-eng-1": true, "lola-app-eng-1-dev-1": true}
}

// The failure this feature exists for: `wails3 dev` prints one line and exits,
// the pane usually clears itself, and the tab reads as "dead, no reason given" —
// while the cause is a process somewhere else on the machine entirely.
func TestReconcileDevTabsExplainsWhyADevTabLostItsPort(t *testing.T) {
	d, _ := devDaemon(t, []string{"cd desktop && wails3 dev"}, devSession("lola-app-eng-1"))
	alive := deadDevTabs(d, "ERROR  listen tcp 127.0.0.1:9245: bind: address already in use\n", nil)
	listeners(d, portproc.Listener{
		PID: 4242, Command: "node", Port: 9245, Addr: "127.0.0.1:9245",
		Dir: "/Users/someone/code/app/desktop/frontend",
	})

	if !d.reconcileDevTabs(context.Background(), alive) {
		t.Fatal("want reconcile to report the clash it recorded")
	}
	s, _ := d.sessions.Get("lola-app-eng-1")
	c := s.DevClash
	if c == nil {
		t.Fatal("no clash recorded for a tab that died on a taken port")
	}
	if c.Port != 9245 || c.PID != 4242 || c.Proc != "node" {
		t.Errorf("clash = :%d held by %s (pid %d), want :9245 held by node (pid 4242)", c.Port, c.Proc, c.PID)
	}
	if c.Tab != "lola-app-eng-1-dev-1" {
		t.Errorf("clash tab = %q, want the dev tab that died", c.Tab)
	}
	// The label comes from CONFIG, never from the pane: it is rendered beside a
	// kill button, so it must not be attacker-influenceable text.
	if c.Command != "cd desktop && wails3 dev" {
		t.Errorf("clash command = %q, want the dev_commands entry", c.Command)
	}
	// A holder outside lola's worktrees is the user's own process — the case the
	// sweep deliberately refuses to touch, and the reason this asks instead.
	if c.Ours {
		t.Error("a holder in the user's own checkout must not be reported as lola's leftover")
	}
}

// A stray server of an earlier session is lola's own mess. It is still only ever
// killed by a human here, but the UIs word the question differently for it.
func TestDevClashMarksLolasOwnLeftoverAsOurs(t *testing.T) {
	d, _ := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	alive := deadDevTabs(d, "Failed to listen on 127.0.0.1:8000 (reason: Address already in use)\n", nil)
	listeners(d, portproc.Listener{
		PID: 77, Command: "php", Port: 8000,
		Dir: filepath.Join(d.home, "worktrees", "app", "lola-app-eng-2"),
	})

	d.reconcileDevTabs(context.Background(), alive)
	s, _ := d.sessions.Get("lola-app-eng-1")
	if s.DevClash == nil || !s.DevClash.Ours {
		t.Fatalf("clash = %+v, want it flagged as lola's own leftover", s.DevClash)
	}
}

// A dead pane never changes again, so the read and the lsof pass happen ONCE per
// death — otherwise every 30s cycle would spend both, forever, on a tab nobody
// has restarted.
func TestDevClashIsExaminedOncePerDeath(t *testing.T) {
	d, _ := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	reads := 0
	alive := deadDevTabs(d, "no port trouble here, just a crash\n", &reads)
	lsofCalls := 0
	d.portListeners = func(context.Context) ([]portproc.Listener, error) {
		lsofCalls++
		return nil, nil
	}

	for range 3 {
		d.reconcileDevTabs(context.Background(), alive)
	}
	if reads != 1 {
		t.Errorf("read the dead pane %d times, want exactly 1", reads)
	}
	// The pane said nothing about a port, so lsof is never asked at all.
	if lsofCalls != 0 {
		t.Errorf("asked lsof %d times for a pane with no port failure, want 0", lsofCalls)
	}
}

// FAIL CLOSED: with nobody holding the port there is nothing to offer and
// nothing to explain — whatever won the race has already exited.
func TestDevClashIsNotRecordedWhenThePortIsFreeAgain(t *testing.T) {
	d, _ := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	alive := deadDevTabs(d, "listen tcp 127.0.0.1:9245: bind: address already in use\n", nil)
	d.portListeners = func(context.Context) ([]portproc.Listener, error) { return nil, nil }

	d.reconcileDevTabs(context.Background(), alive)
	if s, _ := d.sessions.Get("lola-app-eng-1"); s.DevClash != nil {
		t.Fatalf("recorded %+v for a port nobody holds any more", s.DevClash)
	}
}

// The explanation belongs to ONE set of tabs. Restarting replaces them, so a
// stale clash — pointing at a pid that may be long gone — must not survive.
func TestDevClashIsDroppedWhenTheTabsChange(t *testing.T) {
	d, _ := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	d.sessions.Update("lola-app-eng-1", func(s *session.Session) bool {
		s.DevClash = &session.DevClash{Tab: "lola-app-eng-1-dev-1", Port: 8000, PID: 5}
		return true
	})

	d.markDev("lola-app-eng-1", true, 1)
	if s, _ := d.sessions.Get("lola-app-eng-1"); s.DevClash != nil {
		t.Fatalf("clash %+v survived a tab change", s.DevClash)
	}
}

// A tab that lives again gets its examination re-armed, so the NEXT death is
// explained too rather than being silently written off as already-checked.
func TestDevClashChecksAreReArmedWhenTheTabLivesAgain(t *testing.T) {
	d, _ := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	reads := 0
	alive := deadDevTabs(d, "listen tcp 127.0.0.1:8000: bind: address already in use\n", &reads)
	listeners(d, portproc.Listener{PID: 9, Command: "php", Port: 8000, Dir: "/elsewhere"})

	d.reconcileDevTabs(context.Background(), alive)
	// It came back up: no dead panes at all this cycle.
	d.deadPanes = func(context.Context) (map[string]bool, error) { return map[string]bool{}, nil }
	d.reconcileDevTabs(context.Background(), alive)
	if s, _ := d.sessions.Get("lola-app-eng-1"); s.DevClash != nil {
		t.Fatalf("a living tab still carries %+v", s.DevClash)
	}
	// ...and died again.
	deadDevTabs(d, "listen tcp 127.0.0.1:8000: bind: address already in use\n", &reads)
	d.reconcileDevTabs(context.Background(), alive)
	if s, _ := d.sessions.Get("lola-app-eng-1"); s.DevClash == nil {
		t.Fatal("the second death went unexplained")
	}
	if reads != 2 {
		t.Errorf("read the pane %d times across two deaths, want 2", reads)
	}
}

// Without lsof there is no holder to name, so nothing is recorded and no pane is
// read for it either.
func TestDevClashNeedsLsof(t *testing.T) {
	d, _ := devDaemon(t, []string{"composer dev"}, devSession("lola-app-eng-1"))
	reads := 0
	alive := deadDevTabs(d, "listen tcp 127.0.0.1:9245: bind: address already in use\n", &reads)
	d.portListeners = nil

	d.reconcileDevTabs(context.Background(), alive)
	if s, _ := d.sessions.Get("lola-app-eng-1"); s.DevClash != nil {
		t.Fatalf("recorded %+v without a way to check who holds the port", s.DevClash)
	}
	if reads != 0 {
		t.Errorf("read the pane %d times with no lsof to follow up with, want 0", reads)
	}
}
