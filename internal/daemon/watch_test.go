package daemon

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/eventbox"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// wtest is a daemon with its watch hub loop running.
type wtest struct {
	t   *testing.T
	d   *Daemon
	h   *watchHub
	ctx context.Context
}

func newWatchTest(t *testing.T) *wtest {
	t.Helper()
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	ctx, cancel := context.WithCancel(context.Background())
	d.wg.Add(1)
	go d.watch.run(ctx)
	t.Cleanup(func() { cancel(); d.wg.Wait() })
	return &wtest{t: t, d: d, h: d.watch, ctx: ctx}
}

// manual is a hub the test steps itself on a fake clock; nothing runs in the
// background.
func newManualWatchTest(t *testing.T) (*wtest, *time.Time) {
	t.Helper()
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	d.watch.now = func() time.Time { return now }
	return &wtest{t: t, d: d, h: d.watch, ctx: context.Background()}, &now
}

func (w *wtest) step() {
	w.h.step(w.ctx)
	w.h.runs.Wait()
}

func (w *wtest) session(id string) {
	w.t.Helper()
	w.d.sessions.Upsert(session.Session{ID: id, Project: "p1", Source: "native", AgentState: state.AgentWorking})
}

func (w *wtest) box() *eventbox.Box { return eventbox.New(w.h.Resnapshot) }

func (w *wtest) watch(box *eventbox.Box, topic, id string) (func(), protocol.WatchResult) {
	return w.h.Watch(w.ctx, remote.Peer{DeviceID: "dev"}, topic, id, box)
}

func next(t *testing.T, b *eventbox.Box, within time.Duration) (eventbox.Item, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	return b.Next(ctx)
}

func mustNext(t *testing.T, b *eventbox.Box) eventbox.Item {
	t.Helper()
	it, ok := next(t, b, 3*time.Second)
	if !ok {
		t.Fatal("no event within 3s")
	}
	return it
}

// fakeFeed is a controllable reader behind the openFeed seam.
type fakeFeed struct {
	mu       sync.Mutex
	epoch    string
	lastSeq  int64
	scanning bool
	pages    atomic.Int32
	items    []protocol.AgentFeedItem
}

func (f *fakeFeed) install(h *watchHub) {
	h.advanceFeed = func(context.Context, string) {}
	h.openFeed = func(context.Context, string) (feedView, feedAvail) {
		return feedView{
			Page: func(_ context.Context, a protocol.AgentFeedArgs) protocol.AgentFeedData {
				f.pages.Add(1)
				f.mu.Lock()
				defer f.mu.Unlock()
				var items []protocol.AgentFeedItem
				for _, it := range f.items {
					if it.Seq > a.After {
						items = append(items, it)
					}
				}
				if items == nil {
					items = []protocol.AgentFeedItem{}
				}
				return protocol.AgentFeedData{Session: a.Session, Epoch: f.epoch, LastSeq: f.lastSeq, Items: items, Scanning: f.scanning}
			},
			LastSeq:  func() int64 { f.mu.Lock(); defer f.mu.Unlock(); return f.lastSeq },
			Epoch:    func() string { f.mu.Lock(); defer f.mu.Unlock(); return f.epoch },
			Scanning: func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.scanning },
			Health:   func() string { return protocol.FeedOK },
			Close:    func() {},
		}, feedReady
	}
}

func (f *fakeFeed) add(seq int64, id string) {
	f.mu.Lock()
	f.items = append(f.items, protocol.AgentFeedItem{ID: id, Seq: seq})
	f.lastSeq = seq
	f.mu.Unlock()
}

func fakeTimeline(items []protocol.SSHTimelineItem) func(protocol.SessionTimelineArgs) (protocol.SSHTimelineData, error) {
	return func(a protocol.SessionTimelineArgs) (protocol.SSHTimelineData, error) {
		var out []protocol.SSHTimelineItem
		var last int64
		for _, it := range items {
			if it.Seq > a.After {
				out = append(out, it)
			}
			last = max(last, it.Seq)
		}
		if out == nil {
			last = a.After
		}
		return protocol.SSHTimelineData{Session: a.Session, Connections: []protocol.SSHConnInfo{}, Items: out, LastSeq: last}, nil
	}
}

func TestEveryTopicDeliversSeqOne(t *testing.T) {
	w := newWatchTest(t)
	w.session("s1")
	(&fakeFeed{epoch: "e1", lastSeq: 2, items: []protocol.AgentFeedItem{{ID: "a", Seq: 1}, {ID: "b", Seq: 2}}}).install(w.h)
	w.h.timelineRead = fakeTimeline([]protocol.SSHTimelineItem{{ID: "t1", Seq: 1, Order: 1}})
	w.h.talkProbe = func(_ context.Context, id string) (protocol.TalkStateData, error) {
		return protocol.TalkStateData{Session: id, Composer: protocol.ComposerResting, Health: protocol.FeedOK}, nil
	}
	for _, topic := range []string{"sessions", "projects", "status", "talk:s1", "feed:s1", "timeline:s1", "diff:s1"} {
		b := w.box()
		stop, res := w.watch(b, topic, "w1")
		if !res.OK {
			t.Fatalf("%s: refused %+v", topic, res)
		}
		it := mustNext(t, b)
		if it.Topic != topic || it.Seq != 1 || it.WatchID != "w1" {
			t.Fatalf("%s: first item %+v", topic, it)
		}
		if topic == "diff:s1" {
			if d, ok := it.Event.(*protocol.DiffEvent); !ok || d.Rev != 1 {
				t.Fatalf("diff first value %+v", it.Event)
			}
		}
		stop()
	}
}

func TestWatchRefusals(t *testing.T) {
	w := newWatchTest(t)
	if _, res := w.watch(w.box(), "bogus", "w"); res.Code != protocol.WatchCodeInvalidTopic {
		t.Fatalf("invalid topic: %+v", res)
	}
	if _, res := w.watch(w.box(), "talk:nope", "w"); res.Code != protocol.WatchCodeUnknownSession {
		t.Fatalf("unknown session: %+v", res)
	}
	if w.h.active.Load() != 0 {
		t.Fatal("a refused watch left topic state")
	}
}

func TestSessionsSnapshotThenDelta(t *testing.T) {
	w := newWatchTest(t)
	w.session("s1")
	b := w.box()
	stop, _ := w.watch(b, "sessions", "w1")
	defer stop()
	first := mustNext(t, b).Event.(*protocol.SessionsEvent)
	if !first.Reset || first.More || len(first.Upsert) != 1 || first.Upsert[0].ID != "s1" {
		t.Fatalf("snapshot: %+v", first)
	}
	w.session("s2")
	w.h.PokeSession("s2", pokeRow)
	d := mustNext(t, b).Event.(*protocol.SessionsEvent)
	if d.Reset || len(d.Upsert) != 1 || d.Upsert[0].ID != "s2" {
		t.Fatalf("delta: %+v", d)
	}
	w.d.sessions.Delete("s1")
	w.h.PokeSession("s1", pokeRow)
	var gone *protocol.SessionsEvent
	for i := 0; i < 3 && gone == nil; i++ {
		if e := mustNext(t, b).Event.(*protocol.SessionsEvent); len(e.Remove) == 1 {
			gone = e
		}
	}
	if gone == nil || gone.Remove[0] != "s1" {
		t.Fatalf("remove: %+v", gone)
	}
}

func TestSessionsFingerprintIgnoresClock(t *testing.T) {
	row := func(age, ago string, at time.Time) protocol.SessionInfo {
		return protocol.SessionInfo{
			ID: "s", Age: age, HeadlineAgo: ago, PRObservedAt: at,
			Board: &protocol.BoardInfo{UpdatedAgo: ago},
		}
	}
	fp := func(si protocol.SessionInfo) uint64 {
		b, _ := json.Marshal(sessionsVolatile(si))
		return fingerprint(b)
	}
	t0 := time.Now()
	if fp(row("1m", "1m", t0)) != fp(row("61m", "61m", t0.Add(time.Hour))) {
		t.Fatal("clock-derived fields moved the fingerprint")
	}
	other := row("1m", "1m", t0)
	other.Branch = "x"
	if fp(other) == fp(row("1m", "1m", t0)) {
		t.Fatal("a real change did not move the fingerprint")
	}
	ev := func(ago string) []protocol.Event { return []protocol.Event{{ID: "1", To: "idle", Ago: ago}} }
	if eventsFingerprint(ev("1m")) != eventsFingerprint(ev("1h")) {
		t.Fatal("Event.Ago moved the ring fingerprint")
	}
}

func TestObserverPRRefreshIsQuiet(t *testing.T) {
	var p sessionsProjector
	at := time.Now()
	data := func(observed time.Time, age string) protocol.SessionsData {
		return protocol.SessionsData{Sessions: []protocol.SessionInfo{{ID: "s", Age: age, PRObservedAt: observed}}}
	}
	p.project(data(at, "1m"))
	p.ran = true
	if got := p.project(data(at.Add(30*time.Second), "2m")); got != nil {
		t.Fatalf("a PR refresh and a clock tick produced %d events", len(got))
	}
	changed := data(at, "2m")
	changed.Sessions[0].Branch = "b"
	if got := p.project(changed); len(got) != 1 {
		t.Fatalf("a real change produced %d events", len(got))
	}
}

func TestSessionsRemoteProjectionStripsSSH(t *testing.T) {
	si := remoteSessionInfo(protocol.SessionInfo{ID: "s", SSH: &protocol.SSHSessionInfo{}})
	if si.SSH != nil {
		t.Fatal("ssh glance reached the remote projection")
	}
	var p sessionsProjector
	p.project(protocol.SessionsData{Sessions: []protocol.SessionInfo{{ID: "s", SSH: &protocol.SSHSessionInfo{}}}})
	for _, rs := range p.published {
		if rs.info.SSH != nil {
			t.Fatal("projector kept the ssh glance")
		}
	}
}

func TestSessionsResetSplitsIntoParts(t *testing.T) {
	var p sessionsProjector
	var rows []protocol.SessionInfo
	for i := 0; i < 5; i++ {
		rows = append(rows, protocol.SessionInfo{ID: string(rune('a' + i)), Headline: strings.Repeat("x", 200<<10)})
	}
	p.project(protocol.SessionsData{Sessions: rows})
	parts := p.parts()
	if len(parts) < 2 {
		t.Fatalf("want a split reset, got %d part(s)", len(parts))
	}
	for i, e := range parts {
		ev := e.(*protocol.SessionsEvent)
		if (i == 0) != ev.Reset || ev.More != (i < len(parts)-1) || e.Size() > protocol.EventMaxBytes {
			t.Fatalf("part %d malformed: reset=%v more=%v size=%d", i, ev.Reset, ev.More, e.Size())
		}
	}
}

func TestTalkFingerprintIgnoresFeedSeq(t *testing.T) {
	a := talkValue(protocol.TalkStateData{Session: "s", Composer: "resting", FeedSeq: 1, FeedEpoch: "e"}, 0)
	b := talkValue(protocol.TalkStateData{Session: "s", Composer: "resting", FeedSeq: 99, FeedEpoch: "e"}, 0)
	c := talkValue(protocol.TalkStateData{Session: "s", Composer: "resting", FeedSeq: 1, FeedEpoch: "e2"}, 0)
	d := talkValue(protocol.TalkStateData{Session: "s", Composer: "working", FeedSeq: 1, FeedEpoch: "e"}, 0)
	if a.fp != b.fp || a.fp == c.fp || a.fp == d.fp {
		t.Fatal("talk fingerprint must ignore feedSeq only")
	}
}

func TestTalkProbeCadence(t *testing.T) {
	live := session.Session{ID: "s", AgentState: state.AgentIdle}
	for _, tc := range []struct {
		name     string
		s        session.Session
		composer string
		errs     int
		want     time.Duration
	}{
		{"working", live, protocol.ComposerWorking, 0, talkProbeActive},
		{"prompt", live, protocol.ComposerPrompt, 0, talkProbeActive},
		{"modal", live, protocol.ComposerModal, 0, talkProbeActive},
		{"draft", live, protocol.ComposerDraft, 0, talkProbeActive},
		{"unknown", live, protocol.ComposerUnknown, 0, talkProbeActive},
		{"resting", live, protocol.ComposerResting, 0, talkProbeResting},
		{"resting but waiting_input", session.Session{ID: "s", AgentState: state.AgentWaitingInput}, protocol.ComposerResting, 0, talkProbeActive},
		{"agentless", session.Session{ID: "s", Agentless: true}, protocol.ComposerWorking, 0, 0},
		{"goal", session.Session{ID: "s", Kind: session.KindGoal}, protocol.ComposerWorking, 0, 0},
		{"dead agent", session.Session{ID: "s", AgentState: state.AgentDead}, protocol.ComposerWorking, 0, 0},
		{"one read error", live, protocol.ComposerUnknown, 1, 2 * time.Second},
		{"errors cap at resting", live, protocol.ComposerUnknown, 6, talkProbeResting},
	} {
		if got := probeDelay(tc.s, tc.composer, tc.errs); got != tc.want {
			t.Errorf("%s: probeDelay = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTalkProbeRateUnderHookBurst(t *testing.T) {
	w, now := newManualWatchTest(t)
	w.session("s1")
	var probes atomic.Int32
	w.h.talkProbe = func(_ context.Context, id string) (protocol.TalkStateData, error) {
		probes.Add(1)
		return protocol.TalkStateData{Session: id, Composer: protocol.ComposerWorking, Health: protocol.FeedOK}, nil
	}
	(&fakeFeed{epoch: "e"}).install(w.h)
	b := w.box()
	stop, res := w.watch(b, "talk:s1", "w1")
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	defer stop()
	w.step()
	for i := 0; i < 100; i++ {
		w.h.PokeSession("s1", pokeTalk)
	}
	w.step()
	if n := probes.Load(); n > 1 {
		t.Fatalf("a hook burst inside one second caused %d probes", n)
	}
	*now = now.Add(time.Second)
	for i := 0; i < 100; i++ {
		w.h.PokeSession("s1", pokeTalk)
	}
	w.step()
	if n := probes.Load(); n > 2 {
		t.Fatalf("100 hook commits in 1s caused %d probes, want <= 2", n)
	}
}

func TestTalkProbeSharedAcrossWatchers(t *testing.T) {
	w, _ := newManualWatchTest(t)
	w.session("s1")
	var probes atomic.Int32
	w.h.talkProbe = func(_ context.Context, id string) (protocol.TalkStateData, error) {
		probes.Add(1)
		return protocol.TalkStateData{Session: id, Composer: protocol.ComposerResting, Health: protocol.FeedOK}, nil
	}
	(&fakeFeed{epoch: "e"}).install(w.h)
	a, b := w.box(), w.box()
	w.watch(a, "talk:s1", "wa")
	w.watch(b, "talk:s1", "wb")
	w.step()
	if probes.Load() != 1 {
		t.Fatalf("two watchers caused %d probes", probes.Load())
	}
	for _, box := range []*eventbox.Box{a, b} {
		if it := mustNext(t, box); it.Seq != 1 {
			t.Fatalf("watcher missed the shared value: %+v", it)
		}
	}
}

func TestFeedResetThenDeltaThenEpochReset(t *testing.T) {
	w := newWatchTest(t)
	w.session("s1")
	f := &fakeFeed{epoch: "e1"}
	f.add(1, "a")
	f.install(w.h)
	b := w.box()
	stop, _ := w.watch(b, "feed:s1", "w1")
	defer stop()
	first := mustNext(t, b).Event.(*protocol.FeedEvent)
	if !first.Reset || len(first.Items) != 1 || first.Epoch != "e1" {
		t.Fatalf("snapshot: %+v", first)
	}
	f.add(2, "b") // advance between snapshot and publish: nothing is lost
	w.h.PokeSession("s1", pokeFeed)
	d := mustNext(t, b).Event.(*protocol.FeedEvent)
	if d.Reset || len(d.Items) != 1 || d.Items[0].ID != "b" || d.LastSeq != 2 {
		t.Fatalf("delta: %+v", d)
	}
	f.mu.Lock()
	f.epoch = "e2"
	f.mu.Unlock()
	w.h.PokeSession("s1", pokeFeed)
	r := mustNext(t, b).Event.(*protocol.FeedEvent)
	if !r.Reset || r.Epoch != "e2" {
		t.Fatalf("an epoch change must be a reset: %+v", r)
	}
}

func TestRewatchUsesSnapshotCache(t *testing.T) {
	w, now := newManualWatchTest(t)
	w.session("x")
	f := &fakeFeed{epoch: "e"}
	f.add(1, "a")
	f.install(w.h)
	b := w.box()
	for i := 0; i < 30; i++ {
		stop, res := w.watch(b, "feed:x", "w")
		if !res.OK {
			t.Fatalf("%+v", res)
		}
		*now = now.Add(snapshotMinInterval)
		w.step()
		stop()
		b = w.box()
	}
	if n := f.pages.Load(); n != 1 {
		t.Fatalf("30 re-watches of an unmoved feed made %d Page calls, want 1", n)
	}
}

func TestFeedScanBytesBounded(t *testing.T) {
	w, now := newManualWatchTest(t)
	w.session("s")
	f := &fakeFeed{epoch: "e", scanning: true}
	f.install(w.h)
	b := w.box()
	w.watch(b, "feed:s", "w")
	var resets, progress int
	count := func() {
		for {
			it, ok := next(t, b, 20*time.Millisecond)
			if !ok {
				return
			}
			ev := it.Event.(*protocol.FeedEvent)
			if ev.Reset {
				resets++
			} else if ev.Scanning {
				progress++
			}
		}
	}
	w.step()
	count()
	for i := 1; i <= 30; i++ { // 30 s of scanning, the log growing every second
		f.mu.Lock()
		f.lastSeq = int64(i)
		f.mu.Unlock()
		*now = now.Add(time.Second)
		w.h.PokeSession("s", pokeFeed)
		w.step()
		count()
	}
	f.mu.Lock()
	f.scanning = false
	f.mu.Unlock()
	*now = now.Add(time.Second)
	w.h.PokeSession("s", pokeFeed)
	w.step()
	count()
	if resets != 2 {
		t.Fatalf("a scan produced %d resets, want 2 (start and end)", resets)
	}
	if progress > 31 {
		t.Fatalf("scan progress events: %d", progress)
	}
}

func TestTimelineProducerUsesOnlyPublicTimeline(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "watchtopics.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	banned := map[string]bool{"handleSSHTimeline": true, "Timeline": true, "sshStore": true, "sshStores": true, "View": true}
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && banned[sel.Sel.Name] {
			t.Errorf("watchtopics.go reaches the raw timeline through %s", sel.Sel.Name)
		}
		return true
	})
}

func TestTimelineSnapshotWindow(t *testing.T) {
	w, _ := newManualWatchTest(t)
	w.session("s")
	var items []protocol.SSHTimelineItem
	for i := 1; i <= 15; i++ { // 15 x ~200 KiB is over the 2 MiB window
		items = append(items, protocol.SSHTimelineItem{
			ID: "i" + string(rune('a'+i)), Seq: int64(i), Order: int64(i),
			System: &protocol.SSHSystemEvent{Text: strings.Repeat("y", 200<<10)},
		})
	}
	w.h.timelineRead = fakeTimeline(items)
	b := w.box()
	w.watch(b, "timeline:s", "w")
	w.step()
	var got []*protocol.TimelineEvent
	for {
		it, ok := next(t, b, 50*time.Millisecond)
		if !ok {
			break
		}
		got = append(got, it.Event.(*protocol.TimelineEvent))
	}
	if len(got) < 2 {
		t.Fatalf("an over-window snapshot should arrive in parts, got %d", len(got))
	}
	if !got[0].Reset || !got[0].Older || !got[0].More || got[len(got)-1].More {
		t.Fatalf("parts malformed: first=%+v", got[0])
	}
	total, newest := 0, int64(0)
	for _, e := range got {
		total += len(e.Items)
		for _, it := range e.Items {
			newest = max(newest, it.Order)
		}
	}
	if total >= 15 || newest != 15 {
		t.Fatalf("window kept %d items, newest order %d: want the newest only", total, newest)
	}
}

func TestOversizeItemOmitted(t *testing.T) {
	w, _ := newManualWatchTest(t)
	w.session("s")
	w.h.timelineRead = fakeTimeline([]protocol.SSHTimelineItem{
		{ID: "small", Seq: 1, Order: 1},
		{ID: "huge", Seq: 2, Order: 2, System: &protocol.SSHSystemEvent{Text: strings.Repeat("z", 900<<10)}},
	})
	b := w.box()
	w.watch(b, "timeline:s", "w")
	w.step()
	ev := mustNext(t, b).Event.(*protocol.TimelineEvent)
	if len(ev.Items) != 1 || ev.Items[0].ID != "small" || len(ev.Omitted) != 1 || ev.Omitted[0].ID != "huge" {
		t.Fatalf("items=%v omitted=%v", ev.Items, ev.Omitted)
	}
}

func TestTimelineDeltaAndConnections(t *testing.T) {
	w, now := newManualWatchTest(t)
	w.session("s")
	items := []protocol.SSHTimelineItem{{ID: "a", Seq: 1, Order: 1}}
	read := fakeTimeline(items)
	w.h.timelineRead = func(a protocol.SessionTimelineArgs) (protocol.SSHTimelineData, error) { return read(a) }
	b := w.box()
	w.watch(b, "timeline:s", "w")
	w.step()
	mustNext(t, b)
	items = append(items, protocol.SSHTimelineItem{ID: "b", Seq: 2, Order: 2})
	read = fakeTimeline(items)
	w.h.PokeSession("s", pokeTimeline)
	w.step()
	*now = now.Add(time.Second)
	w.step()
	d := mustNext(t, b).Event.(*protocol.TimelineEvent)
	if d.Reset || len(d.Items) != 1 || d.Items[0].ID != "b" || d.LastSeq != 2 {
		t.Fatalf("delta: %+v", d)
	}
}

func TestDiffPokesCollapse(t *testing.T) {
	w, now := newManualWatchTest(t)
	w.session("s")
	b := w.box()
	w.watch(b, "diff:s", "w")
	w.step()
	if e := mustNext(t, b).Event.(*protocol.DiffEvent); e.Rev != 1 {
		t.Fatalf("first rev %d", e.Rev)
	}
	for i := 0; i < 10; i++ {
		w.h.PokeDiff("s", protocol.DiffCauseCheckpoint)
	}
	w.h.PokeDiff("s", protocol.DiffCauseRestore)
	w.step() // folds the pokes in
	*now = now.Add(diffMinInterval + diffDebounce)
	w.step()
	e := mustNext(t, b).Event.(*protocol.DiffEvent)
	if e.Rev != 2 || e.Cause != protocol.DiffCauseRestore {
		t.Fatalf("collapsed pokes should make one event with the latest cause: %+v", e)
	}
	if it, ok := next(t, b, 30*time.Millisecond); ok {
		t.Fatalf("a second event: %+v", it)
	}
}

func TestProbedSessionsLimit(t *testing.T) {
	w := newWatchTest(t)
	for i := 1; i <= 9; i++ {
		w.session("s" + string(rune('0'+i)))
	}
	var codes []string
	for i := 1; i <= 9; i++ {
		// Distinct devices so only the daemon-wide bound applies.
		_, res := w.h.Watch(w.ctx, remote.Peer{DeviceID: "d" + string(rune('0'+i))}, "talk:s"+string(rune('0'+i)), "w", w.box())
		codes = append(codes, res.Code)
	}
	for i, c := range codes {
		if (i < MaxProbedSessions) != (c == "") {
			t.Fatalf("watch %d code %q", i+1, c)
		}
	}
	if codes[8] != protocol.WatchCodeLimit {
		t.Fatalf("ninth session: %q", codes[8])
	}
}

func TestProbedSessionsPerDevice(t *testing.T) {
	w := newWatchTest(t)
	for i := 1; i <= 6; i++ {
		w.session("s" + string(rune('0'+i)))
	}
	a := remote.Peer{DeviceID: "A"}
	var stops []func()
	for i := 1; i <= 4; i++ {
		stop, res := w.h.Watch(w.ctx, a, "feed:s"+string(rune('0'+i)), "w", w.box())
		if !res.OK {
			t.Fatalf("A watch %d: %+v", i, res)
		}
		stops = append(stops, stop)
	}
	if _, res := w.h.Watch(w.ctx, a, "talk:s5", "w", w.box()); res.Code != protocol.WatchCodeLimit {
		t.Fatalf("A's fifth session: %+v", res)
	}
	// Another watch of a session A already probes is free.
	if _, res := w.h.Watch(w.ctx, a, "talk:s1", "w2", w.box()); !res.OK {
		t.Fatalf("A re-watching its own session: %+v", res)
	}
	if _, res := w.h.Watch(w.ctx, remote.Peer{DeviceID: "B"}, "talk:s5", "w", w.box()); !res.OK {
		t.Fatalf("B is blocked by A's slots: %+v", res)
	}
	stops[3]() // A's slot frees
	if _, res := w.h.Watch(w.ctx, a, "talk:s6", "w3", w.box()); !res.OK {
		t.Fatalf("A after a slot freed: %+v", res)
	}
}

func TestStopMatchesWatchIDAndFreesState(t *testing.T) {
	w := newWatchTest(t)
	b := w.box()
	old, _ := w.watch(b, "projects", "w1")
	fresh, _ := w.watch(b, "projects", "w2")
	old() // a late old stop must not remove the new stream
	w.h.mu.Lock()
	ts := w.h.topics["projects"]
	w.h.mu.Unlock()
	if ts == nil {
		t.Fatal("a stale stop removed the new stream")
	}
	if it := mustNext(t, b); it.WatchID != "w2" {
		t.Fatalf("event for %q, want w2", it.WatchID)
	}
	fresh()
	fresh() // idempotent
	if w.h.active.Load() != 0 {
		t.Fatal("topic state survived its last stop")
	}
}

func TestHubShutdownDrains(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	ctx, cancel := context.WithCancel(context.Background())
	d.wg.Add(1)
	go d.watch.run(ctx)
	b := eventbox.New(d.watch.Resnapshot)
	d.watch.Watch(ctx, remote.Peer{DeviceID: "d"}, "projects", "w", b)
	if _, ok := next(t, b, 3*time.Second); !ok {
		t.Fatal("no event")
	}
	cancel()
	done := make(chan struct{})
	go func() { d.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the hub loop did not exit on ctx cancel")
	}
}

func TestTalkFieldsAndHeadChanged(t *testing.T) {
	a := &session.Session{ID: "s", Branch: "b", AgentState: state.AgentWorking}
	b := *a
	if talkFieldsChanged(a, &b) || headChanged(a, &b) {
		t.Fatal("identical sessions changed")
	}
	b.LastSeen = time.Now()
	if talkFieldsChanged(a, &b) {
		t.Fatal("LastSeen must never poke talk")
	}
	b.AtPrompt = true
	if !talkFieldsChanged(a, &b) {
		t.Fatal("AtPrompt must poke talk")
	}
	if !talkFieldsChanged(nil, a) || !talkFieldsChanged(a, nil) {
		t.Fatal("insert and delete must poke talk")
	}
	if headChanged(nil, a) || headChanged(a, nil) {
		t.Fatal("head needs both sides")
	}
	c := *a
	c.Branch = "other"
	if !headChanged(a, &c) {
		t.Fatal("a branch change is a head change")
	}
}
