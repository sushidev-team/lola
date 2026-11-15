package remote

import (
	"crypto/tls"
	"errors"
	"net"
	"sync"
)

// maxRelayConnections equals hubproto.RelayMaxConnsPerDevice and
// maxRelayConnsPerRV equals hubproto.RelayMaxConnsPerRV; a test pins both.
// remote does not import hubproto from production code, so the values are
// repeated, not referenced.
const (
	maxRelayConnections = 4
	maxRelayConnsPerRV  = 2
)

// ErrServerClosed is returned by ServeRelayed on a closed Server.
var ErrServerClosed = errors.New("remote: server closed")

// RelayMeta describes a spliced hub stream. RV is the rendezvous id the hub
// matched; the authorizer binds it to a device.
type RelayMeta struct{ RV string }

// RelaySlot is one taken slot of the relay pre-auth pool. Release is
// idempotent and safe on a nil slot.
type RelaySlot struct {
	s    *Server
	rv   string
	once sync.Once
}

// Release returns the slot to the pool.
func (sl *RelaySlot) Release() {
	if sl == nil || sl.s == nil {
		return
	}
	sl.once.Do(func() {
		s := sl.s
		s.mu.Lock()
		defer s.mu.Unlock()
		select {
		case <-s.relaySlots:
		default:
		}
		if s.relayPerRV[sl.rv]--; s.relayPerRV[sl.rv] <= 0 {
			delete(s.relayPerRV, sl.rv)
		}
	})
}

// ReserveRelay takes one relay slot for rv. Relay slots are the "pre-auth
// (relay)" pool, SEPARATE from the LAN pre-auth pool, so a relay flood cannot
// lock a LAN phone out, nor the reverse; the per-rv count keeps one phone (or
// one stolen bearer) from taking the whole pool. The slot is released as soon
// as the connection authenticates (it then holds an authed slot) or ends.
func (s *Server) ReserveRelay(rv string) (*RelaySlot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.relayPerRV[rv] >= maxRelayConnsPerRV {
		return nil, false
	}
	select {
	case s.relaySlots <- struct{}{}:
	default:
		return nil, false
	}
	s.relayPerRV[rv]++
	return &RelaySlot{s: s, rv: rv}, true
}

// ServeRelayed runs nc (a spliced relay stream) through the same TLS handshake
// and frame loop as an accepted socket. It owns nc and the slot from here on.
func (s *Server) ServeRelayed(nc net.Conn, meta RelayMeta, slot *RelaySlot) error {
	s.mu.Lock()
	if s.closed || s.tlsConfig == nil {
		s.mu.Unlock()
		nc.Close()
		slot.Release()
		return ErrServerClosed
	}
	// Under the same lock Close sets closed with, so no Add can race its Wait.
	s.wg.Add(1)
	s.mu.Unlock()

	tc := tls.Server(relayNetConn{Conn: nc, rv: meta.RV, s: s}, s.tlsConfig)
	go s.serveConn(tc, slot.Release)
	return nil
}

// relayNetConn is the net.Conn under a relayed connection's TLS layer. It
// implements relayed, so conn.authenticate marks the connection ViaRelay.
type relayNetConn struct {
	net.Conn
	rv string
	s  *Server
}

func (c relayNetConn) relayRV() string { return c.rv }

func (c relayNetConn) RemoteAddr() net.Addr { return relayAddr{} }

// Close ends the stream. While the server is closing a stream that can say so
// sends a going-away close (1001) instead of a bare TCP shutdown.
func (c relayNetConn) Close() error {
	if c.s != nil && c.s.ctx.Err() != nil {
		if cn, ok := c.Conn.(interface{ CloseNow(uint16) error }); ok {
			return cn.CloseNow(1001)
		}
	}
	return c.Conn.Close()
}

type relayAddr struct{}

func (relayAddr) Network() string { return "relay" }
func (relayAddr) String() string  { return "relay" }
