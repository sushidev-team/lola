package remote

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/protocol"
)

// pairRig is a registry, a Pairer and an authorizer over a fake clock, plus real
// TLS 1.3 client/server pairs over net.Pipe: the Handshake the authorizer sees
// is built from a genuine tls.ConnectionState, so the exporter-bound proof and
// code are the real thing.
type pairRig struct {
	t      *testing.T
	reg    *devices.Registry
	pairer *Pairer
	auth   *deviceAuthorizer
	server *DeviceKey
	logs   *logSink

	mu  sync.Mutex
	clk time.Time
}

type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) logf(f string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, strings.TrimSpace(fmt.Sprintf(f, a...)))
}

func (l *logSink) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func newPairRig(t *testing.T) *pairRig {
	t.Helper()
	r := &pairRig{t: t, clk: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC), logs: &logSink{}}
	r.reg = devices.Open(t.TempDir(), r.now, r.logs.logf)
	r.pairer = NewPairer(r.reg, r.now, r.logs.logf)
	r.auth = newDeviceAuthorizer(r.reg, r.pairer, "mac.local", r.now, r.logs.logf)
	dk, err := LoadOrCreateDeviceKey(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	r.server = dk
	r.pairer.SetDaemonSPKI(dk.SPKISHA256())
	return r
}

func (r *pairRig) now() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.clk
}

func (r *pairRig) advance(d time.Duration) {
	r.mu.Lock()
	r.clk = r.clk.Add(d)
	r.mu.Unlock()
	r.pairer.sweep()
}

func (r *pairRig) phoneKey() *DeviceKey {
	dk, err := LoadOrCreateDeviceKey(r.t.TempDir(), nil)
	if err != nil {
		r.t.Fatal(err)
	}
	return dk
}

// cand is one connection as the authorizer sees it.
type cand struct {
	t      *testing.T
	hs     *Handshake
	client *tls.Conn
	raw    net.Conn // the phone side of the pipe; tls.Conn.Close would block 5s on a non-reading peer
	srv    *tls.Conn
	key    *DeviceKey
	out    chan *protocol.Frame // frames the daemon wrote to the phone
	cw     *protocol.FrameWriter
	res    chan authResult
}

type authResult struct {
	peer Peer
	err  error
}

// dial completes a TLS 1.3 handshake as phone key pk and returns the server-side
// Handshake. Authenticate is NOT started.
func (r *pairRig) dial(pk *DeviceKey, via, rv, addr string) *cand {
	r.t.Helper()
	cp, sp := net.Pipe()
	srvCfg := &tls.Config{
		Certificates: []tls.Certificate{r.server.Certificate},
		MinVersion:   tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert,
	}
	cliCfg := &tls.Config{
		Certificates: []tls.Certificate{pk.Certificate},
		MinVersion:   tls.VersionTLS13, InsecureSkipVerify: true,
	}
	srv, cli := tls.Server(sp, srvCfg), tls.Client(cp, cliCfg)
	errc := make(chan error, 2)
	go func() { errc <- srv.Handshake() }()
	go func() { errc <- cli.Handshake() }()
	for i := 0; i < 2; i++ {
		if err := <-errc; err != nil {
			r.t.Fatalf("tls handshake: %v", err)
		}
	}
	c := &cand{t: r.t, raw: cp, client: cli, srv: srv, key: pk, out: make(chan *protocol.Frame, 16), cw: protocol.NewFrameWriter(cli), res: make(chan authResult, 1)}
	cfr := protocol.NewFrameReader(cli)
	go func() {
		defer close(c.out)
		for {
			var f protocol.Frame
			if err := cfr.ReadFrame(&f); err != nil {
				return
			}
			c.out <- &f
		}
	}()
	sfr, sfw := protocol.NewFrameReader(srv), protocol.NewFrameWriter(srv)
	var frame protocol.Frame
	st := srv.ConnectionState()
	c.hs = &Handshake{
		RemoteAddr: addr, TLS: &st, At: r.now(), Via: via, RelayRV: rv,
		NextFrame: func() (*protocol.Frame, error) {
			if err := sfr.ReadFrame(&frame); err != nil {
				return nil, err
			}
			return &frame, nil
		},
		Send:            func(f *protocol.Frame) error { return sfw.WriteFrame(f) },
		SetReadDeadline: srv.SetReadDeadline,
	}
	r.t.Cleanup(func() { _ = cp.Close(); _ = sp.Close() })
	return c
}

func (c *cand) start(a Authorizer) {
	go func() {
		p, err := a.Authenticate(context.Background(), c.hs)
		c.res <- authResult{p, err}
	}()
}

func (c *cand) result() authResult {
	c.t.Helper()
	select {
	case r := <-c.res:
		return r
	case <-time.After(5 * time.Second):
		c.t.Fatal("Authenticate did not return")
		return authResult{}
	}
}

func (c *cand) frame() *protocol.Frame {
	c.t.Helper()
	select {
	case f, ok := <-c.out:
		if !ok {
			c.t.Fatal("connection closed before a frame arrived")
		}
		return f
	case <-time.After(5 * time.Second):
		c.t.Fatal("no frame from the daemon")
		return nil
	}
}

func (c *cand) send(f protocol.Frame) {
	c.t.Helper()
	if err := c.cw.WriteFrame(&f); err != nil {
		c.t.Fatalf("client write: %v", err)
	}
}

// pairFrame is the remote.pair request with a proof built exactly as the phone does.
func (c *cand) pairFrame(secret []byte, name string) protocol.Frame {
	c.t.Helper()
	cs := c.client.ConnectionState()
	ex, err := cs.ExportKeyingMaterial(pairProofLabel, nil, 32)
	if err != nil {
		c.t.Fatal(err)
	}
	return reqFrame(protocol.PairRequest{
		Proof: base64.RawURLEncoding.EncodeToString(protocol.PairProof(secret, ex)),
		Name:  name, Platform: "ios",
	})
}

// phoneCode is the code the phone shows, from ITS side of the exporter.
func (c *cand) phoneCode(daemon [32]byte) string {
	cs := c.client.ConnectionState()
	ex, err := cs.ExportKeyingMaterial(pairCodeLabel, nil, 32)
	if err != nil {
		c.t.Fatal(err)
	}
	return protocol.PairCode(ex, daemon, c.key.SPKISHA256())
}

func reqFrame(payload any) protocol.Frame {
	f := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: "p1", Cmd: protocol.CmdRemotePair}
	_ = f.SetPayload(payload)
	return f
}

func errCode(t *testing.T, f *protocol.Frame) string {
	t.Helper()
	if f.Type != protocol.FrameErr {
		t.Fatalf("frame type = %q, want err", f.Type)
	}
	var e protocol.ErrPayload
	if err := f.DecodePayload(&e); err != nil {
		t.Fatal(err)
	}
	return e.Code
}

func (r *pairRig) status(id string) protocol.PairStatusData {
	r.t.Helper()
	d, err := r.pairer.Status(context.Background(), protocol.PairStatusArgs{Pairing: id})
	if err != nil {
		r.t.Fatal(err)
	}
	return d
}

// waitState blocks until the pairing reaches state (long-poll, so no sleeps).
func (r *pairRig) waitState(id, state string) protocol.PairStatusData {
	r.t.Helper()
	for i := 0; i < 20; i++ {
		d := r.status(id)
		if d.State == state {
			return d
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		d, err := r.pairer.Status(ctx, protocol.PairStatusArgs{Pairing: id, Since: d.State, WaitMs: 3000})
		cancel()
		if err == nil && d.State == state {
			return d
		}
	}
	r.t.Fatalf("pairing %s never reached %s", id, state)
	return protocol.PairStatusData{}
}

// presentAndConfirming drives a candidate to `confirming`.
func (r *pairRig) presentAndConfirming(t *testing.T, tk Ticket, c *cand, name string) {
	t.Helper()
	c.start(r.auth)
	c.send(c.pairFrame(tk.Secret, name))
	r.waitState(tk.ID, protocol.PairStateConfirming)
}

func TestPairHappyPathThenReconnect(t *testing.T) {
	r := newPairRig(t)
	tk, err := r.pairer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	pk := r.phoneKey()
	c := r.dial(pk, ViaLAN, "", "192.0.2.7:5000")
	r.presentAndConfirming(t, tk, c, "Martin's iPhone")

	st := r.status(tk.ID)
	if st.DeviceName != "Martin's iPhone" || st.Platform != "ios" || st.Via != ViaLAN || st.TriesLeft != maxConfirmTries {
		t.Fatalf("confirming status = %+v", st)
	}
	if r.pairer.windowOpen() {
		t.Fatal("window must close on the burn")
	}
	// The code the human types is the one the PHONE computes from its side.
	code := c.phoneCode(r.server.SPKISHA256())
	rec := devices.DeviceRelay{
		RV: strings.Repeat("a", 32), BearerHash: strings.Repeat("b", 64), RotatedAt: r.now(),
	}
	st, err = r.pairer.Confirm(protocol.PairConfirmArgs{Pairing: tk.ID, Code: code[:3] + " " + code[3:]},
		&ConfirmRelay{Bearer: "bearer-plain", Record: rec})
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if st.State != protocol.PairStatePaired || st.Device == nil || st.Device.Fingerprint == "" {
		t.Fatalf("paired status = %+v", st)
	}

	f := c.frame()
	if f.Type != protocol.FrameResp || f.ID != "p1" {
		t.Fatalf("outcome frame = %+v", f)
	}
	var resp protocol.Response
	if err := f.DecodePayload(&resp); err != nil || !resp.OK {
		t.Fatalf("response = %+v, %v", resp, err)
	}
	var pr protocol.PairResult
	if err := json.Unmarshal(resp.Data, &pr); err != nil {
		t.Fatal(err)
	}
	if pr.DeviceID != st.Device.ID || pr.Host != "mac.local" || pr.Relay == nil ||
		pr.Relay.RV != rec.RV || pr.Relay.Bearer != "bearer-plain" || len(pr.Tiers) != len(protocol.DefaultTiers) {
		t.Fatalf("PairResult = %+v", pr)
	}
	if res := c.result(); !errors.Is(res.err, ErrPairingDone) {
		t.Fatalf("present err = %v, want ErrPairingDone", res.err)
	}

	// TestPairConfirmRelayRecordWritten: hashes only reach the registry.
	dev, ok := r.reg.Get(pr.DeviceID)
	if !ok || dev.Relay == nil || dev.Relay.RV != rec.RV || dev.Relay.BearerHash != rec.BearerHash {
		t.Fatalf("registry device = %+v ok=%v", dev, ok)
	}
	b, _ := json.Marshal(dev)
	if strings.Contains(string(b), "bearer-plain") {
		t.Fatal("plaintext bearer reached the registry")
	}

	// Reconnect: the same key now authenticates as a paired device.
	c2 := r.dial(pk, ViaLAN, "", "192.0.2.7:5001")
	c2.start(r.auth)
	res := c2.result()
	if res.err != nil || res.peer.DeviceID != pr.DeviceID || res.peer.Via != ViaLAN || res.peer.Epoch != dev.Epoch {
		t.Fatalf("reconnect = %+v", res)
	}
}

func TestPairAuditNeverCarriesSecret(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	c := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.7:5000")
	r.presentAndConfirming(t, tk, c, "Phone")
	code := c.phoneCode(r.server.SPKISHA256())
	if _, err := r.pairer.Confirm(protocol.PairConfirmArgs{Pairing: tk.ID, Code: code}, nil); err != nil {
		t.Fatal(err)
	}
	logs := r.logs.all()
	f := c.pairFrame(tk.Secret, "x")
	var req protocol.PairRequest
	_ = f.DecodePayload(&req)
	for label, secret := range map[string]string{
		"secret": base64.RawURLEncoding.EncodeToString(tk.Secret), "proof": req.Proof, "code": code,
	} {
		if strings.Contains(logs, secret) {
			t.Fatalf("log carries the %s:\n%s", label, logs)
		}
	}
	if !strings.Contains(logs, "pairing="+tk.ID) {
		t.Fatalf("no audit line:\n%s", logs)
	}
}

func TestPairBadProofDoesNotBurn(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()

	bad := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.8:1")
	bad.start(r.auth)
	bad.send(bad.pairFrame(make([]byte, 32), "Intruder"))
	if code := errCode(t, bad.frame()); code != protocol.CodePairFailed {
		t.Fatalf("code = %q", code)
	}
	if res := bad.result(); res.err == nil || errors.Is(res.err, ErrPairingDone) {
		t.Fatalf("bad proof err = %v", res.err)
	}
	if st := r.status(tk.ID); st.State != protocol.PairStateWaiting {
		t.Fatalf("state = %s, want waiting", st.State)
	}
	if !r.pairer.windowOpen() {
		t.Fatal("window closed by a bad proof")
	}

	good := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.9:1")
	r.presentAndConfirming(t, tk, good, "Real")
}

// TestPairBadProofsNeverFailThePairing: any peer that reaches the port may send
// a wrong proof while a window is open, so no number of them moves the pairing.
// The real phone still pairs after a flood; the flood costs one alert line and
// a bounded number of candidate lines.
func TestPairBadProofsNeverFailThePairing(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	const flood = 4 * badPairProofsAlert
	for i := 0; i < flood; i++ {
		c := r.dial(r.phoneKey(), ViaLAN, "", fmt.Sprintf("192.0.2.%d:1", 8+i%3))
		c.start(r.auth)
		c.send(c.pairFrame(make([]byte, 32), "Intruder"))
		if code := errCode(t, c.frame()); code != protocol.CodePairFailed {
			t.Fatalf("attempt %d: code = %q", i, code)
		}
		c.result()
	}
	if st := r.status(tk.ID); st.State != protocol.PairStateWaiting {
		t.Fatalf("status after %d wrong proofs = %+v", flood, st)
	}
	if !r.pairer.windowOpen() {
		t.Fatal("wrong proofs closed the window")
	}
	logs := r.logs.all()
	if n := strings.Count(logs, "wrong proofs; another device"); n != 1 {
		t.Fatalf("alert lines = %d, want 1:\n%s", n, logs)
	}
	if n := strings.Count(logs, "refused a wrong proof"); n > int(refusalLogBurst) {
		t.Fatalf("%d candidate lines for %d wrong proofs; the log is not throttled", n, flood)
	}

	good := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.9:1")
	r.presentAndConfirming(t, tk, good, "Real")
}

// TestSilentCandidatesHoldNoPairingSlot: a candidate slot is taken only once a
// remote.pair has arrived, so peers that connect and say nothing cannot keep
// the real phone out for the whole window.
func TestSilentCandidatesHoldNoPairingSlot(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	var silent []*cand
	for i := 0; i < 2*maxUnpairedConns; i++ {
		c := r.dial(r.phoneKey(), ViaLAN, "", fmt.Sprintf("192.0.2.%d:1", 20+i%2))
		c.start(r.auth)
		silent = append(silent, c)
	}
	time.Sleep(20 * time.Millisecond) // let every silent candidate reach its first read
	if n := r.pairer.unpairedCount(); n != 0 {
		t.Fatalf("silent candidates hold %d slots", n)
	}
	good := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.20:2")
	r.presentAndConfirming(t, tk, good, "Real")
	for _, c := range silent {
		_ = c.raw.Close()
		c.result()
	}
}

func TestPairCandidateWhileConfirmingIsRefusedWithoutStateChange(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	first := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.8:1")
	r.presentAndConfirming(t, tk, first, "First")

	second := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.9:1")
	second.start(r.auth)
	second.send(second.pairFrame(tk.Secret, "Second"))
	if code := errCode(t, second.frame()); code != protocol.CodePairFailed {
		t.Fatalf("code = %q", code)
	}
	second.result()
	if st := r.status(tk.ID); st.State != protocol.PairStateConfirming || st.DeviceName != "First" {
		t.Fatalf("status = %+v", st)
	}
}

func TestPairNoFrameDoesNotBurn(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	c := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.8:1")
	c.start(r.auth)
	_ = c.raw.Close() // no frame at all
	if res := c.result(); res.err == nil {
		t.Fatal("expected an error")
	}
	if st := r.status(tk.ID); st.State != protocol.PairStateWaiting {
		t.Fatalf("state = %s", st.State)
	}
}

func TestUnpairedMaySendOnlyRemotePair(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	for _, f := range []protocol.Frame{
		{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, ID: "x", Cmd: "status"},
		{V: protocol.FrameVersionCurrent, Type: protocol.FrameSub, ID: "x", Pane: "a"},
	} {
		c := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.8:1")
		c.start(r.auth)
		c.send(f)
		if code := errCode(t, c.frame()); code != protocol.CodeDenied {
			t.Fatalf("code = %q", code)
		}
		c.result()
	}
	if st := r.status(tk.ID); st.State != protocol.PairStateWaiting {
		t.Fatalf("state = %s", st.State)
	}
}

func TestPairBadNameKeepsWaiting(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	c := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.8:1")
	c.start(r.auth)
	c.send(c.pairFrame(tk.Secret, "bad\x00name"))
	errCode(t, c.frame())
	c.result()
	if st := r.status(tk.ID); st.State != protocol.PairStateWaiting {
		t.Fatalf("state = %s", st.State)
	}
}

func TestPairExpiredWaiting(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	r.advance(PairSecretTTL + time.Second)
	if st := r.status(tk.ID); st.State != protocol.PairStateExpired {
		t.Fatalf("state = %s", st.State)
	}
	if r.pairer.windowOpen() || r.pairer.pending() {
		t.Fatal("expired pairing still pending")
	}
	r.advance(PairTerminalRetention + time.Second)
	if _, err := r.pairer.Status(context.Background(), protocol.PairStatusArgs{Pairing: tk.ID}); err == nil {
		t.Fatal("terminal pairing not forgotten after retention")
	}
}

func TestPairConfirmTimeout(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	c := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.8:1")
	r.presentAndConfirming(t, tk, c, "Phone")
	r.advance(PairConfirmTTL + time.Second)
	if code := errCode(t, c.frame()); code != protocol.CodePairExpired {
		t.Fatalf("code = %q", code)
	}
	if res := c.result(); !errors.Is(res.err, ErrPairingDone) {
		t.Fatalf("err = %v", res.err)
	}
	if st := r.status(tk.ID); st.State != protocol.PairStateExpired {
		t.Fatalf("state = %s", st.State)
	}
}

func TestPairRejected(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	c := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.8:1")
	r.presentAndConfirming(t, tk, c, "Phone")
	st, err := r.pairer.Reject(tk.ID)
	if err != nil || st.State != protocol.PairStateRejected {
		t.Fatalf("Reject = %+v, %v", st, err)
	}
	if code := errCode(t, c.frame()); code != protocol.CodePairRejected {
		t.Fatalf("code = %q", code)
	}
	c.result()
	if len(r.reg.List()) != 0 {
		t.Fatal("rejected pairing registered a device")
	}
}

func TestPairConfirmWrongCodeCountsDown(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	c := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.8:1")
	r.presentAndConfirming(t, tk, c, "Phone")
	right := c.phoneCode(r.server.SPKISHA256())
	wrong := "000000"
	if right == wrong {
		wrong = "000001"
	}
	for want := 2; want >= 1; want-- {
		if _, err := r.pairer.Confirm(protocol.PairConfirmArgs{Pairing: tk.ID, Code: wrong}, nil); err == nil ||
			!strings.Contains(err.Error(), "tries left") {
			t.Fatalf("err = %v", err)
		}
		if got := r.status(tk.ID).TriesLeft; got != want {
			t.Fatalf("TriesLeft = %d, want %d", got, want)
		}
	}
	if _, err := r.pairer.Confirm(protocol.PairConfirmArgs{Pairing: tk.ID, Code: wrong}, nil); err == nil {
		t.Fatal("third wrong code accepted")
	}
	st := r.status(tk.ID)
	if st.State != protocol.PairStateFailed || st.Reason != protocol.PairReasonCodeMismatch {
		t.Fatalf("status = %+v", st)
	}
	// After failing, even the right code is refused.
	if _, err := r.pairer.Confirm(protocol.PairConfirmArgs{Pairing: tk.ID, Code: right}, nil); err == nil {
		t.Fatal("confirm after failure accepted")
	}
	if len(r.reg.List()) != 0 {
		t.Fatal("device registered after code failure")
	}
}

func TestPairConfirmBadTiersKeepsState(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	c := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.8:1")
	r.presentAndConfirming(t, tk, c, "Phone")
	code := c.phoneCode(r.server.SPKISHA256())
	if _, err := r.pairer.Confirm(protocol.PairConfirmArgs{Pairing: tk.ID, Code: code, Tiers: []string{"answer"}}, nil); !errors.Is(err, devices.ErrBadTiers) {
		t.Fatalf("err = %v", err)
	}
	if st := r.status(tk.ID); st.State != protocol.PairStateConfirming {
		t.Fatalf("state = %s", st.State)
	}
	st, err := r.pairer.Confirm(protocol.PairConfirmArgs{Pairing: tk.ID, Code: code, Tiers: []string{"read", "answer"}, Name: "Renamed"}, nil)
	if err != nil || st.Device.Name != "Renamed" || len(st.Device.Tiers) != 2 {
		t.Fatalf("Confirm = %+v, %v", st, err)
	}
}

func TestPairPhoneLeft(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	c := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.8:1")
	r.presentAndConfirming(t, tk, c, "Phone")
	_ = c.raw.Close()
	st := r.waitState(tk.ID, protocol.PairStateFailed)
	if st.Reason != protocol.PairReasonPhoneLeft {
		t.Fatalf("reason = %s", st.Reason)
	}
	c.result()
}

func TestPairPhoneFrameWhileConfirmingFailsIt(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	c := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.8:1")
	r.presentAndConfirming(t, tk, c, "Phone")
	c.send(reqFrame(struct{}{}))
	if st := r.waitState(tk.ID, protocol.PairStateFailed); st.Reason != protocol.PairReasonPhoneLeft {
		t.Fatalf("reason = %s", st.Reason)
	}
	c.result()
}

func TestPairWatcherIsTheOnlyReader(t *testing.T) {
	// A frame sent at the instant of the confirm is discarded; present returns
	// once, with the outcome, and the race detector sees one reader.
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	c := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.8:1")
	r.presentAndConfirming(t, tk, c, "Phone")
	code := c.phoneCode(r.server.SPKISHA256())
	go func() { _ = c.cw.WriteFrame(&protocol.Frame{V: 2, Type: protocol.FrameReq, ID: "late", Cmd: "status"}) }()
	_, _ = r.pairer.Confirm(protocol.PairConfirmArgs{Pairing: tk.ID, Code: code}, nil)
	if res := c.result(); !errors.Is(res.err, ErrPairingDone) {
		t.Fatalf("err = %v", res.err)
	}
}

func TestPairBeginCancelsOpen(t *testing.T) {
	r := newPairRig(t)
	a, _ := r.pairer.Begin()
	b, _ := r.pairer.Begin()
	if a.ID == b.ID {
		t.Fatal("same id twice")
	}
	if st := r.status(a.ID); st.State != protocol.PairStateCancelled {
		t.Fatalf("first = %s", st.State)
	}
	if st := r.status(b.ID); st.State != protocol.PairStateWaiting {
		t.Fatalf("second = %s", st.State)
	}
}

func TestPairBeginCancelsConfirming(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	c := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.8:1")
	r.presentAndConfirming(t, tk, c, "Phone")
	if _, err := r.pairer.Begin(); err != nil {
		t.Fatal(err)
	}
	if st := r.status(tk.ID); st.State != protocol.PairStateCancelled {
		t.Fatalf("state = %s", st.State)
	}
	if code := errCode(t, c.frame()); code != protocol.CodePairFailed {
		t.Fatalf("code = %q", code)
	}
	c.result()
}

func TestPairRejectCancelsWaiting(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	st, err := r.pairer.Reject(tk.ID)
	if err != nil || st.State != protocol.PairStateCancelled {
		t.Fatalf("Reject = %+v, %v", st, err)
	}
	if r.pairer.windowOpen() {
		t.Fatal("window open after cancel")
	}
	if _, err := r.pairer.Reject("nope"); err == nil {
		t.Fatal("unknown pairing accepted")
	}
}

func TestPairStatusLongPollWakes(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	done := make(chan protocol.PairStatusData, 1)
	go func() {
		d, _ := r.pairer.Status(context.Background(), protocol.PairStatusArgs{
			Pairing: tk.ID, Since: protocol.PairStateWaiting, WaitMs: 60_000})
		done <- d
	}()
	time.Sleep(50 * time.Millisecond)
	_, _ = r.pairer.Reject(tk.ID)
	select {
	case d := <-done:
		if d.State != protocol.PairStateCancelled {
			t.Fatalf("state = %s", d.State)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("long poll did not wake")
	}
	// A short wait with no change returns the current state.
	tk2, _ := r.pairer.Begin()
	start := time.Now()
	d, err := r.pairer.Status(context.Background(), protocol.PairStatusArgs{Pairing: tk2.ID, Since: protocol.PairStateWaiting, WaitMs: 50})
	if err != nil || d.State != protocol.PairStateWaiting || time.Since(start) < 40*time.Millisecond {
		t.Fatalf("short poll = %+v, %v after %v", d, err, time.Since(start))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.pairer.Status(ctx, protocol.PairStatusArgs{Pairing: tk2.ID, Since: protocol.PairStateWaiting, WaitMs: 60_000}); err == nil {
		t.Fatal("cancelled context not honoured")
	}
}

func TestPairNameClashAndReplaces(t *testing.T) {
	r := newPairRig(t)
	pk := r.phoneKey()
	if _, err := r.reg.Add("Martin's iPhone", "ios", pk.SPKISHA256(), protocol.DefaultTiers, nil); err != nil {
		t.Fatal(err)
	}
	tk, _ := r.pairer.Begin()
	other := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.8:1")
	r.presentAndConfirming(t, tk, other, "martin's iphone")
	if st := r.status(tk.ID); !st.NameClash || st.Replaces != "" {
		t.Fatalf("clash status = %+v", st)
	}
	_, _ = r.pairer.Reject(tk.ID)
	other.result()

	// The same key presents again while a window is open: it is a re-pair.
	tk2, _ := r.pairer.Begin()
	// A registered key authenticates as paired, never as a candidate, so the
	// re-pair path is the Pairer's own: present directly.
	c := r.dial(pk, ViaLAN, "", "192.0.2.8:2")
	go func() {
		_, err := r.pairer.present(context.Background(), c.hs, pk.SPKISHA256(), sourceKey(c.hs))
		c.res <- authResult{err: err}
	}()
	c.send(c.pairFrame(tk2.Secret, "New name"))
	st := r.waitState(tk2.ID, protocol.PairStateConfirming)
	if st.Replaces == "" || st.Replaces != devices.IDFor(pk.SPKISHA256()) {
		t.Fatalf("replaces = %q", st.Replaces)
	}
	_, _ = r.pairer.Reject(tk2.ID)
	c.result()
}

func TestSetWindowRelayOnlyWhileWaiting(t *testing.T) {
	r := newPairRig(t)
	tk, _ := r.pairer.Begin()
	if _, _, _, ok := r.pairer.Window(); ok {
		t.Fatal("a fresh window carries a relay")
	}
	rv, th := strings.Repeat("c", 32), strings.Repeat("d", 64)
	if r.pairer.SetWindowRelay("other", rv, th) {
		t.Fatal("wrong id accepted")
	}
	if r.pairer.SetWindowRelay(tk.ID, rv, "") {
		t.Fatal("half an entry accepted")
	}
	if !r.pairer.SetWindowRelay(tk.ID, rv, th) {
		t.Fatal("waiting pairing refused")
	}
	grv, gth, exp, ok := r.pairer.Window()
	if !ok || grv != rv || gth != th || !exp.Equal(tk.ExpiresAt) || r.pairer.windowRV() != rv {
		t.Fatalf("Window = %q %q %v %v", grv, gth, exp, ok)
	}
	if !r.pairer.SetWindowRelay(tk.ID, "", "") {
		t.Fatal("clear refused")
	}
	if _, _, _, ok := r.pairer.Window(); ok {
		t.Fatal("cleared window still reported")
	}
	r.pairer.SetWindowRelay(tk.ID, rv, th)
	c := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.8:1")
	r.presentAndConfirming(t, tk, c, "Phone")
	if r.pairer.SetWindowRelay(tk.ID, rv, th) {
		t.Fatal("confirming pairing accepted a relay")
	}
	if _, _, _, ok := r.pairer.Window(); ok || r.pairer.windowRV() != "" {
		t.Fatal("window outlived the burn")
	}
}

func TestUnpairedSlotsPerSource(t *testing.T) {
	r := newPairRig(t)
	rel1, ok1 := r.pairer.admitUnpaired("192.0.2.1")
	rel2, ok2 := r.pairer.admitUnpaired("192.0.2.1")
	if !ok1 || !ok2 {
		t.Fatal("two candidates from one source must pass")
	}
	if _, ok := r.pairer.admitUnpaired("192.0.2.1"); ok {
		t.Fatal("third candidate from one source passed")
	}
	rel1()
	rel1() // idempotent
	rel3, ok := r.pairer.admitUnpaired("192.0.2.1")
	if !ok {
		t.Fatal("slot not freed")
	}
	rel2()
	rel3()
	// Global limit.
	var rels []func()
	for i := 0; i < maxUnpairedConns; i++ {
		rel, ok := r.pairer.admitUnpaired("10.0.0." + string(rune('a'+i)))
		if !ok {
			t.Fatalf("candidate %d refused", i)
		}
		rels = append(rels, rel)
	}
	_ = rels
	if _, ok := r.pairer.admitUnpaired("fresh"); ok {
		t.Fatal("global limit not enforced")
	}
}

func TestUnpairedSlotsReleasedOnFailure(t *testing.T) {
	r := newPairRig(t)
	if _, err := r.pairer.Begin(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		c := r.dial(r.phoneKey(), ViaLAN, "", "192.0.2.50:100")
		c.start(r.auth)
		_ = c.raw.Close()
		if res := c.result(); res.err == nil {
			t.Fatalf("attempt %d: no error", i)
		}
	}
	rel, ok := r.pairer.admitUnpaired("192.0.2.50")
	if !ok {
		t.Fatal("slots leaked by failed candidates")
	}
	rel()
}

func TestPairedDeviceRemotePairIsDenied(t *testing.T) {
	r := newPairRig(t)
	pk := r.phoneKey()
	dev, _ := r.reg.Add("Phone", "ios", pk.SPKISHA256(), protocol.DefaultTiers, nil)
	peer := Peer{DeviceID: dev.ID, Epoch: dev.Epoch}
	f := reqFrame(protocol.PairRequest{})
	if err := r.auth.AuthorizeFrame(context.Background(), peer, &f); !errors.Is(err, ErrDenied) {
		t.Fatalf("err = %v, want ErrDenied", err)
	}
}

func (p *Pairer) unpairedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.unpaired
}
