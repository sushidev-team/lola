package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// File is the JSON-snapshot Store. Several processes may share one file (the
// server and the host-side admin commands): every Update takes an exclusive
// flock on "<path>.lock", reloads the snapshot when another process replaced
// it, and writes temp+rename. An empty path keeps the state in memory only
// (tests).
type File struct {
	path string
	// Warnf receives the warning when a corrupt snapshot falls back to
	// <path>.bak (nil = the standard logger).
	Warnf func(format string, args ...any)

	mu      sync.Mutex
	data    *Data
	seen    os.FileInfo // stat of the snapshot last loaded or written
	fromBak bool        // data came from <path>.bak; do not back up the corrupt file
}

// Open loads (or starts) the snapshot at path. The directory is created 0700.
func Open(path string) (*File, error) {
	f := &File{path: path, data: NewData()}
	if path == "" {
		return f, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}
	unlock, err := f.lock(syscall.LOCK_SH)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := f.reloadIfChanged(); err != nil {
		return nil, err
	}
	return f, nil
}

// Path returns the snapshot path ("" for memory).
func (f *File) Path() string { return f.path }

func (f *File) lock(how int) (func(), error) {
	lf, err := os.OpenFile(f.path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: open lock: %w", err)
	}
	if err := syscall.Flock(int(lf.Fd()), how); err != nil {
		lf.Close()
		return nil, fmt.Errorf("store: lock: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		lf.Close()
	}, nil
}

// reloadIfChanged re-reads the snapshot when it differs from what this
// process last saw. Caller holds f.mu and the file lock.
func (f *File) reloadIfChanged() error {
	st, err := os.Stat(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: stat: %w", err)
	}
	if f.seen != nil && os.SameFile(f.seen, st) && f.seen.ModTime().Equal(st.ModTime()) && f.seen.Size() == st.Size() {
		return nil
	}
	d, err := readSnapshot(f.path)
	if err != nil {
		// A snapshot that cannot be read or decoded (a torn disk, a hand
		// edit) falls back to the previous one, kept at <path>.bak by every
		// successful write. The warning names the file, never its content.
		bak, berr := readSnapshot(f.path + bakSuffix)
		if berr != nil {
			return err
		}
		f.warnf("store: snapshot %s unreadable, loaded the previous snapshot from %s%s", f.path, f.path, bakSuffix)
		d, f.fromBak = bak, true
	} else {
		f.fromBak = false
	}
	f.data, f.seen = d, st
	return nil
}

// bakSuffix names the copy of the previous snapshot.
const bakSuffix = ".bak"

func readSnapshot(path string) (*Data, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("store: read: %w", err)
	}
	d := NewData()
	if err := json.Unmarshal(raw, d); err != nil {
		return nil, fmt.Errorf("store: decode snapshot: %w", err)
	}
	d.init()
	return d, nil
}

func (f *File) warnf(format string, args ...any) {
	if f.Warnf != nil {
		f.Warnf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// keepBackup makes <path>.bak the snapshot about to be replaced: a hard link
// where the filesystem allows one, a copy otherwise. It is skipped while the
// state in memory came from the backup, so a corrupt snapshot never
// overwrites the good copy. Best-effort: a failed backup does not fail the
// write. Caller holds the exclusive lock.
func (f *File) keepBackup() {
	if f.fromBak {
		return
	}
	bak := f.path + bakSuffix
	if _, err := os.Stat(f.path); err != nil {
		return
	}
	_ = os.Remove(bak)
	if err := os.Link(f.path, bak); err == nil {
		return
	}
	raw, err := os.ReadFile(f.path)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.path), ".hub-*.bak.tmp")
	if err != nil {
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	if tmp.Chmod(0o600) != nil {
		tmp.Close()
		return
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return
	}
	if tmp.Close() != nil {
		return
	}
	_ = os.Rename(name, bak)
}

// View implements Store.
func (f *File) View(fn func(d *Data) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.path != "" {
		unlock, err := f.lock(syscall.LOCK_SH)
		if err != nil {
			return err
		}
		defer unlock()
		if err := f.reloadIfChanged(); err != nil {
			return err
		}
	}
	return fn(f.data)
}

// Update implements Store.
func (f *File) Update(fn func(d *Data) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.path != "" {
		unlock, err := f.lock(syscall.LOCK_EX)
		if err != nil {
			return err
		}
		defer unlock()
		if err := f.reloadIfChanged(); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(f.data)
	if err != nil {
		return fmt.Errorf("store: encode: %w", err)
	}
	work := NewData()
	if err := json.Unmarshal(raw, work); err != nil {
		return fmt.Errorf("store: copy: %w", err)
	}
	work.init()
	if err := fn(work); err != nil {
		return err
	}
	if f.path != "" {
		if err := f.write(work); err != nil {
			return err
		}
	}
	f.data = work
	return nil
}

// write persists d atomically. Caller holds the exclusive lock.
func (f *File) write(d *Data) error {
	raw, err := json.MarshalIndent(d, "", " ")
	if err != nil {
		return fmt.Errorf("store: encode: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.path), ".hub-*.tmp")
	if err != nil {
		return fmt.Errorf("store: temp: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("store: chmod: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("store: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("store: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("store: close: %w", err)
	}
	f.keepBackup()
	if err := os.Rename(name, f.path); err != nil {
		return fmt.Errorf("store: rename: %w", err)
	}
	f.fromBak = false
	st, err := os.Stat(f.path)
	if err != nil {
		return fmt.Errorf("store: stat: %w", err)
	}
	f.seen = st
	return nil
}
