package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/relay"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// ---- harness -------------------------------------------------------------------

func relayEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	rh := relay.New(e.clk.Now, nil)
	e.withServer(func(c *Config) { c.Relay = rh })
	e.hub.acceptTimeout = 2 * time.Second
	t.Cleanup(rh.Shutdown)
	return e
}

func noProxy(*http.Request) (*url.URL, error) { return nil, nil }

// signedUpgrade builds a signed GET carrying the WebSocket upgrade headers.
func (d *daemon) signedUpgrade(path string) *http.Request {
	req, err := http.NewRequest(http.MethodGet, d.e.srv.URL+path, nil)
	if err != nil {
		d.e.t.Fatal(err)
	}
	if err := hubproto.SignAt(req, d.id, d.priv, nil, d.e.clk.Now(), d.e.nextNonce()); err != nil {
		d.e.t.Fatal(err)
	}
	return req
}

func (d *daemon) dialWS(path string) (*hubproto.WSConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return hubproto.WSDialer{Proxy: noProxy}.Dial(ctx, d.signedUpgrade(path))
}

func setUpgradeHeaders(h http.Header) {
	var k [16]byte
	_, _ = rand.Read(k[:])
	h.Set("Upgrade", "websocket")
	h.Set("Connection", "Upgrade")
	h.Set("Sec-WebSocket-Version", "13")
	h.Set("Sec-WebSocket-Key", base64.StdEncoding.EncodeToString(k[:]))
	h.Set("Sec-WebSocket-Protocol", hubproto.WSSubprotocol)
}

// control opens the daemon's control channel and consumes the hello.
func (d *daemon) control() *hubproto.WSConn {
	d.e.t.Helper()
	ws, err := d.dialWS(hubproto.PathRelayDaemon + "?v=1")
	if err != nil {
		d.e.t.Fatalf("control: %v", err)
	}
	d.e.t.Cleanup(func() { _ = ws.CloseNow(hubproto.WSCloseGoingAway) })
	if m := readMsg(d.e.t, ws); m.T != "hello" || m.V != hubproto.RelayControlVersion {
		d.e.t.Fatalf("hello = %+v", m)
	}
	return ws
}

func readMsg(t *testing.T, ws *hubproto.WSConn) hubproto.RelayMsg {
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

func sendMsg(t *testing.T, ws *hubproto.WSConn, m hubproto.RelayMsg) {
	t.Helper()
	b, _ := json.Marshal(m)
	if err := ws.WriteMessage(b); err != nil {
		t.Fatal(err)
	}
}

// phoneCred is one registered rendezvous.
type phoneCred struct{ rv, bearer string }

func newCred(t *testing.T) (phoneCred, hubproto.RelayRendezvous) {
	t.Helper()
	rv, err := hubproto.NewRelayID()
	if err != nil {
		t.Fatal(err)
	}
	b, h, err := hubproto.NewRelayBearer()
	if err != nil {
		t.Fatal(err)
	}
	return phoneCred{rv, b}, hubproto.RelayRendezvous{RV: rv, BearerSHA256: h}
}

func (d *daemon) register(entries ...hubproto.RelayRendezvous) (hubproto.RendezvousResponse, int) {
	d.e.t.Helper()
	if entries == nil {
		entries = []hubproto.RelayRendezvous{}
	}
	r := d.do(http.MethodPost, hubproto.PathRelayRendezvous, hubproto.RendezvousRequest{Entries: entries})
	var out hubproto.RendezvousResponse
	if r.status == 200 {
		r.decode(d.e.t, &out)
	}
	return out, r.status
}

// mustCred registers a fresh paired credential for d.
func (d *daemon) mustCred() phoneCred {
	d.e.t.Helper()
	c, ent := newCred(d.e.t)
	if _, st := d.register(ent); st != 200 {
		d.e.t.Fatalf("register: %d", st)
	}
	return c
}

func (e *env) phoneReq(rv, bearer string) *http.Request {
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+hubproto.PathRelayPhone, nil)
	if rv != "" {
		req.Header.Set(hubproto.HeaderRelayRV, rv)
	}
	if bearer != "" {
		req.Header.Set(hubproto.HeaderRelayBearer, bearer)
	}
	return req
}

func (e *env) dialPhone(c phoneCred) (*hubproto.WSConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, err := hubproto.WSDialer{Proxy: noProxy}.Dial(ctx, e.phoneReq(c.rv, c.bearer))
	if err == nil {
		e.t.Cleanup(func() { _ = ws.CloseNow(hubproto.WSCloseGoingAway) })
	}
	return ws, err
}

type refusal struct {
	status int
	body   string
	code   string // Lola-Relay-Code
	retry  string
}

// rawPhone sends an upgrade request with the given headers and returns the
// non-101 answer.
func (e *env) rawPhone(h http.Header) refusal {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+hubproto.PathRelayPhone, nil)
	setUpgradeHeaders(req.Header)
	for k, v := range h {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return refusal{resp.StatusCode, string(b), resp.Header.Get(hubproto.HeaderRelayCode), resp.Header.Get("Retry-After")}
}

func credHeaders(c phoneCred) http.Header {
	return http.Header{hubproto.HeaderRelayRV: {c.rv}, hubproto.HeaderRelayBearer: {c.bearer}}
}

// connect runs one whole dial: phone, control incoming, accept. It returns the
// phone leg and the daemon's data leg.
func (d *daemon) connect(ctrl *hubproto.WSConn, c phoneCred) (phone, leg *hubproto.WSConn) {
	d.e.t.Helper()
	type res struct {
		ws  *hubproto.WSConn
		err error
	}
	ch := make(chan res, 1)
	go func() { ws, err := d.e.dialPhone(c); ch <- res{ws, err} }()
	m := readMsg(d.e.t, ctrl)
	if m.T != "incoming" || m.RV != c.rv || !hubproto.ValidRelayID(m.Conn) {
		d.e.t.Fatalf("incoming = %+v", m)
	}
	leg, err := d.dialWS(hubproto.PathRelayAccept + "?conn=" + m.Conn)
	if err != nil {
		d.e.t.Fatalf("accept: %v", err)
	}
	d.e.t.Cleanup(func() { _ = leg.CloseNow(hubproto.WSCloseGoingAway) })
	r := <-ch
	if r.err != nil {
		d.e.t.Fatalf("phone: %v", r.err)
	}
	return r.ws, leg
}

func readCloseCode(t *testing.T, ws *hubproto.WSConn) uint16 {
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

func relayDaemon(e *env) *daemon {
	d := e.newDaemon()
	d.mustEnroll("acme")
	return d
}

// ---- end to end ------------------------------------------------------------------

func TestRelayEndToEnd(t *testing.T) {
	e := relayEnv(t)
	d := relayDaemon(e)
	c := d.mustCred()
	ctrl := d.control()
	phone, leg := d.connect(ctrl, c)

	const size = 1 << 20
	up := bytes.Repeat([]byte("u"), size)
	down := bytes.Repeat([]byte("d"), size)
	werr := make(chan error, 2)
	go func() { _, err := phone.Write(up); werr <- err }()
	go func() { _, err := leg.Write(down); werr <- err }()
	gotUp, gotDown := make([]byte, size), make([]byte, size)
	_ = leg.SetReadDeadline(time.Now().Add(20 * time.Second))
	_ = phone.SetReadDeadline(time.Now().Add(20 * time.Second))
	if _, err := io.ReadFull(leg, gotUp); err != nil || !bytes.Equal(gotUp, up) {
		t.Fatalf("up: %v", err)
	}
	if _, err := io.ReadFull(phone, gotDown); err != nil || !bytes.Equal(gotDown, down) {
		t.Fatalf("down: %v", err)
	}
	for range 2 {
		if err := <-werr; err != nil {
			t.Fatal(err)
		}
	}
	// A phone that hangs up ends both legs.
	_ = phone.Close()
	if code := readCloseCode(t, leg); code != hubproto.WSCloseNormal && code != hubproto.RelayClosePeerGone {
		t.Fatalf("daemon leg end code %d", code)
	}
}

// ---- phone leg refusals ----------------------------------------------------------------

func TestPhoneDialUniform401(t *testing.T) {
	e := relayEnv(t)
	d := relayDaemon(e)
	good := d.mustCred()
	unknownRV, _ := newCred(t)

	// A previous bearer, valid only until PrevUntil.
	oldBearer, oldHash, _ := hubproto.NewRelayBearer()
	rot, rotEnt := newCred(t)
	rotEnt.PrevSHA256, rotEnt.PrevUntil = oldHash, e.clk.Now().Add(time.Minute)
	// A window that lapses.
	win, winEnt := newCred(t)
	winEnt.ExpiresAt = e.clk.Now().Add(time.Minute)
	if _, st := d.register(mustEntry(good), rotEnt, winEnt); st != 200 {
		t.Fatalf("register %d", st)
	}
	// A revoked device's entry.
	dead := e.newDaemon()
	dead.mustEnroll("acme")
	deadCred := dead.mustCred()
	if err := e.st.Update(func(dd *store.Data) error {
		now := e.clk.Now()
		dd.Devices[dead.id].RevokedAt = &now
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	e.clk.Add(2 * time.Minute) // window and previous bearer are now past

	otherBearer, _, _ := hubproto.NewRelayBearer()
	cases := map[string]http.Header{
		"unknown rv":      credHeaders(phoneCred{unknownRV.rv, good.bearer}),
		"wrong bearer":    credHeaders(phoneCred{good.rv, otherBearer}),
		"expired window":  credHeaders(win),
		"revoked device":  credHeaders(deadCred),
		"prev past grace": credHeaders(phoneCred{rot.rv, oldBearer}),
		"authorization":   {hubproto.HeaderRelayRV: {good.rv}, "Authorization": {"Bearer " + good.bearer}},
		"no headers":      {},
		"malformed":       {hubproto.HeaderRelayRV: {"zz"}, hubproto.HeaderRelayBearer: {"zz"}},
	}
	var want *refusal
	for name, h := range cases {
		got := e.rawPhone(h)
		if got.status != 401 || got.code != hubproto.CodeUnauthorized {
			t.Fatalf("%s: %+v", name, got)
		}
		if want == nil {
			want = &got
		} else if got != *want {
			t.Fatalf("%s differs: %+v vs %+v", name, got, *want)
		}
	}
}

// mustEntry rebuilds the registered entry of a credential (registered whole).
func mustEntry(c phoneCred) hubproto.RelayRendezvous {
	return hubproto.RelayRendezvous{RV: c.rv, BearerSHA256: hubproto.RelayBearerHash(c.bearer)}
}

func TestPhoneDialOffline503(t *testing.T) {
	e := relayEnv(t)
	c := relayDaemon(e).mustCred()
	got := e.rawPhone(credHeaders(c))
	if got.status != 503 || got.code != hubproto.CodeOffline {
		t.Fatalf("%+v", got)
	}
	var er hubproto.ErrorResponse
	if json.Unmarshal([]byte(got.body), &er) != nil || er.Code != hubproto.CodeOffline || er.Error != "daemon not connected" {
		t.Fatalf("body %q", got.body)
	}
}

func TestPhoneDialTimeout504(t *testing.T) {
	e := relayEnv(t)
	e.hub.acceptTimeout = 150 * time.Millisecond
	d := relayDaemon(e)
	c := d.mustCred()
	ctrl := d.control()
	got := e.rawPhone(credHeaders(c))
	if got.status != 504 || got.code != hubproto.CodeRelayTimeout {
		t.Fatalf("%+v", got)
	}
	// The daemon was offered the dial and is told to drop it.
	if m := readMsg(t, ctrl); m.T != "incoming" {
		t.Fatalf("first %+v", m)
	}
	if m := readMsg(t, ctrl); m.T != "cancel" || !hubproto.ValidRelayID(m.Conn) {
		t.Fatalf("second %+v", m)
	}
}

func TestPhoneDialDaemonReject(t *testing.T) {
	e := relayEnv(t)
	d := relayDaemon(e)
	c := d.mustCred()
	ctrl := d.control()
	for _, tc := range []struct {
		reason string
		status int
		code   string
	}{{"busy", 503, hubproto.CodeBusy}, {"unknown", 401, hubproto.CodeUnauthorized}} {
		ch := make(chan refusal, 1)
		go func() { ch <- e.rawPhone(credHeaders(c)) }()
		m := readMsg(t, ctrl)
		if m.T == "cancel" { // the previous refusal's cleanup
			m = readMsg(t, ctrl)
		}
		if m.T != "incoming" {
			t.Fatalf("%+v", m)
		}
		sendMsg(t, ctrl, hubproto.RelayMsg{T: "reject", Conn: m.Conn, Reason: tc.reason})
		got := <-ch
		if got.status != tc.status || got.code != tc.code {
			t.Fatalf("%s: %+v", tc.reason, got)
		}
	}
}

func TestPhoneGetsNo101BeforeDaemon(t *testing.T) {
	e := relayEnv(t)
	d := relayDaemon(e)
	c := d.mustCred()
	ctrl := d.control()

	u, _ := url.Parse(e.srv.URL)
	nc, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	req := e.phoneReq(c.rv, c.bearer)
	setUpgradeHeaders(req.Header)
	if err := req.Write(nc); err != nil {
		t.Fatal(err)
	}
	m := readMsg(t, ctrl)
	if m.T != "incoming" {
		t.Fatalf("%+v", m)
	}
	_ = nc.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, err := nc.Read(make([]byte, 1)); n != 0 || !isTimeout(err) {
		t.Fatalf("bytes before the daemon accepted: n=%d err=%v", n, err)
	}
	leg, err := d.dialWS(hubproto.PathRelayAccept + "?conn=" + m.Conn)
	if err != nil {
		t.Fatal(err)
	}
	defer leg.CloseNow(hubproto.WSCloseGoingAway)
	_ = nc.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(nc).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "HTTP/1.1 101") {
		t.Fatalf("answer %q %v", line, err)
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// ---- accept leg ---------------------------------------------------------------------------

func TestAcceptAfterPhoneGoneClosesLeg(t *testing.T) {
	e := relayEnv(t)
	e.hub.acceptTimeout = 120 * time.Millisecond
	d := relayDaemon(e)
	c := d.mustCred()
	ctrl := d.control()
	// Two timed-out dials, then a third: had a reservation leaked, the per-rv
	// cap (2) would answer 503 busy instead of 504.
	var conns []string
	for i := range 3 {
		got := e.rawPhone(credHeaders(c))
		if got.status != 504 {
			t.Fatalf("dial %d: %+v", i, got)
		}
		in := readMsg(t, ctrl)
		readMsg(t, ctrl) // cancel
		conns = append(conns, in.Conn)
	}
	// The phone is gone: a late accept is a plain 404.
	_, err := d.dialWS(hubproto.PathRelayAccept + "?conn=" + conns[0])
	var he *hubproto.WSHandshakeError
	if !errors.As(err, &he) || he.Status != 404 {
		t.Fatalf("late accept: %v", err)
	}
}

func TestAcceptOtherDeviceConn404(t *testing.T) {
	e := relayEnv(t)
	a := relayDaemon(e)
	b := relayDaemon(e)
	c := a.mustCred()
	ctrl := a.control()
	ch := make(chan *hubproto.WSConn, 1)
	go func() { ws, _ := e.dialPhone(c); ch <- ws }()
	m := readMsg(t, ctrl)
	_, err := b.dialWS(hubproto.PathRelayAccept + "?conn=" + m.Conn)
	var he *hubproto.WSHandshakeError
	if !errors.As(err, &he) || he.Status != 404 {
		t.Fatalf("other device: %v", err)
	}
	// The owner still can.
	leg, err := a.dialWS(hubproto.PathRelayAccept + "?conn=" + m.Conn)
	if err != nil {
		t.Fatal(err)
	}
	defer leg.CloseNow(hubproto.WSCloseGoingAway)
	if ws := <-ch; ws == nil {
		t.Fatal("phone not connected")
	}
}

func TestAcceptReplayRefused(t *testing.T) {
	e := relayEnv(t)
	d := relayDaemon(e)
	req := d.signedUpgrade(hubproto.PathRelayAccept + "?conn=" + strings.Repeat("a", 32))
	setUpgradeHeaders(req.Header)
	send := func() (int, string) {
		resp, err := http.DefaultClient.Do(req.Clone(context.Background()))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var er hubproto.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&er)
		return resp.StatusCode, er.Code
	}
	if st, code := send(); st != 404 {
		t.Fatalf("first %d %s", st, code)
	}
	if st, code := send(); st != 401 || code != hubproto.CodeReplay {
		t.Fatalf("replay %d %s", st, code)
	}
}

// ---- control channel -------------------------------------------------------------------------

func TestControlReplaced(t *testing.T) {
	e := relayEnv(t)
	d := relayDaemon(e)
	first := d.control()
	_ = d.control()
	if m := readMsg(t, first); m.T != "bye" || m.Reason != "replaced" {
		t.Fatalf("%+v", m)
	}
	if code := readCloseCode(t, first); code != hubproto.RelayCloseReplaced {
		t.Fatalf("close %d", code)
	}
}

func TestControlRequiresVersion(t *testing.T) {
	e := relayEnv(t)
	d := relayDaemon(e)
	for _, q := range []string{"", "?v=2", "?v=1&x=1"} {
		_, err := d.dialWS(hubproto.PathRelayDaemon + q)
		var he *hubproto.WSHandshakeError
		if !errors.As(err, &he) || he.Status != 400 {
			t.Fatalf("%q: %v", q, err)
		}
	}
}

// ---- rendezvous ----------------------------------------------------------------------------------

func TestRendezvousReplaceAll(t *testing.T) {
	e := relayEnv(t)
	d := relayDaemon(e)
	_, a := newCred(t)
	_, b := newCred(t)
	_, c := newCred(t)
	if resp, st := d.register(a, b); st != 200 || resp.Registered != 2 || len(resp.Conflicts) != 0 {
		t.Fatalf("%d %+v", st, resp)
	}
	if resp, st := d.register(b, c); st != 200 || resp.Registered != 2 {
		t.Fatalf("%d %+v", st, resp)
	}
	has := func(rv string) (ok bool) {
		_ = e.st.View(func(dd *store.Data) error { _, ok = dd.Relay[rv]; return nil })
		return
	}
	if has(a.RV) || !has(b.RV) || !has(c.RV) {
		t.Fatal("replace-all did not keep exactly {b, c}")
	}
	if _, st := d.register(); st != 200 || has(b.RV) || has(c.RV) {
		t.Fatal("empty set did not drop everything")
	}
	// Only hashes are stored.
	_, ent := newCred(t)
	d.register(ent)
	_ = e.st.View(func(dd *store.Data) error {
		if got := dd.Relay[ent.RV]; got == nil || got.BearerHash != ent.BearerSHA256 || got.Org != "acme" || got.Device != d.id {
			t.Fatalf("entry %+v", got)
		}
		return nil
	})
	// An invalid set is a 400 and changes nothing.
	bad := ent
	bad.BearerSHA256 = "nope"
	if _, st := d.register(bad); st != 400 || !has(ent.RV) {
		t.Fatalf("invalid set: %d", st)
	}
}

func TestRendezvousConflictDoesNotBlockOthers(t *testing.T) {
	e := relayEnv(t)
	a := relayDaemon(e)
	b := relayDaemon(e)
	_, squatted := newCred(t)
	_, mine := newCred(t)
	_, gone := newCred(t)
	a.register(squatted)
	b.register(mine, gone)
	resp, st := b.register(squatted, mine)
	if st != 200 || resp.Registered != 1 || len(resp.Conflicts) != 1 || resp.Conflicts[0] != squatted.RV {
		t.Fatalf("%d %+v", st, resp)
	}
	_ = e.st.View(func(d *store.Data) error {
		if d.Relay[squatted.RV].Device != a.id {
			t.Fatal("a's rendezvous was taken")
		}
		if d.Relay[gone.RV] != nil || d.Relay[mine.RV] == nil {
			t.Fatal("the removal in the same request did not commit")
		}
		return nil
	})
}

func TestRendezvousRemovalClosesSplice(t *testing.T) {
	e := relayEnv(t)
	d := relayDaemon(e)
	c := d.mustCred()
	ctrl := d.control()
	phone, leg := d.connect(ctrl, c)
	if _, st := d.register(); st != 200 {
		t.Fatal(st)
	}
	if code := readCloseCode(t, phone); code != hubproto.RelayCloseRevoked {
		t.Fatalf("phone close %d", code)
	}
	if code := readCloseCode(t, leg); code != hubproto.RelayCloseRevoked {
		t.Fatalf("daemon close %d", code)
	}
}

func windowEntry(t *testing.T, e *env) (phoneCred, hubproto.RelayRendezvous) {
	c, ent := newCred(t)
	ent.ExpiresAt = e.clk.Now().Add(5 * time.Minute)
	return c, ent
}

func TestWindowRemovalKeepsSplice(t *testing.T) {
	e := relayEnv(t)
	d := relayDaemon(e)
	c, ent := windowEntry(t, e)
	if _, st := d.register(ent); st != 200 {
		t.Fatal(st)
	}
	ctrl := d.control()
	phone, leg := d.connect(ctrl, c)
	if _, st := d.register(); st != 200 { // the window is closed
		t.Fatal(st)
	}
	// The pairing connection keeps working.
	if _, err := phone.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	_ = leg.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(leg, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("splice cut: %v %q", err, buf)
	}
	// A pending dial on the removed window fails.
	c2, ent2 := windowEntry(t, e)
	d.register(ent2)
	ch := make(chan refusal, 1)
	go func() { ch <- e.rawPhone(credHeaders(c2)) }()
	if m := readMsg(t, ctrl); m.T != "incoming" {
		t.Fatalf("%+v", m)
	}
	d.register()
	if got := <-ch; got.status != 503 || got.code != hubproto.CodeOffline {
		t.Fatalf("pending dial: %+v", got)
	}
}

func TestRecheckClosesRevoked(t *testing.T) {
	e := relayEnv(t)
	d := relayDaemon(e)
	c := d.mustCred()
	ctrl := d.control()
	phone, leg := d.connect(ctrl, c)
	// A CLI-style revocation: another process writes the store.
	if err := e.st.Update(func(dd *store.Data) error {
		now := e.clk.Now()
		dd.Devices[d.id].RevokedAt = &now
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	e.hub.RelaySweep()
	if code := readCloseCode(t, phone); code != hubproto.RelayCloseRevoked {
		t.Fatalf("phone close %d", code)
	}
	if code := readCloseCode(t, leg); code != hubproto.RelayCloseRevoked {
		t.Fatalf("daemon close %d", code)
	}
	if m := readMsg(t, ctrl); m.T != "bye" || m.Reason != "revoked" {
		t.Fatalf("%+v", m)
	}
	_ = e.st.View(func(dd *store.Data) error {
		if len(dd.Relay) != 0 {
			t.Fatal("a revoked device's entries survived the sweep")
		}
		return nil
	})
}

func TestRelaySweepDropsExpired(t *testing.T) {
	e := relayEnv(t)
	d := relayDaemon(e)
	_, win := windowEntry(t, e)
	_, keep := newCred(t)
	_, rot := newCred(t)
	_, prev, _ := hubproto.NewRelayBearer()
	rot.PrevSHA256, rot.PrevUntil = prev, e.clk.Now().Add(time.Hour)
	d.register(win, keep, rot)
	e.clk.Add(2 * time.Hour)
	e.hub.RelaySweep()
	_ = e.st.View(func(dd *store.Data) error {
		if dd.Relay[win.RV] != nil || dd.Relay[keep.RV] == nil || dd.Relay[rot.RV] == nil || dd.Relay[rot.RV].PrevHash != "" {
			t.Fatalf("sweep: %+v", dd.Relay)
		}
		return nil
	})
}

// ---- upgrade and routing -----------------------------------------------------------------------

func TestOriginRefused403(t *testing.T) {
	e := relayEnv(t)
	d := relayDaemon(e)
	c := d.mustCred()
	for _, tc := range []struct {
		name string
		req  *http.Request
	}{
		{"daemon", d.signedUpgrade(hubproto.PathRelayDaemon + "?v=1")},
		{"accept", d.signedUpgrade(hubproto.PathRelayAccept + "?conn=" + strings.Repeat("b", 32))},
		{"phone", e.phoneReq(c.rv, c.bearer)},
	} {
		setUpgradeHeaders(tc.req.Header)
		tc.req.Header.Set("Origin", "https://evil.example")
		resp, err := http.DefaultClient.Do(tc.req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Fatalf("%s: %d", tc.name, resp.StatusCode)
		}
	}
}

func TestNotAnUpgradeIs400(t *testing.T) {
	e := relayEnv(t)
	d := relayDaemon(e)
	req := d.signedUpgrade(hubproto.PathRelayDaemon + "?v=1") // no upgrade headers
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("%d", resp.StatusCode)
	}
}

func TestRelayDisabledIs404(t *testing.T) {
	e := newEnv(t) // Config.Relay == nil
	d := e.newDaemon()
	d.mustEnroll("acme")
	for _, path := range []string{
		hubproto.PathRelayRendezvous, hubproto.PathRelayDaemon + "?v=1",
		hubproto.PathRelayAccept + "?conn=" + strings.Repeat("c", 32), hubproto.PathRelayPhone,
	} {
		method := http.MethodGet
		if path == hubproto.PathRelayRendezvous {
			method = http.MethodPost
		}
		req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader("{}"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var er hubproto.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&er)
		resp.Body.Close()
		if resp.StatusCode != 404 || er.Code != hubproto.CodeNotFound {
			t.Fatalf("%s: %d %s", path, resp.StatusCode, er.Code)
		}
	}
	e.hub.RelaySweep() // a no-op without a core
}

func TestRevokeEndpointRechecksRelay(t *testing.T) {
	// The org-admin revoke runs the recheck itself; here its helper must close
	// a device the store already reports revoked, with no sweep.
	e := relayEnv(t)
	d := relayDaemon(e)
	ctrl := d.control()
	_ = e.st.Update(func(dd *store.Data) error {
		now := e.clk.Now()
		dd.Devices[d.id].RevokedAt = &now
		return nil
	})
	e.hub.RelayRecheck()
	if m := readMsg(t, ctrl); m.T != "bye" || m.Reason != "revoked" {
		t.Fatalf("%+v", m)
	}
}
