package remote

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/protocol"
)

const (
	// reqConcurrency bounds how many req frames one connection may have in
	// flight. An authenticated device is not a trusted one: a stolen phone must
	// not be able to fan out unbounded concurrent work on a daemon that spawns
	// processes, execs gh and tmux, and holds one global mutex. Four is enough
	// for any screen the app draws and small enough that a flood is visible.
	reqConcurrency = 4

	// The connection pools (spec 01 §5.1). The listener is untagged and meant
	// for bind = "lan", so an unauthenticated LAN peer must not be able to fill
	// the slots a paired phone needs.
	//
	// The pre-auth slot is taken at ACCEPT, before TLS, keyed by source IP, and
	// is the whole point of the bound: a peer that opens sockets and never sends
	// a ClientHello would otherwise hold a file descriptor and a goroutine for
	// the full handshakeTimeout while admission kept saying yes. It is released
	// when the connection ends or MOVES to the authed pool after Authenticate.
	// The relay path has its own pre-auth pool, so a LAN flood never blocks a
	// relayed reconnect, nor the reverse.
	maxPreAuthConns = 32
	maxPreAuthPerIP = 2

	// maxConnsPerDevice: past it the device's OLDEST connection is shut down.
	// The newest has just proven the key; the oldest is the likeliest half-open
	// socket left by a network change.
	maxConnsPerDevice = 4

	// maxAuthedConns is reachable only by paired devices.
	maxAuthedConns = devices.MaxDevices * maxConnsPerDevice

	// maxRefusalsPerConn bounds a probing client: refusals keep the connection,
	// so something must end one that only ever asks for what it may not have.
	maxRefusalsPerConn = 64

	// limiterSweepEvery spaces the opportunistic sweep of idle device buckets.
	limiterSweepEvery = time.Minute

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

	// Devices is the registry of paired phones. Required: it is owned by the
	// daemon and survives listener rebuilds.
	Devices *devices.Registry

	// Pairer runs pairing. Required, with the same ownership as Devices.
	Pairer *Pairer

	// Host is os.Hostname(), for ReadyData and PairResult. Display only.
	Host string

	// Events is the push seam. nil answers every watch with WatchCodeUnavailable.
	Events Events

	// RelayOnly allows a Server with no listener: bind = "off" with the hub
	// relay on still serves relayed connections.
	RelayOnly bool
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

	// tlsConfig is built once and shared by the listeners and by ServeRelayed:
	// one config, one code path.
	tlsConfig *tls.Config

	ctx    context.Context
	cancel context.CancelFunc

	wg sync.WaitGroup

	// lim holds the per-device rate buckets; refusals throttles the unpaired
	// certificate log line.
	lim      *limiter
	refusals *refusalLog

	// preAuthLog throttles the lines an unauthenticated peer can cause at will
	// (a refused accept, a failed handshake) with one global bucket, so a flood
	// costs the unrotated daemon log a bounded number of lines and this process
	// no per-address state.
	preAuthLog *refusalLog

	// relaySlots is the relay pre-auth pool (taken by relay.go before TLS) and
	// relayPerRV counts the slots one rendezvous holds, under mu.
	relaySlots chan struct{}
	relayPerRV map[string]int

	mu     sync.Mutex
	lns    []net.Listener
	conns  map[*conn]struct{}
	closed bool

	// preAuth and preAuthIP count LAN sockets between accept and Authenticate;
	// authed counts authenticated ones. All under mu. preAuthIP holds only IPs
	// with a live pre-auth socket, so it is bounded by maxPreAuthConns.
	preAuth   int
	preAuthIP map[string]int
	authed    int
	connSeq   uint64
	lastSweep time.Time

	closeOnce sync.Once
}

// Listen is the single entry point: it checks the registry, builds the device
// authorizer, loads the identity, resolves the bind addresses, binds them and
// starts accepting.
//
// A registry that cannot persist a revocation refuses the listener outright (a
// revoked phone must not be accepted again after a restart); one that cannot be
// READ still listens, so the doctor and pairing report the problem through the
// daemon rather than a dead port, and authenticates nobody.
func Listen(ctx context.Context, opts Options) (*Server, error) {
	if opts.Devices == nil || opts.Pairer == nil {
		return nil, errors.New("remote: Options.Devices and Options.Pairer are required")
	}
	if err := opts.Devices.Unsaved(); err != nil {
		return nil, fmt.Errorf("remote: refusing to listen: %w", err)
	}
	logf := opts.logf()
	if err := opts.Devices.Err(); err != nil {
		logf("remote: devices.json is unreadable; no phone can connect: %v", err)
	}
	auth := newDeviceAuthorizer(opts.Devices, opts.Pairer, opts.Host, opts.Now, logf)
	return listen(ctx, opts, auth)
}

// newServer builds a Server with no sockets. key may be nil only in a test that
// drives connections by hand.
func newServer(ctx context.Context, opts Options, auth Authorizer, key *DeviceKey) *Server {
	s := &Server{
		opts:       opts,
		auth:       auth,
		key:        key,
		conns:      map[*conn]struct{}{},
		preAuthIP:  map[string]int{},
		relaySlots: make(chan struct{}, maxRelayConnections),
		relayPerRV: map[string]int{},
		lim:        newLimiter(opts.Now),
		refusals:   newRefusalLog(opts.now()),
		preAuthLog: newRefusalLog(opts.now()),
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	if key != nil {
		s.tlsConfig = key.TLSConfig(s.verifyPeer)
	}
	return s
}

// listen is the half of Listen that needs an Authorizer: tests drive it with a
// stub one.
func listen(ctx context.Context, opts Options, auth Authorizer) (*Server, error) {
	if auth == nil {
		return nil, errors.New("remote: an authorizer is required")
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
	if opts.Pairer != nil {
		// The confirmation code binds both keys and the connection state does
		// not expose the local certificate, so the listener supplies it.
		opts.Pairer.SetDaemonSPKI(key.SPKISHA256())
	}

	var addrs []BindAddr
	if !opts.RelayOnly {
		if addrs, err = resolveBind(opts.Bind, opts.Port, systemIfaces); err != nil {
			return nil, err
		}
	}

	s := newServer(ctx, opts, auth, key)
	s.addrs = addrs

	// A mode may resolve to several addresses (both loopback families, or every
	// qualifying LAN interface), and one of them failing is ordinary — a
	// machine with IPv6 disabled has no ::1 to bind. So every failure is LOGGED
	// BY NAME and the listener comes up on what it could take; only failing on
	// ALL of them is fatal, because a listener that binds nothing and reports
	// success is a daemon that looks healthy and cannot be reached. Which
	// addresses it actually took is otherwise unanswerable, which is the same
	// reason bind = "lan" logs its interfaces.
	var bound []BindAddr
	var lastErr error
	for _, ba := range addrs {
		ln, err := net.Listen("tcp", ba.Addr)
		if err != nil {
			lastErr = err
			logf("remote: cannot listen on %s%s: %v", ba.Addr, ifaceNote(ba.Iface), err)
			continue
		}
		tln := tls.NewListener(ln, s.tlsConfig)
		s.mu.Lock()
		s.lns = append(s.lns, tln)
		s.mu.Unlock()
		bound = append(bound, ba)
		logf("remote: listening on %s%s", ba.Addr, ifaceNote(ba.Iface))
		s.wg.Add(1)
		go s.acceptLoop(tln)
	}
	if len(addrs) > 0 && len(bound) == 0 {
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
// from. It is what the daemon's startup log and the doctor read. Empty for a
// relay-only server.
func (s *Server) Addrs() []BindAddr {
	out := make([]BindAddr, len(s.addrs))
	copy(out, s.addrs)
	return out
}

// liveConns reports how many peers are connected right now.
//
// Read by BindDrifted, which must not tear a listener down over an address
// APPEARING while somebody is streaming a pane through it.
func (s *Server) liveConns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// Port reports the TCP port the listener is actually on.
//
// Read from a BOUND address rather than from the options, because the two can
// differ — a port of 0 asks the kernel to pick one — and the advertiser must
// publish the port a phone can dial, not the one that was requested. A server
// with no bound address (relay-only) has no port to state and answers 0.
func (s *Server) Port() int {
	s.mu.Lock()
	addrs := append([]BindAddr(nil), s.addrs...)
	s.mu.Unlock()
	for _, ba := range addrs {
		if _, port, err := net.SplitHostPort(ba.Addr); err == nil {
			if n, err := strconv.Atoi(port); err == nil && n > 0 {
				return n
			}
		}
	}
	if len(addrs) == 0 {
		return 0
	}
	return s.opts.Port
}

// SPKIPin is the base64 SHA-256 of this daemon's public key: the value a client
// pins and the value the pairing QR carries.
func (s *Server) SPKIPin() string { return s.key.SPKIPin() }

// DropDevice shuts down every authenticated connection of the device and
// forgets its rate buckets. It is called synchronously after a revoke or a tier
// change, so a revoked or downgraded device's attached panes end with its
// connections; the per-frame check covers the race in between.
func (s *Server) DropDevice(id string) int {
	var hit []*conn
	s.mu.Lock()
	for c := range s.conns {
		if c.authed && c.peer.DeviceID == id {
			hit = append(hit, c)
		}
	}
	s.mu.Unlock()
	for _, c := range hit {
		c.shutdown()
	}
	s.lim.forget(id)
	return len(hit)
}

// DeviceConns reports the live authenticated connections of the device.
func (s *Server) DeviceConns(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for c := range s.conns {
		if c.authed && c.peer.DeviceID == id {
			n++
		}
	}
	return n
}

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

// closeConns shuts every connection down CONCURRENTLY: tls.Conn.Close sends
// close_notify under its own 5 s write deadline, and in series four stalled
// connections would add 20 s to a stop.
func (s *Server) closeConns() {
	s.mu.Lock()
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, c := range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.shutdown()
		}()
	}
	wg.Wait()
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
		release, why := s.admitPreAuth(sourceIP(nc.RemoteAddr()))
		if release == nil {
			s.logPreAuth("remote: refused %s: %s", logSafe(nc.RemoteAddr().String()), why)
			nc.Close()
			continue
		}
		s.wg.Add(1)
		go s.serveConn(nc, release)
	}
}

// sourceIP is the host part of an address, the key of the per-IP pre-auth cap.
func sourceIP(a net.Addr) string {
	if a == nil {
		return ""
	}
	if host, _, err := net.SplitHostPort(a.String()); err == nil {
		return host
	}
	return a.String()
}

// admitPreAuth takes a LAN pre-auth slot for ip. The slot is held from accept
// until the connection ends or authenticates, so an unauthenticated peer parked
// in the handshake occupies one exactly as an authenticated peer used to. A nil
// release means refused, and why names the pool for the log. release is
// idempotent.
func (s *Server) admitPreAuth(ip string) (release func(), why string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, "server closing"
	}
	if s.preAuth >= maxPreAuthConns {
		s.mu.Unlock()
		return nil, "pre-auth pool full"
	}
	if s.preAuthIP[ip] >= maxPreAuthPerIP {
		s.mu.Unlock()
		return nil, "too many unauthenticated connections from one address"
	}
	s.preAuth++
	s.preAuthIP[ip]++
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.preAuth--
			if s.preAuthIP[ip]--; s.preAuthIP[ip] <= 0 {
				delete(s.preAuthIP, ip)
			}
		})
	}, ""
}

// addConn records a connection from the moment it is served, so closeConns
// reaches one still in its handshake. Bookkeeping, not admission control.
func (s *Server) addConn(c *conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.connSeq++
	c.seq = s.connSeq
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) removeConn(c *conn) {
	s.mu.Lock()
	delete(s.conns, c)
	if c.authed {
		s.authed--
		c.authed = false
	}
	s.mu.Unlock()
}

// admitAuthed moves a connection that just authenticated into the authed pool
// as peer. At maxConnsPerDevice the device's OLDEST connection is shut down;
// maxAuthedConns can only be reached by paired devices, and a connection past
// it is refused. It also publishes the peer under mu, which is what DropDevice
// and DeviceConns read.
func (s *Server) admitAuthed(c *conn, peer Peer) bool {
	var evict *conn
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	if s.authed >= maxAuthedConns {
		s.mu.Unlock()
		s.opts.logf()("remote: device=%s refused: %d authenticated connections already open", logSafe(peer.DeviceID), maxAuthedConns)
		return false
	}
	n := 0
	for o := range s.conns {
		if o != c && o.authed && o.peer.DeviceID == peer.DeviceID {
			n++
			if evict == nil || o.seq < evict.seq {
				evict = o
			}
		}
	}
	if n < maxConnsPerDevice {
		evict = nil
	}
	c.peer = peer
	c.authed = true
	s.authed++
	sweep := s.opts.now().Sub(s.lastSweep) >= limiterSweepEvery
	if sweep {
		s.lastSweep = s.opts.now()
	}
	s.mu.Unlock()
	if evict != nil {
		s.opts.logf()("remote: device=%s evicted oldest connection", logSafe(peer.DeviceID))
		evict.shutdown()
	}
	if sweep {
		s.lim.sweep(func(id string) bool { return s.DeviceConns(id) > 0 })
	}
	return true
}

// serveConn completes the TLS handshake under a deadline and runs the frame
// loop. release frees the connection's PRE-AUTH slot: conn.run calls it right
// after Authenticate succeeds (the connection then holds an authed slot), and
// the deferred call here covers every other way out. Every goroutine this
// package starts is panic-guarded: a panic in a frame handler must cost one
// connection, never the daemon.
func (s *Server) serveConn(nc net.Conn, release func()) {
	defer s.wg.Done()
	var once sync.Once
	rel := func() { once.Do(release) }
	defer rel()
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
			// The error text is not sensitive, but nothing from the handshake is
			// echoed back. A refused unpaired certificate has its own throttled
			// line, so it is not logged a second time here.
			if !errors.Is(err, errUnpaired) {
				s.logPreAuth("remote: handshake from %s failed: %v", logSafe(nc.RemoteAddr().String()), err)
			}
			return
		}
	}

	c := newConn(s, nc)
	c.releasePre = rel
	if !s.addConn(c) {
		return
	}
	defer s.removeConn(c)
	c.run()
}

// logPreAuth writes a line an unauthenticated peer caused, through the global
// pre-auth bucket; suppressed lines are counted and reported with the next one.
func (s *Server) logPreAuth(format string, args ...any) {
	s.preAuthLog.printf(s.opts.now(), s.opts.logf(), format, args...)
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
