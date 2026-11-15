package daemon

import (
	"context"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sushidev-team/lola/internal/eventbox"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/sshstore"
)

// The event push hub (contract: docs/mobile/02-events.md §7). One watchHub per
// Daemon owns one topicState per WATCHED topic and nothing for an unwatched one,
// so with no phone watching every poke costs one atomic load.
//
// Everything here is display-only. A watch counts as a viewer exactly like an
// open desktop Conversation; no producer types, answers or moves a field the
// pane did not prove.
//
// Lock order (total): L1 producer state (sessionsProjector.mu, feedPub.mu,
// timelinePub.mu) > L2 the daemon's data locks (store, feedHub, feedEntry,
// sshstore) > L3 topicState.mu > L4 eventbox.Box.mu > L5 leaves (pokeMu,
// watchHub.mu, probed). A producer computes with no lock above L1 held, then
// takes topicState.mu and in ONE critical section offers the delta to live
// slots and installs the snapshot into waiting ones.

// Limits of the daemon side (the wire limits live in protocol).
const (
	MaxProbedSessions          = feedSessions // pinned readers never exceed the feed LRU
	MaxProbedSessionsPerDevice = 4
	watchProducerConcurrency   = 4 // = remote's reqConcurrency
	watchTick                  = 250 * time.Millisecond
	snapshotMinInterval        = 250 * time.Millisecond
)

// pokeBits say what a poke means for one session (or, via PokeGlobal, for
// everyone).
type pokeBits uint8

const (
	pokeRow         pokeBits = 1 << iota // the session's SessionInfo may have changed
	pokeTalk                             // probe talk:<id> soon
	pokeFeed                             // feed:<id> moved (a reader advanced)
	pokeTimeline                         // timeline:<id> moved
	pokeDiff                             // diff:<id> maybe changed (cause in diffs)
	pokeSessionsAll                      // every row (config reload)
	pokeStatus
	pokeProjects
)

// timing is a topic's schedule: a dirty topic runs debounce after its first
// poke and at least minInterval after its previous run; tick (when set) runs it
// regardless, while watched.
type timing struct {
	debounce, minInterval, tick time.Duration
}

// topicOut is what one producer run hands the hub.
type topicOut struct {
	// Replace topics: the new value (nil: nothing changed) and its fingerprint.
	value eventbox.Event
	fp    uint64
	// Delta topics: events for live slots, in order, and (only when a slot
	// wanted one) the snapshot parts at the producer's new cursor.
	deltas []eventbox.Event
	parts  []eventbox.Event
	// end ends every stream of the topic (frame_too_large).
	end *protocol.ErrPayload
	// again schedules the next run after this long; 0 parks the topic until a poke.
	again time.Duration
}

// producer computes one topic. build runs on a sem slot under no lock above its
// own L1 lock. want says some slot is waiting for a snapshot; snapOnly says no
// poke or tick is due, only a snapshot is wanted; cause is the latest diff
// cause (diff topics only).
type producer interface {
	timing() timing
	timeout() time.Duration
	replace() bool // a replace topic keeps its last value in topicState
	build(ctx context.Context, ts *topicState, snapOnly, want bool, cause string) topicOut
}

type subInfo struct {
	watchID string
	device  string
}

type topicState struct {
	mu    sync.Mutex
	topic string
	kind  string
	sess  string
	dead  bool                      // set when the last sub left; Watch retries the lookup
	subs  map[*eventbox.Box]subInfo // box -> stream
	last  eventbox.Event            // replace topics: last published value; nil before the first
	fp    uint64                    // fingerprint of last
	bigFP uint64                    // fingerprint of the last value that was too large (logged once)

	prod producer

	// Scheduling, guarded by watchHub.mu.
	dirty      bool
	dirtyAt    time.Time
	snapWanted bool
	due        time.Time
	inFlight   bool
	ran        bool
	lastStart  time.Time
	cause      string
}

type watchHub struct {
	d *Daemon

	mu     sync.Mutex                // LEAF: guards topics, probed and the scheduling fields; never held while taking another lock
	topics map[string]*topicState    // only watched topics
	probed map[string]map[string]int // session id -> device key -> watch refcount (talk/feed)
	revs   map[string]int64          // diff revision per session, daemon memory
	pubs   map[string]producer       // feed/timeline publishers kept across an unwatch
	active atomic.Int32              // len(topics); 0 makes every poke a no-op

	pokeMu sync.Mutex          // LEAF: taken under store locks and Box.mu, takes nothing
	pokes  map[string]pokeBits // session id -> bits
	diffs  map[string]string   // session id -> latest diff cause
	snaps  map[string]bool     // topics with a slot waiting for a snapshot
	global pokeBits
	wake   chan struct{} // cap 1, non-blocking send

	sem  chan struct{}
	runs sync.WaitGroup // producer runs, drained when the loop exits

	// Seams (tests replace them).
	now          func() time.Time
	talkProbe    func(ctx context.Context, id string) (protocol.TalkStateData, error)
	timelineRead func(a protocol.SessionTimelineArgs) (protocol.SSHTimelineData, error)
	openFeed     func(ctx context.Context, id string) (feedView, feedAvail)
	advanceFeed  func(ctx context.Context, id string)
}

func newWatchHub(d *Daemon) *watchHub {
	h := &watchHub{
		d:      d,
		topics: map[string]*topicState{},
		probed: map[string]map[string]int{},
		revs:   map[string]int64{},
		pubs:   map[string]producer{},
		pokes:  map[string]pokeBits{},
		diffs:  map[string]string{},
		snaps:  map[string]bool{},
		wake:   make(chan struct{}, 1),
		sem:    make(chan struct{}, watchProducerConcurrency),
		now:    time.Now,
	}
	h.talkProbe = func(ctx context.Context, id string) (protocol.TalkStateData, error) {
		return d.handleTalkState(ctx, protocol.TalkStateArgs{Session: id})
	}
	h.timelineRead = func(a protocol.SessionTimelineArgs) (protocol.SSHTimelineData, error) {
		return d.handleSessionTimeline(a)
	}
	h.openFeed = d.openFeedView
	h.advanceFeed = d.advanceFeedFor
	return h
}

// ---- poke API: non-blocking, safe under any lock in the daemon ----

func (h *watchHub) signal() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// PokeSession records that something about a session may have changed.
func (h *watchHub) PokeSession(id string, bits pokeBits) {
	if h.active.Load() == 0 {
		return
	}
	h.pokeMu.Lock()
	h.pokes[id] |= bits
	h.pokeMu.Unlock()
	h.signal()
}

// PokeDiff records that diff:<id> may have changed, and why.
func (h *watchHub) PokeDiff(id, cause string) {
	if h.active.Load() == 0 {
		return
	}
	h.pokeMu.Lock()
	h.pokes[id] |= pokeDiff
	h.diffs[id] = cause
	h.pokeMu.Unlock()
	h.signal()
}

// PokeGlobal records a change that concerns every session or a global topic.
func (h *watchHub) PokeGlobal(bits pokeBits) {
	if h.active.Load() == 0 {
		return
	}
	h.pokeMu.Lock()
	h.global |= bits
	h.pokeMu.Unlock()
	h.signal()
}

// Resnapshot is the box's onSnap: a slot of topic wants a snapshot.
func (h *watchHub) Resnapshot(topic string) {
	if h.active.Load() == 0 {
		return
	}
	h.pokeMu.Lock()
	h.snaps[topic] = true
	h.pokeMu.Unlock()
	h.signal()
}

// ---- Watch ----

func deviceKey(p remote.Peer) string {
	if p.DeviceID == "" {
		return "local"
	}
	return p.DeviceID
}

// watchable says whether a session topic of this kind may be watched now:
// talk and diff need a live session; feed and timeline also accept an ended,
// still readable one.
func (h *watchHub) watchable(kind, id string) bool {
	if _, ok := h.d.sessions.Get(id); ok {
		return true
	}
	if kind == protocol.TopicTalk || kind == protocol.TopicDiff {
		return false
	}
	return sshstore.ReadEnded(h.d.home, id) != nil
}

// maxCachedPubs bounds the feed and timeline publishers kept across an unwatch.
const maxCachedPubs = 16

// newProducer builds the producer of a topic. The feed and timeline publishers
// survive their topic's last unwatch (bounded), so a re-watch with an unmoved
// cursor is served from their snapshot cache instead of a recompute. The caller
// holds h.mu.
func (h *watchHub) newProducer(kind, id string) producer {
	if kind == protocol.TopicFeed || kind == protocol.TopicTimeline {
		key := kind + ":" + id
		if p, ok := h.pubs[key]; ok {
			return p
		}
		var p producer
		if kind == protocol.TopicFeed {
			p = &feedProducer{h: h, id: id}
		} else {
			p = &timelineProducer{h: h, id: id}
		}
		for k := range h.pubs {
			if len(h.pubs) < maxCachedPubs {
				break
			}
			if h.topics[k] == nil {
				delete(h.pubs, k)
			}
		}
		h.pubs[key] = p
		return p
	}
	switch kind {
	case protocol.TopicSessions:
		return &sessionsProducer{h: h}
	case protocol.TopicProjects:
		return &projectsProducer{h: h}
	case protocol.TopicStatus:
		return &statusProducer{h: h}
	case protocol.TopicTalk:
		return &talkProducer{h: h, id: id}
	default:
		return &diffProducer{h: h, id: id}
	}
}

// admitLocked is the probe-slot rule (spec §9): a talk or feed watch of S by D
// is admitted when S is already probed for D, or D probes fewer than
// MaxProbedSessionsPerDevice sessions and (S is probed by someone, or fewer than
// MaxProbedSessions are probed). The caller holds h.mu.
func (h *watchHub) admitLocked(id, dev string) bool {
	m := h.probed[id]
	if m[dev] > 0 {
		return true
	}
	n := 0
	for _, per := range h.probed {
		if per[dev] > 0 {
			n++
		}
	}
	if n >= MaxProbedSessionsPerDevice {
		return false
	}
	if len(m) > 0 {
		return true
	}
	return len(h.probed) < MaxProbedSessions
}

func isProbeKind(kind string) bool { return kind == protocol.TopicTalk || kind == protocol.TopicFeed }

// Watch registers box for topic on behalf of peer. It builds nothing and
// performs no I/O: the snapshot follows from a producer run.
func (h *watchHub) Watch(ctx context.Context, peer remote.Peer, topic, watchID string, box *eventbox.Box) (func(), protocol.WatchResult) {
	res := protocol.WatchResult{Topic: topic}
	refuse := func(code, msg string) (func(), protocol.WatchResult) {
		res.Code, res.Message = code, msg
		return func() {}, res
	}
	kind, sid, ok := protocol.ParseTopic(topic)
	if !ok {
		return refuse(protocol.WatchCodeInvalidTopic, "not a topic this daemon streams")
	}
	if sid != "" && !h.watchable(kind, sid) {
		return refuse(protocol.WatchCodeUnknownSession, "no such session")
	}
	dev := deviceKey(peer)

	var ts *topicState
	for {
		h.mu.Lock()
		if isProbeKind(kind) && !h.admitLocked(sid, dev) {
			h.mu.Unlock()
			return refuse(protocol.WatchCodeLimit, "too many watched sessions")
		}
		ts = h.topics[topic]
		if ts == nil {
			ts = &topicState{topic: topic, kind: kind, sess: sid, subs: map[*eventbox.Box]subInfo{}, prod: h.newProducer(kind, sid)}
			h.topics[topic] = ts
			h.active.Store(int32(len(h.topics)))
		}
		pin := false
		if isProbeKind(kind) {
			pin = h.probeAddLocked(sid, dev)
		}
		h.mu.Unlock()
		if pin {
			h.d.feedHub().pin(sid)
		}

		ts.mu.Lock()
		if ts.dead {
			ts.mu.Unlock()
			h.releaseProbe(kind, sid, dev)
			continue
		}
		ts.subs[box] = subInfo{watchID: watchID, device: dev}
		box.Start(topic, watchID)
		ts.mu.Unlock()
		break
	}

	h.mu.Lock()
	ts.snapWanted = true
	h.mu.Unlock()
	h.signal()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			ts.mu.Lock()
			if sub, ok := ts.subs[box]; ok && sub.watchID == watchID {
				delete(ts.subs, box)
			}
			if len(ts.subs) == 0 {
				ts.dead = true
			}
			dead := ts.dead
			ts.mu.Unlock()
			h.mu.Lock()
			if dead && h.topics[topic] == ts {
				delete(h.topics, topic)
				h.active.Store(int32(len(h.topics)))
			}
			h.mu.Unlock()
			h.releaseProbe(kind, sid, dev)
		})
	}
	res.OK = true
	return stop, res
}

// probeAddLocked counts a talk/feed watch and reports whether the session just
// became pinned. The caller holds h.mu.
func (h *watchHub) probeAddLocked(id, dev string) bool {
	m := h.probed[id]
	first := m == nil
	if first {
		m = map[string]int{}
		h.probed[id] = m
	}
	m[dev]++
	return first
}

// releaseProbe undoes one probeAddLocked and unpins the reader when it was the
// last.
func (h *watchHub) releaseProbe(kind, id, dev string) {
	if !isProbeKind(kind) {
		return
	}
	h.mu.Lock()
	unpin := false
	if m := h.probed[id]; m != nil {
		if m[dev] > 0 {
			m[dev]--
		}
		if m[dev] <= 0 {
			delete(m, dev)
		}
		if len(m) == 0 {
			delete(h.probed, id)
			unpin = true
		}
	}
	h.mu.Unlock()
	if unpin {
		h.d.feedHub().unpin(id)
	}
}

// ---- the loop ----

// run is the hub loop. It ends on ctx, after the producer runs it started.
func (h *watchHub) run(ctx context.Context) {
	defer h.d.wg.Done()
	defer h.runs.Wait()
	for {
		var tc <-chan time.Time
		var timer *time.Timer
		if h.active.Load() > 0 {
			timer = time.NewTimer(watchTick)
			tc = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-h.wake:
		case <-tc:
		}
		if timer != nil {
			timer.Stop()
		}
		h.safeStep(ctx)
	}
}

func (h *watchHub) safeStep(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			h.d.logf("", "watch hub: panic in loop: %v", r)
		}
	}()
	h.step(ctx)
}

// step is one loop iteration: fold the pokes into the topics, then start every
// producer run that is due. It takes no lock above L5 and does no I/O.
func (h *watchHub) step(ctx context.Context) {
	now := h.now()
	h.pokeMu.Lock()
	var pokes map[string]pokeBits
	var diffs map[string]string
	var snaps map[string]bool
	global := h.global
	if len(h.pokes) > 0 || len(h.diffs) > 0 || len(h.snaps) > 0 {
		pokes, diffs, snaps = h.pokes, h.diffs, h.snaps
		h.pokes, h.diffs, h.snaps = map[string]pokeBits{}, map[string]string{}, map[string]bool{}
	}
	h.global = 0
	h.pokeMu.Unlock()

	type launch struct {
		ts     *topicState
		snap   bool
		cause  string
		starts time.Time
	}
	var runs []launch

	h.mu.Lock()
	mark := func(topic string) *topicState {
		ts := h.topics[topic]
		if ts != nil && !ts.dirty {
			ts.dirty, ts.dirtyAt = true, now
		}
		return ts
	}
	if global&(pokeSessionsAll|pokeRow) != 0 {
		mark(protocol.TopicSessions)
	}
	if global&pokeStatus != 0 {
		mark(protocol.TopicStatus)
	}
	if global&pokeProjects != 0 {
		mark(protocol.TopicProjects)
	}
	for id, bits := range pokes {
		if bits&pokeRow != 0 {
			mark(protocol.TopicSessions)
		}
		if bits&pokeTalk != 0 {
			if ts := h.topics[protocol.TopicTalk+":"+id]; ts != nil {
				due := ts.lastStart.Add(talkProbeMinGap)
				if due.Before(now) {
					due = now
				}
				if ts.due.IsZero() || due.Before(ts.due) {
					ts.due = due
				}
			}
			// A feed with no talk watcher is advanced by the same probe.
			mark(protocol.TopicFeed + ":" + id)
		}
		if bits&pokeFeed != 0 {
			mark(protocol.TopicFeed + ":" + id)
		}
		if bits&pokeTimeline != 0 {
			mark(protocol.TopicTimeline + ":" + id)
		}
		if bits&pokeDiff != 0 {
			if ts := mark(protocol.TopicDiff + ":" + id); ts != nil {
				ts.cause = diffs[id]
			}
		}
	}
	for topic := range snaps {
		if ts := h.topics[topic]; ts != nil {
			ts.snapWanted = true
		}
	}
	for _, ts := range h.topics {
		if ts.inFlight {
			continue
		}
		full, snap := h.decideLocked(ts, now)
		if !full && !snap {
			continue
		}
		ts.inFlight, ts.lastStart, ts.ran = true, now, true
		ts.dirty, ts.snapWanted, ts.due = false, false, time.Time{}
		cause := ts.cause
		ts.cause = ""
		runs = append(runs, launch{ts: ts, snap: !full, cause: cause})
	}
	h.mu.Unlock()

	for _, r := range runs {
		h.runs.Add(1)
		go h.exec(ctx, r.ts, r.snap, r.cause)
	}
}

// decideLocked says whether a topic needs a full run (something may have
// changed) or only a snapshot. The caller holds h.mu.
func (h *watchHub) decideLocked(ts *topicState, now time.Time) (full, snap bool) {
	tm := ts.prod.timing()
	switch {
	case !ts.ran:
		full = true
	case ts.dirty && !now.Before(ts.dirtyAt.Add(tm.debounce)) && now.Sub(ts.lastStart) >= tm.minInterval:
		full = true
	case !ts.due.IsZero() && !now.Before(ts.due):
		full = true
	case tm.tick > 0 && now.Sub(ts.lastStart) >= tm.tick:
		full = true
	}
	if !full && ts.snapWanted && now.Sub(ts.lastStart) >= snapshotMinInterval {
		snap = true
	}
	return full, snap
}

// exec runs one producer run on a sem slot, bounded and panic-guarded, and
// hands its output to the topic's slots.
func (h *watchHub) exec(ctx context.Context, ts *topicState, snapOnly bool, cause string) {
	defer h.runs.Done()
	again := time.Duration(0)
	defer func() {
		if r := recover(); r != nil {
			h.d.logf("", "watch hub: panic in %s: %v", ts.topic, r)
		}
		h.mu.Lock()
		ts.inFlight = false
		if again > 0 {
			if due := h.now().Add(again); ts.due.IsZero() || due.Before(ts.due) {
				ts.due = due
			}
		}
		h.mu.Unlock()
		h.signal()
	}()
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
	case <-ctx.Done():
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ts.prod.timeout())
	defer cancel()

	if snapOnly && ts.prod.replace() {
		ts.mu.Lock()
		last := ts.last
		ts.mu.Unlock()
		if last != nil {
			h.deliver(ts, topicOut{})
			return
		}
		snapOnly = false
	}
	out := ts.prod.build(rctx, ts, snapOnly, h.wanting(ts), cause)
	again = out.again
	h.deliver(ts, out)
}

// topicWatched reports whether anyone watches topic.
func (h *watchHub) topicWatched(topic string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.topics[topic] != nil
}

// wanting reports whether any slot of ts waits for a snapshot.
func (h *watchHub) wanting(ts *topicState) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for box, s := range ts.subs {
		if box.WantsSnapshot(ts.topic, s.watchID) {
			return true
		}
	}
	return false
}

// deliver is the install step (rule 5): in one critical section offer the new
// delta or value to every live slot and install the snapshot into every slot
// that wants one. Nothing is computed while ts.mu is held.
func (h *watchHub) deliver(ts *topicState, out topicOut) {
	var tooBig bool
	ts.mu.Lock()
	if len(ts.subs) == 0 {
		ts.mu.Unlock()
		return
	}
	if out.end != nil {
		for box, s := range ts.subs {
			box.End(ts.topic, s.watchID, *out.end)
		}
		ts.mu.Unlock()
		return
	}
	if out.value != nil {
		if out.value.Size() > protocol.EventMaxBytes {
			// A replace value cannot be split: nobody gets it.
			tooBig = ts.bigFP != out.fp
			ts.bigFP = out.fp
			e := protocol.ErrPayload{Code: protocol.CodeFrameTooLarge, Message: "this view is too large to stream"}
			for box, s := range ts.subs {
				box.End(ts.topic, s.watchID, e)
			}
			ts.mu.Unlock()
			if tooBig {
				h.d.logf("", "watch hub: %s is too large to stream", ts.topic)
			}
			return
		}
		if ts.last == nil || ts.fp != out.fp {
			ts.last, ts.fp = out.value, out.fp
			for box, s := range ts.subs {
				box.Offer(ts.topic, s.watchID, out.value)
			}
		}
	}
	for _, ev := range out.deltas {
		for box, s := range ts.subs {
			box.Offer(ts.topic, s.watchID, ev)
		}
	}
	parts := out.parts
	if parts == nil && ts.last != nil {
		parts = []eventbox.Event{ts.last}
	}
	if parts != nil {
		for box, s := range ts.subs {
			if box.WantsSnapshot(ts.topic, s.watchID) {
				box.Install(ts.topic, s.watchID, parts)
			}
		}
	}
	ts.mu.Unlock()
}

// fingerprint is FNV-1a 64 over b.
func fingerprint(b []byte) uint64 {
	f := fnv.New64a()
	f.Write(b)
	return f.Sum64()
}
