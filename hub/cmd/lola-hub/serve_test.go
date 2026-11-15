package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/console"
	"github.com/sushidev-team/lola/hub/internal/relay"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// serve's stderr is the server log (docker logs, journald): it names the
// admin token's file but never prints the token itself.
func TestServeNeverLogsTheAdminToken(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "hub.json")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var errb lockedBuffer
	c := &cli{stdout: io.Discard, stderr: &errb, getenv: func(string) string { return "" }, ctx: ctx}
	done := make(chan int, 1)
	go func() {
		done <- c.run([]string{"serve", "--data", data, "--addr", "127.0.0.1:0", "--admin-addr", "127.0.0.1:0"})
	}()
	deadline := time.After(10 * time.Second)
	for !strings.Contains(errb.String(), "admin token:") {
		select {
		case code := <-done:
			t.Fatalf("serve exited %d: %s", code, errb.String())
		case <-deadline:
			t.Fatalf("serve never announced the console: %s", errb.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	<-done
	raw, err := os.ReadFile(filepath.Join(dir, console.TokenFile))
	if err != nil {
		t.Fatal(err)
	}
	tok := strings.TrimSpace(string(raw))
	got := errb.String()
	if len(tok) != 64 || strings.Contains(got, tok) {
		t.Fatalf("admin token leaked to stderr: %q", got)
	}
	if !strings.Contains(got, "admin token: created in "+filepath.Join(dir, console.TokenFile)) {
		t.Fatalf("stderr does not name the token file: %q", got)
	}
}

// relayHub is a real `serve` wiring (runHub) with one enrolled device and one
// paired rendezvous seeded into its store.
type relayHub struct {
	t      *testing.T
	url    string
	log    *lockedBuffer
	cancel context.CancelFunc
	done   chan error
	id     string
	priv   []byte
	rv     string
	bearer string
	pub    string
}

func startRelayHub(t *testing.T, mod func(*hubOpts)) *relayHub {
	t.Helper()
	pub, priv, err := hubproto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	h := &relayHub{t: t, log: &lockedBuffer{}, id: hubproto.DeviceID(pub), priv: priv}
	h.rv, err = hubproto.NewRelayID()
	if err != nil {
		t.Fatal(err)
	}
	var hash string
	h.bearer, hash, err = hubproto.NewRelayBearer()
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(t.TempDir(), "hub.json")
	st, err := store.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := st.Update(func(d *store.Data) error {
		d.Orgs["acme"] = &store.Org{Name: "acme"}
		d.Devices[h.id] = &store.Device{ID: h.id, Org: "acme", Name: "dev", PublicKey: hubproto.EncodePublicKey(pub), CreatedAt: now}
		if d.Relay == nil {
			d.Relay = map[string]*store.RelayEntry{}
		}
		d.Relay[h.rv] = &store.RelayEntry{RV: h.rv, Device: h.id, Org: "acme", BearerHash: hash, UpdatedAt: now}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	o := hubOpts{addr: "127.0.0.1:0", data: data, ttl: time.Hour, escAfter: 3, sweep: time.Minute, clientIP: "none"}
	if mod != nil {
		mod(&o)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	t.Cleanup(cancel)
	c := &cli{stdout: io.Discard, stderr: h.log, getenv: func(string) string { return "" }, ctx: ctx}
	ready := make(chan hubReady, 1)
	h.done = make(chan error, 1)
	go func() { h.done <- c.runHub(ctx, o, func(r hubReady) { ready <- r }) }()
	select {
	case r := <-ready:
		h.url = r.APIURL
	case err := <-h.done:
		t.Fatalf("hub exited: %v %s", err, h.log.String())
	case <-time.After(10 * time.Second):
		t.Fatal("hub never became ready")
	}
	return h
}

func noProxy(*http.Request) (*url.URL, error) { return nil, nil }

func (h *relayHub) signed(path string) *http.Request {
	req, err := http.NewRequest(http.MethodGet, h.url+path, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	nonce, err := hubproto.NewNonce()
	if err != nil {
		h.t.Fatal(err)
	}
	if err := hubproto.SignAt(req, h.id, h.priv, nil, time.Now(), nonce); err != nil {
		h.t.Fatal(err)
	}
	return req
}

func (h *relayHub) dial(req *http.Request) *hubproto.WSConn {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, err := hubproto.WSDialer{Proxy: noProxy}.Dial(ctx, req)
	if err != nil {
		h.t.Fatalf("dial %s: %v", req.URL.Path, err)
	}
	h.t.Cleanup(func() { _ = ws.CloseNow(hubproto.WSCloseGoingAway) })
	return ws
}

func controlMsg(t *testing.T, ws *hubproto.WSConn) hubproto.RelayMsg {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	b, err := ws.ReadMessage(hubproto.RelayMaxControlMessage)
	if err != nil {
		t.Fatalf("control read: %v", err)
	}
	var m hubproto.RelayMsg
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// connect runs one whole dial and returns the control channel, the phone leg
// and the daemon's data leg.
func (h *relayHub) connect() (ctrl, phone, leg *hubproto.WSConn) {
	h.t.Helper()
	ctrl = h.dial(h.signed(hubproto.PathRelayDaemon + "?v=1"))
	if m := controlMsg(h.t, ctrl); m.T != "hello" {
		h.t.Fatalf("hello = %+v", m)
	}
	req, _ := http.NewRequest(http.MethodGet, h.url+hubproto.PathRelayPhone, nil)
	req.Header.Set(hubproto.HeaderRelayRV, h.rv)
	req.Header.Set(hubproto.HeaderRelayBearer, h.bearer)
	ch := make(chan *hubproto.WSConn, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		ws, err := hubproto.WSDialer{Proxy: noProxy}.Dial(ctx, req)
		if err != nil {
			h.t.Errorf("phone dial: %v", err)
			ch <- nil
			return
		}
		ch <- ws
	}()
	m := controlMsg(h.t, ctrl)
	if m.T != "incoming" {
		h.t.Fatalf("incoming = %+v", m)
	}
	leg = h.dial(h.signed(hubproto.PathRelayAccept + "?conn=" + m.Conn))
	if phone = <-ch; phone == nil {
		h.t.FailNow()
	}
	h.t.Cleanup(func() { _ = phone.CloseNow(hubproto.WSCloseGoingAway) })
	return ctrl, phone, leg
}

func closeCode(t *testing.T, ws *hubproto.WSConn) uint16 {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	for {
		if _, err := ws.Read(buf); err != nil {
			code, ok := ws.PeerCloseCode()
			if !ok {
				t.Fatalf("no close code (err %v)", err)
			}
			return code
		}
	}
}

// The relay's legs are hijacked through accessLog's statusWriter, outlive the
// http.Server's WriteTimeout, and the access log says 101 without naming a
// rendezvous id, a bearer or a header.
func TestServeHijackThroughAccessLog(t *testing.T) {
	h := startRelayHub(t, func(o *hubOpts) { o.writeTimeout = 200 * time.Millisecond })
	_, phone, leg := h.connect()
	time.Sleep(600 * time.Millisecond) // past WriteTimeout
	if _, err := phone.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	_ = leg.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(leg, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("leg read %q: %v", buf, err)
	}
	if _, err := leg.Write([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	_ = phone.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(phone, buf); err != nil || string(buf) != "pong" {
		t.Fatalf("phone read %q: %v", buf, err)
	}
	_ = phone.CloseNow(hubproto.WSCloseGoingAway)
	_ = leg.CloseNow(hubproto.WSCloseGoingAway)
	// The phone handler logs when its splice ends; the control handler only
	// when the link does, so its 101 is not asserted here.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(h.log.String(), `"/v1/relay/phone" 101`) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	got := h.log.String()
	for _, p := range []string{"/v1/relay/accept", "/v1/relay/phone"} {
		if !strings.Contains(got, `"`+p+`" 101`) {
			t.Errorf("no 101 logged for %s:\n%s", p, got)
		}
	}
	for _, secret := range []string{h.rv, h.bearer} {
		if strings.Contains(got, secret) {
			t.Fatalf("access log leaks a relay credential: %s", got)
		}
	}
}

// accessLog's status rules on a handler that does not hijack.
func TestAccessLogStatus(t *testing.T) {
	var buf lockedBuffer
	l := log.New(&buf, "", 0)
	serve := func(h http.HandlerFunc, hdr ...string) {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		accessLog(l, h).ServeHTTP(httptest.NewRecorder(), req)
	}
	serve(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("body")) })
	serve(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	serve(func(http.ResponseWriter, *http.Request) {})
	serve(func(http.ResponseWriter, *http.Request) {}, "Upgrade", "websocket")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	want := []string{" 200 ", " 418 ", " 200 ", " 101 "}
	if len(lines) != len(want) {
		t.Fatalf("log = %q", lines)
	}
	for i, w := range want {
		if !strings.Contains(lines[i], w) {
			t.Errorf("line %d = %q, want %q", i, lines[i], w)
		}
	}
}

// Shutdown reaches hijacked legs through RegisterOnShutdown: every control
// link and splice leg gets 1001, inside the shutdown budget.
func TestServeShutdownClosesLegs(t *testing.T) {
	h := startRelayHub(t, nil)
	ctrl, phone, leg := h.connect()
	h.cancel()
	start := time.Now()
	for name, ws := range map[string]*hubproto.WSConn{"phone": phone, "leg": leg} {
		if code := closeCode(t, ws); code != hubproto.WSCloseGoingAway {
			t.Errorf("%s close code %d, want 1001", name, code)
		}
	}
	if m := controlMsg(t, ctrl); m.T != "bye" || m.Reason != "shutdown" {
		t.Errorf("control = %+v, want bye shutdown", m)
	}
	if code := closeCode(t, ctrl); code != hubproto.WSCloseGoingAway {
		t.Errorf("control close code %d, want 1001", code)
	}
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("runHub: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("shutdown out of budget")
	}
}

// The per-network half of TestDialBuckets: --client-ip decides the key, IPv6
// shares a bucket inside one /64, IPv4 is a /32, "none" has no bucket.
func TestDialBuckets(t *testing.T) {
	req := func(remote string, hdr ...string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		for i := 0; i+1 < len(hdr); i += 2 {
			r.Header.Set(hdr[i], hdr[i+1])
		}
		return r
	}
	if f, err := clientIPFunc("none"); f != nil || err != nil {
		t.Fatalf("none = %v, %v", f != nil, err)
	}
	if f, err := clientIPFunc(""); f != nil || err != nil {
		t.Fatalf("empty = %v, %v", f != nil, err)
	}
	for _, bad := range []string{"remote", "header:", "header:A B", "x-real-ip"} {
		if _, err := clientIPFunc(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	ra, err := clientIPFunc("remote-addr")
	if err != nil {
		t.Fatal(err)
	}
	key := func(f func(*http.Request) (netip.Prefix, bool), r *http.Request) string {
		p, ok := f(r)
		if !ok {
			return ""
		}
		return p.String()
	}
	if a, b := key(ra, req("[2001:db8:1:2::1]:443")), key(ra, req("[2001:db8:1:2:ffff::9]:1")); a != b || a != "2001:db8:1:2::/64" {
		t.Errorf("same /64 keys differ: %q %q", a, b)
	}
	if a, b := key(ra, req("[2001:db8:1:2::1]:443")), key(ra, req("[2001:db8:1:3::1]:443")); a == b {
		t.Errorf("two /64s share key %q", a)
	}
	if k := key(ra, req("203.0.113.7:5")); k != "203.0.113.7/32" {
		t.Errorf("v4 key %q", k)
	}
	if k := key(ra, req("[::ffff:203.0.113.7]:5")); k != "203.0.113.7/32" {
		t.Errorf("mapped key %q", k)
	}
	hf, err := clientIPFunc("header:X-Real-IP")
	if err != nil {
		t.Fatal(err)
	}
	if k := key(hf, req("10.0.0.1:1", "X-Real-Ip", "198.51.100.9")); k != "198.51.100.9/32" {
		t.Errorf("header key %q", k)
	}
	if k := key(hf, req("10.0.0.1:1", "X-Real-Ip", "not-an-ip")); k != "" {
		t.Errorf("unparseable header gave key %q", k)
	}
	if k := key(hf, req("10.0.0.1:1")); k != "" {
		t.Errorf("missing header gave key %q", k)
	}
	// Through the relay core: rotating inside one /64 drains one bucket, a
	// second /64 is untouched.
	rh := relay.New(nil, nil)
	for i := 0; i < hubproto.RelayDialBurstPerIP; i++ {
		p, _ := ra(req(fmt.Sprintf("[2001:db8:1:2::%x]:1", i+1)))
		if !rh.AllowIP(p) {
			t.Fatalf("dial %d refused inside the burst", i)
		}
	}
	p, _ := ra(req("[2001:db8:1:2:9::1]:1"))
	if rh.AllowIP(p) {
		t.Error("same /64 not throttled after the burst")
	}
	p, _ = ra(req("[2001:db8:1:3::1]:1"))
	if !rh.AllowIP(p) {
		t.Error("a different /64 shares the bucket")
	}
}

// An unknown --client-ip refuses to start before anything listens.
func TestServeRefusesBadClientIP(t *testing.T) {
	var errb lockedBuffer
	c := &cli{stdout: io.Discard, stderr: &errb, getenv: func(string) string { return "" }}
	if code := c.run([]string{"serve", "--data", filepath.Join(t.TempDir(), "h.json"), "--addr", "127.0.0.1:0", "--client-ip", "bogus"}); code == 0 {
		t.Fatal("bogus --client-ip started")
	}
}
