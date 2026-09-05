package daemon

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/mdns"
	"github.com/sushidev-team/lola/internal/panebus"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/session"
)

// TestPaneAuxParentNamesTheParentSession pins the one piece of vocabulary the
// identity gate re-expresses from internal/runtime: which suffixes make a tmux
// session an AUXILIARY surface of another one. A suffix this drops wrongly
// would resolve a pane against the wrong session; one it fails to recognize
// makes a legitimate shell or dev tab unreachable from the phone.
func TestPaneAuxParentNamesTheParentSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{"lola-web-eng-42", ""},                        // the agent's own pane
		{"lola-web-eng-42-shell-1", "lola-web-eng-42"}, // an embedded shell tab
		{"lola-web-eng-42-shell-12", "lola-web-eng-42"},
		{"lola-web-eng-42-review", "lola-web-eng-42"}, // a visible review pass
		{"lola-web-eng-42-dev-1", "lola-web-eng-42"},  // a dev tab
		{"lola-web-eng-42-dev-3", "lola-web-eng-42"},
		{"lola-web-eng-42-shell", ""},   // no index: not the shape lola builds
		{"lola-web-eng-42-dev", ""},     // likewise
		{"lola-web-eng-42-reviews", ""}, // anchored at the end
		{"", ""},
	} {
		if got := paneAuxParent(tc.name); got != tc.want {
			t.Errorf("paneAuxParent(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestResolvePaneNameIsTheSessionStore is the identity gate's whole contract:
// the session store is the authority, an auxiliary tab resolves through its
// parent, and everything else is refused. panebus checks the SHAPE of a name
// because it owns the argv; only this can say whether a well-formed name names
// something lola actually runs, and the failure direction matters — a gate that
// answered "sure" for an unknown name would hand a remote peer a tmux attach on
// any session on the machine.
func TestResolvePaneNameIsTheSessionStore(t *testing.T) {
	d := newRemoteTestDaemon(t)
	d.sessions.Upsert(session.Session{ID: "lola-web-eng-42", TmuxName: "lola-web-eng-42"})
	// A record whose TmuxName was never filled (an adopted session) resolves
	// through paneTarget's fallback to the id, exactly as send-keys does.
	d.sessions.Upsert(session.Session{ID: "lola-api-eng-7"})

	for _, tc := range []struct {
		pane string
		ok   bool
		why  string
	}{
		{"lola-web-eng-42", true, "the session's own pane"},
		{"lola-api-eng-7", true, "a record with no TmuxName falls back to the id"},
		{"lola-web-eng-42-shell-2", true, "a shell tab of a known session"},
		{"lola-web-eng-42-review", true, "a review pane of a known session"},
		{"lola-web-eng-42-dev-1", true, "a dev tab of a known session"},
		{"lola-web-eng-99", false, "no such session"},
		{"lola-web-eng-99-shell-1", false, "a shell tab of no session"},
		{"", false, "the empty name"},
		{"lola-web-eng-4", false, "a prefix of a known session is not that session"},
	} {
		err := d.resolvePaneName(context.Background(), tc.pane)
		if tc.ok && err != nil {
			t.Errorf("resolvePaneName(%q) = %v, want nil (%s)", tc.pane, err, tc.why)
		}
		if !tc.ok {
			if err == nil {
				t.Errorf("resolvePaneName(%q) = nil, want a refusal (%s)", tc.pane, tc.why)
			} else if !errors.Is(err, errUnknownPane) {
				t.Errorf("resolvePaneName(%q) = %v, want it to wrap errUnknownPane", tc.pane, err)
			}
		}
	}
}

// TestPaneFrameKindsMapAcrossThePackageBoundary is the parity test for the one
// place two independent enums have to agree. internal/panebus and
// internal/remote deliberately do not import each other, so nothing but this
// holds their frame kinds together: a kind added to panebus and not mapped here
// would be silently DROPPED from every phone's stream, and a kind mapped onto
// the wrong constant would render keystrokes as a screen.
func TestPaneFrameKindsMapAcrossThePackageBoundary(t *testing.T) {
	want := map[panebus.FrameKind]remote.PaneKind{
		panebus.KindResync: remote.PaneResync,
		panebus.KindOutput: remote.PaneOutput,
		panebus.KindExit:   remote.PaneExit,
	}
	// Walk the enum from zero until String() stops naming a kind, so a NEW
	// panebus kind fails here rather than vanishing from the wire.
	for k := panebus.FrameKind(0); k.String() != "unknown"; k++ {
		exp, named := want[k]
		if !named {
			t.Fatalf("panebus.FrameKind %d (%s) has no mapping in toRemoteFrame; add one", k, k)
		}
		got, ok := toRemoteFrame(panebus.Frame{Kind: k})
		if !ok {
			t.Fatalf("toRemoteFrame refused kind %s, which panebus can produce", k)
		}
		if got.Kind != exp {
			t.Errorf("kind %s mapped to remote kind %d, want %d", k, got.Kind, exp)
		}
	}
	if _, ok := toRemoteFrame(panebus.Frame{Kind: panebus.FrameKind(99)}); ok {
		t.Error("an unknown kind must be dropped, never mapped onto the zero value (which is a resync)")
	}

	// The payload rides across unchanged, Seq included: the bus numbers per
	// PANE across the frames it dropped for a slow subscriber, so renumbering
	// or losing it here would hide exactly the gap the counter exists to show.
	sc := &panebus.Screen{W: 200, H: 51, Lines: []string{"a", "b"}, CursorX: 3, CursorY: 4, AltScreen: true}
	got, ok := toRemoteFrame(panebus.Frame{Kind: panebus.KindResync, Screen: sc, Seq: 77})
	if !ok {
		t.Fatal("a resync must map")
	}
	if got.Seq != 77 {
		t.Errorf("Seq = %d, want 77 forwarded verbatim", got.Seq)
	}
	if got.Screen == nil || got.Screen.Cols != 200 || got.Screen.Rows != 51 ||
		got.Screen.CursorX != 3 || got.Screen.CursorY != 4 || !got.Screen.AltScreen {
		t.Errorf("screen = %+v, want the panebus reading re-typed field for field", got.Screen)
	}
	out, _ := toRemoteFrame(panebus.Frame{Kind: panebus.KindOutput, Data: []byte("hi")})
	if string(out.Data) != "hi" {
		t.Errorf("Data = %q, want %q", out.Data, "hi")
	}
}

// TestPaneStreamCloseReleasesACopierNobodyIsReading covers the adapter's one
// real hazard. remote's pump abandons a stream the moment its connection is
// torn down, so the copier can be parked on a send that will never complete;
// if Close waited on that goroutine, or closed the panebus side first and left
// it parked, every teardown would leak a goroutine and Server.Close would stop
// being bounded — which is the property the whole shutdown ordering rests on.
func TestPaneStreamCloseReleasesACopierNobodyIsReading(t *testing.T) {
	fake := panebus.NewFake()
	reg := fake.Registry()
	t.Cleanup(func() { reg.Close() })

	bus := remotePanes{reg: reg, logf: func(string, ...any) {}}
	stream, err := bus.Subscribe(context.Background(), "lola-web-eng-42", 55, 34)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Take the opening resync so the copier is idle, then produce output and
	// never read it: the copier is now parked on the unbuffered send.
	select {
	case f := <-stream.Frames():
		if f.Kind != remote.PaneResync {
			t.Fatalf("first frame = %d, want a resync", f.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no opening resync")
	}
	fake.Pane("lola-web-eng-42").Emit([]byte("output nobody will read"))

	done := make(chan error, 1)
	go func() { done <- stream.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked on a reader that walked away; it must never wait on the consumer")
	}

	// And the copier really ended: its channel is closed, not merely quiet.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-stream.Frames():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the copier never closed its channel after Close")
		}
	}
}

// TestRemoteStaysDownWhenTheTableDoesNotAskForAListener pins the two off
// switches. `enabled = false` is the default and the absent-table value;
// `bind = "off"` is the keep-my-settings-but-stop variant. Both mean no
// listener, no pane registry and — the part worth asserting — no attempt to
// build either, because the construction seam is where a real tmux client and
// the device identity would come from.
func TestRemoteStaysDownWhenTheTableDoesNotAskForAListener(t *testing.T) {
	for _, tc := range []struct {
		name string
		rc   config.RemoteConfig
	}{
		{"absent table", config.RemoteConfig{}},
		{"disabled", config.RemoteConfig{Enabled: false, Bind: "localhost", Port: 7717}},
		{"bind off", config.RemoteConfig{Enabled: true, Bind: "off", Port: 7717}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newRemoteTestDaemon(t)
			d.cfg.Remote = tc.rc
			built := 0
			d.paneRegistry = func() *panebus.Registry {
				built++
				return panebus.NewFake().Registry()
			}
			d.startRemote(context.Background())
			t.Cleanup(d.stopRemote)

			if d.remote != nil {
				t.Error("a table that does not ask for a listener must not bind one")
			}
			if built != 0 {
				t.Errorf("pane registry built %d times, want 0 — nothing may be constructed for a listener that is not starting", built)
			}
		})
	}
}

// newRemoteTestDaemon builds a daemon on a temp LOLA_HOME with nothing external
// wired: no tmux, no git, no network. It is the base every remote test starts
// from, tagged and untagged alike.
func newRemoteTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	d := newDaemon(remoteTestConfig(), &linear.Fake{}, log.New(io.Discard, "", 0), home)
	// NO REAL dns-sd. A listener started in a test would otherwise advertise
	// this service on whatever network the machine running the suite is on,
	// which is both a side effect outside the test and a process to leak.
	d.mdnsStart = func(ctx context.Context, _ string, _ []string) (mdns.Process, error) {
		return stubMDNS{ctx: ctx}, nil
	}
	return d
}

// stubMDNS is a registration that lives until its context is cancelled, which
// is what the real child does.
type stubMDNS struct{ ctx context.Context }

func (p stubMDNS) Wait() error {
	<-p.ctx.Done()
	return p.ctx.Err()
}

// newLoggingRemoteDaemon is newRemoteTestDaemon with the daemon log captured,
// for the tests whose subject is what an operator is told.
func newLoggingRemoteDaemon(t *testing.T) (*Daemon, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	var buf bytes.Buffer
	return newDaemon(remoteTestConfig(), &linear.Fake{}, log.New(&buf, "", 0), home), &buf
}

// freePort asks the kernel for a port and gives it straight back. The window
// between that and the real bind is a test-only race and is the standard price
// for an ephemeral port on a listener whose port is configuration; it is the
// same helper internal/remote's own tests use.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	defer ln.Close()
	_, p, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("probe addr: %v", err)
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		t.Fatalf("probe port: %v", err)
	}
	return n
}

func remoteTestConfig() *config.Config {
	return &config.Config{
		Defaults: config.Defaults{PollInterval: time.Minute, ConcurrencyCap: 10, GlobalCap: 10},
	}
}

// TestPaneStreamCloseIsIdempotentOnItsOwn pins the promise the doc comment
// makes rather than the one its caller happens to enforce.
//
// Close did a check-then-close on a channel, which is not atomic: two
// concurrent calls both took the default branch and the second panicked on
// close of a closed channel. It was safe only because its single caller wraps
// it in a sync.Once of its own — a property of that caller, not of this type,
// and the next caller would have inherited a landmine. Every other one-shot in
// this codebase is a sync.Once.
func TestPaneStreamCloseIsIdempotentOnItsOwn(t *testing.T) {
	fake := panebus.NewFake()
	reg := fake.Registry()
	t.Cleanup(func() { reg.Close() })

	bus := remotePanes{reg: reg, logf: func(string, ...any) {}}
	stream, err := bus.Subscribe(context.Background(), "lola-web-eng-42", 55, 34)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := stream.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()
	}
	wg.Wait()

	// Still closed, and still exactly once.
	if err := stream.Close(); err != nil {
		t.Errorf("a later Close: %v", err)
	}
}

// TestResyncCarriesWhetherTheCursorIsHidden closes the loop on DECTCEM.
//
// vtterm has modelled cursor visibility since the pane bus was written, but
// nothing carried it to the wire, so a client attaching mid-session drew a
// caret over a pane that had hidden one — which on an agent pane reads as
// "waiting for your input" when it is not. The field is stated in the NEGATIVE
// on the wire so the common case costs nothing and, more importantly, so a
// client reading it against a daemon that does not write it degrades to
// "visible" rather than painting no caret at all.
func TestResyncCarriesWhetherTheCursorIsHidden(t *testing.T) {
	for _, tc := range []struct {
		name    string
		visible bool
	}{
		{"a visible caret", true},
		{"a hidden caret", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := toRemoteScreen(&panebus.Screen{W: 80, H: 24, CursorVisible: tc.visible})
			if got.CursorVisible != tc.visible {
				t.Fatalf("the adapter dropped cursor visibility: got %v, want %v", got.CursorVisible, tc.visible)
			}
		})
	}
}
