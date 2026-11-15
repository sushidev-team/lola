package relay

import (
	"encoding/json"
	"io"
	"net"
	"net/netip"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// pipeWS returns the hub-side (server role) and far-side (client role) ends of
// an in-memory WebSocket.
func pipeWS(srv hubproto.WSConfig) (hub, far *hubproto.WSConn) {
	a, b := net.Pipe()
	return hubproto.NewWSConn(a, nil, hubproto.WSServer, srv), hubproto.NewWSConn(b, nil, hubproto.WSClient, hubproto.WSConfig{})
}

type rig struct {
	t   *testing.T
	h   *Hub
	clk *fakeClock
	n   int // splices started
}

func newRig(t *testing.T) *rig {
	clk := newClock()
	h := New(clk.Now, func(string, ...any) {})
	h.sleep = func(d time.Duration, stop <-chan struct{}) bool {
		select {
		case <-stop:
			return false
		default:
		}
		clk.Advance(d)
		runtime.Gosched()
		return true
	}
	t.Cleanup(h.Shutdown)
	return &rig{t: t, h: h, clk: clk}
}

// control attaches a control link and returns the far end's messages.
func (r *rig) control(device, org string) (far *hubproto.WSConn, msgs chan hubproto.RelayMsg) {
	hub, far := pipeWS(hubproto.WSConfig{})
	run := r.h.AttachControl(device, org, hub)
	go run()
	msgs = make(chan hubproto.RelayMsg, 32)
	go func() {
		defer close(msgs)
		for {
			b, err := far.ReadMessage(hubproto.RelayMaxControlMessage)
			if err != nil {
				return
			}
			var m hubproto.RelayMsg
			if json.Unmarshal(b, &m) == nil {
				msgs <- m
			}
		}
	}()
	return far, msgs
}

type legs struct {
	p      *Pending
	phone  *hubproto.WSConn // far end of the phone leg
	daemon *hubproto.WSConn // far end of the daemon leg
	done   chan struct{}    // closed when Splice returned
	rel    *atomic.Int32
}

func newID(t *testing.T) string {
	id, err := hubproto.NewRelayID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// splice reserves, offers, claims, delivers, takes and starts Splice.
func (r *rig) splice(device, org, rv string) *legs {
	r.t.Helper()
	r.h.mu.Lock()
	has := r.h.links[device] != nil
	r.h.mu.Unlock()
	if !has {
		r.control(device, org)
	}
	var rel atomic.Int32
	release, err := r.h.Reserve(device, org, rv)
	if err != nil {
		r.t.Fatal(err)
	}
	p, err := r.h.Offer(device, org, rv, func() { rel.Add(1); release() })
	if err != nil {
		r.t.Fatal(err)
	}
	if _, ok := r.h.Claim(p.Conn, device); !ok {
		r.t.Fatal("claim refused")
	}
	dHub, dFar := pipeWS(hubproto.WSConfig{OnFrame: p.OnDaemonFrame})
	if !r.h.Deliver(p, dHub) {
		r.t.Fatal("deliver refused")
	}
	if !r.h.Take(p, <-p.Ready()) {
		r.t.Fatal("take refused")
	}
	pHub, pFar := pipeWS(hubproto.WSConfig{OnFrame: p.OnPhoneFrame})
	l := &legs{p: p, phone: pFar, daemon: dFar, done: make(chan struct{}), rel: &rel}
	go func() { r.h.Splice(p, pHub); close(l.done) }()
	// Splice registers itself asynchronously: wait, so revocation sees it.
	for i := 0; i < 2000; i++ {
		r.h.mu.Lock()
		n := len(r.h.splices)
		r.h.mu.Unlock()
		if n > r.n {
			break
		}
		time.Sleep(time.Millisecond)
	}
	r.n++
	return l
}

func waitDone(t *testing.T, ch <-chan struct{}, d time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(d):
		t.Fatalf("timeout: %s", what)
	}
}

// closeCode reads ws to its end and returns the peer's close code.
func closeCode(t *testing.T, ws *hubproto.WSConn) uint16 {
	t.Helper()
	ch := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, ws); close(ch) }()
	waitDone(t, ch, 5*time.Second, "far end never saw a close")
	c, ok := ws.PeerCloseCode()
	if !ok {
		t.Fatal("no close code")
	}
	return c
}

func TestCaps(t *testing.T) {
	r := newRig(t)
	h := r.h
	a, b := newID(t), newID(t)

	// per rv
	var rels []func()
	for i := 0; i < hubproto.RelayMaxConnsPerRV; i++ {
		rel, err := h.Reserve("dev", "org", a)
		if err != nil {
			t.Fatal(err)
		}
		rels = append(rels, rel)
	}
	if _, err := h.Reserve("dev", "org", a); err != ErrBusy {
		t.Fatalf("per-rv cap: %v", err)
	}
	// release runs once however often it is called.
	rels[0]()
	rels[0]()
	if _, err := h.Reserve("dev", "org", a); err != nil {
		t.Fatalf("freed slot refused: %v", err)
	}
	if _, err := h.Reserve("dev", "org", a); err != ErrBusy {
		t.Fatal("double release freed two slots")
	}

	// per device (2 used by rv a); two more under another rv, then full.
	if _, err := h.Reserve("dev", "org", b); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Reserve("dev", "org", b); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Reserve("dev", "org", newID(t)); err != ErrBusy {
		t.Fatalf("per-device cap: %v", err)
	}

	// per org
	h.lim.perOrg = 3
	for i := 0; i < 3; i++ {
		if _, err := h.Reserve("d"+string(rune('a'+i)), "org2", newID(t)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Reserve("dz", "org2", newID(t)); err != ErrBusy {
		t.Fatalf("per-org cap: %v", err)
	}

	// hub wide
	h.lim.perOrg = 1000
	h.lim.total = h.total
	if _, err := h.Reserve("other", "org3", newID(t)); err != ErrBusy {
		t.Fatalf("hub cap: %v", err)
	}
}

func TestDialBuckets(t *testing.T) {
	r := newRig(t)
	h := r.h
	rv := newID(t)
	for i := 0; i < hubproto.RelayDialBurstPerRV; i++ {
		if !h.AllowAuth(rv) {
			t.Fatalf("burst dial %d refused", i)
		}
	}
	if h.AllowAuth(rv) {
		t.Fatal("per-rv bucket did not run dry")
	}
	if !h.AllowAuth(newID(t)) {
		t.Fatal("another rv shares the per-rv bucket")
	}
	r.clk.Advance(6 * time.Second) // 0.2/s: one token
	if !h.AllowAuth(rv) {
		t.Fatal("per-rv bucket did not refill")
	}

	// An invalid-credential flood empties the unauth bucket only.
	n := 0
	for h.AllowUnauth() {
		if n++; n > 1000 {
			t.Fatal("unauth bucket never empties")
		}
	}
	if n != hubproto.RelayDialBurstUnauth {
		t.Fatalf("unauth burst = %d", n)
	}
	if !h.AllowAuth(newID(t)) {
		t.Fatal("a valid phone was refused after an unauth flood")
	}

	// The auth bucket is hub wide.
	h2 := New(newClock().Now, nil)
	n = 0
	for h2.AllowAuth(newID(t)) {
		if n++; n > 1000 {
			t.Fatal("auth bucket never empties")
		}
	}
	if n != hubproto.RelayDialBurstAuth {
		t.Fatalf("auth burst = %d", n)
	}

	// One rv flooding past its own bucket spends no hub-wide token: every
	// other rendezvous id is still admitted.
	h4 := New(newClock().Now, nil)
	flood := newID(t)
	for i := 0; i < 10*hubproto.RelayDialBurstAuth; i++ {
		h4.AllowAuth(flood)
	}
	for i := 0; i < hubproto.RelayDialBurstAuth-hubproto.RelayDialBurstPerRV; i++ {
		if !h4.AllowAuth(newID(t)) {
			t.Fatalf("dial %d of another rv refused after one rv flooded", i)
		}
	}

	// A dial the hub-wide bucket refuses gives the rv its token back.
	h5 := New(newClock().Now, nil)
	for h5.AllowAuth(newID(t)) {
	}
	quiet := newID(t)
	for i := 0; i < hubproto.RelayDialBurstPerRV+2; i++ {
		h5.AllowAuth(quiet) // all refused by the empty hub-wide bucket
	}
	h5.dialAuth.tokens = hubproto.RelayDialBurstAuth
	for i := 0; i < hubproto.RelayDialBurstPerRV; i++ {
		if !h5.AllowAuth(quiet) {
			t.Fatalf("rv lost token %d to dials the hub-wide bucket refused", i)
		}
	}

	// Per network: /64 for IPv6, /32 for IPv4.
	h3 := New(newClock().Now, nil)
	for i := 0; i < hubproto.RelayDialBurstPerIP; i++ {
		a := netip.MustParseAddr("2001:db8:1:2::1").As16()
		a[15] = byte(i + 1) // rotating inside one /64
		if !h3.AllowIP(netip.PrefixFrom(netip.AddrFrom16(a), 128)) {
			t.Fatalf("v6 burst %d refused", i)
		}
	}
	if h3.AllowIP(netip.MustParsePrefix("2001:db8:1:2:ffff::9/128")) {
		t.Fatal("addresses of one /64 do not share a bucket")
	}
	if !h3.AllowIP(netip.MustParsePrefix("2001:db8:1:3::1/128")) {
		t.Fatal("two /64s share a bucket")
	}
	for i := 0; i < hubproto.RelayDialBurstPerIP; i++ {
		if !h3.AllowIP(netip.MustParsePrefix("192.0.2.7/32")) {
			t.Fatal("v4 burst refused")
		}
	}
	if h3.AllowIP(netip.MustParsePrefix("192.0.2.7/32")) {
		t.Fatal("v4 /32 bucket did not run dry")
	}
	if !h3.AllowIP(netip.MustParsePrefix("192.0.2.8/32")) {
		t.Fatal("two v4 addresses share a bucket")
	}
}

func TestShaping(t *testing.T) {
	r := newRig(t)
	l := r.splice("dev", "org", newID(t))
	go io.Copy(io.Discard, l.phone) // daemon -> phone side stays drained
	var got atomic.Int64
	go func() {
		buf := make([]byte, 64<<10)
		for {
			n, err := l.daemon.Read(buf)
			got.Add(int64(n))
			if err != nil {
				return
			}
		}
	}()

	const total = 12 << 20
	start := r.clk.Now()
	chunk := make([]byte, 16<<10)
	for sent := 0; sent < total; sent += len(chunk) {
		if _, err := l.phone.Write(chunk); err != nil {
			t.Fatalf("write after %d bytes: %v", sent, err)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for got.Load() < total && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got.Load() != total {
		t.Fatalf("daemon got %d of %d bytes", got.Load(), total)
	}
	// (12 MiB - 4 MiB burst) at 2 MiB/s: about 4 s of shaping, never a close.
	if el := r.clk.Now().Sub(start); el < 3500*time.Millisecond {
		t.Fatalf("not shaped: only %s of delay", el)
	}
	select {
	case <-l.done:
		t.Fatal("shaping closed the splice")
	default:
	}
	if _, ok := l.phone.PeerCloseCode(); ok {
		t.Fatal("shaping closed a leg")
	}

	// Pings, empty frames and every other opcode are charged like data.
	r.h.acct.Lock()
	before := l.p.bytes[dirUp]
	r.h.acct.Unlock()
	l.p.OnPhoneFrame(2)
	l.p.OnPhoneFrame(6)
	r.h.acct.Lock()
	after, dev := l.p.bytes[dirUp], r.h.dailyDev["dev"].n
	r.h.acct.Unlock()
	if after != before+8 || dev < after {
		t.Fatalf("control frames not charged: %d -> %d, daily %d", before, after, dev)
	}
	r.h.Shutdown()
	waitDone(t, l.done, 5*time.Second, "splice after shutdown")
}

func TestSpliceNonReadingPeerCloses(t *testing.T) {
	r := newRig(t)
	r.h.lim.writeStall = 200 * time.Millisecond
	l := r.splice("dev", "org", newID(t))
	// The daemon far end never reads; the phone keeps sending.
	go func() {
		chunk := make([]byte, 16<<10)
		for {
			if _, err := l.phone.Write(chunk); err != nil {
				return
			}
		}
	}()
	waitDone(t, l.done, 8*time.Second, "splice with a non-reading peer")
	if l.rel.Load() != 1 {
		t.Fatalf("release ran %d times", l.rel.Load())
	}
}

func TestIdleClose(t *testing.T) {
	r := newRig(t)
	tick := make(chan time.Time)
	r.h.idleTick = func() <-chan time.Time { return tick }
	l := r.splice("dev", "org", newID(t))
	got := make(chan int, 8)
	go func() {
		buf := make([]byte, 64)
		for {
			n, err := l.daemon.Read(buf)
			if n > 0 {
				got <- n
			}
			if err != nil {
				return
			}
		}
	}()
	pcode := make(chan uint16, 1)
	go func() { pcode <- closeCode(t, l.phone) }()

	fire := func() { tick <- time.Time{}; tick <- time.Time{} } // second send: first was handled

	r.clk.Advance(29 * time.Minute)
	fire()
	if _, err := l.phone.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("payload not delivered")
	}
	// Payload reset the idle clock: 29 more minutes is still fine.
	r.clk.Advance(29 * time.Minute)
	fire()
	select {
	case <-l.done:
		t.Fatal("closed although payload moved 29 minutes ago")
	default:
	}
	r.clk.Advance(2 * time.Minute)
	tick <- time.Time{}
	waitDone(t, l.done, 5*time.Second, "idle close")
	if c := <-pcode; c != hubproto.RelayCloseIdle {
		t.Fatalf("phone saw close %d, want 4002", c)
	}
	if c, _ := l.daemon.PeerCloseCode(); c != hubproto.RelayCloseIdle {
		t.Fatalf("daemon saw close %d, want 4002", c)
	}
}

func TestDailyQuota(t *testing.T) {
	for _, which := range []string{"device", "rv"} {
		t.Run(which, func(t *testing.T) {
			r := newRig(t)
			if which == "device" {
				r.h.lim.dailyDevice = 1000
			} else {
				r.h.lim.dailyRV = 1000
			}
			rv := newID(t)
			l := r.splice("dev", "org", rv)
			pc := make(chan uint16, 1)
			go func() { pc <- closeCode(t, l.phone) }()
			go func() { _ = closeCode(t, l.daemon) }()
			l.p.OnPhoneFrame(1500)
			waitDone(t, l.done, 5*time.Second, "quota close")
			if c := <-pc; c != hubproto.RelayCloseQuota {
				t.Fatalf("close %d, want 4003", c)
			}
			if _, err := r.h.Reserve("dev", "org", rv); err != ErrBusy {
				t.Fatalf("Reserve after quota: %v", err)
			}
			if which == "rv" {
				if rel, err := r.h.Reserve("dev", "org", newID(t)); err != nil {
					t.Fatalf("another rv refused after an rv quota: %v", err)
				} else {
					rel()
				}
			}
			r.clk.Advance(24 * time.Hour)
			rel, err := r.h.Reserve("dev", "org", rv)
			if err != nil {
				t.Fatalf("Reserve next UTC day: %v", err)
			}
			rel()
		})
	}
}

func TestHalfClose(t *testing.T) {
	r := newRig(t)
	l := r.splice("dev", "org", newID(t))
	dc := make(chan uint16, 1)
	go func() { dc <- closeCode(t, l.daemon) }()
	start := time.Now()
	if err := l.phone.CloseWithCode(hubproto.WSCloseNormal); err != nil {
		t.Fatal(err)
	}
	waitDone(t, l.done, hubproto.RelayHalfCloseGrace+3*time.Second, "half close")
	if el := time.Since(start); el > hubproto.RelayHalfCloseGrace+time.Second {
		t.Fatalf("took %s", el)
	}
	if c := <-dc; c != hubproto.WSCloseNormal {
		t.Fatalf("daemon saw close %d", c)
	}
	if l.rel.Load() != 1 {
		t.Fatalf("release ran %d times", l.rel.Load())
	}
}

func TestAbandonDrainsReadyLeg(t *testing.T) {
	r := newRig(t)
	_, msgs := r.control("dev", "org")
	var rel atomic.Int32
	release, err := r.h.Reserve("dev", "org", newID(t))
	if err != nil {
		t.Fatal(err)
	}
	rv := newID(t)
	p, err := r.h.Offer("dev", "org", rv, func() { rel.Add(1); release() })
	if err != nil {
		t.Fatal(err)
	}
	if m := <-msgs; m.T != "hello" {
		t.Fatalf("first message %q", m.T)
	}
	if m := <-msgs; m.T != "incoming" || m.Conn != p.Conn || m.RV != rv {
		t.Fatalf("incoming = %+v", m)
	}
	if _, ok := r.h.Claim(p.Conn, "someone-else"); ok {
		t.Fatal("another device claimed the conn")
	}
	if _, ok := r.h.Claim(p.Conn, "dev"); !ok {
		t.Fatal("claim refused")
	}
	if _, ok := r.h.Claim(p.Conn, "dev"); ok {
		t.Fatal("claimed twice")
	}
	hub, far := pipeWS(hubproto.WSConfig{})
	cc := make(chan uint16, 1)
	go func() { cc <- closeCode(t, far) }()
	if !r.h.Deliver(p, hub) {
		t.Fatal("deliver refused")
	}
	r.h.Abandon(p)
	if c := <-cc; c != hubproto.RelayClosePeerGone {
		t.Fatalf("leg closed %d, want 4004", c)
	}
	if rel.Load() != 1 {
		t.Fatalf("release ran %d times", rel.Load())
	}
	r.h.Abandon(p)
	if rel.Load() != 1 {
		t.Fatal("second Abandon released again")
	}
	// A leg arriving after the phone left is refused; the slot is free.
	hub2, _ := pipeWS(hubproto.WSConfig{})
	if r.h.Deliver(p, hub2) {
		t.Fatal("Deliver after Abandon succeeded")
	}
	if _, ok := r.h.Claim(p.Conn, "dev"); ok {
		t.Fatal("claim after Abandon succeeded")
	}
	r.h.mu.Lock()
	n := r.h.total
	r.h.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d slots leaked", n)
	}
}

func TestOfferOfflineAndCancel(t *testing.T) {
	r := newRig(t)
	if _, err := r.h.Offer("dev", "org", newID(t), nil); err != ErrOffline {
		t.Fatalf("offer without a link: %v", err)
	}
	_, msgs := r.control("dev", "org")
	p, err := r.h.Offer("dev", "org", newID(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	r.h.Abandon(p) // still waiting: the daemon is told not to dial
	var sawCancel bool
	for m := range msgs {
		if m.T == "cancel" && m.Conn == p.Conn {
			sawCancel = true
			break
		}
	}
	if !sawCancel {
		t.Fatal("no cancel")
	}
	if _, ok := r.h.Claim(p.Conn, "dev"); ok {
		t.Fatal("abandoned offer claimable")
	}
}

func TestControlReplacedFailsPending(t *testing.T) {
	r := newRig(t)
	far1, msgs1 := r.control("dev", "org")
	p, err := r.h.Offer("dev", "org", newID(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = r.control("dev", "org")
	select {
	case <-p.Gone():
	case <-time.After(3 * time.Second):
		t.Fatal("pending not failed by replacement")
	}
	var bye string
	for m := range msgs1 {
		if m.T == "bye" {
			bye = m.Reason
		}
	}
	if bye != "replaced" {
		t.Fatalf("bye = %q", bye)
	}
	if c, _ := far1.PeerCloseCode(); c != hubproto.RelayCloseReplaced {
		t.Fatalf("close %d, want 4000", c)
	}
}

func TestRejectReachesWaiter(t *testing.T) {
	r := newRig(t)
	far, _ := r.control("dev", "org")
	p, err := r.h.Offer("dev", "org", newID(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(hubproto.RelayMsg{T: "reject", Conn: p.Conn, Reason: "busy"})
	if err := far.WriteMessage(b); err != nil {
		t.Fatal(err)
	}
	select {
	case reason := <-p.Rejected():
		if reason != "busy" {
			t.Fatalf("reason %q", reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reject not delivered")
	}
}

func TestRetainAndRecheck(t *testing.T) {
	r := newRig(t)
	paired, window := newID(t), newID(t)
	lp := r.splice("dev", "org", paired)
	lw := r.splice("dev", "org", window)
	lw.p.MarkWindow()
	for _, l := range []*legs{lp, lw} {
		go io.Copy(io.Discard, l.phone)
		go io.Copy(io.Discard, l.daemon)
	}
	r.h.Retain("dev", map[string]bool{})
	waitDone(t, lp.done, 5*time.Second, "paired splice must close")
	select {
	case <-lw.done:
		t.Fatal("window splice was cut")
	case <-time.After(100 * time.Millisecond):
	}
	r.h.Recheck(func(string) bool { return true })
	select {
	case <-lw.done:
		t.Fatal("live device was cut")
	case <-time.After(100 * time.Millisecond):
	}
	r.h.Recheck(func(string) bool { return false })
	waitDone(t, lw.done, 5*time.Second, "revoked device splice must close")
	if c, _ := lw.daemon.PeerCloseCode(); c != hubproto.RelayCloseRevoked {
		t.Fatalf("close %d, want 4001", c)
	}
}

func TestShutdownClosesLegs(t *testing.T) {
	r := newRig(t)
	far, _ := r.control("dev", "org")
	l := r.splice("dev", "org", newID(t))
	go io.Copy(io.Discard, l.phone)
	go io.Copy(io.Discard, l.daemon)
	start := time.Now()
	r.h.Shutdown()
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("Shutdown took %s", el)
	}
	waitDone(t, l.done, 5*time.Second, "splice after shutdown")
	if c, _ := l.daemon.PeerCloseCode(); c != hubproto.WSCloseGoingAway {
		t.Fatalf("close %d, want 1001", c)
	}
	_ = far
	if _, err := r.h.Reserve("dev", "org", newID(t)); err != ErrBusy {
		t.Fatalf("Reserve after Shutdown: %v", err)
	}
}
