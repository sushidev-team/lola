package remote

import (
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// Per-device limits (spec 01 §7). Buckets are keyed by device id, so a direct
// and a relayed connection of one phone share them.
const (
	readRate  = 20.0
	readBurst = 60.0

	writeRate  = 1.0
	writeBurst = 10.0

	extRate  = 0.2
	extBurst = 3.0

	attachBytesRate  = 2 << 20
	attachBytesBurst = 20 << 20

	ptyBytesRate  = 32 << 10
	ptyBytesBurst = 256 << 10

	subRate  = 5.0
	subBurst = 20.0

	// scrollRate bounds pty scroll frames: each one execs tmux twice and may
	// put a shared pane into copy mode for every attached client.
	scrollRate  = 10.0
	scrollBurst = 30.0

	// credRate bounds relayCred, a read-tier command whose answer may mint a
	// bearer: a disk write of devices.json, a log line and a rendezvous sync
	// with the hub. A phone sends it twice per authenticated session.
	credRate  = 1.0 / 30
	credBurst = 6.0

	// logRate bounds the lines one device's refusals can write to the daemon
	// log, which is append-only and unrotated: a refusal that costs no strike
	// (rate limited, too many in flight) or a reported err frame would
	// otherwise be one line per frame at line rate.
	logRate  = 0.2
	logBurst = 10.0

	// limiterIdle is how long a device with no live connection keeps its buckets.
	limiterIdle = 10 * time.Minute
)

// extCommands are the reachable commands whose handler calls gh or the Linear
// API on the request path. They pay extRate IN ADDITION to the tier bucket, so
// one device cannot drain the GitHub quota the observer and reviews share.
var extCommands = map[string]bool{
	"prs":               true,
	"tickets":           true,
	"prReadiness":       true,
	"mergePR":           true,
	"addressPRFeedback": true,
	"openPr":            true,
	"openTicket":        true,
}

// bucket is a token bucket. Callers hold limiter.mu.
type bucket struct {
	rate, burst float64
	tokens      float64
	last        time.Time
}

func newBucket(rate, burst float64, now time.Time) *bucket {
	return &bucket{rate: rate, burst: burst, tokens: burst, last: now}
}

// refill tops the bucket up to now. A clock that moved backwards adds nothing.
func (b *bucket) refill(now time.Time) {
	if d := now.Sub(b.last); d > 0 {
		b.tokens += d.Seconds() * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
	}
	b.last = now
}

// need is the charge for n tokens: never more than the burst, so a single
// oversized request is judged against a full bucket instead of failing forever.
func (b *bucket) need(n float64) float64 {
	if n > b.burst {
		return b.burst
	}
	return n
}

func (b *bucket) can(n float64) bool { return b.tokens >= b.need(n) }
func (b *bucket) take(n float64)     { b.tokens -= b.need(n) }

type deviceLimits struct {
	read, write, ext, attach, pty, sub, scroll, cred, log *bucket

	// logSuppressed counts the lines the log bucket refused since the last
	// one it let through.
	logSuppressed int

	used time.Time
}

type limiter struct {
	mu   sync.Mutex
	now  func() time.Time
	devs map[string]*deviceLimits
}

func newLimiter(now func() time.Time) *limiter {
	if now == nil {
		now = time.Now
	}
	return &limiter{now: now, devs: map[string]*deviceLimits{}}
}

// get returns the device's buckets refilled to now, creating them on first
// use. Callers hold l.mu.
func (l *limiter) get(dev string, now time.Time) *deviceLimits {
	d := l.devs[dev]
	if d == nil {
		d = &deviceLimits{
			read:   newBucket(readRate, readBurst, now),
			write:  newBucket(writeRate, writeBurst, now),
			ext:    newBucket(extRate, extBurst, now),
			attach: newBucket(attachBytesRate, attachBytesBurst, now),
			pty:    newBucket(ptyBytesRate, ptyBytesBurst, now),
			sub:    newBucket(subRate, subBurst, now),
			scroll: newBucket(scrollRate, scrollBurst, now),
			cred:   newBucket(credRate, credBurst, now),
			log:    newBucket(logRate, logBurst, now),
		}
		l.devs[dev] = d
	}
	for _, b := range []*bucket{d.read, d.write, d.ext, d.attach, d.pty, d.sub, d.scroll, d.cred, d.log} {
		b.refill(now)
	}
	d.used = now
	return d
}

// allowReq charges one req frame. The tier bucket is read for tier read and
// write for every other tier (an unknown tier is a write: fail closed). Ext
// commands also pay extRate, and relayCred pays credRate. Every part of an
// attachImage pays its raw bytes; only the final part pays the write bucket,
// so a large photo uploads at link speed and costs one composer write. A
// non-final part pays one read token instead: the exemption covers bytes, not
// request count, so a stream of tiny parts is bounded like any read. Nothing
// is charged when any bucket refuses.
func (l *limiter) allowReq(dev, tier, cmd string, attachBytes int, attachLast bool) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := l.get(dev, l.now())

	tierB := d.write
	switch {
	case tier == protocol.TierRead:
		tierB = d.read
	case cmd == protocol.CmdAttachImage && !attachLast:
		tierB = d.read
	}
	cred := cmd == protocol.CmdRelayCred
	if !tierB.can(1) {
		return false
	}
	if extCommands[cmd] && !d.ext.can(1) {
		return false
	}
	if cred && !d.cred.can(1) {
		return false
	}
	if attachBytes > 0 && !d.attach.can(float64(attachBytes)) {
		return false
	}
	tierB.take(1)
	if extCommands[cmd] {
		d.ext.take(1)
	}
	if cred {
		d.cred.take(1)
	}
	if attachBytes > 0 {
		d.attach.take(float64(attachBytes))
	}
	return true
}

// allowScroll charges one pty scroll frame.
func (l *limiter) allowScroll(dev string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := l.get(dev, l.now())
	if !d.scroll.can(1) {
		return false
	}
	d.scroll.take(1)
	return true
}

// allowLog reports whether one of the device's throttled lines may be written
// now and, when it may, how many were suppressed since the last one.
func (l *limiter) allowLog(dev string) (ok bool, suppressed int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := l.get(dev, l.now())
	if !d.log.can(1) {
		d.logSuppressed++
		return false, 0
	}
	d.log.take(1)
	suppressed, d.logSuppressed = d.logSuppressed, 0
	return true, suppressed
}

// allowPTY charges n payload bytes of a pty write.
func (l *limiter) allowPTY(dev string, n int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := l.get(dev, l.now())
	if !d.pty.can(float64(n)) {
		return false
	}
	d.pty.take(float64(n))
	return true
}

// allowSub charges one sub frame.
func (l *limiter) allowSub(dev string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := l.get(dev, l.now())
	if !d.sub.can(1) {
		return false
	}
	d.sub.take(1)
	return true
}

// forget drops a device's buckets (DropDevice).
func (l *limiter) forget(dev string) {
	l.mu.Lock()
	delete(l.devs, dev)
	l.mu.Unlock()
}

// sweep deletes the buckets of devices with no live connection that have been
// idle for limiterIdle.
func (l *limiter) sweep(live func(dev string) bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for id, d := range l.devs {
		if now.Sub(d.used) >= limiterIdle && (live == nil || !live(id)) {
			delete(l.devs, id)
		}
	}
}
