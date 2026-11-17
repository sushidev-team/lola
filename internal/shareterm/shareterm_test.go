package shareterm

import (
	"context"
	"encoding/base64"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/shareclient"
)

const (
	testRV    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testShare = "11111111111111111111111111111111"
	testPane  = "lola-a"
)

type stream struct {
	ch   chan remote.PaneFrame
	once sync.Once
}

func (s *stream) Frames() <-chan remote.PaneFrame { return s.ch }
func (s *stream) Close() error                    { s.once.Do(func() { close(s.ch) }); return nil }

type bus struct {
	mu     sync.Mutex
	st     *stream
	writes []string
}

func (b *bus) Subscribe(context.Context, string, int, int) (remote.PaneStream, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.st = &stream{ch: make(chan remote.PaneFrame, 16)}
	b.st.ch <- remote.PaneFrame{Kind: remote.PaneResync, Seq: 1, Screen: &remote.PaneScreen{
		Cols: 80, Rows: 24, Lines: []string{"hello"}, CursorVisible: true}}
	return b.st, nil
}
func (b *bus) Write(_ string, p []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writes = append(b.writes, string(p))
	return nil
}
func (b *bus) Scroll(context.Context, string, int) error { return nil }

type gate struct {
	mu    sync.Mutex
	level string
}

func (g *gate) KnownRV(rv string) bool { return rv == testRV }
func (g *gate) PaneAccess(_, pane string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if pane != testPane {
		return ""
	}
	return g.level
}
func (g *gate) Roster(string, []remote.Attachment) (protocol.ShareSessionsData, error) {
	return protocol.ShareSessionsData{Sessions: []protocol.SharedSession{{ID: testShare, Title: "api", Level: g.level, Pane: testPane}}}, nil
}

type rig struct {
	t     *testing.T
	srv   *remote.Server
	bus   *bus
	gate  *gate
	host  *remote.DeviceKey
	me    *remote.DeviceKey
	mu    sync.Mutex
	evts  []string
	terms *Terms
}

func newRig(t *testing.T, level string) *rig {
	t.Helper()
	r := &rig{t: t, bus: &bus{}, gate: &gate{level: level}}
	hostDir := t.TempDir()
	var err error
	if r.me, err = remote.LoadOrCreateDeviceKey(t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	phones := devices.Open(t.TempDir(), nil, nil)
	members := devices.OpenMembers(t.TempDir(), nil, nil)
	if _, err := members.AddMember("ada", r.me.SPKISHA256(), devices.HubIdentity{DeviceID: "d", PublicKey: "k", Fingerprint: "f"}); err != nil {
		t.Fatal(err)
	}
	r.srv, err = remote.Listen(context.Background(), remote.Options{
		Dir: hostDir, RelayOnly: true, Handle: func(context.Context, protocol.Request) protocol.Response { return protocol.Response{} },
		Panes: r.bus, Devices: phones, Pairer: remote.NewPairer(phones, nil, nil),
		Members: members, MemberGate: r.gate,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.srv.Close() })
	if r.host, err = remote.LoadOrCreateDeviceKey(hostDir, nil); err != nil {
		t.Fatal(err)
	}
	r.terms = New(Options{
		Emit: func(event string, data any) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if s, ok := data.(string); ok && strings.HasPrefix(event, "pty:") && !strings.HasSuffix(event, ":exit") {
				if b, err := base64.StdEncoding.DecodeString(s); err == nil {
					data = string(b)
				}
			}
			r.evts = append(r.evts, event+"|"+toString(data))
		},
		Dial:        r.dial,
		RosterEvery: 50 * time.Millisecond,
	})
	return r
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case protocol.SharedSession:
		return "roster:" + x.ID
	}
	return "?"
}

func (r *rig) dial(ctx context.Context, shareID string) (*shareclient.Conn, error) {
	slot, ok := r.srv.ReserveRelay(testRV)
	if !ok {
		r.t.Fatal("no relay slot")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer ln.Close()
	acc := make(chan net.Conn, 1)
	go func() { c, _ := ln.Accept(); acc <- c }()
	cp, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		return nil, err
	}
	go func() { _ = r.srv.ServeRelayed(<-acc, remote.RelayMeta{RV: testRV}, slot) }()
	return shareclient.Dial(ctx, cp, shareclient.Config{Cert: r.me.Certificate, HostSPKI: r.host.SPKISHA256()})
}

func (r *rig) seen(substr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.evts {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("never: %s", what)
}

func TestAttachStreamsTheSharedPaneIntoTheTerminalEvents(t *testing.T) {
	r := newRig(t, remote.LevelType)
	name := Prefix + testShare
	got, err := r.terms.Attach(name, 100, 30)
	if err != nil || got != name {
		t.Fatalf("Attach = %q, %v", got, err)
	}
	t.Cleanup(r.terms.DetachAll)
	waitFor(t, "the first screen", func() bool { return r.seen("pty:"+name+"|") && r.seen("hello") })
	r.bus.mu.Lock()
	r.bus.st.ch <- remote.PaneFrame{Kind: remote.PaneOutput, Seq: 2, Data: []byte("world")}
	r.bus.mu.Unlock()
	waitFor(t, "live output", func() bool { return r.seen("world") })
	info, ok := r.terms.Session(name)
	if !ok || info.Level != remote.LevelType || info.Pane != testPane {
		t.Fatalf("session = %+v %v", info, ok)
	}
	waitFor(t, "a roster refresh", func() bool { return r.seen("share:" + name + "|roster:" + testShare) })

	if err := r.terms.Write(name, "ls\r"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "keystrokes", func() bool {
		r.bus.mu.Lock()
		defer r.bus.mu.Unlock()
		return len(r.bus.writes) == 1 && r.bus.writes[0] == "ls\r"
	})
}

func TestDetachIsNotAnExit(t *testing.T) {
	r := newRig(t, remote.LevelView)
	name := Prefix + testShare
	if _, err := r.terms.Attach(name, 80, 24); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first screen", func() bool { return r.seen("hello") })
	if err := r.terms.Detach(name); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if r.seen(":exit") {
		t.Fatal("a detach announced an exit")
	}
	// A write to a detached stream is a quiet no-op, like a local terminal.
	if err := r.terms.Write(name, "x"); err != nil {
		t.Fatalf("write after detach: %v", err)
	}
}

func TestUnsharingEndsTheTerminal(t *testing.T) {
	r := newRig(t, remote.LevelView)
	name := Prefix + testShare
	if _, err := r.terms.Attach(name, 80, 24); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first screen", func() bool { return r.seen("hello") })
	r.gate.mu.Lock()
	r.gate.level = ""
	r.gate.mu.Unlock()
	r.srv.RecheckMembers()
	waitFor(t, "the exit event", func() bool { return r.seen("pty:" + name + ":exit") })
	if _, ok := r.terms.Session(name); ok {
		t.Fatal("an ended stream is still registered")
	}
}

func TestAttachRefusesWhatIsNotAShare(t *testing.T) {
	r := newRig(t, remote.LevelView)
	for _, name := range []string{Prefix, Prefix + "nope", Prefix + "ABCDEFABCDEFABCDEFABCDEFABCDEFAB", "lola-a"} {
		if _, err := r.terms.Attach(name, 80, 24); err == nil {
			t.Errorf("Attach(%q) succeeded", name)
		}
	}
	if IsStream("lola-a") || !IsStream(Prefix+testShare) {
		t.Error("IsStream misclassifies")
	}
}

func TestOwnerSPKIComesOnlyFromTheTrustFile(t *testing.T) {
	home := t.TempDir()
	if _, err := ownerSPKI(home, "d1"); err == nil {
		t.Fatal("an untrusted teammate resolved")
	}
	reg := devices.OpenOwners(home, nil, nil)
	spki := [32]byte{4, 2}
	if _, err := reg.AddMember("ada", spki, devices.HubIdentity{DeviceID: "d1", PublicKey: "k", Fingerprint: "f"}); err != nil {
		t.Fatal(err)
	}
	got, err := ownerSPKI(home, "d1")
	if err != nil || got != spki {
		t.Fatalf("ownerSPKI = %x %v", got, err)
	}
	// The members file (who may attach to US) is not a trust list for theirs.
	home2 := t.TempDir()
	m := devices.OpenMembers(home2, nil, nil)
	if _, err := m.AddMember("ada", spki, devices.HubIdentity{DeviceID: "d1", PublicKey: "k", Fingerprint: "f"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerSPKI(home2, "d1"); err == nil {
		t.Fatal("allowing a teammate to attach to us trusted their sessions")
	}
}
