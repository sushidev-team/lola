package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
)

var pngMagic = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

// pngBytes is n bytes that sniff as image/png.
func pngBytes(n int) []byte {
	b := append([]byte{}, pngMagic...)
	for len(b) < n {
		b = append(b, byte(len(b)))
	}
	return b[:n]
}

type attachEnv struct {
	d  *Daemon
	wt string
	id string
}

func newAttachEnv(t *testing.T) *attachEnv {
	t.Helper()
	d, _ := talkFakeDaemon(t)
	d.attachReg().statfs = func(string) (uint64, error) { return 10 << 30, nil }
	id := "att-1"
	wt := filepath.Join(d.home, "worktrees", "p1", id)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	d.sessions.Upsert(session.Session{ID: id, Source: "native", Project: "p1", TmuxName: id, Agent: "claude", Worktree: wt})
	return &attachEnv{d: d, wt: wt, id: id}
}

func (e *attachEnv) part(ctx context.Context, name string, n int, data []byte, last bool) (protocol.AttachImageData, error) {
	return e.d.handleAttachImage(ctx, protocol.AttachImageArgs{Session: e.id, Name: name, Mime: "image/png", Data: data, Part: n, Last: last})
}

func devCtx(id string) context.Context {
	return remote.WithPeer(context.Background(), remote.Peer{DeviceID: id})
}

func wantErr(t *testing.T, err error, msg string) {
	t.Helper()
	if err == nil || err.Error() != msg {
		t.Fatalf("error = %v, want %q", err, msg)
	}
}

func partsLeft(t *testing.T, wt string) []string {
	t.Helper()
	ents, _ := os.ReadDir(filepath.Join(wt, ".lola", "images"))
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func TestAttachImageHappyPath(t *testing.T) {
	e := newAttachEnv(t)
	whole := pngBytes(2*protocol.AttachChunkBytes + 100)
	ctx := devCtx("dev1")
	var got protocol.AttachImageData
	for i, c := range [][]byte{whole[:protocol.AttachChunkBytes], whole[protocol.AttachChunkBytes : 2*protocol.AttachChunkBytes], whole[2*protocol.AttachChunkBytes:]} {
		var err error
		got, err = e.part(ctx, "upload-key-1", i, c, i == 2)
		if err != nil {
			t.Fatalf("part %d: %v", i, err)
		}
		if i < 2 && (got.Path != "" || got.Received != int64(i+1)*protocol.AttachChunkBytes) {
			t.Fatalf("part %d answered %+v", i, got)
		}
	}
	re := regexp.MustCompile(`^paste-[0-9a-f]{16}\.png$`)
	if filepath.Dir(got.Path) != filepath.Join(e.wt, ".lola", "images") || !re.MatchString(filepath.Base(got.Path)) || got.Thumb != "" {
		t.Fatalf("token %+v", got)
	}
	fi, err := os.Stat(got.Path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("stored file: %v %v", fi, err)
	}
	b, _ := os.ReadFile(got.Path)
	if !bytes.Equal(b, whole) {
		t.Fatal("stored bytes differ")
	}
	if left := partsLeft(t, e.wt); len(left) != 1 {
		t.Fatalf("leftovers: %v", left)
	}
	if n := len(e.d.attachReg().uploads); n != 0 {
		t.Fatalf("%d uploads still open", n)
	}
}

func TestAttachImageRefusals(t *testing.T) {
	small := pngBytes(64)
	t.Run("unknown session", func(t *testing.T) {
		e := newAttachEnv(t)
		e.id = "nope"
		_, err := e.part(context.Background(), "upload-key-1", 0, small, true)
		wantErr(t, err, protocol.AttachErrNoFolder)
	})
	t.Run("worktree outside home", func(t *testing.T) {
		e := newAttachEnv(t)
		s, _ := e.d.sessions.Get(e.id)
		s.Worktree = t.TempDir()
		e.d.sessions.Upsert(s)
		_, err := e.part(context.Background(), "upload-key-1", 0, small, true)
		wantErr(t, err, protocol.AttachErrNoFolder)
	})
	t.Run("local session under home", func(t *testing.T) {
		e := newAttachEnv(t)
		s, _ := e.d.sessions.Get(e.id)
		s.Kind = session.KindLocal
		e.d.sessions.Upsert(s)
		_, err := e.part(context.Background(), "upload-key-1", 0, small, true)
		wantErr(t, err, protocol.AttachErrNoFolder)
	})
	t.Run("lola dir is a link", func(t *testing.T) {
		e := newAttachEnv(t)
		if err := os.Symlink(t.TempDir(), filepath.Join(e.wt, ".lola")); err != nil {
			t.Fatal(err)
		}
		_, err := e.part(context.Background(), "upload-key-1", 0, small, true)
		wantErr(t, err, protocol.AttachErrLink)
	})
	t.Run("images dir is a link", func(t *testing.T) {
		e := newAttachEnv(t)
		out := t.TempDir()
		os.MkdirAll(filepath.Join(e.wt, ".lola"), 0o700)
		if err := os.Symlink(out, filepath.Join(e.wt, ".lola", "images")); err != nil {
			t.Fatal(err)
		}
		_, err := e.part(context.Background(), "upload-key-1", 0, small, true)
		wantErr(t, err, protocol.AttachErrLink)
		if ents, _ := os.ReadDir(out); len(ents) != 0 {
			t.Fatalf("wrote through the link: %v", ents)
		}
	})
	t.Run("wrong sniff", func(t *testing.T) {
		e := newAttachEnv(t)
		_, err := e.part(context.Background(), "upload-key-1", 0, []byte("not an image at all"), true)
		wantErr(t, err, protocol.AttachErrType)
		if left := partsLeft(t, e.wt); len(left) != 0 {
			t.Fatalf("left %v", left)
		}
	})
	t.Run("heic mime", func(t *testing.T) {
		e := newAttachEnv(t)
		_, err := e.d.handleAttachImage(context.Background(), protocol.AttachImageArgs{Session: e.id, Name: "upload-key-1", Mime: "image/heic", Data: small, Last: true})
		wantErr(t, err, protocol.AttachErrType)
	})
	t.Run("over 20 MiB", func(t *testing.T) {
		e := newAttachEnv(t)
		chunk := pngBytes(protocol.AttachChunkBytes)
		var err error
		for i := 0; i <= protocol.AttachMaxParts && err == nil; i++ {
			_, err = e.part(context.Background(), "upload-key-1", i, chunk, false)
		}
		wantErr(t, err, protocol.AttachErrTooLarge)
		if left := partsLeft(t, e.wt); len(left) != 0 {
			t.Fatalf("left %v", left)
		}
	})
	t.Run("out of order", func(t *testing.T) {
		e := newAttachEnv(t)
		if _, err := e.part(context.Background(), "upload-key-1", 0, small, false); err != nil {
			t.Fatal(err)
		}
		_, err := e.part(context.Background(), "upload-key-1", 2, small, false)
		wantErr(t, err, protocol.AttachErrOrder)
		if left := partsLeft(t, e.wt); len(left) != 0 {
			t.Fatalf("left %v", left)
		}
	})
	t.Run("part 0 twice", func(t *testing.T) {
		e := newAttachEnv(t)
		if _, err := e.part(context.Background(), "upload-key-1", 0, small, false); err != nil {
			t.Fatal(err)
		}
		_, err := e.part(context.Background(), "upload-key-1", 0, small, false)
		wantErr(t, err, protocol.AttachErrOrder)
	})
	t.Run("third concurrent upload", func(t *testing.T) {
		e := newAttachEnv(t)
		for _, n := range []string{"upload-key-1", "upload-key-2"} {
			if _, err := e.part(devCtx("dev1"), n, 0, small, false); err != nil {
				t.Fatal(err)
			}
		}
		_, err := e.part(devCtx("dev1"), "upload-key-3", 0, small, false)
		wantErr(t, err, protocol.AttachErrBusy)
		if _, err := e.part(devCtx("dev2"), "upload-key-3", 0, small, false); err != nil {
			t.Fatalf("another device was refused: %v", err)
		}
	})
	t.Run("planted tmp link", func(t *testing.T) {
		e := newAttachEnv(t)
		if _, err := e.part(context.Background(), "upload-key-1", 0, small, false); err != nil {
			t.Fatal(err)
		}
		// The tmp name is random and O_EXCL: a pre-planted link of any name cannot be adopted.
		out := filepath.Join(t.TempDir(), "victim")
		os.WriteFile(out, []byte("keep"), 0o600)
		os.Symlink(out, filepath.Join(e.wt, ".lola", "images", ".upload-0000000000000000.part"))
		if _, err := e.part(context.Background(), "upload-key-2", 0, small, true); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(out); string(b) != "keep" {
			t.Fatalf("victim rewritten: %q", b)
		}
	})
	t.Run("denied pin", func(t *testing.T) {
		e := newAttachEnv(t)
		// A worktree gitguard denies has a record without a valid pin.
		gitguard.Deny(e.wt)
		t.Cleanup(func() { gitguard.Clear(e.wt) })
		_, err := e.part(context.Background(), "upload-key-1", 0, small, true)
		wantErr(t, err, protocol.AttachErrPin)
	})
	t.Run("bad key and size", func(t *testing.T) {
		e := newAttachEnv(t)
		_, err := e.part(context.Background(), "../etc/passwd", 0, small, true)
		wantErr(t, err, errAttachBad.Error())
		_, err = e.part(context.Background(), "upload-key-1", 0, nil, true)
		wantErr(t, err, errAttachBad.Error())
	})
}

func TestAttachImageSessionQuota(t *testing.T) {
	e := newAttachEnv(t)
	imgs := filepath.Join(e.wt, ".lola", "images")
	os.MkdirAll(imgs, 0o700)
	f, _ := os.Create(filepath.Join(imgs, "big.png"))
	f.Truncate(AttachMaxBytesPerSession)
	f.Close()
	_, err := e.part(context.Background(), "upload-key-1", 0, pngBytes(64), true)
	wantErr(t, err, protocol.AttachErrQuota)
}

func TestAttachImageDeviceDailyQuota(t *testing.T) {
	e := newAttachEnv(t)
	r := e.d.attachReg()
	r.mu.Lock()
	r.dayLocked("dev1").bytes = AttachMaxBytesPerDevicePerDay
	r.mu.Unlock()
	_, err := e.part(devCtx("dev1"), "upload-key-1", 0, pngBytes(64), true)
	wantErr(t, err, protocol.AttachErrQuota)
	if _, err := e.part(devCtx("dev2"), "upload-key-1", 0, pngBytes(64), true); err != nil {
		t.Fatalf("another device: %v", err)
	}
	// The day rolls over.
	r.now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	if _, err := e.part(devCtx("dev1"), "upload-key-2", 0, pngBytes(64), true); err != nil {
		t.Fatalf("next day: %v", err)
	}
}

func TestAttachImageDiskFloor(t *testing.T) {
	e := newAttachEnv(t)
	e.d.attachReg().statfs = func(string) (uint64, error) { return AttachMinFreeBytes - 1, nil }
	_, err := e.part(context.Background(), "upload-key-1", 0, pngBytes(64), true)
	wantErr(t, err, protocol.AttachErrDisk)
	// Re-checked on the last part.
	e.d.attachReg().statfs = func(string) (uint64, error) { return 10 << 30, nil }
	if _, err := e.part(context.Background(), "upload-key-2", 0, pngBytes(64), false); err != nil {
		t.Fatal(err)
	}
	e.d.attachReg().statfs = func(string) (uint64, error) { return 0, nil }
	_, err = e.part(context.Background(), "upload-key-2", 1, pngBytes(64), true)
	wantErr(t, err, protocol.AttachErrDisk)
	if left := partsLeft(t, e.wt); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

func TestAttachImageSwapBeforeLink(t *testing.T) {
	for _, mode := range []string{"file", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			e := newAttachEnv(t)
			imgs := filepath.Join(e.wt, ".lola", "images")
			e.d.attachReg().beforeLink = func(_ *os.Root, tmp string) {
				p := filepath.Join(imgs, tmp)
				os.Remove(p)
				if mode == "file" {
					os.WriteFile(p, pngBytes(80), 0o600)
				} else {
					os.Symlink(filepath.Join(e.wt, "elsewhere"), p)
				}
			}
			_, err := e.part(context.Background(), "upload-key-1", 0, pngBytes(64), true)
			wantErr(t, err, protocol.AttachErrLink)
			for _, n := range partsLeft(t, e.wt) {
				if strings.HasPrefix(n, "paste-") {
					t.Fatalf("a paste file survived: %s", n)
				}
			}
		})
	}
}

func TestAttachImageExpiry(t *testing.T) {
	e := newAttachEnv(t)
	r := e.d.attachReg()
	now := time.Now()
	r.now = func() time.Time { return now }
	if _, err := e.part(context.Background(), "upload-key-1", 0, pngBytes(64), false); err != nil {
		t.Fatal(err)
	}
	if len(partsLeft(t, e.wt)) != 1 {
		t.Fatal("no tmp part")
	}
	now = now.Add(AttachIdleTimeout - time.Second)
	r.expire()
	if len(r.uploads) != 1 {
		t.Fatal("expired early")
	}
	now = now.Add(2 * time.Second)
	r.expire()
	if len(r.uploads) != 0 || len(partsLeft(t, e.wt)) != 0 {
		t.Fatalf("not expired: %v", partsLeft(t, e.wt))
	}
	_, err := e.part(context.Background(), "upload-key-1", 1, pngBytes(64), true)
	wantErr(t, err, protocol.AttachErrExpired)
	if r.timer != nil {
		t.Fatal("timer still armed with nothing open")
	}
}

func TestAttachImageTimerFires(t *testing.T) {
	e := newAttachEnv(t)
	r := e.d.attachReg()
	fired := make(chan func(), 1)
	r.afterFunc = func(_ time.Duration, f func()) *time.Timer {
		select {
		case fired <- f:
		default:
		}
		return time.NewTimer(time.Hour)
	}
	if _, err := e.part(context.Background(), "upload-key-1", 0, pngBytes(64), false); err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return time.Now().Add(2 * AttachIdleTimeout) }
	(<-fired)()
	if len(partsLeft(t, e.wt)) != 0 {
		t.Fatal("timer callback left the part")
	}
}

func TestAttachImageSweepsStaleParts(t *testing.T) {
	e := newAttachEnv(t)
	imgs := filepath.Join(e.wt, ".lola", "images")
	os.MkdirAll(imgs, 0o700)
	stale := filepath.Join(imgs, ".upload-aaaaaaaaaaaaaaaa.part")
	fresh := filepath.Join(imgs, ".upload-bbbbbbbbbbbbbbbb.part")
	os.WriteFile(stale, []byte("x"), 0o600)
	os.WriteFile(fresh, []byte("x"), 0o600)
	old := time.Now().Add(-2 * AttachIdleTimeout)
	os.Chtimes(stale, old, old)
	if _, err := e.part(context.Background(), "upload-key-1", 0, pngBytes(64), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Error("stale part kept")
	}
	if _, err := os.Lstat(fresh); err != nil {
		t.Error("fresh part removed")
	}
}

func TestAttachImageNoExec(t *testing.T) {
	// No command runs on the attach path: an empty PATH makes any exec fail, and
	// the upload must still succeed.
	t.Setenv("PATH", "")
	if _, err := exec.LookPath("sips"); err == nil {
		t.Skip("sips resolvable")
	}
	e := newAttachEnv(t)
	got, err := e.part(context.Background(), "upload-key-1", 0, pngBytes(64), true)
	if err != nil || got.Path == "" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAttachImageAbortAll(t *testing.T) {
	e := newAttachEnv(t)
	if _, err := e.part(context.Background(), "upload-key-1", 0, pngBytes(64), false); err != nil {
		t.Fatal(err)
	}
	e.d.abortAttachments()
	if len(partsLeft(t, e.wt)) != 0 || len(e.d.attachReg().uploads) != 0 {
		t.Fatal("shutdown left an upload")
	}
}

func TestAttachmentImageRead(t *testing.T) {
	e := newAttachEnv(t)
	whole := pngBytes(protocol.AttachChunkBytes + 1000)
	imgs := filepath.Join(e.wt, ".lola", "images")
	os.MkdirAll(imgs, 0o700)
	tok := filepath.Join(imgs, "paste-0123456789abcdef.png")
	os.WriteFile(tok, whole, 0o600)

	read := func(a protocol.AttachmentImageArgs) (protocol.AttachmentImageData, error) {
		return e.d.handleAttachmentImage(context.Background(), a)
	}
	var got []byte
	for off := int64(0); ; off += protocol.AttachChunkBytes {
		c, err := read(protocol.AttachmentImageArgs{Session: e.id, Path: tok, Offset: off})
		if err != nil {
			t.Fatal(err)
		}
		if c.Mime != "image/png" || c.Size != int64(len(whole)) || c.Offset != off {
			t.Fatalf("chunk %+v", c)
		}
		got = append(got, c.Data...)
		if c.EOF {
			break
		}
	}
	if !bytes.Equal(got, whole) {
		t.Fatal("chunks do not reassemble")
	}
	// The wire shape: Data is base64.
	if b, _ := json.Marshal(protocol.AttachmentImageData{Data: []byte{1}}); !strings.Contains(string(b), `"AQ=="`) {
		t.Fatalf("data not base64: %s", b)
	}

	t.Run("refused", func(t *testing.T) {
		os.WriteFile(filepath.Join(e.wt, "README.png"), whole, 0o600)
		os.Symlink(filepath.Join(e.wt, "README.png"), filepath.Join(imgs, "inside.png"))
		os.Symlink("/etc/hosts", filepath.Join(imgs, "outside.png"))
		os.WriteFile(filepath.Join(imgs, "text.png"), []byte("hello"), 0o600)
		cases := []struct{ name, path, msg string }{
			{"dotdot", imgs + "/../images/paste-0123456789abcdef.png", protocol.AttachErrNotHere},
			{"unclean", imgs + "//paste-0123456789abcdef.png", protocol.AttachErrNotHere},
			{"relative", "paste-0123456789abcdef.png", protocol.AttachErrNotHere},
			{"other dir", filepath.Join(e.wt, "README.png"), protocol.AttachErrNotHere},
			{"bad ext", filepath.Join(imgs, "x.txt"), protocol.AttachErrNotHere},
			{"link out", filepath.Join(imgs, "outside.png"), protocol.AttachErrUnavailable},
			{"link in", filepath.Join(imgs, "inside.png"), protocol.AttachErrUnavailable},
			{"missing", filepath.Join(imgs, "gone.png"), protocol.AttachErrUnavailable},
			{"not image", filepath.Join(imgs, "text.png"), protocol.AttachErrNotImage},
		}
		for _, c := range cases {
			_, err := read(protocol.AttachmentImageArgs{Session: e.id, Path: c.path})
			if err == nil || err.Error() != c.msg {
				t.Errorf("%s: %v, want %q", c.name, err, c.msg)
			}
		}
		_, err := read(protocol.AttachmentImageArgs{Session: e.id, Path: tok, Offset: 7})
		wantErr(t, err, protocol.AttachErrUnreadable)
		_, err = read(protocol.AttachmentImageArgs{Session: e.id, Path: tok, Offset: 10 * protocol.AttachChunkBytes})
		wantErr(t, err, protocol.AttachErrUnreadable)
		_, err = read(protocol.AttachmentImageArgs{Session: "other", Path: tok})
		wantErr(t, err, protocol.AttachErrNotHere)
		// A path in another session's images folder.
		_, err = read(protocol.AttachmentImageArgs{Session: e.id, Path: filepath.Join(e.d.home, "worktrees", "p1", "att-2", ".lola", "images", "a.png")})
		wantErr(t, err, protocol.AttachErrNotHere)
	})

	t.Run("linked images folder", func(t *testing.T) {
		e2 := newAttachEnv(t)
		out := t.TempDir()
		os.WriteFile(filepath.Join(out, "paste-0123456789abcdef.png"), whole, 0o600)
		os.MkdirAll(filepath.Join(e2.wt, ".lola"), 0o700)
		os.Symlink(out, filepath.Join(e2.wt, ".lola", "images"))
		_, err := e2.d.handleAttachmentImage(context.Background(), protocol.AttachmentImageArgs{Session: e2.id, Path: filepath.Join(e2.wt, ".lola", "images", "paste-0123456789abcdef.png")})
		wantErr(t, err, protocol.AttachErrFolderUnavailable)
	})

	t.Run("local session", func(t *testing.T) {
		s, _ := e.d.sessions.Get(e.id)
		s.Kind = session.KindLocal
		e.d.sessions.Upsert(s)
		_, err := read(protocol.AttachmentImageArgs{Session: e.id, Path: tok})
		wantErr(t, err, protocol.AttachErrNotHere)
	})
}

func TestAttachmentImageEndedSession(t *testing.T) {
	e := newAttachEnv(t)
	imgs := filepath.Join(e.wt, ".lola", "images")
	os.MkdirAll(imgs, 0o700)
	tok := filepath.Join(imgs, "paste-0123456789abcdef.png")
	os.WriteFile(tok, pngBytes(300), 0o600)

	endWith := func(id, kept string) {
		store, err := e.d.sshStore(id, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Update(func(st *sshstore.State) error {
			st.Ended = &sshstore.EndedInfo{EndedSession: protocol.EndedSession{KeptWorktree: kept}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	endWith("ended-1", e.wt)
	c, err := e.d.handleAttachmentImage(context.Background(), protocol.AttachmentImageArgs{Session: "ended-1", Path: tok})
	if err != nil || !c.EOF || len(c.Data) != 300 {
		t.Fatalf("%+v %v", c, err)
	}

	// A kept worktree outside $LOLA_HOME/worktrees is never read, even under home.
	outside := filepath.Join(e.d.home, "borrowed")
	os.MkdirAll(filepath.Join(outside, ".lola", "images"), 0o700)
	os.WriteFile(filepath.Join(outside, ".lola", "images", "a.png"), pngBytes(10), 0o600)
	endWith("ended-2", outside)
	_, err = e.d.handleAttachmentImage(context.Background(), protocol.AttachmentImageArgs{Session: "ended-2", Path: filepath.Join(outside, ".lola", "images", "a.png")})
	wantErr(t, err, protocol.AttachErrNotHere)
}
