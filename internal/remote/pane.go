package remote

import (
	"context"
	"sync"

	"github.com/sushidev-team/lola/internal/protocol"
)

// The pane path: subscribe, unsubscribe, and the three PTY actions.
//
// All of it runs on ONE ordered worker per connection, and that ordering is the
// point. Keystrokes must reach the agent in the order they were typed, a write
// may have to cancel copy mode first (which execs tmux), and a pty frame that
// overtook its own subscribe would be refused for a pane the client believes it
// is attached to. Serializing them with each other costs nothing a human can
// perceive; serializing them with a five-minute request would cost the feature,
// which is why requests run somewhere else entirely.

// maxPaneName bounds a pane name this package will queue, echo or log. It is
// this package's OWN bound on a wire field rather than an import of
// panebus.MaxPaneNameLen: remote deliberately does not depend on the pane
// layer's concrete types (see panebus.go), and the two bounds answer different
// questions — panebus caps what may reach a tmux argv, this caps what may reach
// a frame and a log line. Names lola builds are nowhere near either.
const maxPaneName = 128

// paneOp is a decoded pane frame, owned by the worker. Everything in it was
// decoded on the reader goroutine, so nothing here aliases the frame buffer.
type paneOp struct {
	kind string // protocol.FrameSub | FrameUnsub | FramePTY
	id   string
	pane string
	sub  protocol.SubPayload
	in   protocol.PTYInputPayload
}

// subscription is one attached pane on one connection.
type subscription struct {
	pane   string
	id     string // the sub frame's correlation id; echoed on every resync
	stream PaneStream

	// seq is touched only by this subscription's pump goroutine.
	seq uint64

	// cols and rows are the client's last stated viewport. They are advisory —
	// the bus attaches at the tmux WINDOW size and the client pans — and are
	// kept so a resize action has somewhere honest to land.
	//
	// They are owned by the PANE WORKER goroutine and by nothing else: the
	// subscription is built there and PTYActionResize writes them there, so
	// today they need no synchronization. Anything that starts READING them
	// from another goroutine — the pump, a status command — makes that untrue
	// and must take c.mu first.
	cols, rows int

	once sync.Once
}

func (s *subscription) close() {
	s.once.Do(func() {
		if s.stream != nil {
			_ = s.stream.Close()
		}
	})
}

// enqueuePane decodes a pane frame on the reader goroutine and hands it to the
// ordered worker. A full queue closes the connection: it means the bus is
// wedged, and silently dropping keystrokes into a live agent is the worst
// outcome available.
func (c *conn) enqueuePane(f *protocol.Frame) error {
	op := paneOp{kind: f.Type, id: f.ID, pane: f.Pane}
	switch f.Type {
	case protocol.FrameSub:
		if err := f.DecodePayload(&op.sub); err != nil {
			c.refusePane(f.ID, f.Pane, protocol.CodeInternal, "malformed subscribe payload")
			return nil
		}
	case protocol.FramePTY:
		if err := f.DecodePayload(&op.in); err != nil {
			c.refusePane(f.ID, f.Pane, protocol.CodeInternal, "malformed pty payload")
			return nil
		}
	}
	if op.pane == "" {
		c.refusePane(f.ID, "", protocol.CodeUnknownPane, "no pane named")
		return nil
	}
	if len(op.pane) > maxPaneName {
		// Bounded HERE, before the name is queued, logged or handed to the pane
		// layer. Frame.Pane is otherwise capped only by the frame size — a
		// megabyte — and every one of those three destinations is a place an
		// authenticated peer could then drive a megabyte per frame into. The
		// refusal reuses CodeUnknownPane rather than inventing a length code,
		// because a refusal that distinguishes "too long" from "not yours"
		// starts answering questions about which panes exist.
		c.refusePane(f.ID, "", protocol.CodeUnknownPane, "pane is not available")
		return nil
	}
	select {
	case c.queue <- op:
		return nil
	case <-c.done:
		return errRefused
	default:
		c.refusePane(f.ID, op.pane, protocol.CodeRateLimited, "pane input queue is full")
		c.srv.opts.logf()("remote: device=%s pane queue full; closing", logSafe(c.peer.DeviceID))
		return errRefused
	}
}

// paneLoop drains the ordered queue.
func (c *conn) paneLoop() {
	defer c.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			c.srv.opts.logf()("remote: pane worker panic: %v", r)
			c.shutdown()
		}
	}()
	for {
		select {
		case <-c.done:
			return
		case op := <-c.queue:
			c.handlePaneOp(op)
		}
	}
}

func (c *conn) handlePaneOp(op paneOp) {
	switch op.kind {
	case protocol.FrameSub:
		c.subscribe(op)
	case protocol.FrameUnsub:
		if s := c.dropSub(op.pane); s != nil {
			s.close()
			c.srv.opts.logf()("remote: device=%s unsubscribed pane=%s", logSafe(c.peer.DeviceID), logSafe(op.pane))
		}
	case protocol.FramePTY:
		c.paneInput(op)
	}
}

// subscribe attaches, or RE-attaches. A second sub for a pane already attached
// replaces the first rather than being refused, because a re-subscribe is the
// client's defined recovery from a Seq gap: the contract says a gap means
// re-sub rather than render corruption, so refusing one would leave a client
// that noticed a gap with nothing to do about it.
func (c *conn) subscribe(op paneOp) {
	if c.srv.opts.Panes == nil {
		c.refusePane(op.id, op.pane, protocol.CodeUnknownPane, "this daemon serves no panes")
		return
	}
	if old := c.dropSub(op.pane); old != nil {
		old.close()
	}

	ctx, cancel := context.WithTimeout(c.ctx, paneOpTimeout)
	stream, err := c.srv.opts.Panes.Subscribe(ctx, op.pane, op.sub.Cols, op.sub.Rows)
	cancel()
	if err != nil {
		// Fail closed and say nothing about WHY beyond the code: an
		// unresolvable name and a name the device may not see must look
		// identical, or the refusal enumerates panes.
		c.refusePane(op.id, op.pane, protocol.CodeUnknownPane, "pane is not available")
		c.srv.opts.logf()("remote: device=%s subscribe pane=%s refused: %v",
			logSafe(c.peer.DeviceID), logSafe(op.pane), err)
		return
	}

	s := &subscription{pane: op.pane, id: op.id, stream: stream, cols: op.sub.Cols, rows: op.sub.Rows}
	c.mu.Lock()
	if c.isDone() {
		c.mu.Unlock()
		s.close()
		return
	}
	c.subs[op.pane] = s
	c.mu.Unlock()

	c.srv.opts.logf()("remote: device=%s subscribed pane=%s", logSafe(c.peer.DeviceID), logSafe(op.pane))
	c.wg.Add(1)
	go c.pump(s)
}

// paneInput applies one PTY action to an attached pane.
func (c *conn) paneInput(op paneOp) {
	s := c.getSub(op.pane)
	if s == nil {
		// A write racing an unsubscribe, or a pane this connection never
		// attached. Never forwarded: a pty write is authorized by the
		// SUBSCRIPTION, so acting on one without it would bypass the check.
		c.refusePane(op.id, op.pane, protocol.CodeUnknownPane, "not subscribed to this pane")
		return
	}
	switch op.in.Action {
	case protocol.PTYActionWrite:
		if len(op.in.Data) == 0 {
			return
		}
		if err := c.srv.opts.Panes.Write(op.pane, op.in.Data); err != nil {
			c.refusePane(op.id, op.pane, protocol.CodeInternal, "write failed")
			c.srv.opts.logf()("remote: device=%s write pane=%s failed: %v",
				logSafe(c.peer.DeviceID), logSafe(op.pane), err)
		}
	case protocol.PTYActionScroll:
		ctx, cancel := context.WithTimeout(c.ctx, paneOpTimeout)
		err := c.srv.opts.Panes.Scroll(ctx, op.pane, op.in.Lines)
		cancel()
		if err != nil {
			c.refusePane(op.id, op.pane, protocol.CodeInternal, "scroll failed")
			c.srv.opts.logf()("remote: device=%s scroll pane=%s failed: %v",
				logSafe(c.peer.DeviceID), logSafe(op.pane), err)
		}
	case protocol.PTYActionResize:
		// Recorded and ignored, which is what the contract promises. The bus
		// attaches at the tmux window size so the phone cannot shrink the
		// developer's window; honouring a resize here is an M3 decision that
		// needs to know whether any other client is attached.
		s.cols, s.rows = op.in.Cols, op.in.Rows
	default:
		// Never approximated: guessing here would type bytes into a live agent.
		c.refusePane(op.id, op.pane, protocol.CodeUnknownType, "unknown pty action")
	}
}

// pump turns one subscription's frames into wire frames. It is the only writer
// of a subscription's Seq.
func (c *conn) pump(s *subscription) {
	defer c.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			c.srv.opts.logf()("remote: pane pump panic: %v", r)
			c.shutdown()
		}
	}()
	frames := s.stream.Frames()
	for {
		select {
		case <-c.done:
			return
		case pf, ok := <-frames:
			if !ok {
				return
			}
			// A pane DEATH leaves c.subs BEFORE it is announced, and the order
			// is the invariant rather than tidiness. c.subs is what authorizes
			// a pty frame, and a client is entitled to react to the exit frame
			// the instant it arrives — so with the write first, the reply's
			// keystrokes found the subscription still registered and were
			// forwarded to a pane that no longer exists. The peer then saw
			// "write failed" where the protocol promises unknown_pane, and once
			// the bus had also dropped the pane, a new tmux session reusing the
			// name (session ids are reused, e.g. across revive) could take the
			// keystrokes for real.
			exit := pf.Kind == PaneExit
			var dropped *subscription
			if exit {
				dropped = c.dropSubIf(s.pane, s)
			}
			err := c.sendPaneFrame(s, pf)
			// Closed only after the frame is on the wire: the stream is what
			// produced the frame, and tearing it down first would be racing the
			// write for no reason.
			if dropped != nil {
				dropped.close()
			}
			if err != nil {
				c.failWrite(err)
				return
			}
			if exit {
				return
			}
		}
	}
}

// sendPaneFrame encodes one PaneFrame.
//
// The resync carries the sub frame's correlation id — it IS the
// acknowledgement — while a pty frame carries none, because a stream of output
// is nobody's reply.
func (c *conn) sendPaneFrame(s *subscription, pf PaneFrame) error {
	// The bus's own number wins whenever it has one. It counts per PANE across
	// every frame the bus produced, INCLUDING the ones it dropped for a slow
	// subscriber, which is the only numbering from which a client can tell that
	// it missed something. A connection-local counter would renumber the stream
	// and make every drop invisible. Zero means the bus does not number, and
	// then the connection counts — safe only because such a bus never drops.
	seq := pf.Seq
	busNumbered := seq != 0
	if !busNumbered {
		seq = s.seq + 1
	}

	out := protocol.Frame{V: protocol.FrameVersionCurrent, Pane: s.pane, Seq: seq}
	switch pf.Kind {
	case PaneOutput:
		if len(pf.Data) == 0 && !busNumbered {
			// An empty flush is not a gap. Skipping a NUMBERED one would
			// fabricate a gap, so that case is forwarded instead.
			return nil
		}
		out.Type = protocol.FramePTY
		if err := out.SetPayload(protocol.PTYOutputPayload{Data: pf.Data}); err != nil {
			return err
		}
	case PaneResync, PaneExit:
		out.Type = protocol.FrameResync
		out.ID = s.id
		if err := out.SetPayload(resyncPayload(pf)); err != nil {
			return err
		}
	default:
		// An unknown kind is dropped rather than guessed at.
		c.srv.opts.logf()("remote: pane=%s produced an unknown frame kind %d", logSafe(s.pane), pf.Kind)
		return nil
	}
	if err := c.fw.WriteFrame(&out); err != nil {
		return err
	}
	s.seq = seq
	return nil
}

// resyncPayload renders a PaneFrame's screen for the wire. A PaneExit with no
// screen still produces a payload, because Exited is the field that tells a
// client a death from a detach.
func resyncPayload(pf PaneFrame) protocol.ResyncPayload {
	p := protocol.ResyncPayload{Exited: pf.Kind == PaneExit}
	if sc := pf.Screen; sc != nil {
		p.Cols, p.Rows = sc.Cols, sc.Rows
		p.Lines = sc.Lines
		p.CursorX, p.CursorY = sc.CursorX, sc.CursorY
		p.CursorHidden = !sc.CursorVisible
		p.AltScreen = sc.AltScreen
	}
	return p
}

func (c *conn) getSub(pane string) *subscription {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.subs[pane]
}

// dropSub removes whatever subscription is registered for pane, whoever
// created it. That is what an explicit unsub and a re-subscribe both want:
// the client named the pane, not a particular attachment to it.
func (c *conn) dropSub(pane string) *subscription {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.subs[pane]
	delete(c.subs, pane)
	return s
}

// dropSubIf removes s only while it is STILL the subscription registered for
// its pane, and is the form every background goroutine must use — the same
// identity check panebus.Registry.drop makes, for the same reason. A pump
// noticing its pane died can be arbitrarily far behind a client that has
// already re-subscribed, and an unconditional delete there would unregister
// (and then close) the replacement instead of the corpse.
func (c *conn) dropSubIf(pane string, s *subscription) *subscription {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subs[pane] != s {
		return nil
	}
	delete(c.subs, pane)
	return s
}
