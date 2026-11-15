package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	pngenc "image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/gitdiff"

	"github.com/sushidev-team/lola/internal/hook"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/reviewpack"
)

func TestReviewPublicationRetainedAndDisplayOnly(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "working")
	s.Worktree = t.TempDir()
	d.sessions.Upsert(s)
	before, _ := d.sessions.Get(s.ID)
	m := reviewpack.Manifest{Version: 1, Title: "Review", Body: "Changes and limitations", Base: strings.Repeat("a", 40), Head: strings.Repeat("b", 40)}
	manifest, _ := json.Marshal(m)
	args, _ := json.Marshal(protocol.ReviewPublishArgs{Manifest: manifest})
	req := protocol.Request{Cmd: protocol.CmdReviewPublish, Session: s.ID, Args: args}
	if r := d.handle(context.Background(), req); !r.OK {
		t.Fatal(r.Error)
	}
	after, _ := d.sessions.Get(s.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("publication changed session state")
	}
	r := d.handle(context.Background(), protocol.Request{Cmd: protocol.CmdReviewPackages, Session: s.ID})
	var data protocol.ReviewPackagesData
	if err := json.Unmarshal(r.Data, &data); err != nil || len(data.Packages) != 1 {
		t.Fatalf("read: %s %v", r.Data, err)
	}
	// The storage read does not depend on a live record; a new daemon with the same home can inspect it.
	other := &Daemon{home: d.home}
	if r := other.handleReviewPackage(protocol.Request{Cmd: protocol.CmdReviewPackages, Session: s.ID}); !r.OK {
		t.Fatal(r.Error)
	}
	req.Session = "missing"
	if r := d.handle(context.Background(), req); r.OK {
		t.Fatal("unknown session published")
	}
	req.Session = s.ID
	secret, err := d.hookSecret()
	if err != nil {
		t.Fatal(err)
	}
	req.HookToken = hook.TokenFor(secret, s.ID)
	if err := d.bridgeAdmit(req); err != nil {
		t.Fatal(err)
	}
	req.Session = "other"
	if err := d.bridgeAdmit(req); err == nil {
		t.Fatal("token crossed sessions")
	}
	for cmd, tier := range map[string]string{
		protocol.CmdReviewPublish:  protocol.TierNever,
		protocol.CmdReviewPackages: protocol.TierRead,
		protocol.CmdReviewImage:    protocol.TierRead,
		protocol.CmdReviewMark:     protocol.TierAnswer,
		protocol.CmdReviewDiff:     protocol.TierRead,
	} {
		if got := remote.TierFor(cmd); got != tier {
			t.Fatalf("%s: wrong tier for a phone: %q, want %q", cmd, got, tier)
		}
		if cmd != protocol.CmdReviewPublish && hookBridgeCmds[cmd] {
			t.Fatalf("bridge permits read %s", cmd)
		}
	}
}

func TestReviewMarkAndDiffUseRetainedIdentities(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "working")
	s.Worktree, s.Repo = t.TempDir(), "acme/app"
	d.sessions.Upsert(s)
	m := reviewpack.Manifest{Version: 1, Title: "Review", Body: "Before", Base: strings.Repeat("a", 40), Head: strings.Repeat("b", 40)}
	from, err := reviewpack.Publish(d.home, s.ID, s.Worktree, s.Repo, 42, m)
	if err != nil {
		t.Fatal(err)
	}
	m.Head, m.Body = strings.Repeat("c", 40), "After"
	to, err := reviewpack.Publish(d.home, s.ID, s.Worktree, s.Repo, 42, m)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := d.sessions.Get(s.ID)
	a, _ := json.Marshal(protocol.ReviewMarkArgs{Package: from.ID})
	if r := d.handle(context.Background(), protocol.Request{Cmd: protocol.CmdReviewMark, Session: s.ID, Args: a}); !r.OK {
		t.Fatal(r.Error)
	}
	after, _ := d.sessions.Get(s.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("inspection changed execution state")
	}
	called := false
	d.commitDiff = func(_ context.Context, dir, a, b string) (gitdiff.Result, error) {
		called = true
		if dir != s.Worktree || a != from.Head || b != to.Head {
			t.Fatalf("wrong comparison %q %q %q", dir, a, b)
		}
		return gitdiff.Result{Files: []gitdiff.File{{Path: "app.go", Patch: "@@ -1 +1 @@\n-old\n+new\n"}}}, nil
	}
	a, _ = json.Marshal(protocol.ReviewDiffArgs{From: from.ID, To: to.ID})
	req := protocol.Request{Cmd: protocol.CmdReviewDiff, Session: s.ID, Args: a}
	if r := d.handle(context.Background(), req); !r.OK || !called {
		t.Fatalf("diff: %+v", r)
	}
	called = false
	other, err := reviewpack.Publish(d.home, s.ID, s.Worktree, "acme/other", 43, m)
	if err != nil {
		t.Fatal(err)
	}
	req.Args, _ = json.Marshal(protocol.ReviewDiffArgs{From: from.ID, To: other.ID})
	if r := d.handle(context.Background(), req); r.OK || called {
		t.Fatal("compared different repositories")
	}
	read := (&Daemon{home: d.home}).handleReviewPackage(protocol.Request{Cmd: protocol.CmdReviewPackages, Session: s.ID})
	var data protocol.ReviewPackagesData
	if err := json.Unmarshal(read.Data, &data); err != nil || data.Reviewed[s.ID].Package != from.ID || !data.ReviewTracking {
		t.Fatalf("retained review %+v %v", data, err)
	}
}

// TestReviewImageChunkedReadFitsAFrame: the desktop keeps its one-shot data
// URL, while a chunked read hands out parts whose frames stay under the
// remote cap and reassemble to the exact retained bytes.
func TestReviewImageChunkedReadFitsAFrame(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "working")
	s.Worktree, s.Repo = t.TempDir(), "acme/app"
	d.sessions.Upsert(s)
	img := image.NewNRGBA(image.Rect(0, 0, 600, 600))
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range img.Pix {
		img.Pix[i] = byte(rng.Uint32())
	}
	var png bytes.Buffer
	if err := pngenc.Encode(&png, img); err != nil {
		t.Fatal(err)
	}
	if png.Len() <= 2*protocol.ReviewImageChunkBytes {
		t.Fatalf("fixture too small to need three parts: %d", png.Len())
	}
	if err := os.WriteFile(filepath.Join(s.Worktree, "after.png"), png.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	m := reviewpack.Manifest{Version: 1, Title: "Review", Body: "Screens", Base: strings.Repeat("a", 40), Head: strings.Repeat("b", 40), Screens: []reviewpack.Screen{{Title: "Home", After: "after.png"}}}
	p, err := reviewpack.Publish(d.home, s.ID, s.Worktree, s.Repo, 42, m)
	if err != nil {
		t.Fatal(err)
	}
	read := func(args protocol.ReviewImageArgs) protocol.Response {
		raw, _ := json.Marshal(args)
		return d.handle(context.Background(), protocol.Request{Cmd: protocol.CmdReviewImage, Session: s.ID, Args: raw})
	}
	whole := read(protocol.ReviewImageArgs{Package: p.ID, Artifact: p.Screens[0].After})
	var full protocol.ReviewImageData
	if err := json.Unmarshal(whole.Data, &full); err != nil || full.DataURL != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(png.Bytes()) || full.Data != nil {
		t.Fatalf("desktop read changed: %v", err)
	}
	var got []byte
	for offset, parts := int64(0), 0; ; parts++ {
		r := read(protocol.ReviewImageArgs{Package: p.ID, Artifact: p.Screens[0].After, Chunked: true, Offset: offset})
		if !r.OK {
			t.Fatal(r.Error)
		}
		frame := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameResp, ID: "x"}
		if err := frame.SetPayload(r); err != nil {
			t.Fatal(err)
		}
		if enc, _ := json.Marshal(frame); len(enc) > protocol.MaxFrameBytes {
			t.Fatalf("part %d is %d bytes, over the frame cap", parts, len(enc))
		}
		var part protocol.ReviewImageData
		if err := json.Unmarshal(r.Data, &part); err != nil || part.DataURL != "" || part.Mime != "image/png" || part.Size != int64(png.Len()) || part.Offset != offset {
			t.Fatalf("part %d: %+v %v", parts, part.Offset, err)
		}
		got = append(got, part.Data...)
		if part.EOF {
			break
		}
		offset += int64(len(part.Data))
		if parts > 10 {
			t.Fatal("no end of file")
		}
	}
	if !bytes.Equal(got, png.Bytes()) {
		t.Fatal("chunks do not reassemble to the retained image")
	}
	for _, bad := range []int64{-1, 7, int64(png.Len()) + protocol.ReviewImageChunkBytes} {
		if r := read(protocol.ReviewImageArgs{Package: p.ID, Artifact: p.Screens[0].After, Chunked: true, Offset: bad}); r.OK {
			t.Fatalf("offset %d accepted", bad)
		}
	}
}
