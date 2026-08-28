package remote

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// TestUnauthenticatedConnectionsAreBoundedAtAccept proves the connection bound
// counts what it claims to.
//
// maxConnections was checked against the map of peers that had FINISHED a
// handshake, so a peer that opens TCP sockets and never sends a ClientHello was
// not counted at all: each held a file descriptor and a goroutine for the whole
// handshake timeout while admission kept saying yes. fd exhaustion in this
// process takes the unix socket every other lola client uses down with it.
func TestUnauthenticatedConnectionsAreBoundedAtAccept(t *testing.T) {
	s := startTestListener(t, &stubHandler{}, newFakeBus())
	addr := s.Addrs()[0].Addr

	// Fill every slot with peers that complete TCP and then say nothing.
	var held []net.Conn
	defer func() {
		for _, c := range held {
			c.Close()
		}
	}()
	for i := 0; i < maxConnections; i++ {
		c, err := net.DialTimeout("tcp", addr, testTimeout)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		held = append(held, c)
	}

	// Give the accept loop a moment to take every slot, then prove the next
	// connection is refused PROMPTLY rather than parked in the handshake for
	// handshakeTimeout.
	waitFor(t, "one slot per ACCEPTED connection (a peer parked in the handshake must not be counted for nothing)",
		func() bool { return len(s.slots) == maxConnections })

	extra, err := net.DialTimeout("tcp", addr, testTimeout)
	if err != nil {
		return // refused at the kernel is also a refusal
	}
	defer extra.Close()
	_ = extra.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := extra.Read(make([]byte, 1)); err == nil {
		t.Fatal("the over-limit connection was served")
	} else if errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("the over-limit connection was accepted and left parked; %d unauthenticated peers counted for nothing", maxConnections)
	}

	// A slot freed by a departing peer is reusable, so the bound is a bound and
	// not a leak.
	held[0].Close()
	held = held[1:]
	waitFor(t, "the slot to be released", func() bool { return len(s.slots) < maxConnections })
}

// TestCloseIsNotHeldByAShieldedHandler is the shutdown bound.
//
// Server.Close runs FIRST in the daemon's shutdown precisely so the unbounded
// drain group never sees a live stream. But it transitively waited for every
// in-flight Handle call, and two remote-reachable commands deliberately outlive
// a cancel: d.handle's pollOnce runs its tick on context.WithoutCancel and can
// spawn sessions under a ten-minute timeout. That reintroduced the unbounded
// wait inside the very step meant to remove it.
func TestCloseIsNotHeldByAShieldedHandler(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{})
	var once sync.Once

	h := &stubHandler{fn: func(ctx context.Context, _ protocol.Request) protocol.Response {
		once.Do(func() { close(entered) })
		// Shielded from the connection's cancel, exactly as handlePollOnce is.
		<-release
		return protocol.Response{OK: true}
	}}

	s, err := listen(context.Background(), Options{
		Bind:         "localhost",
		Port:         freePort(t),
		Dir:          t.TempDir(),
		Handle:       h.handle,
		Panes:        newFakeBus(),
		Logf:         func(string, ...any) {},
		HandlerGrace: 150 * time.Millisecond,
	}, &stubAuth{})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	_, w, _ := dialTLS(t, s)
	f := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: "p1", Cmd: "pollOnce"}
	if err := f.SetPayload(protocol.Request{Cmd: "pollOnce"}); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if err := w.WriteFrame(&f); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(testTimeout):
		t.Fatal("the handler was never reached")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Close()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited for a handler that is shielded from its cancel")
	}
}

// TestTuneKeepAliveFailsOpen pins the shape of the keep-alive tightening: a
// real TCP connection takes it, and anything else is left exactly as it was. A
// keep-alive is a courtesy on top of Close, never something shutdown depends
// on, so an unsupported connection must be a no-op rather than an error path.
func TestTuneKeepAliveFailsOpen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			tuneKeepAlive(c) // must not panic on the accepted side either
			c.Close()
		}
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	tuneKeepAlive(c)

	// A net.Pipe is not TCP; the rig runs over one, so this path is taken by
	// every other test in the package.
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	tuneKeepAlive(a)
}

// waitFor polls a condition with a deadline, so a missed transition fails the
// test instead of hanging it.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
