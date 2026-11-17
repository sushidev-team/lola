// Package relay is the hub's blind relay core: an in-memory registry of
// daemon control links and pending phone dials, the splice between two
// WebSocket legs, and the limiters around them. It has no store and no HTTP
// routing; the API layer owns authentication and calls into it. The hub never
// parses what a splice carries (inner TLS), it only moves and counts bytes.
package relay

import (
	"encoding/json"
	"errors"
	"net/netip"
	"sync"
	"time"

	"github.com/sushidev-team/lola/hubproto"
)

var (
	// ErrBusy: a cap or a daily quota refuses a new connection.
	ErrBusy = errors.New("relay: busy")
	// ErrOffline: the device has no control link.
	ErrOffline = errors.New("relay: daemon not connected")
)

// controlQueue bounds the messages queued toward one daemon. A daemon that
// does not read its control channel is broken, not slow.
const controlQueue = 16

// Hub is the relay registry. Build it with New; every method is safe for
// concurrent use.
type Hub struct {
	Now  func() time.Time
	Logf func(string, ...any)

	lim limits

	// Test seams: a bounded sleep that returns false once stop closed, and the
	// per-splice idle ticker.
	sleep    func(d time.Duration, stop <-chan struct{}) bool
	idleTick func() <-chan time.Time

	// mu guards the registry below. Lock order: mu, then acct.
	mu      sync.Mutex
	links   map[string]*link    // device id -> control link
	pending map[string]*Pending // conn id (kept until its splice ends)
	splices map[*splice]struct{}
	perRV   map[string]int // live + pending
	perDev  map[string]int
	perOrg  map[string]int
	total   int
	closed  bool

	// acct guards byte accounting: shaping debts and the UTC day counters.
	acct     sync.Mutex
	devRate  map[rateKey]*byteBucket
	orgRate  map[rateKey]*byteBucket
	dailyDev map[string]*dayCount
	dailyRV  map[string]*dayCount

	dialRV, dialIP       *keyedBuckets
	dialAuth, dialUnauth *bucket
}

type rateKey struct {
	id  string
	dir int
}

// New returns a Hub. A nil now is time.Now, a nil logf discards.
func New(now func() time.Time, logf func(string, ...any)) *Hub {
	if now == nil {
		now = time.Now
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	t := now()
	return &Hub{
		Now: now, Logf: logf,
		lim:      defaultLimits(),
		sleep:    realSleep,
		idleTick: func() <-chan time.Time { return time.After(time.Minute) },
		links:    map[string]*link{},
		pending:  map[string]*Pending{},
		splices:  map[*splice]struct{}{},
		perRV:    map[string]int{},
		perDev:   map[string]int{},
		perOrg:   map[string]int{},
		devRate:  map[rateKey]*byteBucket{},
		orgRate:  map[rateKey]*byteBucket{},
		dailyDev: map[string]*dayCount{},
		dailyRV:  map[string]*dayCount{},
		dialRV:   newKeyedBuckets(hubproto.RelayDialRatePerRV, hubproto.RelayDialBurstPerRV, maxKeyedBuckets),
		dialIP:   newKeyedBuckets(hubproto.RelayDialRatePerIP, hubproto.RelayDialBurstPerIP, maxKeyedBuckets),
		dialAuth: newBucket(hubproto.RelayDialRateAuth, hubproto.RelayDialBurstAuth, t),

		dialUnauth: newBucket(hubproto.RelayDialRateUnauth, hubproto.RelayDialBurstUnauth, t),
	}
}

func realSleep(d time.Duration, stop <-chan struct{}) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-stop:
		return false
	}
}

func (h *Hub) logf(format string, a ...any) {
	if h.Logf != nil {
		h.Logf(format, a...)
	}
}

// short is the first 8 characters of an id: all a log line may carry of one.
func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// ---- dial limiters ----

// AllowIP charges the client network's bucket (/64 IPv6, /32 IPv4).
func (h *Hub) AllowIP(p netip.Prefix) bool {
	key, ok := network(p)
	if !ok {
		return true
	}
	return h.dialIP.allow(key, h.Now())
}

// AllowUnauth charges the bucket of dials whose credential check failed, so
// failed probes never spend the budget valid phones use.
func (h *Hub) AllowUnauth() bool { return h.dialUnauth.allow(h.Now()) }

// AllowAuth charges the rendezvous id's own bucket, then the hub-wide bucket
// of authenticated dials. The order is the isolation: a dial its own rv's
// bucket refuses spends no shared token, so one credential hammering the
// relay throttles only itself. The rv token is given back when the hub-wide
// bucket refuses, so a busy hub does not also drain every phone's own budget.
func (h *Hub) AllowAuth(rv string) bool {
	now := h.Now()
	if !h.dialRV.allow(rv, now) {
		return false
	}
	if !h.dialAuth.allow(now) {
		h.dialRV.refund(rv)
		return false
	}
	return true
}

// ---- reservations ----

// Reserve takes one connection slot under the rv, device, org and hub caps and
// refuses (ErrBusy) a rendezvous id or device past its daily byte quota. The
// returned release runs once, whoever calls it.
func (h *Hub) Reserve(device, org, rv string) (release func(), err error) {
	now := h.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrBusy
	}
	h.acct.Lock()
	over := h.dailyDev[device].get(now) >= h.lim.dailyDevice || h.dailyRV[rv].get(now) >= h.lim.dailyRV
	h.acct.Unlock()
	if over ||
		h.perRV[rv] >= h.lim.perRV || h.perDev[device] >= h.lim.perDevice ||
		h.perOrg[org] >= h.lim.perOrg || h.total >= h.lim.total {
		return nil, ErrBusy
	}
	h.perRV[rv]++
	h.perDev[device]++
	h.perOrg[org]++
	h.total++
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			dec(h.perRV, rv)
			dec(h.perDev, device)
			dec(h.perOrg, org)
			h.total--
		})
	}, nil
}

func dec(m map[string]int, k string) {
	if m[k] <= 1 {
		delete(m, k)
		return
	}
	m[k]--
}

// ---- control links ----

// link is one daemon's control channel.
type link struct {
	h      *Hub
	device string
	org    string
	ws     *hubproto.WSConn
	out    chan []byte
	done   chan struct{} // closed when the link ended
	once   sync.Once
}

// AttachControl registers ws as the device's control link, replacing (bye
// "replaced", close 4000) any earlier one, and queues the hello. The returned
// run serves the link and returns when it ends; the caller runs it in the
// handler goroutine. Live splices never depend on the link.
func (h *Hub) AttachControl(device, org string, ws *hubproto.WSConn) (run func()) {
	l := &link{h: h, device: device, org: org, ws: ws,
		out: make(chan []byte, controlQueue), done: make(chan struct{})}
	hello, _ := json.Marshal(hubproto.RelayMsg{T: "hello", V: hubproto.RelayControlVersion,
		PingS: int(hubproto.RelayPingInterval / time.Second), Max: hubproto.RelayMaxConnsPerDevice})
	l.out <- hello

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return func() { _ = ws.CloseNow(hubproto.WSCloseGoingAway) }
	}
	old := h.links[device]
	h.links[device] = l
	h.mu.Unlock()
	if old != nil {
		go h.endLink(old, "replaced", hubproto.RelayCloseReplaced, false)
	}
	return func() { h.runLink(l) }
}

func (h *Hub) runLink(l *link) {
	go func() {
		for {
			select {
			case <-l.done:
				return
			case b := <-l.out:
				_ = l.ws.SetWriteDeadline(time.Now().Add(h.lim.writeStall))
				if err := l.ws.WriteMessage(b); err != nil {
					h.endLink(l, "", hubproto.WSClosePolicy, true)
					return
				}
			}
		}
	}()
	for {
		b, err := l.ws.ReadMessage(hubproto.RelayMaxControlMessage)
		if err != nil {
			break
		}
		var m hubproto.RelayMsg
		if json.Unmarshal(b, &m) != nil {
			h.endLink(l, "", hubproto.WSCloseProtocol, false)
			break
		}
		if m.Validate() != nil {
			h.endLink(l, "", hubproto.WSCloseProtocol, false)
			break
		}
		if m.T == "reject" {
			h.reject(l, m.Conn, m.Reason)
		}
		// Every other daemon message, known or not, is ignored.
	}
	h.endLink(l, "", hubproto.WSCloseNormal, false)
}

// reject hands a daemon's refusal to the waiting phone handler. Only the
// device that was offered the conn can refuse it.
func (h *Hub) reject(l *link, conn, reason string) {
	h.mu.Lock()
	p := h.pending[conn]
	h.mu.Unlock()
	if p == nil || p.link != l {
		return
	}
	select {
	case p.rejected <- reason:
	default:
	}
}

// send queues one message. A full queue ends the link (1008): the daemon is
// not reading.
func (l *link) send(m hubproto.RelayMsg) bool {
	b, err := json.Marshal(m)
	if err != nil {
		return false
	}
	select {
	case <-l.done:
		return false
	default:
	}
	select {
	case l.out <- b:
		return true
	default:
		go l.h.endLink(l, "", hubproto.WSClosePolicy, false)
		return false
	}
}

// endLink ends a link once: unregisters it, fails its waiting dials, says bye
// (best effort, bounded) and closes. now skips waiting for the peer's Close.
func (h *Hub) endLink(l *link, bye string, code uint16, now bool) {
	l.once.Do(func() {
		h.mu.Lock()
		if h.links[l.device] == l {
			delete(h.links, l.device)
		}
		for _, p := range h.pending {
			if p.link == l && p.state != pendingSpliced {
				p.fail()
			}
		}
		h.mu.Unlock()
		close(l.done)
		if bye != "" {
			b, _ := json.Marshal(hubproto.RelayMsg{T: "bye", Reason: bye})
			sent := make(chan struct{})
			go func() {
				defer close(sent)
				_ = l.ws.SetWriteDeadline(time.Now().Add(hubproto.WSControlWriteTimeout))
				_ = l.ws.WriteMessage(b)
			}()
			t := time.NewTimer(hubproto.WSControlWriteTimeout)
			select {
			case <-sent:
			case <-t.C:
			}
			t.Stop()
		}
		if now {
			_ = l.ws.CloseNow(code)
		} else {
			_ = l.ws.CloseWithCode(code)
		}
	})
}

// ---- revocation and shutdown ----

// Retain closes what a rendezvous replace-all just removed. kept is the set of
// rvs the device still owns. A removed paired entry loses its live splices
// (4001); a removed window entry (Pending.MarkWindow) keeps its splice, which
// carries the pairing connection. Pending dials of any removed rv fail.
func (h *Hub) Retain(device string, kept map[string]bool) {
	var cut []*splice
	h.mu.Lock()
	for _, p := range h.pending {
		if p.Device != device || kept[p.RV] || p.state == pendingSpliced {
			continue
		}
		p.fail()
	}
	for s := range h.splices {
		if s.p.Device == device && !kept[s.p.RV] && !s.p.window {
			cut = append(cut, s)
		}
	}
	h.mu.Unlock()
	for _, s := range cut {
		s.shutdown(hubproto.RelayCloseRevoked, false)
	}
}

// Recheck closes the control link and the splices of every device live
// reports as gone (revoked or deleted), 4001, and drops idle accounting.
func (h *Hub) Recheck(live func(device string) bool) {
	h.mu.Lock()
	devs := map[string]bool{}
	for d := range h.links {
		devs[d] = true
	}
	for s := range h.splices {
		devs[s.p.Device] = true
	}
	h.mu.Unlock()
	dead := map[string]bool{}
	for d := range devs {
		if !live(d) {
			dead[d] = true
		}
	}
	if len(dead) > 0 {
		var links []*link
		var cut []*splice
		h.mu.Lock()
		for d := range dead {
			if l := h.links[d]; l != nil {
				links = append(links, l)
			}
		}
		for s := range h.splices {
			if dead[s.p.Device] {
				cut = append(cut, s)
			}
		}
		h.mu.Unlock()
		for _, l := range links {
			go h.endLink(l, "revoked", hubproto.RelayCloseRevoked, false)
		}
		for _, s := range cut {
			s.shutdown(hubproto.RelayCloseRevoked, false)
		}
	}
	h.prune()
}

// prune drops accounting that can no longer matter: yesterday's day counters
// and rate debts of devices and orgs with nothing live.
func (h *Hub) prune() {
	now := h.Now()
	h.mu.Lock()
	h.acct.Lock()
	day := utcDay(now)
	for k, d := range h.dailyDev {
		if d.day != day {
			delete(h.dailyDev, k)
		}
	}
	for k, d := range h.dailyRV {
		if d.day != day {
			delete(h.dailyRV, k)
		}
	}
	for k, b := range h.devRate {
		if h.perDev[k.id] == 0 && b.wait(now) == 0 && b.debt == 0 {
			delete(h.devRate, k)
		}
	}
	for k, b := range h.orgRate {
		if h.perOrg[k.id] == 0 && b.wait(now) == 0 && b.debt == 0 {
			delete(h.orgRate, k)
		}
	}
	h.acct.Unlock()
	h.mu.Unlock()
}

// Shutdown says bye{shutdown} on every control link and closes every leg 1001
// without waiting for peers. The hub accepts nothing afterwards.
func (h *Hub) Shutdown() {
	h.mu.Lock()
	h.closed = true
	links := make([]*link, 0, len(h.links))
	for _, l := range h.links {
		links = append(links, l)
	}
	splices := make([]*splice, 0, len(h.splices))
	for s := range h.splices {
		splices = append(splices, s)
	}
	pend := make([]*Pending, 0, len(h.pending))
	for _, p := range h.pending {
		pend = append(pend, p)
	}
	h.mu.Unlock()

	var wg sync.WaitGroup
	for _, l := range links {
		wg.Add(1)
		go func() { defer wg.Done(); h.endLink(l, "shutdown", hubproto.WSCloseGoingAway, true) }()
	}
	for _, s := range splices {
		s.shutdown(hubproto.WSCloseGoingAway, true)
	}
	for _, p := range pend {
		p.fail()
		select {
		case ws := <-p.ready:
			_ = ws.CloseNow(hubproto.WSCloseGoingAway)
		default:
		}
	}
	wg.Wait()
}
