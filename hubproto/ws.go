package hubproto

// RFC 6455 (version 13) WebSocket codec for the relay: binary frames only, no
// extensions, no reassembly buffer. Stdlib only. The hub uses the server role,
// the daemon the client role. See docs/mobile/03-hub-relay.md section 2.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// WSRole says which side of the connection this end is.
type WSRole uint8

const (
	WSClient WSRole = iota + 1 // masks every frame it sends; refuses masked frames
	WSServer                   // never masks; refuses unmasked frames
)

// Constants of the codec.
const (
	// WSSubprotocol is required in Sec-WebSocket-Protocol; it versions the relay.
	WSSubprotocol = "lola-relay.v1"
	// WSMaxFramePayload is the read cap per frame; a bigger frame closes 1009.
	WSMaxFramePayload = 64 << 10
	// WSWriteChunk bounds every outgoing data frame (matches the TLS record size).
	WSWriteChunk = 16 << 10
	// WSMaxControlPayload is RFC 6455 section 5.5.
	WSMaxControlPayload = 125
	// WSHandshakeTimeout bounds the daemon's dial plus upgrade.
	WSHandshakeTimeout = 15 * time.Second
	// WSControlWriteTimeout is the deadline of every control-frame write and
	// of taking the write lock for one.
	WSControlWriteTimeout = 2 * time.Second
	// WSCloseTimeout is the wait for the peer's Close after sending ours.
	WSCloseTimeout = 2 * time.Second
	// WSMaxHandshakeResponse caps the body of a non-101 answer.
	WSMaxHandshakeResponse = 4 << 10
	// WSMaxHandshakeHead caps everything a dial reads before the upgrade is
	// accepted: status line, headers and a non-101 answer's body together.
	// net/http's own reader has no bound once http.Transport is not in the way.
	WSMaxHandshakeHead = 16 << 10
	// WSMaxControlFramesPerSec caps control frames (ping, pong, close)
	// received per one-second window; above it the conn closes 1008.
	WSMaxControlFramesPerSec = 4
	// WSMaxEmptyFramesInARow caps consecutive zero-payload data frames.
	WSMaxEmptyFramesInARow = 16
)

// Close codes the codec itself uses (RFC 6455 section 7.4.1). The relay's
// 4000-4004 codes live in relay.go.
const (
	WSCloseNormal      uint16 = 1000
	WSCloseGoingAway   uint16 = 1001
	WSCloseProtocol    uint16 = 1002
	WSCloseUnsupported uint16 = 1003
	WSClosePolicy      uint16 = 1008
	WSCloseTooBig      uint16 = 1009
	WSCloseInternal    uint16 = 1011
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// wsCloseNoStatus is the code recorded for a Close without a payload.
const wsCloseNoStatus uint16 = 1005

// WebSocket opcodes.
const (
	opCont   = 0x0
	opText   = 0x1
	opBinary = 0x2
	opClose  = 0x8
	opPing   = 0x9
	opPong   = 0xA
)

var (
	ErrWSHandshake = errors.New("hubproto: bad websocket handshake")
	ErrWSProtocol  = errors.New("hubproto: websocket protocol violation")
	ErrWSTooBig    = errors.New("hubproto: websocket frame too large")
	ErrWSClosed    = errors.New("hubproto: websocket closed")
	ErrWSBroken    = errors.New("hubproto: websocket stream broken by a failed write")
)

// WSConfig tunes one connection. The zero value is valid.
type WSConfig struct {
	MaxFramePayload int           // 0 = WSMaxFramePayload; larger frame closes 1009
	PingInterval    time.Duration // 0 = send no pings
	DeadAfter       time.Duration // 0 = no watchdog; else close if NOTHING was received this long
	WriteStall      time.Duration // 0 = none; bound on a data write when the caller set no write deadline
	// OnFrame, when set, is called by the reading goroutine for EVERY frame
	// read (any opcode), with its wire size (header + payload), after the
	// header is parsed and before the payload is consumed. It must not block.
	OnFrame func(wire int)
}

// WSConn is a WebSocket carrying an opaque byte stream. It implements
// net.Conn. One goroutine may Read while others Write; writes (data and
// control) are serialized by wsem.
//
// A data leg uses Read/Write only; the control channel uses
// ReadMessage/WriteMessage only. Mixing them on one conn is a programming
// error (documented, not checked).
type WSConn struct {
	nc   net.Conn
	br   *bufio.Reader
	role WSRole
	cfg  WSConfig

	wsem chan struct{} // cap 1: the write lock, acquirable with a timeout
	wdl  atomic.Int64  // caller's write deadline, unix nanos; 0 = none

	// Guarded by wsem.
	wbuf    []byte
	maskBuf [4096]byte
	maskOff int

	// Guarded by rmu.
	rmu         sync.Mutex
	remaining   int64
	maskKey     [4]byte
	masked      bool
	maskPos     int
	fragmented  bool
	emptyRun    int
	ctlStart    time.Time
	ctlInWindow int
	rerr        error // sticky read-side result (EOF, violation, broken)

	broken      atomic.Bool
	closeSent   atomic.Bool
	closeRecv   atomic.Bool
	closeRecvCh chan struct{}
	peerCode    atomic.Int32 // 0 = none yet
	lastRx      atomic.Int64
	done        chan struct{}
	finishOnce  sync.Once
}

// NewWSConn wraps an upgraded connection. br holds any bytes already read
// from nc (it may be nil, meaning a fresh reader over nc).
func NewWSConn(nc net.Conn, br *bufio.Reader, role WSRole, cfg WSConfig) *WSConn {
	if br == nil {
		br = bufio.NewReader(nc)
	}
	if cfg.MaxFramePayload <= 0 {
		cfg.MaxFramePayload = WSMaxFramePayload
	}
	c := &WSConn{
		nc: nc, br: br, role: role, cfg: cfg,
		wsem:        make(chan struct{}, 1),
		closeRecvCh: make(chan struct{}),
		done:        make(chan struct{}),
	}
	c.maskOff = len(c.maskBuf) // force a fill on first use
	c.lastRx.Store(time.Now().UnixNano())
	if cfg.PingInterval > 0 {
		go c.pinger(cfg.PingInterval)
	}
	if cfg.DeadAfter > 0 {
		go c.watchdog(cfg.DeadAfter)
	}
	return c
}

// ---- net.Conn ----

func (c *WSConn) LocalAddr() net.Addr  { return c.nc.LocalAddr() }
func (c *WSConn) RemoteAddr() net.Addr { return c.nc.RemoteAddr() }

func (c *WSConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

// SetReadDeadline goes to nc: only Read/ReadMessage read nc, so it bounds
// exactly "waiting for the peer".
func (c *WSConn) SetReadDeadline(t time.Time) error { return c.nc.SetReadDeadline(t) }

// SetWriteDeadline is STORED, never passed to nc: it governs data writes
// only, so an idle conn can still answer pings (section 2.6).
func (c *WSConn) SetWriteDeadline(t time.Time) error {
	if t.IsZero() {
		c.wdl.Store(0)
	} else {
		c.wdl.Store(t.UnixNano())
	}
	return nil
}

// Close is CloseWithCode(WSCloseNormal).
func (c *WSConn) Close() error { return c.CloseWithCode(WSCloseNormal) }

// PeerCloseCode reports the code of the peer's Close frame (1005 when it
// carried none) and whether one arrived.
func (c *WSConn) PeerCloseCode() (uint16, bool) {
	v := c.peerCode.Load()
	if v == 0 {
		return 0, false
	}
	return uint16(v), true
}

// ---- reading ----

type wsFrame struct {
	fin    bool
	op     byte
	n      int64
	masked bool
	key    [4]byte
	hdrLen int
}

func (c *WSConn) touchRx() { c.lastRx.Store(time.Now().UnixNano()) }

// mapReadErr turns a transport error into the conn's answer.
func (c *WSConn) mapReadErr(err error) error {
	if isTimeout(err) {
		select {
		case <-c.done:
		default:
			return err
		}
	}
	if c.closeRecv.Load() {
		return io.EOF
	}
	select {
	case <-c.done:
		return ErrWSClosed
	default:
	}
	return err
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// breakRead marks the stream unusable after a partial frame was consumed.
func (c *WSConn) breakRead(err error) error {
	c.broken.Store(true)
	err = c.mapReadErr(err)
	if errors.Is(err, io.EOF) && !c.closeRecv.Load() {
		err = io.ErrUnexpectedEOF
	}
	c.rerr = err
	_ = c.nc.Close()
	return err
}

// fail sends Close(code) best effort and makes err the sticky read result.
func (c *WSConn) fail(code uint16, err error, why string) error {
	_ = c.CloseNow(code)
	c.rerr = fmt.Errorf("%w: %s", err, why)
	return c.rerr
}

// readHeader parses one frame header. A failure before the first byte is
// returned raw (a read timeout there leaves the stream intact).
func (c *WSConn) readHeader() (wsFrame, error) {
	var f wsFrame
	b0, err := c.br.ReadByte()
	if err != nil {
		return f, c.mapReadErr(err)
	}
	var hb [8]byte
	if _, err := io.ReadFull(c.br, hb[:1]); err != nil {
		return f, c.breakRead(err)
	}
	b1 := hb[0]
	c.touchRx()
	f.fin = b0&0x80 != 0
	f.op = b0 & 0x0f
	f.hdrLen = 2
	if b0&0x70 != 0 {
		return f, c.fail(WSCloseProtocol, ErrWSProtocol, "reserved bits set")
	}
	switch f.op {
	case opCont, opText, opBinary, opClose, opPing, opPong:
	default:
		return f, c.fail(WSCloseProtocol, ErrWSProtocol, "unknown opcode")
	}
	f.masked = b1&0x80 != 0
	if f.masked != (c.role == WSServer) {
		return f, c.fail(WSCloseProtocol, ErrWSProtocol, "wrong mask bit")
	}
	l := int64(b1 & 0x7f)
	switch l {
	case 126:
		if _, err := io.ReadFull(c.br, hb[:2]); err != nil {
			return f, c.breakRead(err)
		}
		l = int64(binary.BigEndian.Uint16(hb[:2]))
		f.hdrLen += 2
		if l < 126 {
			return f, c.fail(WSCloseProtocol, ErrWSProtocol, "non-minimal length")
		}
	case 127:
		if _, err := io.ReadFull(c.br, hb[:8]); err != nil {
			return f, c.breakRead(err)
		}
		u := binary.BigEndian.Uint64(hb[:8])
		f.hdrLen += 8
		if u>>63 != 0 {
			return f, c.fail(WSCloseProtocol, ErrWSProtocol, "length high bit set")
		}
		if u < 65536 {
			return f, c.fail(WSCloseProtocol, ErrWSProtocol, "non-minimal length")
		}
		if u > uint64(c.cfg.MaxFramePayload) {
			return f, c.fail(WSCloseTooBig, ErrWSTooBig, "frame over the cap")
		}
		l = int64(u)
	}
	if f.masked {
		if _, err := io.ReadFull(c.br, f.key[:]); err != nil {
			return f, c.breakRead(err)
		}
		f.hdrLen += 4
	}
	f.n = l
	if f.op >= opClose {
		if !f.fin || l > WSMaxControlPayload {
			return f, c.fail(WSCloseProtocol, ErrWSProtocol, "bad control frame")
		}
	} else if l > int64(c.cfg.MaxFramePayload) {
		return f, c.fail(WSCloseTooBig, ErrWSTooBig, "frame over the cap")
	}
	if f.op == opText {
		return f, c.fail(WSCloseUnsupported, ErrWSProtocol, "text frame")
	}
	if c.cfg.OnFrame != nil {
		c.cfg.OnFrame(f.hdrLen + int(f.n))
	}
	return f, nil
}

func xorMask(key [4]byte, pos int, b []byte) {
	for i := range b {
		b[i] ^= key[(pos+i)&3]
	}
}

// ctlFlood counts one received control frame against the per-second window.
func (c *WSConn) ctlFlood() bool {
	now := time.Now()
	if c.ctlStart.IsZero() || now.Sub(c.ctlStart) >= time.Second {
		c.ctlStart, c.ctlInWindow = now, 0
	}
	c.ctlInWindow++
	return c.ctlInWindow > WSMaxControlFramesPerSec
}

// nextFrame returns the next DATA frame header, handling control frames on
// the way. msg selects ReadMessage rules (no fragments). Caller holds rmu.
func (c *WSConn) nextFrame(msg bool) (wsFrame, error) {
	for {
		f, err := c.readHeader()
		if err != nil {
			return f, err
		}
		if f.op >= opClose {
			if c.ctlFlood() {
				return f, c.fail(WSClosePolicy, ErrWSProtocol, "control frame flood")
			}
			var pl [WSMaxControlPayload]byte
			p := pl[:f.n]
			if _, err := io.ReadFull(c.br, p); err != nil {
				return f, c.breakRead(err)
			}
			c.touchRx()
			if f.masked {
				xorMask(f.key, 0, p)
			}
			switch f.op {
			case opPing:
				c.ctlWrite(opPong, p)
			case opPong:
			case opClose:
				code := wsCloseNoStatus
				if len(p) >= 2 {
					code = binary.BigEndian.Uint16(p)
				}
				c.peerCode.Store(int32(code))
				if c.closeRecv.CompareAndSwap(false, true) {
					close(c.closeRecvCh)
				}
				if c.closeSent.CompareAndSwap(false, true) {
					c.ctlWriteClose(code)
					if c.role == WSServer {
						c.finish()
					}
				}
				c.rerr = io.EOF
				return f, io.EOF
			}
			continue
		}
		// Data frame.
		switch {
		case msg && f.op == opBinary && !f.fin:
			return f, c.fail(WSCloseTooBig, ErrWSTooBig, "fragmented message")
		case f.op == opCont && (msg || !c.fragmented):
			return f, c.fail(WSCloseProtocol, ErrWSProtocol, "unexpected continuation")
		case f.op == opBinary && c.fragmented:
			return f, c.fail(WSCloseProtocol, ErrWSProtocol, "new message inside a fragment")
		}
		c.fragmented = !f.fin
		if f.n == 0 {
			c.emptyRun++
			if c.emptyRun > WSMaxEmptyFramesInARow {
				return f, c.fail(WSCloseProtocol, ErrWSProtocol, "empty frame flood")
			}
			if msg {
				return f, nil
			}
			continue
		}
		c.emptyRun = 0
		return f, nil
	}
}

// Read returns payload bytes of binary and continuation frames. A frame's
// payload is streamed as it arrives. A read timeout between frames is
// resumable; one inside a frame (header or payload partly read) breaks the
// stream.
func (c *WSConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for {
		if c.rerr != nil {
			return 0, c.rerr
		}
		if c.remaining > 0 {
			want := len(p)
			if int64(want) > c.remaining {
				want = int(c.remaining)
			}
			n, err := c.br.Read(p[:want])
			if n > 0 {
				c.touchRx()
				if c.masked {
					xorMask(c.maskKey, c.maskPos, p[:n])
					c.maskPos += n
				}
				c.remaining -= int64(n)
				return n, nil
			}
			if err != nil {
				// A timeout here leaves a frame half consumed: never resumed.
				return 0, c.breakRead(err)
			}
			continue
		}
		f, err := c.nextFrame(false)
		if err != nil {
			return 0, err
		}
		c.remaining, c.masked, c.maskKey, c.maskPos = f.n, f.masked, f.key, 0
	}
}

// ReadMessage reads one FIN binary frame of at most limit bytes. Control
// channel only.
func (c *WSConn) ReadMessage(limit int) ([]byte, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	if c.rerr != nil {
		return nil, c.rerr
	}
	if c.remaining > 0 {
		return nil, ErrWSProtocol
	}
	f, err := c.nextFrame(true)
	if err != nil {
		return nil, err
	}
	if f.n > int64(limit) {
		return nil, c.fail(WSCloseTooBig, ErrWSTooBig, "message over the limit")
	}
	out := make([]byte, f.n)
	if _, err := io.ReadFull(c.br, out); err != nil {
		return nil, c.breakRead(err)
	}
	c.touchRx()
	if f.masked {
		xorMask(f.key, 0, out)
	}
	return out, nil
}

// ---- writing ----

// lockCtl takes the write lock for a control frame, bounded.
func (c *WSConn) lockCtl() bool {
	select {
	case c.wsem <- struct{}{}:
		return true
	default:
	}
	t := time.NewTimer(WSControlWriteTimeout)
	defer t.Stop()
	select {
	case c.wsem <- struct{}{}:
		return true
	case <-t.C:
		return false
	case <-c.done:
		return false
	}
}

func (c *WSConn) unlock() { <-c.wsem }

// frame encodes one FIN frame into the scratch buffer. Caller holds wsem.
func (c *WSConn) frame(op byte, payload []byte) []byte {
	need := 14 + len(payload)
	if cap(c.wbuf) < need {
		c.wbuf = make([]byte, 0, need)
	}
	b := c.wbuf[:0]
	b = append(b, 0x80|op)
	var mbit byte
	if c.role == WSClient {
		mbit = 0x80
	}
	switch n := len(payload); {
	case n < 126:
		b = append(b, mbit|byte(n))
	case n < 65536:
		b = append(b, mbit|126, byte(n>>8), byte(n))
	default:
		b = append(b, mbit|127, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	if c.role == WSClient {
		if c.maskOff+4 > len(c.maskBuf) {
			if _, err := rand.Read(c.maskBuf[:]); err != nil {
				panic("hubproto: crypto/rand failed")
			}
			c.maskOff = 0
		}
		var key [4]byte
		copy(key[:], c.maskBuf[c.maskOff:c.maskOff+4])
		c.maskOff += 4
		b = append(b, key[:]...)
		start := len(b)
		b = append(b, payload...)
		xorMask(key, 0, b[start:])
	} else {
		b = append(b, payload...)
	}
	c.wbuf = b
	return b
}

// writeRaw writes an encoded frame; any error may have left a partial frame,
// so the stream is broken for good. Caller holds wsem and set the deadline.
func (c *WSConn) writeRaw(b []byte) error {
	if _, err := c.nc.Write(b); err != nil {
		c.broken.Store(true)
		_ = c.nc.Close()
		return err
	}
	return nil
}

// ctlWrite sends a pong or ping under its own deadline; skipped when the
// lock is not free in time or a Close already went out.
func (c *WSConn) ctlWrite(op byte, payload []byte) {
	if c.closeSent.Load() || c.broken.Load() || !c.lockCtl() {
		return
	}
	defer c.unlock()
	_ = c.nc.SetWriteDeadline(time.Now().Add(WSControlWriteTimeout))
	_ = c.writeRaw(c.frame(op, payload))
}

// ctlWriteClose sends the Close frame (the caller already won closeSent).
func (c *WSConn) ctlWriteClose(code uint16) bool {
	if c.broken.Load() {
		return false
	}
	_ = c.nc.SetWriteDeadline(time.Now().Add(WSControlWriteTimeout))
	if !c.lockCtl() {
		return false
	}
	defer c.unlock()
	// 1005 means "no code" and never goes on the wire (RFC 6455 section
	// 7.4.1): echoing a code-less Close is a code-less Close.
	if code == wsCloseNoStatus {
		return c.writeRaw(c.frame(opClose, nil)) == nil
	}
	var pl [2]byte
	binary.BigEndian.PutUint16(pl[:], code)
	return c.writeRaw(c.frame(opClose, pl[:])) == nil
}

// dataWrite writes one frame under the caller's (or stall) deadline.
func (c *WSConn) dataWrite(op byte, payload []byte) error {
	if c.broken.Load() {
		return ErrWSBroken
	}
	if c.closeSent.Load() {
		return ErrWSClosed
	}
	select {
	case c.wsem <- struct{}{}:
	case <-c.done:
		return ErrWSClosed
	}
	defer c.unlock()
	if c.broken.Load() {
		return ErrWSBroken
	}
	if c.closeSent.Load() {
		return ErrWSClosed
	}
	var dl time.Time
	if v := c.wdl.Load(); v != 0 {
		dl = time.Unix(0, v)
		if !time.Now().Before(dl) {
			// Nothing written yet: the stream stays intact.
			return os.ErrDeadlineExceeded
		}
	} else if c.cfg.WriteStall > 0 {
		dl = time.Now().Add(c.cfg.WriteStall)
	}
	_ = c.nc.SetWriteDeadline(dl)
	return c.writeRaw(c.frame(op, payload))
}

// Write splits p into FIN binary frames of at most WSWriteChunk.
func (c *WSConn) Write(p []byte) (int, error) {
	if c.broken.Load() {
		return 0, ErrWSBroken
	}
	written := 0
	for len(p) > 0 {
		n := len(p)
		if n > WSWriteChunk {
			n = WSWriteChunk
		}
		if err := c.dataWrite(opBinary, p[:n]); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

// WriteMessage sends p as one FIN binary frame. Control channel only.
func (c *WSConn) WriteMessage(p []byte) error {
	if len(p) > RelayMaxControlMessage {
		return ErrWSTooBig
	}
	return c.dataWrite(opBinary, p)
}

// ---- close ----

func (c *WSConn) finish() {
	c.finishOnce.Do(func() {
		_ = c.nc.Close()
		close(c.done)
	})
}

// closeFrame runs steps 1-3 of section 2.7. It reports whether a Close frame
// went out and whether this call was the one to send it.
func (c *WSConn) closeFrame(code uint16) (sent bool, first bool) {
	if !c.closeSent.CompareAndSwap(false, true) {
		return false, false
	}
	// Fail a data write parked toward a peer that stopped reading, which
	// frees the write lock; deliberately without taking it.
	_ = c.nc.SetWriteDeadline(time.Now().Add(WSControlWriteTimeout))
	return c.ctlWriteClose(code), true
}

// CloseWithCode sends Close(code) (never a reason), waits for the peer's
// Close, then closes the conn. Bounded by WSControlWriteTimeout +
// WSCloseTimeout whatever the peer and any writer do.
func (c *WSConn) CloseWithCode(code uint16) error {
	sent, first := c.closeFrame(code)
	if !first {
		if !c.closeRecv.Load() {
			t := time.NewTimer(WSControlWriteTimeout + WSCloseTimeout)
			select {
			case <-c.done:
			case <-t.C:
			}
			t.Stop()
		}
		c.finish()
		return nil
	}
	if sent {
		t := time.NewTimer(WSCloseTimeout)
		select {
		case <-c.closeRecvCh:
		case <-t.C:
		case <-c.done:
		}
		t.Stop()
	}
	c.finish()
	return nil
}

// CloseNow sends a best-effort Close and closes the conn without waiting.
func (c *WSConn) CloseNow(code uint16) error {
	c.closeFrame(code)
	c.finish()
	return nil
}

// CloseWrite sends Close and shuts the write side; Read keeps working until
// the peer's Close or EOF.
func (c *WSConn) CloseWrite() error {
	if c.closeSent.CompareAndSwap(false, true) {
		c.ctlWriteClose(WSCloseNormal)
	}
	return nil
}

func (c *WSConn) pinger(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			c.ctlWrite(opPing, nil)
		}
	}
}

func (c *WSConn) watchdog(after time.Duration) {
	step := after / 4
	if step < time.Millisecond {
		step = time.Millisecond
	}
	t := time.NewTicker(step)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			if time.Since(time.Unix(0, c.lastRx.Load())) > after {
				c.broken.Store(true)
				c.finish()
				return
			}
		}
	}
}

// ---- server handshake ----

func headerHasToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, t := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

func acceptKey(key string) string {
	sum := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func hsErr(what string) error { return fmt.Errorf("%w: %s", ErrWSHandshake, what) }

// CheckUpgrade validates a WebSocket upgrade request and writes nothing. The
// errors name the header, never its value.
func CheckUpgrade(r *http.Request) error {
	if r.Method != http.MethodGet {
		return hsErr("method")
	}
	if r.ProtoMajor != 1 || !r.ProtoAtLeast(1, 1) {
		return hsErr("protocol version")
	}
	if !headerHasToken(r.Header, "Upgrade", "websocket") {
		return hsErr("Upgrade header")
	}
	if !headerHasToken(r.Header, "Connection", "upgrade") {
		return hsErr("Connection header")
	}
	if v := r.Header.Values("Sec-WebSocket-Version"); len(v) != 1 || strings.TrimSpace(v[0]) != "13" {
		return hsErr("Sec-WebSocket-Version header")
	}
	k := r.Header.Values("Sec-WebSocket-Key")
	if len(k) != 1 {
		return hsErr("Sec-WebSocket-Key header")
	}
	if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(k[0])); err != nil || len(raw) != 16 {
		return hsErr("Sec-WebSocket-Key header")
	}
	if !headerHasToken(r.Header, "Sec-WebSocket-Protocol", WSSubprotocol) {
		return hsErr("Sec-WebSocket-Protocol header")
	}
	// Daemons and phones never send Origin; a browser always does. Refusing
	// it rules out cross-site WebSocket hijacking by construction.
	if _, ok := r.Header["Origin"]; ok {
		return hsErr("Origin header")
	}
	return nil
}

// AcceptWebSocket upgrades r. On error before the hijack nothing has been
// written and the caller answers; after it the connection is closed.
func AcceptWebSocket(w http.ResponseWriter, r *http.Request, cfg WSConfig) (*WSConn, error) {
	if err := CheckUpgrade(r); err != nil {
		return nil, err
	}
	nc, brw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return nil, fmt.Errorf("%w: hijack unavailable", ErrWSHandshake)
	}
	// net/http armed its read/write timeouts before the handler ran and a
	// hijack does not clear them.
	_ = nc.SetDeadline(time.Time{})
	if brw.Reader.Buffered() > 0 {
		_ = nc.Close()
		return nil, hsErr("data before the upgrade answer")
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + acceptKey(key) + "\r\n" +
		"Sec-WebSocket-Protocol: " + WSSubprotocol + "\r\n\r\n"
	_ = nc.SetWriteDeadline(time.Now().Add(WSControlWriteTimeout))
	if _, err := io.WriteString(nc, resp); err != nil {
		_ = nc.Close()
		return nil, fmt.Errorf("%w: writing the answer failed", ErrWSHandshake)
	}
	_ = nc.SetWriteDeadline(time.Time{})
	return NewWSConn(nc, brw.Reader, WSServer, cfg), nil
}

// ---- client handshake ----

// WSDialer opens client-role connections.
type WSDialer struct {
	TLSConfig        *tls.Config                           // nil = system roots; NextProtos forced to ["http/1.1"]
	Proxy            func(*http.Request) (*url.URL, error) // nil = http.ProxyFromEnvironment
	HandshakeTimeout time.Duration                         // 0 = WSHandshakeTimeout
	Config           WSConfig
}

// WSHandshakeError is a non-101 answer. Code and RetryAfter are RAW hub
// values: hubproto does not bound them. internal/hub converts this error
// before anything else sees it.
type WSHandshakeError struct {
	Status     int
	Code       string        // ErrorResponse.code, or the Lola-Relay-Code header when the body is absent; "" otherwise
	RetryAfter time.Duration // from Retry-After seconds, 0 = none
}

func (e *WSHandshakeError) Error() string {
	return "hubproto: websocket handshake refused: " + strconv.Itoa(e.Status)
}

func isLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func hostPort(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// proxyConnect tunnels through an http proxy with CONNECT. The response is
// read byte by byte so nothing past the proxy's headers is consumed.
func proxyConnect(conn net.Conn, proxy *url.URL, target string) error {
	var sb strings.Builder
	sb.WriteString("CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n")
	if proxy.User != nil {
		pw, _ := proxy.User.Password()
		sb.WriteString("Proxy-Authorization: Basic " +
			base64.StdEncoding.EncodeToString([]byte(proxy.User.Username()+":"+pw)) + "\r\n")
	}
	sb.WriteString("\r\n")
	if _, err := io.WriteString(conn, sb.String()); err != nil {
		return hsErr("proxy write")
	}
	var head []byte
	one := make([]byte, 1)
	for len(head) < 4<<10 {
		if _, err := conn.Read(one); err != nil {
			return hsErr("proxy read")
		}
		head = append(head, one[0])
		if n := len(head); n >= 4 && string(head[n-4:]) == "\r\n\r\n" {
			line := string(head[:strings.Index(string(head), "\r\n")])
			f := strings.Fields(line)
			if len(f) < 2 || f[1] != "200" {
				return hsErr("proxy refused the tunnel")
			}
			return nil
		}
	}
	return hsErr("proxy answer too large")
}

// Dial performs the upgrade for a fully built GET request (headers, signature
// and query already set by the caller). It adds the WebSocket headers, never
// Origin.
func (d WSDialer) Dial(ctx context.Context, req *http.Request) (*WSConn, error) {
	if req.URL == nil || (req.URL.Scheme != "https" && req.URL.Scheme != "http") {
		return nil, hsErr("url scheme")
	}
	if req.URL.Scheme == "http" && !isLoopbackHost(req.URL.Hostname()) {
		return nil, hsErr("plain http to a non-loopback host")
	}
	timeout := d.HandshakeTimeout
	if timeout <= 0 {
		timeout = WSHandshakeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()

	var rawKey [16]byte
	if _, err := rand.Read(rawKey[:]); err != nil {
		return nil, hsErr("random key")
	}
	key := base64.StdEncoding.EncodeToString(rawKey[:])
	r := req.Clone(ctx)
	r.Method = http.MethodGet
	r.Body, r.ContentLength = nil, 0
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", key)
	r.Header.Set("Sec-WebSocket-Protocol", WSSubprotocol)
	r.Header.Del("Origin")

	proxyFn := d.Proxy
	if proxyFn == nil {
		proxyFn = http.ProxyFromEnvironment
	}
	proxy, err := proxyFn(r)
	if err != nil {
		return nil, hsErr("proxy lookup")
	}
	dialAddr := hostPort(r.URL)
	if proxy != nil {
		if proxy.Scheme != "http" {
			return nil, hsErr("proxy scheme")
		}
		dialAddr = hostPort(&url.URL{Scheme: "http", Host: proxy.Host})
	}
	nd := net.Dialer{KeepAlive: 30 * time.Second}
	conn, err := nd.DialContext(ctx, "tcp", dialAddr)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	ok := false
	defer func() {
		if !ok {
			stop()
			_ = conn.Close()
		}
	}()
	_ = conn.SetDeadline(deadline)
	if proxy != nil {
		if err := proxyConnect(conn, proxy, hostPort(r.URL)); err != nil {
			return nil, err
		}
	}
	var wire net.Conn = conn
	if r.URL.Scheme == "https" {
		var cfg *tls.Config
		if d.TLSConfig != nil {
			cfg = d.TLSConfig.Clone()
		} else {
			cfg = &tls.Config{}
		}
		if cfg.ServerName == "" {
			cfg.ServerName = r.URL.Hostname()
		}
		if cfg.MinVersion < tls.VersionTLS12 {
			cfg.MinVersion = tls.VersionTLS12
		}
		// ingress-nginx would negotiate h2, where an Upgrade does not exist.
		cfg.NextProtos = []string{"http/1.1"}
		tc := tls.Client(conn, cfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		wire = tc
	}
	if err := r.Write(wire); err != nil {
		return nil, err
	}
	// Every byte read before the upgrade is accepted goes through lim, so a
	// hub that answers with an endless header line or body costs this process
	// WSMaxHandshakeHead bytes, not memory until the deadline. The WSConn keeps
	// reading through the same bufio.Reader, so the limit is lifted only once
	// the answer has been accepted.
	lim := &io.LimitedReader{R: wire, N: WSMaxHandshakeHead}
	br := bufio.NewReader(lim)
	resp, err := http.ReadResponse(br, r)
	if err != nil {
		if lim.N <= 0 {
			return nil, hsErr("answer too large")
		}
		return nil, err
	}
	// The body is never Closed: Close drains a chunked body to its end. What is
	// read of it is bounded by lim, and the connection is closed on every
	// refusal below.
	if resp.StatusCode != http.StatusSwitchingProtocols {
		he := &WSHandshakeError{Status: resp.StatusCode}
		var er ErrorResponse
		if DecodeStrict(io.LimitReader(resp.Body, WSMaxHandshakeResponse), &er) == nil {
			he.Code = er.Code
		}
		if he.Code == "" {
			he.Code = resp.Header.Get("Lola-Relay-Code")
		}
		if s, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && s > 0 {
			he.RetryAfter = time.Duration(s) * time.Second
		}
		return nil, he
	}
	if !strings.EqualFold(strings.TrimSpace(resp.Header.Get("Upgrade")), "websocket") ||
		!headerHasToken(resp.Header, "Connection", "upgrade") ||
		resp.Header.Get("Sec-WebSocket-Accept") != acceptKey(key) ||
		resp.Header.Get("Sec-WebSocket-Protocol") != WSSubprotocol {
		return nil, hsErr("upgrade answer")
	}
	stop()
	_ = conn.SetDeadline(time.Time{})
	lim.N = math.MaxInt64
	ok = true
	return NewWSConn(wire, br, WSClient, d.Config), nil
}
