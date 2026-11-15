package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/eventbox"
	"github.com/sushidev-team/lola/internal/protocol"
)

// The `feed:<id>` topic: a snapshot (reset) of the newest page, then keyed
// deltas read through Feed.Page's change-log cursor. The feed is a CLAIM of the
// agent's log and drives display only; the one thing read from it is whether
// an edit or run item appeared, which pokes the display-only diff tick.

const (
	feedScanProbe            = time.Second
	feedScanProgressInterval = time.Second
	feedPublishGap           = 250 * time.Millisecond
)

// feedView is the publisher's read access to one session's reader. Everything
// it calls runs with the reader locked; Close releases it.
type feedView struct {
	Page     func(ctx context.Context, a protocol.AgentFeedArgs) protocol.AgentFeedData
	LastSeq  func() int64
	Epoch    func() string
	Scanning func() bool
	Health   func() string
	Close    func()
}

// feedAvail says what openFeed found.
type feedAvail uint8

const (
	feedGone        feedAvail = iota // no such session
	feedUnsupported                  // the agent has no feed reader
	feedReady
)

// openFeed is the default watchHub.openFeed seam: find the session's reader and
// lock it. The publisher holds feedEntry.mu, never while it holds topicState.mu.
func (d *Daemon) openFeedView(_ context.Context, id string) (feedView, feedAvail) {
	t, ok, err := d.feedResolve(id)
	if err != nil || !ok {
		return feedView{}, feedGone
	}
	if !agent.Caps(t.kind).Feed {
		return feedView{}, feedUnsupported
	}
	e := d.feedHub().entry(d, t)
	e.mu.Lock()
	return feedView{
		Page:     e.feed.Page,
		LastSeq:  e.feed.LastSeq,
		Epoch:    e.feed.Epoch,
		Scanning: e.feed.Scanning,
		Health: func() string {
			if h, _ := e.health.Load().(string); h != "" {
				return h
			}
			return protocol.FeedOK
		},
		Close: e.mu.Unlock,
	}, feedReady
}

// advanceFeedFor moves a session's reader a little (the feed-only probe).
func (d *Daemon) advanceFeedFor(ctx context.Context, id string) {
	if s, ok := d.sessions.Get(id); ok {
		d.advanceFeed(ctx, s, talkFeedBudget)
	}
}

type feedPub struct {
	mu           sync.Mutex // L1
	has          bool
	epoch        string
	lastSeq      int64 // the cursor deltas continue from
	scanning     bool
	lastProgress time.Time
	snap         *protocol.FeedEvent // cached reset at snapKey
	snapKey      feedKey
	logged       map[string]bool // oversize items already logged
}

type feedKey struct {
	epoch    string
	lastSeq  int64
	scanning bool
}

type feedProducer struct {
	h  *watchHub
	id string
	p  feedPub
}

func (*feedProducer) timing() timing         { return timing{minInterval: feedPublishGap} }
func (*feedProducer) timeout() time.Duration { return talkProbeTimeout }
func (*feedProducer) replace() bool          { return false }

func (fp *feedProducer) build(ctx context.Context, _ *topicState, _, want bool, _ string) topicOut {
	h, id, p := fp.h, fp.id, &fp.p
	// With no talk watcher nothing else moves the reader, so this run does.
	if !h.topicWatched(protocol.TopicTalk + ":" + id) {
		h.advanceFeed(ctx, id)
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	view, avail := h.openFeed(ctx, id)
	switch avail {
	case feedGone:
		return topicOut{}
	case feedUnsupported:
		if p.has && !want {
			return topicOut{}
		}
		p.has = true
		ev := protocol.SizeOf(&protocol.FeedEvent{
			Reset: true, Session: id, Health: protocol.FeedUnsupported, Items: []protocol.AgentFeedItem{},
		})
		return topicOut{deltas: nil, parts: []eventbox.Event{ev}}
	}
	defer view.Close()

	lastSeq, epoch, scanning := view.LastSeq(), view.Epoch(), view.Scanning()
	health := view.Health()
	now := h.now()
	var out topicOut
	if scanning {
		out.again = feedScanProbe
	}

	reset := func() *protocol.FeedEvent {
		key := feedKey{epoch, lastSeq, scanning}
		if p.snap != nil && p.snapKey == key {
			return p.snap
		}
		pg := view.Page(ctx, protocol.AgentFeedArgs{Session: id})
		ev := fp.fitReset(&protocol.FeedEvent{
			Reset: true, Older: pg.More, Session: id, Epoch: pg.Epoch, LastSeq: pg.LastSeq,
			Scanning: pg.Scanning, Health: health, Items: pg.Items,
		})
		p.snap, p.snapKey = ev, key
		return ev
	}

	switch {
	case !p.has || epoch != p.epoch || (scanning != p.scanning):
		// First sight, a new reader (restart, eviction, agent switch, segment
		// rotation), the start of a scan and its end are all resets.
		ev := reset()
		out.deltas = []eventbox.Event{ev}
		p.has, p.epoch, p.scanning, p.lastSeq = true, epoch, scanning, resetCursor(ev, epoch, lastSeq)
		p.lastProgress = now
	case scanning:
		if lastSeq != p.lastSeq && now.Sub(p.lastProgress) >= feedScanProgressInterval {
			out.deltas = []eventbox.Event{protocol.SizeOf(&protocol.FeedEvent{
				Session: id, Epoch: epoch, LastSeq: lastSeq, Scanning: true, Health: health,
				Items: []protocol.AgentFeedItem{},
			})}
			p.lastSeq, p.lastProgress = lastSeq, now
		}
	case lastSeq != p.lastSeq:
		if p.lastSeq == 0 {
			ev := reset()
			out.deltas = []eventbox.Event{ev}
			p.lastSeq = resetCursor(ev, epoch, lastSeq)
			break
		}
		edited := false
		after := p.lastSeq
		for after < lastSeq {
			pg := view.Page(ctx, protocol.AgentFeedArgs{Session: id, Epoch: epoch, After: after, Limit: protocol.FeedDeltaItems})
			if pg.LastSeq <= after {
				break
			}
			for _, it := range pg.Items {
				if it.Tool != nil && (it.Tool.Category == "edit" || it.Tool.Category == "run") {
					edited = true
				}
			}
			for _, ev := range fp.splitDelta(&protocol.FeedEvent{
				Session: id, Epoch: epoch, LastSeq: pg.LastSeq, Health: health, Items: pg.Items,
			}) {
				out.deltas = append(out.deltas, ev)
			}
			after = pg.LastSeq
		}
		p.lastSeq = after
		if edited {
			// PokeDiff takes only a leaf lock, so it is safe under the reader lock.
			defer h.PokeDiff(id, protocol.DiffCauseEdit)
		}
	}
	if want {
		out.parts = []eventbox.Event{reset()}
	}
	return out
}

// resetCursor is where deltas continue after a published reset: the snapshot's
// own LastSeq when the reader advanced between LastSeq() and Page (those items
// are in the snapshot, so a delta from the older cursor would repeat them),
// otherwise the cursor read before the page.
func resetCursor(ev *protocol.FeedEvent, epoch string, lastSeq int64) int64 {
	if ev.Epoch == epoch && ev.LastSeq > lastSeq {
		return ev.LastSeq
	}
	return lastSeq
}

// fitItems drops items over EventItemMaxBytes into omitted (logged once each).
func (fp *feedProducer) fitItems(items []protocol.AgentFeedItem) ([]protocol.AgentFeedItem, []protocol.OmittedItem) {
	kept := make([]protocol.AgentFeedItem, 0, len(items))
	var omitted []protocol.OmittedItem
	for _, it := range items {
		b, _ := json.Marshal(it)
		if len(b) > protocol.EventItemMaxBytes {
			omitted = append(omitted, protocol.OmittedItem{ID: it.ID, Seq: it.Seq})
			if fp.p.logged == nil {
				fp.p.logged = map[string]bool{}
			}
			if !fp.p.logged[it.ID] {
				fp.p.logged[it.ID] = true
				fp.h.d.logf("", "watch hub: feed item of %s over the event item bound; omitted", fp.id)
			}
			continue
		}
		kept = append(kept, it)
	}
	return kept, omitted
}

// fitReset keeps the newest items that fit one event; the rest is Older.
func (fp *feedProducer) fitReset(ev *protocol.FeedEvent) *protocol.FeedEvent {
	items, omitted := fp.fitItems(ev.Items)
	ev.Items, ev.Omitted = items, omitted
	for protocol.SizeOf(ev).Size() > protocol.EventMaxBytes && len(ev.Items) > 1 {
		ev.Items = ev.Items[1:]
		ev.Older = true
	}
	return ev
}

// splitDelta cuts a delta page into events of at most EventMaxBytes.
func (fp *feedProducer) splitDelta(ev *protocol.FeedEvent) []eventbox.Event {
	items, omitted := fp.fitItems(ev.Items)
	ev.Items, ev.Omitted = items, omitted
	if protocol.SizeOf(ev).Size() <= protocol.EventMaxBytes || len(ev.Items) < 2 {
		return []eventbox.Event{ev}
	}
	half := len(ev.Items) / 2
	a := &protocol.FeedEvent{Session: ev.Session, Epoch: ev.Epoch, LastSeq: ev.LastSeq, Health: ev.Health, Items: ev.Items[:half], Omitted: ev.Omitted}
	b := &protocol.FeedEvent{Session: ev.Session, Epoch: ev.Epoch, LastSeq: ev.LastSeq, Health: ev.Health, Items: ev.Items[half:]}
	return append(fp.splitDelta(a), fp.splitDelta(b)...)
}
