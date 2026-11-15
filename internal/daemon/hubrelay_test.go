package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/devices"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/mdns"
	"github.com/sushidev-team/lola/internal/panebus"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
)

// relayHubFake is a loopback stand-in for the hub's relay endpoints. It does
// not verify signatures (internal/hub's tests pin that); it speaks the real
// hubproto WebSocket codec.
type relayHubFake struct {
	t   *testing.T
	srv *httptest.Server

	mu          sync.Mutex
	posts       [][]hubproto.RelayRendezvous
	postStatus  int
	postCode    string
	conflicts   func([]hubproto.RelayRendezvous) []string
	postGate    chan struct{} // when set, the FIRST post waits on it
	ctlStatus   int
	ctlCode     string
	ctlMsg      string
	ctlHang     bool
	hello       hubproto.RelayMsg
	cur         *hubproto.WSConn
	controlHits int
	accepts     chan *hubproto.WSConn
	rejects     chan hubproto.RelayMsg
	gated       bool
}

func newRelayHubFake(t *testing.T) *relayHubFake {
	t.Helper()
	f := &relayHubFake{
		t:       t,
		hello:   hubproto.RelayMsg{T: "hello", V: hubproto.RelayControlVersion, PingS: 25, Max: 4},
		accepts: make(chan *hubproto.WSConn, 8),
		rejects: make(chan hubproto.RelayMsg, 16),
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *relayHubFake) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case hubproto.PathRelayRendezvous:
		f.rendezvous(w, r)
	case hubproto.PathRelayDaemon:
		f.control(w, r)
	case hubproto.PathRelayAccept:
		ws, err := hubproto.AcceptWebSocket(w, r, hubproto.WSConfig{})
		if err == nil {
			f.accepts <- ws
		}
	default:
		http.NotFound(w, r)
	}
}

func (f *relayHubFake) fail(w http.ResponseWriter, status int, code, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(hubproto.ErrorResponse{Error: msg, Code: code})
}

func (f *relayHubFake) rendezvous(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	gate := f.postGate
	first := gate != nil && !f.gated
	if first {
		f.gated = true
	}
	f.mu.Unlock()
	if first {
		<-gate
	}
	var req hubproto.RendezvousRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Validate(time.Now()) != nil {
		f.fail(w, 400, hubproto.CodeBadRequest, "bad")
		return
	}
	f.mu.Lock()
	status, code := f.postStatus, f.postCode
	if status == 0 {
		f.posts = append(f.posts, req.Entries)
	}
	conflicts := f.conflicts
	f.mu.Unlock()
	if status != 0 {
		if status == 429 {
			w.Header().Set("Retry-After", "1")
		}
		f.fail(w, status, code, strings.Repeat("x", 4<<10))
		return
	}
	resp := hubproto.RendezvousResponse{Registered: len(req.Entries)}
	if conflicts != nil {
		resp.Conflicts = conflicts(req.Entries)
		resp.Registered -= len(resp.Conflicts)
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (f *relayHubFake) control(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.controlHits++
	status, code, msg, hang, hello := f.ctlStatus, f.ctlCode, f.ctlMsg, f.ctlHang, f.hello
	f.mu.Unlock()
	if hang {
		<-r.Context().Done()
		return
	}
	if status != 0 {
		f.fail(w, status, code, msg)
		return
	}
	ws, err := hubproto.AcceptWebSocket(w, r, hubproto.WSConfig{})
	if err != nil {
		return
	}
	b, _ := json.Marshal(hello)
	_ = ws.WriteMessage(b)
	f.mu.Lock()
	f.cur = ws
	f.mu.Unlock()
	for {
		raw, err := ws.ReadMessage(hubproto.RelayMaxControlMessage)
		if err != nil {
			return
		}
		var m hubproto.RelayMsg
		if json.Unmarshal(raw, &m) == nil && m.T == "reject" {
			f.rejects <- m
		}
	}
}

func (f *relayHubFake) send(m hubproto.RelayMsg) {
	f.t.Helper()
	f.mu.Lock()
	ws := f.cur
	f.mu.Unlock()
	if ws == nil {
		f.t.Fatal("no control channel")
	}
	b, _ := json.Marshal(m)
	if err := ws.WriteMessage(b); err != nil {
		f.t.Fatalf("send: %v", err)
	}
}

func (f *relayHubFake) postCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.posts)
}

func (f *relayHubFake) lastPost() []hubproto.RelayRendezvous {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.posts) == 0 {
		return nil
	}
	return f.posts[len(f.posts)-1]
}

func relayWaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (d *Daemon) relayStateNow() (string, string) {
	r := d.relay()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state, r.reason
}

func relayWaitState(t *testing.T, d *Daemon, state string) {
	t.Helper()
	relayWaitFor(t, "relay state "+state, func() bool { s, _ := d.relayStateNow(); return s == state })
}

// relayDaemon is a joined daemon whose [remote] is on with bind = "off", so the
// only server it has is a relay-only one.
type relayDaemon struct {
	*Daemon
	log  *lockedWriter
	fake *relayHubFake
}

func newRelayDaemon(t *testing.T, f *relayHubFake) *relayDaemon {
	t.Helper()
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	cfg := remoteTestConfig()
	cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "off"}
	cfg.Hub = config.HubConfig{Enabled: true, URL: f.srv.URL}
	lw := &lockedWriter{w: &bytes.Buffer{}}
	d := newDaemon(cfg, &linear.Fake{}, log.New(lw, "", 0), home)
	d.paneRegistry = func() *panebus.Registry { return panebus.NewFake().Registry() }
	rd := &relayDaemon{Daemon: d, log: lw, fake: f}
	rd.join(t, f.srv.URL)
	r := d.relay()
	r.mu.Lock()
	r.delay = func(int, time.Duration) time.Duration { return 10 * time.Millisecond }
	r.mu.Unlock()
	return rd
}

type lockedWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (rd *relayDaemon) logs() string {
	rd.log.mu.Lock()
	defer rd.log.mu.Unlock()
	return rd.log.w.String()
}

// join writes the identity and key of a device enrolled at url.
func (rd *relayDaemon) join(t *testing.T, url string) {
	t.Helper()
	dir := rd.hubDir()
	key, err := hub.LoadOrCreateKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := hub.SaveIdentity(dir, hub.Identity{URL: url, DeviceID: key.DeviceID(), Org: "acme"}); err != nil {
		t.Fatal(err)
	}
}

func (rd *relayDaemon) serve(t *testing.T) {
	t.Helper()
	rd.startRemote(context.Background())
	t.Cleanup(rd.stopRemote)
	if rd.remote == nil {
		t.Fatal("no remote server")
	}
}

// run starts the loop the way Run does and returns a stop that waits for d.wg.
func (rd *relayDaemon) run(t *testing.T) (stop func() time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	rd.wg.Add(1)
	go rd.hubRelayLoop(ctx)
	var once sync.Once
	stop = func() time.Duration {
		var took time.Duration
		once.Do(func() {
			start := time.Now()
			cancel()
			rd.wg.Wait()
			took = time.Since(start)
		})
		return took
	}
	t.Cleanup(func() { stop() })
	return stop
}

var relaySPKI byte

func (rd *relayDaemon) pair(t *testing.T, withRelay bool) devices.Device {
	t.Helper()
	relaySPKI++
	var rl *devices.DeviceRelay
	if withRelay {
		rv, _ := hubproto.NewRelayID()
		_, h, _ := hubproto.NewRelayBearer()
		rl = &devices.DeviceRelay{RV: rv, BearerHash: h, RotatedAt: time.Now()}
	}
	dev, err := rd.devices.Add("phone", "ios", [32]byte{relaySPKI, 7}, []string{protocol.TierRead}, rl)
	if err != nil {
		t.Fatal(err)
	}
	return dev
}

func TestRelayStateMachine(t *testing.T) {
	t.Run("off reasons", func(t *testing.T) {
		cases := map[string]func(*relayDaemon){
			"disabled":   func(rd *relayDaemon) { rd.cfg.Remote.RelayOff = true },
			"hub_off":    func(rd *relayDaemon) { rd.cfg.Hub.Enabled = false },
			"not_joined": func(rd *relayDaemon) { _ = hub.RemoveIdentity(rd.hubDir()) },
			"remote_off": nil,
			"no_phones":  nil,
		}
		for want, mut := range cases {
			t.Run(want, func(t *testing.T) {
				f := newRelayHubFake(t)
				rd := newRelayDaemon(t, f)
				if want != "no_phones" {
					rd.pair(t, true)
				}
				if want != "remote_off" {
					rd.serve(t)
				}
				if mut != nil {
					mut(rd)
				}
				rd.run(t)
				relayWaitFor(t, "off/"+want, func() bool { s, r := rd.relayStateNow(); return s == protocol.HubRelayStateOff && r == want })
				f.mu.Lock()
				hits := f.controlHits
				f.mu.Unlock()
				if hits != 0 {
					t.Error("an off relay must not dial the hub")
				}
			})
		}
	})

	t.Run("connects and registers", func(t *testing.T) {
		f := newRelayHubFake(t)
		rd := newRelayDaemon(t, f)
		dev := rd.pair(t, true)
		rd.serve(t)
		rd.run(t)
		relayWaitState(t, rd.Daemon, protocol.HubRelayStateConnected)
		got := f.lastPost()
		if len(got) != 1 || got[0].RV != dev.Relay.RV || got[0].BearerSHA256 != dev.Relay.BearerHash {
			t.Fatalf("registered %+v", got)
		}
		relayWaitFor(t, "status file", func() bool {
			st, ok := hub.LoadRelayStatus(rd.hubDir())
			return ok && st.State == protocol.HubRelayStateConnected && st.Registered == 1 && st.RegisteredURL == f.srv.URL
		})
	})

	t.Run("404 is unsupported until the recheck", func(t *testing.T) {
		f := newRelayHubFake(t)
		f.postStatus = 404
		rd := newRelayDaemon(t, f)
		rd.pair(t, true)
		rd.serve(t)
		r := rd.relay()
		r.mu.Lock()
		r.unsupportedRecheck = 50 * time.Millisecond
		r.mu.Unlock()
		rd.run(t)
		relayWaitState(t, rd.Daemon, protocol.HubRelayStateUnsupported)
		f.mu.Lock()
		f.postStatus = 0
		f.mu.Unlock()
		relayWaitState(t, rd.Daemon, protocol.HubRelayStateConnected)
	})

	t.Run("401 refuses and revokes", func(t *testing.T) {
		f := newRelayHubFake(t)
		f.postStatus, f.postCode = 401, hubproto.CodeUnauthorized
		rd := newRelayDaemon(t, f)
		rd.pair(t, true)
		rd.serve(t)
		reloaded := make(chan struct{}, 4)
		rd.hubWire().reload = func(context.Context) error { reloaded <- struct{}{}; return nil }
		rd.run(t)
		relayWaitFor(t, "revocation record", func() bool { _, ok := hub.LoadRevocation(rd.hubDir()); return ok })
		select {
		case <-reloaded:
		case <-time.After(2 * time.Second):
			t.Fatal("revocation did not reload")
		}
		relayWaitFor(t, "refused or revoked", func() bool {
			s, r := rd.relayStateNow()
			return s == protocol.HubRelayStateRefused || (s == protocol.HubRelayStateOff && r == "revoked")
		})
	})

	t.Run("429 floors the backoff", func(t *testing.T) {
		f := newRelayHubFake(t)
		f.postStatus = 429
		rd := newRelayDaemon(t, f)
		rd.pair(t, true)
		rd.serve(t)
		floors := make(chan time.Duration, 4)
		r := rd.relay()
		r.mu.Lock()
		r.delay = func(_ int, floor time.Duration) time.Duration { floors <- floor; return time.Hour }
		r.mu.Unlock()
		rd.run(t)
		select {
		case fl := <-floors:
			if fl < hubproto.RelayRateLimitedFloor {
				t.Fatalf("floor %v < %v", fl, hubproto.RelayRateLimitedFloor)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no backoff")
		}
	})

	t.Run("backoff delay bounds", func(t *testing.T) {
		for fails := 0; fails < 12; fails++ {
			ceil := min(hubproto.RelayBackoffMax, hubproto.RelayBackoffMin<<fails)
			for range 50 {
				if d := relayBackoffDelay(fails, 0); d < 0 || d >= ceil {
					t.Fatalf("fails=%d delay %v outside [0,%v)", fails, d, ceil)
				}
			}
		}
		if d := relayBackoffDelay(0, time.Minute); d < time.Minute {
			t.Fatalf("floor ignored: %v", d)
		}
	})
}

// hubWire is the daemon's hub loop state (test accessor for the reload seam).
func (rd *relayDaemon) hubWire() *hubWire { return rd.Daemon.hub }

func TestRelayHandshakeErrorIsCleaned(t *testing.T) {
	f := newRelayHubFake(t)
	f.ctlStatus, f.ctlCode, f.ctlMsg = 429, "Not-A-Known_Code!", strings.Repeat("x", 4<<10)
	rd := newRelayDaemon(t, f)
	rd.pair(t, true)
	rd.serve(t)
	rd.run(t)
	relayWaitFor(t, "a recorded error", func() bool {
		r := rd.relay()
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.lastErr != ""
	})
	st := rd.relayStatus()
	raw, _ := json.Marshal(rd.hubStatusData())
	file, _ := json.Marshal(func() hub.RelayStatus { s, _ := hub.LoadRelayStatus(rd.hubDir()); return s }())
	for name, b := range map[string][]byte{"hubStatus": raw, "status file": file} {
		if strings.Contains(string(b), "xxx") || strings.Contains(string(b), "Not-A-Known") {
			t.Errorf("%s carries hub text: %s", name, b)
		}
	}
	if st == nil || len(st.LastError) > relayErrMax {
		t.Errorf("last error %+v", st)
	}
}

func TestRelayHelloValuesIgnored(t *testing.T) {
	f := newRelayHubFake(t)
	f.hello = hubproto.RelayMsg{T: "hello", V: 1, PingS: 0, Max: 999}
	rd := newRelayDaemon(t, f)
	dev := rd.pair(t, true)
	rd.serve(t)
	rd.run(t)
	relayWaitState(t, rd.Daemon, protocol.HubRelayStateConnected)
	// The slot count stays the daemon's own: a third conn on one rv is refused.
	s1, ok1 := rd.remote.ReserveRelay(dev.Relay.RV)
	s2, ok2 := rd.remote.ReserveRelay(dev.Relay.RV)
	_, ok3 := rd.remote.ReserveRelay(dev.Relay.RV)
	s1.Release()
	s2.Release()
	if !ok1 || !ok2 || ok3 {
		t.Fatalf("slots %v %v %v", ok1, ok2, ok3)
	}
}

func TestRelayUnsupportedHelloVersion(t *testing.T) {
	f := newRelayHubFake(t)
	f.hello = hubproto.RelayMsg{T: "hello", V: 99}
	rd := newRelayDaemon(t, f)
	rd.pair(t, true)
	rd.serve(t)
	rd.run(t)
	relayWaitState(t, rd.Daemon, protocol.HubRelayStateUnsupported)
}

func TestRelayLoopNeverBlocksShutdown(t *testing.T) {
	limit := hubproto.WSControlWriteTimeout + 100*time.Millisecond
	t.Run("connected", func(t *testing.T) {
		f := newRelayHubFake(t)
		rd := newRelayDaemon(t, f)
		rd.pair(t, true)
		rd.serve(t)
		stop := rd.run(t)
		relayWaitState(t, rd.Daemon, protocol.HubRelayStateConnected)
		if took := stop(); took > limit {
			t.Fatalf("shutdown took %v", took)
		}
	})
	t.Run("dialling control", func(t *testing.T) {
		f := newRelayHubFake(t)
		f.ctlHang = true
		rd := newRelayDaemon(t, f)
		rd.pair(t, true)
		rd.serve(t)
		stop := rd.run(t)
		relayWaitState(t, rd.Daemon, protocol.HubRelayStateConnecting)
		if took := stop(); took > limit {
			t.Fatalf("shutdown took %v", took)
		}
	})
	t.Run("accept dial hangs", func(t *testing.T) {
		f := newRelayHubFake(t)
		rd := newRelayDaemon(t, f)
		dev := rd.pair(t, true)
		rd.serve(t)
		stop := rd.run(t)
		relayWaitState(t, rd.Daemon, protocol.HubRelayStateConnected)
		// The accept endpoint of this fake answers, so make it hang by using a
		// conn id the fake never completes: block the handler on a closed hub.
		f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == hubproto.PathRelayAccept {
				<-r.Context().Done()
				return
			}
			f.serve(w, r)
		})
		conn, _ := hubproto.NewRelayID()
		f.send(hubproto.RelayMsg{T: "incoming", Conn: conn, RV: dev.Relay.RV})
		relayWaitFor(t, "accept in flight", func() bool {
			r := rd.relay()
			r.mu.Lock()
			defer r.mu.Unlock()
			return len(r.inflight) == 1
		})
		if took := stop(); took > limit {
			t.Fatalf("shutdown took %v", took)
		}
	})
}

func TestRelayIncomingRejects(t *testing.T) {
	f := newRelayHubFake(t)
	rd := newRelayDaemon(t, f)
	dev := rd.pair(t, true)
	rd.serve(t)
	rd.run(t)
	relayWaitState(t, rd.Daemon, protocol.HubRelayStateConnected)

	expect := func(why string, want string) {
		t.Helper()
		select {
		case m := <-f.rejects:
			if m.Reason != want {
				t.Fatalf("%s: reject %q, want %q", why, m.Reason, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: no reject", why)
		}
	}
	other, _ := hubproto.NewRelayID()
	conn, _ := hubproto.NewRelayID()
	f.send(hubproto.RelayMsg{T: "incoming", Conn: conn, RV: other})
	expect("unknown rv", "unknown")

	s1, _ := rd.remote.ReserveRelay(dev.Relay.RV)
	s2, _ := rd.remote.ReserveRelay(dev.Relay.RV)
	f.send(hubproto.RelayMsg{T: "incoming", Conn: conn, RV: dev.Relay.RV})
	expect("per-rv full", "busy")
	s1.Release()
	s2.Release()

	r := rd.relay()
	for r.incoming.take() {
	}
	f.send(hubproto.RelayMsg{T: "incoming", Conn: conn, RV: dev.Relay.RV})
	expect("bucket empty", "busy")
	if len(f.accepts) != 0 {
		t.Error("a rejected incoming dialled accept")
	}
}

func TestRelayDirtyResyncs(t *testing.T) {
	f := newRelayHubFake(t)
	rd := newRelayDaemon(t, f)
	rd.pair(t, true)
	rd.serve(t)
	rd.run(t)
	relayWaitState(t, rd.Daemon, protocol.HubRelayStateConnected)
	n := f.postCount()
	second := rd.pair(t, true) // Add notifies d.devices subscribers -> hubRelayDirty
	relayWaitFor(t, "resync", func() bool { return f.postCount() > n })
	if got := f.lastPost(); len(got) != 2 {
		t.Fatalf("resync set %+v", got)
	}
	_ = second
}

func TestRelaySyncSerialized(t *testing.T) {
	f := newRelayHubFake(t)
	f.postGate = make(chan struct{})
	rd := newRelayDaemon(t, f)
	a := rd.pair(t, true)
	b := rd.pair(t, true)
	rd.serve(t)
	api, _, id, err := rd.relay().newClient(f.srv.URL, rd.hubDir())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = rd.relaySync(context.Background(), api, id, f.srv.URL) }()
	time.Sleep(50 * time.Millisecond) // the first POST is now parked at the gate
	if _, err := rd.devices.Revoke(b.ID); err != nil {
		t.Fatal(err)
	}
	go func() { defer wg.Done(); _ = rd.relaySync(context.Background(), api, id, f.srv.URL) }()
	time.Sleep(50 * time.Millisecond)
	close(f.postGate)
	wg.Wait()
	last := f.lastPost()
	if len(last) != 1 || last[0].RV != a.Relay.RV {
		t.Fatalf("the revoked rv came back: %+v", last)
	}
}

func TestRelayRevokeLastPhonePostsEmpty(t *testing.T) {
	f := newRelayHubFake(t)
	rd := newRelayDaemon(t, f)
	dev := rd.pair(t, true)
	rd.serve(t)
	rd.run(t)
	relayWaitState(t, rd.Daemon, protocol.HubRelayStateConnected)
	n := f.postCount()
	if _, err := rd.devices.Revoke(dev.ID); err != nil {
		t.Fatal(err)
	}
	relayWaitFor(t, "off/no_phones", func() bool { s, r := rd.relayStateNow(); return s == protocol.HubRelayStateOff && r == "no_phones" })
	if got := f.postCount() - n; got != 1 {
		t.Fatalf("%d posts after the revoke, want exactly 1", got)
	}
	if got := f.lastPost(); len(got) != 0 {
		t.Fatalf("last post %+v, want empty", got)
	}
}

func TestRelayReleaseOnLeaveAndURLChange(t *testing.T) {
	seed := func(rd *relayDaemon, url string) {
		r := rd.relay()
		key, _ := hub.LoadKey(rd.hubDir())
		r.mu.Lock()
		r.reg = hub.RelayStatus{Registered: 2, RegisteredURL: url, RegisteredDevice: key.DeviceID()}
		r.mu.Unlock()
	}
	t.Run("leave posts before the identity goes", func(t *testing.T) {
		f := newRelayHubFake(t)
		rd := newRelayDaemon(t, f)
		seed(rd, f.srv.URL)
		rd.hubRelayRelease(context.Background())
		if f.postCount() != 1 || len(f.lastPost()) != 0 {
			t.Fatalf("posts %v", f.posts)
		}
		if st, _ := hub.LoadRelayStatus(rd.hubDir()); st.Registered != 0 {
			t.Fatalf("registered still %d", st.Registered)
		}
		rd.hubRelayRelease(context.Background())
		if f.postCount() != 1 {
			t.Error("a released set was posted again")
		}
	})
	t.Run("url change posts to the old hub", func(t *testing.T) {
		old, fresh := newRelayHubFake(t), newRelayHubFake(t)
		rd := newRelayDaemon(t, old)
		rd.pair(t, true)
		rd.serve(t)
		seed(rd, old.srv.URL)
		rd.mu.Lock()
		rd.cfg.Hub.URL = fresh.srv.URL
		rd.mu.Unlock()
		rd.run(t)
		relayWaitFor(t, "release on the old hub", func() bool { return old.postCount() == 1 })
		if fresh.postCount() != 0 {
			t.Error("the new hub got the old registrations")
		}
	})
	t.Run("relay off posts", func(t *testing.T) {
		f := newRelayHubFake(t)
		rd := newRelayDaemon(t, f)
		rd.pair(t, true)
		rd.serve(t)
		seed(rd, f.srv.URL)
		rd.mu.Lock()
		rd.cfg.Remote.RelayOff = true
		rd.mu.Unlock()
		rd.run(t)
		relayWaitFor(t, "release", func() bool { return f.postCount() == 1 && len(f.lastPost()) == 0 })
	})
	t.Run("restart with registrations posts", func(t *testing.T) {
		f := newRelayHubFake(t)
		rd := newRelayDaemon(t, f)
		key, _ := hub.LoadKey(rd.hubDir())
		if err := hub.SaveRelayStatus(rd.hubDir(), hub.RelayStatus{State: "connected", Registered: 1, RegisteredURL: f.srv.URL, RegisteredDevice: key.DeviceID()}); err != nil {
			t.Fatal(err)
		}
		rd.cfg.Remote.RelayOff = true
		rd.hubRelay = &hubRelay{} // a restart: the persisted status is read afresh
		rd.run(t)
		relayWaitFor(t, "release after restart", func() bool { return f.postCount() == 1 })
	})
}

func TestRelayEntriesNoDuplicateRV(t *testing.T) {
	f := newRelayHubFake(t)
	rd := newRelayDaemon(t, f)
	rd.serve(t)
	tk, err := rd.pairer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	rv, _ := hubproto.NewRelayID()
	_, hash, _ := hubproto.NewRelayBearer()
	if !rd.pairer.SetWindowRelay(tk.ID, rv, hash) {
		t.Fatal("window refused a relay")
	}
	// A device with a live and an expired previous bearer.
	live := rd.pair(t, true)
	expired := rd.pair(t, true)
	now := time.Now()
	for _, dv := range []struct {
		d     devices.Device
		until time.Time
	}{{live, now.Add(time.Hour)}, {expired, now.Add(-time.Hour)}} {
		_, ph, _ := hubproto.NewRelayBearer()
		rl := *dv.d.Relay
		rl.PrevHash, rl.PrevUntil = ph, dv.until
		if _, err := rd.devices.SetRelay(dv.d.ID, &rl); err != nil {
			t.Fatal(err)
		}
	}
	got := rd.relayEntries(now)
	seen := map[string]bool{}
	var windows, prevs int
	for _, e := range got {
		if seen[e.RV] {
			t.Fatalf("rv %s twice", e.RV)
		}
		seen[e.RV] = true
		if !e.ExpiresAt.IsZero() {
			windows++
		}
		if e.PrevSHA256 != "" {
			prevs++
		}
	}
	if len(got) != 3 || windows != 1 || prevs != 1 || !seen[rv] {
		t.Fatalf("entries %+v", got)
	}
	// An expired window drops out.
	if got := rd.relayEntries(tk.ExpiresAt.Add(time.Second)); len(got) != 2 {
		t.Fatalf("expired window kept: %+v", got)
	}
}

func TestRelayClockAhead(t *testing.T) {
	f := newRelayHubFake(t) // its POST handler validates against the REAL clock
	rd := newRelayDaemon(t, f)
	ahead := func() time.Time { return time.Now().Add(4 * time.Minute) }
	rd.Daemon.hub.now = ahead
	dev := rd.pair(t, true)
	rl := *dev.Relay
	_, rl.PrevHash, _ = hubproto.NewRelayBearer()
	rl.PrevUntil = ahead().Add(hubproto.RelayPrevBearerGrace)
	if _, err := rd.devices.SetRelay(dev.ID, &rl); err != nil {
		t.Fatal(err)
	}
	rd.serve(t)
	api, _, id, err := rd.relay().newClient(f.srv.URL, rd.hubDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := rd.relaySync(context.Background(), api, id, f.srv.URL); err != nil {
		t.Fatalf("a daemon clock 4 minutes ahead was refused: %v", err)
	}
	if got := f.lastPost(); len(got) != 1 || got[0].PrevSHA256 == "" {
		t.Fatalf("post %+v", got)
	}
}

func TestRelayPairOfferFirstPhoneRelayOnly(t *testing.T) {
	t.Run("relay in the QR", func(t *testing.T) {
		f := newRelayHubFake(t)
		rd := newRelayDaemon(t, f)
		rd.serve(t)
		if len(rd.remote.Addrs()) != 0 {
			t.Fatal("bind = off must have no listener")
		}
		rd.run(t)
		tk, err := rd.pairer.Begin()
		if err != nil {
			t.Fatal(err)
		}
		qr := rd.relayPairOffer(context.Background(), tk)
		if qr == nil {
			t.Fatal("no relay in the QR")
		}
		if qr.URL != f.srv.URL || !hubproto.ValidRelayID(qr.RV) || !hubproto.ValidRelayBearer(qr.Tok) {
			t.Fatalf("qr %+v", qr)
		}
		var win *hubproto.RelayRendezvous
		for _, e := range f.lastPost() {
			if e.RV == qr.RV {
				win = &e
			}
		}
		if win == nil || win.BearerSHA256 != hubproto.RelayBearerHash(qr.Tok) || win.ExpiresAt.IsZero() {
			t.Fatalf("hub window entry %+v", win)
		}
		if rv, _, _, ok := rd.pairer.Window(); !ok || rv != qr.RV {
			t.Fatal("the pairing does not carry the window relay")
		}
		if strings.Contains(rd.logs(), qr.Tok) {
			t.Error("the window token was logged")
		}
	})
	t.Run("unsupported hub", func(t *testing.T) {
		f := newRelayHubFake(t)
		f.postStatus = 404
		rd := newRelayDaemon(t, f)
		rd.serve(t)
		rd.relay().pairWait = 3 * time.Second
		rd.run(t)
		tk, _ := rd.pairer.Begin()
		if qr := rd.relayPairOffer(context.Background(), tk); qr != nil {
			t.Fatalf("an unsupported hub offered %+v", qr)
		}
		if _, _, _, ok := rd.pairer.Window(); ok {
			t.Error("the window kept a relay")
		}
	})
}

// A joined hub on plain http (a loopback `lola-hub dev`) must not break
// pairBegin: the QR's relay has to be https on both ends, so the window drops
// its relay and the QR goes out without one. Found live: before this,
// `lola pair` on a daemon joined to `lola-hub dev` failed with "pairing payload
// relay url must be https", LAN path included.
func TestPairBeginPlainHTTPHubOmitsRelay(t *testing.T) {
	f := newRelayHubFake(t)
	if strings.HasPrefix(f.srv.URL, "https://") {
		t.Fatal("the fake must be plain http for this test")
	}
	rd := newRelayDaemon(t, f)
	rd.serve(t)
	rd.run(t)
	got, err := rd.handlePairBegin(context.Background(), protocol.PairBeginArgs{})
	if err != nil {
		t.Fatalf("pairBegin failed on a plain-http hub: %v", err)
	}
	// bind = "off" and no usable relay: the documented sentence, not an error.
	if got.Relay || got.Payload != "" || !strings.Contains(got.Problem, "no hub relay is available") {
		t.Fatalf("pairBegin = %+v", got)
	}
	if _, _, _, ok := rd.pairer.Window(); ok {
		t.Error("the window kept a relay rv")
	}
}

func TestRelayPairConfirmFreshRV(t *testing.T) {
	f := newRelayHubFake(t)
	rd := newRelayDaemon(t, f)
	rd.serve(t)
	rd.run(t)
	tk, _ := rd.pairer.Begin()
	qr := rd.relayPairOffer(context.Background(), tk)
	if qr == nil {
		t.Fatal("no relay offer")
	}
	cr := rd.relayMint()
	if cr == nil {
		t.Fatal("relayMint returned nil while eligible")
	}
	rec := cr.Record
	if rec.RV == qr.RV || rec.PrevHash != "" || !hubproto.ValidRelayBearer(cr.Bearer) || rec.BearerHash != hubproto.RelayBearerHash(cr.Bearer) {
		t.Fatalf("mint %+v (qr rv %s)", rec, qr.RV)
	}
	// Once the window is gone the next set holds the device rv only.
	rd.pairer.SetWindowRelay(tk.ID, "", "")
	rd.pair(t, false)
	if _, err := rd.devices.SetRelay(rd.devices.List()[0].ID, &rec); err != nil {
		t.Fatal(err)
	}
	for _, e := range rd.relayEntries(time.Now()) {
		if e.RV == qr.RV {
			t.Error("the QR rv outlived the window")
		}
	}
	rd.mu.Lock()
	rd.cfg.Remote.RelayOff = true
	rd.mu.Unlock()
	if rd.relayMint() != nil {
		t.Error("relayMint must be nil when not eligible")
	}
}

func relayCredCtx(dev devices.Device) context.Context {
	return remote.WithPeer(context.Background(), remote.Peer{DeviceID: dev.ID, Via: remote.ViaLAN})
}

func TestRelayCredUnixSocketRefused(t *testing.T) {
	f := newRelayHubFake(t)
	rd := newRelayDaemon(t, f)
	rd.serve(t)
	if _, err := rd.handleRelayCred(context.Background(), protocol.RelayCredArgs{}); err == nil {
		t.Fatal("the unix socket got relay credentials")
	}
}

func TestRelayCredEmptyHoldsReissues(t *testing.T) {
	f := newRelayHubFake(t)
	rd := newRelayDaemon(t, f)
	rd.serve(t)
	dev := rd.pair(t, true)
	out, err := rd.handleRelayCred(relayCredCtx(dev), protocol.RelayCredArgs{})
	if err != nil || !out.Available || out.Bearer == "" || out.RV != dev.Relay.RV || out.URL != f.srv.URL {
		t.Fatalf("%+v %v", out, err)
	}
	got, _ := rd.devices.Get(dev.ID)
	if got.Relay.BearerHash != hubproto.RelayBearerHash(out.Bearer) || got.Relay.PrevHash != "" {
		t.Fatalf("record %+v", got.Relay)
	}
}

func TestRelayCredTable(t *testing.T) {
	f := newRelayHubFake(t)
	rd := newRelayDaemon(t, f)
	rd.serve(t)
	call := func(dev devices.Device, holds string) protocol.RelayCredData {
		t.Helper()
		out, err := rd.handleRelayCred(relayCredCtx(dev), protocol.RelayCredArgs{Holds: holds})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	record := func(dev devices.Device) devices.DeviceRelay {
		g, _ := rd.devices.Get(dev.ID)
		return *g.Relay
	}

	// Row 1: no record -> issue.
	bare := rd.pair(t, false)
	out := call(bare, "")
	if out.Bearer == "" || !hubproto.ValidRelayID(out.RV) || record(bare).BearerHash != hubproto.RelayBearerHash(out.Bearer) {
		t.Fatalf("issued %+v", out)
	}

	// Row 2: holds the current bearer, fresh -> nothing to send; a prev is cleared (ack).
	dev := rd.pair(t, true)
	cur := record(dev)
	out = call(dev, cur.BearerHash)
	if out.Bearer != "" || out.RV != cur.RV || !out.Available {
		t.Fatalf("current %+v", out)
	}
	prev := cur
	_, prev.PrevHash, _ = hubproto.NewRelayBearer()
	prev.PrevUntil = time.Now().Add(time.Hour)
	if _, err := rd.devices.SetRelay(dev.ID, &prev); err != nil {
		t.Fatal(err)
	}
	if out = call(dev, cur.BearerHash); out.Bearer != "" {
		t.Fatalf("ack sent a bearer %+v", out)
	}
	if r := record(dev); r.PrevHash != "" || !r.PrevUntil.IsZero() {
		t.Fatalf("ack kept prev %+v", r)
	}

	// Row 3: holds the current bearer but it is older than the max age -> rotate.
	old := record(dev)
	old.RotatedAt = time.Now().Add(-hubproto.RelayBearerMaxAge - time.Hour)
	if _, err := rd.devices.SetRelay(dev.ID, &old); err != nil {
		t.Fatal(err)
	}
	out = call(dev, old.BearerHash)
	r3 := record(dev)
	if out.Bearer == "" || r3.PrevHash != old.BearerHash || r3.BearerHash != hubproto.RelayBearerHash(out.Bearer) || !r3.PrevUntil.After(time.Now().Add(5*24*time.Hour)) {
		t.Fatalf("rotated %+v %+v", out, r3)
	}

	// Row 4: the phone holds the previous bearer (never stored the rotation).
	out = call(dev, old.BearerHash)
	r4 := record(dev)
	if out.Bearer == "" || r4.PrevHash != old.BearerHash || r4.BearerHash != hubproto.RelayBearerHash(out.Bearer) {
		t.Fatalf("recovery %+v %+v", out, r4)
	}

	// Row 5: anything else -> new bearer, no prev.
	unknown := hubproto.RelayBearerHash("something else entirely")
	out = call(dev, unknown)
	if r5 := record(dev); out.Bearer == "" || r5.PrevHash != "" || r5.BearerHash != hubproto.RelayBearerHash(out.Bearer) {
		t.Fatalf("reissue %+v %+v", out, r5)
	}

	// Validation and eligibility.
	if _, err := rd.handleRelayCred(relayCredCtx(dev), protocol.RelayCredArgs{Holds: "not-a-hash"}); err == nil {
		t.Error("a malformed holds was accepted")
	}
	rd.cfg.Remote.RelayOff = true
	if out := call(dev, ""); out.Available || out.Bearer != "" {
		t.Errorf("ineligible reply %+v", out)
	}
	rd.cfg.Remote.RelayOff = false
	if _, err := rd.handleRelayCred(remote.WithPeer(context.Background(), remote.Peer{DeviceID: "dnobody"}), protocol.RelayCredArgs{}); err == nil {
		t.Error("an unpaired peer got a credential")
	}
	if strings.Contains(rd.logs(), out.Bearer) || !strings.Contains(rd.logs(), "hub relay: cred device=") {
		t.Errorf("log: %s", rd.logs())
	}
}

func TestRelayConflictReissuesRV(t *testing.T) {
	f := newRelayHubFake(t)
	rd := newRelayDaemon(t, f)
	dev := rd.pair(t, true)
	f.conflicts = func(es []hubproto.RelayRendezvous) []string {
		for _, e := range es {
			if e.RV == dev.Relay.RV {
				return []string{e.RV}
			}
		}
		return nil
	}
	rd.serve(t)
	api, _, id, err := rd.relay().newClient(f.srv.URL, rd.hubDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := rd.relaySync(context.Background(), api, id, f.srv.URL); err != nil {
		t.Fatal(err)
	}
	got, _ := rd.devices.Get(dev.ID)
	if got.Relay.RV == dev.Relay.RV || got.Relay.BearerHash != dev.Relay.BearerHash {
		t.Fatalf("rv %s -> %s, bearer kept: %v", dev.Relay.RV, got.Relay.RV, got.Relay.BearerHash == dev.Relay.BearerHash)
	}
	out, err := rd.handleRelayCred(relayCredCtx(dev), protocol.RelayCredArgs{Holds: dev.Relay.BearerHash})
	if err != nil || out.RV != got.Relay.RV || out.Bearer != "" {
		t.Fatalf("cred %+v %v", out, err)
	}
}

// TestRelayConflictRotationIsBounded: a hub that names every rv it was asked
// about as a conflict, on every POST, gets one rotation per device per
// relayConflictRotateEvery, and a refused rotation starts no further sync, so
// the registry is not rewritten and the hub not re-posted in a loop.
func TestRelayConflictRotationIsBounded(t *testing.T) {
	f := newRelayHubFake(t)
	rd := newRelayDaemon(t, f)
	dev := rd.pair(t, true)
	f.mu.Lock()
	f.conflicts = func(es []hubproto.RelayRendezvous) []string {
		var out []string
		for _, e := range es {
			out = append(out, e.RV)
		}
		return out
	}
	f.mu.Unlock()
	rd.serve(t)
	api, _, id, err := rd.relay().newClient(f.srv.URL, rd.hubDir())
	if err != nil {
		t.Fatal(err)
	}
	before := f.postCount()
	for i := 0; i < 3; i++ {
		if err := rd.relaySync(context.Background(), api, id, f.srv.URL); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.postCount() - before; n > 4 {
		t.Fatalf("%d rendezvous POSTs for 3 syncs against a hostile hub; want at most 4", n)
	}
	got, _ := rd.devices.Get(dev.ID)
	if got.Relay.RV == dev.Relay.RV {
		t.Fatal("the first conflict did not rotate the rv")
	}
	logs := rd.logs()
	if n := strings.Count(logs, "new rendezvous id issued"); n != 1 {
		t.Fatalf("%d rotations, want 1:\n%s", n, logs)
	}
	if n := strings.Count(logs, "its rendezvous id is kept"); n != 1 {
		t.Fatalf("%d repeat lines, want 1:\n%s", n, logs)
	}
	r := rd.relay()
	r.mu.Lock()
	pending := r.syncedGen != r.gen.Load()
	r.mu.Unlock()
	if pending {
		t.Fatal("a declined conflict left the set dirty, so the loop would sync again at once")
	}

	// After the window the device may rotate once more.
	r.mu.Lock()
	for k, m := range r.conflictAt {
		m.at = m.at.Add(-relayConflictRotateEvery)
		r.conflictAt[k] = m
	}
	r.mu.Unlock()
	if err := rd.relaySync(context.Background(), api, id, f.srv.URL); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(rd.logs(), "new rendezvous id issued"); n != 2 {
		t.Fatalf("%d rotations after the window, want 2", n)
	}
}

func TestRelayOnlyServerHasNoListeners(t *testing.T) {
	f := newRelayHubFake(t)
	rd := newRelayDaemon(t, f)
	started := 0
	rd.mdnsStart = func(context.Context, string, []string) (mdns.Process, error) {
		started++
		return nil, io.EOF
	}
	rd.serve(t)
	if len(rd.remote.Addrs()) != 0 || rd.remote.Port() != 0 {
		t.Fatalf("relay-only server has addresses: %v", rd.remote.Addrs())
	}
	if started != 0 {
		t.Error("an advertiser was started for a server with no port")
	}
	if !strings.Contains(rd.logs(), "remote: relay-only server up") {
		t.Errorf("log: %s", rd.logs())
	}
}

func TestRelayOnlyServerFollowsJoin(t *testing.T) {
	f := newRelayHubFake(t)
	rd := newRelayDaemon(t, f)
	if err := hub.RemoveIdentity(rd.hubDir()); err != nil {
		t.Fatal(err)
	}
	rd.startRemote(context.Background())
	t.Cleanup(rd.stopRemote)
	if rd.remote != nil {
		t.Fatal("a daemon that has not joined served relay-only")
	}
	rd.join(t, f.srv.URL)
	rd.startRemote(context.Background())
	if rd.remote == nil {
		t.Fatal("joining did not produce a relay-only server")
	}
	if err := hub.RemoveIdentity(rd.hubDir()); err != nil {
		t.Fatal(err)
	}
	rd.stopRemote()
	rd.startRemote(context.Background())
	if rd.remote != nil {
		t.Fatal("leaving left a server")
	}
}

func TestHubStatusRelayCarriesNoSecrets(t *testing.T) {
	f := newRelayHubFake(t)
	rd := newRelayDaemon(t, f)
	dev := rd.pair(t, true)
	rd.serve(t)
	rd.run(t)
	relayWaitState(t, rd.Daemon, protocol.HubRelayStateConnected)
	st := rd.hubStatusData()
	if st.Relay == nil || st.Relay.State != protocol.HubRelayStateConnected || st.Relay.Rendezvous != 1 {
		t.Fatalf("relay info %+v", st.Relay)
	}
	raw, _ := json.Marshal(st)
	for _, secret := range []string{dev.Relay.RV, dev.Relay.BearerHash} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("hubStatus carries %s", secret)
		}
	}
	rd.mu.Lock()
	rd.cfg.Remote.Enabled = false
	rd.mu.Unlock()
	if rd.relayStatus() != nil {
		t.Error("relay status shown with [remote] disabled")
	}
}
