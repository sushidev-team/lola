package daemon

import (
	"testing"

	"io"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/session"
)

// The dev-server forward: an ACTIVE session's loopback addresses republished on
// one private interface, so a phone can open the app an agent is building.
//
// Every test here drives the sync pass with the listener seam stubbed. What a
// forward DOES with bytes is internal/devforward's own business and is tested
// there against real sockets; what matters here is WHICH forwards exist, which
// is the half that decides what is exposed.

// fakeForwards records what was opened and hands back closable stubs.
type fakeForwards struct {
	opened []string
	closed []string
}

func (f *fakeForwards) install(t *testing.T, d *Daemon) {
	t.Helper()
	d.forwardHosts = func() []string { return []string{"192.168.20.3"} }
	d.forwardOpen = func(host, target string) (string, io.Closer, error) {
		f.opened = append(f.opened, host+" -> "+target)
		return host + ":39000", closerFunc(func() error {
			f.closed = append(f.closed, target)
			return nil
		}), nil
	}
}

// closerFunc adapts a function to io.Closer, which is the whole surface the
// daemon uses a forward through.
type closerFunc func() error

func (c closerFunc) Close() error { return c() }

func activeSession(id string, urls ...string) session.Session {
	return session.Session{
		ID: id, Source: "native", TmuxName: id,
		DevActive: true, DevURLs: urls,
	}
}

func newForwardDaemon(t *testing.T, on bool) (*Daemon, *fakeForwards) {
	t.Helper()
	d := newRemoteTestDaemon(t)
	d.cfg.Remote = config.RemoteConfig{DevForward: on}
	f := &fakeForwards{}
	f.install(t, d)
	t.Cleanup(d.stopDevForwards)
	return d, f
}

func TestForwardsPublishAnActiveSessionsLoopbackURLs(t *testing.T) {
	d, f := newForwardDaemon(t, true)
	d.sessions.Upsert(activeSession("acc-1", "http://127.0.0.1:8000", "http://127.0.0.1:5173"))

	d.syncDevForwards()

	if len(f.opened) != 2 {
		t.Fatalf("opened %v, want both addresses", f.opened)
	}
	if got := d.devForwardsFor("acc-1"); len(got) != 2 {
		t.Fatalf("published %v, want two URLs", got)
	}
	// And they reach the wire, which is how a phone learns about them.
	s, _ := d.sessions.Get("acc-1")
	if len(s.DevForwards) != 2 {
		t.Errorf("session record carries %v", s.DevForwards)
	}
}

// OFF UNLESS ASKED FOR. Publishing a dev server on a network is a decision, so
// a daemon that was not asked opens nothing.
func TestForwardsAreOffUnlessConfigured(t *testing.T) {
	d, f := newForwardDaemon(t, false)
	d.sessions.Upsert(activeSession("acc-2", "http://127.0.0.1:8000"))

	d.syncDevForwards()

	if len(f.opened) != 0 {
		t.Fatalf("opened %v with dev_forward off", f.opened)
	}
	if got := d.devForwardsFor("acc-2"); len(got) != 0 {
		t.Errorf("published %v", got)
	}
}

// A session that is not the active one is not published, whatever addresses it
// last printed: only one session's dev servers are running at a time, and the
// records of the others are stale by construction.
func TestOnlyActiveSessionsAreForwarded(t *testing.T) {
	d, f := newForwardDaemon(t, true)
	idle := activeSession("acc-3", "http://127.0.0.1:8000")
	idle.DevActive = false
	d.sessions.Upsert(idle)

	d.syncDevForwards()

	if len(f.opened) != 0 {
		t.Fatalf("opened %v for an inactive session", f.opened)
	}
}

// The lifetime IS the session's activity: when it stops being active the
// listener goes with it, which is what keeps the exposure bounded without a
// timer anyone has to remember.
func TestAForwardEndsWhenTheSessionStopsBeingActive(t *testing.T) {
	d, f := newForwardDaemon(t, true)
	d.sessions.Upsert(activeSession("acc-4", "http://127.0.0.1:8000"))
	d.syncDevForwards()

	d.sessions.Update("acc-4", func(s *session.Session) bool {
		s.DevActive = false
		return true
	})
	d.syncDevForwards()

	if len(f.closed) != 1 {
		t.Fatalf("closed %v, want the one forward", f.closed)
	}
	if got := d.devForwardsFor("acc-4"); len(got) != 0 {
		t.Errorf("still published: %v", got)
	}
	s, _ := d.sessions.Get("acc-4")
	if len(s.DevForwards) != 0 {
		t.Errorf("the record still carries %v", s.DevForwards)
	}
}

// A dev tab that restarted on another port changes DevURLs; the forward follows
// rather than pointing at whatever took the old one.
func TestAForwardFollowsAChangedAddress(t *testing.T) {
	d, f := newForwardDaemon(t, true)
	d.sessions.Upsert(activeSession("acc-5", "http://127.0.0.1:8000"))
	d.syncDevForwards()

	d.sessions.Update("acc-5", func(s *session.Session) bool {
		s.DevURLs = []string{"http://127.0.0.1:8001"}
		return true
	})
	d.syncDevForwards()

	if len(f.closed) != 1 || f.closed[0] != "127.0.0.1:8000" {
		t.Fatalf("closed %v, want the old address", f.closed)
	}
	if len(f.opened) != 2 || f.opened[1] != "192.168.20.3 -> 127.0.0.1:8001" {
		t.Fatalf("opened %v, want the new address", f.opened)
	}
}

// Running twice with nothing changed opens and closes nothing: the pass runs on
// every observe cycle, so a churning one would restart a listener under a
// browser every thirty seconds.
func TestSyncIsIdempotent(t *testing.T) {
	d, f := newForwardDaemon(t, true)
	d.sessions.Upsert(activeSession("acc-6", "http://127.0.0.1:8000"))

	d.syncDevForwards()
	d.syncDevForwards()
	d.syncDevForwards()

	if len(f.opened) != 1 {
		t.Fatalf("opened %v across three passes", f.opened)
	}
	if len(f.closed) != 0 {
		t.Fatalf("closed %v across three passes", f.closed)
	}
}

// THE RAIL. Only a loopback address is ever published — the target comes from
// the session's own DevURLs, and anything else in that list is skipped rather
// than forwarded, so a LAN or public address cannot turn this into a proxy for
// another machine.
func TestOnlyLoopbackTargetsAreForwarded(t *testing.T) {
	d, f := newForwardDaemon(t, true)
	d.sessions.Upsert(activeSession("acc-7",
		"http://192.168.20.9:8000", // another machine
		"http://example.com/app",   // a name
		"ftp://127.0.0.1:21",       // not http
		"http://127.0.0.1:3000",    // the only publishable one
	))

	d.syncDevForwards()

	if len(f.opened) != 1 || f.opened[0] != "192.168.20.3 -> 127.0.0.1:3000" {
		t.Fatalf("opened %v, want only the loopback address", f.opened)
	}
}

// vite prints "Local: http://localhost:5175/" and internal/devurl carries the
// NAME through, so refusing every name meant the bundler was silently the one
// address that never appeared. localhost is the single exception, resolved here
// rather than at dial time; any other name would make the check a DNS lookup
// whose answer can change before the dial.
func TestLocalhostIsForwardedAndOtherNamesAreNot(t *testing.T) {
	d, f := newForwardDaemon(t, true)
	d.sessions.Upsert(activeSession("acc-10",
		"http://localhost:5175",
		"http://LOCALHOST:5176",
		"http://db.internal:5432",
	))

	d.syncDevForwards()

	want := []string{
		"192.168.20.3 -> 127.0.0.1:5175",
		"192.168.20.3 -> 127.0.0.1:5176",
	}
	if len(f.opened) != len(want) {
		t.Fatalf("opened %v, want %v", f.opened, want)
	}
	for i, w := range want {
		if f.opened[i] != w {
			t.Errorf("opened[%d] = %q, want %q", i, f.opened[i], w)
		}
	}
}

// A machine with no private address has nowhere to publish, and that is not an
// error: the dev servers still work on the Mac itself.
func TestNoPrivateAddressPublishesNothing(t *testing.T) {
	d, f := newForwardDaemon(t, true)
	d.forwardHosts = func() []string { return nil }
	d.sessions.Upsert(activeSession("acc-8", "http://127.0.0.1:8000"))

	d.syncDevForwards()

	if len(f.opened) != 0 {
		t.Fatalf("opened %v with no address to bind", f.opened)
	}
}

// Shutdown closes them, because a listener that outlives its daemon is a port
// nobody can account for.
func TestStopClosesEveryForward(t *testing.T) {
	d, f := newForwardDaemon(t, true)
	d.sessions.Upsert(activeSession("acc-9", "http://127.0.0.1:8000", "http://127.0.0.1:5173"))
	d.syncDevForwards()

	d.stopDevForwards()

	if len(f.closed) != 2 {
		t.Fatalf("closed %v, want both", f.closed)
	}
	d.stopDevForwards() // idempotent
}
