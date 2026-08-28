package remote

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

const (
	// reqConcurrency bounds how many req frames one connection may have in
	// flight. An authenticated device is not a trusted one: a stolen phone must
	// not be able to fan out unbounded concurrent work on a daemon that spawns
	// processes, execs gh and tmux, and holds one global mutex. Four is enough
	// for any screen the app draws and small enough that a flood is visible.
	reqConcurrency = 4

	// maxConnections bounds live connections per server. A phone needs one;
	// a reconnect briefly needs two. Beyond that a peer is either broken or
	// exhausting file descriptors, and the refusal is immediate and logged.
	//
	// The slot is taken at ACCEPT, not after the handshake, and that is the
	// whole point of the bound. Counting connections that had already
	// authenticated left a peer that opens sockets and never sends a ClientHello
	// entirely uncounted: each one held a file descriptor and a goroutine for
	// the full handshakeTimeout while admit kept saying yes, so sustained
	// dialling pinned ten seconds' worth of connections at a time and fd
	// exhaustion in this process would have taken the unix socket every other
	// client uses down with it.
	maxConnections = 8

	// keepAliveIdle, keepAliveInterval and keepAliveCount reclaim the slot of a
	// peer that vanished without a FIN — a phone that walked out of WiFi range.
	// Nothing else can: the frame loop deliberately runs with no read deadline,
	// because an attached pane is legitimately silent for minutes at a time.
	// Go's defaults (15s idle, 9 probes) take roughly 150 seconds, which is long
	// enough for a phone on a flaky link to hold every slot and lock itself out;
	// these take about a minute, which is still far above any real round trip.
	keepAliveIdle     = 30 * time.Second
	keepAliveInterval = 10 * time.Second
	keepAliveCount    = 3

	// handshakeTimeout bounds the TLS handshake AND the authenticator's
	// in-band exchange, so a peer that opens a socket and says nothing cannot
	// hold a connection slot indefinitely. There is deliberately no read
	// deadline after that point: an attached pane is legitimately silent for
	// minutes at a time, and shutdown is bounded by the server closing the
	// connection rather than by a timer.
	handshakeTimeout = 10 * time.Second

	// writeTimeout bounds one frame write. A peer that stops reading — a phone
	// whose radio dropped mid-flush — must not pin a pane pump goroutine
	// forever, because that goroutine is one of the things Close waits for.
	writeTimeout = 15 * time.Second

	// paneOpTimeout bounds one PaneBus call. Subscribe and Scroll exec tmux,
	// and a wedged external process must never be able to hang shutdown.
	paneOpTimeout = 10 * time.Second

	// paneQueueDepth bounds the ordered pane-input queue. It is deep enough to
	// absorb a burst of typing and a scroll, and a full queue means the bus
	// itself is wedged — at which point dropping keystrokes silently into a
	// live agent is the worst available outcome, so the connection is closed
	// instead.
	paneQueueDepth = 256
)

// DefaultHandlerGrace bounds how long Close waits for a request handler that is
// still inside Handle. Options.HandlerGrace overrides it, as a field rather
// than a constant for the same reason panebus's intervals are fields: a test
// has to be deterministic without sleeping through the real value.
//
// Close cancels every connection's context first, so an ordinary handler is
// already unwinding by the time this matters; what it bounds is the handler
// that is deliberately SHIELDED from that cancel. d.handle's pollOnce runs its
// tick on context.WithoutCancel and can spawn sessions under a ten-minute
// timeout, and a phone that fires one just before shutdown would otherwise pin
// the listener's Close for the whole of it — reintroducing, inside the step
// that runs FIRST, exactly the unbounded wait that step exists to keep out of
// the daemon's shutdown path. Such a handler also holds a beginConnWork
// registration, so letting go of it here does not lose it: it is waited for in
// drainConnWork, the group built for bounded socket work, which is where it
// belonged all along.
const DefaultHandlerGrace = 5 * time.Second

// Options configures a listener. Handle, Panes, Now and Logf are the exec seams
// — every one of them is a plain func or a small interface so the tests below
// drive a stub and never touch a socket, a process or the daemon.
type Options struct {
	// Bind is the [remote].bind mode: off | localhost | lan | all | an IP
	// literal. "" means localhost.
	Bind string

	// Port is the TCP port.
	Port int

	// Dir is the lola home directory, where device.key and device.crt live.
	// Required: this package never derives it, so a test never writes into a
	// real home.
	Dir string

	// Handle is the daemon's dispatcher, bound to d.handle in production. It is
	// reached only by a frame that has already been versioned, direction
	// checked and authorized, and only with a Request this package rebuilt.
	Handle func(ctx context.Context, req protocol.Request) protocol.Response

	// Panes is the pane-attach seam. A nil Panes refuses every subscription
	// with CodeUnknownPane rather than panicking: a daemon built without a bus
	// still serves the read commands.
	Panes PaneBus

	// Now is the clock seam. nil means time.Now.
	Now func() time.Time

	// Logf writes the daemon's log. nil discards, which is what the tests want;
	// production always passes one, because the audit line is the only forensic
	// capability this transport has.
	Logf func(format string, args ...any)

	// HandlerGrace bounds Close's wait for a request still inside Handle; zero
	// means DefaultHandlerGrace. See that constant for why the wait is bounded
	// at all.
	HandlerGrace time.Duration
}

func (o Options) handlerGrace() time.Duration {
	if o.HandlerGrace > 0 {
		return o.HandlerGrace
	}
	return DefaultHandlerGrace
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o Options) logf() func(string, ...any) {
	if o.Logf != nil {
		return o.Logf
	}
	return func(string, ...any) {}
}

// Server is a running listener. It owns its goroutines and its own WaitGroup:
// Close is bounded and synchronous, so the daemon must NOT register any of this
// with connWg, whose drain is unbounded and was built for bounded socket work.
type Server struct {
	opts  Options
	auth  Authorizer
	key   *DeviceKey
	addrs []BindAddr

	ctx    context.Context
	cancel context.CancelFunc

	wg sync.WaitGroup

	// slots is the connection bound, taken at accept and released when
	// serveConn returns. It is a semaphore rather than len(conns) because conns
	// only records peers that finished a handshake.
	slots chan struct{}

	mu     sync.Mutex
	lns    []net.Listener
	conns  map[*conn]struct{}
	closed bool

	closeOnce sync.Once
}

// listen is the tag-free half of Listen: it loads the identity, resolves the
// bind addresses, binds them and starts accepting. The tagged half chooses the
// authorizer and, in an insecure build, forces the bind to loopback before
// calling this.
func listen(ctx context.Context, opts Options, auth Authorizer) (*Server, error) {
	if auth == nil {
		return nil, ErrNoAuthorizer
	}
	if opts.Handle == nil {
		return nil, errors.New("remote: Options.Handle is required")
	}
	if opts.Dir == "" {
		return nil, errors.New("remote: Options.Dir is required")
	}
	logf := opts.logf()

	key, err := LoadOrCreateDeviceKey(opts.Dir, logf)
	if err != nil {
		return nil, err
	}
	if key.Created {
		logf("remote: generated a new device identity at %s (SPKI pin %s)", key.KeyPath, key.SPKIPin())
	}

	addrs, err := resolveBind(opts.Bind, opts.Port, systemIfaces)
	if err != nil {
		return nil, err
	}

	s := &Server{
		opts:  opts,
		auth:  auth,
		key:   key,
		addrs: addrs,
		conns: map[*conn]struct{}{},
		slots: make(chan struct{}, maxConnections),
	}
	s.ctx, s.cancel = context.WithCancel(ctx)

	// A mode may resolve to several addresses (both loopback families, or every
	// qualifying LAN interface), and one of them failing is ordinary — a
	// machine with IPv6 disabled has no ::1 to bind. So every failure is LOGGED
	// BY NAME and the listener comes up on what it could take; only failing on
	// ALL of them is fatal, because a listener that binds nothing and reports
	// success is a daemon that looks healthy and cannot be reached. Which
	// addresses it actually took is otherwise unanswerable, which is the same
	// reason bind = "lan" logs its interfaces.
	tlsCfg := key.TLSConfig()
	var bound []BindAddr
	var lastErr error
	for _, ba := range addrs {
		ln, err := net.Listen("tcp", ba.Addr)
		if err != nil {
			lastErr = err
			logf("remote: cannot listen on %s%s: %v", ba.Addr, ifaceNote(ba.Iface), err)
			continue
		}
		tln := tls.NewListener(ln, tlsCfg)
		s.mu.Lock()
		s.lns = append(s.lns, tln)
		s.mu.Unlock()
		bound = append(bound, ba)
		logf("remote: listening on %s%s", ba.Addr, ifaceNote(ba.Iface))
		s.wg.Add(1)
		go s.acceptLoop(tln)
	}
	if len(bound) == 0 {
		s.closeListeners()
		s.cancel()
		return nil, fmt.Errorf("remote: no address of %d could be bound: %w", len(addrs), lastErr)
	}
	s.addrs = bound

	// The run context is the daemon's CANCELLABLE one, the same posture as the
	// interpreter and review workers: everything here is read-only fan-out or a
	// request already in flight, so aborting costs a repaint rather than state.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		<-s.ctx.Done()
		s.closeListeners()
		s.closeConns()
	}()

	return s, nil
}

// ifaceNote renders the interface an address came from, for a log line.
func ifaceNote(name string) string {
	if name == "" {
		return ""
	}
	return " (" + name + ")"
}

// Addrs reports the addresses actually bound, with the interface each came
// from. It is what the daemon's startup log and the doctor read.
func (s *Server) Addrs() []BindAddr {
	out := make([]BindAddr, len(s.addrs))
	copy(out, s.addrs)
	return out
}

// SPKIPin is the base64 SHA-256 of this daemon's public key: the value a client
// pins and, from M2, the value the pairing QR carries.
func (s *Server) SPKIPin() string { return s.key.SPKIPin() }

// Close shuts the listener down. It is bounded and synchronous, and the ORDER
// is the invariant: close the listeners first so Accept returns net.ErrClosed
// exactly as the daemon's unix path does, then close every live connection —
// which unblocks its reader and every writer stuck on a peer that stopped
// reading — and only then wait. Waiting before closing would hang until the
// phone happened to disconnect, which is the failure this ordering exists to
// prevent. Close is idempotent.
//
// "Bounded" is a claim this package can make about everything it owns —
// listeners, readers, pane workers and pane pumps all stop on the cancel and
// the socket close — but NOT about the Handle seam, which is the daemon's code
// and may deliberately outlive a cancel. So a request still inside Handle is
// waited for only up to handlerGrace and then left to finish under the daemon's
// own drain group; see waitGoroutines.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.cancel()
		s.closeListeners()
		s.closeConns()
		s.wg.Wait()
		s.opts.logf()("remote: listener stopped")
	})
	return nil
}

func (s *Server) closeListeners() {
	s.mu.Lock()
	lns := s.lns
	s.lns = nil
	s.mu.Unlock()
	for _, ln := range lns {
		ln.Close()
	}
}

func (s *Server) closeConns() {
	s.mu.Lock()
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.shutdown()
	}
}

// acceptLoop runs until the listener is closed.
func (s *Server) acceptLoop(ln net.Listener) {
	defer s.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			s.opts.logf()("remote: accept loop panic: %v", r)
		}
	}()
	for {
		nc, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || s.ctx.Err() != nil {
				return
			}
			s.opts.logf()("remote: accept: %v", err)
			continue
		}
		if !s.admit() {
			s.opts.logf()("remote: refused %s: %d connections already open", nc.RemoteAddr(), maxConnections)
			nc.Close()
			continue
		}
		s.wg.Add(1)
		go s.serveConn(nc)
	}
}

// admit takes a connection slot, or reports that there is none. The slot is
// held from accept until serveConn returns, so an unauthenticated peer parked
// in the handshake occupies one exactly as an authenticated peer does.
func (s *Server) admit() bool {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return false
	}
	select {
	case s.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

// release returns a connection slot. Paired with every admit that returned
// true, from serveConn's defer, so a handshake failure frees the slot too.
func (s *Server) release() {
	select {
	case <-s.slots:
	default:
	}
}

// addConn records a peer that finished its handshake. It is bookkeeping for
// closeConns, not admission control — the slot was taken at accept.
func (s *Server) addConn(c *conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) removeConn(c *conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

// serveConn completes the TLS handshake under a deadline and runs the frame
// loop. Every goroutine this package starts is panic-guarded: a panic in a
// frame handler must cost one connection, never the daemon.
func (s *Server) serveConn(nc net.Conn) {
	defer s.wg.Done()
	defer s.release()
	defer func() {
		if r := recover(); r != nil {
			s.opts.logf()("remote: connection panic: %v", r)
		}
	}()
	defer nc.Close()

	tuneKeepAlive(nc)

	if tc, ok := nc.(*tls.Conn); ok {
		hctx, cancel := context.WithTimeout(s.ctx, handshakeTimeout)
		err := tc.HandshakeContext(hctx)
		cancel()
		if err != nil {
			// The peer's address is worth a line; the error text is not
			// sensitive, but nothing from the handshake is echoed back.
			s.opts.logf()("remote: handshake from %s failed: %v", nc.RemoteAddr(), err)
			return
		}
	}

	c := newConn(s, nc)
	if !s.addConn(c) {
		return
	}
	defer s.removeConn(c)
	c.run()
}

// tuneKeepAlive shortens the OS keep-alive on an accepted connection so a peer
// that vanished without a FIN gives its slot (and its pane subscriptions, and
// therefore a tmux attach) back in about a minute rather than in about two and
// a half.
//
// It fails OPEN in every direction: a connection that is not TCP, or a platform
// that will not take the config, keeps the system default. A keep-alive is a
// courtesy on top of Close, never the thing shutdown depends on.
func tuneKeepAlive(nc net.Conn) {
	tc, ok := nc.(*net.TCPConn)
	if !ok {
		// An accepted connection is the TLS one, which wraps the TCP conn.
		if tc2, isTLS := nc.(*tls.Conn); isTLS {
			tc, ok = tc2.NetConn().(*net.TCPConn)
		}
		if !ok {
			return
		}
	}
	_ = tc.SetKeepAliveConfig(net.KeepAliveConfig{
		Enable:   true,
		Idle:     keepAliveIdle,
		Interval: keepAliveInterval,
		Count:    keepAliveCount,
	})
}
