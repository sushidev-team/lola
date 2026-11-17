package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
)

// Image attachments over the remote link: a phone uploads into
// <worktree>/.lola/images in parts and reads an attachment back in chunks.
// Contract: docs/mobile/02-events.md §12.
//
// An agent owns the worktree and can plant links in it, so every filesystem
// step goes through os.Root handles (openat-based, a link cannot leave them),
// writes go through the descriptor and never by name, and no command runs:
// no sips, no git. That keeps "no host commands in a sandboxed worktree after
// the agent starts" true.

// Daemon-side limits of an upload (§12.2).
const (
	AttachIdleTimeout             = 60 * time.Second
	AttachMaxOpenPerPeer          = 2
	AttachMaxOpenTotal            = 8
	AttachMaxBytesPerSession      = 200 << 20
	AttachMaxBytesPerDevicePerDay = 500 << 20
	AttachMinFreeBytes            = 1 << 30
)

// attachLocalPeer keys the uploads of a unix-socket request, which has no peer.
const attachLocalPeer = "local"

var (
	attachNameRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	attachLeafRe    = regexp.MustCompile(`(?i)^[A-Za-z0-9][A-Za-z0-9._-]{0,127}\.(png|jpe?g|gif|webp)$`)
	errAttachBad    = errors.New("bad image upload request")
	attachTmpPrefix = ".upload-"
	attachTmpGlob   = func(name string) bool {
		return strings.HasPrefix(name, attachTmpPrefix) && strings.HasSuffix(name, ".part")
	}
)

func attachErr(msg string) error { return errors.New(msg) }

type attachKey struct{ peer, session, name string }

// attachUpload is one open upload. mu serializes its parts (reqConcurrency can
// deliver them concurrently); the fields below it are guarded by the registry's
// mu, which is only ever taken AFTER an upload's mu, never before.
type attachUpload struct {
	key      attachKey
	wt       string
	mime     string
	tmp      string
	f        *os.File
	imgRoot  *os.Root
	existing int64 // bytes already in the images folder when part 0 arrived

	mu   sync.Mutex
	dead atomic.Bool
	once sync.Once

	received int64
	next     int
	lastPart time.Time
}

// release removes the temporary file and closes the handles. Idempotent, takes
// no lock, safe against a part writing at the same moment (it then fails).
func (u *attachUpload) release() {
	u.once.Do(func() {
		u.dead.Store(true)
		_ = u.imgRoot.Remove(u.tmp)
		_ = u.f.Close()
		_ = u.imgRoot.Close()
	})
}

type dayBytes struct {
	day   string
	bytes int64
}

// attachRegistry holds the open uploads and the per-device daily byte count.
// Seams (now, statfs, afterFunc, beforeLink) exist for tests.
type attachRegistry struct {
	mu      sync.Mutex
	uploads map[attachKey]*attachUpload
	daily   map[string]*dayBytes
	timer   *time.Timer

	now        func() time.Time
	statfs     func(path string) (uint64, error)
	afterFunc  func(d time.Duration, f func()) *time.Timer
	beforeLink func(imgRoot *os.Root, tmp string)
}

func newAttachRegistry() *attachRegistry {
	return &attachRegistry{
		uploads:   map[attachKey]*attachUpload{},
		daily:     map[string]*dayBytes{},
		now:       time.Now,
		statfs:    statfsFree,
		afterFunc: time.AfterFunc,
	}
}

func statfsFree(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil //nolint:unconvert // field widths differ per OS
}

// attachReg is the daemon's upload registry; it lives as long as its daemon.
func (d *Daemon) attachReg() *attachRegistry {
	d.attachOnce.Do(func() { d.attach = newAttachRegistry() })
	return d.attach
}

// abortAttachments ends every open upload (daemon shutdown).
func (d *Daemon) abortAttachments() {
	r := d.attachReg()
	r.mu.Lock()
	all := make([]*attachUpload, 0, len(r.uploads))
	for k, u := range r.uploads {
		all = append(all, u)
		delete(r.uploads, k)
	}
	r.rearmLocked()
	r.mu.Unlock()
	for _, u := range all {
		u.release()
	}
}

// attachUnder cleans dir and reports whether it is absolute and strictly inside
// base (the desktop's rule).
func attachUnder(dir, base string) (string, bool) {
	if dir == "" || !filepath.IsAbs(dir) {
		return "", false
	}
	b, err := filepath.Abs(base)
	if err != nil {
		return "", false
	}
	c := filepath.Clean(dir)
	if !strings.HasPrefix(c, b+string(filepath.Separator)) {
		return "", false
	}
	return c, true
}

// openImagesRoot opens <wt>/.lola/images, creating the folders when create is
// set. Anything that is not a plain directory, or any failure to open through
// the root, is link (the folder cannot be trusted). gone is a missing worktree.
func openImagesRoot(wt string, create bool) (imgRoot *os.Root, gone bool, err error) {
	root, err := os.OpenRoot(wt)
	if err != nil {
		return nil, true, err
	}
	defer root.Close()
	for _, dir := range []string{".lola", ".lola/images"} {
		if create {
			if err := root.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return nil, false, err
			}
		}
		fi, err := root.Lstat(dir)
		if err != nil || !fi.IsDir() {
			return nil, false, errors.New("not a plain directory")
		}
	}
	imgRoot, err = root.OpenRoot(".lola/images")
	if err != nil {
		return nil, false, err
	}
	return imgRoot, false, nil
}

func randHex8() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// handleAttachImage takes one part of an upload (§12.2).
func (d *Daemon) handleAttachImage(ctx context.Context, a protocol.AttachImageArgs) (protocol.AttachImageData, error) {
	var zero protocol.AttachImageData
	peer := attachLocalPeer
	if p, ok := remote.PeerFromContext(ctx); ok && p.DeviceID != "" {
		peer = p.DeviceID
	}
	if !attachNameRe.MatchString(a.Name) || a.Part < 0 || len(a.Data) == 0 || len(a.Data) > protocol.AttachChunkBytes {
		return zero, errAttachBad
	}
	r := d.attachReg()
	key := attachKey{peer, a.Session, a.Name}

	var u *attachUpload
	if a.Part == 0 {
		var err error
		if u, err = r.open(d, key, a); err != nil {
			return zero, err
		}
	} else {
		r.mu.Lock()
		u = r.uploads[key]
		if u != nil {
			u.lastPart = r.now()
		}
		r.mu.Unlock()
		if u == nil {
			return zero, attachErr(protocol.AttachErrExpired)
		}
	}
	return r.accept(u, a)
}

// open finds or starts the upload of key. Part 0 repeated finds the open one,
// which accept then refuses as out of order and aborts.
func (r *attachRegistry) open(d *Daemon, key attachKey, a protocol.AttachImageArgs) (*attachUpload, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if u, ok := r.uploads[key]; ok {
		u.lastPart = r.now()
		return u, nil
	}

	s, ok := d.sessions.Get(a.Session)
	if _, lane := session.SplitLaneID(a.Session); !ok || lane != "" || s.Kind == session.KindLocal || s.IsAgentless() || s.GoalManaged() {
		return nil, attachErr(protocol.AttachErrNoFolder)
	}
	wt, ok := attachUnder(sessionWorktree(d.home, s), d.home)
	if !ok {
		return nil, attachErr(protocol.AttachErrNoFolder)
	}
	if _, pin, ok := gitguard.Lookup(wt); ok && !pin.Valid() {
		return nil, attachErr(protocol.AttachErrPin)
	}
	imgRoot, gone, err := openImagesRoot(wt, true)
	if err != nil {
		if gone {
			return nil, attachErr(protocol.AttachErrGone)
		}
		return nil, attachErr(protocol.AttachErrLink)
	}
	keep := false
	defer func() {
		if !keep {
			imgRoot.Close()
		}
	}()

	if _, ok := protocol.AttachMimes[a.Mime]; !ok {
		return nil, attachErr(protocol.AttachErrType)
	}

	var perPeer int
	var otherReceived int64
	owned := map[string]bool{}
	for k, o := range r.uploads {
		if k.peer == key.peer {
			perPeer++
		}
		if o.wt == wt {
			otherReceived += o.received
			owned[o.tmp] = true
		}
	}
	if perPeer >= AttachMaxOpenPerPeer || len(r.uploads) >= AttachMaxOpenTotal {
		return nil, attachErr(protocol.AttachErrBusy)
	}
	if err := r.checkDisk(wt); err != nil {
		return nil, err
	}
	existing, err := r.scanImages(imgRoot, owned)
	if err != nil {
		return nil, attachErr(protocol.AttachErrLink)
	}
	existing += otherReceived
	if existing >= AttachMaxBytesPerSession || r.dayLocked(key.peer).bytes >= AttachMaxBytesPerDevicePerDay {
		return nil, attachErr(protocol.AttachErrQuota)
	}

	id, err := randHex8()
	if err != nil {
		return nil, attachErr(protocol.AttachErrDisk)
	}
	tmp := attachTmpPrefix + id + ".part"
	f, err := imgRoot.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, attachErr(protocol.AttachErrLink)
	}
	u := &attachUpload{key: key, wt: wt, mime: a.Mime, tmp: tmp, f: f, imgRoot: imgRoot, existing: existing, lastPart: r.now()}
	r.uploads[key] = u
	r.rearmLocked()
	keep = true
	return u, nil
}

// scanImages sums the regular files of the images folder, skipping the
// temporary parts (open ones are counted by received) and removing stale
// leftovers of a crash that no open upload owns.
func (r *attachRegistry) scanImages(imgRoot *os.Root, owned map[string]bool) (int64, error) {
	dir, err := imgRoot.Open(".")
	if err != nil {
		return 0, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() {
			continue
		}
		fi, err := imgRoot.Lstat(name)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if attachTmpGlob(name) {
			if !owned[name] && r.now().Sub(fi.ModTime()) > AttachIdleTimeout {
				_ = imgRoot.Remove(name)
			}
			continue
		}
		total += fi.Size()
	}
	return total, nil
}

func (r *attachRegistry) checkDisk(wt string) error {
	free, err := r.statfs(wt)
	if err != nil || free < AttachMinFreeBytes {
		return attachErr(protocol.AttachErrDisk)
	}
	return nil
}

// dayLocked is the peer's bucket for the current UTC day. r.mu held.
func (r *attachRegistry) dayLocked(peer string) *dayBytes {
	today := r.now().UTC().Format("2006-01-02")
	b := r.daily[peer]
	if b == nil {
		b = &dayBytes{}
		r.daily[peer] = b
	}
	if b.day != today {
		b.day, b.bytes = today, 0
	}
	return b
}

// abort ends u and forgets it. The caller holds u.mu (or owns u exclusively).
func (r *attachRegistry) abort(u *attachUpload) {
	u.release()
	r.mu.Lock()
	if r.uploads[u.key] == u {
		delete(r.uploads, u.key)
	}
	r.rearmLocked()
	r.mu.Unlock()
}

// rearmLocked sets the single expiry timer to the earliest idle deadline of
// the open uploads, or stops it when none is open. r.mu held.
func (r *attachRegistry) rearmLocked() {
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	if len(r.uploads) == 0 {
		return
	}
	var earliest time.Time
	for _, u := range r.uploads {
		if earliest.IsZero() || u.lastPart.Before(earliest) {
			earliest = u.lastPart
		}
	}
	wait := earliest.Add(AttachIdleTimeout).Sub(r.now())
	if wait < 0 {
		wait = 0
	}
	r.timer = r.afterFunc(wait, r.expire)
}

// expire ends every upload idle for AttachIdleTimeout. It takes no upload's
// mu: release is lock-free, and a part that arrives later finds the upload
// gone or dead and answers AttachErrExpired.
func (r *attachRegistry) expire() {
	r.mu.Lock()
	var gone []*attachUpload
	for k, u := range r.uploads {
		if r.now().Sub(u.lastPart) >= AttachIdleTimeout {
			gone = append(gone, u)
			delete(r.uploads, k)
		}
	}
	r.rearmLocked()
	r.mu.Unlock()
	for _, u := range gone {
		u.release()
	}
}

// accept validates and stores one part; on the last it publishes the file.
func (r *attachRegistry) accept(u *attachUpload, a protocol.AttachImageArgs) (protocol.AttachImageData, error) {
	var zero protocol.AttachImageData
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.dead.Load() {
		return zero, attachErr(protocol.AttachErrExpired)
	}
	if a.Part != u.next || a.Mime != u.mime {
		r.abort(u)
		return zero, attachErr(protocol.AttachErrOrder)
	}
	n := int64(len(a.Data))
	if u.received+n > protocol.AttachMaxBytes {
		r.abort(u)
		return zero, attachErr(protocol.AttachErrTooLarge)
	}
	r.mu.Lock()
	day := r.dayLocked(u.key.peer)
	if u.existing+u.received+n > AttachMaxBytesPerSession || day.bytes+n > AttachMaxBytesPerDevicePerDay {
		r.mu.Unlock()
		r.abort(u)
		return zero, attachErr(protocol.AttachErrQuota)
	}
	day.bytes += n
	r.mu.Unlock()

	if _, err := u.f.Write(a.Data); err != nil {
		r.abort(u)
		if u.dead.Load() && errors.Is(err, os.ErrClosed) {
			return zero, attachErr(protocol.AttachErrExpired)
		}
		return zero, attachErr(protocol.AttachErrDisk)
	}
	r.mu.Lock()
	u.received += n
	u.next++
	u.lastPart = r.now()
	received := u.received
	r.rearmLocked()
	r.mu.Unlock()

	if !a.Last {
		return protocol.AttachImageData{Received: received}, nil
	}
	path, err := r.finish(u)
	if err != nil {
		r.abort(u)
		return zero, err
	}
	r.abort(u) // done: forget it (the tmp name is already gone)
	return protocol.AttachImageData{Received: received, Path: path}, nil
}

// finish publishes the temporary file under its final name (§12.2, last part).
func (r *attachRegistry) finish(u *attachUpload) (string, error) {
	if err := r.checkDisk(u.wt); err != nil {
		return "", err
	}
	head := make([]byte, min(512, u.received))
	if _, err := u.f.ReadAt(head, 0); err != nil && !errors.Is(err, io.EOF) {
		return "", attachErr(protocol.AttachErrType)
	}
	if http.DetectContentType(head) != u.mime {
		return "", attachErr(protocol.AttachErrType)
	}
	if r.beforeLink != nil {
		r.beforeLink(u.imgRoot, u.tmp)
	}
	var final string
	linked := false
	for try := 0; try < 3 && !linked; try++ {
		id, err := randHex8()
		if err != nil {
			return "", attachErr(protocol.AttachErrDisk)
		}
		final = "paste-" + id + protocol.AttachMimes[u.mime]
		err = u.imgRoot.Link(u.tmp, final)
		switch {
		case err == nil:
			linked = true
		case errors.Is(err, fs.ErrExist):
		default:
			return "", attachErr(protocol.AttachErrLink)
		}
	}
	if !linked {
		return "", attachErr(protocol.AttachErrLink)
	}
	// The link names whatever tmp was at this instant. It must be the inode this
	// upload wrote, or the agent swapped tmp between the last write and the link.
	li, lerr := u.imgRoot.Lstat(final)
	fi, ferr := u.f.Stat()
	if lerr != nil || ferr != nil || !li.Mode().IsRegular() || !os.SameFile(li, fi) {
		_ = u.imgRoot.Remove(final)
		return "", attachErr(protocol.AttachErrLink)
	}
	return filepath.Join(u.wt, ".lola", "images", final), nil
}

// handleAttachmentImage returns one chunk of an attachment (§12.3). The
// worktree comes from the session, never from the client.
func (d *Daemon) handleAttachmentImage(_ context.Context, a protocol.AttachmentImageArgs) (protocol.AttachmentImageData, error) {
	var zero protocol.AttachmentImageData
	var wt string
	if s, ok := d.sessions.Get(a.Session); ok {
		if _, lane := session.SplitLaneID(a.Session); lane != "" || s.Kind == session.KindLocal {
			return zero, attachErr(protocol.AttachErrNotHere)
		}
		wt, ok = attachUnder(sessionWorktree(d.home, s), d.home)
		if !ok {
			return zero, attachErr(protocol.AttachErrNotHere)
		}
	} else if end := sshstore.ReadEnded(d.home, a.Session); end != nil {
		wt, ok = attachUnder(end.KeptWorktree, filepath.Join(d.home, "worktrees"))
		if !ok {
			return zero, attachErr(protocol.AttachErrNotHere)
		}
	} else {
		return zero, attachErr(protocol.AttachErrNotHere)
	}

	imgDir := filepath.Join(wt, ".lola", "images")
	if !filepath.IsAbs(a.Path) || filepath.Clean(a.Path) != a.Path || filepath.Dir(a.Path) != imgDir ||
		!attachLeafRe.MatchString(filepath.Base(a.Path)) {
		return zero, attachErr(protocol.AttachErrNotHere)
	}
	base := filepath.Base(a.Path)

	imgRoot, _, err := openImagesRoot(wt, false)
	if err != nil {
		return zero, attachErr(protocol.AttachErrFolderUnavailable)
	}
	defer imgRoot.Close()
	li, err := imgRoot.Lstat(base)
	if err != nil || !li.Mode().IsRegular() {
		return zero, attachErr(protocol.AttachErrUnavailable)
	}
	f, err := imgRoot.Open(base)
	if err != nil {
		return zero, attachErr(protocol.AttachErrUnavailable)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !os.SameFile(li, fi) {
		return zero, attachErr(protocol.AttachErrUnavailable)
	}
	size := fi.Size()
	if size > protocol.AttachMaxBytes {
		return zero, attachErr(protocol.AttachErrUnreadable)
	}
	head := make([]byte, min(512, size))
	if _, err := f.ReadAt(head, 0); err != nil && !errors.Is(err, io.EOF) {
		return zero, attachErr(protocol.AttachErrUnreadable)
	}
	mime := http.DetectContentType(head)
	if _, ok := protocol.AttachMimes[mime]; !ok {
		return zero, attachErr(protocol.AttachErrNotImage)
	}
	if a.Offset < 0 || a.Offset%protocol.AttachChunkBytes != 0 || a.Offset > size {
		return zero, attachErr(protocol.AttachErrUnreadable)
	}
	buf := make([]byte, min(int64(protocol.AttachChunkBytes), size-a.Offset))
	n, err := f.ReadAt(buf, a.Offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return zero, attachErr(protocol.AttachErrUnreadable)
	}
	if n != len(buf) {
		return zero, attachErr(protocol.AttachErrUnreadable)
	}
	return protocol.AttachmentImageData{Mime: mime, Size: size, Offset: a.Offset, Data: buf, EOF: a.Offset+int64(n) >= size}, nil
}
