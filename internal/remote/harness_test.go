package remote

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// The tests below drive the frame loop over an in-memory pipe with stub seams,
// so nothing here touches tmux, a process or a real socket. Two of them
// deliberately do open a real loopback TLS listener, because bind, identity and
// bounded shutdown are only meaningfully proved end to end.

const testTimeout = 5 * time.Second

// --- stub authorizer -------------------------------------------------------

type stubAuth struct {
	peer      Peer
	authErr   error
	frameErr  error
	mu        sync.Mutex
	seenTypes []string
	seenPeers []Peer
}

func (a *stubAuth) Authenticate(context.Context, *Handshake) (Peer, error) {
	if a.authErr != nil {
		return Peer{}, a.authErr
	}
	p := a.peer
	if p.DeviceID == "" {
		p.DeviceID = "test-device"
	}
	return p, nil
}

func (a *stubAuth) AuthorizeFrame(_ context.Context, p Peer, f *protocol.Frame) error {
	a.mu.Lock()
	a.seenTypes = append(a.seenTypes, f.Type+":"+f.Cmd)
	a.seenPeers = append(a.seenPeers, p)
	a.mu.Unlock()
	return a.frameErr
}

func (a *stubAuth) seen() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.seenTypes...)
}

func (a *stubAuth) peers() []Peer {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Peer(nil), a.seenPeers...)
}

// --- stub handler ----------------------------------------------------------

type stubHandler struct {
	mu    sync.Mutex
	calls []protocol.Request
	fn    func(ctx context.Context, req protocol.Request) protocol.Response
}

func (h *stubHandler) handle(ctx context.Context, req protocol.Request) protocol.Response {
	h.mu.Lock()
	h.calls = append(h.calls, req)
	h.mu.Unlock()
	if h.fn != nil {
		return h.fn(ctx, req)
	}
	return protocol.Response{OK: true}
}

func (h *stubHandler) requests() []protocol.Request {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]protocol.Request(nil), h.calls...)
}

func (h *stubHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.calls)
}

// --- fake pane bus ---------------------------------------------------------

type fakeStream struct {
	ch     chan PaneFrame
	closed atomic.Bool
}

func newFakeStream() *fakeStream { return &fakeStream{ch: make(chan PaneFrame, 64)} }

func (s *fakeStream) Frames() <-chan PaneFrame { return s.ch }

func (s *fakeStream) Close() error {
	if s.closed.CompareAndSwap(false, true) {
		close(s.ch)
	}
	return nil
}

// push is the test's way of making the bus emit; it is a no-op after Close so a
// teardown race cannot panic the test binary.
func (s *fakeStream) push(f PaneFrame) {
	if s.closed.Load() {
		return
	}
	s.ch <- f
}

type fakeBus struct {
	mu       sync.Mutex
	subs     []string
	writes   []string
	scrolls  []int
	streams  []*fakeStream
	refuse   map[string]error
	writeErr error
}

func newFakeBus() *fakeBus { return &fakeBus{refuse: map[string]error{}} }

// refusePane arms Subscribe to fail for one pane, under the lock the bus reads
// it with.
func (b *fakeBus) refusePane(pane string, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refuse[pane] = err
}

// failWrites arms Write to fail.
func (b *fakeBus) failWrites(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writeErr = err
}

func (b *fakeBus) Subscribe(_ context.Context, pane string, cols, rows int) (PaneStream, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.refuse[pane]; err != nil {
		return nil, err
	}
	b.subs = append(b.subs, pane)
	st := newFakeStream()
	// A real bus always leads with a resync; the fake does too, because the
	// tests below are about the transport honouring that, not about inventing
	// it.
	st.ch <- PaneFrame{Kind: PaneResync, Screen: &PaneScreen{
		Cols: 200, Rows: 50, Lines: []string{"\x1b[2m*\x1b[0m Cogitated", "", "> "},
		CursorX: 2, CursorY: 2, AltScreen: true,
	}}
	b.streams = append(b.streams, st)
	_ = cols
	_ = rows
	return st, nil
}

func (b *fakeBus) Write(pane string, p []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.writeErr != nil {
		return b.writeErr
	}
	b.writes = append(b.writes, pane+":"+string(p))
	return nil
}

func (b *fakeBus) Scroll(_ context.Context, _ string, lines int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.scrolls = append(b.scrolls, lines)
	return nil
}

func (b *fakeBus) snapshot() (subs, writes []string, scrolls []int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.subs...), append([]string(nil), b.writes...), append([]int(nil), b.scrolls...)
}

func (b *fakeBus) stream(i int) *fakeStream {
	b.mu.Lock()
	defer b.mu.Unlock()
	if i >= len(b.streams) {
		return nil
	}
	return b.streams[i]
}

// --- log capture -----------------------------------------------------------

type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func (l *logCapture) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, sprintf(format, args...))
}

func (l *logCapture) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// sprintf is fmt.Sprintf under another name so the capture reads like the
// daemon's own logf, which takes the same shape.
func sprintf(format string, args ...any) string {
	return fmtSprintf(format, args...)
}

// --- test rig --------------------------------------------------------------

type rig struct {
	t       *testing.T
	srv     *Server
	auth    *stubAuth
	handler *stubHandler
	bus     *fakeBus
	log     *logCapture

	// authOverride replaces the stub authorizer before the connection starts.
	// The tagged tests use it to drive the real bearer-key handshake.
	authOverride Authorizer

	// server is the SERVER side of the pipe, for the handful of assertions
	// about connection state that no frame exchange can reach.
	server *conn

	client  *protocol.FrameWriter
	in      chan protocol.Frame
	readErr chan error
	closed  chan struct{}
	conn    net.Conn
}

// newRig wires a connection over net.Pipe and runs the server side of it.
//
// setup runs BEFORE the connection goroutine starts, which matters for
// anything the authenticate step reads: a test that armed stubAuth after
// newRig returned would be racing the handshake.
func newRig(t *testing.T, setup ...func(*rig, *Options)) *rig {
	t.Helper()
	r := &rig{
		t:       t,
		auth:    &stubAuth{},
		handler: &stubHandler{},
		bus:     newFakeBus(),
		log:     &logCapture{},
		in:      make(chan protocol.Frame, 64),
		readErr: make(chan error, 1),
		closed:  make(chan struct{}),
	}
	opts := Options{Handle: r.handler.handle, Panes: r.bus, Logf: r.log.logf}
	for _, m := range setup {
		m(r, &opts)
	}

	var auth Authorizer = r.auth
	if r.authOverride != nil {
		auth = r.authOverride
	}
	s := &Server{opts: opts, auth: auth, conns: map[*conn]struct{}{}, slots: make(chan struct{}, maxConnections)}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	r.srv = s

	clientSide, serverSide := net.Pipe()
	r.conn = clientSide
	c := newConn(s, serverSide)
	r.server = c
	// Registered with the server exactly as serveConn does, so Server.Close
	// and closeConns reach it.
	s.addConn(c)
	go func() {
		defer close(r.closed)
		defer s.removeConn(c)
		c.run()
	}()

	r.client = protocol.NewFrameWriter(clientSide)
	go func() {
		fr := protocol.NewFrameReader(clientSide)
		for {
			var f protocol.Frame
			if err := fr.ReadFrame(&f); err != nil {
				r.readErr <- err
				return
			}
			r.in <- f
		}
	}()

	t.Cleanup(func() {
		s.cancel()
		clientSide.Close()
	})
	return r
}

// send writes one frame from the client. A write to a closed pipe is not a test
// failure by itself: several tests deliberately provoke a close.
func (r *rig) send(f protocol.Frame) {
	r.t.Helper()
	if err := r.client.WriteFrame(&f); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		r.t.Logf("send: %v", err)
	}
}

func (r *rig) req(id, cmd string, payload any) {
	r.t.Helper()
	f := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: id, Cmd: cmd}
	if payload != nil {
		if err := f.SetPayload(payload); err != nil {
			r.t.Fatalf("payload: %v", err)
		}
	}
	r.send(f)
}

// next waits for one frame from the server.
//
// A frame ALREADY delivered wins over a close, and that priority is what makes
// the "refuse, then hang up" tests deterministic. The client reader pushes the
// refusal into r.in and then, on its next read, pushes EOF into r.readErr; once
// both are ready a single select picks between them uniformly at random, so a
// refusal the server genuinely sent was thrown away in favour of the close
// roughly half the time it raced. That reads as a transport bug and is not one.
func (r *rig) next() protocol.Frame {
	r.t.Helper()
	select {
	case f := <-r.in:
		return f
	default:
	}
	select {
	case f := <-r.in:
		return f
	case err := <-r.readErr:
		r.t.Fatalf("connection closed while waiting for a frame: %v", err)
	case <-time.After(testTimeout):
		r.t.Fatalf("timed out waiting for a frame")
	}
	return protocol.Frame{}
}

// wantErr reads one frame and requires it to be a refusal with the given code.
func (r *rig) wantErr(code string) protocol.ErrPayload {
	r.t.Helper()
	f := r.next()
	if f.Type != protocol.FrameErr {
		r.t.Fatalf("got frame type %q, want %q", f.Type, protocol.FrameErr)
	}
	var p protocol.ErrPayload
	if err := json.Unmarshal(f.Payload, &p); err != nil {
		r.t.Fatalf("decode err payload: %v", err)
	}
	if p.Code != code {
		r.t.Fatalf("got refusal code %q, want %q (message %q)", p.Code, code, p.Message)
	}
	return p
}

// wantClosed requires the server to have hung up.
func (r *rig) wantClosed() {
	r.t.Helper()
	select {
	case <-r.closed:
	case <-time.After(testTimeout):
		r.t.Fatalf("connection was not closed")
	}
}

// wantOpen requires the connection to still be serving, proved by a round trip.
func (r *rig) wantOpen() {
	r.t.Helper()
	r.req("ping", "status", protocol.Request{})
	f := r.next()
	if f.Type != protocol.FrameResp || f.ID != "ping" {
		r.t.Fatalf("connection did not answer a follow-up request: %+v", f)
	}
}

// rawFrame writes a length prefix and body directly, for the cases the codec
// would refuse to produce.
func (r *rig) rawFrame(length uint32, body []byte) {
	r.t.Helper()
	hdr := make([]byte, protocol.FrameHeaderBytes)
	binary.BigEndian.PutUint32(hdr, length)
	if _, err := r.conn.Write(hdr); err != nil {
		r.t.Logf("raw header: %v", err)
		return
	}
	if len(body) > 0 {
		if _, err := r.conn.Write(body); err != nil {
			r.t.Logf("raw body: %v", err)
		}
	}
}

// fmtSprintf keeps the fmt import in one place.
func fmtSprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// serverConn is the server side of the rig's connection.
func (r *rig) serverConn() *conn {
	r.t.Helper()
	if r.server == nil {
		r.t.Fatal("the rig has no server connection")
	}
	return r.server
}
