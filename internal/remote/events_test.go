package remote

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/eventbox"
	"github.com/sushidev-team/lola/internal/protocol"
)

// --- fakes -----------------------------------------------------------------

// fakeEvents records the order of Watch and stop calls and keeps each topic's
// box so a test can play the producer.
type fakeEvents struct {
	mu     sync.Mutex
	order  []string
	boxes  map[string]*eventbox.Box
	ids    map[string]string
	refuse map[string]protocol.WatchResult
	snaps  []string
}

func newFakeEvents() *fakeEvents {
	return &fakeEvents{boxes: map[string]*eventbox.Box{}, ids: map[string]string{}, refuse: map[string]protocol.WatchResult{}}
}

func (f *fakeEvents) Watch(_ context.Context, _ Peer, topic, watchID string, box *eventbox.Box) (func(), protocol.WatchResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.order = append(f.order, "watch:"+topic+":"+watchID)
	if r, ok := f.refuse[topic]; ok {
		return nil, r
	}
	box.Start(topic, watchID)
	f.boxes[topic], f.ids[topic] = box, watchID
	return func() {
		f.mu.Lock()
		f.order = append(f.order, "stop:"+topic+":"+watchID)
		f.mu.Unlock()
		box.Stop(topic, watchID)
	}, protocol.WatchResult{Topic: topic, OK: true}
}

func (f *fakeEvents) Resnapshot(topic string) {
	f.mu.Lock()
	f.snaps = append(f.snaps, topic)
	f.mu.Unlock()
}

func (f *fakeEvents) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.order...)
}

func (f *fakeEvents) stops() int {
	n := 0
	for _, l := range f.log() {
		if strings.HasPrefix(l, "stop:") {
			n++
		}
	}
	return n
}

// install plays the producer: it hands the topic's current slot a snapshot.
func (f *fakeEvents) install(t *testing.T, topic string, ev eventbox.Event) {
	t.Helper()
	f.mu.Lock()
	box, id := f.boxes[topic], f.ids[topic]
	f.mu.Unlock()
	if box == nil {
		t.Fatalf("topic %s was never watched", topic)
	}
	if !box.Install(topic, id, []eventbox.Event{ev}) {
		t.Fatalf("install %s refused", topic)
	}
}

func statusEvent() eventbox.Event { return protocol.SizeOf(&protocol.StatusEvent{}) }

// bigEvent claims a small size and encodes above the frame cap: the case the
// producer-side bounds are supposed to make impossible.
type bigEvent struct{}

func (bigEvent) MarshalJSON() ([]byte, error) {
	return []byte(`"` + strings.Repeat("a", protocol.MaxFrameBytes) + `"`), nil
}
func (bigEvent) Merge(protocol.TopicEvent) (protocol.TopicEvent, bool) { return nil, false }
func (bigEvent) Size() int                                             { return 16 }
func (bigEvent) Part() bool                                            { return false }

func withEvents(ev Events) func(*rig, *Options) {
	return func(_ *rig, o *Options) { o.Events = ev }
}

func watchReq(topics ...string) protocol.Request {
	b, _ := json.Marshal(protocol.WatchArgs{Topics: topics})
	return protocol.Request{Args: b}
}

func unwatchReq(a protocol.UnwatchArgs) protocol.Request {
	b, _ := json.Marshal(a)
	return protocol.Request{Args: b}
}

// wantWatchData reads the next frame as the resp of id and decodes its results.
func wantWatchData(t *testing.T, f protocol.Frame, id string) []protocol.WatchResult {
	t.Helper()
	if f.Type != protocol.FrameResp || f.ID != id {
		t.Fatalf("got %+v, want resp %q", f, id)
	}
	var resp protocol.Response
	if err := json.Unmarshal(f.Payload, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("response not ok: %q", resp.Error)
	}
	var d protocol.WatchData
	if err := json.Unmarshal(resp.Data, &d); err != nil {
		t.Fatal(err)
	}
	return d.Topics
}

func wantBadWatch(t *testing.T, f protocol.Frame, id string) {
	t.Helper()
	var resp protocol.Response
	if f.Type != protocol.FrameResp || f.ID != id || json.Unmarshal(f.Payload, &resp) != nil || resp.OK || resp.Error != "bad watch request" {
		t.Fatalf("got %+v (%s), want a refused watch", f, f.Payload)
	}
}

func wantEvent(t *testing.T, f protocol.Frame, id, topic string, seq uint64) {
	t.Helper()
	if f.Type != protocol.FrameEvent || f.ID != id || f.Topic != topic || f.Seq != seq {
		t.Fatalf("got %+v, want event %s %s seq %d", f, id, topic, seq)
	}
}

// bareConn is a connection that never authenticates or reads: enough to drive
// dispatchEvents and the pump against a shared Server, so the box-slot bound
// can be exercised without eight devices.
func bareConn(t *testing.T, s *Server) (*conn, chan protocol.Frame) {
	t.Helper()
	a, b := net.Pipe()
	c := newConn(s, a)
	c.peer = Peer{DeviceID: "bare"}
	out := make(chan protocol.Frame, 256)
	go func() {
		fr := protocol.NewFrameReader(b)
		for {
			var f protocol.Frame
			if fr.ReadFrame(&f) != nil {
				return
			}
			out <- f
		}
	}()
	t.Cleanup(func() { c.shutdown(); b.Close() })
	return c, out
}

func respOf(t *testing.T, out chan protocol.Frame, id string) []protocol.WatchResult {
	t.Helper()
	select {
	case f := <-out:
		return wantWatchData(t, f, id)
	case <-time.After(testTimeout):
		t.Fatal("no response")
	}
	return nil
}

// --- tests -----------------------------------------------------------------

func TestWatchInlineAndPump(t *testing.T) {
	fe := newFakeEvents()
	r := newRig(t, withEvents(fe))
	r.req("w1", protocol.CmdWatch, watchReq("status"))
	res := wantWatchData(t, r.next(), "w1")
	if len(res) != 1 || !res[0].OK || res[0].Topic != "status" {
		t.Fatalf("results = %+v", res)
	}
	if r.handler.count() != 0 {
		t.Error("watch reached Options.Handle")
	}
	fe.install(t, "status", statusEvent())
	wantEvent(t, r.next(), "w1", "status", 1)
}

func TestWatchIDBounded(t *testing.T) {
	fe := newFakeEvents()
	r := newRig(t, withEvents(fe))
	for i, id := range []string{strings.Repeat("a", 300<<10), strings.Repeat("a", protocol.MaxWatchIDBytes+1), "a/b", "a b"} {
		r.req(id, protocol.CmdWatch, watchReq("status"))
		f := r.next()
		if i == 0 {
			// The oversize id cannot be answered on its own id inside the frame
			// cap, or the request is refused whole; either way nothing registers.
			continue
		}
		wantBadWatch(t, f, id)
	}
	if len(fe.log()) != 0 {
		t.Errorf("a bad watch id registered: %v", fe.log())
	}
	r.wantOpen()
}

func TestBadWatchRequests(t *testing.T) {
	fe := newFakeEvents()
	r := newRig(t, withEvents(fe))
	dup := watchReq("status", "status")
	many := make([]string, protocol.MaxWatchTopicsPerRequest+1)
	for i := range many {
		many[i] = "diff:s" + strings.Repeat("x", i)
	}
	for _, q := range []protocol.Request{watchReq(), dup, watchReq(many...)} {
		r.req("w", protocol.CmdWatch, q)
		wantBadWatch(t, r.next(), "w")
	}
	r.req("u", protocol.CmdUnwatch, unwatchReq(protocol.UnwatchArgs{}))
	wantBadWatch(t, r.next(), "u")
	if len(fe.log()) != 0 {
		t.Errorf("registered: %v", fe.log())
	}
}

func TestWatchResultsPerTopic(t *testing.T) {
	fe := newFakeEvents()
	fe.refuse["talk:gone"] = protocol.WatchResult{Code: protocol.WatchCodeUnknownSession, Message: "no such session"}
	r := newRig(t, withEvents(fe))
	r.req("w1", protocol.CmdWatch, watchReq("status", "bogus", "talk:gone", "projects"))
	res := wantWatchData(t, r.next(), "w1")
	want := []struct {
		topic string
		ok    bool
		code  string
	}{{"status", true, ""}, {"bogus", false, protocol.WatchCodeInvalidTopic}, {"talk:gone", false, protocol.WatchCodeUnknownSession}, {"projects", true, ""}}
	if len(res) != len(want) {
		t.Fatalf("results = %+v", res)
	}
	for i, w := range want {
		if res[i].Topic != w.topic || res[i].OK != w.ok || res[i].Code != w.code {
			t.Errorf("result %d = %+v, want %+v", i, res[i], w)
		}
	}
}

func TestRewatchNewIDAndSeq(t *testing.T) {
	fe := newFakeEvents()
	r := newRig(t, withEvents(fe))
	r.req("w1", protocol.CmdWatch, watchReq("status"))
	wantWatchData(t, r.next(), "w1")
	fe.install(t, "status", statusEvent())
	wantEvent(t, r.next(), "w1", "status", 1)

	r.req("w2", protocol.CmdWatch, watchReq("status"))
	wantWatchData(t, r.next(), "w2")
	got := strings.Join(fe.log(), ",")
	if want := "watch:status:w1,stop:status:w1,watch:status:w2"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	fe.install(t, "status", statusEvent())
	wantEvent(t, r.next(), "w2", "status", 1)
}

func TestRewatchRefusedLeavesTopicUnwatched(t *testing.T) {
	fe := newFakeEvents()
	r := newRig(t, withEvents(fe))
	r.req("w1", protocol.CmdWatch, watchReq("status"))
	wantWatchData(t, r.next(), "w1")
	fe.mu.Lock()
	fe.refuse["status"] = protocol.WatchResult{Code: protocol.WatchCodeLimit}
	fe.mu.Unlock()
	r.req("w2", protocol.CmdWatch, watchReq("status"))
	if res := wantWatchData(t, r.next(), "w2"); res[0].OK {
		t.Fatalf("refusal reported ok: %+v", res)
	}
	if fe.stops() != 1 {
		t.Errorf("old stream not stopped: %v", fe.log())
	}
	r.req("u", protocol.CmdUnwatch, unwatchReq(protocol.UnwatchArgs{Topics: []string{"status"}}))
	if res := wantWatchData(t, r.next(), "u"); !res[0].OK {
		t.Errorf("unwatch of an unwatched topic = %+v", res)
	}
}

func TestUnwatchAll(t *testing.T) {
	fe := newFakeEvents()
	r := newRig(t, withEvents(fe))
	r.req("w1", protocol.CmdWatch, watchReq("status", "projects", "sessions"))
	wantWatchData(t, r.next(), "w1")
	r.req("u1", protocol.CmdUnwatch, unwatchReq(protocol.UnwatchArgs{Topics: []string{"projects"}}))
	wantWatchData(t, r.next(), "u1")
	if fe.stops() != 1 {
		t.Fatalf("stops after one unwatch = %d", fe.stops())
	}
	r.req("u2", protocol.CmdUnwatch, unwatchReq(protocol.UnwatchArgs{All: true}))
	if res := wantWatchData(t, r.next(), "u2"); len(res) != 0 {
		t.Errorf("unwatch all results = %+v", res)
	}
	if fe.stops() != 3 {
		t.Errorf("stops = %d, want 3", fe.stops())
	}
	// A late event for a dropped watch must not be framed.
	r.req("w2", protocol.CmdWatch, watchReq("status"))
	wantWatchData(t, r.next(), "w2")
	fe.install(t, "status", statusEvent())
	wantEvent(t, r.next(), "w2", "status", 1)
}

func TestWatchLimitPerConn(t *testing.T) {
	fe := newFakeEvents()
	r := newRig(t, withEvents(fe))
	topics := make([]string, protocol.MaxWatchTopicsPerConn)
	for i := range topics {
		topics[i] = "diff:s" + strings.Repeat("x", i)
	}
	r.req("w1", protocol.CmdWatch, watchReq(topics...))
	for _, x := range wantWatchData(t, r.next(), "w1") {
		if !x.OK {
			t.Fatalf("refused below the limit: %+v", x)
		}
	}
	r.req("w2", protocol.CmdWatch, watchReq("status"))
	res := wantWatchData(t, r.next(), "w2")
	if res[0].OK || res[0].Code != protocol.WatchCodeLimit {
		t.Fatalf("33rd topic = %+v", res[0])
	}
	// Re-watching a held topic is not a new topic.
	r.req("w3", protocol.CmdWatch, watchReq(topics[0]))
	if res := wantWatchData(t, r.next(), "w3"); !res[0].OK {
		t.Fatalf("re-watch at the limit = %+v", res[0])
	}
}

func TestWatchingConnsBounded(t *testing.T) {
	fe := newFakeEvents()
	r := newRig(t, withEvents(fe))
	s := r.srv
	var conns []*conn
	var outs []chan protocol.Frame
	for i := 0; i < maxWatchingConns; i++ {
		c, out := bareConn(t, s)
		if !c.dispatchEvents("w", watchRequest("status")) {
			t.Fatal("watch not consumed")
		}
		if res := respOf(t, out, "w"); !res[0].OK {
			t.Fatalf("conn %d refused: %+v", i, res[0])
		}
		conns, outs = append(conns, c), append(outs, out)
	}
	ninth, out := bareConn(t, s)
	ninth.dispatchEvents("w", watchRequest("status", "projects"))
	for _, x := range respOf(t, out, "w") {
		if x.OK || x.Code != protocol.WatchCodeLimit {
			t.Fatalf("ninth conn topic = %+v", x)
		}
	}
	if evState.mu.Lock(); evState.boxes[s] != maxWatchingConns {
		t.Errorf("box slots = %d", evState.boxes[s])
	}
	evState.mu.Unlock()

	conns[0].shutdown()
	_ = outs
	ninth.dispatchEvents("w2", watchRequest("status"))
	if res := respOf(t, out, "w2"); !res[0].OK {
		t.Fatalf("slot not freed by a close: %+v", res[0])
	}
}

func watchRequest(topics ...string) protocol.Request {
	r := watchReq(topics...)
	r.Cmd = protocol.CmdWatch
	return r
}

func TestWatchWithoutEventsUnavailable(t *testing.T) {
	r := newRig(t)
	r.req("w1", protocol.CmdWatch, watchReq("status", "bogus"))
	res := wantWatchData(t, r.next(), "w1")
	if res[0].OK || res[0].Code != protocol.WatchCodeUnavailable {
		t.Errorf("status = %+v", res[0])
	}
	if res[1].Code != protocol.WatchCodeInvalidTopic {
		t.Errorf("bogus = %+v", res[1])
	}
	r.wantOpen()
}

func TestShutdownStopsWatches(t *testing.T) {
	fe := newFakeEvents()
	r := newRig(t, withEvents(fe))
	s := r.srv
	c, out := bareConn(t, s)
	c.dispatchEvents("w", watchRequest("status", "projects"))
	respOf(t, out, "w")
	c.shutdown()
	if fe.stops() != 2 {
		t.Errorf("stops = %d, want 2", fe.stops())
	}
	evState.mu.Lock()
	defer evState.mu.Unlock()
	if evState.boxes[s] != 0 || evState.conns[c] != nil {
		t.Errorf("state leaked after shutdown: slots=%d", evState.boxes[s])
	}
}

func TestPeerInHandleContext(t *testing.T) {
	got := make(chan Peer, 1)
	r := newRig(t, func(r *rig, o *Options) {
		o.Handle = func(ctx context.Context, _ protocol.Request) protocol.Response {
			p, _ := PeerFromContext(ctx)
			got <- p
			return protocol.Response{OK: true}
		}
	})
	r.req("x", "sessions", nil)
	r.next()
	if p := <-got; p.DeviceID != r.dev.ID {
		t.Errorf("peer = %+v", p)
	}
}

func TestOversizeFrameEndsStreamOnce(t *testing.T) {
	fe := newFakeEvents()
	r := newRig(t, withEvents(fe))
	for i, id := range []string{"w1", "w2"} {
		r.req(id, protocol.CmdWatch, watchReq("status"))
		wantWatchData(t, r.next(), id)
		fe.install(t, "status", bigEvent{})
		f := r.next()
		if f.Type != protocol.FrameErr || f.ID != id || f.Topic != "status" {
			t.Fatalf("round %d: got %+v", i, f)
		}
		var p protocol.ErrPayload
		_ = json.Unmarshal(f.Payload, &p)
		if p.Code != protocol.CodeFrameTooLarge {
			t.Fatalf("code = %q", p.Code)
		}
	}
	if n := strings.Count(r.log.all(), "stream ended"); n != 1 {
		t.Errorf("log lines = %d, want 1 per (connection, topic)", n)
	}
	if len(fe.snaps) != 0 {
		t.Errorf("an oversize frame asked for a resnapshot: %v", fe.snaps)
	}
	r.wantOpen()
}

func TestRevokedSilentWatcherStopsReceiving(t *testing.T) {
	fe := newFakeEvents()
	l := startTestListener(t, &stubHandler{}, newFakeBus(), func(o *Options) { o.Events = fe })
	c := l.dial()
	c.wantReady()
	c.req("w1", protocol.CmdWatch, watchReq("status"))
	wantWatchData(t, c.mustRead(), "w1")

	// Revoked in the registry only: DropDevice never reaches the connection.
	if _, err := l.reg.Revoke(l.dev.ID); err != nil {
		t.Fatal(err)
	}
	fe.install(t, "status", statusEvent())
	for {
		f, err := c.read()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("connection stayed open")
			}
			return
		}
		if f.Type == protocol.FrameEvent {
			t.Fatalf("an event reached a revoked device: %+v", f)
		}
	}
}

func TestDeniedReadEndsStreams(t *testing.T) {
	fe := newFakeEvents()
	r := newRig(t, withEvents(fe))
	r.req("w1", protocol.CmdWatch, watchReq("status", "projects"))
	wantWatchData(t, r.next(), "w1")
	r.auth.setFrameErr(ErrDenied)
	// A watch request is itself refused now; the pump is what ends the streams.
	fe.install(t, "status", statusEvent())
	f := r.next()
	if f.Type != protocol.FrameErr || f.ID != "w1" {
		t.Fatalf("got %+v", f)
	}
	var p protocol.ErrPayload
	_ = json.Unmarshal(f.Payload, &p)
	if p.Code != protocol.CodeDenied {
		t.Fatalf("code = %q", p.Code)
	}
	f2 := r.next()
	if f2.Type != protocol.FrameErr || f2.ID != "w1" || f2.Topic == f.Topic || f2.Topic == "" {
		t.Fatalf("second stream not ended: %+v", f2)
	}
	if fe.stops() != 2 {
		t.Errorf("stops = %d", fe.stops())
	}
	r.auth.setFrameErr(nil)
	r.wantOpen()
}

func TestV1FrameRefused(t *testing.T) {
	r := newRig(t, withEvents(newFakeEvents()))
	r.send(protocol.Frame{V: 1, Type: protocol.FrameReq, ID: "w", Cmd: protocol.CmdWatch})
	p := r.wantErr(protocol.CodeUnsupportedVersion)
	if p.MinV != 2 || p.MaxV != 2 {
		t.Errorf("bounds = %d..%d", p.MinV, p.MaxV)
	}
	r.wantClosed()
}

func TestV1PairFrameRefused(t *testing.T) {
	l := startTestListener(t, &stubHandler{}, newFakeBus())
	c := l.dial()
	c.wantReady()
	f := protocol.Frame{V: 1, Type: protocol.FrameReq, ID: "p", Cmd: "remote.pair"}
	if err := c.fw.WriteFrame(&f); err != nil {
		t.Fatal(err)
	}
	got := c.mustRead()
	var p protocol.ErrPayload
	_ = json.Unmarshal(got.Payload, &p)
	if got.Type != protocol.FrameErr || p.Code != protocol.CodeUnsupportedVersion {
		t.Fatalf("got %+v (%s)", got, got.Payload)
	}
	c.wantClosed()
}
