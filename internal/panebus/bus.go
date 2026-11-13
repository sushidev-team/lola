package panebus

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/vtterm"
)

// bus is one pane: one tmux attach, one shadow emulator, N subscribers.
//
// Lock order is ALWAYS the pane's tapMu (taken inside vtterm.WithScreen) and
// then b.mu, never the other way round. Concretely: the tap handler already
// holds tapMu when it takes b.mu, and every path that needs a coherent screen
// goes through withScreen, which takes b.mu inside the callback. Nothing may
// hold b.mu while calling into the Pane, because that inverts the order.
//
// initMu is separate and sits above both: it serializes the one-shot attach, so
// two concurrent subscribers cannot spawn two tmux clients, and it is held
// across an exec, which b.mu never is. It also guards term, so a reader can
// take the pane without touching the hot lock.
type bus struct {
	reg  *Registry
	name string

	initMu   sync.Mutex
	term     Pane
	attached bool

	mu       sync.Mutex
	subs     map[*Sub]struct{}
	pending  []byte // output coalesced since the last flush
	seq      uint64
	size     WinSize
	closed   bool
	sizeWarn bool // the size probe failure has already been logged for this attach
	linger   *time.Timer

	// scrollMu serializes everything that moves the pane in or out of tmux copy
	// mode: the enter in Scroll, the scrolled flag, and the cancel in Write. It
	// is NOT mu, which guards the byte buffer on the reader's path, because it
	// is held across a tmux exec and mu never may be.
	scrollMu sync.Mutex
	scrolled bool

	done       chan struct{} // closed when the bus stops; every loop selects on it
	terminated chan struct{} // closed when the last goroutine and the pane are gone
	wg         sync.WaitGroup
}

func newBus(r *Registry, name string) *bus {
	return &bus{
		reg:        r,
		name:       name,
		subs:       map[*Sub]struct{}{},
		done:       make(chan struct{}),
		terminated: make(chan struct{}),
	}
}

// pane returns the attached shadow terminal, or nil before the attach.
func (b *bus) pane() Pane {
	b.initMu.Lock()
	defer b.initMu.Unlock()
	return b.term
}

// ready reports whether the attach has completed and the bus is still live.
func (b *bus) ready() bool {
	b.initMu.Lock()
	att := b.attached
	b.initMu.Unlock()
	if !att {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.closed
}

// ensureAttached measures the window and opens the single tmux attach, once. It
// runs outside b.mu so the execs cannot block the reader or a flush.
//
// A window whose size cannot be read REFUSES the attach rather than picking a
// default: an attach at the wrong size makes tmux redraw the pane for every
// client watching it, which is a visible corruption of somebody else's screen,
// and at this point there is no last-known-good size to fall back on.
func (b *bus) ensureAttached(ctx context.Context) error {
	b.initMu.Lock()
	defer b.initMu.Unlock()
	if b.attached {
		return nil
	}
	if b.reg.WinSize == nil || b.reg.Attach == nil {
		return fmt.Errorf("panebus: attach %q: no tmux seams installed", b.name)
	}

	sctx, scancel := context.WithTimeout(ctx, b.reg.execTimeout())
	ws, err := b.reg.WinSize(sctx, b.name)
	scancel()
	if err != nil {
		return fmt.Errorf("%w: %q: %w", ErrNoSize, b.name, err)
	}
	if !ws.Valid() {
		return fmt.Errorf("%w: %q: reported %dx%d", ErrNoSize, b.name, ws.Cols, ws.Rows)
	}
	cols, rows := ws.PTY()

	actx, acancel := context.WithTimeout(ctx, b.reg.execTimeout())
	term, err := b.reg.Attach(actx, b.name, cols, rows)
	acancel()
	if err != nil {
		return fmt.Errorf("panebus: attach %q: %w", b.name, err)
	}

	b.mu.Lock()
	b.size = ws
	b.mu.Unlock()
	b.term = term
	b.attached = true

	// The tap is installed before any goroutine of ours runs, so no byte the
	// emulator has modelled can escape the fan-out.
	term.Tap(b.onBytes)

	b.wg.Add(3)
	go b.guard("flush", b.flushLoop)
	go b.guard("size", b.sizeLoop)
	go b.guard("exit", b.exitWatch)

	b.reg.logf("panebus: attached %s at %dx%d (window %dx%d, status %d)",
		b.name, cols, rows, ws.Cols, ws.Rows, ws.StatusLines)
	if ws.Bigger {
		b.reg.logf("panebus: %s window is bigger than the attach terminal; the stream is a panned viewport", b.name)
	}
	return nil
}

// guard runs a bus goroutine behind a panic barrier, so a bug in one pane's
// loop can never take the daemon down with it, and accounts for it in b.wg so
// teardown can wait for it.
func (b *bus) guard(what string, fn func()) {
	defer b.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			b.reg.logf("panebus: %s %s loop panicked: %v", b.name, what, r)
		}
	}()
	fn()
}

// onBytes is the tap. It runs on the READER goroutine holding the pane's tapMu,
// so it does the least possible work: append to the coalescing buffer and
// return. It never allocates per subscriber, never blocks on one, and never
// calls back into the Pane.
func (b *bus) onBytes(p []byte) {
	b.mu.Lock()
	if !b.closed && len(b.subs) > 0 {
		b.pending = append(b.pending, p...)
	}
	b.mu.Unlock()
}

// flushLoop coalesces output onto one tick, exactly as desktop/termsvc.go's
// flushLoop does. A tick with nothing buffered produces NO frame at all, which
// is what makes an idle pane free.
func (b *bus) flushLoop() {
	t := time.NewTicker(b.reg.flushInterval())
	defer t.Stop()
	for {
		select {
		case <-b.done:
			return
		case <-t.C:
			b.mu.Lock()
			b.flushLocked()
			repair := b.anyDesyncedLocked()
			b.mu.Unlock()
			if repair {
				b.repair()
			}
		}
	}
}

// flushLocked hands whatever has been buffered to every subscriber as ONE
// frame, copied once and shared. Called with b.mu held, from the flush tick and
// from inside a withScreen callback — the latter is what makes a subscription
// coherent: registering a new subscriber flushes the pending bytes to the
// EXISTING ones first, so nothing already visible in the newcomer's resync is
// replayed to it afterwards.
func (b *bus) flushLocked() {
	if b.closed || len(b.pending) == 0 {
		return
	}
	data := b.pending
	b.pending = nil
	if len(b.subs) == 0 {
		return
	}
	b.seq++
	f := Frame{Kind: KindOutput, Data: data, Seq: b.seq}
	for s := range b.subs {
		b.enqueueLocked(s, f)
	}
}

// enqueueLocked offers a frame to one subscriber without ever blocking. A full
// buffer means this subscriber has fallen seconds behind; the frame is DROPPED
// and the subscriber marked desynced, which stops further output reaching it
// until a fresh resync repairs it.
//
// Dropping is not recoverable by replay — a byte range cannot be re-sent from
// halfway through an escape sequence — which is exactly why the flag exists
// rather than a hope that the queue drains in time.
func (b *bus) enqueueLocked(s *Sub, f Frame) {
	if s.isClosed() {
		return
	}
	if f.Kind == KindOutput && s.Desynced() {
		return
	}
	select {
	case s.ch <- f:
	default:
		s.markDesynced()
		b.reg.logf("panebus: %s subscriber fell behind; dropped a frame and queued a resync", b.name)
	}
}

func (b *bus) anyDesyncedLocked() bool {
	for s := range b.subs {
		if s.Desynced() {
			return true
		}
	}
	return false
}

// repair re-seeds every desynced subscriber with a fresh resync. It runs from
// the flush goroutine but OUTSIDE b.mu, because a coherent screen needs the
// pane's tapMu and the order is always tapMu then b.mu.
//
// A subscriber whose queue is still full stays desynced and is retried on the
// next tick; trying too early costs nothing and drops nothing.
func (b *bus) repair() {
	b.withScreen(func(scr Screen) {
		// The flush is not a tidy-up, it is the correctness of the repair, and
		// it is why subscribe and pushResync do it too. flushLoop drops b.mu
		// before calling this, and taking the pane's tapMu inside withScreen
		// waits out however long the reader spends parsing its current batch —
		// so bytes can land in b.pending in between, be RENDERED into the
		// screen below, and then still be delivered by the next flush to the
		// very subscriber this resync just repaired. Replaying a byte range is
		// not idempotent (a newline scrolls twice), so a recovery that skipped
		// this would hand a slow client a corrupted screen with no Seq gap to
		// tell it so.
		b.flushLocked()
		for s := range b.subs {
			if !s.Desynced() {
				continue
			}
			// The number is taken only on a frame that is actually sent. A
			// subscriber whose queue is STILL full gets nothing, and burning a
			// sequence number for it would fabricate a gap no drop caused.
			f := Frame{Kind: KindResync, Screen: &scr, Seq: b.seq + 1}
			select {
			case s.ch <- f:
				b.seq++
				s.clearDesynced()
			default:
			}
		}
	})
}

// withScreen takes one coherent reading of the pane and runs fn under b.mu with
// it. It is the ONLY way this file reads the screen, so the lock order can be
// stated in one place: pane tapMu, then b.mu. fn is not called at all once the
// bus is closed.
func (b *bus) withScreen(fn func(Screen)) {
	term := b.pane()
	if term == nil {
		return
	}
	term.WithScreen(func(scr vtterm.Screen) {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.closed {
			return
		}
		fn(scr)
	})
}

// subscribe registers a new stream. The registration and the resync it opens
// with happen inside ONE screen reading, which is the whole contract: the
// subscriber receives exactly the bytes that follow the screen it was handed.
func (b *bus) subscribe() (*Sub, error) {
	s := newSub(b)
	var registered bool
	b.withScreen(func(scr Screen) {
		// Existing subscribers get everything buffered so far BEFORE this one is
		// added, or the newcomer would be sent bytes its own resync already shows
		// and would render them twice.
		b.flushLocked()
		b.stopLingerLocked()
		b.seq++
		s.ch <- Frame{Kind: KindResync, Screen: &scr, Seq: b.seq}
		b.subs[s] = struct{}{}
		registered = true
	})
	if !registered {
		return nil, fmt.Errorf("%w: %q", ErrClosed, b.name)
	}
	return s, nil
}

// release drops a subscriber and, when it was the last, begins the linger: the
// attach outlives its subscribers briefly so a phone reconnecting after a
// network blip does not churn a tmux client. A zero Linger tears down
// synchronously, which is also what makes teardown deterministic in tests.
func (b *bus) release(s *Sub) {
	b.mu.Lock()
	if _, ok := b.subs[s]; !ok {
		b.mu.Unlock()
		return
	}
	delete(b.subs, s)
	if len(b.subs) > 0 || b.closed {
		b.mu.Unlock()
		return
	}
	b.pending = nil // nobody is watching; stop buffering for no one
	linger := b.reg.Linger
	if linger > 0 {
		b.stopLingerLocked()
		b.linger = time.AfterFunc(linger, b.lingerExpired)
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	b.lingerExpired()
}

func (b *bus) stopLingerLocked() {
	if b.linger != nil {
		b.linger.Stop()
		b.linger = nil
	}
}

// lingerExpired tears the pane down if it is still unwatched.
func (b *bus) lingerExpired() {
	b.mu.Lock()
	b.linger = nil
	idle := !b.closed && len(b.subs) == 0
	b.mu.Unlock()
	if !idle {
		return
	}
	b.shutdown("no subscribers", false)
	b.wait()
}

// sizeLoop re-measures the tmux window while anyone is watching.
//
// Polling is the ONLY mechanism available. panebus owns the PTY MASTER and the
// tmux client is the slave, so size flows master to slave: a window resized by
// the desktop produces no SIGWINCH here — measured, the attach PTY sat at its
// original size long after the window had moved.
func (b *bus) sizeLoop() {
	t := time.NewTicker(b.reg.sizeInterval())
	defer t.Stop()
	for {
		select {
		case <-b.done:
			return
		case <-t.C:
			b.pollSize()
		}
	}
}

// pollSize measures once, and only while somebody is watching. A probe that
// will not answer MUTATES NOTHING: the last known size stands, the failure is
// logged once per attach, and the next tick tries again. Resizing on a bad read
// would reflow the agent's TUI for every client watching it.
func (b *bus) pollSize() {
	b.mu.Lock()
	idle := len(b.subs) == 0 || b.closed
	b.mu.Unlock()
	if idle {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), b.reg.execTimeout())
	defer cancel()
	ws, err := b.reg.WinSize(ctx, b.name)
	if err != nil || !ws.Valid() {
		b.mu.Lock()
		warn := !b.sizeWarn
		b.sizeWarn = true
		b.mu.Unlock()
		if warn {
			b.reg.logf("panebus: %s window size unavailable, keeping the last known size: %v", b.name, err)
		}
		return
	}
	b.mu.Lock()
	b.sizeWarn = false
	b.mu.Unlock()
	b.applySize(ws)
}

// applySize propagates a changed window size to the PTY and the shadow
// emulator, then pushes a FRESH resync to every subscriber — without which the
// resync frame, the whole reason the emulator is here, would keep describing a
// grid that no longer exists.
func (b *bus) applySize(ws WinSize) {
	b.mu.Lock()
	prev := b.size
	same := prev.Cols == ws.Cols && prev.Rows == ws.Rows && prev.StatusLines == ws.StatusLines
	b.size = ws
	closed := b.closed
	b.mu.Unlock()
	term := b.pane()
	if closed || term == nil {
		return
	}
	if ws.Bigger && !prev.Bigger {
		b.reg.logf("panebus: %s window is bigger than the attach terminal; the stream is a panned viewport", b.name)
	}
	if same {
		return
	}
	cols, rows := ws.PTY()
	term.Resize(cols, rows)
	b.reg.logf("panebus: %s resized to %dx%d (window %dx%d, status %d)",
		b.name, cols, rows, ws.Cols, ws.Rows, ws.StatusLines)
	b.pushResync()
}

// pushResync sends one coherent screen to every subscriber. Anything buffered
// is flushed first, so the resync is unambiguously the newest state on the
// stream.
func (b *bus) pushResync() {
	b.withScreen(func(scr Screen) {
		b.flushLocked()
		b.seq++
		f := Frame{Kind: KindResync, Screen: &scr, Seq: b.seq}
		for s := range b.subs {
			select {
			case s.ch <- f:
				s.clearDesynced()
			default:
				// Full queue: this subscriber is behind. Mark it, and the repair
				// pass re-seeds it as soon as it has drained.
				s.markDesynced()
			}
		}
	})
}

// exitWatch turns the pane's death into a terminal frame. vtterm signals
// Frames() once more when the child exits, so this costs nothing beyond one
// wake per output batch while the pane is alive.
func (b *bus) exitWatch() {
	term := b.pane()
	if term == nil {
		return
	}
	for {
		select {
		case <-b.done:
			return
		case <-term.Frames():
			if term.Exited() {
				// Not awaited here: this goroutine is one of the ones teardown
				// waits for, so waiting on it from inside would deadlock.
				b.shutdown("pane exited", true)
				return
			}
		}
	}
}

// shutdown ends the bus exactly once and returns without blocking, so it is
// safe to call from a bus goroutine. wait blocks until the goroutines, the tmux
// child and the PTY are all gone.
//
// exited distinguishes the two endings a subscriber must be able to tell apart
// — the same distinction desktop/termsvc.go draws with its detached flag. A
// pane that DIED delivers a final KindExit frame and reports Sub.Exited; an
// unsubscribe or a registry shutdown simply closes the channel.
func (b *bus) shutdown(reason string, exited bool) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.stopLingerLocked()
	subs := make([]*Sub, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
	}
	b.subs = map[*Sub]struct{}{}
	pending := b.pending
	b.pending = nil
	b.seq++
	seq := b.seq
	b.mu.Unlock()

	close(b.done)
	term := b.pane()
	if term != nil {
		term.Tap(nil)
	}

	// Started BEFORE the fan-out below, and unconditionally. b.terminated is
	// what wait() — and therefore Registry.Close, and therefore the daemon's
	// stopRemote — blocks on, so nothing between here and the end of this
	// function may be allowed to strand it. Starting it after the loop made a
	// panic in any subscriber's teardown hang shutdown forever AND leak the
	// tmux child, its PTY and the bus's slot in the registry with it; with the
	// goroutine already running, the worst such a panic can now cost is one
	// subscriber's final frame.
	go func() {
		defer close(b.terminated)
		b.wg.Wait()
		if term != nil {
			_ = term.Close()
		}
		b.reg.drop(b.name, b)
		b.reg.logf("panebus: %s torn down (%s)", b.name, reason)
	}()

	for _, s := range subs {
		if len(pending) > 0 {
			s.offer(Frame{Kind: KindOutput, Data: pending, Seq: seq})
		}
		if exited {
			s.setExited()
			s.offer(Frame{Kind: KindExit, Seq: seq + 1})
		}
		s.close()
	}
}

// wait blocks until a shutdown has fully completed. Calling it before any
// shutdown would block forever, so every caller pairs it with one.
func (b *bus) wait() { <-b.terminated }
