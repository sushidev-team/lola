// Package eventbox is the per-connection mailbox between event producers and
// one connection's pump. Producers never block: a slow link costs bounded
// memory, then a fresh snapshot, then (after repeated overflow) the end of
// that one stream. The contract is docs/mobile/02-events.md §8.
//
// The package is a stdlib + protocol leaf: it knows nothing about TLS, frames
// or the daemon, so a future local stream can share it.
package eventbox

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// Box limits (docs/mobile/02-events.md §9).
const (
	// SlotQueueMax is the number of delta events a live slot may hold.
	SlotQueueMax = 2
	// ConnPendingMaxBytes bounds the delta bytes queued across all slots.
	ConnPendingMaxBytes = 2 << 20
	// BoxSnapshotMaxBytes bounds the installed snapshot-part bytes of one box.
	BoxSnapshotMaxBytes = 4 << 20
	// ResnapshotLimit overflows of one slot within ResnapshotWindow end its
	// stream with rate_limited instead of requesting another snapshot.
	ResnapshotLimit  = 3
	ResnapshotWindow = 60 * time.Second
)

// Event is one pushed value; see protocol.TopicEvent.
type Event = protocol.TopicEvent

// Item is what the pump takes: one event to frame, or a stream's final err.
type Item struct {
	Topic, WatchID string
	Seq            uint64               // assigned at take time; 0 for an End item
	Event          Event                // nil when End != nil
	End            *protocol.ErrPayload // the stream's final err frame
}

type slotState uint8

const (
	awaitingSnapshot slotState = iota // Start called, no snapshot installed yet
	live                              // snapshot installed, deltas accepted
	needsSnapshot                     // overflowed: queue dropped, snapshot wanted
)

type slot struct {
	watchID string
	state   slotState
	seq     uint64

	// q holds the first nparts installed parts, then deltas. While nparts > 0
	// at most one delta (the tail) follows them.
	q          []Event
	nparts     int
	deltaBytes int

	deferred  bool        // an Install was refused for the snapshot budget
	overflows []time.Time // within ResnapshotWindow
	end       *protocol.ErrPayload
}

// Box is one connection's mailbox. All methods are safe for concurrent use and
// none blocks except Next.
type Box struct {
	mu         sync.Mutex
	onSnap     func(topic string)
	now        func() time.Time // seam for tests
	slots      map[string]*slot
	rr         map[string]string // kind -> last topic served
	partBytes  int               // queued snapshot-part bytes
	deltaBytes int               // queued delta bytes
	closed     bool
	wake       chan struct{} // cap 1
}

// New makes one box per connection. onSnap is called under Box.mu when a slot
// needs a snapshot it does not have; it must be a poke (lock level L5 only).
func New(onSnap func(topic string)) *Box {
	return &Box{
		onSnap: onSnap,
		now:    time.Now,
		slots:  map[string]*slot{},
		rr:     map[string]string{},
		wake:   make(chan struct{}, 1),
	}
}

func (b *Box) signal() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func (b *Box) poke(topic string) {
	if b.onSnap != nil {
		b.onSnap(topic)
	}
}

func (b *Box) cur(topic, watchID string) *slot {
	if b.closed {
		return nil
	}
	s := b.slots[topic]
	if s == nil || s.watchID != watchID {
		return nil
	}
	return s
}

// drop discards every queued event of s and releases its byte accounting.
func (b *Box) drop(s *slot) {
	for i, ev := range s.q {
		if i < s.nparts {
			b.partBytes -= ev.Size()
		} else {
			b.deltaBytes -= ev.Size()
		}
	}
	s.q, s.nparts, s.deltaBytes = nil, 0, 0
}

// Start begins a stream: slot := awaiting_snapshot, seq 0. A slot of the same
// topic (a re-watch) is replaced, whatever its watch id.
func (b *Box) Start(topic, watchID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	if old := b.slots[topic]; old != nil {
		b.drop(old)
	}
	b.slots[topic] = &slot{watchID: watchID, state: awaitingSnapshot}
}

// WantsSnapshot reports whether the slot of this watch id needs a snapshot.
func (b *Box) WantsSnapshot(topic, watchID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.cur(topic, watchID)
	return s != nil && s.end == nil && s.state != live
}

// Install replaces the slot's queue with a snapshot (parts, in order). It
// returns false for a stale watch id, a closed box, an ended slot, a slot that
// does not want one, or when the box's snapshot budget would be exceeded (the
// slot then stays wanting and is poked once the budget drains).
func (b *Box) Install(topic, watchID string, parts []Event) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.cur(topic, watchID)
	if s == nil || s.end != nil || s.state == live {
		return false
	}
	total := 0
	for _, p := range parts {
		total += p.Size()
	}
	if b.partBytes > 0 && b.partBytes+total > BoxSnapshotMaxBytes {
		s.deferred = true
		return false
	}
	b.drop(s)
	s.q = append([]Event(nil), parts...)
	s.nparts = len(parts)
	b.partBytes += total
	s.state = live
	s.deferred = false
	b.signal()
	return true
}

// Offer queues a delta on a live slot; it never blocks and never fails. A
// delta that cannot be kept (slot full and unmergeable, or the connection's
// delta budget) turns the slot into needs_snapshot.
func (b *Box) Offer(topic, watchID string, ev Event) {
	if ev == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.cur(topic, watchID)
	if s == nil || s.end != nil || s.state != live {
		return // awaiting/needs: the coming snapshot supersedes it
	}
	if !b.addDelta(s, ev) {
		b.overflow(topic, s)
		return
	}
	for b.deltaBytes > ConnPendingMaxBytes {
		t, big := b.largestDelta()
		if big == nil {
			break
		}
		b.overflow(t, big)
	}
	b.signal()
}

// addDelta merges ev into the tail or appends it; false means overflow.
func (b *Box) addDelta(s *slot, ev Event) bool {
	if n := len(s.q); n > s.nparts {
		tail := s.q[n-1]
		if !ev.Part() && !tail.Part() {
			if m, ok := tail.Merge(ev); ok {
				b.deltaBytes += m.Size() - tail.Size()
				s.deltaBytes += m.Size() - tail.Size()
				s.q[n-1] = m
				return true
			}
		}
		if s.nparts > 0 || n-s.nparts >= SlotQueueMax {
			return false
		}
	}
	s.q = append(s.q, ev)
	b.deltaBytes += ev.Size()
	s.deltaBytes += ev.Size()
	return true
}

func (b *Box) largestDelta() (string, *slot) {
	var (
		topic string
		best  *slot
	)
	for t, s := range b.slots {
		if s.deltaBytes > 0 && (best == nil || s.deltaBytes > best.deltaBytes ||
			(s.deltaBytes == best.deltaBytes && t < topic)) {
			topic, best = t, s
		}
	}
	return topic, best
}

// overflow drops the slot's queue and asks for a snapshot, or ends the stream
// when it has overflowed ResnapshotLimit times within ResnapshotWindow.
func (b *Box) overflow(topic string, s *slot) {
	b.drop(s)
	now := b.now()
	kept := s.overflows[:0]
	for _, t := range s.overflows {
		if now.Sub(t) < ResnapshotWindow {
			kept = append(kept, t)
		}
	}
	s.overflows = append(kept, now)
	if len(s.overflows) >= ResnapshotLimit {
		s.end = &protocol.ErrPayload{
			Code:    protocol.CodeRateLimited,
			Message: "this view changes faster than this link",
		}
		b.signal()
		return
	}
	s.state = needsSnapshot
	s.deferred = false
	b.poke(topic)
}

// End queues the stream's final err frame; Next returns it after the events
// already queued and then removes the slot. Later offers and installs for the
// slot are refused.
func (b *Box) End(topic, watchID string, e protocol.ErrPayload) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.cur(topic, watchID)
	if s == nil || s.end != nil {
		return
	}
	s.end = &e
	b.signal()
}

// Stop drops the slot if its watch id matches; a late stop of an old stream
// leaves the new one alone.
func (b *Box) Stop(topic, watchID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.cur(topic, watchID)
	if s == nil {
		return
	}
	b.drop(s)
	delete(b.slots, topic)
	b.drained()
}

// Close ends the box: every slot is dropped and Next returns false.
func (b *Box) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	b.slots = map[string]*slot{}
	b.partBytes, b.deltaBytes = 0, 0
	b.signal()
}

// Next blocks until an item is ready, ctx is done or the box is closed (false).
func (b *Box) Next(ctx context.Context) (Item, bool) {
	for {
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return Item{}, false
		}
		if it, ok := b.take(); ok {
			b.mu.Unlock()
			return it, true
		}
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return Item{}, false
		case <-b.wake:
		}
	}
}

// kindRank orders topics: talk > sessions > feed > timeline > diff > status >
// projects.
var kindRank = map[string]int{
	protocol.TopicTalk: 0, protocol.TopicSessions: 1, protocol.TopicFeed: 2,
	protocol.TopicTimeline: 3, protocol.TopicDiff: 4, protocol.TopicStatus: 5,
	protocol.TopicProjects: 6,
}

func (s *slot) ready() bool {
	return len(s.q) > 0 && s.state == live || s.end != nil && len(s.q) == 0
}

// take picks the next ready slot by kind priority, round-robin within a kind.
func (b *Box) take() (Item, bool) {
	best, bestRank := []string(nil), len(kindRank)
	for t, s := range b.slots {
		if !s.ready() {
			continue
		}
		kind, _, ok := protocol.ParseTopic(t)
		if !ok {
			continue
		}
		switch r := kindRank[kind]; {
		case r < bestRank:
			best, bestRank = []string{t}, r
		case r == bestRank:
			best = append(best, t)
		}
	}
	if len(best) == 0 {
		return Item{}, false
	}
	sort.Strings(best)
	kind, _, _ := protocol.ParseTopic(best[0])
	pick := best[0]
	for _, t := range best {
		if t > b.rr[kind] {
			pick = t
			break
		}
	}
	b.rr[kind] = pick
	s := b.slots[pick]
	if len(s.q) == 0 { // ready() means s.end != nil
		it := Item{Topic: pick, WatchID: s.watchID, End: s.end}
		delete(b.slots, pick)
		return it, true
	}
	ev := s.q[0]
	s.q[0] = nil
	s.q = s.q[1:]
	if s.nparts > 0 {
		s.nparts--
		b.partBytes -= ev.Size()
		b.drained()
	} else {
		b.deltaBytes -= ev.Size()
		s.deltaBytes -= ev.Size()
	}
	s.seq++
	return Item{Topic: pick, WatchID: s.watchID, Seq: s.seq, Event: ev}, true
}

// drained pokes the slots whose Install was refused for budget once queued
// part bytes are back under it.
func (b *Box) drained() {
	if b.partBytes >= BoxSnapshotMaxBytes {
		return
	}
	for t, s := range b.slots {
		if s.deferred {
			s.deferred = false
			b.poke(t)
		}
	}
}
