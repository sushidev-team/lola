package remote

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/protocol"
)

// deviceAuthorizer is the production Authorizer: a connection is authenticated
// by the SPKI of its client certificate, looked up in the device registry, and a
// frame is allowed by the device's tiers, read live on every frame.
type deviceAuthorizer struct {
	reg     *devices.Registry
	members *devices.Registry
	gate    MemberGate
	pairer  *Pairer
	host    string
	now     func() time.Time
	logf    func(string, ...any)

	// rejLog throttles the refusal lines: they are written before the peer
	// proved anything, so any peer that reaches the port can cause them.
	rejLog *refusalLog
}

var _ Authorizer = (*deviceAuthorizer)(nil)

func newDeviceAuthorizer(reg *devices.Registry, p *Pairer, host string, now func() time.Time, logf func(string, ...any)) *deviceAuthorizer {
	if now == nil {
		now = time.Now
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	p.setHost(host)
	return &deviceAuthorizer{reg: reg, pairer: p, host: host, now: now, logf: logf, rejLog: newRefusalLog(now())}
}

// Authenticate identifies the peer from its certificate. The registry lookup is
// repeated here although verifyPeer did it: a revoke or a window expiry may land
// between the handshake and the first frame.
func (a *deviceAuthorizer) Authenticate(ctx context.Context, hs *Handshake) (Peer, error) {
	if hs.TLS == nil {
		return Peer{}, ErrUnauthenticated
	}
	if len(hs.TLS.PeerCertificates) != 1 {
		return Peer{}, a.notPaired(hs, "no single client certificate")
	}
	h := devices.HashSPKI(hs.TLS.PeerCertificates[0].RawSubjectPublicKeyInfo)
	if a.reg.Err() != nil {
		return Peer{}, a.notPaired(hs, "device registry is unreadable")
	}
	relay := hs.Via == ViaRelay
	if dev, ok := a.reg.Lookup(h); ok {
		if relay && (dev.Relay == nil || dev.Relay.RV != hs.RelayRV) {
			return Peer{}, a.rvMismatch(hs, dev.ID)
		}
		a.reg.Touch(dev.ID)
		return Peer{DeviceID: dev.ID, Label: dev.Name, Epoch: dev.Epoch, Via: via(hs)}, nil
	}
	if p, known, err := a.authenticateMember(hs, h); known {
		return p, err
	}
	if !a.pairer.pending() {
		return Peer{}, a.notPaired(hs, "not paired")
	}
	// A pairing candidate. A relayed one must come through the open window's rv.
	if relay && (hs.RelayRV == "" || hs.RelayRV != a.pairer.windowRV()) {
		return Peer{}, a.rvMismatch(hs, "")
	}
	return a.pairer.present(ctx, hs, h, sourceKey(hs))
}

// AuthorizeFrame re-reads the device on every frame. It has NO side effects (no
// strike, bucket or log): the event pump calls it before every pushed event.
func (a *deviceAuthorizer) AuthorizeFrame(_ context.Context, peer Peer, f *protocol.Frame) error {
	if peer.Member {
		// A member holds no tier; conn.admitMember applies the per-frame policy.
		if !a.memberRegistryLive(peer) {
			return ErrUnauthenticated
		}
		return nil
	}
	dev, ok := a.reg.Get(peer.DeviceID)
	if !ok || dev.Epoch != peer.Epoch {
		return ErrUnauthenticated
	}
	if !dev.Has(frameTier(f)) {
		return ErrDenied
	}
	return nil
}

// sourceKey keys the per-source candidate limit: a LAN peer's IPv4 address or
// IPv6 /64 (one host picks addresses inside its /64 at will), the rendezvous
// id for a relayed one (every relayed peer shares the hub's address).
func sourceKey(hs *Handshake) string {
	if hs.Via == ViaRelay {
		return "relay:" + hs.RelayRV
	}
	host, _, err := net.SplitHostPort(hs.RemoteAddr)
	if err != nil {
		host = hs.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is6() {
		if p, err := addr.Prefix(64); err == nil {
			return p.String()
		}
	}
	return addr.String()
}

// notPaired writes the best-effort not_paired refusal, logs the reason locally
// and returns ErrUnauthenticated. The reason never reaches the peer.
func (a *deviceAuthorizer) notPaired(hs *Handshake, reason string) error {
	a.rejLog.printf(a.now(), a.logf, "remote: rejected %s via=%s: %s", logSafe(hs.RemoteAddr), via(hs), reason)
	a.sendNotPaired(hs)
	return ErrUnauthenticated
}

func (a *deviceAuthorizer) rvMismatch(hs *Handshake, dev string) error {
	a.rejLog.printf(a.now(), a.logf, "remote: relay rv mismatch device=%s", logSafe(dev))
	a.sendNotPaired(hs)
	return ErrUnauthenticated
}

func (a *deviceAuthorizer) sendNotPaired(hs *Handshake) {
	if hs.Send == nil {
		return
	}
	f := protocol.ErrorFrame("", protocol.CodeNotPaired, "this device is not paired")
	_ = hs.Send(&f)
}
