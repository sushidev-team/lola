package shareclient

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
)

const testRV = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type fakeStream struct {
	ch   chan remote.PaneFrame
	once sync.Once
}

func (s *fakeStream) Frames() <-chan remote.PaneFrame { return s.ch }
func (s *fakeStream) Close() error                    { s.once.Do(func() { close(s.ch) }); return nil }

// fakeBus hands out one controllable stream per subscribe.
type fakeBus struct {
	mu      sync.Mutex
	streams []*fakeStream
	subs    int
	writes  []string
}

func (b *fakeBus) Subscribe(_ context.Context, pane string, _, _ int) (remote.PaneStream, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := &fakeStream{ch: make(chan remote.PaneFrame, 16)}
	b.subs++
	b.streams = append(b.streams, s)
	s.ch <- remote.PaneFrame{Kind: remote.PaneResync, Seq: 1, Screen: &remote.PaneScreen{
		Cols: 80, Rows: 24, Lines: []string{"\x1b[32mhello\x1b[0m"}, CursorX: 5, CursorY: 0, CursorVisible: true}}
	return s, nil
}

func (b *fakeBus) Write(_ string, p []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writes = append(b.writes, string(p))
	return nil
}

func (b *fakeBus) Scroll(context.Context, string, int) error { return nil }

func (b *fakeBus) stream(i int) *fakeStream {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.streams[i]
}

func (b *fakeBus) subCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.subs
}

type gate struct {
	mu    sync.Mutex
	level map[string]string
}

func (g *gate) KnownRV(rv string) bool { return rv == testRV }
func (g *gate) PaneAccess(_, pane string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.level[pane]
}
func (g *gate) Roster(member string, att []remote.Attachment) (protocol.ShareSessionsData, error) {
	d := protocol.ShareSessionsData{Sessions: []protocol.SharedSession{}}
	for pane, lvl := range g.level {
		ss := protocol.SharedSession{ID: "s1", Title: "api work", Level: lvl, Pane: pane}
		for _, a := range att {
			if a.Pane == pane {
				ss.Attached = append(ss.Attached, protocol.ShareViewer{Name: a.Name, Me: a.MemberID == member})
			}
		}
		d.Sessions = append(d.Sessions, ss)
	}
	return d, nil
}

type rig struct {
	t      *testing.T
	srv    *remote.Server
	bus    *fakeBus
	gate   *gate
	host   *remote.DeviceKey
	member *remote.DeviceKey
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, bus: &fakeBus{}, gate: &gate{level: map[string]string{"lola-a": remote.LevelType, "lola-v": remote.LevelView}}}
	hostDir := t.TempDir()
	var err error
	if r.member, err = remote.LoadOrCreateDeviceKey(t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	phones := devices.Open(t.TempDir(), nil, nil)
	members := devices.OpenMembers(t.TempDir(), nil, nil)
	if _, err := members.AddMember("ada", r.member.SPKISHA256(), devices.HubIdentity{DeviceID: "d", PublicKey: "k", Fingerprint: "f"}); err != nil {
		t.Fatal(err)
	}
	r.srv, err = remote.Listen(context.Background(), remote.Options{
		Dir: hostDir, RelayOnly: true, Handle: func(context.Context, protocol.Request) protocol.Response { return protocol.Response{} },
		Panes: r.bus, Devices: phones, Pairer: remote.NewPairer(phones, nil, nil), Host: "office-mini",
		Members: members, MemberGate: r.gate,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.srv.Close() })
	r.host, err = remote.LoadOrCreateDeviceKey(hostDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// dial hands the server one end of a pipe, as the relay does, and returns the client.
func (r *rig) dial(pin [32]byte, rv string) (*Conn, error) {
	slot, ok := r.srv.ReserveRelay(rv)
	if !ok {
		r.t.Fatal("no relay slot")
	}
	cp, sp := tcpPair(r.t)
	go func() { _ = r.srv.ServeRelayed(sp, remote.RelayMeta{RV: rv}, slot) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, cp, Config{Cert: r.member.Certificate, HostSPKI: pin})
	if err != nil {
		_ = cp.Close()
		return nil, err
	}
	r.t.Cleanup(func() { c.Close() })
	return c, nil
}

func next(t *testing.T, c *Conn) Event {
	t.Helper()
	select {
	case ev, ok := <-c.Events():
		if !ok {
			t.Fatalf("events closed: %v", c.Err())
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}
	return Event{}
}

func TestAttachEndToEnd(t *testing.T) {
	r := newRig(t)
	c, err := r.dial(r.host.SPKISHA256(), testRV)
	if err != nil {
		t.Fatal(err)
	}
	if c.Ready.Via != remote.ViaRelay || len(c.Ready.Tiers) != 0 || c.Ready.Host != "office-mini" {
		t.Fatalf("ready = %+v", c.Ready)
	}
	roster, err := c.Roster(context.Background())
	if err != nil || len(roster.Sessions) != 2 {
		t.Fatalf("roster = %+v %v", roster, err)
	}
	if err := c.Subscribe("lola-a", 100, 30); err != nil {
		t.Fatal(err)
	}
	ev := next(t, c)
	if ev.Kind != Resync || ev.Pane != "lola-a" || ev.Screen.Lines[0] != "\x1b[32mhello\x1b[0m" {
		t.Fatalf("first event = %+v", ev)
	}
	// Output arrives in order after the resync.
	st := r.bus.stream(0)
	st.ch <- remote.PaneFrame{Kind: remote.PaneOutput, Seq: 2, Data: []byte("one")}
	st.ch <- remote.PaneFrame{Kind: remote.PaneOutput, Seq: 3, Data: []byte("two")}
	if a, b := next(t, c), next(t, c); a.Kind != Output || string(a.Data) != "one" || string(b.Data) != "two" {
		t.Fatalf("output = %+v %+v", a, b)
	}
	// The roster now shows this viewer attached, by the name the owner pinned.
	roster, _ = c.Roster(context.Background())
	var seen bool
	for _, s := range roster.Sessions {
		for _, v := range s.Attached {
			seen = seen || (v.Me && v.Name == "ada")
		}
	}
	if !seen {
		t.Fatalf("roster lacks this viewer: %+v", roster)
	}
	// type level: keystrokes reach the owner's pane.
	if err := c.Write("lola-a", []byte("ls\n")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		r.bus.mu.Lock()
		defer r.bus.mu.Unlock()
		return len(r.bus.writes) == 1 && r.bus.writes[0] == "ls\n"
	})
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("condition not reached")
}

func TestViewLevelRefusesKeystrokes(t *testing.T) {
	r := newRig(t)
	c, err := r.dial(r.host.SPKISHA256(), testRV)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Subscribe("lola-v", 80, 24)
	_ = next(t, c)
	_ = c.Write("lola-v", []byte("rm -rf ~\n"))
	ev := next(t, c)
	if ev.Kind != Refused || ev.Code != protocol.CodeDenied {
		t.Fatalf("event = %+v, want a denied refusal", ev)
	}
	r.bus.mu.Lock()
	defer r.bus.mu.Unlock()
	if len(r.bus.writes) != 0 {
		t.Fatalf("view-level keystrokes reached the pane: %v", r.bus.writes)
	}
}

func TestAnotherKeyIsNotTheHost(t *testing.T) {
	r := newRig(t)
	other, err := remote.LoadOrCreateDeviceKey(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.dial(other.SPKISHA256(), testRV); !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("err = %v, want a pin mismatch", err)
	}
}

func TestHostRefusalIsNotReady(t *testing.T) {
	r := newRig(t)
	// A rendezvous that is not a published share's: the host answers not_paired.
	_, err := r.dial(r.host.SPKISHA256(), "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("err = %v, want not ready", err)
	}
}

func TestGapResubscribes(t *testing.T) {
	r := newRig(t)
	c, err := r.dial(r.host.SPKISHA256(), testRV)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Subscribe("lola-a", 80, 24)
	_ = next(t, c)
	st := r.bus.stream(0)
	st.ch <- remote.PaneFrame{Kind: remote.PaneOutput, Seq: 2, Data: []byte("ok")}
	if ev := next(t, c); string(ev.Data) != "ok" {
		t.Fatalf("event = %+v", ev)
	}
	// Seq 4 follows 2: output was dropped. The corrupted tail is never shown;
	// the client re-subscribes and the next thing it sees is a fresh resync.
	st.ch <- remote.PaneFrame{Kind: remote.PaneOutput, Seq: 4, Data: []byte("lost-context")}
	waitFor(t, func() bool { return r.bus.subCount() == 2 })
	ev := next(t, c)
	if ev.Kind != Resync {
		t.Fatalf("after a gap the client drew %+v, want a resync", ev)
	}
}

func TestUnshareEndsTheStream(t *testing.T) {
	r := newRig(t)
	c, err := r.dial(r.host.SPKISHA256(), testRV)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Subscribe("lola-a", 80, 24)
	_ = next(t, c)
	r.gate.mu.Lock()
	delete(r.gate.level, "lola-a")
	r.gate.mu.Unlock()
	r.srv.RecheckMembers()
	ev := next(t, c)
	if ev.Kind != Refused || ev.Code != protocol.CodeUnknownPane || ev.Pane != "lola-a" {
		t.Fatalf("event = %+v, want the pane to be gone", ev)
	}
}

func TestRenderPaintsTheScreen(t *testing.T) {
	out := string(Render(protocol.ResyncPayload{Cols: 80, Rows: 24, Lines: []string{"a", "\x1b[31mb"}, CursorX: 3, CursorY: 1}))
	for _, want := range []string{"\x1b[2J", "a\x1b[0m\r\n\x1b[31mb\x1b[0m", "\x1b[2;4H", "\x1b[?25h"} {
		if !strings.Contains(out, want) {
			t.Errorf("render lacks %q: %q", want, out)
		}
	}
	hidden := string(Render(protocol.ResyncPayload{Lines: []string{"x"}, CursorHidden: true}))
	if strings.Contains(hidden, "\x1b[?25h") {
		t.Error("a hidden cursor was shown")
	}
}

// tcpPair is a connected loopback pair. A pipe has no buffer, so a handshake
// that ends in an alert would block both sides writing; the relay is a stream
// with buffers, like this.
func tcpPair(t *testing.T) (client, server net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	acc := make(chan net.Conn, 1)
	go func() { c, _ := ln.Accept(); acc <- c }()
	client, err = net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client, <-acc
}
