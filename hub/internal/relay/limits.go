package relay

import (
	"container/list"
	"net/netip"
	"sync"
	"time"

	"github.com/sushidev-team/lola/hubproto"
)

// maxKeyedBuckets bounds each per-key dial limiter: a flood of distinct keys
// evicts the least recently used one instead of growing the map.
const maxKeyedBuckets = 10000

// limits holds every tunable the hub reads. The zero value is not valid; use
// defaultLimits. Tests shorten fields on the Hub they own.
type limits struct {
	idle       time.Duration
	halfClose  time.Duration
	writeStall time.Duration

	spliceRate, spliceBurst float64 // bytes/s, bytes (per splice, per direction)
	deviceRate, orgRate     float64 // bytes/s (per direction); burst is one second

	dailyDevice, dailyRV int64

	perRV, perDevice, perOrg, total int
}

func defaultLimits() limits {
	return limits{
		idle:        hubproto.RelayIdleTimeout,
		halfClose:   hubproto.RelayHalfCloseGrace,
		writeStall:  hubproto.RelayWriteStall,
		spliceRate:  hubproto.RelaySpliceBytesPerSec,
		spliceBurst: hubproto.RelaySpliceBurst,
		deviceRate:  hubproto.RelayDeviceBytesPerSec,
		orgRate:     hubproto.RelayOrgBytesPerSec,
		dailyDevice: hubproto.RelayDailyBytesPerDevice,
		dailyRV:     hubproto.RelayDailyBytesPerRV,
		perRV:       hubproto.RelayMaxConnsPerRV,
		perDevice:   hubproto.RelayMaxConnsPerDevice,
		perOrg:      hubproto.RelayMaxConnsPerOrg,
		total:       hubproto.RelayMaxConnsHub,
	}
}

// bucket is a token bucket that admits whole events.
type bucket struct {
	mu     sync.Mutex
	rate   float64 // tokens per second
	burst  float64
	tokens float64
	last   time.Time
}

func newBucket(rate, burst float64, now time.Time) *bucket {
	return &bucket{rate: rate, burst: burst, tokens: burst, last: now}
}

func (b *bucket) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if dt := now.Sub(b.last); dt > 0 {
		b.tokens += dt.Seconds() * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// refund gives back one token taken by allow, never above the burst.
func (b *bucket) refund() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tokens = min(b.tokens+1, b.burst)
}

// keyedBuckets is a bounded LRU of buckets.
type keyedBuckets struct {
	mu          sync.Mutex
	rate, burst float64
	max         int
	ll          *list.List // front = most recent; values are *keyedEntry
	m           map[string]*list.Element
}

type keyedEntry struct {
	key string
	b   *bucket
}

func newKeyedBuckets(rate, burst float64, max int) *keyedBuckets {
	return &keyedBuckets{rate: rate, burst: burst, max: max, ll: list.New(), m: map[string]*list.Element{}}
}

func (k *keyedBuckets) allow(key string, now time.Time) bool {
	k.mu.Lock()
	el, ok := k.m[key]
	if ok {
		k.ll.MoveToFront(el)
	} else {
		el = k.ll.PushFront(&keyedEntry{key: key, b: newBucket(k.rate, k.burst, now)})
		k.m[key] = el
		for k.ll.Len() > k.max {
			old := k.ll.Back()
			k.ll.Remove(old)
			delete(k.m, old.Value.(*keyedEntry).key)
		}
	}
	b := el.Value.(*keyedEntry).b
	k.mu.Unlock()
	return b.allow(now)
}

// refund gives back one token to key's bucket, if it is still held.
func (k *keyedBuckets) refund(key string) {
	k.mu.Lock()
	el, ok := k.m[key]
	k.mu.Unlock()
	if ok {
		el.Value.(*keyedEntry).b.refund()
	}
}

// network maps a client address to its rate-limit key: /64 for IPv6, /32 for
// IPv4 (a v4-mapped address counts as v4). A caller may hand over any prefix
// length; only its address matters.
func network(p netip.Prefix) (string, bool) {
	a := p.Addr().Unmap()
	if !a.IsValid() {
		return "", false
	}
	bits := 64
	if a.Is4() {
		bits = 32
	}
	return netip.PrefixFrom(a, bits).Masked().String(), true
}

// byteBucket meters bytes as a debt that drains at rate. Shaping waits while
// the debt exceeds burst; it never refuses. Guarded by Hub.acct.
type byteBucket struct {
	rate, burst float64
	debt        float64
	last        time.Time
}

func newByteBucket(rate, burst float64, now time.Time) *byteBucket {
	return &byteBucket{rate: rate, burst: burst, last: now}
}

func (b *byteBucket) drain(now time.Time) {
	if dt := now.Sub(b.last); dt > 0 {
		b.debt -= dt.Seconds() * b.rate
		if b.debt < 0 {
			b.debt = 0
		}
		b.last = now
	}
}

func (b *byteBucket) charge(n int, now time.Time) {
	b.drain(now)
	b.debt += float64(n)
}

// wait is how long until the debt is back within burst; 0 when it already is.
func (b *byteBucket) wait(now time.Time) time.Duration {
	b.drain(now)
	if b.debt <= b.burst || b.rate <= 0 {
		return 0
	}
	return time.Duration((b.debt - b.burst) / b.rate * float64(time.Second))
}

// dayCount counts wire bytes for one UTC day; a new day starts from zero.
type dayCount struct {
	day int64
	n   int64
}

func utcDay(t time.Time) int64 { return t.UTC().Unix() / 86400 }

func (d *dayCount) add(now time.Time, n int64) int64 {
	if day := utcDay(now); d.day != day {
		d.day, d.n = day, 0
	}
	d.n += n
	return d.n
}

func (d *dayCount) get(now time.Time) int64 {
	if d == nil || d.day != utcDay(now) {
		return 0
	}
	return d.n
}
