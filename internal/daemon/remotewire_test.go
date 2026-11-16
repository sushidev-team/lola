package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/mdns"
	"github.com/sushidev-team/lola/internal/panebus"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
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
func newLoggingRemoteDaemon(t *testing.T) (*Daemon, *syncBuf) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	buf := &syncBuf{}
	return newDaemon(remoteTestConfig(), &linear.Fake{}, log.New(buf, "", 0), home), buf
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

// TestNewDaemonHasRegistryAndPairer pins that both exist straight out of
// newDaemon, before Run: every test daemon, startRemote and d.handle meet them
// non-nil, and a started listener authenticates against THIS registry.
func TestNewDaemonHasRegistryAndPairer(t *testing.T) {
	d := newRemoteTestDaemon(t)
	if d.devices == nil || d.pairer == nil || d.watch == nil {
		t.Fatalf("newDaemon left devices=%v pairer=%v watch=%v", d.devices, d.pairer, d.watch)
	}
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: freePort(t)}
	d.paneRegistry = func() *panebus.Registry { return panebus.NewFake().Registry() }
	d.startRemote(context.Background())
	t.Cleanup(d.stopRemote)
	if d.remote == nil {
		t.Fatalf("no listener without Run: %s", d.remoteErr)
	}
}

// TestRegistryChangeNudgesTheRelay pins the subscription newDaemon makes: a
// registry change reaches devicesChanged (spec 03 hangs the relay's rendezvous
// set on it).
func TestRegistryChangeNudgesTheRelay(t *testing.T) {
	d := newRemoteTestDaemon(t)
	fired := make(chan struct{}, 4)
	d.devices.Subscribe(func() { fired <- struct{}{} })
	addPairedPhone(t, d, nil)
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("a registry change notified no subscriber")
	}
}

// TestHandleRemoteRefusesLocalOnly is the defence behind the tier gate: a
// local-only or unclassified command that reaches handleRemote anyway is
// refused, logged, and never routed to d.handle.
func TestHandleRemoteRefusesLocalOnly(t *testing.T) {
	d, logBuf := newLoggingRemoteDaemon(t)
	cmds := append([]string{"stop", "reload", "hookEvent", "no-such-command", ""}, protocol.LocalOnlyCommands...)
	for _, cmd := range cmds {
		resp := d.handleRemote(context.Background(), protocol.Request{Cmd: cmd})
		if resp.OK || resp.Error != "not available remotely" {
			t.Errorf("handleRemote(%q) = %+v, want the fixed refusal", cmd, resp)
		}
	}
	if !strings.Contains(logBuf.String(), "tier gate bypassed") {
		t.Errorf("the refusal left no log line: %q", logBuf.String())
	}
}

// TestPairCommandsNotOnBridge: the sandbox hook bridge admits only the two
// agent callbacks, so no pairing or device command can be reached from inside
// a sandbox.
func TestPairCommandsNotOnBridge(t *testing.T) {
	for _, cmd := range protocol.LocalOnlyCommands {
		if hookBridgeCmds[cmd] {
			t.Errorf("%s must not be on the sandbox hook bridge", cmd)
		}
	}
}

// answerElement posts a question element into session s1 and returns its id,
// optionally scoped to an SSH connection.
func answerElement(t *testing.T, r *sshRig, conn string) string {
	t.Helper()
	q, err := r.d.handleSSHAgentPost("s1", protocol.SSHAgentPostArgs{Argv: []string{"question", "--ref", "q", "Restart?"}})
	if err != nil {
		t.Fatalf("question: %v", err)
	}
	if conn != "" {
		store, err := r.d.sshStore("s1", false)
		if err != nil || store == nil {
			t.Fatalf("store: %v", err)
		}
		if err := store.Update(func(st *sshstore.State) error {
			st.Item(q.ID).Element.Conn = conn
			return nil
		}); err != nil {
			t.Fatalf("scope: %v", err)
		}
	}
	return q.ID
}

func sshAnswerReq(t *testing.T, element string) protocol.Request {
	t.Helper()
	raw, err := json.Marshal(protocol.SSHAnswerArgs{Session: "s1", Element: element, Revision: 1, Text: "yes"})
	if err != nil {
		t.Fatal(err)
	}
	return protocol.Request{Cmd: protocol.CmdSSHAnswer, Session: "s1", Args: raw}
}

// TestHandleRemoteRefusesSSHScopedAnswer: a phone may answer an agent's plain
// question, but a question scoped to an SSH connection is an SSH human act and
// stays on the Mac. Anything unreadable refuses too.
func TestHandleRemoteRefusesSSHScopedAnswer(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	plain := answerElement(t, r, "")

	if resp := r.d.handleRemote(context.Background(), sshAnswerReq(t, plain)); !resp.OK {
		t.Fatalf("a plain question must be answerable from a phone: %s", resp.Error)
	}

	r2 := newSSHRig(t, hostDev)
	r2.session("s1", "")
	scoped := answerElement(t, r2, "c1")
	resp := r2.d.handleRemote(context.Background(), sshAnswerReq(t, scoped))
	if resp.OK || !strings.Contains(resp.Error, "answer it on the Mac") {
		t.Fatalf("an ssh-scoped question was answered from a phone: %+v", resp)
	}

	for name, req := range map[string]protocol.Request{
		"unknown element": sshAnswerReq(t, "nope"),
		"bad args":        {Cmd: protocol.CmdSSHAnswer, Args: json.RawMessage(`{`)},
		"no session":      {Cmd: protocol.CmdSSHAnswer, Args: json.RawMessage(`{"element":"x"}`)},
	} {
		if r.d.remoteMayAnswer(req) {
			t.Errorf("%s: remoteMayAnswer = true, want false", name)
		}
	}
}

// TestRemoteAnswerScopeIsCheckedUnderTheRevisionPin: handleRemote's early read
// and the handler's update are two reads, and an agent update may scope a
// question to an SSH connection between them. The handler therefore refuses a
// phone's answer to a scoped question itself, inside the update that pins the
// revision, and the early read refuses a revision it did not see.
func TestRemoteAnswerScopeIsCheckedUnderTheRevisionPin(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	el := answerElement(t, r, "")

	// The early read only admits the revision it saw.
	stale := sshAnswerReq(t, el)
	raw, _ := json.Marshal(protocol.SSHAnswerArgs{Session: "s1", Element: el, Revision: 2, Text: "yes"})
	stale.Args = raw
	if r.d.remoteMayAnswer(stale) {
		t.Fatal("remoteMayAnswer admitted a revision the element is not at")
	}

	// The element is scoped after the early read: the handler refuses a phone...
	store, err := r.d.sshStore("s1", false)
	if err != nil || store == nil {
		t.Fatalf("store: %v", err)
	}
	if err := store.Update(func(st *sshstore.State) error {
		st.Item(el).Element.Conn = "c1"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	a := protocol.SSHAnswerArgs{Session: "s1", Element: el, Revision: 1, Text: "yes"}
	remoteCtx := context.WithValue(context.Background(), remoteCallKey{}, true)
	if _, err := r.d.handleSSHAnswer(remoteCtx, a); !errors.Is(err, errSSHAnswerOnMac) {
		t.Fatalf("phone answered a scoped question: err = %v", err)
	}
	// ...and still takes the desktop's answer.
	if _, err := r.d.handleSSHAnswer(context.Background(), a); err != nil {
		t.Fatalf("desktop answer refused: %v", err)
	}
}

// TestRemoteRecallTakesBackQueuedMessage: sshRecall is an answer-tier command
// that goes through handleRemote to the same handler the desktop uses.
func TestRemoteRecallTakesBackQueuedMessage(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	say, err := r.d.handleSSHSay(context.Background(), protocol.SSHSayArgs{Session: "s1", Text: "never mind"})
	if err != nil || say.Item == "" {
		t.Fatalf("say: %+v %v", say, err)
	}
	if got := remote.TierFor(protocol.CmdSSHRecall); got != protocol.TierAnswer {
		t.Fatalf("sshRecall tier = %q, want answer", got)
	}
	raw, _ := json.Marshal(protocol.SSHRecallArgs{Session: "s1", Item: say.Item})
	resp := r.d.handleRemote(context.Background(), protocol.Request{Cmd: protocol.CmdSSHRecall, Session: "s1", Args: raw})
	if !resp.OK {
		t.Fatalf("recall from a phone: %s", resp.Error)
	}
	var out protocol.SSHRecallData
	if err := json.Unmarshal(resp.Data, &out); err != nil || !out.Recalled {
		t.Fatalf("recall reply = %s, %v", resp.Data, err)
	}
	if s, _ := r.d.sessions.Get("s1"); strings.Contains(s.PendingSSH, "never mind") {
		t.Errorf("the recalled words are still pending: %q", s.PendingSSH)
	}
}

// TestRevokeUnsavedStopsListener: a revocation the registry could persist
// nowhere would be forgotten by a restart, so the server stops (and says why)
// until a later save succeeds, and then comes back by itself.
func TestRevokeUnsavedStopsListener(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	d := newRemoteTestDaemon(t)
	d.shutdownCtx = context.Background()
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: freePort(t)}
	d.paneRegistry = func() *panebus.Registry { return panebus.NewFake().Registry() }
	phone := addPairedPhone(t, d, nil)

	d.startRemote(context.Background())
	t.Cleanup(d.stopRemote)
	if d.remote == nil {
		t.Fatalf("no listener: %s", d.remoteErr)
	}

	// Neither devices.json nor its journal can be written in a read-only home.
	if err := os.Chmod(d.home, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(d.home, 0o700) })
	if _, err := d.devices.Revoke(phone.dev.ID); err == nil || d.devices.Unsaved() == nil {
		t.Fatalf("Revoke err=%v unsaved=%v, want an unsaved revocation", err, d.devices.Unsaved())
	}
	d.dropRemoteDevice(phone.dev.ID)

	if d.remote != nil {
		t.Fatal("the listener kept running with a revocation that lives only in memory")
	}
	if !strings.Contains(d.remoteErr, "revoked") {
		t.Errorf("remoteErr = %q, want the registry's reason", d.remoteErr)
	}
	d.startRemote(context.Background())
	if d.remote != nil {
		t.Fatal("startRemote must refuse while the registry has an unsaved revocation")
	}

	// A later save clears it, and the observe cycle brings the server back.
	if err := os.Chmod(d.home, 0o700); err != nil {
		t.Fatal(err)
	}
	addPairedPhone(t, d, nil)
	if d.devices.Unsaved() != nil {
		t.Fatal("a good save must clear Unsaved")
	}
	d.reconcileRemoteBind()
	if d.remote == nil {
		t.Fatalf("the server did not come back: %s", d.remoteErr)
	}
}

// TestReloadIdenticalRemoteDoesNotRebind: a reload that changes nothing about
// [remote] (and not whether a server should exist) must keep the live listener
// and its connections.
func TestReloadIdenticalRemoteDoesNotRebind(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	cfgPath, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(strings.Join([]string{
		"[defaults]", `poll_interval = "1m"`, "concurrency_cap = 10", "global_cap = 10", "",
		"[remote]", "enabled = true", `bind = "localhost"`, "port = " + strconv.Itoa(port), "",
	}, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	d := newDaemon(cfg, &linear.Fake{}, log.New(io.Discard, "", 0), home)
	d.shutdownCtx = context.Background()
	d.mdnsStart = func(ctx context.Context, _ string, _ []string) (mdns.Process, error) { return stubMDNS{ctx: ctx}, nil }
	d.paneRegistry = func() *panebus.Registry { return panebus.NewFake().Registry() }
	d.startRemote(context.Background())
	t.Cleanup(d.stopRemote)
	if d.remote == nil {
		t.Fatalf("no listener: %s", d.remoteErr)
	}
	before := d.remote

	if err := d.handleReload(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if d.remote != before {
		t.Error("an unchanged [remote] table must not rebuild the listener")
	}

	// A changed table does.
	cfg.Remote.Port = freePort(t)
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := d.handleReload(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if d.remote == before || d.remote == nil {
		t.Error("a changed [remote] table must rebuild the listener")
	}
}
