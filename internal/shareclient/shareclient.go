// Package shareclient is the teammate's end of a shared session
// (docs/team-sessions.md): a mutual-TLS client over an already-open byte
// stream to the owner's daemon, pinned to the owner's key, speaking the pane
// frames of internal/protocol. It dials nothing itself (the caller hands it the
// hub relay leg), holds no secrets beyond the client certificate it is given,
// and imports no daemon state, so a desktop app, a TUI or a test can all use it.
package shareclient

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// Bounds. A request that gets no answer fails; a stalled write fails the
// connection rather than queueing keystrokes behind it.
const (
	readyTimeout    = 10 * time.Second
	requestTimeout  = 15 * time.Second
	writeTimeout    = 10 * time.Second
	eventBuffer     = 256
	maxPendingReqs  = 8
	maxWriteBytes   = 64 << 10
	maxScrollLines  = 200
	reconnectResubs = 3
)

var (
	// ErrPinMismatch is returned when the peer's key is not the one the person
	// pinned: a different machine, or someone in the middle.
	ErrPinMismatch = errors.New("shareclient: the host's key is not the one you trusted")
	// ErrClosed is returned by calls on a closed connection.
	ErrClosed = errors.New("shareclient: connection closed")
	// ErrNotReady is returned when the host did not accept the connection.
	ErrNotReady = errors.New("shareclient: the host did not accept this device")
)

// Kind discriminates an Event.
type Kind int

const (
	// Resync is a coherent screen: paint it in place of what is shown.
	Resync Kind = iota
	// Output is raw terminal bytes.
	Output
	// Exit is the end of the pane: its child exited, or it was unshared.
	Exit
	// Refused is a refusal of a frame on this pane (Code names it).
	Refused
)

// Event is one thing that happened on a subscribed pane.
type Event struct {
	Pane   string
	Kind   Kind
	Data   []byte // Output
	Screen protocol.ResyncPayload
	Code   string // Refused: a protocol.Code*
	Msg    string // Refused: the host's short line
}

// Config is what a dial needs.
type Config struct {
	// Cert is this device's client certificate (the daemon's device key).
	Cert tls.Certificate
	// HostSPKI is the SHA-256 of the owner's SubjectPublicKeyInfo, pinned by
	// the person who trusted that teammate.
	HostSPKI [32]byte
}

// Conn is one authenticated connection to an owner.
type Conn struct {
	nc net.Conn
	fr *protocol.FrameReader
	fw *protocol.FrameWriter

	Ready protocol.ReadyData

	events chan Event
	done   chan struct{}
	once   sync.Once
	err    atomic.Value // error

	mu      sync.Mutex
	pending map[string]chan protocol.Frame
	seq     map[string]uint64 // last pane seq seen
	subs    map[string]protocol.SubPayload
	resubs  map[string]int
	nextID  atomic.Uint64
}

// Dial runs the TLS handshake over raw, pins the host's key, and waits for the
// ready frame, which is the only proof that the host accepted this device (TLS
// 1.3 finishes before the server judges the client certificate).
func Dial(ctx context.Context, raw net.Conn, cfg Config) (*Conn, error) {
	pinned := cfg.HostSPKI
	tc := tls.Client(raw, &tls.Config{
		Certificates: []tls.Certificate{cfg.Cert},
		MinVersion:   tls.VersionTLS13,
		// The certificate is self-signed; trust is the pin, checked below.
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) != 1 {
				return ErrPinMismatch
			}
			leaf, err := x509.ParseCertificate(cs.PeerCertificates[0].Raw)
			if err != nil || sha256.Sum256(leaf.RawSubjectPublicKeyInfo) != pinned {
				return ErrPinMismatch
			}
			return nil
		},
	})
	if dl, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(dl)
	} else {
		_ = raw.SetDeadline(time.Now().Add(readyTimeout))
	}
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	if err := tc.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		if errors.Is(err, ErrPinMismatch) {
			return nil, ErrPinMismatch
		}
		return nil, fmt.Errorf("shareclient: handshake: %w", err)
	}
	c := &Conn{
		nc:      tc,
		fr:      protocol.NewFrameReader(tc),
		fw:      protocol.NewFrameWriter(writerWithDeadline{tc}),
		events:  make(chan Event, eventBuffer),
		done:    make(chan struct{}),
		pending: map[string]chan protocol.Frame{},
		seq:     map[string]uint64{},
		subs:    map[string]protocol.SubPayload{},
		resubs:  map[string]int{},
	}
	var f protocol.Frame
	if err := c.fr.ReadFrame(&f); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("%w: %v", ErrNotReady, err)
	}
	if f.Type != protocol.FrameReady {
		_ = raw.Close()
		return nil, ErrNotReady
	}
	if err := json.Unmarshal(f.Payload, &c.Ready); err != nil {
		_ = raw.Close()
		return nil, ErrNotReady
	}
	_ = raw.SetDeadline(time.Time{})
	go c.readLoop()
	return c, nil
}

type writerWithDeadline struct{ c net.Conn }

func (w writerWithDeadline) Write(p []byte) (int, error) {
	_ = w.c.SetWriteDeadline(time.Now().Add(writeTimeout))
	return w.c.Write(p)
}

// Events delivers pane events in order. It is closed when the connection ends;
// Err then says why. A consumer that falls behind loses the connection rather
// than the order of the stream.
func (c *Conn) Events() <-chan Event { return c.events }

// Done is closed when the connection has ended.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err is why the connection ended (nil while it lives).
func (c *Conn) Err() error {
	if e, ok := c.err.Load().(error); ok {
		return e
	}
	return nil
}

// Close ends the connection. It is idempotent.
func (c *Conn) Close() error {
	c.fail(ErrClosed)
	return nil
}

func (c *Conn) fail(err error) {
	c.once.Do(func() {
		c.err.Store(err)
		close(c.done)
		_ = c.nc.Close()
		c.mu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
	})
}

func (c *Conn) send(f *protocol.Frame) error {
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	f.V = protocol.FrameVersionCurrent
	if err := c.fw.WriteFrame(f); err != nil {
		c.fail(err)
		return err
	}
	return nil
}

// Roster asks the host which sessions it shares with this device and who is
// attached to each.
func (c *Conn) Roster(ctx context.Context) (protocol.ShareSessionsData, error) {
	var out protocol.ShareSessionsData
	resp, err := c.request(ctx, protocol.CmdShareSessions)
	if err != nil {
		return out, err
	}
	if !resp.OK {
		return out, errors.New("shareclient: the host refused the roster")
	}
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return out, errors.New("shareclient: the host sent a roster that does not decode")
	}
	return out, nil
}

func (c *Conn) request(ctx context.Context, cmd string) (protocol.Response, error) {
	id := fmt.Sprintf("q%d", c.nextID.Add(1))
	ch := make(chan protocol.Frame, 1)
	c.mu.Lock()
	if len(c.pending) >= maxPendingReqs {
		c.mu.Unlock()
		return protocol.Response{}, errors.New("shareclient: too many requests in flight")
	}
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	f := protocol.Frame{Type: protocol.FrameReq, ID: id, Cmd: cmd}
	if err := f.SetPayload(protocol.Request{Cmd: cmd}); err != nil {
		return protocol.Response{}, err
	}
	if err := c.send(&f); err != nil {
		return protocol.Response{}, err
	}
	t := time.NewTimer(requestTimeout)
	defer t.Stop()
	select {
	case r, ok := <-ch:
		if !ok {
			return protocol.Response{}, ErrClosed
		}
		if r.Type == protocol.FrameErr {
			var e protocol.ErrPayload
			_ = json.Unmarshal(r.Payload, &e)
			return protocol.Response{}, fmt.Errorf("shareclient: refused (%s)", e.Code)
		}
		var resp protocol.Response
		if err := json.Unmarshal(r.Payload, &resp); err != nil {
			return protocol.Response{}, errors.New("shareclient: undecodable response")
		}
		return resp, nil
	case <-t.C:
		return protocol.Response{}, errors.New("shareclient: the host did not answer")
	case <-ctx.Done():
		return protocol.Response{}, ctx.Err()
	case <-c.done:
		return protocol.Response{}, ErrClosed
	}
}

// Subscribe attaches to pane. The first event for it is a Resync. cols and
// rows are advisory (the host attaches at its own window size).
func (c *Conn) Subscribe(pane string, cols, rows int) error {
	sp := protocol.SubPayload{Cols: cols, Rows: rows}
	c.mu.Lock()
	c.subs[pane] = sp
	delete(c.seq, pane)
	c.mu.Unlock()
	return c.sendSub(pane, sp)
}

func (c *Conn) sendSub(pane string, sp protocol.SubPayload) error {
	f := protocol.Frame{Type: protocol.FrameSub, ID: fmt.Sprintf("s%d", c.nextID.Add(1)), Pane: pane}
	if err := f.SetPayload(sp); err != nil {
		return err
	}
	return c.send(&f)
}

// Unsubscribe detaches from pane.
func (c *Conn) Unsubscribe(pane string) error {
	c.mu.Lock()
	delete(c.subs, pane)
	delete(c.seq, pane)
	c.mu.Unlock()
	f := protocol.Frame{Type: protocol.FrameUnsub, Pane: pane}
	return c.send(&f)
}

func (c *Conn) pty(pane string, in protocol.PTYInputPayload) error {
	f := protocol.Frame{Type: protocol.FramePTY, Pane: pane}
	if err := f.SetPayload(in); err != nil {
		return err
	}
	return c.send(&f)
}

// Write sends keystrokes. The host refuses them unless the share allows typing.
func (c *Conn) Write(pane string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if len(data) > maxWriteBytes {
		return errors.New("shareclient: too much input at once")
	}
	return c.pty(pane, protocol.PTYInputPayload{Action: protocol.PTYActionWrite, Data: data})
}

// Scroll moves the pane's own transcript; the host decides how.
func (c *Conn) Scroll(pane string, lines int) error {
	lines = max(-maxScrollLines, min(maxScrollLines, lines))
	if lines == 0 {
		return nil
	}
	return c.pty(pane, protocol.PTYInputPayload{Action: protocol.PTYActionScroll, Lines: lines})
}

// Resize states this viewer's size. The host records it and keeps its own.
func (c *Conn) Resize(pane string, cols, rows int) error {
	c.mu.Lock()
	if sp, ok := c.subs[pane]; ok {
		sp.Cols, sp.Rows = cols, rows
		c.subs[pane] = sp
	}
	c.mu.Unlock()
	return c.pty(pane, protocol.PTYInputPayload{Action: protocol.PTYActionResize, Cols: cols, Rows: rows})
}

// emit hands an event to the consumer. A consumer that does not keep up ends
// the connection: dropping events would corrupt the screen it is drawing.
func (c *Conn) emit(ev Event) {
	select {
	case c.events <- ev:
	default:
		c.fail(errors.New("shareclient: the consumer fell behind"))
	}
}

func (c *Conn) readLoop() {
	defer close(c.events)
	var f protocol.Frame
	for {
		if err := c.fr.ReadFrame(&f); err != nil {
			c.fail(err)
			return
		}
		switch f.Type {
		case protocol.FrameResp:
			c.deliver(f)
		case protocol.FrameErr:
			c.onErr(f)
		case protocol.FrameResync:
			var p protocol.ResyncPayload
			if json.Unmarshal(f.Payload, &p) != nil {
				continue
			}
			c.mu.Lock()
			c.seq[f.Pane] = f.Seq
			c.resubs[f.Pane] = 0
			c.mu.Unlock()
			if p.Exited {
				c.emit(Event{Pane: f.Pane, Kind: Exit, Screen: p})
			} else {
				c.emit(Event{Pane: f.Pane, Kind: Resync, Screen: p})
			}
		case protocol.FramePTY:
			var p protocol.PTYOutputPayload
			if json.Unmarshal(f.Payload, &p) != nil {
				continue
			}
			if !c.inOrder(f.Pane, f.Seq) {
				continue
			}
			c.emit(Event{Pane: f.Pane, Kind: Output, Data: append([]byte(nil), p.Data...)})
		}
		f = protocol.Frame{}
	}
}

// inOrder applies the contract's gap rule: a pane's seq counts up by one, and a
// gap means output was dropped, so the client re-subscribes (which yields a
// fresh Resync) instead of drawing a corrupted screen. Output of the gapped
// stream is discarded until that resync arrives.
func (c *Conn) inOrder(pane string, seq uint64) bool {
	c.mu.Lock()
	last, ok := c.seq[pane]
	sp, subscribed := c.subs[pane]
	if !ok || !subscribed {
		c.mu.Unlock()
		return false // output before the resync, or for a pane we left
	}
	if seq == last+1 {
		c.seq[pane] = seq
		c.mu.Unlock()
		return true
	}
	n := c.resubs[pane]
	delete(c.seq, pane)
	c.resubs[pane] = n + 1
	c.mu.Unlock()
	if n >= reconnectResubs {
		c.fail(errors.New("shareclient: the stream kept losing output"))
		return false
	}
	_ = c.sendSub(pane, sp)
	return false
}

func (c *Conn) deliver(f protocol.Frame) {
	c.mu.Lock()
	ch := c.pending[f.ID]
	c.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- f:
	default:
	}
}

func (c *Conn) onErr(f protocol.Frame) {
	var e protocol.ErrPayload
	_ = json.Unmarshal(f.Payload, &e)
	if f.ID != "" {
		c.mu.Lock()
		_, isReq := c.pending[f.ID]
		c.mu.Unlock()
		if isReq {
			c.deliver(f)
			return
		}
	}
	if f.Pane != "" {
		c.mu.Lock()
		delete(c.subs, f.Pane)
		delete(c.seq, f.Pane)
		c.mu.Unlock()
		c.emit(Event{Pane: f.Pane, Kind: Refused, Code: e.Code, Msg: e.Message})
		return
	}
	if e.Code == protocol.CodeNotPaired {
		c.fail(ErrNotReady)
	}
}
