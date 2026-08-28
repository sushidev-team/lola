package remote

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

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

// startTestListener brings up a real loopback TLS listener with stub seams. It
// deliberately goes through the same listen() the tagged Listen calls, so bind
// selection, the device identity and the TLS configuration are all exercised.
func startTestListener(t *testing.T, h *stubHandler, bus PaneBus) *Server {
	t.Helper()
	s, err := listen(context.Background(), Options{
		Bind:   "localhost",
		Port:   freePort(t),
		Dir:    t.TempDir(),
		Handle: h.handle,
		Panes:  bus,
		Logf:   func(string, ...any) {},
	}, &stubAuth{})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// dialTLS connects to the first bound address.
func dialTLS(t *testing.T, s *Server) (*protocol.FrameReader, *protocol.FrameWriter, net.Conn) {
	t.Helper()
	addrs := s.Addrs()
	if len(addrs) == 0 {
		t.Fatal("the server bound no address")
	}
	// The client pins the SPKI rather than verifying a name; M1's stand-in for
	// that is skipping verification and checking the pin here.
	conn, err := tls.Dial("tcp", addrs[0].Addr, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatalf("dial %s: %v", addrs[0].Addr, err)
	}
	t.Cleanup(func() { conn.Close() })
	return protocol.NewFrameReader(conn), protocol.NewFrameWriter(conn), conn
}

// TestListenServesAFrameOverRealTLS is the one end-to-end pass: a real
// loopback listener, a real TLS 1.3 handshake against the generated device
// identity, and a request that reaches the handler and comes back.
func TestListenServesAFrameOverRealTLS(t *testing.T) {
	h := &stubHandler{}
	s := startTestListener(t, h, newFakeBus())

	for _, ba := range s.Addrs() {
		if !loopbackAddr(ba.Addr) {
			t.Fatalf("bound a non-loopback address %s for bind = localhost", ba.Addr)
		}
	}

	fr, fw, conn := dialTLS(t, s)
	if st := conn.(*tls.Conn).ConnectionState(); st.Version != tls.VersionTLS13 {
		t.Errorf("negotiated %#x, want TLS 1.3", st.Version)
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
	s := startTestListener(t, &stubHandler{}, bus)

	fr, fw, conn := dialTLS(t, s)
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
	s := startTestListener(t, &stubHandler{}, newFakeBus())
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
