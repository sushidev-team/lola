package remote

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"

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

	// peer, authed and seq are written under srv.mu (admitAuthed, addConn,
	// removeConn), which is what DropDevice and DeviceConns read them under. The
	// reader goroutine reads peer unlocked: it is the only writer after the
	// handshake.
	peer   Peer
	authed bool
	seq    uint64

	// releasePre frees this connection's pre-auth slot; nil outside serveConn.
	releasePre func()

	// strikes counts refusals that kept the connection; reader goroutine only.
	strikes int

	// ptyAudited records the panes whose first raw write was audited; pane
	// worker only.
	ptyAudited map[string]bool

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

		ptyAudited: map[string]bool{},
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

	peer, ok := c.authenticate()
	if !ok {
		return
	}
	// Authenticated: the connection leaves the pre-auth pool for the authed one.
	if c.releasePre != nil {
		c.releasePre()
	}
	if !c.srv.admitAuthed(c, peer) {
		return
	}
	if !c.sendReady() {
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
		switch f.Type {
		case protocol.FrameReq:
			err = c.handleReq(f)
		case protocol.FrameSub, protocol.FrameUnsub, protocol.FramePTY:
			err = c.handlePane(f)
		case protocol.FrameErr:
			// A client reporting a refusal it made locally. The daemon never
			// asked it a question, so there is nothing to act on; it is logged
			// without the payload, through the device's log bucket (it costs
			// the peer nothing else), and the connection carries on.
			if _, err = c.admit(f, protocol.TierRead); err == nil {
				c.logDevice("remote: device=%s reported an error frame", logSafe(c.peer.DeviceID))
			}
		}
		if err != nil {
			return
		}
	}
}

// authenticate runs the authorizer's one-shot handshake under a bounded read
// deadline, then clears it: an attached pane is legitimately silent for
// minutes, so there is no idle deadline afterwards and shutdown is bounded by
// Close rather than by a timer. It does not publish the peer; admitAuthed does,
// under the server's lock.
func (c *conn) authenticate() (Peer, bool) {
	_ = c.nc.SetReadDeadline(time.Now().Add(handshakeTimeout))
	defer func() { _ = c.nc.SetReadDeadline(time.Time{}) }()

	hs := &Handshake{
		RemoteAddr:      c.nc.RemoteAddr().String(),
		At:              c.srv.opts.now(),
		Via:             ViaLAN,
		NextFrame:       c.readValidated,
		SetReadDeadline: c.nc.SetReadDeadline,
		Send: func(f *protocol.Frame) error {
			return c.fw.WriteFrame(f)
		},
	}
	if tc, ok := c.nc.(*tls.Conn); ok {
		st := tc.ConnectionState()
		hs.TLS = &st
		if r, ok := tc.NetConn().(relayed); ok {
			hs.Via, hs.RelayRV = ViaRelay, r.relayRV()
		}
	}

	peer, err := c.srv.auth.Authenticate(c.ctx, hs)
	switch {
	case errors.Is(err, ErrPairingDone):
		c.srv.opts.logf()("remote: pairing connection from %s via=%s finished", logSafe(hs.RemoteAddr), hs.Via)
		return Peer{}, false
	case err != nil:
		// Any certificate passes TLS while a pairing window is open, so this
		// line is one an unauthenticated peer can cause at will.
		c.srv.logPreAuth("remote: rejected %s via=%s: %v", logSafe(hs.RemoteAddr), hs.Via, err)
		return Peer{}, false
	}
	peer.RemoteAddr = hs.RemoteAddr
	peer.Via = hs.Via
	if peer.ConnectedAt.IsZero() {
		peer.ConnectedAt = hs.At
	}
	return peer, true
}

// sendReady writes the one ready frame of a connection, before any frame is
// read. TLS 1.3 cannot tell a client it was accepted, so a client counts a path
// as authenticated only when this arrives. A device that vanished from the
// registry since Authenticate gets not_paired instead.
func (c *conn) sendReady() bool {
	data := protocol.ReadyData{
		DeviceID: c.peer.DeviceID, Name: c.peer.Label, Tiers: []string{},
		Via: c.peer.Via, Host: c.srv.opts.Host,
	}
	if reg := c.srv.opts.Devices; reg != nil {
		d, ok := reg.Get(c.peer.DeviceID)
		if !ok || d.Epoch != c.peer.Epoch {
			c.refuse("", protocol.CodeNotPaired, "this device is not paired")
			c.srv.opts.logf()("remote: device=%s vanished before ready", logSafe(c.peer.DeviceID))
			return false
		}
		data.Tiers = d.Tiers
	}
	f := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameReady}
	if err := f.SetPayload(data); err != nil {
		c.srv.opts.logf()("remote: encode ready: %v", err)
		return false
	}
	if err := c.fw.WriteFrame(&f); err != nil {
		c.failWrite(err)
		return false
	}
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
		c.logPeer("remote: %s sent envelope v%d; this daemon speaks v%d..v%d",
			c.nc.RemoteAddr(), f.V, protocol.FrameVersionMin, protocol.FrameVersionCurrent)
		return nil, errRefused
	}
	if !protocol.KnownFrameType(f.Type) {
		c.refuse(f.ID, protocol.CodeUnknownType, "unknown frame type")
		c.logPeer("remote: %s sent unknown frame type %q", c.nc.RemoteAddr(), logSafe(f.Type))
		return nil, errRefused
	}
	if !protocol.DaemonAcceptsFrame(f.Type) {
		// A daemon-to-client type arriving inbound. Refused rather than
		// guessed at: the direction table is part of the contract.
		c.refuse(f.ID, protocol.CodeUnknownType, "frame type is not accepted by the daemon")
		c.logPeer("remote: %s sent outbound-only frame type %q", c.nc.RemoteAddr(), logSafe(f.Type))
		return nil, errRefused
	}
	return f, nil
}

// verdict is a refusal that keeps the connection.
type verdict struct{ code, msg string }

func (v verdict) refused() bool { return v.code != "" }

// errStrikeLimit closes a connection that keeps asking for what it may not have.
var errStrikeLimit = errors.New("remote: too many refusals on one connection")

// admit is the per-frame gate, shared by every inbound frame type. Tier never is
// refused BEFORE the authorizer is consulted, so no implementation can grant
// stop, hookEvent or a forced kill. The authorizer then answers ErrDenied (a
// refusal; the connection survives) or ErrUnauthenticated, which means the
// device was revoked or re-paired and ends the connection.
//
// A refusal does not close the connection: the strike counter bounds a probing
// client. The returned error is non-nil only when the connection must close.
func (c *conn) admit(f *protocol.Frame, tier string) (verdict, error) {
	if tier == protocol.TierNever {
		c.srv.opts.logf()("remote: device=%s denied type=%s cmd=%s", logSafe(c.peer.DeviceID), logSafe(f.Type), logSafe(f.Cmd))
		return verdict{protocol.CodeUnknownCmd, "command is not available remotely"}, c.strike()
	}
	if err := c.srv.auth.AuthorizeFrame(c.ctx, c.peer, f); err != nil {
		if errors.Is(err, ErrDenied) {
			c.srv.opts.logf()("remote: device=%s denied type=%s cmd=%s tier=%s", logSafe(c.peer.DeviceID),
				logSafe(f.Type), logSafe(f.Cmd), logSafe(tier))
			return verdict{protocol.CodeDenied, "not permitted for this device"}, c.strike()
		}
		c.srv.opts.logf()("remote: device=%s closed on type=%s: %v", logSafe(c.peer.DeviceID), logSafe(f.Type), err)
		return verdict{}, errRefused
	}
	return verdict{}, nil
}

// strike counts one refusal.
func (c *conn) strike() error {
	c.strikes++
	if c.strikes >= maxRefusalsPerConn {
		c.srv.opts.logf()("remote: device=%s closed after %d refusals", logSafe(c.peer.DeviceID), c.strikes)
		return errStrikeLimit
	}
	return nil
}

// hasTier reads the device's tiers live from the registry. A server with no
// registry grants nothing.
func (c *conn) hasTier(tier string) bool {
	reg := c.srv.opts.Devices
	if reg == nil {
		return false
	}
	d, ok := reg.Get(c.peer.DeviceID)
	return ok && d.Has(tier)
}

// handleReq gates, rebuilds, rate-limits and dispatches one req frame. It
// decodes HERE, on the reader goroutine (the payload aliases a buffer the next
// read overwrites). A returned error closes the connection; a refusal on the
// frame's id does not.
func (c *conn) handleReq(f *protocol.Frame) error {
	tier := frameTier(f)
	cmd, id := f.Cmd, f.ID

	var in protocol.Request
	decodeErr := f.DecodePayload(&in)

	v, err := c.admit(f, tier)
	if v.refused() {
		c.audit(auditRecord{tier: tier, cmd: cmd, session: in.Session, code: v.code})
		c.refuse(id, v.code, v.msg)
		return err
	}
	if err != nil {
		return err
	}
	if decodeErr != nil {
		// The framing is intact, so the connection survives and the refusal goes
		// back on this frame's id; the decoder's own text stays off the wire.
		c.audit(auditRecord{tier: tier, cmd: cmd, code: protocol.CodeInternal})
		c.refuse(id, protocol.CodeInternal, "malformed request payload")
		// Costs a strike like every other refusal: it reaches neither the
		// handler nor a rate bucket, so nothing else bounds a peer repeating it.
		return c.strike()
	}

	// Cmd comes from the ENVELOPE, never from the payload: authorization read the
	// envelope's Cmd, so a payload naming another command would be authorized as
	// one thing and executed as another. rebuild builds a FRESH request from the
	// command's own field spec and discards everything else the phone sent.
	req, res, rerr := rebuild(cmd, in, c.hasTier)
	if rerr != nil {
		c.audit(auditRecord{tier: tier, cmd: cmd, session: in.Session, code: protocol.CodeDenied})
		c.refuse(id, protocol.CodeDenied, rerr.Error())
		return c.strike()
	}
	rec := auditRecord{
		tier: tier, cmd: cmd, session: req.Session,
		fields: auditFields(requestFields[cmd].audit, req), dropped: res.dropped,
	}
	// A rate-limit refusal keeps the connection and costs no strike (a busy
	// phone is not a probing one), so its line goes through the device's log
	// bucket: the frames are bounded by the bucket that refused them, the
	// lines by this one.
	if !c.srv.lim.allowReq(c.peer.DeviceID, tier, cmd, res.attachBytes, res.attachLast) {
		rec.code = protocol.CodeRateLimited
		c.auditThrottled(rec)
		c.refuse(id, protocol.CodeRateLimited, "rate limit exceeded")
		return nil
	}

	if c.dispatchEvents(id, req) {
		return nil
	}

	select {
	case c.sem <- struct{}{}:
	default:
		// Refused rather than queued: queueing it HERE would block the reader,
		// which is exactly the head-of-line block this design removes.
		rec.code = protocol.CodeRateLimited
		c.auditThrottled(rec)
		c.refuse(id, protocol.CodeRateLimited, "too many requests in flight on this connection")
		return nil
	}

	c.reqWg.Add(1)
	go func() {
		defer c.reqWg.Done()
		defer func() { <-c.sem }()
		defer func() {
			if r := recover(); r != nil {
				c.srv.opts.logf()("remote: handler panic on cmd=%s: %v", logSafe(cmd), r)
				rec.code = protocol.CodeInternal
				c.audit(rec)
				c.refuse(id, protocol.CodeInternal, "handler failed")
			}
		}()

		resp := c.srv.opts.Handle(WithPeer(c.ctx, c.peer), req)
		rec.ok = resp.OK
		// An accepted non-final attachImage part writes no line; the last part
		// does, and a refused non-final part writes one through the device's
		// log bucket, because it paid a read token, not a write.
		switch {
		case cmd != protocol.CmdAttachImage || res.attachLast:
			c.audit(rec)
		case !resp.OK:
			c.auditThrottled(rec)
		}

		out := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameResp, ID: id}
		if err := out.SetPayload(resp); err != nil {
			c.srv.opts.logf()("remote: encode response for cmd=%s: %v", logSafe(cmd), err)
			c.refuse(id, protocol.CodeInternal, "response could not be encoded")
			return
		}
		if err := c.fw.WriteFrame(&out); err != nil {
			c.failWrite(err)
		}
	}()
	return nil
}

// handlePane gates a sub, unsub or pty frame and hands it to the ordered worker.
func (c *conn) handlePane(f *protocol.Frame) error {
	v, err := c.admit(f, frameTier(f))
	if v.refused() {
		c.refusePane(f.ID, f.Pane, v.code, v.msg)
		return err
	}
	if err != nil {
		return err
	}
	return c.enqueuePane(f)
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
		c.stopEvents()
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
