package remote

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/sushidev-team/lola/internal/protocol"
)

// errRefused is the reader's internal signal that a frame has already been
// answered with a refusal and the connection must now close. It never reaches
// the peer — the peer got a protocol.ErrPayload — and it never reaches a log
// line on its own, because the refusal itself was logged where it was decided.
var errRefused = errors.New("remote: frame refused")

// conn is one accepted connection.
//
// Exactly ONE goroutine reads: that is what makes protocol.FrameReader's buffer
// reuse safe, and it is why every payload is decoded here, before anything is
// handed off. The reader never waits on the work it dispatches — req frames go
// to a bounded pool, and sub/unsub/pty frames go to one ordered worker — which
// is the whole difference from internal/daemon's strictly serial handleConn.
type conn struct {
	srv *Server
	nc  net.Conn
	fr  *protocol.FrameReader
	fw  *protocol.FrameWriter

	ctx    context.Context
	cancel context.CancelFunc

	peer  Peer
	frame protocol.Frame // the reader's reusable destination

	sem   chan struct{}
	queue chan paneOp

	// wg covers the goroutines this package can PROMISE will stop: the pane
	// worker and the pane pumps, both of which select on done and whose writes
	// carry a deadline. The reader is tracked by the server, and it is the
	// goroutine that waits on this group, so it must not be in it.
	wg sync.WaitGroup

	// reqWg covers the in-flight request goroutines, and it is separate from wg
	// precisely because this package cannot promise anything about them: they
	// are sitting inside the daemon's Handle seam, which is allowed to shield
	// itself from the cancel (pollOnce runs its tick on context.WithoutCancel).
	// Waiting on them unconditionally would have made Server.Close — the FIRST
	// step of the daemon's shutdown — as long as a Linear poll plus its spawns.
	reqWg sync.WaitGroup

	mu   sync.Mutex
	subs map[string]*subscription

	closeOnce sync.Once
	done      chan struct{}
}

func newConn(s *Server, nc net.Conn) *conn {
	c := &conn{
		srv:   s,
		nc:    nc,
		fr:    protocol.NewFrameReader(nc),
		fw:    protocol.NewFrameWriter(deadlineWriter{c: nc, d: writeTimeout}),
		sem:   make(chan struct{}, reqConcurrency),
		queue: make(chan paneOp, paneQueueDepth),
		subs:  map[string]*subscription{},
		done:  make(chan struct{}),
	}
	c.ctx, c.cancel = context.WithCancel(s.ctx)
	return c
}

// deadlineWriter arms a write deadline before every write, so a peer that stops
// reading cannot pin a writer. protocol.FrameWriter serializes the writes, so
// the deadline is per frame.
type deadlineWriter struct {
	c net.Conn
	d time.Duration
}

func (w deadlineWriter) Write(p []byte) (int, error) {
	if w.d > 0 {
		_ = w.c.SetWriteDeadline(time.Now().Add(w.d))
	}
	return w.c.Write(p)
}

// run authenticates the peer and then reads frames until the connection ends.
func (c *conn) run() {
	// Deferred LIFO: shutdown runs first (it unblocks every goroutine below),
	// then the groups drain. Waiting before closing would hang on a pump
	// blocked writing to a peer that stopped reading.
	defer c.waitGoroutines()
	defer c.shutdown()

	if !c.authenticate() {
		return
	}

	c.wg.Add(1)
	go c.paneLoop()

	for {
		f, err := c.readValidated()
		if err != nil {
			c.logClose(err)
			return
		}
		if err := c.authorize(f); err != nil {
			return
		}
		switch f.Type {
		case protocol.FrameReq:
			if err := c.dispatchReq(f); err != nil {
				return
			}
		case protocol.FrameSub, protocol.FrameUnsub, protocol.FramePTY:
			if err := c.enqueuePane(f); err != nil {
				return
			}
		case protocol.FrameErr:
			// A client reporting a refusal it made locally. The daemon never
			// asked it a question, so there is nothing to act on; it is logged
			// without the payload and the connection carries on.
			c.srv.opts.logf()("remote: device=%s reported an error frame", logSafe(c.peer.DeviceID))
		}
	}
}

// authenticate runs the authorizer's one-shot handshake under a bounded read
// deadline, then clears it: an attached pane is legitimately silent for
// minutes, so there is no idle deadline afterwards and shutdown is bounded by
// Close rather than by a timer.
func (c *conn) authenticate() bool {
	_ = c.nc.SetReadDeadline(time.Now().Add(handshakeTimeout))
	defer func() { _ = c.nc.SetReadDeadline(time.Time{}) }()

	hs := &Handshake{
		RemoteAddr: c.nc.RemoteAddr().String(),
		At:         c.srv.opts.now(),
		NextFrame:  c.readValidated,
		Send: func(f *protocol.Frame) error {
			return c.fw.WriteFrame(f)
		},
	}
	if tc, ok := c.nc.(*tls.Conn); ok {
		st := tc.ConnectionState()
		hs.TLS = &st
	}

	peer, err := c.srv.auth.Authenticate(c.ctx, hs)
	if err != nil {
		c.srv.opts.logf()("remote: rejected %s: %v", hs.RemoteAddr, err)
		return false
	}
	peer.RemoteAddr = hs.RemoteAddr
	if peer.ConnectedAt.IsZero() {
		peer.ConnectedAt = hs.At
	}
	c.peer = peer
	return true
}

// readValidated reads one frame and applies the ENVELOPE rules, which are the
// three that fail closed by closing: an unsupported version, an unknown type,
// and a type travelling the wrong way. Each writes one refusal and returns
// errRefused; the caller closes.
//
// The returned frame points at the reader's reusable destination, so its
// Payload is valid only until the next call. Every caller decodes before
// reading on.
func (c *conn) readValidated() (*protocol.Frame, error) {
	if err := c.fr.ReadFrame(&c.frame); err != nil {
		return nil, err
	}
	f := &c.frame
	if !protocol.SupportedFrameVersion(f.V) {
		// The reply carries the DAEMON's own version and both bounds, so the
		// client can name which side is behind instead of showing a connect
		// error. The daemon never adopts an unknown version.
		ref := protocol.UnsupportedVersionFrame(f.ID)
		_ = c.fw.WriteFrame(&ref)
		c.srv.opts.logf()("remote: %s sent envelope v%d; this daemon speaks v%d..v%d",
			c.nc.RemoteAddr(), f.V, protocol.FrameVersionMin, protocol.FrameVersionCurrent)
		return nil, errRefused
	}
	if !protocol.KnownFrameType(f.Type) {
		c.refuse(f.ID, protocol.CodeUnknownType, "unknown frame type")
		c.srv.opts.logf()("remote: %s sent unknown frame type %q", c.nc.RemoteAddr(), logSafe(f.Type))
		return nil, errRefused
	}
	if !protocol.DaemonAcceptsFrame(f.Type) {
		// A daemon-to-client type arriving inbound. Refused rather than
		// guessed at: the direction table is part of the contract.
		c.refuse(f.ID, protocol.CodeUnknownType, "frame type is not accepted by the daemon")
		c.srv.opts.logf()("remote: %s sent outbound-only frame type %q", c.nc.RemoteAddr(), logSafe(f.Type))
		return nil, errRefused
	}
	return f, nil
}

// authorize applies the unconditional denials FIRST and only then consults the
// authorizer, so no implementation of Authorizer can ever grant stop,
// hookEvent or one of the pairing commands. Both refusals close the connection:
// a peer asking for a denied command is broken or hostile, and there is nothing
// useful for it to do next on this connection.
func (c *conn) authorize(f *protocol.Frame) error {
	if f.Type == protocol.FrameReq && CommandDenied(f.Cmd) {
		c.refuse(f.ID, protocol.CodeUnknownCmd, "command is not available remotely")
		c.srv.opts.logf()("remote: device=%s denied cmd=%s", logSafe(c.peer.DeviceID), logSafe(f.Cmd))
		return errRefused
	}
	if err := c.srv.auth.AuthorizeFrame(c.ctx, c.peer, f); err != nil {
		c.refuse(f.ID, protocol.CodeDenied, "not permitted for this device")
		c.srv.opts.logf()("remote: device=%s denied type=%s cmd=%s: %v",
			logSafe(c.peer.DeviceID), logSafe(f.Type), logSafe(f.Cmd), err)
		return errRefused
	}
	return nil
}

// dispatchReq decodes the request HERE, on the reader goroutine (the payload
// aliases a buffer the next read overwrites), and then runs it concurrently.
// A returned error closes the connection; a refusal on the frame's id does not.
func (c *conn) dispatchReq(f *protocol.Frame) error {
	var req protocol.Request
	if err := f.DecodePayload(&req); err != nil {
		// The framing is intact, so the connection survives and the refusal
		// goes back on this frame's id. The closed code list has no
		// bad-payload member at V=1, so the message names it; the decoder's
		// own text stays in the local log, never on the wire.
		c.refuse(f.ID, protocol.CodeInternal, "malformed request payload")
		return nil
	}
	req = normalizeRequest(f.Cmd, req)
	id := f.ID

	select {
	case c.sem <- struct{}{}:
	default:
		// Refused rather than queued: queueing it HERE would block the reader,
		// which is exactly the head-of-line block this design removes.
		c.refuse(id, protocol.CodeRateLimited, "too many requests in flight on this connection")
		return nil
	}

	c.reqWg.Add(1)
	go func() {
		defer c.reqWg.Done()
		defer func() { <-c.sem }()
		defer func() {
			if r := recover(); r != nil {
				c.srv.opts.logf()("remote: handler panic on cmd=%s: %v", logSafe(req.Cmd), r)
				c.refuse(id, protocol.CodeInternal, "handler failed")
			}
		}()

		resp := c.srv.opts.Handle(c.ctx, req)
		c.audit(req, resp)

		out := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameResp, ID: id}
		if err := out.SetPayload(resp); err != nil {
			c.srv.opts.logf()("remote: encode response for cmd=%s: %v", logSafe(req.Cmd), err)
			c.refuse(id, protocol.CodeInternal, "response could not be encoded")
			return
		}
		if err := c.fw.WriteFrame(&out); err != nil {
			c.failWrite(err)
		}
	}()
	return nil
}

// audit writes the one line a mutating remote frame leaves behind: the device,
// the command, the session and the outcome — and NEVER the payload. That
// exclusion is load-bearing rather than fastidious: answer carries prose a human
// typed at a phone, which may contain a pasted token; openManual and openTicket
// carry prompt seeds; openURL's own handler logs a full URL today and this path
// must not. PTY bytes and resync frames are never logged at any level.
//
// The daemon's log is append-only, unrotated, and mirrored to stderr through an
// io.MultiWriter, so a remote peer can now drive writes into a file that grows
// without bound and into whatever a launchd stderr redirect points at. Reads
// are therefore not audited at all, and every field that reaches the line is
// clipped and stripped of control characters first.
func (c *conn) audit(req protocol.Request, resp protocol.Response) {
	if !auditable(req.Cmd) {
		return
	}
	c.srv.opts.logf()("remote: device=%s insecure=%t cmd=%s session=%s ok=%t",
		logSafe(c.peer.DeviceID), c.peer.Insecure, logSafe(req.Cmd), logSafe(req.Session), resp.OK)
}

// refuse writes one err frame, best effort. A write failure here is not worth a
// second error path: the connection is closing either way.
func (c *conn) refuse(id, code, msg string) {
	f := protocol.ErrorFrame(id, code, msg)
	_ = c.fw.WriteFrame(&f)
}

// refusePane is refuse with the pane named, for the frames that carry no id.
//
// The name is CLIPPED because it is the peer's own string echoed back: an
// oversized one made the encoded err frame exceed the frame cap, so the writer
// returned ErrFrameTooLarge and wrote nothing at all. The peer then saw silence
// where the protocol promises a coded refusal, and could not tell a refusal
// from a hang.
func (c *conn) refusePane(id, pane, code, msg string) {
	f := protocol.ErrorFrame(id, code, msg)
	if len(pane) > maxPaneName {
		pane = pane[:maxPaneName]
	}
	f.Pane = pane
	_ = c.fw.WriteFrame(&f)
}

// failWrite tears the connection down after a write error: the transport is
// broken, so every other goroutine sharing it is writing into a hole.
func (c *conn) failWrite(err error) {
	if c.isDone() {
		return
	}
	c.srv.opts.logf()("remote: device=%s write failed: %v", logSafe(c.peer.DeviceID), err)
	c.shutdown()
}

// logClose reports why the reader stopped. A clean close and a shutdown are
// silent; anything else is a line.
func (c *conn) logClose(err error) {
	switch {
	case err == nil, errors.Is(err, errRefused), errors.Is(err, net.ErrClosed), c.isDone():
		return
	case errors.Is(err, protocol.ErrFrameTooLarge), errors.Is(err, protocol.ErrFrameEmpty):
		// The length prefix could not be honoured, so the stream cannot be
		// resynchronized. The refusal is best effort and the connection closes.
		c.refuse("", protocol.CodeFrameTooLarge, "frame exceeds the maximum frame size")
		c.srv.opts.logf()("remote: %s: %v", c.nc.RemoteAddr(), err)
	case errors.Is(err, protocol.ErrFrameMalformed):
		// A malformed ENVELOPE has no id to answer on. The decoder's text is
		// for this log only and is never echoed to the peer.
		c.refuse("", protocol.CodeInternal, "malformed frame")
		c.srv.opts.logf()("remote: %s sent an undecodable frame: %v", c.nc.RemoteAddr(), err)
	default:
		c.srv.opts.logf()("remote: %s: read: %v", c.nc.RemoteAddr(), err)
	}
}

// waitGoroutines drains what this connection started, and the two groups are
// waited on DIFFERENTLY on purpose.
//
// wg is this package's own work and stops on the cancel plus the socket close,
// so it is waited for without a bound. reqWg is inside the daemon's Handle
// seam, which may legitimately outlive a cancel, so it gets handlerGrace and
// then a log line — the handler carries a beginConnWork registration of its
// own, so it is still waited for by the daemon's drain group, just not by the
// listener's Close. The straggler writes into a socket that is already closed,
// which fails harmlessly.
func (c *conn) waitGoroutines() {
	c.wg.Wait()

	done := make(chan struct{})
	go func() {
		c.reqWg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(c.srv.opts.handlerGrace()):
		c.srv.opts.logf()("remote: device=%s left a request running past shutdown; it finishes under the daemon's drain",
			logSafe(c.peer.DeviceID))
	}
}

func (c *conn) isDone() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// shutdown unblocks everything this connection owns and is idempotent. It does
// NOT wait: the reader goroutine owns the wait, because it is not in the group.
func (c *conn) shutdown() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.cancel()
		c.nc.Close()
		c.mu.Lock()
		subs := make([]*subscription, 0, len(c.subs))
		for _, s := range c.subs {
			subs = append(subs, s)
		}
		c.subs = map[string]*subscription{}
		c.mu.Unlock()
		for _, s := range subs {
			s.close()
		}
	})
}

// logSafe clips an untrusted string and strips whatever would forge a log line.
// Session ids, command names and device labels all reach the audit line, and
// all of them are peer-supplied; a newline in one of them would write a second
// line that reads as the daemon's own.
func logSafe(s string) string {
	const max = 64
	if s == "" {
		return "-"
	}
	var b strings.Builder
	for i, r := range s {
		if i >= max {
			b.WriteString("...")
			break
		}
		if r == unicode.ReplacementChar || unicode.IsControl(r) {
			b.WriteByte('?')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
