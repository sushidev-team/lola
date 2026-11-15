package remote

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/sushidev-team/lola/internal/eventbox"
	"github.com/sushidev-team/lola/internal/protocol"
)

// Events is the push seam: the daemon's watch hub, behind Options.Events. A nil
// Events answers every watched topic with protocol.WatchCodeUnavailable.
type Events interface {
	// Watch registers box for topic on behalf of peer. It builds nothing and
	// performs no I/O; the snapshot follows from a producer run. stop is
	// idempotent and removes only this watch id's registration.
	Watch(ctx context.Context, peer Peer, topic, watchID string, box *eventbox.Box) (stop func(), res protocol.WatchResult)
	// Resnapshot is the box's onSnap: a non-blocking poke.
	Resnapshot(topic string)
}

// maxWatchingConns bounds the connections that may hold an event box at once.
const maxWatchingConns = 8

// Fixed sentences. None is built from peer input.
const (
	errBadWatch     = "bad watch request"
	msgTooManyConns = "too many watching connections"
	msgTooManyTopic = "too many watched topics on this connection"
	msgTooLarge     = "this view is too large to stream"
	msgNotPermitted = "not permitted for this device"
	msgNotStreamed  = "this view could not be streamed"
)

// pushProbe is the frame the pump hands the authorizer before every outbound
// event: the check is the one a watch request itself passes, so a revoked or
// re-paired device, or one that lost the read tier, stops receiving at once.
var pushProbe = protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReq, Cmd: protocol.CmdWatch}

type watchEntry struct {
	watchID string
	stop    func()
}

// connEvents is one connection's push state: its box, its watches and the
// guard that keeps the pump's logging bounded. mu is the connection's evMu.
type connEvents struct {
	mu      sync.Mutex
	box     *eventbox.Box // nil until a watch took a box slot
	watches map[string]watchEntry
	closed  bool
	logged  map[string]bool // topics whose oversize frame was already logged
}

// The event state is kept beside the connection, not on it: conn.go belongs to
// the transport, and everything push-related stays in this file. evState maps a
// connection to its state (present only from its first watch until stopEvents)
// and counts, per Server, the connections holding a box slot.
var evState = struct {
	mu    sync.Mutex
	conns map[*conn]*connEvents
	boxes map[*Server]int
}{conns: map[*conn]*connEvents{}, boxes: map[*Server]int{}}

// eventsFor returns the connection's push state, creating it when create is set
// and the connection is still live. A connection that has begun shutting down
// never gets one, so stopEvents cannot be raced into a leak.
func (c *conn) eventsFor(create bool) *connEvents {
	evState.mu.Lock()
	defer evState.mu.Unlock()
	ce := evState.conns[c]
	if ce == nil && create && !c.isDone() {
		ce = &connEvents{watches: map[string]watchEntry{}, logged: map[string]bool{}}
		evState.conns[c] = ce
	}
	return ce
}

// takeBoxSlot reserves one of the Server's maxWatchingConns box slots.
func (c *conn) takeBoxSlot() bool {
	evState.mu.Lock()
	defer evState.mu.Unlock()
	if c.isDone() || evState.boxes[c.srv] >= maxWatchingConns {
		return false
	}
	evState.boxes[c.srv]++
	return true
}

func (c *conn) releaseBoxSlot() {
	evState.mu.Lock()
	defer evState.mu.Unlock()
	if evState.boxes[c.srv]--; evState.boxes[c.srv] <= 0 {
		delete(evState.boxes, c.srv)
	}
}

// validWatchID reports whether id matches ^[A-Za-z0-9_-]{1,64}$.
func validWatchID(id string) bool {
	if id == "" || len(id) > protocol.MaxWatchIDBytes {
		return false
	}
	for i := 0; i < len(id); i++ {
		b := id[i]
		switch {
		case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9', b == '_', b == '-':
		default:
			return false
		}
	}
	return true
}

// dispatchEvents answers watch and unwatch on the reader goroutine and reports
// whether it consumed the request. Registration only: no I/O besides the
// response. Every other command returns false and goes on to Options.Handle.
func (c *conn) dispatchEvents(id string, req protocol.Request) bool {
	switch req.Cmd {
	case protocol.CmdWatch:
		c.respondWatch(id, c.watch(id, req))
	case protocol.CmdUnwatch:
		c.respondWatch(id, c.unwatch(req))
	default:
		return false
	}
	return true
}

func (c *conn) respondWatch(id string, resp protocol.Response) {
	out := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameResp, ID: id}
	if err := out.SetPayload(resp); err != nil {
		c.refuse(id, protocol.CodeInternal, "response could not be encoded")
		return
	}
	if err := c.fw.WriteFrame(&out); err != nil {
		c.failWrite(err)
	}
}

func watchReply(res []protocol.WatchResult) protocol.Response {
	b, err := json.Marshal(protocol.WatchData{Topics: res})
	if err != nil {
		return protocol.Response{Error: errBadWatch}
	}
	return protocol.Response{OK: true, Data: b}
}

func badWatch() protocol.Response { return protocol.Response{Error: errBadWatch} }

// watch registers every topic of one request under the request's frame id.
// Re-watching a topic stops the old stream first (old stop, box.Stop, then the
// new Events.Watch), so a refusal leaves the topic unwatched.
func (c *conn) watch(id string, req protocol.Request) protocol.Response {
	var args protocol.WatchArgs
	if !validWatchID(id) || json.Unmarshal(req.Args, &args) != nil ||
		len(args.Topics) == 0 || len(args.Topics) > protocol.MaxWatchTopicsPerRequest {
		return badWatch()
	}
	seen := make(map[string]bool, len(args.Topics))
	for _, t := range args.Topics {
		if seen[t] {
			return badWatch()
		}
		seen[t] = true
	}

	res := make([]protocol.WatchResult, len(args.Topics))
	refuse := func(i int, code, msg string) {
		res[i] = protocol.WatchResult{Topic: args.Topics[i], Code: code, Message: msg}
	}
	ev := c.srv.opts.Events
	ce := c.eventsFor(ev != nil)
	if ev == nil || ce == nil {
		for i := range args.Topics {
			code := protocol.WatchCodeUnavailable
			if _, _, ok := protocol.ParseTopic(args.Topics[i]); !ok {
				code = protocol.WatchCodeInvalidTopic
			}
			refuse(i, code, "")
		}
		return watchReply(res)
	}

	ce.mu.Lock()
	defer ce.mu.Unlock()
	if ce.closed {
		for i := range args.Topics {
			refuse(i, protocol.WatchCodeUnavailable, "")
		}
		return watchReply(res)
	}
	for i, topic := range args.Topics {
		if _, _, ok := protocol.ParseTopic(topic); !ok {
			refuse(i, protocol.WatchCodeInvalidTopic, "")
			continue
		}
		if ce.box == nil && !c.startBox(ce, ev) {
			refuse(i, protocol.WatchCodeLimit, msgTooManyConns)
			continue
		}
		old, had := ce.watches[topic]
		if !had && len(ce.watches) >= protocol.MaxWatchTopicsPerConn {
			refuse(i, protocol.WatchCodeLimit, msgTooManyTopic)
			continue
		}
		if had {
			delete(ce.watches, topic)
			old.stop()
			ce.box.Stop(topic, old.watchID)
		}
		stop, r := ev.Watch(c.ctx, c.peer, topic, id, ce.box)
		r.Topic = topic
		if !r.OK {
			if stop != nil {
				stop()
			}
			ce.box.Stop(topic, id)
			res[i] = r
			continue
		}
		if stop == nil {
			stop = func() {}
		}
		ce.watches[topic] = watchEntry{watchID: id, stop: stop}
		res[i] = r
	}
	return watchReply(res)
}

// startBox takes a box slot and starts the pump. ce.mu is held.
func (c *conn) startBox(ce *connEvents, ev Events) bool {
	if !c.takeBoxSlot() {
		return false
	}
	ce.box = eventbox.New(ev.Resnapshot)
	// Added on the reader goroutine, which is also the one that waits on wg, so
	// the Add cannot race the Wait.
	c.wg.Add(1)
	go c.eventPump(ce, ce.box)
	return true
}

// unwatch stops streams. Stopping what is not watched succeeds, so a client
// can unwatch after an error without knowing which side noticed first.
func (c *conn) unwatch(req protocol.Request) protocol.Response {
	var args protocol.UnwatchArgs
	if json.Unmarshal(req.Args, &args) != nil ||
		(!args.All && (len(args.Topics) == 0 || len(args.Topics) > protocol.MaxWatchTopicsPerRequest)) {
		return badWatch()
	}
	ce := c.eventsFor(false)
	if args.All {
		if ce != nil {
			ce.stopAll()
		}
		return watchReply([]protocol.WatchResult{})
	}
	res := make([]protocol.WatchResult, len(args.Topics))
	for i, topic := range args.Topics {
		if _, _, ok := protocol.ParseTopic(topic); !ok {
			res[i] = protocol.WatchResult{Topic: topic, Code: protocol.WatchCodeInvalidTopic}
			continue
		}
		if ce != nil {
			ce.stopOne(topic, "")
		}
		res[i] = protocol.WatchResult{Topic: topic, OK: true}
	}
	return watchReply(res)
}

// stopOne ends one topic's watch (only the one with watchID, when given) and
// reports whether there was one.
func (ce *connEvents) stopOne(topic, watchID string) bool {
	ce.mu.Lock()
	w, ok := ce.watches[topic]
	if !ok || (watchID != "" && w.watchID != watchID) {
		ce.mu.Unlock()
		return false
	}
	delete(ce.watches, topic)
	box := ce.box
	ce.mu.Unlock()
	w.stop()
	if box != nil {
		box.Stop(topic, w.watchID)
	}
	return true
}

// stopAll ends every watch and returns what it ended.
func (ce *connEvents) stopAll() map[string]watchEntry {
	ce.mu.Lock()
	ended := ce.watches
	ce.watches = map[string]watchEntry{}
	box := ce.box
	ce.mu.Unlock()
	for topic, w := range ended {
		w.stop()
		if box != nil {
			box.Stop(topic, w.watchID)
		}
	}
	return ended
}

// eventPump writes the box's items to the connection, one at a time, on the
// connection's own goroutine group. It authorizes EVERY item: a watcher that
// never sends another frame must not outlive a revocation or a tier change.
func (c *conn) eventPump(ce *connEvents, box *eventbox.Box) {
	defer c.wg.Done()
	for {
		it, ok := box.Next(c.ctx)
		if !ok {
			return
		}
		// A replaced or stopped stream's leftovers are dropped here.
		ce.mu.Lock()
		w, cur := ce.watches[it.Topic]
		ce.mu.Unlock()
		if !cur || w.watchID != it.WatchID {
			continue
		}

		probe := pushProbe
		switch err := c.srv.auth.AuthorizeFrame(c.ctx, c.peer, &probe); {
		case err == nil:
		case errors.Is(err, ErrDenied):
			c.endAll(ce, protocol.CodeDenied, msgNotPermitted)
			continue
		default: // unauthenticated, or anything unknown: send nothing more
			c.shutdown()
			return
		}

		if it.End != nil {
			ce.stopOne(it.Topic, it.WatchID)
			if !c.writeEnd(it.Topic, it.WatchID, it.End.Code, it.End.Message) {
				return
			}
			continue
		}
		f := protocol.Frame{
			V: protocol.FrameVersionCurrent, Type: protocol.FrameEvent,
			ID: it.WatchID, Topic: it.Topic, Seq: it.Seq,
		}
		if err := f.SetPayload(it.Event); err != nil {
			if !c.dropStream(ce, it.Topic, it.WatchID, protocol.CodeInternal, msgNotStreamed, "could not be encoded") {
				return
			}
			continue
		}
		if err := c.fw.WriteFrame(&f); err != nil {
			if errors.Is(err, protocol.ErrFrameTooLarge) {
				// Never a resnapshot: it would rebuild the same oversize frame.
				if !c.dropStream(ce, it.Topic, it.WatchID, protocol.CodeFrameTooLarge, msgTooLarge, "exceeds the frame limit") {
					return
				}
				continue
			}
			c.failWrite(err)
			return
		}
	}
}

// dropStream ends one stream with a final err frame and logs once per topic of
// this connection. It reports false when the connection is gone.
func (c *conn) dropStream(ce *connEvents, topic, watchID, code, msg, why string) bool {
	ce.stopOne(topic, watchID)
	ce.mu.Lock()
	first := !ce.logged[topic]
	ce.logged[topic] = true
	ce.mu.Unlock()
	if first {
		c.srv.opts.logf()("remote: device=%s event on topic=%s %s; stream ended", logSafe(c.peer.DeviceID), logSafe(topic), why)
	}
	return c.writeEnd(topic, watchID, code, msg)
}

// endAll ends every stream of the connection with one err frame each; the
// connection itself survives.
func (c *conn) endAll(ce *connEvents, code, msg string) {
	for topic, w := range ce.stopAll() {
		if !c.writeEnd(topic, w.watchID, code, msg) {
			return
		}
	}
}

// writeEnd writes a stream's final err frame (id = watch id, topic = topic).
func (c *conn) writeEnd(topic, watchID, code, msg string) bool {
	f := protocol.ErrorFrame(watchID, code, msg)
	f.Topic = topic
	if err := c.fw.WriteFrame(&f); err != nil {
		c.failWrite(err)
		return false
	}
	return true
}

// stopEvents ends every watch of this connection, closes its box and frees its
// box slot. Called from shutdown; it must complete without waiting on I/O, and
// it does: stop funcs take only the hub's short locks.
func (c *conn) stopEvents() {
	evState.mu.Lock()
	ce := evState.conns[c]
	delete(evState.conns, c)
	evState.mu.Unlock()
	if ce == nil {
		return
	}
	ce.mu.Lock()
	ce.closed = true
	ended := ce.watches
	ce.watches = map[string]watchEntry{}
	box := ce.box
	ce.mu.Unlock()
	for _, w := range ended {
		w.stop()
	}
	if box != nil {
		box.Close()
		c.releaseBoxSlot()
	}
}
