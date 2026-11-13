package agentfeed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/sushidev-team/lola/internal/protocol"
)

// Retention and reply bounds.
const (
	// MaxItems is what a Feed keeps in memory. Older items are reached by a rescan.
	MaxItems = 5000
	// DefaultLimit is what a page holds when a client names no limit.
	DefaultLimit = 300
	maxLimit     = 1000
	// maxReplyBytes keeps one reply a reasonable line on the daemon socket: 300 items at
	// the text cap would otherwise be several megabytes.
	maxReplyBytes = 768 << 10
)

// Feed holds what a Source has said and serves it to clients: it assigns each item a
// fixed Order, moves Seq on every change, and pages. It is not safe for concurrent use;
// the caller holds one lock per session around every method.
type Feed struct {
	mk      func() Source
	src     Source
	epoch   string
	gen     int64
	limit   int
	items   []Item
	index   map[string]int64
	seq     int64
	next    int64
	oldHash map[string]string
}

// NewFeed starts a Feed. mk must build a fresh Source each time it is called: older
// history is reached by reading the log again with a second one.
func NewFeed(mk func() Source) *Feed {
	f := newFeedOf(mk(), MaxItems)
	f.mk = mk
	return f
}

func newFeedOf(src Source, limit int) *Feed {
	return &Feed{src: src, limit: limit, epoch: newEpoch(), gen: src.Generation(), index: map[string]int64{}}
}

func newEpoch() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Epoch names this read of the log. A client seeing a different one discards and refetches.
func (f *Feed) Epoch() string { return f.epoch }

// LastSeq is the newest change.
func (f *Feed) LastSeq() int64 { return f.seq }

// Scanning is true until the log has been read to its end.
func (f *Feed) Scanning() bool { return !f.src.AtEOF() }

// Counts is what the Source has seen since it last started over.
func (f *Feed) Counts() Counts { return f.src.Counts() }

// Generation moves when the read started over.
func (f *Feed) Generation() int64 { return f.gen }

// TextHash is Source.TextHash, plus what a rescan of older history learned.
func (f *Feed) TextHash(id string) string {
	if h := f.src.TextHash(id); h != "" {
		return h
	}
	return f.oldHash[id]
}

// Advance moves the Source and files what it reports. It answers the items that changed,
// as filed, so a caller can mark them with Update.
func (f *Feed) Advance(ctx context.Context, budget int64) ([]Item, Health) {
	changed, h := f.src.Advance(ctx, budget)
	if g := f.src.Generation(); g != f.gen {
		f.gen = g
		f.epoch = newEpoch()
		f.items, f.index, f.next, f.oldHash = nil, map[string]int64{}, 0, nil
	}
	f.apply(changed)
	out := make([]Item, 0, len(changed))
	for _, c := range changed {
		if it := f.lookup(c.ID); it != nil {
			out = append(out, *it)
		}
	}
	return out, h
}

// Item is a copy of one held item.
func (f *Feed) Item(id string) (Item, bool) {
	if it := f.lookup(id); it != nil {
		return *it, true
	}
	return Item{}, false
}

func (f *Feed) lookup(id string) *Item {
	ord, ok := f.index[id]
	if !ok || len(f.items) == 0 {
		return nil
	}
	return &f.items[ord-f.items[0].Order]
}

func (f *Feed) apply(changed []Item) {
	for _, c := range changed {
		f.seq++
		c.Seq = f.seq
		if cur := f.lookup(c.ID); cur != nil {
			// What the caller marked on a user item is not the Source's to overwrite.
			c.Order, c.Echo = cur.Order, cur.Echo
			if c.Origin == "" {
				c.Origin = cur.Origin
			}
			*cur = c
			continue
		}
		c.Order = f.next
		f.next++
		f.index[c.ID] = c.Order
		f.items = append(f.items, c)
	}
	if f.limit > 0 && len(f.items) > f.limit {
		drop := len(f.items) - f.limit
		for _, it := range f.items[:drop] {
			delete(f.index, it.ID)
		}
		// Copy rather than reslice, or the dropped text stays pinned by the array.
		f.items = append([]Item(nil), f.items[drop:]...)
	}
}

// Update changes one item and moves its Seq. It reports whether the item is held.
func (f *Feed) Update(id string, change func(*Item)) bool {
	it := f.lookup(id)
	if it == nil {
		return false
	}
	change(it)
	f.seq++
	it.Seq = f.seq
	return true
}

// All is every held item, in Order.
func (f *Feed) All() []Item { return append([]Item(nil), f.items...) }

// Page answers an agentFeed request. Marking older items needs the caller's knowledge of
// sends, so a rescan's page comes back as is, with TextHash able to answer for it.
func (f *Feed) Page(ctx context.Context, a protocol.AgentFeedArgs) protocol.AgentFeedData {
	limit := a.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, maxLimit)
	data := protocol.AgentFeedData{Session: a.Session, Epoch: f.epoch, LastSeq: f.seq, Scanning: f.Scanning()}
	stale := a.Epoch != "" && a.Epoch != f.epoch
	switch {
	case stale || (a.After == 0 && a.Before == 0):
		data.Items, data.More = f.newest(f.items, limit)
		if len(f.items) > 0 && f.items[0].Order > 0 {
			data.More = true
		}
	case a.Before != 0:
		data.Items, data.More = f.older(ctx, a.Before, limit)
		// This page says nothing about changes, and a client that adopted the newest
		// sequence from it would skip the ones it has not fetched. Zero makes its next
		// fetch the tail, which is always safe.
		data.LastSeq = 0
	default:
		data.Items, data.LastSeq = f.since(a.After, limit)
	}
	if data.Items == nil {
		data.Items = []Item{}
	}
	return data
}

// newest takes the last items that fit, in Order, and says whether any were left out.
func (f *Feed) newest(items []Item, limit int) ([]Item, bool) {
	start, size := len(items), 0
	for start > 0 && len(items)-start < limit {
		size += itemSize(&items[start-1])
		if size > maxReplyBytes && start < len(items) {
			break
		}
		start--
	}
	return append([]Item(nil), items[start:]...), start > 0
}

// since takes what changed after a sequence, oldest change first. When it does not all
// fit, the sequence it answers is the last one included, so the next fetch resumes there.
func (f *Feed) since(after int64, limit int) ([]Item, int64) {
	var picked []Item
	for i := range f.items {
		if f.items[i].Seq > after {
			picked = append(picked, f.items[i])
		}
	}
	sortBySeq(picked)
	last, size := f.seq, 0
	for i := range picked {
		size += itemSize(&picked[i])
		if i >= limit || (size > maxReplyBytes && i > 0) {
			last = picked[i-1].Seq
			picked = picked[:i]
			break
		}
	}
	return picked, last
}

func sortBySeq(items []Item) {
	// Insertion sort: items are in Order, and nearly all are already in Seq order too.
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].Seq < items[j-1].Seq; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func itemSize(it *Item) int {
	b, err := json.Marshal(it)
	if err != nil {
		return 0
	}
	return len(b)
}

// older pages history before an Order. What memory holds is answered from memory; past
// that the log is read again, which reassigns the same Orders because it is the same file
// read the same way.
func (f *Feed) older(ctx context.Context, before int64, limit int) ([]Item, bool) {
	if len(f.items) > 0 && before > f.items[0].Order {
		first := f.items[0].Order
		end := min(before-first, int64(len(f.items)))
		page, more := f.newest(f.items[:end], limit)
		return page, more || first > 0
	}
	if len(f.items) > 0 && f.items[0].Order == 0 {
		return nil, false // nothing is older than the first item
	}
	if f.mk == nil {
		return nil, false
	}
	full := newFeedOf(f.mk(), 0)
	for ctx.Err() == nil {
		changed, _ := full.src.Advance(ctx, 8<<20)
		full.apply(changed)
		if full.src.AtEOF() {
			break
		}
	}
	end := min(before, int64(len(full.items)))
	if end <= 0 {
		return nil, false
	}
	page, more := f.newest(full.items[:end], limit)
	if f.oldHash == nil {
		f.oldHash = map[string]string{}
	}
	for _, it := range page {
		if h := full.src.TextHash(it.ID); h != "" {
			f.oldHash[it.ID] = h
		}
	}
	return page, more
}

// Output reads one tool's output back from the log. An item old enough to have lost its
// location is found by reading the log again.
func (f *Feed) Output(ctx context.Context, id string) (string, bool, error) {
	text, cut, err := f.src.Output(ctx, id)
	if !errors.Is(err, ErrNoOutput) || f.mk == nil {
		return text, cut, err
	}
	full := f.mk()
	if c, ok := full.(interface{ retainRefs() }); ok {
		c.retainRefs()
	}
	for ctx.Err() == nil {
		full.Advance(ctx, 8<<20)
		if full.AtEOF() {
			break
		}
	}
	return full.Output(ctx, id)
}

// Thread reads one subagent's conversation.
func (f *Feed) Thread(ctx context.Context, parent string, limit int) ([]Item, bool, error) {
	items, err := f.src.Thread(ctx, parent)
	if err != nil {
		return nil, false, err
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	page, more := f.newest(items, min(limit, maxLimit))
	return page, more, nil
}
