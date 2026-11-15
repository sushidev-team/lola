package relay

import (
	"sync"
	"time"

	"github.com/sushidev-team/lola/hubproto"
)

type pendingState uint8

const (
	pendingWaiting   pendingState = iota // offered; incoming sent
	pendingClaimed                       // the device's accept request passed Claim
	pendingSpliced                       // the phone handler took the leg
	pendingAbandoned                     // the phone handler left without a leg
)

// Byte directions of a splice.
const (
	dirUp   = 0 // phone -> daemon
	dirDown = 1 // daemon -> phone
)

// Pending is one phone dial the hub offered to a daemon:
//
//	Offer -> waiting --Claim--> claimed --Deliver--> (Take) -> spliced
//	   \________________ Abandon ________________/ -> abandoned
type Pending struct {
	Conn, Device, Org, RV string

	h        *Hub
	link     *link
	ready    chan *hubproto.WSConn // cap 1
	rejected chan string           // cap 1: the daemon's reject reason
	gone     chan struct{}         // closed when the offer can no longer be answered
	goneOnce sync.Once

	state   pendingState // guarded by Hub.mu
	expires time.Time
	window  bool // guarded by Hub.mu: the rv is a pairing window
	daemon  *hubproto.WSConn
	release func()
	relOnce sync.Once

	// Guarded by Hub.acct.
	spl       [2]*byteBucket
	bytes     [2]int64
	quotaOnce sync.Once
	quotaCh   chan struct{} // closed once a daily quota is exhausted
}

// Ready delivers the daemon's accept leg once Deliver ran.
func (p *Pending) Ready() <-chan *hubproto.WSConn { return p.ready }

// Rejected delivers the daemon's reject reason ("busy", "unknown", "off").
func (p *Pending) Rejected() <-chan string { return p.rejected }

// Gone is closed when the control link ended or a rendezvous removal failed the
// offer; the phone handler answers 503 offline.
func (p *Pending) Gone() <-chan struct{} { return p.gone }

// MarkWindow records that the rv is a pairing window entry, so removing it
// later fails pending dials but keeps a live splice (Retain).
func (p *Pending) MarkWindow() {
	p.h.mu.Lock()
	p.window = true
	p.h.mu.Unlock()
}

// OnPhoneFrame and OnDaemonFrame are the WSConfig.OnFrame hooks of the phone
// and the daemon leg: every frame read, any opcode, charged at wire size to
// the splice, device, org and daily counters of its direction. They never
// block.
func (p *Pending) OnPhoneFrame(wire int)  { p.h.charge(p, dirUp, wire) }
func (p *Pending) OnDaemonFrame(wire int) { p.h.charge(p, dirDown, wire) }

func (p *Pending) fail() { p.goneOnce.Do(func() { close(p.gone) }) }

func (p *Pending) doRelease() {
	if p.release != nil {
		p.relOnce.Do(p.release)
	}
}

// Offer creates the pending dial and sends incoming{conn, rv} to the device's
// control link. ErrOffline when it has none (or it cannot take the message);
// then nothing was registered and the caller still owns release.
func (h *Hub) Offer(device, org, rv string, release func()) (*Pending, error) {
	conn, err := hubproto.NewRelayID()
	if err != nil {
		return nil, err
	}
	now := h.Now()
	h.mu.Lock()
	l := h.links[device]
	if h.closed || l == nil {
		h.mu.Unlock()
		return nil, ErrOffline
	}
	p := &Pending{
		Conn: conn, Device: device, Org: org, RV: rv,
		h: h, link: l,
		ready:    make(chan *hubproto.WSConn, 1),
		rejected: make(chan string, 1),
		gone:     make(chan struct{}),
		quotaCh:  make(chan struct{}),
		expires:  now.Add(hubproto.RelayAcceptTimeout),
		release:  release,
	}
	p.spl[dirUp] = newByteBucket(h.lim.spliceRate, h.lim.spliceBurst, now)
	p.spl[dirDown] = newByteBucket(h.lim.spliceRate, h.lim.spliceBurst, now)
	h.pending[conn] = p
	h.mu.Unlock()
	if !l.send(hubproto.RelayMsg{T: "incoming", Conn: conn, RV: rv}) {
		h.mu.Lock()
		delete(h.pending, conn)
		h.mu.Unlock()
		return nil, ErrOffline
	}
	return p, nil
}

// Claim moves waiting -> claimed, only for an unexpired offer made to this
// very device. One answer (false) for unknown, expired, claimed, abandoned and
// another device's conn.
func (h *Hub) Claim(conn, device string) (*Pending, bool) {
	now := h.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.pending[conn]
	if p == nil || p.Device != device || p.state != pendingWaiting || !now.Before(p.expires) {
		return nil, false
	}
	p.state = pendingClaimed
	return p, true
}

// Deliver hands the daemon's accept leg to the phone handler. It reports false
// when the phone handler already left (abandoned) or the dial was not claimed;
// the caller then closes the leg itself.
func (h *Hub) Deliver(p *Pending, ws *hubproto.WSConn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p.state != pendingClaimed {
		return false
	}
	select {
	case p.ready <- ws:
		return true
	default:
		return false
	}
}

// Take is the phone handler's claim -> spliced step. ws is the leg it received
// from Ready; nil takes it from the channel if present. It reports false when
// the dial was abandoned meanwhile.
func (h *Hub) Take(p *Pending, ws *hubproto.WSConn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p.state != pendingClaimed {
		return false
	}
	if ws == nil {
		select {
		case ws = <-p.ready:
		default:
			return false
		}
	}
	p.daemon = ws
	p.state = pendingSpliced
	return true
}

// Abandon ends a dial that did not reach Take: timeout, reject, link end,
// client gone or a failed phone upgrade. A leg already delivered is closed
// 4004, a still-waiting offer is cancelled toward the daemon, and the
// reservation is released. Safe to call more than once.
func (h *Hub) Abandon(p *Pending) {
	h.mu.Lock()
	if p.state == pendingSpliced {
		h.mu.Unlock()
		return
	}
	was := p.state
	p.state = pendingAbandoned
	delete(h.pending, p.Conn)
	h.mu.Unlock()
	if was == pendingWaiting {
		p.link.send(hubproto.RelayMsg{T: "cancel", Conn: p.Conn})
	}
	select {
	case ws := <-p.ready:
		_ = ws.CloseNow(hubproto.RelayClosePeerGone)
	default:
	}
	p.doRelease()
}

// charge books wire bytes read from one leg.
func (h *Hub) charge(p *Pending, dir, wire int) {
	if wire <= 0 {
		return
	}
	now := h.Now()
	h.acct.Lock()
	p.spl[dir].charge(wire, now)
	h.rate(h.devRate, rateKey{p.Device, dir}, h.lim.deviceRate, now).charge(wire, now)
	h.rate(h.orgRate, rateKey{p.Org, dir}, h.lim.orgRate, now).charge(wire, now)
	p.bytes[dir] += int64(wire)
	dd := h.dailyDev[p.Device]
	if dd == nil {
		dd = &dayCount{}
		h.dailyDev[p.Device] = dd
	}
	dr := h.dailyRV[p.RV]
	if dr == nil {
		dr = &dayCount{}
		h.dailyRV[p.RV] = dr
	}
	over := dd.add(now, int64(wire)) >= h.lim.dailyDevice
	if dr.add(now, int64(wire)) >= h.lim.dailyRV {
		over = true
	}
	h.acct.Unlock()
	if over {
		p.quotaOnce.Do(func() { close(p.quotaCh) })
	}
}

// rate returns the bucket for k, creating it. Caller holds acct.
func (h *Hub) rate(m map[rateKey]*byteBucket, k rateKey, perSec float64, now time.Time) *byteBucket {
	b := m[k]
	if b == nil {
		b = newByteBucket(perSec, perSec, now)
		m[k] = b
	}
	return b
}

// shapeWait is how long the direction must pause before reading more.
func (h *Hub) shapeWait(p *Pending, dir int) time.Duration {
	now := h.Now()
	h.acct.Lock()
	defer h.acct.Unlock()
	w := p.spl[dir].wait(now)
	if b := h.devRate[rateKey{p.Device, dir}]; b != nil {
		w = max(w, b.wait(now))
	}
	if b := h.orgRate[rateKey{p.Org, dir}]; b != nil {
		w = max(w, b.wait(now))
	}
	return w
}
