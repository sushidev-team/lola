// Package devforward publishes ONE loopback dev server on the local network,
// for as long as somebody is looking at it.
//
// WHY IT EXISTS. A session's dev servers announce themselves — internal/devurl
// reads the addresses the dev tabs print — and those addresses are useless from
// a phone: `php artisan serve` binds 127.0.0.1 and vite binds [::1], so no
// address typed into a phone browser can reach them. The alternative is telling
// every project to bind 0.0.0.0, which publishes a half-built application, with
// whatever auth it has so far and an agent actively writing to it, to every
// peer on a network the laptop did not choose, permanently, on a well-known
// port.
//
// WHAT THIS IS INSTEAD. A listener on ONE private address, on a RANDOM high
// port, that exists only while the session it belongs to is the active one, and
// that can reach exactly one loopback address. The exposure is real and this
// package does not pretend otherwise — anything on that network can reach the
// dev server while the forward is up — but it is narrow, unguessable in
// practice, and it ends by itself.
//
// WHAT IT IS NOT. It is not the tunnel mobile/PLAN.md specifies as M7. That one
// carries the same bytes over the phone's already-authenticated connection and
// exposes nothing at all; it costs a stream multiplexer, a local proxy in the
// Swift plugin and a new frame vocabulary mirrored in three languages. This is
// the ninety per cent that ships first, and the UI it needs is the UI that one
// needs, so it is a step toward it rather than a detour.
//
// THE TARGET IS THE CALLER'S PROBLEM, NOT THIS PACKAGE'S. Open takes a target
// and dials it; it refuses anything that is not loopback, but it cannot know
// which loopback addresses a phone is allowed to reach. The daemon matches the
// requested URL against the Session.DevURLs it derived itself, and that match
// is the rail — without it a client picks its own port and this becomes a proxy
// into everything on the Mac's loopback.
package devforward

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// dialTimeout bounds one connection to the dev server. It is loopback, so a
// dial that takes longer than this is a server that is gone rather than a slow
// network.
const dialTimeout = 5 * time.Second

// ErrNotLoopback is returned for a target outside 127.0.0.0/8 and ::1.
//
// A dev command that printed a LAN address is already reachable and needs no
// forward; anything else is a request to proxy some other machine, which this
// must never do — the whole containment argument is that a forward reaches one
// server on this host.
var ErrNotLoopback = errors.New("devforward: target is not a loopback address")

// Forward is one live listener. It stops when Close is called and not before.
type Forward struct {
	// Addr is where a browser goes: "192.168.20.3:39273".
	Addr string
	// Target is the loopback address being published.
	Target string

	ln     net.Listener
	closed chan struct{}
	once   sync.Once
	wg     sync.WaitGroup
}

// Open publishes target on bindHost, choosing a free high port.
//
// bindHost is a specific address rather than a wildcard, and that is deliberate:
// 0.0.0.0 would include the VPN, container-bridge and hotspot interfaces that
// internal/remote's bind rules exist to keep a listener off. The caller passes
// an address from that same filtered set, so a forward is reachable exactly
// where the phone already reaches the daemon.
//
// Port 0 asks the kernel for a free one. A random high port is not security —
// anything on the network can scan — but it does mean a forward is not sitting
// on the port every scanner tries first, and it lets several run at once.
func Open(bindHost, target string) (*Forward, error) {
	if err := checkLoopback(target); err != nil {
		return nil, err
	}
	if bindHost == "" {
		return nil, errors.New("devforward: no address to publish on")
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(bindHost, "0"))
	if err != nil {
		return nil, fmt.Errorf("devforward: listen on %s: %w", bindHost, err)
	}
	f := &Forward{
		Addr:   ln.Addr().String(),
		Target: target,
		ln:     ln,
		closed: make(chan struct{}),
	}
	f.wg.Add(1)
	go f.serve()
	return f, nil
}

// checkLoopback refuses a target that is not on this machine's loopback.
//
// The host is parsed as an IP rather than resolved: a NAME would turn this
// check into a DNS lookup whose answer can change between the check and the
// dial, which is the shape of every TOCTOU bug. internal/devurl only ever
// produces loopback literals, so nothing legitimate is lost.
func checkLoopback(target string) error {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return fmt.Errorf("devforward: %q is not host:port: %w", target, err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("devforward: %q has no usable port", target)
	}
	// "localhost" is accepted as the ONE name and NOT resolved here, so the dial
	// below is dual-stack: vite binds [::1] and php binds 127.0.0.1, and a
	// forward that had already collapsed the name to one family reached only
	// half of them. Resolving it here and dialing the literal would also be the
	// wrong shape — the check would be a lookup whose answer can change before
	// the dial. The guarantee is enforced on the CONNECTION instead, in pipe.
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("%w: %q", ErrNotLoopback, host)
	}
	return nil
}

// serve accepts until Close. Each connection is its own goroutine, because a
// browser opens several at once for one page and a serial accept loop would
// serialize a page load.
func (f *Forward) serve() {
	defer f.wg.Done()
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			select {
			case <-f.closed:
				return
			default:
			}
			// A transient accept error is not worth ending a forward a human is
			// using; a permanent one ends it on the next iteration when the
			// listener is closed.
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return
		}
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			f.pipe(conn)
		}()
	}
}

// pipe joins one accepted connection to a fresh connection to the target.
//
// RAW BYTES, in both directions, with no idea what they are. That is what makes
// a WebSocket work — vite's HMR is one, and an HTTP-aware proxy would have to
// special-case the upgrade — and it is also why nothing here is logged: the
// payload is somebody's application traffic.
func (f *Forward) pipe(client net.Conn) {
	defer client.Close()

	server, err := net.DialTimeout("tcp", f.Target, dialTimeout)
	if err != nil {
		// The dev server is gone. Closing the accepted connection is the honest
		// answer: the browser reports a refused connection, which is what it
		// would have got had it reached the server directly.
		return
	}
	defer server.Close()

	// THE LOOPBACK GUARANTEE, ON THE CONNECTION THAT WAS ACTUALLY MADE.
	//
	// checkLoopback passes "localhost" through without resolving it, so that the
	// dial above is dual-stack — vite binds [::1] and php binds 127.0.0.1, and a
	// name collapsed to one family reaches only half of them. That leaves the
	// theoretical hole of a hosts file pointing localhost somewhere else, and
	// this closes it against the peer that answered rather than against a name
	// that was checked earlier.
	if !remoteIsLoopback(server) {
		return
	}

	// Closed when EITHER direction ends, so a half-closed connection does not
	// leave the other goroutine parked on a read forever.
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(server, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, server); done <- struct{}{} }()

	select {
	case <-done:
	case <-f.closed:
	}
}

// Close stops the listener and waits for the connections it accepted.
//
// Idempotent: the daemon closes forwards on session change, on dev-tab change
// and on shutdown, and those overlap.
func (f *Forward) Close() error {
	var err error
	f.once.Do(func() {
		close(f.closed)
		err = f.ln.Close()
	})
	f.wg.Wait()
	return err
}

// remoteIsLoopback reports whether a connection actually landed on this host.
//
// The address of the PEER, not of the name that was dialed: it is the one fact
// that cannot be stale by the time the bytes flow.
func remoteIsLoopback(c net.Conn) bool {
	host, _, err := net.SplitHostPort(c.RemoteAddr().String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
