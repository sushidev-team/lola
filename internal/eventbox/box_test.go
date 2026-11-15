package eventbox

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

type fakeEv struct {
	name      string
	size      int
	part      bool
	mergeable bool
}

func (e *fakeEv) Merge(next protocol.TopicEvent) (protocol.TopicEvent, bool) {
	n, ok := next.(*fakeEv)
	if !ok || !e.mergeable || !n.mergeable {
		return nil, false
	}
	return &fakeEv{name: n.name, size: e.size + n.size, mergeable: true}, true
}
func (e *fakeEv) Size() int  { return e.size }
func (e *fakeEv) Part() bool { return e.part }

func delta(name string, size int) *fakeEv { return &fakeEv{name: name, size: size} }
func mdelta(name string, size int) *fakeEv {
	return &fakeEv{name: name, size: size, mergeable: true}
}
func part(name string, size int) *fakeEv { return &fakeEv{name: name, size: size, part: true} }

type snaps struct {
	mu sync.Mutex
	n  map[string]int
}

func newSnaps() *snaps { return &snaps{n: map[string]int{}} }
func (s *snaps) on(topic string) {
	s.mu.Lock()
	s.n[topic]++
	s.mu.Unlock()
}
func (s *snaps) get(topic string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n[topic]
}

func next(t *testing.T, b *Box) Item {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	it, ok := b.Next(ctx)
	if !ok {
		t.Fatal("Next returned no item")
	}
	return it
}

func none(t *testing.T, b *Box) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if it, ok := b.Next(ctx); ok {
		t.Fatalf("unexpected item %+v", it)
	}
}

func liveSlot(t *testing.T, b *Box, topic, id string) {
	t.Helper()
	b.Start(topic, id)
	if !b.Install(topic, id, []Event{delta("snap", 1)}) {
		t.Fatal("install refused")
	}
	next(t, b)
}

func TestOfferNeverBlocks(t *testing.T) {
	b := New(nil)
	liveSlot(t, b, "talk:a", "w1")
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			b.Offer("talk:a", "w1", delta("x", 100))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Offer blocked")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.deltaBytes > ConnPendingMaxBytes || len(b.slots["talk:a"].q) > SlotQueueMax {
		t.Fatalf("unbounded: %d bytes, %d queued", b.deltaBytes, len(b.slots["talk:a"].q))
	}
}

func TestOverflowBecomesSnapshot(t *testing.T) {
	s := newSnaps()
	b := New(s.on)
	liveSlot(t, b, "talk:a", "w1") // seq 1 taken
	for i := 0; i < SlotQueueMax+1; i++ {
		b.Offer("talk:a", "w1", delta("d", 1))
	}
	if s.get("talk:a") != 1 {
		t.Fatalf("onSnap calls = %d", s.get("talk:a"))
	}
	if !b.WantsSnapshot("talk:a", "w1") {
		t.Fatal("slot should want a snapshot")
	}
	b.Offer("talk:a", "w1", delta("late", 1)) // dropped
	none(t, b)
	if !b.Install("talk:a", "w1", []Event{part("s2", 1)}) {
		t.Fatal("install refused")
	}
	it := next(t, b)
	if it.Seq != 2 || it.Event.(*fakeEv).name != "s2" {
		t.Fatalf("seq must continue: %+v", it)
	}
}

func TestMultipartSnapshotUnderContinuousDeltas(t *testing.T) {
	s := newSnaps()
	b := New(s.on)
	b.Start("feed:a", "w1")
	if !b.Install("feed:a", "w1", []Event{part("p1", 10), part("p2", 10), part("p3", 10)}) {
		t.Fatal("install refused")
	}
	for i := 0; i < 1000; i++ {
		b.Offer("feed:a", "w1", mdelta("d", 1))
	}
	var names []string
	for i := 0; i < 4; i++ {
		names = append(names, next(t, b).Event.(*fakeEv).name)
	}
	if names[0] != "p1" || names[1] != "p2" || names[2] != "p3" || names[3] != "d" {
		t.Fatalf("order %v", names)
	}
	none(t, b)
	if s.get("feed:a") != 0 {
		t.Fatal("unexpected resnapshot")
	}
}

func TestResnapshotLimitEndsStream(t *testing.T) {
	s := newSnaps()
	b := New(s.on)
	now := time.Unix(1000, 0)
	b.now = func() time.Time { return now }
	b.Start("diff:a", "w1")
	for i := 0; i < ResnapshotLimit; i++ {
		if !b.Install("diff:a", "w1", []Event{delta("s", 1)}) {
			t.Fatalf("install %d refused", i)
		}
		for j := 0; j < SlotQueueMax+1; j++ {
			b.Offer("diff:a", "w1", delta("d", 1))
		}
	}
	if s.get("diff:a") != ResnapshotLimit-1 {
		t.Fatalf("onSnap = %d", s.get("diff:a"))
	}
	it := next(t, b)
	if it.End == nil || it.End.Code != protocol.CodeRateLimited {
		t.Fatalf("want rate_limited end, got %+v", it)
	}
	// old overflows age out of the window
	b.Start("diff:b", "w1")
	b.Install("diff:b", "w1", []Event{delta("s", 1)})
	for j := 0; j < SlotQueueMax+1; j++ {
		b.Offer("diff:b", "w1", delta("d", 1))
	}
	now = now.Add(ResnapshotWindow + time.Second)
	b.Install("diff:b", "w1", []Event{delta("s", 1)})
	for j := 0; j < SlotQueueMax+1; j++ {
		b.Offer("diff:b", "w1", delta("d", 1))
	}
	if !b.WantsSnapshot("diff:b", "w1") {
		t.Fatal("window should have forgotten the first overflow")
	}
}

func TestSnapshotBudgetDefersInstall(t *testing.T) {
	s := newSnaps()
	b := New(s.on)
	b.Start("timeline:a", "w1")
	b.Start("timeline:b", "w1")
	if !b.Install("timeline:a", "w1", []Event{part("a", BoxSnapshotMaxBytes-1), part("a2", 1)}) {
		t.Fatal("first install refused")
	}
	if b.Install("timeline:b", "w1", []Event{part("b", 100)}) {
		t.Fatal("over-budget install accepted")
	}
	if !b.WantsSnapshot("timeline:b", "w1") {
		t.Fatal("slot must keep wanting")
	}
	next(t, b) // drains a's big part
	if s.get("timeline:b") != 1 {
		t.Fatalf("drain should poke the waiting slot, got %d", s.get("timeline:b"))
	}
	if !b.Install("timeline:b", "w1", []Event{part("b", 100)}) {
		t.Fatal("install after drain refused")
	}
}

func TestConnPendingBudgetDropsLargestDelta(t *testing.T) {
	s := newSnaps()
	b := New(s.on)
	liveSlot(t, b, "talk:a", "w1")
	liveSlot(t, b, "talk:b", "w1")
	b.Offer("talk:a", "w1", delta("small", 100))
	b.Offer("talk:b", "w1", delta("big", ConnPendingMaxBytes-50))
	b.Offer("talk:a", "w1", delta("trigger", 100))
	if s.get("talk:b") != 1 || s.get("talk:a") != 0 {
		t.Fatalf("largest must overflow: a=%d b=%d", s.get("talk:a"), s.get("talk:b"))
	}
	if it := next(t, b); it.Topic != "talk:a" {
		t.Fatalf("a keeps its deltas: %+v", it)
	}
}

func TestPriorityTalkFirst(t *testing.T) {
	b := New(nil)
	order := []string{"projects", "status", "diff:a", "timeline:a", "feed:a", "sessions", "talk:a"}
	for _, topic := range order {
		b.Start(topic, "w")
		b.Install(topic, "w", []Event{delta(topic, 1)})
	}
	want := []string{"talk:a", "sessions", "feed:a", "timeline:a", "diff:a", "status", "projects"}
	for _, w := range want {
		if got := next(t, b).Topic; got != w {
			t.Fatalf("got %s want %s", got, w)
		}
	}
}

func TestRoundRobinWithinKind(t *testing.T) {
	b := New(nil)
	for _, topic := range []string{"talk:a", "talk:b"} {
		b.Start(topic, "w")
		b.Install(topic, "w", []Event{part("1", 1), part("2", 1)})
	}
	var got []string
	for i := 0; i < 4; i++ {
		got = append(got, next(t, b).Topic)
	}
	if got[0] == got[1] || got[2] == got[3] {
		t.Fatalf("not round robin: %v", got)
	}
}

func TestRestartResetsSeq(t *testing.T) {
	b := New(nil)
	liveSlot(t, b, "talk:a", "w1")
	b.Offer("talk:a", "w1", delta("x", 1))
	if it := next(t, b); it.Seq != 2 {
		t.Fatalf("seq %d", it.Seq)
	}
	b.Start("talk:a", "w2")
	b.Install("talk:a", "w2", []Event{delta("s", 1)})
	if it := next(t, b); it.Seq != 1 || it.WatchID != "w2" {
		t.Fatalf("restart: %+v", it)
	}
}

func TestStopMatchesWatchID(t *testing.T) {
	b := New(nil)
	b.Start("talk:a", "w1")
	b.Start("talk:a", "w2")
	b.Stop("talk:a", "w1") // stale
	if !b.WantsSnapshot("talk:a", "w2") {
		t.Fatal("stale stop removed the new stream")
	}
	if !b.Install("talk:a", "w2", []Event{delta("s", 1)}) {
		t.Fatal("install refused")
	}
	b.Stop("talk:a", "w2")
	none(t, b)
	if b.Install("talk:a", "w2", []Event{delta("s", 1)}) {
		t.Fatal("install on a stopped slot")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.partBytes != 0 || b.deltaBytes != 0 {
		t.Fatal("bytes leaked")
	}
}

func TestEndIsLast(t *testing.T) {
	b := New(nil)
	liveSlot(t, b, "talk:a", "w1")
	b.Offer("talk:a", "w1", delta("x", 1))
	b.End("talk:a", "w1", protocol.ErrPayload{Code: protocol.CodeDenied})
	b.Offer("talk:a", "w1", delta("y", 1)) // refused after End
	if it := next(t, b); it.Event == nil {
		t.Fatalf("queued event first: %+v", it)
	}
	it := next(t, b)
	if it.End == nil || it.End.Code != protocol.CodeDenied || it.Event != nil {
		t.Fatalf("end item: %+v", it)
	}
	none(t, b)
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.slots) != 0 {
		t.Fatal("slot not removed after End taken")
	}
}

func TestNextBlocksUntilInstallAndStopsOnClose(t *testing.T) {
	b := New(nil)
	b.Start("status", "w")
	got := make(chan Item, 1)
	go func() {
		it, _ := b.Next(context.Background())
		got <- it
	}()
	time.Sleep(10 * time.Millisecond)
	b.Install("status", "w", []Event{delta("s", 1)})
	select {
	case it := <-got:
		if it.Topic != "status" {
			t.Fatalf("%+v", it)
		}
	case <-time.After(time.Second):
		t.Fatal("Next did not wake")
	}
	done := make(chan bool, 1)
	go func() { _, ok := b.Next(context.Background()); done <- ok }()
	time.Sleep(10 * time.Millisecond)
	b.Close()
	if ok := <-done; ok {
		t.Fatal("Next after Close must be false")
	}
	b.Offer("status", "w", delta("x", 1)) // no panic
}
