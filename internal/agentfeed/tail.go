package agentfeed

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Line and chunk bounds. A line above maxLineBytes is skipped rather than kept in memory.
// The largest result line measured was 1.36 MB.
const (
	maxLineBytes = 8 << 20
	chunkBytes   = 1 << 20
	headBytes    = 4 << 10
)

// errRefused means a path is not a plain file lola is willing to read.
var errRefused = errors.New("not a regular file")

// openRegular opens path only if it is a regular file, and no symlink at its last
// component. The Lstat and the no-follow open are both needed: the first refuses a FIFO
// before an open could block on it, the second closes the swap after it.
func openRegular(path string) (*os.File, os.FileInfo, error) {
	li, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !li.Mode().IsRegular() {
		return nil, nil, errRefused
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(li, info) {
		f.Close()
		return nil, nil, errRefused
	}
	return f, info, nil
}

// plainDir refuses a directory that is really a symlink. The projects root above it may
// legitimately be one, so only the levels inside it are checked.
func plainDir(dir string) bool {
	li, err := os.Lstat(dir)
	return err == nil && li.IsDir()
}

type tailStatus int

const (
	tailOK tailStatus = iota
	tailMissing
	tailReplaced
)

// tail reads one append-only log incrementally. It keeps an offset and the file's
// identity, never a descriptor, so eight idle sessions hold nothing open.
type tail struct {
	path     string
	info     os.FileInfo
	offset   int64
	skipping bool
	atEOF    bool
}

// advance hands each complete line, with its file offset, to onLine. onOversize gets the
// head of a line too long to keep. It reads about budget bytes and stops at a line
// boundary; atEOF says nothing more can be read right now.
func (t *tail) advance(ctx context.Context, budget int64, onLine func(line []byte, off int64), onOversize func(head []byte)) (int64, tailStatus) {
	f, info, err := openRegular(t.path)
	if err != nil {
		t.atEOF = true
		return 0, tailMissing
	}
	defer f.Close()
	if t.info != nil && (!os.SameFile(t.info, info) || info.Size() < t.offset) {
		return 0, tailReplaced
	}
	t.info = info
	t.atEOF = false

	var read int64
	size := info.Size()
	var buf []byte
	for read < budget {
		remaining := size - t.offset
		if remaining <= 0 {
			t.atEOF = true
			return read, tailOK
		}
		if ctx.Err() != nil {
			return read, tailOK
		}
		want := min(remaining, chunkBytes)
		if int64(len(buf)) > want {
			want = min(remaining, int64(len(buf)))
		}
		if int64(len(buf)) < want {
			buf = make([]byte, want)
		}
		n, err := f.ReadAt(buf[:want], t.offset)
		if err != nil && err != io.EOF {
			t.atEOF = true
			return read, tailOK
		}
		chunk := buf[:n]
		if n == 0 {
			t.atEOF = true
			return read, tailOK
		}
		var consumed int
		for read+int64(consumed) < budget || consumed == 0 {
			nl := bytes.IndexByte(chunk[consumed:], '\n')
			if nl < 0 {
				break
			}
			line := chunk[consumed : consumed+nl]
			if t.skipping {
				t.skipping = false
			} else if len(line) > 0 {
				onLine(line, t.offset+int64(consumed))
			}
			consumed += nl + 1
		}
		if consumed > 0 {
			t.offset += int64(consumed)
			read += int64(consumed)
			continue
		}
		// No newline anywhere in this chunk.
		switch {
		case t.skipping:
			t.offset += int64(n)
			read += int64(n)
		case int64(n) == remaining && n <= maxLineBytes:
			// A partial line still being written: read it again when it has grown.
			t.atEOF = true
			return read, tailOK
		case n >= maxLineBytes:
			onOversize(chunk[:min(n, headBytes)])
			t.skipping = true
			t.offset += int64(n)
			read += int64(n)
		default:
			buf = make([]byte, min(int64(len(buf))*2, maxLineBytes))
		}
	}
	t.atEOF = t.offset >= size
	return read, tailOK
}

// readRange re-reads one recorded line, refusing if the file is no longer the one that
// was scanned.
func readRange(path string, was os.FileInfo, off, length int64) ([]byte, error) {
	f, info, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if was == nil || !os.SameFile(was, info) || off+length > info.Size() {
		return nil, errors.New("the log changed")
	}
	buf := make([]byte, length)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return nil, err
	}
	return buf, nil
}

// join is filepath.Join for a name already checked to be a single segment.
func join(dir, name string) string { return filepath.Join(dir, name) }
