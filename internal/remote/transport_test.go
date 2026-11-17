package remote

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// These tests drive the real listener over real TLS with generated client
// certificates (the first half) and the frame loop over a pipe (the second).

// syncClock is a settable clock for the rate limits.
type syncClock struct {
	mu sync.Mutex
	t  time.Time
}

func newSyncClock() *syncClock { return &syncClock{t: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)} }

func (c *syncClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *syncClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func withClock(c *syncClock) func(*rig, *Options) {
	return func(_ *rig, o *Options) { o.Now = c.now }
}

// selfSigned makes a self-signed certificate over any key.
func selfSigned(t *testing.T, priv crypto.Signer) tls.Certificate {
	t.Helper()
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "phone"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, priv.Public(), priv)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
}

// refusedAtHandshake asserts the server refused the certificate inside TLS: the
// client sees a TLS alert (never a frame), and nothing reached the handler.
func refusedAtHandshake(t *testing.T, l *lrig, c *client) {
	t.Helper()
	f, err := c.read()
	if err == nil {
		t.Fatalf("the server answered an unpaired certificate with a %q frame", f.Type)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatal("the connection was left open instead of refused")
	}
}

func TestUnknownCertRejectedAtHandshake(t *testing.T) {
	h := &stubHandler{}
	l := startTestListener(t, h, newFakeBus())
	c := dialCert(t, l.srv.Addrs()[0].Addr, newTestKey(t).Certificate)
	refusedAtHandshake(t, l, c)
	if h.count() != 0 {
		t.Error("a frame from an unpaired certificate reached the handler")
	}
	if !strings.Contains(l.log.all(), "refused unpaired certificate") {
		t.Errorf("the refusal left no line:\n%s", l.log.all())
	}
}

func TestNonP256CertRejected(t *testing.T) {
	l := startTestListener(t, &stubHandler{}, newFakeBus())
	_, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for name, key := range map[string]crypto.Signer{"ed25519": edPriv, "p384": p384} {
		// Even with a pairing window open: the algorithm check comes first.
		if _, err := l.pairer.Begin(); err != nil {
			t.Fatal(err)
		}
		c := dialCert(t, l.srv.Addrs()[0].Addr, selfSigned(t, key))
		refusedAtHandshake(t, l, c)
		_ = name
	}
}

func TestTwoCertChainRejected(t *testing.T) {
	l := startTestListener(t, &stubHandler{}, newFakeBus())
	// The paired phone's own certificate plus a second one: one self-signed leaf,
	// no chains.
	extra := selfSigned(t, mustP256(t))
	cert := l.phone.Certificate
	cert.Certificate = append(append([][]byte(nil), cert.Certificate...), extra.Certificate[0])
	c := dialCert(t, l.srv.Addrs()[0].Addr, cert)
	refusedAtHandshake(t, l, c)
}

func mustP256(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestPairedPhoneConnectsOverRealTLS(t *testing.T) {
	h := &stubHandler{}
	l := startTestListener(t, h, newFakeBus())
	c := l.dial()
	rd := c.wantReady()
	if rd.DeviceID != l.dev.ID || rd.Name != "Test phone" || rd.Via != ViaLAN {
		t.Errorf("ready = %+v", rd)
	}
	c.req("a", "sessions", nil)
	if f := c.mustRead(); f.Type != protocol.FrameResp || f.ID != "a" {
		t.Fatalf("got %+v", f)
	}
	if got, _ := l.reg.Get(l.dev.ID); got.LastSeenAt.IsZero() {
		t.Error("the registry was not touched")
	}
}

// TestReadyIsFirstFrame: TLS 1.3 reports success to a client before the server
// checks its certificate, so ready is the only proof, and it precedes every
// other frame the daemon writes.
func TestReadyIsFirstFrame(t *testing.T) {
	l := startTestListener(t, &stubHandler{}, newFakeBus())
	if _, err := l.reg.SetTiers(l.dev.ID, []string{protocol.TierRead, protocol.TierAnswer}); err != nil {
		t.Fatal(err)
	}
	c := l.dial()
	c.req("early", "sessions", nil) // sent before ready is read
	rd := c.wantReady()
	if len(rd.Tiers) != 2 || rd.Tiers[0] != protocol.TierRead || rd.Tiers[1] != protocol.TierAnswer {
		t.Errorf("ready tiers = %v", rd.Tiers)
	}
	if f := c.mustRead(); f.ID != "early" {
		t.Errorf("second frame = %+v", f)
	}
}

func TestRevokeClosesLiveConn(t *testing.T) {
	l := startTestListener(t, &stubHandler{}, newFakeBus())
	c := l.dial()
	c.wantReady()
	if n := l.srv.DeviceConns(l.dev.ID); n != 1 {
		t.Fatalf("DeviceConns = %d, want 1", n)
	}
	if _, err := l.reg.Revoke(l.dev.ID); err != nil {
		t.Fatal(err)
	}
	if n := l.srv.DropDevice(l.dev.ID); n != 1 {
		t.Errorf("DropDevice = %d, want 1", n)
	}
	c.wantClosed()
	// And the revoked certificate is refused at the next handshake.
	refusedAtHandshake(t, l, l.dial())
	waitFor(t, "the connection to leave the pool", func() bool { return l.srv.DeviceConns(l.dev.ID) == 0 })
}

// TestRepairBumpsEpochClosesOldConn: a re-pair replaces the entry under a new
// epoch, which the per-frame check reads: the old connection ends on its next
// frame without anything calling DropDevice.
func TestRepairBumpsEpochClosesOldConn(t *testing.T) {
	h := &stubHandler{}
	l := startTestListener(t, h, newFakeBus())
	c := l.dial()
	c.wantReady()
	if _, err := l.reg.Add("Renamed", "ios", l.phone.SPKISHA256(), protocol.DefaultTiers, nil); err != nil {
		t.Fatal(err)
	}
	c.req("x", "sessions", nil)
	c.wantClosed()
	if h.count() != 0 {
		t.Error("a frame from the superseded connection reached the handler")
	}
}

func TestTierDowngradeAppliesNextFrame(t *testing.T) {
	h := &stubHandler{}
	l := startTestListener(t, h, newFakeBus())
	c := l.dial()
	c.wantReady()
	c.req("a", "revive", protocol.Request{Session: "s1"})
	if f := c.mustRead(); f.Type != protocol.FrameResp {
		t.Fatalf("control before the downgrade: %+v", f)
	}
	if _, err := l.reg.SetTiers(l.dev.ID, []string{protocol.TierRead}); err != nil {
		t.Fatal(err)
	}
	c.req("b", "revive", protocol.Request{Session: "s1"})
	if f := c.mustRead(); f.Type != protocol.FrameErr || f.ID != "b" {
		t.Fatalf("control after the downgrade: %+v", f)
	}
	c.req("c", "sessions", nil)
	if f := c.mustRead(); f.Type != protocol.FrameResp {
		t.Fatalf("read after the downgrade: %+v", f)
	}
	if h.count() != 2 {
		t.Errorf("handler saw %d requests, want 2", h.count())
	}
}

// TestRefusalLogIsGloballyThrottled: one token bucket for the whole process, no
// per-address map, so cycling source addresses costs no memory.
func TestRefusalLogIsGloballyThrottled(t *testing.T) {
	clk := newSyncClock()
	l := startTestListener(t, &stubHandler{}, newFakeBus(), func(o *Options) { o.Now = clk.now })
	leaf, err := x509.ParseCertificate(newTestKey(t).Certificate.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}
	for i := 0; i < 10000; i++ {
		if err := l.srv.verifyPeer(cs); !errors.Is(err, errUnpaired) {
			t.Fatalf("verifyPeer = %v", err)
		}
	}
	lines := strings.Count(l.log.all(), "refused unpaired certificate")
	if lines < 1 || lines > int(refusalLogBurst) {
		t.Fatalf("%d lines for 10,000 refusals, want 1..%d", lines, int(refusalLogBurst))
	}
	clk.advance(5 * time.Second)
	_ = l.srv.verifyPeer(cs)
	if !strings.Contains(l.log.all(), "9990 similar lines suppressed") {
		t.Errorf("the suppressed count was not reported:\n%s", l.log.all())
	}
}

func TestRefusalKeepsConnection(t *testing.T) {
	l := startTestListener(t, &stubHandler{}, newFakeBus())
	c := l.dial()
	c.wantReady()
	c.req("n", "stop", nil)
	if f := c.mustRead(); f.Type != protocol.FrameErr || f.ID != "n" {
		t.Fatalf("got %+v", f)
	}
	c.req("ok", "sessions", nil)
	if f := c.mustRead(); f.Type != protocol.FrameResp || f.ID != "ok" {
		t.Fatalf("the connection did not survive a refusal: %+v", f)
	}
}

func TestStrikeLimitCloses(t *testing.T) {
	r := newRig(t)
	for i := 0; i < maxRefusalsPerConn; i++ {
		r.req("n", "stop", nil)
	}
	r.wantClosed()
	if !strings.Contains(r.log.all(), "refusals") {
		t.Errorf("the close was not logged:\n%s", r.log.all())
	}
	if r.handler.count() != 0 {
		t.Error("a refused command reached the handler")
	}
}

// TestMalformedPayloadCostsAStrike: a req whose payload does not decode reaches
// neither the handler nor a rate bucket, so the strike counter is what bounds a
// peer that repeats it.
func TestMalformedPayloadCostsAStrike(t *testing.T) {
	r := newRig(t)
	for i := 0; i < maxRefusalsPerConn; i++ {
		r.send(protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq,
			ID: "m", Cmd: "sessions", Payload: json.RawMessage(`"not an object"`)})
	}
	r.wantClosed()
	if r.handler.count() != 0 {
		t.Error("a malformed request reached the handler")
	}
}

// TestPreAuthLogIsGloballyThrottled: refused accepts and failed handshakes are
// lines an unauthenticated peer causes at will, so one global bucket bounds
// them and the suppressed count is reported with the next line that passes.
func TestPreAuthLogIsGloballyThrottled(t *testing.T) {
	clk := newSyncClock()
	l := startTestListener(t, &stubHandler{}, newFakeBus(), func(o *Options) { o.Now = clk.now })
	for i := 0; i < 10000; i++ {
		l.srv.logPreAuth("remote: handshake from %s failed: %v", "192.0.2.1:1", "boom")
	}
	lines := strings.Count(l.log.all(), "handshake from")
	if lines < 1 || lines > int(refusalLogBurst) {
		t.Fatalf("%d lines for 10,000 failures, want 1..%d", lines, int(refusalLogBurst))
	}
	clk.advance(5 * time.Second)
	l.srv.logPreAuth("remote: handshake from %s failed: %v", "192.0.2.1:1", "boom")
	if !strings.Contains(l.log.all(), "9990 similar lines suppressed") {
		t.Errorf("the suppressed count was not reported:\n%s", l.log.all())
	}
}

// TestRateLimitSharedAcrossDeviceConns: buckets are keyed by device, so a phone
// cannot double its allowance by opening a second connection.
func TestRateLimitSharedAcrossDeviceConns(t *testing.T) {
	clk := newSyncClock()
	l := startTestListener(t, &stubHandler{}, newFakeBus(), func(o *Options) { o.Now = clk.now })
	a, b := l.dial(), l.dial()
	a.wantReady()
	b.wantReady()
	for i := 0; i < int(writeBurst)/2; i++ {
		for _, c := range []*client{a, b} {
			c.req("w", "revive", protocol.Request{Session: "s1"})
			if f := c.mustRead(); f.Type != protocol.FrameResp {
				t.Fatalf("write %d: %+v", i, f)
			}
		}
	}
	a.req("over", "revive", protocol.Request{Session: "s1"})
	f := a.mustRead()
	var p protocol.ErrPayload
	if f.Type != protocol.FrameErr || f.DecodePayload(&p) != nil || p.Code != protocol.CodeRateLimited {
		t.Fatalf("the shared bucket did not run dry: %+v", f)
	}
	clk.advance(2 * time.Second)
	b.req("again", "revive", protocol.Request{Session: "s1"})
	if f := b.mustRead(); f.Type != protocol.FrameResp {
		t.Fatalf("the bucket did not refill: %+v", f)
	}
}

func TestMaxConnsPerDeviceEvictsOldest(t *testing.T) {
	l := startTestListener(t, &stubHandler{}, newFakeBus())
	var cs []*client
	for i := 0; i <= maxConnsPerDevice; i++ {
		c := l.dial()
		c.wantReady()
		cs = append(cs, c)
	}
	cs[0].wantClosed()
	for i, c := range cs[1:] {
		c.req("p", "sessions", nil)
		if f := c.mustRead(); f.Type != protocol.FrameResp {
			t.Errorf("conn %d did not survive: %+v", i+1, f)
		}
	}
	// The client sees the close before the server drops the connection from its count.
	waitFor(t, "the evicted connection to leave the count", func() bool { return l.srv.DeviceConns(l.dev.ID) == maxConnsPerDevice })
	if !strings.Contains(l.log.all(), "evicted oldest connection") {
		t.Error("the eviction was not logged")
	}
}

// TestUnauthenticatedFloodCannotLockOutPairedDevice: established phones are
// never displaced by strangers, and the per-IP cap refuses a third socket.
func TestUnauthenticatedFloodCannotLockOutPairedDevice(t *testing.T) {
	l := startTestListener(t, &stubHandler{}, newFakeBus())
	live := l.dial()
	live.wantReady()

	// Fill the whole pre-auth pool from distinct (fabricated) source addresses.
	var releases []func()
	for i := 0; i < maxPreAuthConns; i++ {
		rel, why := l.srv.admitPreAuth("10.0." + strconvI(i/maxPreAuthPerIP) + ".1")
		if rel == nil {
			t.Fatalf("slot %d refused early: %s", i, why)
		}
		releases = append(releases, rel)
	}
	if rel, _ := l.srv.admitPreAuth("10.9.9.9"); rel != nil {
		t.Fatal("a slot was granted past maxPreAuthConns")
	}

	// The paired phone's existing connection keeps working.
	live.req("still", "sessions", nil)
	if f := live.mustRead(); f.Type != protocol.FrameResp {
		t.Fatalf("the established connection was disturbed: %+v", f)
	}
	// A new socket is refused promptly, not parked.
	if c, err := net.DialTimeout("tcp", l.srv.Addrs()[0].Addr, time.Second); err == nil {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := c.Read(make([]byte, 1)); err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("an over-limit socket was served or parked: %v", err)
		}
		c.Close()
	}
	// Once a flooding socket goes away, a paired phone gets in.
	releases[0]()
	again := l.dial()
	again.wantReady()
}

func strconvI(i int) string {
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func TestPreAuthSlotMovesToAuthedPool(t *testing.T) {
	l := startTestListener(t, &stubHandler{}, newFakeBus())
	c := l.dial()
	c.wantReady()
	l.srv.mu.Lock()
	pre, authed := l.srv.preAuth, l.srv.authed
	l.srv.mu.Unlock()
	if pre != 0 || authed != 1 {
		t.Errorf("preAuth=%d authed=%d, want 0 and 1", pre, authed)
	}
	c.conn.Close()
	waitFor(t, "the authed slot to be released", func() bool {
		l.srv.mu.Lock()
		defer l.srv.mu.Unlock()
		return l.srv.authed == 0
	})
}

func TestPtyWriteNeedsShell(t *testing.T) {
	bus := newFakeBus()
	l := startTestListener(t, &stubHandler{}, bus)
	if _, err := l.reg.SetTiers(l.dev.ID, []string{protocol.TierRead, protocol.TierAnswer, protocol.TierControl}); err != nil {
		t.Fatal(err)
	}
	c := l.dial()
	c.wantReady()
	sub := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameSub, ID: "s", Pane: "lola-fe-1"}
	_ = sub.SetPayload(protocol.SubPayload{Cols: 80, Rows: 24})
	if err := c.fw.WriteFrame(&sub); err != nil {
		t.Fatal(err)
	}
	if f := c.mustRead(); f.Type != protocol.FrameResync {
		t.Fatalf("got %+v, want the resync", f)
	}
	w := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FramePTY, Pane: "lola-fe-1"}
	_ = w.SetPayload(protocol.PTYInputPayload{Action: protocol.PTYActionWrite, Data: []byte("rm -rf\n")})
	if err := c.fw.WriteFrame(&w); err != nil {
		t.Fatal(err)
	}
	f := c.mustRead()
	var p protocol.ErrPayload
	if f.Type != protocol.FrameErr || f.DecodePayload(&p) != nil || p.Code != protocol.CodeDenied {
		t.Fatalf("a pty write without the shell tier got %+v", f)
	}
	// Scrolling is a read and still works.
	sc := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FramePTY, Pane: "lola-fe-1"}
	_ = sc.SetPayload(protocol.PTYInputPayload{Action: protocol.PTYActionScroll, Lines: 3})
	if err := c.fw.WriteFrame(&sc); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the scroll to reach the bus", func() bool { _, _, sc := bus.snapshot(); return len(sc) == 1 })
	if _, writes, _ := bus.snapshot(); len(writes) != 0 {
		t.Errorf("bus saw writes %q", writes)
	}
}

func TestPtyBytesRateLimited(t *testing.T) {
	clk := newSyncClock()
	r := newRig(t, withClock(clk))
	r.sub("s", "lola-fe-1", 80, 24)
	r.wantResync("lola-fe-1")
	big := make([]byte, 200<<10)
	r.pty("lola-fe-1", protocol.PTYInputPayload{Action: protocol.PTYActionWrite, Data: big})
	r.pty("lola-fe-1", protocol.PTYInputPayload{Action: protocol.PTYActionWrite, Data: big})
	r.wantErr(protocol.CodeRateLimited)
	waitFor(t, "the first write", func() bool { _, w, _ := r.bus.snapshot(); return len(w) == 1 })
	if _, w, _ := r.bus.snapshot(); len(w) != 1 {
		t.Errorf("bus saw %d writes, want 1 (the second was dropped)", len(w))
	}
	r.wantOpen()
}

func TestExtRateLimitsExternalCommands(t *testing.T) {
	clk := newSyncClock()
	r := newRig(t, withClock(clk))
	for i := 0; i < int(extBurst); i++ {
		r.req("t", "tickets", nil)
		if f := r.next(); f.Type != protocol.FrameResp {
			t.Fatalf("ticket %d: %+v", i, f)
		}
	}
	r.req("t", "tickets", nil)
	r.wantErr(protocol.CodeRateLimited)
	// Other reads are untouched by the ext bucket.
	r.wantOpen()
	clk.advance(10 * time.Second)
	r.req("t", "tickets", nil)
	if f := r.next(); f.Type != protocol.FrameResp {
		t.Fatalf("the ext bucket did not refill: %+v", f)
	}
}

// TestAttachPartsChargeBytesNotWritesOverTransport: a many-part upload does not
// spend the composer's write allowance; only the last part does.
func TestAttachPartsChargeBytesNotWritesOverTransport(t *testing.T) {
	clk := newSyncClock()
	r := newRig(t, withClock(clk))
	part := func(n int, last bool) protocol.AttachImageArgs {
		return protocol.AttachImageArgs{Session: "s1", Name: "abcdefgh12", Mime: "image/png", Data: []byte("x"), Part: n, Last: last}
	}
	for i := 0; i < int(writeBurst)+5; i++ {
		r.req("p", protocol.CmdAttachImage, protocol.Request{Args: mustJSON(t, part(i, false))})
		if f := r.next(); f.Type != protocol.FrameResp {
			t.Fatalf("part %d: %+v", i, f)
		}
	}
	r.req("last", protocol.CmdAttachImage, protocol.Request{Args: mustJSON(t, part(99, true))})
	if f := r.next(); f.Type != protocol.FrameResp {
		t.Fatalf("last part: %+v", f)
	}
	// The write bucket still has nine tokens: a say right after is allowed.
	r.req("say", protocol.CmdAgentSay, protocol.Request{Args: mustJSON(t, protocol.AgentSayArgs{Session: "s1", Text: "hi"})})
	if f := r.next(); f.Type != protocol.FrameResp {
		t.Fatalf("agentSay after an upload: %+v", f)
	}
	// Only the last part left an audit line.
	if n := strings.Count(r.log.all(), "cmd=attachImage"); n != 1 {
		t.Errorf("%d attachImage audit lines, want 1:\n%s", n, r.log.all())
	}
}

func TestHandlerSeesThePeer(t *testing.T) {
	l := startTestListener(t, &stubHandler{}, newFakeBus())
	got := make(chan Peer, 1)
	h := &stubHandler{fn: func(ctx context.Context, _ protocol.Request) protocol.Response {
		p, _ := PeerFromContext(ctx)
		got <- p
		return protocol.Response{OK: true}
	}}
	l.srv.opts.Handle = h.handle
	c := l.dial()
	c.wantReady()
	c.req("x", "sessions", nil)
	c.mustRead()
	p := <-got
	if p.DeviceID != l.dev.ID || p.Via != ViaLAN || p.Label != "Test phone" {
		t.Errorf("peer = %+v", p)
	}
	if _, ok := PeerFromContext(context.Background()); ok {
		t.Error("a context with no peer reported one")
	}
}

// --- audit -----------------------------------------------------------------

func auditLines(r *rig) []string {
	var out []string
	for _, l := range strings.Split(r.log.all(), "\n") {
		if strings.Contains(l, "remote: audit ") {
			out = append(out, l)
		}
	}
	return out
}

func TestAuditLineNamesDeviceNeverPayload(t *testing.T) {
	r := newRig(t)
	r.req("a", "answer", protocol.Request{Session: "lola-fe-1", Text: "sk-ant-SECRET-token"})
	r.next()
	lines := auditLines(r)
	if len(lines) != 1 {
		t.Fatalf("audit lines = %q", lines)
	}
	want := "remote: audit device=" + r.dev.ID + " name=Test phone via=lan tier=answer cmd=answer session=lola-fe-1 ok=true code=- fields=- dropped=-"
	if lines[0] != want {
		t.Errorf("audit line\n got %q\nwant %q", lines[0], want)
	}
	if strings.Contains(r.log.all(), "SECRET") {
		t.Error("the payload reached the log")
	}
}

func TestAuditRecordsDroppedAndRefused(t *testing.T) {
	r := newRig(t)
	args := protocol.AgentSayArgs{Session: "s1", Text: "hi\nforged line", Conn: "c1", StopFirst: true}
	r.req("a", protocol.CmdAgentSay, protocol.Request{Args: mustJSON(t, args)})
	r.next()
	r.req("b", "stop", nil)
	r.wantErr(protocol.CodeUnknownCmd)
	r.req("c", "answer\nforged", nil)
	r.wantErr(protocol.CodeUnknownCmd)

	lines := auditLines(r)
	if len(lines) != 3 {
		t.Fatalf("audit lines = %q", lines)
	}
	if !strings.Contains(lines[0], "fields=stopFirst=true") || !strings.Contains(lines[0], "dropped=conn") ||
		!strings.Contains(lines[0], "ok=true") {
		t.Errorf("dropped/fields missing: %q", lines[0])
	}
	if !strings.Contains(lines[1], "tier=never cmd=stop") || !strings.Contains(lines[1], "ok=false code=unknown_cmd") {
		t.Errorf("refusal missing: %q", lines[1])
	}
	if strings.Contains(r.log.all(), "forged line") {
		t.Error("message text reached the log")
	}
	if strings.Count(r.log.all(), "\n") != strings.Count(strings.TrimRight(r.log.all(), "\n"), "\n") {
		t.Error("log has trailing blank lines")
	}
	for _, l := range strings.Split(r.log.all(), "\n") {
		if strings.HasPrefix(l, "forged") {
			t.Errorf("a command name forged a log line: %q", l)
		}
	}
}

func TestReadIsNotAudited(t *testing.T) {
	r := newRig(t)
	r.req("a", "sessions", nil)
	r.next()
	r.req("b", "tickets", nil)
	r.next()
	if lines := auditLines(r); len(lines) != 0 {
		t.Errorf("reads were audited: %q", lines)
	}
}

func TestPtyWriteIsAuditedOncePerPane(t *testing.T) {
	r := newRig(t)
	r.sub("s", "lola-fe-1", 80, 24)
	r.wantResync("lola-fe-1")
	for i := 0; i < 3; i++ {
		r.pty("lola-fe-1", protocol.PTYInputPayload{Action: protocol.PTYActionWrite, Data: []byte("secret-keys")})
	}
	waitFor(t, "the writes", func() bool { _, w, _ := r.bus.snapshot(); return len(w) == 3 })
	if n := strings.Count(r.log.all(), "tier=shell pty-write pane=lola-fe-1"); n != 1 {
		t.Errorf("%d pty-write audit lines, want 1", n)
	}
	if strings.Contains(r.log.all(), "secret-keys") {
		t.Error("keystrokes reached the log")
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
