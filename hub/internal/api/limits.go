package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/sushidev-team/lola/hubproto"
)

// bucket is a token bucket. Callers serialize access.
type bucket struct {
	tokens float64
	last   time.Time
}

func (b *bucket) take(now time.Time, rate float64, burst int) bool {
	if !b.last.IsZero() {
		if dt := now.Sub(b.last).Seconds(); dt > 0 {
			b.tokens += dt * rate
		}
	}
	if b.tokens > float64(burst) {
		b.tokens = float64(burst)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// limiter keeps one bucket per device.
type limiter struct {
	mu      sync.Mutex
	rate    float64
	burst   int
	buckets map[string]*bucket
}

func newLimiter(rate float64, burst int) *limiter {
	return &limiter{rate: rate, burst: burst, buckets: map[string]*bucket{}}
}

func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil {
		// Devices are enrolled, so this map is bounded by the device count.
		b = &bucket{tokens: float64(l.burst)}
		l.buckets[key] = b
	}
	return b.take(now, l.rate, l.burst)
}

// replayCache remembers (device, nonce) pairs for replayWindow. It is only
// consulted AFTER a signature verified AND the device's rate limit admitted
// the request, so an attacker cannot fill it without a device key, and a
// device cannot insert faster than its bucket allows.
//
// Two bounds keep one device from taking the shared cache: perDev caps the
// live nonces of any one device, max caps the whole cache. Every entry lives
// exactly replayWindow, so insertion order is expiry order: a FIFO queue
// prunes expired entries in amortized O(1) instead of sweeping the map.
type replayCache struct {
	mu     sync.Mutex
	max    int
	perDev int
	seen   map[string]time.Time // device|nonce -> forget after
	count  map[string]int       // device -> live entries
	queue  []replayEntry        // insertion (= expiry) order
	head   int
}

type replayEntry struct {
	dev, key string
	until    time.Time
}

func newReplayCache(max, perDev int) *replayCache {
	return &replayCache{max: max, perDev: perDev, seen: map[string]time.Time{}, count: map[string]int{}}
}

// prune drops expired entries from the front of the queue.
func (c *replayCache) prune(now time.Time) {
	for c.head < len(c.queue) && !now.Before(c.queue[c.head].until) {
		e := c.queue[c.head]
		c.queue[c.head] = replayEntry{}
		c.head++
		// A key can be re-inserted after it expired (a clock that moved
		// backwards); only the entry that owns the map slot removes it.
		if until, ok := c.seen[e.key]; ok && until.Equal(e.until) {
			delete(c.seen, e.key)
			if c.count[e.dev]--; c.count[e.dev] <= 0 {
				delete(c.count, e.dev)
			}
		}
	}
	// Compact once the consumed prefix dominates.
	if c.head > 1024 && c.head*2 > len(c.queue) {
		c.queue = append([]replayEntry(nil), c.queue[c.head:]...)
		c.head = 0
	}
}

func (c *replayCache) check(dev, nonce string, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := dev + "|" + nonce
	if until, ok := c.seen[key]; ok && now.Before(until) {
		return fail(http.StatusUnauthorized, hubproto.CodeReplay)
	}
	c.prune(now)
	if _, ok := c.seen[key]; ok {
		// Expired but still owned by an out-of-order queue entry: forget it.
		delete(c.seen, key)
		if c.count[dev]--; c.count[dev] <= 0 {
			delete(c.count, dev)
		}
	}
	if len(c.seen) >= c.max || (c.perDev > 0 && c.count[dev] >= c.perDev) {
		// Full of live nonces: refuse rather than forget one early.
		return fail(http.StatusTooManyRequests, hubproto.CodeRateLimited)
	}
	until := now.Add(replayWindow)
	c.seen[key] = until
	c.count[dev]++
	c.queue = append(c.queue, replayEntry{dev: dev, key: key, until: until})
	return nil
}
