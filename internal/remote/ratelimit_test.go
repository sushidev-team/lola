package remote

import (
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestLimiter() (*limiter, *fakeClock) {
	c := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	return newLimiter(c.now), c
}

func TestExtCommandsAreTheExternalOnes(t *testing.T) {
	var got []string
	for c := range extCommands {
		got = append(got, c)
	}
	sort.Strings(got)
	want := []string{"addressPRFeedback", "mergePR", "openPr", "openTicket", "prReadiness", "prs", "tickets"}
	if !slices.Equal(got, want) {
		t.Fatalf("extCommands = %v, want %v", got, want)
	}
}

func TestReadBucketArithmetic(t *testing.T) {
	l, c := newTestLimiter()
	for i := 0; i < int(readBurst); i++ {
		if !l.allowReq("d", protocol.TierRead, "sessions", 0, false) {
			t.Fatalf("read %d refused inside the burst", i)
		}
	}
	if l.allowReq("d", protocol.TierRead, "sessions", 0, false) {
		t.Fatal("read allowed past the burst")
	}
	c.advance(100 * time.Millisecond) // 2 tokens at 20/s
	if !l.allowReq("d", protocol.TierRead, "sessions", 0, false) || !l.allowReq("d", protocol.TierRead, "sessions", 0, false) {
		t.Fatal("refill did not restore two reads")
	}
	if l.allowReq("d", protocol.TierRead, "sessions", 0, false) {
		t.Fatal("refill granted more than elapsed time pays for")
	}
	// Another device has its own buckets.
	if !l.allowReq("other", protocol.TierRead, "sessions", 0, false) {
		t.Fatal("buckets leak across devices")
	}
}

func TestWriteBucketAndTierSplit(t *testing.T) {
	l, c := newTestLimiter()
	for i := 0; i < int(writeBurst); i++ {
		if !l.allowReq("d", protocol.TierControl, "send", 0, false) {
			t.Fatalf("write %d refused", i)
		}
	}
	if l.allowReq("d", protocol.TierAnswer, "answer", 0, false) {
		t.Fatal("answer shares the write bucket and must be refused")
	}
	if !l.allowReq("d", protocol.TierRead, "sessions", 0, false) {
		t.Fatal("reads must not be charged to the write bucket")
	}
	c.advance(time.Second)
	if !l.allowReq("d", protocol.TierShell, "x", 0, false) {
		t.Fatal("write bucket did not refill at 1/s")
	}
	if l.allowReq("d", "bogus", "x", 0, false) {
		t.Fatal("an unknown tier must be charged as a write")
	}
}

func TestExtChargedInAddition(t *testing.T) {
	l, c := newTestLimiter()
	for i := 0; i < int(extBurst); i++ {
		if !l.allowReq("d", protocol.TierRead, "prs", 0, false) {
			t.Fatalf("ext %d refused", i)
		}
	}
	if l.allowReq("d", protocol.TierRead, "prs", 0, false) {
		t.Fatal("ext allowed past its burst")
	}
	if !l.allowReq("d", protocol.TierRead, "sessions", 0, false) {
		t.Fatal("non-ext read must survive an empty ext bucket")
	}
	c.advance(5 * time.Second) // 1 token at 0.2/s
	if !l.allowReq("d", protocol.TierRead, "tickets", 0, false) {
		t.Fatal("ext did not refill")
	}
}

func TestRefusalChargesNothing(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < int(extBurst); i++ {
		l.allowReq("d", protocol.TierRead, "prs", 0, false)
	}
	before := l.devs["d"].read.tokens
	if l.allowReq("d", protocol.TierRead, "prs", 0, false) {
		t.Fatal("expected refusal")
	}
	if l.devs["d"].read.tokens != before {
		t.Fatal("a refused request still drained the tier bucket")
	}
}

func TestAttachPartsChargeBytesNotWrites(t *testing.T) {
	l, c := newTestLimiter()
	const part = 4 << 20
	for i := 0; i < 5; i++ { // 20 MiB burst
		if !l.allowReq("d", protocol.TierControl, "attachImage", part, false) {
			t.Fatalf("part %d refused", i)
		}
	}
	if l.allowReq("d", protocol.TierControl, "attachImage", part, false) {
		t.Fatal("bytes allowed past the burst")
	}
	if got := l.devs["d"].write.tokens; got != writeBurst {
		t.Fatalf("non-last parts charged the write bucket: %v", got)
	}
	c.advance(2 * time.Second) // 4 MiB
	if !l.allowReq("d", protocol.TierControl, "attachImage", part, true) {
		t.Fatal("last part refused after refill")
	}
	if got := l.devs["d"].write.tokens; got != writeBurst-1 {
		t.Fatalf("last part must cost exactly one write, tokens=%v", got)
	}
	// An empty write bucket refuses the last part and spends no bytes.
	for i := 0; i < int(writeBurst); i++ {
		l.allowReq("d", protocol.TierControl, "send", 0, false)
	}
	b := l.devs["d"].attach.tokens
	if l.allowReq("d", protocol.TierControl, "attachImage", 1024, true) {
		t.Fatal("last part allowed with an empty write bucket")
	}
	if l.devs["d"].attach.tokens != b {
		t.Fatal("a refused last part still spent bytes")
	}
}

// TestNonFinalAttachPartsPayReadTokens: the write-bucket exemption of a
// non-final part covers its bytes, not the request: each part pays one read
// token, so tiny parts are bounded like any read.
func TestNonFinalAttachPartsPayReadTokens(t *testing.T) {
	l, _ := newTestLimiter()
	for i := 0; i < int(readBurst); i++ {
		if !l.allowReq("d", protocol.TierAnswer, protocol.CmdAttachImage, 1, false) {
			t.Fatalf("part %d refused inside the read burst", i)
		}
	}
	if l.allowReq("d", protocol.TierAnswer, protocol.CmdAttachImage, 1, false) {
		t.Fatal("a non-final part passed with an empty read bucket")
	}
	if got := l.devs["d"].write.tokens; got != writeBurst {
		t.Fatalf("non-final parts charged the write bucket: %v", got)
	}
}

// TestRelayCredPaysTheCredBucket: relayCred is tier read but may mint a bearer
// (a registry write and a hub sync), so it pays credRate on top of the read.
func TestRelayCredPaysTheCredBucket(t *testing.T) {
	l, c := newTestLimiter()
	for i := 0; i < int(credBurst); i++ {
		if !l.allowReq("d", protocol.TierRead, protocol.CmdRelayCred, 0, false) {
			t.Fatalf("relayCred %d refused inside the burst", i)
		}
	}
	if l.allowReq("d", protocol.TierRead, protocol.CmdRelayCred, 0, false) {
		t.Fatal("relayCred allowed past its burst")
	}
	if !l.allowReq("d", protocol.TierRead, "sessions", 0, false) {
		t.Fatal("other reads must survive an empty cred bucket")
	}
	c.advance(time.Duration(float64(time.Second) / credRate))
	if !l.allowReq("d", protocol.TierRead, protocol.CmdRelayCred, 0, false) {
		t.Fatal("cred bucket did not refill")
	}
}

func TestScrollAndLogBuckets(t *testing.T) {
	l, c := newTestLimiter()
	for i := 0; i < int(scrollBurst); i++ {
		if !l.allowScroll("d") {
			t.Fatalf("scroll %d refused", i)
		}
	}
	if l.allowScroll("d") {
		t.Fatal("scroll allowed past the burst")
	}
	for i := 0; i < int(logBurst); i++ {
		if ok, _ := l.allowLog("d"); !ok {
			t.Fatalf("log line %d refused", i)
		}
	}
	for i := 0; i < 7; i++ {
		if ok, _ := l.allowLog("d"); ok {
			t.Fatal("log line allowed past the burst")
		}
	}
	c.advance(time.Duration(float64(time.Second) / logRate))
	if ok, n := l.allowLog("d"); !ok || n != 7 {
		t.Fatalf("after refill: ok=%v suppressed=%d, want true 7", ok, n)
	}
}

func TestPTYAndSub(t *testing.T) {
	l, c := newTestLimiter()
	if !l.allowPTY("d", ptyBytesBurst) {
		t.Fatal("full burst refused")
	}
	if l.allowPTY("d", 1) {
		t.Fatal("pty allowed past the burst")
	}
	c.advance(time.Second)
	if !l.allowPTY("d", ptyBytesRate) || l.allowPTY("d", 1) {
		t.Fatal("pty refill arithmetic wrong")
	}
	for i := 0; i < int(subBurst); i++ {
		if !l.allowSub("d") {
			t.Fatalf("sub %d refused", i)
		}
	}
	if l.allowSub("d") {
		t.Fatal("sub allowed past the burst")
	}
}

func TestForgetAndSweep(t *testing.T) {
	l, c := newTestLimiter()
	l.allowSub("a")
	l.allowSub("b")
	l.allowSub("c")
	l.forget("a")
	if _, ok := l.devs["a"]; ok {
		t.Fatal("forget kept the device")
	}
	c.advance(limiterIdle - time.Second)
	l.sweep(func(string) bool { return false })
	if len(l.devs) != 2 {
		t.Fatal("swept before the idle window")
	}
	c.advance(time.Second)
	l.sweep(func(id string) bool { return id == "b" })
	if _, ok := l.devs["b"]; !ok {
		t.Fatal("swept a device with a live connection")
	}
	if _, ok := l.devs["c"]; ok {
		t.Fatal("kept an idle device with no connection")
	}
}
