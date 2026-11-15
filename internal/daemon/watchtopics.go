package daemon

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/eventbox"
	"github.com/sushidev-team/lola/internal/protocol"
)

// The remaining topics: projects and status (replace), timeline (snapshot then
// deltas) and diff (a signal-only counter).
//
// The timeline producer reads ONLY d.handleSessionTimeline, the redacted
// reader. It must not reach the raw store: TestTimelineProducerUsesOnlyPublicTimeline
// walks this file's AST and fails on any such call.

const (
	statusMinInterval = 2 * time.Second
	statusTick        = 30 * time.Second
	statusRunTimeout  = 5 * time.Second
	timelineDebounce  = 200 * time.Millisecond
	diffDebounce      = time.Second
	diffMinInterval   = 3 * time.Second
	timelineRetry     = 5 * time.Second
)

// ---- projects, status ----

type projectsProducer struct{ h *watchHub }

func (*projectsProducer) timing() timing {
	return timing{debounce: 100 * time.Millisecond, minInterval: statusMinInterval, tick: statusTick}
}
func (*projectsProducer) timeout() time.Duration { return statusRunTimeout }
func (*projectsProducer) replace() bool          { return true }
func (p *projectsProducer) build(ctx context.Context, _ *topicState, _, _ bool, _ string) topicOut {
	data := p.h.d.projectsData(ctx)
	b, _ := json.Marshal(data)
	return topicOut{value: protocol.SizeOf(&protocol.ProjectsEvent{ProjectsData: data}), fp: fingerprint(b)}
}

type statusProducer struct{ h *watchHub }

func (*statusProducer) timing() timing {
	return timing{debounce: 100 * time.Millisecond, minInterval: statusMinInterval, tick: statusTick}
}
func (*statusProducer) timeout() time.Duration { return statusRunTimeout }
func (*statusProducer) replace() bool          { return true }
func (p *statusProducer) build(ctx context.Context, _ *topicState, _, _ bool, _ string) topicOut {
	data := p.h.d.statusData(ctx)
	b, _ := json.Marshal(data)
	return topicOut{value: protocol.SizeOf(&protocol.StatusEvent{StatusData: data}), fp: fingerprint(b)}
}

// ---- diff ----

type diffProducer struct {
	h  *watchHub
	id string
}

func (*diffProducer) timing() timing {
	return timing{debounce: diffDebounce, minInterval: diffMinInterval}
}
func (*diffProducer) timeout() time.Duration { return statusRunTimeout }
func (*diffProducer) replace() bool          { return true }

// build moves the counter. The first value exists from the first watch
// ({rev: current or 1}); a later run is a "maybe changed" tick and bumps it.
func (p *diffProducer) build(_ context.Context, ts *topicState, _, _ bool, cause string) topicOut {
	ts.mu.Lock()
	has := ts.last != nil
	ts.mu.Unlock()
	h := p.h
	h.mu.Lock()
	rev := h.revs[p.id]
	if rev == 0 {
		rev = 1
	}
	if has {
		rev++
	}
	h.revs[p.id] = rev
	h.mu.Unlock()
	if !has {
		cause = ""
	}
	return topicOut{value: protocol.SizeOf(&protocol.DiffEvent{Rev: rev, Cause: cause}), fp: uint64(rev)}
}

// ---- timeline ----

type timelinePub struct {
	mu      sync.Mutex // L1
	has     bool
	lastSeq int64 // the cursor deltas continue from
	connFP  uint64
	snap    []eventbox.Event
	snapSeq int64
	snapOK  bool
	logged  map[string]bool
	failed  bool
}

type timelineProducer struct {
	h  *watchHub
	id string
	p  timelinePub
}

func (*timelineProducer) timing() timing {
	return timing{debounce: timelineDebounce, minInterval: timelineDebounce}
}
func (*timelineProducer) timeout() time.Duration { return statusRunTimeout }
func (*timelineProducer) replace() bool          { return false }

func (tp *timelineProducer) read(after int64, limit int) (protocol.SSHTimelineData, error) {
	return tp.h.timelineRead(protocol.SessionTimelineArgs{Session: tp.id, After: after, Limit: limit})
}

func connsFP(c []protocol.SSHConnInfo) uint64 {
	b, _ := json.Marshal(c)
	return fingerprint(b)
}

func (tp *timelineProducer) build(_ context.Context, _ *topicState, _, want bool, _ string) topicOut {
	p := &tp.p
	p.mu.Lock()
	defer p.mu.Unlock()
	var out topicOut
	fail := func(err error) topicOut {
		if !p.failed {
			p.failed = true
			tp.h.d.logf("", "watch hub: timeline of %s unreadable: %v", tp.id, err)
		}
		return topicOut{again: timelineRetry}
	}
	p.failed = false

	if p.has {
		after := p.lastSeq
		for {
			data, err := tp.read(after, protocol.TimelineDeltaItems)
			if err != nil {
				return fail(err)
			}
			items, omitted := tp.fitItems(data.Items)
			ev := &protocol.TimelineEvent{Session: tp.id, LastSeq: data.LastSeq, Items: items, Omitted: omitted}
			if fp := connsFP(data.Connections); fp != p.connFP {
				p.connFP = fp
				ev.Connections = data.Connections
			}
			if len(items) > 0 || len(omitted) > 0 || ev.Connections != nil {
				out.deltas = append(out.deltas, tp.splitDelta(ev)...)
			}
			moved := data.LastSeq > after
			if moved {
				after = data.LastSeq
			}
			if !data.More || !moved {
				break
			}
		}
		p.lastSeq = after
	}
	if !p.has || (want && !(p.snapOK && p.snapSeq == p.lastSeq)) {
		parts, last, fp, err := tp.snapshot()
		if err != nil {
			return fail(err)
		}
		if !p.has {
			p.has, p.lastSeq, p.connFP = true, last, fp
		}
		p.snap, p.snapSeq, p.snapOK = parts, p.lastSeq, true
	}
	if want {
		out.parts = p.snap
	}
	return out
}

// snapshot reads the whole redacted timeline from cursor 0 and cuts it into
// reset parts. Over TimelineSnapshotMaxBytes only the newest items by Order
// that fit are kept and the first part says Older.
func (tp *timelineProducer) snapshot() (parts []eventbox.Event, last int64, connFP uint64, err error) {
	var all []protocol.SSHTimelineItem
	var conns []protocol.SSHConnInfo
	after := int64(0)
	for {
		data, e := tp.read(after, protocol.TimelineSnapshotItems)
		if e != nil {
			return nil, 0, 0, e
		}
		all = append(all, data.Items...)
		conns, last = data.Connections, data.LastSeq
		moved := data.LastSeq > after
		after = data.LastSeq
		if !data.More || !moved {
			break
		}
	}
	items, omitted := tp.fitItems(all)
	total := 0
	sizes := make([]int, len(items))
	for i, it := range items {
		b, _ := json.Marshal(it)
		sizes[i] = len(b) + 1
		total += sizes[i]
	}
	older := false
	if total > protocol.TimelineSnapshotMaxBytes {
		older = true
		byOrder := make([]int, len(items))
		for i := range byOrder {
			byOrder[i] = i
		}
		sort.Slice(byOrder, func(a, b int) bool { return items[byOrder[a]].Order > items[byOrder[b]].Order })
		keep := map[int]bool{}
		used := 0
		for _, i := range byOrder {
			if used+sizes[i] > protocol.TimelineSnapshotMaxBytes {
				break
			}
			used += sizes[i]
			keep[i] = true
		}
		kept := make([]protocol.SSHTimelineItem, 0, len(keep))
		ksz := make([]int, 0, len(keep))
		for i, it := range items {
			if keep[i] {
				kept = append(kept, it)
				ksz = append(ksz, sizes[i])
			}
		}
		items, sizes = kept, ksz
	}

	budget := protocol.EventMaxBytes - 8192
	cur := &protocol.TimelineEvent{Reset: true, Older: older, Session: tp.id, LastSeq: last, Connections: conns, Omitted: omitted, Items: []protocol.SSHTimelineItem{}}
	var evs []*protocol.TimelineEvent
	size := 0
	for i, it := range items {
		if size+sizes[i] > budget && len(cur.Items) > 0 {
			evs = append(evs, cur)
			cur = &protocol.TimelineEvent{Session: tp.id, LastSeq: last, Items: []protocol.SSHTimelineItem{}}
			size = 0
		}
		cur.Items = append(cur.Items, it)
		size += sizes[i]
	}
	evs = append(evs, cur)
	for i, e := range evs {
		e.More = i < len(evs)-1
		parts = append(parts, protocol.SizeOf(e))
	}
	return parts, last, connsFP(conns), nil
}

// fitItems moves items over EventItemMaxBytes to omitted (logged once each).
func (tp *timelineProducer) fitItems(items []protocol.SSHTimelineItem) ([]protocol.SSHTimelineItem, []protocol.OmittedItem) {
	kept := make([]protocol.SSHTimelineItem, 0, len(items))
	var omitted []protocol.OmittedItem
	for _, it := range items {
		b, _ := json.Marshal(it)
		if len(b) > protocol.EventItemMaxBytes {
			omitted = append(omitted, protocol.OmittedItem{ID: it.ID, Seq: it.Seq})
			if tp.p.logged == nil {
				tp.p.logged = map[string]bool{}
			}
			if !tp.p.logged[it.ID] {
				tp.p.logged[it.ID] = true
				tp.h.d.logf("", "watch hub: timeline item of %s over the event item bound; omitted", tp.id)
			}
			continue
		}
		kept = append(kept, it)
	}
	return kept, omitted
}

// splitDelta cuts a delta into events of at most EventMaxBytes.
func (tp *timelineProducer) splitDelta(ev *protocol.TimelineEvent) []eventbox.Event {
	if protocol.SizeOf(ev).Size() <= protocol.EventMaxBytes || len(ev.Items) < 2 {
		return []eventbox.Event{ev}
	}
	half := len(ev.Items) / 2
	a := &protocol.TimelineEvent{Session: ev.Session, LastSeq: ev.LastSeq, Items: ev.Items[:half], Connections: ev.Connections, Omitted: ev.Omitted}
	b := &protocol.TimelineEvent{Session: ev.Session, LastSeq: ev.LastSeq, Items: ev.Items[half:]}
	return append(tp.splitDelta(a), tp.splitDelta(b)...)
}
