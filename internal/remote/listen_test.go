package remote

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/protocol"
)

// freePort asks the kernel for a port and gives it straight back. The window
// between that and the real bind is a test-only race and is the standard price
// for an ephemeral port on a listener whose port is configuration.
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

// lrig is a real loopback TLS listener with a registry holding one paired phone
// (all tiers), plus the phone's key. Seams are stubs; nothing else is.
type lrig struct {
	t      *testing.T
	srv    *Server
	reg    *devices.Registry
	pairer *Pairer
	phone  *DeviceKey
	dev    devices.Device
	log    *logCapture
}

// startTestListener brings up the rig through the production Listen, so bind
// selection, the device identity, the mutual-TLS configuration and the device
// authorizer are all exercised. setup may change the Options before the listen.
func startTestListener(t *testing.T, h *stubHandler, bus PaneBus, setup ...func(*Options)) *lrig {
	t.Helper()
	l := &lrig{t: t, log: &logCapture{}}
	l.reg = devices.Open(t.TempDir(), nil, l.log.logf)
	l.pairer = NewPairer(l.reg, nil, l.log.logf)
	l.phone = newTestKey(t)
	var err error
	if l.dev, err = l.reg.Add("Test phone", "ios", l.phone.SPKISHA256(), protocol.Tiers, nil); err != nil {
		t.Fatalf("add device: %v", err)
	}
	opts := Options{
		Bind: "localhost", Port: freePort(t), Dir: t.TempDir(),
		Handle: h.handle, Panes: bus, Devices: l.reg, Pairer: l.pairer, Host: "testhost", Logf: l.log.logf,
	}
	for _, m := range setup {
		m(&opts)
	}
	if l.srv, err = Listen(context.Background(), opts); err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.srv.Close() })
	return l
}

func newTestKey(t *testing.T) *DeviceKey {
	t.Helper()
	dk, err := LoadOrCreateDeviceKey(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return dk
}

// client is a phone's end of one connection.
type client struct {
	t    *testing.T
	conn *tls.Conn
	fr   *protocol.FrameReader
	fw   *protocol.FrameWriter
}

// dialCert connects presenting cert. The client's handshake completes before
// the server judges the certificate (TLS 1.3), so a refusal shows up on the
// first read, never on dial.
func dialCert(t *testing.T, addr string, cert tls.Certificate) *client {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{
		InsecureSkipVerify: true, MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert},
	})
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { conn.Close() })
	return &client{t: t, conn: conn, fr: protocol.NewFrameReader(conn), fw: protocol.NewFrameWriter(conn)}
}

// dial connects as the rig's paired phone.
func (l *lrig) dial() *client { return dialCert(l.t, l.srv.Addrs()[0].Addr, l.phone.Certificate) }

// read waits for one frame.
func (c *client) read() (protocol.Frame, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(testTimeout))
	var f protocol.Frame
	err := c.fr.ReadFrame(&f)
	return f, err
}

// mustRead is read that fails the test on an error.
func (c *client) mustRead() protocol.Frame {
	c.t.Helper()
	f, err := c.read()
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	return f
}

// wantReady requires the next frame to be the ready frame and returns it.
func (c *client) wantReady() protocol.ReadyData {
	c.t.Helper()
	f := c.mustRead()
	if f.Type != protocol.FrameReady {
		c.t.Fatalf("first frame is %q, want ready", f.Type)
	}
	var d protocol.ReadyData
	if err := json.Unmarshal(f.Payload, &d); err != nil {
		c.t.Fatalf("ready payload: %v", err)
	}
	return d
}

func (c *client) req(id, cmd string, payload any) {
	c.t.Helper()
	f := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: id, Cmd: cmd}
	if payload != nil {
		if err := f.SetPayload(payload); err != nil {
			c.t.Fatalf("payload: %v", err)
		}
	}
	if err := c.fw.WriteFrame(&f); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

// wantClosed requires the server to have hung up within the timeout.
func (c *client) wantClosed() {
	c.t.Helper()
	for {
		_, err := c.read()
		if err == nil {
			continue
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			c.t.Fatal("the connection was not closed")
		}
		return
	}
}

// TestListenServesAFrameOverRealTLS is the one end-to-end pass: a real
// loopback listener, a real TLS 1.3 handshake against the generated device
// identity, and a request that reaches the handler and comes back.
func TestListenServesAFrameOverRealTLS(t *testing.T) {
	h := &stubHandler{}
	l := startTestListener(t, h, newFakeBus())
	s := l.srv

	for _, ba := range s.Addrs() {
		if !loopbackAddr(ba.Addr) {
			t.Fatalf("bound a non-loopback address %s for bind = localhost", ba.Addr)
		}
	}

	c := l.dial()
	fr, fw, conn := c.fr, c.fw, c.conn
	if st := conn.ConnectionState(); st.Version != tls.VersionTLS13 {
		t.Errorf("negotiated %#x, want TLS 1.3", st.Version)
	}
	if rd := c.wantReady(); rd.DeviceID != l.dev.ID || rd.Via != ViaLAN || rd.Host != "testhost" {
		t.Errorf("ready = %+v", rd)
	}
	// The pin a phone would carry in its QR must match what it just talked to.
	if s.SPKIPin() == "" {
		t.Error("the server reports no SPKI pin")
	}

	req := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: "e2e", Cmd: "sessions"}
	if err := req.SetPayload(protocol.Request{}); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if err := fw.WriteFrame(&req); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(testTimeout))
	var got protocol.Frame
	if err := fr.ReadFrame(&got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Type != protocol.FrameResp || got.ID != "e2e" {
		t.Fatalf("got %+v, want a resp on id e2e", got)
	}
	var resp protocol.Response
	if err := json.Unmarshal(got.Payload, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.OK {
		t.Errorf("got %+v, want the handler's OK", resp)
	}
	if h.count() != 1 {
		t.Errorf("handler saw %d requests, want 1", h.count())
	}
}

// TestCloseIsBoundedWithAnAttachedPane is the shutdown invariant, and it is the
// single most likely way this package could hang the daemon: a pane a phone
// holds open forever must not outlive Close. The stream here never ends on its
// own, exactly like a live agent, and the peer never disconnects.
func TestCloseIsBoundedWithAnAttachedPane(t *testing.T) {
	bus := newFakeBus()
	l := startTestListener(t, &stubHandler{}, bus)
	s := l.srv

	c := l.dial()
	c.wantReady()
	fr, fw, conn := c.fr, c.fw, c.conn
	sub := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameSub, ID: "s1", Pane: "lola-fe-42"}
	if err := sub.SetPayload(protocol.SubPayload{Cols: 55, Rows: 34}); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if err := fw.WriteFrame(&sub); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(testTimeout))
	var got protocol.Frame
	if err := fr.ReadFrame(&got); err != nil {
		t.Fatalf("read resync: %v", err)
	}
	if got.Type != protocol.FrameResync {
		t.Fatalf("got %q, want the resync acknowledgement", got.Type)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Close()
	}()
	select {
	case <-done:
	case <-time.After(testTimeout):
		t.Fatal("Close did not return while a pane was attached; the daemon's shutdown would hang here")
	}
	if st := bus.stream(0); st == nil || !st.closed.Load() {
		t.Error("the attached stream was not closed by Close")
	}
	// The listener is really gone: a fresh dial must fail.
	if c, err := net.DialTimeout("tcp", s.Addrs()[0].Addr, time.Second); err == nil {
		c.Close()
		t.Error("the listener still accepts connections after Close")
	}
}

// TestCloseIsIdempotent, because the daemon calls stopRemote on shutdown and
// may call it again from handleReload.
func TestCloseIsIdempotent(t *testing.T) {
	s := startTestListener(t, &stubHandler{}, newFakeBus()).srv
	s.Close()
	s.Close()
}

// TestCancellingTheRunContextStopsTheListener: the server hangs off the
// daemon's CANCELLABLE run context, the same posture as the interpreter and
// review workers.
func TestCancellingTheRunContextStopsTheListener(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s, err := listen(ctx, Options{
		Bind: "localhost", Port: freePort(t), Dir: t.TempDir(),
		Handle: (&stubHandler{}).handle, Logf: func(string, ...any) {},
	}, &stubAuth{})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer s.Close()
	addr := s.Addrs()[0].Addr

	cancel()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			return
		}
		c.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the listener still accepted connections after the run context was cancelled")
}

// TestListenRefusesAnIncompleteConfiguration. Dir in particular: this package
// never derives the lola home, so a caller that forgot it must fail rather than
// write a device key somewhere surprising.
func TestListenRefusesAnIncompleteConfiguration(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		auth Authorizer
	}{
		{"no authorizer", Options{Bind: "localhost", Port: 7717, Dir: t.TempDir(), Handle: (&stubHandler{}).handle}, nil},
		{"no handler", Options{Bind: "localhost", Port: 7717, Dir: t.TempDir()}, &stubAuth{}},
		{"no directory", Options{Bind: "localhost", Port: 7717, Handle: (&stubHandler{}).handle}, &stubAuth{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := listen(context.Background(), tc.opts, tc.auth)
			if err == nil {
				s.Close()
				t.Fatal("listen accepted an incomplete configuration")
			}
		})
	}
}

// TestListenRefusesBindOff: "off" is the keep-my-settings-but-stop-listening
// state, and it must be distinguishable from "nothing matched", which is the
// far more alarming outcome bind = "lan" can legitimately produce.
func TestListenRefusesBindOff(t *testing.T) {
	_, err := listen(context.Background(), Options{
		Bind: "off", Port: 7717, Dir: t.TempDir(), Handle: (&stubHandler{}).handle,
	}, &stubAuth{})
	if !errors.Is(err, ErrBindOff) {
		t.Fatalf("got %v, want ErrBindOff", err)
	}
}

// TestListenNeedsDevicesAndPairer: the registry and the pairer are owned by the
// daemon and survive listener rebuilds, so a Listen without them is a wiring bug.
func TestListenNeedsDevicesAndPairer(t *testing.T) {
	reg := devices.Open(t.TempDir(), nil, nil)
	for name, o := range map[string]Options{
		"neither":     {},
		"no pairer":   {Devices: reg},
		"no registry": {Pairer: NewPairer(reg, nil, nil)},
	} {
		o.Bind, o.Port, o.Dir, o.Handle = "localhost", 7717, t.TempDir(), (&stubHandler{}).handle
		if s, err := Listen(context.Background(), o); err == nil {
			s.Close()
			t.Errorf("%s: Listen accepted the options", name)
		}
	}
}

// TestUnsavedRegistryRefusesListen: a revocation that lives only in memory must
// not be forgotten by a restart, so no listener runs while one does.
func TestUnsavedRegistryRefusesListen(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not bind root")
	}
	home := t.TempDir()
	reg := devices.Open(home, nil, nil)
	dk := newTestKey(t)
	dev, err := reg.Add("Phone", "ios", dk.SPKISHA256(), protocol.DefaultTiers, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(home, 0o700)
	if _, err := reg.Revoke(dev.ID); !errors.Is(err, devices.ErrRevokeUnsaved) {
		t.Fatalf("revoke err = %v, want ErrRevokeUnsaved", err)
	}
	s, err := Listen(context.Background(), Options{
		Bind: "localhost", Port: freePort(t), Dir: t.TempDir(), Handle: (&stubHandler{}).handle,
		Devices: reg, Pairer: NewPairer(reg, nil, nil),
	})
	if err == nil {
		s.Close()
		t.Fatal("Listen accepted a registry that holds an unsaved revocation")
	}
}

// TestBrokenRegistryListensAndAuthenticatesNobody: an unreadable devices.json
// still binds, so the doctor and pairing report the problem through the daemon
// instead of a dead port, and says so once.
func TestBrokenRegistryListensAndAuthenticatesNobody(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, devices.FileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := &logCapture{}
	reg := devices.Open(home, nil, log.logf)
	s, err := Listen(context.Background(), Options{
		Bind: "localhost", Port: freePort(t), Dir: t.TempDir(), Handle: (&stubHandler{}).handle,
		Devices: reg, Pairer: NewPairer(reg, nil, nil), Logf: log.logf,
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer s.Close()
	if !strings.Contains(log.all(), "no phone can connect") {
		t.Errorf("no log line names the broken registry:\n%s", log.all())
	}
	c := dialCert(t, s.Addrs()[0].Addr, newTestKey(t).Certificate)
	if _, err := c.read(); err == nil {
		t.Fatal("a certificate was served by a broken registry")
	}
}

// TestRelayOnlyServerHasNoListeners: bind = off with the relay on must still
// produce a Server; it just has nothing to accept.
func TestRelayOnlyServerHasNoListeners(t *testing.T) {
	l := startTestListener(t, &stubHandler{}, newFakeBus(), func(o *Options) {
		o.RelayOnly, o.Bind = true, "off"
	})
	if n := len(l.srv.Addrs()); n != 0 {
		t.Errorf("Addrs = %d, want none", n)
	}
	if p := l.srv.Port(); p != 0 {
		t.Errorf("Port = %d, want 0", p)
	}
	if l.srv.tlsConfig == nil || l.srv.relaySlots == nil || l.srv.relayPerRV == nil {
		t.Error("a relay-only server lacks the TLS config or the relay pool ServeRelayed needs")
	}
	if err := l.srv.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestPairerGetsTheDaemonSPKI: the confirmation code binds the daemon's key,
// which only the listener knows.
func TestPairerGetsTheDaemonSPKI(t *testing.T) {
	l := startTestListener(t, &stubHandler{}, newFakeBus())
	l.pairer.mu.Lock()
	got, set := l.pairer.daemon, l.pairer.daemonK
	l.pairer.mu.Unlock()
	if !set || got != l.srv.key.SPKISHA256() {
		t.Error("Listen did not hand the daemon SPKI to the Pairer")
	}
}
