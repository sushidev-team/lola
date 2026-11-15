package remote

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/protocol"
)

const (
	testRV  = "0123456789abcdef0123456789abcdef"
	otherRV = "fedcba9876543210fedcba9876543210"
)

// closeNowConn is the stream stand-in for *hubproto.WSConn: it records which
// way it was closed.
type closeNowConn struct {
	net.Conn
	closeNow atomic.Int32
	plain    atomic.Int32
}

func (c *closeNowConn) CloseNow(code uint16) error {
	c.closeNow.Store(int32(code))
	return c.Conn.Close()
}

func (c *closeNowConn) Close() error {
	c.plain.Add(1)
	return c.Conn.Close()
}

// relayRig is a relay-only server (bind = "off") holding one phone whose relay
// rendezvous is testRV.
type relayRig struct {
	*lrig
}

func newRelayRig(t *testing.T) *relayRig {
	t.Helper()
	l := startTestListener(t, &stubHandler{}, newFakeBus(), func(o *Options) { o.RelayOnly = true })
	var err error
	l.dev, err = l.reg.SetRelay(l.dev.ID,
		&devices.DeviceRelay{RV: testRV, BearerHash: strings.Repeat("a", 64), RotatedAt: time.Now()})
	if err != nil {
		t.Fatalf("set relay: %v", err)
	}
	return &relayRig{l}
}

// serve hands one end of a pipe to ServeRelayed and returns the phone's end.
func (r *relayRig) serve(rv string) (net.Conn, error) {
	slot, ok := r.srv.ReserveRelay(rv)
	if !ok {
		r.t.Fatalf("ReserveRelay(%s) refused", rv)
	}
	cp, sp := net.Pipe()
	r.t.Cleanup(func() { cp.Close() })
	return cp, r.srv.ServeRelayed(sp, RelayMeta{RV: rv}, slot)
}

func (r *relayRig) client(cp net.Conn) *client {
	tc := tls.Client(cp, &tls.Config{
		InsecureSkipVerify: true, MinVersion: tls.VersionTLS13,
		Certificates: []tls.Certificate{r.phone.Certificate},
	})
	return &client{t: r.t, conn: tc, fr: protocol.NewFrameReader(tc), fw: protocol.NewFrameWriter(tc)}
}

func (r *relayRig) request(c *client, id string) {
	r.t.Helper()
	req := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: id, Cmd: "sessions"}
	if err := req.SetPayload(protocol.Request{}); err != nil {
		r.t.Fatalf("payload: %v", err)
	}
	if err := c.fw.WriteFrame(&req); err != nil {
		r.t.Fatalf("write: %v", err)
	}
	if got := c.mustRead(); got.Type != protocol.FrameResp || got.ID != id {
		r.t.Fatalf("reply = %s/%s, want resp/%s", got.Type, got.ID, id)
	}
}

func TestServeRelayedUsesListenerTLSConfig(t *testing.T) {
	r := newRelayRig(t)
	cp, err := r.serve(testRV)
	if err != nil {
		t.Fatalf("ServeRelayed: %v", err)
	}
	c := r.client(cp)
	_ = c.conn.SetDeadline(time.Now().Add(testTimeout))
	if err := c.conn.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	st := c.conn.ConnectionState()
	if st.Version != tls.VersionTLS13 {
		t.Errorf("negotiated %#x, want TLS 1.3", st.Version)
	}
	cert, err := x509.ParseCertificate(st.PeerCertificates[0].Raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if devices.HashSPKI(cert.RawSubjectPublicKeyInfo) != devices.HashSPKI(r.srv.key.Certificate.Leaf.RawSubjectPublicKeyInfo) {
		t.Error("the relayed handshake did not present the daemon's device key")
	}
	if r.srv.tlsConfig == nil {
		t.Fatal("no shared TLS config")
	}
}

func TestServeRelayedEndToEnd(t *testing.T) {
	r := newRelayRig(t)
	cp, err := r.serve(testRV)
	if err != nil {
		t.Fatalf("ServeRelayed: %v", err)
	}
	c := r.client(cp)
	rd := c.wantReady()
	if rd.Via != ViaRelay || rd.DeviceID != r.dev.ID {
		t.Fatalf("ready = %+v, want via=relay device=%s", rd, r.dev.ID)
	}
	r.request(c, "e2e")
	if n := r.srv.DeviceConns(r.dev.ID); n != 1 {
		t.Errorf("DeviceConns = %d, want 1", n)
	}
	// Audit lines read the peer's path; read-tier requests are not audited, so
	// the conn's peer is the witness.
	r.srv.mu.Lock()
	for cn := range r.srv.conns {
		if cn.peer.Via != ViaRelay {
			t.Errorf("peer.Via = %q, want %q", cn.peer.Via, ViaRelay)
		}
	}
	r.srv.mu.Unlock()
	// The pre-auth slot went back when the connection authenticated.
	r.srv.mu.Lock()
	held := len(r.srv.relaySlots)
	r.srv.mu.Unlock()
	if held != 0 {
		t.Errorf("relay slots held after authentication = %d, want 0", held)
	}
}

func TestRelayedConnSurvivesIdlePings(t *testing.T) {
	r := newRelayRig(t)
	cp, err := r.serve(testRV)
	if err != nil {
		t.Fatalf("ServeRelayed: %v", err)
	}
	c := r.client(cp)
	c.wantReady()
	// No read deadline runs on an authenticated connection: silence is not a
	// reason to end it.
	time.Sleep(400 * time.Millisecond)
	r.request(c, "after-idle")
}

func TestServeRelayedRacesClose(t *testing.T) {
	for i := 0; i < 20; i++ {
		r := newRelayRig(t)
		var wg sync.WaitGroup
		results := make(chan error, 4)
		for j := 0; j < 4; j++ {
			slot, ok := r.srv.ReserveRelay(testRV + string(rune('a'+j)))
			if !ok {
				t.Fatal("reserve refused")
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				cp, sp := net.Pipe()
				defer cp.Close()
				results <- r.srv.ServeRelayed(sp, RelayMeta{RV: testRV}, slot)
			}()
		}
		r.srv.Close()
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil && err != ErrServerClosed {
				t.Fatalf("ServeRelayed: %v", err)
			}
		}
	}
	// After Close the answer is fixed and the stream is released.
	r := newRelayRig(t)
	r.srv.Close()
	if _, ok := r.srv.ReserveRelay(testRV); ok {
		t.Error("a closed server reserved a relay slot")
	}
	cp, sp := net.Pipe()
	defer cp.Close()
	if err := r.srv.ServeRelayed(sp, RelayMeta{RV: testRV}, nil); err != ErrServerClosed {
		t.Fatalf("ServeRelayed after Close = %v, want ErrServerClosed", err)
	}
	_ = cp.SetReadDeadline(time.Now().Add(testTimeout))
	if _, err := cp.Read(make([]byte, 1)); err == nil {
		t.Error("the refused stream was left open")
	}
}

func TestRelaySlotsSeparateFromLAN(t *testing.T) {
	r := newRelayRig(t)
	var slots []*RelaySlot
	for _, rv := range []string{testRV, testRV, otherRV, otherRV} {
		sl, ok := r.srv.ReserveRelay(rv)
		if !ok {
			t.Fatalf("reserve %s refused early", rv)
		}
		slots = append(slots, sl)
	}
	if _, ok := r.srv.ReserveRelay("1111111111111111aaaaaaaaaaaaaaaa"); ok {
		t.Error("a fifth relay slot was granted")
	}
	// A full relay pool leaves the LAN pool untouched.
	rel, why := r.srv.admitPreAuth("203.0.113.9")
	if rel == nil {
		t.Fatalf("LAN pre-auth refused while the relay pool is full: %s", why)
	}
	rel()
	// And the reverse: a full LAN pool leaves relay slots alone.
	slots[0].Release()
	slots[0].Release() // idempotent
	r.srv.mu.Lock()
	r.srv.preAuth = maxPreAuthConns
	r.srv.mu.Unlock()
	if _, ok := r.srv.ReserveRelay(testRV); !ok {
		t.Error("relay reserve refused because the LAN pool is full")
	}
	r.srv.mu.Lock()
	r.srv.preAuth = 0
	r.srv.mu.Unlock()
}

func TestRelaySlotPerRVCap(t *testing.T) {
	r := newRelayRig(t)
	a, _ := r.srv.ReserveRelay(testRV)
	b, _ := r.srv.ReserveRelay(testRV)
	if a == nil || b == nil {
		t.Fatal("two slots per rv must be granted")
	}
	if _, ok := r.srv.ReserveRelay(testRV); ok {
		t.Error("a third slot for one rv was granted")
	}
	a.Release()
	c, ok := r.srv.ReserveRelay(testRV)
	if !ok {
		t.Fatal("a released slot was not reusable")
	}
	b.Release()
	c.Release()
	r.srv.mu.Lock()
	n := len(r.srv.relayPerRV)
	r.srv.mu.Unlock()
	if n != 0 {
		t.Errorf("relayPerRV keeps %d empty entries", n)
	}
}

func TestServeRelayedRVBinding(t *testing.T) {
	r := newRelayRig(t)
	// The device owns testRV; a stream matched under another rv is refused.
	cp, err := r.serve(otherRV)
	if err != nil {
		t.Fatalf("ServeRelayed: %v", err)
	}
	c := r.client(cp)
	_ = c.conn.SetDeadline(time.Now().Add(testTimeout))
	// The refusal is an err frame (or a bare close); never a ready frame.
	for {
		f, err := c.read()
		if err != nil {
			break
		}
		if f.Type == protocol.FrameReady {
			t.Fatal("a relayed connection with a foreign rv was served")
		}
	}
	if !strings.Contains(r.log.all(), "rv mismatch") {
		t.Errorf("no rv mismatch line in:\n%s", r.log.all())
	}
	if n := r.srv.DeviceConns(r.dev.ID); n != 0 {
		t.Errorf("DeviceConns = %d, want 0", n)
	}
}

func TestStopRemoteBoundedWithRelayConns(t *testing.T) {
	r := newRelayRig(t)
	var raws []*closeNowConn
	var clients []*client
	for i := 0; i < maxRelayConnsPerRV; i++ {
		slot, ok := r.srv.ReserveRelay(testRV)
		if !ok {
			t.Fatal("reserve refused")
		}
		cp, sp := net.Pipe()
		t.Cleanup(func() { cp.Close() })
		raw := &closeNowConn{Conn: sp}
		raws = append(raws, raw)
		if err := r.srv.ServeRelayed(raw, RelayMeta{RV: testRV}, slot); err != nil {
			t.Fatalf("ServeRelayed: %v", err)
		}
		c := r.client(cp)
		c.wantReady()
		clients = append(clients, c)
	}
	// A stream stuck mid-handshake must not hold the stop either.
	slot, _ := r.srv.ReserveRelay(otherRV)
	cp, sp := net.Pipe()
	t.Cleanup(func() { cp.Close() })
	stuck := &closeNowConn{Conn: sp}
	if err := r.srv.ServeRelayed(stuck, RelayMeta{RV: otherRV}, slot); err != nil {
		t.Fatalf("ServeRelayed: %v", err)
	}

	done := make(chan struct{})
	go func() { r.srv.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Close did not return with relayed connections open")
	}
	for i, raw := range append(raws, stuck) {
		if raw.closeNow.Load() != 1001 {
			t.Errorf("stream %d closed with CloseNow code %d, want 1001", i, raw.closeNow.Load())
		}
	}
	_ = clients
}

func TestRelayNetConnShape(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	var rc net.Conn = relayNetConn{Conn: a, rv: testRV}
	if rc.RemoteAddr().Network() != "relay" || rc.RemoteAddr().String() != "relay" {
		t.Errorf("RemoteAddr = %v", rc.RemoteAddr())
	}
	if got := rc.(relayed).relayRV(); got != testRV {
		t.Errorf("relayRV = %q", got)
	}
	tuneKeepAlive(rc) // not TCP: a no-op, not a panic
	if err := rc.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestMaxRelayConnectionsMatchesHubproto(t *testing.T) {
	if maxRelayConnections != hubproto.RelayMaxConnsPerDevice {
		t.Errorf("maxRelayConnections = %d, hubproto.RelayMaxConnsPerDevice = %d", maxRelayConnections, hubproto.RelayMaxConnsPerDevice)
	}
	if maxRelayConnsPerRV != hubproto.RelayMaxConnsPerRV {
		t.Errorf("maxRelayConnsPerRV = %d, hubproto.RelayMaxConnsPerRV = %d", maxRelayConnsPerRV, hubproto.RelayMaxConnsPerRV)
	}
}
