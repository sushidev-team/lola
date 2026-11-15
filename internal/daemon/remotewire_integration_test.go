package daemon

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/panebus"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// pairedPhone is a phone the registry already knows: a generated P-256
// certificate whose SPKI hash was registered with Registry.Add, which is all a
// pairing leaves behind. The tests that are not about pairing use it instead of
// running one.
type pairedPhone struct {
	cert tls.Certificate
	dev  devices.Device
}

func addPairedPhone(t *testing.T, d *Daemon, tiers []string) pairedPhone {
	t.Helper()
	if tiers == nil {
		tiers = protocol.DefaultTiers
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "phone"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, priv.Public(), priv)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	dev, err := d.devices.Add("Test phone", "ios", devices.HashSPKI(leaf.RawSubjectPublicKeyInfo), tiers, nil)
	if err != nil {
		t.Fatalf("Registry.Add: %v", err)
	}
	return pairedPhone{cert: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, dev: dev}
}

// TestRemoteFrameReachesTheDaemonAndASubscribeReachesThePaneBus is the
// architecture test: one real mutual-TLS connection from a registered phone
// carrying the real frame codec into the real dispatcher and the real pane
// registry, with only tmux faked.
//
// It proves the joins nothing else covers, because each lives in a different
// package and none of those packages may import another:
//
//  1. The registry the daemon owns is the one the listener authenticates
//     against: a phone added with Registry.Add is greeted with a ready frame.
//  2. A req frame decoded by internal/remote reaches internal/daemon's own
//     d.handle through handleRemote and answers out of the daemon's own state.
//  3. A sub frame reaches internal/panebus through the remotePanes adapter, and
//     the resync that comes back carries the geometry the bus measured, not the
//     phone's viewport.
//  4. Pane bytes cross the adapter and arrive as pty frames, and a pty write
//     crosses back into the pane.
//  5. The identity gate is the daemon's session store: a pane in no session's
//     namespace is refused BEFORE any attach, so a refusal costs no process.
func TestRemoteFrameReachesTheDaemonAndASubscribeReachesThePaneBus(t *testing.T) {
	d := newRemoteTestDaemon(t)
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: freePort(t)}

	fake := panebus.NewFake()
	d.paneRegistry = func() *panebus.Registry { return fake.Registry() }

	// One session in the store. Its tmux name is the pane name a phone may
	// subscribe to; everything else is refused by resolvePaneName.
	const pane = "lola-web-eng-42"
	d.sessions.Upsert(session.Session{
		ID: pane, TmuxName: pane, Project: "web", Issue: "ENG-42", Status: "working",
	})
	phone := addPairedPhone(t, d, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.startRemote(ctx)
	t.Cleanup(d.stopRemote)

	if d.remote == nil {
		t.Fatalf("the listener did not start: %s", d.remoteErr)
	}
	addrs := d.remote.Addrs()
	if len(addrs) == 0 {
		t.Fatal("the listener bound no address")
	}

	c := dialRemote(t, addrs[0].Addr, phone.cert)

	// (1) The first frame is ready, naming the device the registry holds.
	t.Run("the first frame is ready for the registered device", func(t *testing.T) {
		f := c.await(t, func(f *protocol.Frame) bool { return f.Type == protocol.FrameReady })
		var rd protocol.ReadyData
		if err := f.DecodePayload(&rd); err != nil {
			t.Fatalf("decode ready: %v", err)
		}
		if rd.DeviceID != phone.dev.ID || rd.Via != "lan" {
			t.Errorf("ready = %+v, want device %s via lan", rd, phone.dev.ID)
		}
	})

	// (2) A req frame reaches d.handle and answers from the daemon's own store.
	t.Run("a request frame reaches the daemon's dispatcher", func(t *testing.T) {
		c.send(t, protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: "r1", Cmd: "sessions"},
			protocol.Request{Cmd: "sessions"})
		f := c.await(t, func(f *protocol.Frame) bool { return f.Type == protocol.FrameResp && f.ID == "r1" })

		var resp protocol.Response
		if err := f.DecodePayload(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !resp.OK {
			t.Fatalf("sessions failed: %s", resp.Error)
		}
		var data protocol.SessionsData
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			t.Fatalf("decode sessions data: %v", err)
		}
		if len(data.Sessions) != 1 || data.Sessions[0].ID != pane {
			t.Fatalf("sessions = %+v, want the one record this daemon holds — the reply must come from d.handle, not a stub", data.Sessions)
		}
		if data.Sessions[0].Issue != "ENG-42" {
			t.Errorf("issue = %q, want ENG-42", data.Sessions[0].Issue)
		}
	})

	// (5) The identity gate runs BEFORE anything is spawned. Asserted first, so
	// the attach count below is unambiguous.
	t.Run("a pane in no session's namespace is refused before any attach", func(t *testing.T) {
		before := fake.CallCount()
		c.send(t, protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameSub, ID: "s0", Pane: "lola-web-eng-99"},
			protocol.SubPayload{Cols: 55, Rows: 34})
		f := c.await(t, func(f *protocol.Frame) bool { return f.Type == protocol.FrameErr && f.ID == "s0" })

		var e protocol.ErrPayload
		if err := f.DecodePayload(&e); err != nil {
			t.Fatalf("decode err payload: %v", err)
		}
		if e.Code != protocol.CodeUnknownPane {
			t.Errorf("code = %q, want %q", e.Code, protocol.CodeUnknownPane)
		}
		if n := fake.Count("attach"); n != 0 {
			t.Errorf("attach ran %d times for an unresolvable pane; the gate must refuse before any process", n)
		}
		if fake.CallCount() != before {
			t.Errorf("seam calls went %d -> %d for a refused pane; a refusal must cost no exec at all",
				before, fake.CallCount())
		}
	})

	// (3) A sub frame reaches the bus, and the resync reports the WINDOW.
	t.Run("a subscribe reaches the pane bus and is acknowledged by a resync", func(t *testing.T) {
		c.send(t, protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameSub, ID: "s1", Pane: pane},
			protocol.SubPayload{Cols: 55, Rows: 34})
		f := c.await(t, func(f *protocol.Frame) bool { return f.Type == protocol.FrameResync && f.ID == "s1" })

		var r protocol.ResyncPayload
		if err := f.DecodePayload(&r); err != nil {
			t.Fatalf("decode resync: %v", err)
		}
		// The fake reports a 200x50 window with one status row, so the PTY —
		// and therefore the emulator the resync is rendered from — is 200x51.
		// The phone asked for 55x34 and is told the truth instead: the desktop's
		// window is never reflowed for a subscriber, the phone pans.
		if r.Cols != 200 || r.Rows != 51 {
			t.Errorf("resync = %dx%d, want 200x51 (the tmux window plus its status row), never the phone's 55x34", r.Cols, r.Rows)
		}
		if f.Pane != pane {
			t.Errorf("resync names pane %q, want %q", f.Pane, pane)
		}
		if n := fake.Count("attach"); n != 1 {
			t.Errorf("attach ran %d times, want exactly 1", n)
		}
	})

	// (4) Bytes cross the adapter in both directions.
	t.Run("pane output arrives as pty frames and a write reaches the pane", func(t *testing.T) {
		p := fake.Pane(pane)
		if p == nil {
			t.Fatal("nothing attached the pane")
		}
		p.Emit([]byte("\x1b[32mready\x1b[0m"))
		f := c.await(t, func(f *protocol.Frame) bool { return f.Type == protocol.FramePTY && f.Pane == pane })

		var out protocol.PTYOutputPayload
		if err := f.DecodePayload(&out); err != nil {
			t.Fatalf("decode pty output: %v", err)
		}
		if string(out.Data) != "\x1b[32mready\x1b[0m" {
			t.Errorf("pty data = %q, want the pane's bytes verbatim", out.Data)
		}
		if f.Seq == 0 {
			t.Error("a pty frame must carry the bus's per-pane sequence; 0 means a gap can never be seen")
		}

		c.send(t, protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FramePTY, ID: "w1", Pane: pane},
			protocol.PTYInputPayload{Action: protocol.PTYActionWrite, Data: []byte("y\r")})
		deadline := time.Now().Add(3 * time.Second)
		for !strings.Contains(string(p.Written()), "y\r") {
			if time.Now().After(deadline) {
				t.Fatalf("the pane never received the write; got %q", p.Written())
			}
			time.Sleep(5 * time.Millisecond)
		}
	})

	// Teardown is bounded and takes the registry with it. It is asserted here
	// rather than left to t.Cleanup because "Close returns" is the property the
	// whole shutdown ordering rests on: stopRemote runs before the unbounded
	// drainConnWork, with a client still attached and a pane still streaming.
	t.Run("stopRemote returns with a client still attached", func(t *testing.T) {
		done := make(chan struct{})
		go func() { defer close(done); d.stopRemote() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("stopRemote blocked with a phone attached; shutdown would hang at drainConnWork")
		}
		if d.remote != nil || d.panes != nil {
			t.Error("stopRemote must clear both handles")
		}
		ln, err := net.Listen("tcp", addrs[0].Addr)
		if err != nil {
			t.Fatalf("the listener's port is still bound after stopRemote: %v", err)
		}
		ln.Close()
	})
}

// TestUntaggedBuildListens replaces the old "an untagged build binds nothing"
// pin: every binary carries the one listener, so a config that asks for it gets
// a bound socket with no build tag and no environment key.
func TestUntaggedBuildListens(t *testing.T) {
	d := newRemoteTestDaemon(t)
	port := freePort(t)
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: port}
	d.paneRegistry = func() *panebus.Registry { return panebus.NewFake().Registry() }

	d.startRemote(context.Background())
	t.Cleanup(d.stopRemote)

	if d.remote == nil {
		t.Fatalf("the listener did not start: %s", d.remoteErr)
	}
	if d.remoteErr != "" {
		t.Errorf("remoteErr = %q after a successful start", d.remoteErr)
	}
	if !d.remoteServes.Load() {
		t.Error("remoteServes must record that a server should exist")
	}
	if _, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err == nil {
		t.Fatalf("port %d is free, so nothing is listening on it", port)
	}
}

// TestRemoteReloadRebindsOntoTheNewPort covers handleReload's half of the
// wiring: a changed [remote] table tears the listener down and brings a new one
// up on the new address, and the old port is genuinely released rather than
// held by a listener nobody can reach any more.
func TestRemoteReloadRebindsOntoTheNewPort(t *testing.T) {
	d := newRemoteTestDaemon(t)
	d.shutdownCtx = context.Background()
	first := freePort(t)
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: first}
	d.paneRegistry = func() *panebus.Registry { return panebus.NewFake().Registry() }

	d.startRemote(context.Background())
	t.Cleanup(d.stopRemote)
	if d.remote == nil {
		t.Fatal("the first listener did not start")
	}
	firstAddr := d.remote.Addrs()[0].Addr

	second := freePort(t)
	d.mu.Lock()
	d.cfg.Remote.Port = second
	d.mu.Unlock()
	d.reloadRemote()

	if d.remote == nil {
		t.Fatal("the reload left no listener at all")
	}
	if got := d.remote.Addrs()[0].Addr; !strings.HasSuffix(got, ":"+strconv.Itoa(second)) {
		t.Errorf("listening on %s, want port %d", got, second)
	}
	// The old socket is gone, not merely unreferenced.
	ln, err := net.Listen("tcp", firstAddr)
	if err != nil {
		t.Fatalf("the old port %s is still bound after a rebind: %v", firstAddr, err)
	}
	ln.Close()
}

// TestRegistrySurvivesListenerReload pins who owns the device registry and the
// pairing window: the daemon, not the listener. A rebind must keep paired
// devices (the same *Registry, the same entries) and an open pairing window.
func TestRegistrySurvivesListenerReload(t *testing.T) {
	d := newRemoteTestDaemon(t)
	d.shutdownCtx = context.Background()
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: freePort(t)}
	d.paneRegistry = func() *panebus.Registry { return panebus.NewFake().Registry() }

	phone := addPairedPhone(t, d, nil)
	reg, pairer := d.devices, d.pairer

	d.startRemote(context.Background())
	t.Cleanup(d.stopRemote)
	if d.remote == nil {
		t.Fatalf("no listener: %s", d.remoteErr)
	}
	ticket, err := d.pairer.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	d.reloadRemote()
	if d.remote == nil {
		t.Fatalf("no listener after the reload: %s", d.remoteErr)
	}
	if d.devices != reg || d.pairer != pairer {
		t.Fatal("a reload replaced the registry or the pairer")
	}
	if _, ok := d.devices.Get(phone.dev.ID); !ok {
		t.Error("the paired device is gone after a listener reload")
	}
	st, err := d.pairer.Status(context.Background(), protocol.PairStatusArgs{Pairing: ticket.ID})
	if err != nil || st.State != protocol.PairStateWaiting {
		t.Errorf("pairing after the reload = %+v, %v; want it still waiting", st, err)
	}
}

// remoteClient is a minimal phone: one mutual-TLS connection speaking the real
// codec.
type remoteClient struct {
	fr   *protocol.FrameReader
	fw   *protocol.FrameWriter
	conn net.Conn
}

func dialRemote(t *testing.T, addr string, cert tls.Certificate) *remoteClient {
	t.Helper()
	// A real phone pins the daemon's SPKI; pinning is internal/remote's own
	// test, so this one skips verification and presents its own certificate.
	conn, err := tls.Dial("tcp", addr, &tls.Config{
		InsecureSkipVerify: true, MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert},
	})
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { conn.Close() })
	return &remoteClient{fr: protocol.NewFrameReader(conn), fw: protocol.NewFrameWriter(conn), conn: conn}
}

func (c *remoteClient) send(t *testing.T, f protocol.Frame, payload any) {
	t.Helper()
	if payload != nil {
		if err := f.SetPayload(payload); err != nil {
			t.Fatalf("encode payload: %v", err)
		}
	}
	if err := c.fw.WriteFrame(&f); err != nil {
		t.Fatalf("write %s frame: %v", f.Type, err)
	}
}

// await reads until a frame matches, or fails. The connection multiplexes —
// resyncs and pane output interleave with replies — so a test can never assume
// the next frame is the one it asked for; that interleaving is the whole point
// of the correlation id.
func (c *remoteClient) await(t *testing.T, match func(*protocol.Frame) bool) protocol.Frame {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer func() { _ = c.conn.SetReadDeadline(time.Time{}) }()
	var seen []string
	for {
		var f protocol.Frame
		if err := c.fr.ReadFrame(&f); err != nil {
			t.Fatalf("read frame: %v (frames seen first: %s)", err, strings.Join(seen, ", "))
		}
		if match(&f) {
			return f
		}
		seen = append(seen, f.Type+"/"+f.ID)
		if len(seen) > 64 {
			t.Fatalf("no matching frame in 64 frames: %s", strings.Join(seen, ", "))
		}
	}
}

// TestRemoteRefusesToBindOnceTheRunContextIsCancelled closes the one race the
// reload path opens. handleReload runs on a socket goroutine that belongs to no
// drain group, so a reload arriving while the daemon is shutting down could
// otherwise bind a listener AFTER stopRemote had already run — and nothing
// would ever wait for it or take it down.
func TestRemoteRefusesToBindOnceTheRunContextIsCancelled(t *testing.T) {
	d := newRemoteTestDaemon(t)
	port := freePort(t)
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: port}
	d.paneRegistry = func() *panebus.Registry { return panebus.NewFake().Registry() }

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.startRemote(ctx)
	t.Cleanup(d.stopRemote)

	if d.remote != nil {
		t.Fatal("a cancelled run context must bind nothing")
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("port %d is bound, so a listener started during shutdown: %v", port, err)
	}
	ln.Close()
}
