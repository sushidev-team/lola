package panebus

import "sync"

// Sub is one subscriber's view of a pane: a buffered stream of Frames that
// always opens with a resync.
//
// The channel is a QUEUE, not a ring. A ring of raw bytes hands a waking client
// a stream that begins mid-escape-sequence, which is precisely the failure the
// resync frame exists to remove — so an overrun DROPS the frame and marks the
// subscription desynced, and the bus repairs it with a fresh resync once the
// consumer has caught up. A slow consumer therefore costs itself a repaint and
// costs its peers and the reader goroutine nothing at all.
//
// The consumer owns exactly one obligation: drain Frames() until it closes, or
// call Close. Frame.Data is shared with every other subscriber of the pane and
// must never be written into.
type Sub struct {
	bus  *bus
	ch   chan Frame
	name string

	mu       sync.Mutex
	closed   bool
	desynced bool
	exited   bool

	closeOnce sync.Once
}

func newSub(b *bus) *Sub {
	return &Sub{bus: b, name: b.name, ch: make(chan Frame, subBuffer)}
}

// Pane is the tmux session name this subscription streams.
func (s *Sub) Pane() string { return s.name }

// Frames is the stream. It is CLOSED when the subscription ends, for any of
// three reasons: Close was called, the registry shut down, or the pane's child
// exited. Exited tells the last of those apart from the other two, and a pane
// that died also delivers a final KindExit frame before the close.
func (s *Sub) Frames() <-chan Frame { return s.ch }

// Desynced reports whether output had to be dropped for this subscriber. It
// clears itself when the bus delivers the repairing resync, so a consumer never
// has to act on it; it is exported because it is the one honest measure of
// whether a client is keeping up.
func (s *Sub) Desynced() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.desynced
}

// Exited reports whether the stream ended because the pane's child ended, as
// opposed to an unsubscribe or a daemon shutdown. Read it after Frames() has
// closed; a caller that only ever consumes frames can use KindExit instead.
func (s *Sub) Exited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited
}

// Close ends the subscription. It is idempotent, and it is what releases the
// pane's reference count: the last Close tears the tmux attach down, after the
// registry's linger. Frames() is closed by the time it returns.
func (s *Sub) Close() error {
	s.bus.release(s)
	s.close()
	return nil
}

func (s *Sub) markDesynced() {
	s.mu.Lock()
	s.desynced = true
	s.mu.Unlock()
}

func (s *Sub) clearDesynced() {
	s.mu.Lock()
	s.desynced = false
	s.mu.Unlock()
}

func (s *Sub) setExited() {
	s.mu.Lock()
	s.exited = true
	s.mu.Unlock()
}

func (s *Sub) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// offer enqueues a final frame during teardown, evicting one buffered frame if
// it has to. Eviction is acceptable HERE and nowhere else: the subscription is
// ending, so a gap costs a repaint that will never be drawn, whereas losing the
// exit frame would leave a client unable to tell a dead pane from a detached
// one.
//
// s.mu is held across the CHECK AND THE SENDS, and that is the whole safety of
// this function rather than a widened critical section. Every other send in the
// package happens under bus.mu with the Sub still in bus.subs, which is what
// makes close() unreachable meanwhile; offer is the one send that does not,
// because bus.shutdown empties bus.subs before it calls this. With the mutex
// released in between, a concurrent Sub.Close could close the channel in the
// gap and the send would panic — and that panic is worse than a crash, because
// bus.guard recovers it and the teardown it interrupted never runs. All three
// sends are non-blocking, so holding the mutex across them cannot deadlock, and
// close() sets s.closed under this same mutex before it closes the channel.
func (s *Sub) offer(f Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- f:
		return
	default:
	}
	select {
	case <-s.ch:
	default:
	}
	select {
	case s.ch <- f:
	default:
	}
}

// close shuts the stream down once.
//
// Two separate invariants make a send on a closed channel impossible, and both
// are needed because there are two kinds of sender. On the ordinary path it is
// MEMBERSHIP: a Sub is only ever enqueued to while it is in bus.subs, and it is
// always REMOVED from bus.subs under bus.mu before anything closes it. On the
// teardown path bus.shutdown has already emptied bus.subs, so membership says
// nothing and the FLAG carries it instead — which is why closed is set under
// s.mu here, before the channel closes, and why offer holds s.mu across its
// sends rather than only across the check.
func (s *Sub) close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.ch)
	})
}
