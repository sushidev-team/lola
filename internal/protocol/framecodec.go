package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// The framing is a 4-byte big-endian length prefix followed by exactly that
// many bytes of JSON. This file is the only I/O in the package, and it is here
// rather than in internal/remote because both ends of the wire have to agree on
// it byte for byte and neither owns the other (see the note on Frame).
//
// Why a prefix rather than the newline-delimited JSON the unix socket uses.
// internal/daemon's handleConn reads with a bufio.Scanner capped at 1 MiB, and a
// scanner must BUFFER TO THE DELIMITER before it can know a message's size: an
// oversized frame is only detected after a megabyte has been read into a
// doubling buffer, and Scanner then returns ErrTooLong with no way to
// resynchronize. With a prefix the reader takes 4 bytes, compares against
// MaxFrameBytes, and refuses BEFORE ALLOCATING. That does not matter much when
// the peer is a local CLI; it matters here, where the peer is a device on a
// network. The prefix also makes each frame self-delimiting on the wire, so a
// short write is a detectable desync rather than two silently merged lines —
// which is what makes it safe for several goroutines to interleave replies and
// pane output onto one connection, keyed by Frame.ID.
//
// The prefix is the STREAM ADAPTER and it disappears over a message transport:
// on a WebSocket each frame is one binary message and the transport already
// frames it. Keeping the JSON body identical either way is what lets the client
// codec be written once.

var (
	// ErrFrameTooLarge is a length prefix (inbound) or an encoded body
	// (outbound) exceeding MaxFrameBytes. Inbound it is FATAL to the
	// connection: skipping the frame would mean reading the very bytes the
	// reader just refused to read, and a stream whose length prefix cannot be
	// trusted cannot be resynchronized. Write the refusal best-effort and close.
	// Outbound it is a bug in the daemon: log and DROP the frame, never
	// truncate it — a truncated resync renders a wrong screen, which is worse
	// than no screen at all.
	ErrFrameTooLarge = errors.New("protocol: frame exceeds the maximum frame size")

	// ErrFrameEmpty is a zero-length prefix. There is no empty frame in this
	// protocol — the envelope always carries at least V and Type — so a zero is
	// either a bug or a probe. Fatal to the connection, same reasoning as
	// ErrFrameTooLarge: nothing downstream can be trusted to be a frame boundary.
	ErrFrameEmpty = errors.New("protocol: zero-length frame")

	// ErrFrameMalformed is a body that is not a decodable envelope. The framing
	// itself was intact, so the connection MAY survive when the caller can tell
	// which frame it was (a bad payload on a good envelope is answered on that
	// frame's ID); a malformed ENVELOPE has no id to answer on and is closed.
	// The wrapped text is for the local log only: it must never be echoed to the
	// peer, which gets CodeInternal and a fixed message.
	ErrFrameMalformed = errors.New("protocol: malformed frame")

	errNilFrame = errors.New("protocol: ReadFrame into a nil Frame")
)

// FrameReader reads length-prefixed frames from a stream. It is NOT safe for
// concurrent use: one reader goroutine per connection, which is the shape the
// correlation id exists to make sufficient.
//
// It keeps ONE body buffer and reuses it across calls, so after the first frame
// of a given size the hot path allocates nothing for the body itself. The buffer
// grows only to the largest frame actually seen and can never exceed
// MaxFrameBytes, because the size is checked against that cap before a single
// byte is allocated.
type FrameReader struct {
	r   io.Reader
	hdr [FrameHeaderBytes]byte
	buf []byte
}

// NewFrameReader wraps r. Callers on a network connection are expected to set a
// read deadline on the connection itself and refresh it per frame; the codec
// deliberately owns no timeouts, because the right deadline differs between a
// request stream and an attached pane.
func NewFrameReader(r io.Reader) *FrameReader { return &FrameReader{r: r} }

// ReadFrame decodes the next frame into dst.
//
// dst is RESET FIRST — every field zeroed, the Payload's backing array kept for
// reuse — because encoding/json leaves struct fields the JSON does not mention
// untouched. Reusing a Frame without that reset is how a previous frame's Cmd or
// Pane silently becomes this frame's, which is a fail-OPEN authorization bug
// rather than a cosmetic one.
//
// The returned Payload aliases dst's own buffer and is therefore only valid
// until the next ReadFrame into the SAME dst: a caller that hands the payload to
// another goroutine must copy it, or decode it before reading on.
//
// Errors: io.EOF exactly when the peer closed cleanly on a frame boundary;
// io.ErrUnexpectedEOF for a truncated header or body; ErrFrameEmpty and
// ErrFrameTooLarge for a length prefix that cannot be honoured (both fatal to
// the connection); ErrFrameMalformed for a body that is not an envelope. Nothing
// here validates V or Type — that is dispatch policy, and it lives with the
// authorizer that also needs the frame's identity. Use SupportedFrameVersion and
// DaemonAcceptsFrame / ClientAcceptsFrame for it.
func (fr *FrameReader) ReadFrame(dst *Frame) error {
	if dst == nil {
		return errNilFrame
	}
	if _, err := io.ReadFull(fr.r, fr.hdr[:]); err != nil {
		// io.ReadFull reports a clean close as io.EOF and a partial header as
		// io.ErrUnexpectedEOF; both are passed through unchanged so the caller
		// can tell "peer went away" from "peer went away mid-frame".
		return err
	}
	n := binary.BigEndian.Uint32(fr.hdr[:])
	if n == 0 {
		return ErrFrameEmpty
	}
	if n > MaxFrameBytes {
		return fmt.Errorf("%w: %d bytes (max %d)", ErrFrameTooLarge, n, MaxFrameBytes)
	}
	if cap(fr.buf) < int(n) {
		fr.buf = make([]byte, n)
	}
	body := fr.buf[:n]
	if _, err := io.ReadFull(fr.r, body); err != nil {
		if errors.Is(err, io.EOF) {
			// A prefix promised n bytes and the stream ended short. That is a
			// truncated frame, never a clean close.
			return io.ErrUnexpectedEOF
		}
		return err
	}
	*dst = Frame{Payload: dst.Payload[:0]}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("%w: %w", ErrFrameMalformed, err)
	}
	return nil
}

// FrameWriter writes length-prefixed frames to a stream. Unlike FrameReader it
// IS safe for concurrent use, and it has to be: replies, pane output and
// resyncs are produced by different goroutines and share one connection.
type FrameWriter struct {
	w  io.Writer
	mu sync.Mutex
}

// NewFrameWriter wraps w.
func NewFrameWriter(w io.Writer) *FrameWriter { return &FrameWriter{w: w} }

// WriteFrame encodes f and writes it as one length-prefixed message.
//
// The body is marshalled OUTSIDE the lock, so a slow encode never blocks the
// other goroutines sharing this connection, and the prefix is copied in front of
// it so the whole frame reaches the transport in ONE Write — a separate 4-byte
// write would put a header in its own TCP segment and interact badly with Nagle
// on the exact stream that carries keystrokes. The cost is one extra copy per
// frame, which at a coalesced pane's frame rate is not measurable; the
// allocation that actually mattered was the reader's doubling buffer, and the
// length prefix is what removes it.
//
// A body over MaxFrameBytes returns ErrFrameTooLarge and writes NOTHING.
// Truncating is never an option: half a resync paints a wrong screen.
func (fw *FrameWriter) WriteFrame(f *Frame) error {
	body, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("protocol: encode frame: %w", err)
	}
	if len(body) > MaxFrameBytes {
		return fmt.Errorf("%w: %d bytes (max %d)", ErrFrameTooLarge, len(body), MaxFrameBytes)
	}
	out := make([]byte, FrameHeaderBytes+len(body))
	binary.BigEndian.PutUint32(out, uint32(len(body)))
	copy(out[FrameHeaderBytes:], body)

	fw.mu.Lock()
	defer fw.mu.Unlock()
	_, err = fw.w.Write(out)
	return err
}
