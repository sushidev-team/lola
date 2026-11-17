package sshstore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshelement"
)

// TailBytes is how much of a stream's end survives past the output cap.
const TailBytes = 64 << 10

func outPath(dir, block, stream string) string {
	return filepath.Join(dir, "out", block+"."+stream)
}

func tailPath(dir, block, stream string) string {
	return outPath(dir, block, stream) + ".tail"
}

func validStream(stream string) bool {
	return stream == protocol.SSHStreamStdout || stream == protocol.SSHStreamStderr
}

func (s *Store) removeOutput(block string) {
	for _, st := range []string{protocol.SSHStreamStdout, protocol.SSHStreamStderr} {
		_ = os.Remove(outPath(s.dir, block, st))
		_ = os.Remove(tailPath(s.dir, block, st))
	}
}

// Sink receives one stream of one block. Bytes are sanitized BEFORE they are
// stored, the agent and the human read the same text, and the raw bytes are never
// kept. Write never fails and never blocks on the cap: the pipe must keep draining
// or the remote command would stall on a full pipe. Past the cap the middle is
// dropped and counted, and the end of the stream is kept as a tail.
type Sink struct {
	mu      sync.Mutex
	dir     string
	block   string
	stream  string
	capB    int64
	f       *os.File
	clean   sshelement.Cleaner
	stored  int64
	past    int64 // bytes seen beyond the head
	tail    []byte
	recent  []byte // the last bytes of the stream, for the row preview
	failed  bool
	flushed bool
}

// NewSink opens a stream for writing. A file that cannot be created is not an
// error here: the command still runs, the output is counted as dropped, and
// Failed says so.
func (s *Store) NewSink(block, stream string, capBytes int64) *Sink {
	k := &Sink{dir: s.dir, block: block, stream: stream, capB: capBytes}
	if err := os.MkdirAll(filepath.Join(s.dir, "out"), 0o700); err != nil {
		k.failed = true
		return k
	}
	f, err := os.OpenFile(outPath(s.dir, block, stream), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		k.failed = true
		return k
	}
	k.f = f
	return k
}

// Write implements io.Writer.
func (k *Sink) Write(p []byte) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.absorb(k.clean.Write(p))
	return len(p), nil
}

func (k *Sink) absorb(b []byte) {
	if len(b) == 0 {
		return
	}
	k.recent = keepLast(append(k.recent, b...), protocol.SSHMaxPreviewBytes*2)
	if room := k.capB - k.stored; room > 0 && k.f != nil {
		head := b
		if int64(len(head)) > room {
			// Cut at a rune boundary so a stored head is always valid UTF-8.
			n := int(room)
			for n > 0 && !utf8.RuneStart(head[n]) {
				n--
			}
			head = head[:n]
		}
		if _, err := k.f.Write(head); err != nil {
			k.failed = true
			_ = k.f.Close()
			k.f = nil
			head = nil
		}
		k.stored += int64(len(head))
		b = b[len(head):]
		if len(b) > 0 {
			// Whatever did not fit, including a rune straddling the cap, goes to the
			// tail, and the head is closed so it stays one contiguous range.
			k.capB = k.stored
		}
	}
	if len(b) > 0 {
		k.past += int64(len(b))
		k.tail = keepLast(append(k.tail, b...), TailBytes)
	}
}

func keepLast(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return append(b[:0:0], b[len(b)-n:]...)
}

// Close flushes the sanitizer and writes the tail when anything was dropped.
func (k *Sink) Close() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.flushed {
		return
	}
	k.flushed = true
	k.absorb(k.clean.Flush())
	if k.f != nil {
		if err := k.f.Close(); err != nil {
			k.failed = true
		}
		k.f = nil
	}
	if k.past > 0 {
		if err := os.WriteFile(tailPath(k.dir, k.block, k.stream), k.tail, 0o600); err != nil {
			k.failed = true
		}
	}
}

// Stats is a sink's progress.
type Stats struct {
	Stored  int64
	Dropped int64
	Recent  string
	Binary  bool
	Failed  bool
}

// Stats reports progress. Dropped is what no longer exists anywhere: bytes past
// the head that also fell out of the tail.
func (k *Sink) Stats() Stats {
	k.mu.Lock()
	defer k.mu.Unlock()
	return Stats{
		Stored:  k.stored,
		Dropped: max(k.past-int64(len(k.tail)), 0),
		Recent:  Preview(k.recent),
		Binary:  k.clean.Binary,
		Failed:  k.failed,
	}
}

// Preview bounds text to SSHMaxPreviewBytes at a rune boundary, keeping the end.
func Preview(b []byte) string {
	if len(b) > protocol.SSHMaxPreviewBytes {
		b = b[len(b)-protocol.SSHMaxPreviewBytes:]
		for len(b) > 0 && !utf8.RuneStart(b[0]) {
			b = b[1:]
		}
	}
	return string(b)
}

// ReadRange reads stored output. It returns as much of [offset, offset+limit) as
// exists, ending on a rune boundary. Missing output reads as empty.
func (s *Store) ReadRange(block, stream string, offset int64, limit int) (data []byte, total int64, err error) {
	if !validStream(stream) {
		return nil, 0, fmt.Errorf("unknown stream %q", stream)
	}
	return readFileRange(outPath(s.dir, block, stream), offset, limit)
}

func readFileRange(path string, offset int64, limit int) ([]byte, int64, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	total := fi.Size()
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return nil, total, nil
	}
	if limit <= 0 {
		limit = protocol.SSHMaxPollBytes
	}
	if rest := total - offset; int64(limit) > rest {
		limit = int(rest)
	}
	buf := make([]byte, limit)
	n, err := f.ReadAt(buf, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, total, err
	}
	buf = buf[:n]
	if offset+int64(n) < total {
		// Do not split a rune across two reads, unless a single rune is all there is.
		end := len(buf)
		for end > 0 && !utf8.RuneStart(buf[end-1]) {
			end--
		}
		if end > 0 && !utf8.FullRune(buf[end-1:]) {
			buf = buf[:end-1]
		}
	}
	return buf, total, nil
}

// ReadTail returns the tail kept past the cap, "" when nothing was dropped.
func (s *Store) ReadTail(block, stream string) (string, error) {
	if !validStream(stream) {
		return "", fmt.Errorf("unknown stream %q", stream)
	}
	raw, err := os.ReadFile(tailPath(s.dir, block, stream))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(raw), err
}

// ReadPoll is the agent's view of a stream: the stored head, then, once the block
// has ended and output was dropped, a line saying how much plus the tail. Offsets
// are positions in that view, so a poll never has to know about the cap and the
// end of a long log still reaches the agent.
func (s *Store) ReadPoll(block, stream string, offset int64, limit int, dropped int64, terminal bool) (data []byte, next, end int64, err error) {
	head, total, err := s.ReadRange(block, stream, offset, limit)
	if err != nil {
		return nil, offset, offset, err
	}
	end = total
	var suffix []byte
	if terminal {
		tail, terr := s.ReadTail(block, stream)
		if terr != nil {
			return nil, offset, offset, terr
		}
		if tail != "" {
			suffix = fmt.Appendf(nil, "\n[lola: %d bytes dropped here, output_cap_kb reached]\n%s", dropped, tail)
			end += int64(len(suffix))
		}
	}
	if offset < total {
		return head, offset + int64(len(head)), end, nil
	}
	if i := offset - total; i < int64(len(suffix)) {
		out := suffix[i:]
		if limit <= 0 {
			limit = protocol.SSHMaxPollBytes
		}
		if len(out) > limit {
			n := limit
			for n > 0 && !utf8.RuneStart(out[n]) {
				n--
			}
			out = out[:n]
		}
		return out, offset + int64(len(out)), end, nil
	}
	return nil, offset, end, nil
}
