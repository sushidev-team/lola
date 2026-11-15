package remote

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// The daemon log is append-only and unrotated, so every line a peer can cause
// without paying a strike or a rate token must go through a bucket. These tests
// flood each such path and count the lines.

const floodFrames = 200

func countLines(log, substr string) int { return strings.Count(log, substr) }

// TestRateLimitedFloodWritesBoundedLog: a device looping a write command past
// its bucket gets CodeRateLimited on every frame and keeps its connection, but
// the audit lines for those refusals are throttled per device.
func TestRateLimitedFloodWritesBoundedLog(t *testing.T) {
	clk := newSyncClock()
	r := newRig(t, withClock(clk))
	limited := 0
	for i := 0; i < floodFrames; i++ {
		r.req("k", "kill", protocol.Request{Session: "x"})
		f := r.next()
		var p protocol.ErrPayload
		if f.Type == protocol.FrameErr && f.DecodePayload(&p) == nil && p.Code == protocol.CodeRateLimited {
			limited++
		}
	}
	if limited < floodFrames-int(writeBurst) {
		t.Fatalf("only %d of %d frames were rate limited", limited, floodFrames)
	}
	r.wantOpen()
	if n := countLines(r.log.all(), "code=rate_limited"); n > int(logBurst) {
		t.Fatalf("%d audit lines for %d rate-limited frames; want at most %d", n, limited, int(logBurst))
	}
	// The trail still says how much it left out once the bucket refills.
	clk.advance(time.Minute)
	for i := 0; i <= int(writeBurst); i++ {
		r.req("k", "kill", protocol.Request{Session: "x"})
		r.next()
	}
	if !strings.Contains(r.log.all(), "similar lines suppressed") {
		t.Fatalf("no suppressed count after the flood:\n%s", r.log.all())
	}
}

// TestErrFrameFloodWritesBoundedLog: a read-only device sending err frames back
// to back writes a bounded number of lines.
func TestErrFrameFloodWritesBoundedLog(t *testing.T) {
	r := newRig(t, withClock(newSyncClock()))
	for i := 0; i < floodFrames; i++ {
		f := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameErr}
		_ = f.SetPayload(protocol.ErrPayload{})
		r.send(f)
	}
	r.req("s", "sessions", nil) // a round trip orders the check after the flood
	if f := r.next(); f.Type != protocol.FrameResp {
		t.Fatalf("frame after the flood = %+v", f)
	}
	if n := countLines(r.log.all(), "reported an error frame"); n > int(logBurst) {
		t.Fatalf("%d lines for %d err frames; want at most %d", n, floodFrames, int(logBurst))
	}
}

// TestEmptyAttachPartIsRefusedBeforeHandler: an attachImage part with no data
// would cost nothing in the byte bucket; it is refused by the field check and
// never reaches the handler.
func TestEmptyAttachPartIsRefusedBeforeHandler(t *testing.T) {
	r := newRig(t)
	for _, data := range [][]byte{nil, make([]byte, protocol.AttachChunkBytes+1)} {
		args, _ := json.Marshal(protocol.AttachImageArgs{Session: "s1", Name: "aaaaaaaa", Mime: "image/png", Data: data})
		r.req("a", protocol.CmdAttachImage, protocol.Request{Session: "s1", Args: args})
		r.wantErr(protocol.CodeDenied)
	}
	if n := r.handler.count(); n != 0 {
		t.Fatalf("a bad attach part reached the handler %d times", n)
	}
	r.wantOpen()
}

func TestAttachPartSentenceMatchesTheChunkSize(t *testing.T) {
	if protocol.AttachChunkBytes != 512<<10 {
		t.Fatal("errAttachPart names 512 KiB; update it with protocol.AttachChunkBytes")
	}
}

// TestScrollIsRateLimited: a scroll execs tmux twice, so a read-only device
// cannot stream them unbounded; the excess is refused on the pane and never
// reaches the bus.
func TestScrollIsRateLimited(t *testing.T) {
	r := newRig(t, withClock(newSyncClock()))
	r.sub("s", "lola-fe-1", 80, 24)
	r.wantResync("lola-fe-1")
	for i := 0; i < int(scrollBurst)+5; i++ {
		r.pty("lola-fe-1", protocol.PTYInputPayload{Action: protocol.PTYActionScroll, Lines: 1})
	}
	for i := 0; i < 5; i++ {
		r.wantErr(protocol.CodeRateLimited)
	}
	waitFor(t, "the admitted scrolls", func() bool { _, _, s := r.bus.snapshot(); return len(s) == int(scrollBurst) })
	time.Sleep(20 * time.Millisecond)
	if _, _, s := r.bus.snapshot(); len(s) != int(scrollBurst) {
		t.Fatalf("bus saw %d scrolls, want %d", len(s), int(scrollBurst))
	}
	r.wantOpen()
}

// TestUnauthenticatedRejectionsAreThrottled: while a pairing window is open any
// certificate passes TLS, so the line for a refused candidate is one any peer
// on the network can cause; a loop of them writes a bounded number of lines.
func TestUnauthenticatedRejectionsAreThrottled(t *testing.T) {
	clk := newSyncClock()
	log := &logCapture{}
	opts := Options{Handle: (&stubHandler{}).handle, Logf: log.logf, Now: clk.now}
	s := newServer(context.Background(), opts, &stubAuth{authErr: ErrUnauthenticated}, nil)
	t.Cleanup(s.cancel)
	for i := 0; i < floodFrames; i++ {
		cli, srv := net.Pipe()
		c := newConn(s, srv)
		c.releasePre = func() {}
		c.run()
		cli.Close()
	}
	if n := countLines(log.all(), "remote: rejected"); n > int(refusalLogBurst) {
		t.Fatalf("%d rejection lines for %d candidates; want at most %d", n, floodFrames, int(refusalLogBurst))
	}
}
