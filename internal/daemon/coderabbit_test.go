package daemon

// Tests for the [coderabbit] PR-comment WATCH (coderabbit.go): the watermark
// advance + fire-once, the routed sinks (notify + sanitized/idle-gated worker
// hand-off), the mid-turn deferral + later flush, and the opt-in / PR-open gates.
//
// All seams are hermetic: fakeCodeRabbit stands in for the scm fetch seam;
// fakeReactSeams (reactions_test.go) provides send-keys + the notifier.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/session"
)

// fakeCodeRabbit installs a counting fake for the daemon's coderabbitComments
// fetch seam, recording the `since` watermark and `author` it was called with.
type fakeCodeRabbit struct {
	mu         sync.Mutex
	calls      int
	text       string
	latest     time.Time
	err        error
	lastSince  time.Time
	lastAuthor string
}

func (f *fakeCodeRabbit) install(d *Daemon) {
	d.coderabbitComments = func(_ context.Context, _ string, _ int, since time.Time, author string) (string, time.Time, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		f.lastSince, f.lastAuthor = since, author
		return f.text, f.latest, f.err
	}
}

func (f *fakeCodeRabbit) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// coderabbitTestConfig is a native test config with the [coderabbit] watch
// enabled (notify + send_to_agent on, comment off — the enabled defaults).
func coderabbitTestConfig(polls ...config.Poll) *config.Config {
	c := nativeTestConfig(polls...)
	c.CodeRabbit = config.CodeRabbitConfig{
		Enabled:     true,
		Author:      config.DefaultCodeRabbitAuthor,
		Notify:      true,
		SendToAgent: true,
	}
	return c
}

// --- happy path: route to worker + notify + advance the watermark -------------

func TestCodeRabbitWatchRoutesAndWatermarks(t *testing.T) {
	d := newTestDaemon(t, coderabbitTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	latest := time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC)
	fcr := &fakeCodeRabbit{text: "New CodeRabbit feedback on PR #7:\n\n[review COMMENTED]\nfix the nil deref", latest: latest}
	fcr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.coderabbitWatch(context.Background(), s)

	// The fetch got the session's (zero) watermark and the configured author.
	if fcr.lastAuthor != config.DefaultCodeRabbitAuthor {
		t.Errorf("author passed to fetch = %q, want %q", fcr.lastAuthor, config.DefaultCodeRabbitAuthor)
	}
	if !fcr.lastSince.IsZero() {
		t.Errorf("first watch must pass a zero `since`, got %v", fcr.lastSince)
	}

	// Watermark advanced to the newest item.
	got, _ := d.sessions.Get(s.ID)
	if !got.LastCodeRabbitAt.Equal(latest) {
		t.Errorf("LastCodeRabbitAt = %v, want %v", got.LastCodeRabbitAt, latest)
	}

	// Handed off to the worker as a SINGLE-LINE pointer to the PR (not the raw
	// comment text), AtPrompt consumed.
	sends := seams.sendCalls()
	if len(sends) != 1 {
		t.Fatalf("want one send-keys hand-off, got %d", len(sends))
	}
	if !strings.Contains(sends[0].text, "PR #7") || !strings.Contains(sends[0].text, "gh pr view 7") {
		t.Errorf("hand-off must be the single-line PR pointer, got %q", sends[0].text)
	}
	if strings.Contains(sends[0].text, "nil deref") || strings.Contains(sends[0].text, "\n") {
		t.Errorf("hand-off must NOT carry the raw multi-line comment, got %q", sends[0].text)
	}
	if got.AtPrompt {
		t.Error("AtPrompt must be consumed after the hand-off")
	}

	// Surfaced to the human once.
	if action := seams.notesByPriority(notify.Action); len(action) != 1 {
		t.Errorf("want one Action notification, got %+v", seams.notes)
	}
}

// --- fire-once: with the watermark advanced, an empty fetch routes nothing -----

func TestCodeRabbitWatchFiresOncePerComment(t *testing.T) {
	d := newTestDaemon(t, coderabbitTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	latest := time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC)
	fcr := &fakeCodeRabbit{text: "feedback: fix it", latest: latest}
	fcr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.coderabbitWatch(context.Background(), s)
	if len(seams.sendCalls()) != 1 {
		t.Fatalf("first watch should hand off once, got %d", len(seams.sendCalls()))
	}

	// Next cycle: the PR has nothing newer than the watermark → empty fetch.
	fcr.mu.Lock()
	fcr.text, fcr.latest = "", latest
	fcr.mu.Unlock()
	s2, _ := d.sessions.Get(s.ID)
	d.coderabbitWatch(context.Background(), s2)

	if got := len(seams.sendCalls()); got != 1 {
		t.Errorf("empty fetch must not re-fire: sends = %d, want 1", got)
	}
	if got := len(seams.notesByPriority(notify.Action)); got != 1 {
		t.Errorf("empty fetch must not re-notify: notes = %d, want 1", got)
	}
	// The 2nd fetch was handed the ADVANCED watermark.
	if !fcr.lastSince.Equal(latest) {
		t.Errorf("second fetch `since` = %v, want the advanced %v", fcr.lastSince, latest)
	}
}

// --- mid-turn: hand-off is deferred, then flushed once the worker is idle ------

func TestCodeRabbitWatchDefersWhenBusyThenFlushes(t *testing.T) {
	d := newTestDaemon(t, coderabbitTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	latest := time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC)
	fcr := &fakeCodeRabbit{text: "feedback: address the review", latest: latest}
	fcr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = false // agent mid-turn
	d.sessions.Upsert(s)

	d.coderabbitWatch(context.Background(), s)

	// Nothing typed; the raw text is stashed; the human is still notified; the
	// watermark still advanced (the stash carries the text forward).
	if got := len(seams.sendCalls()); got != 0 {
		t.Fatalf("must not type into a mid-turn agent, got %d sends", got)
	}
	got, _ := d.sessions.Get(s.ID)
	if got.PendingCodeRabbit == "" {
		t.Error("hand-off must be stashed on PendingCodeRabbit when deferred")
	}
	if !got.LastCodeRabbitAt.Equal(latest) {
		t.Errorf("watermark must advance even when the hand-off is deferred: %v", got.LastCodeRabbitAt)
	}
	if len(seams.notesByPriority(notify.Action)) != 1 {
		t.Errorf("human must be notified even when the worker hand-off is deferred")
	}

	// Worker returns to its prompt; the flush delivers the deferred hand-off.
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.AtPrompt = true
		return true
	})
	d.flushPendingCodeRabbit(context.Background(), s.ID)

	sends := seams.sendCalls()
	if len(sends) != 1 || !strings.Contains(sends[0].text, "PR #7") {
		t.Fatalf("flush must deliver the deferred pointer hand-off, got %+v", sends)
	}
	got, _ = d.sessions.Get(s.ID)
	if got.PendingCodeRabbit != "" {
		t.Error("PendingCodeRabbit must be cleared once delivered")
	}
	if got.AtPrompt {
		t.Error("AtPrompt must be consumed by the flush")
	}
}

// --- gates: off when disabled, or the PR is not open --------------------------

func TestCodeRabbitWatchGates(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
		seams := &fakeReactSeams{}
		seams.install(d)
		fcr := &fakeCodeRabbit{text: "x", latest: time.Now().UTC()}
		fcr.install(d)

		s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
		s.AtPrompt = true
		d.sessions.Upsert(s)
		d.coderabbitWatch(context.Background(), s)

		if fcr.callCount() != 0 {
			t.Errorf("disabled watch must not fetch, got %d calls", fcr.callCount())
		}
		if seams.noteCount() != 0 || len(seams.sendCalls()) != 0 {
			t.Error("disabled watch must not notify or send")
		}
	})

	t.Run("pr not open", func(t *testing.T) {
		d := newTestDaemon(t, coderabbitTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
		seams := &fakeReactSeams{}
		seams.install(d)
		fcr := &fakeCodeRabbit{text: "x", latest: time.Now().UTC()}
		fcr.install(d)

		pr := openPR(7, "MERGEABLE", "", "pass")
		pr.State = "MERGED"
		s := reactSess("FE-1", "merged", pr)
		s.AtPrompt = true
		d.sessions.Upsert(s)
		d.coderabbitWatch(context.Background(), s)

		if fcr.callCount() != 0 {
			t.Errorf("a non-open PR must not be polled, got %d calls", fcr.callCount())
		}
	})
}

// --- manual `lola coderabbit <session>`: force poll ignoring the watermark -----

func TestHandleCodeRabbitForcesIgnoringWatermark(t *testing.T) {
	d := newTestDaemon(t, coderabbitTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	latest := time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC)
	fcr := &fakeCodeRabbit{text: "feedback: fix the thing", latest: latest}
	fcr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	// A watermark already AT the newest item: the observer would find nothing new,
	// but the forced command must still re-surface by polling from zero.
	s.LastCodeRabbitAt = latest
	d.sessions.Upsert(s)

	data, err := d.handleCodeRabbit(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("handleCodeRabbit: %v", err)
	}
	if !data.Ran || !data.Found || data.Skipped != "" {
		t.Errorf("want Ran+Found, got %+v", data)
	}
	if !strings.Contains(data.Comments, "fix the thing") {
		t.Errorf("Comments must carry the feedback, got %q", data.Comments)
	}
	// Forced poll used a zero `since` (ignored the watermark) and still routed.
	if !fcr.lastSince.IsZero() {
		t.Errorf("forced poll must ignore the watermark (zero `since`), got %v", fcr.lastSince)
	}
	if len(seams.sendCalls()) != 1 {
		t.Errorf("forced poll must route to the worker, got %d sends", len(seams.sendCalls()))
	}
}

func TestHandleCodeRabbitSkippedWhenDisabled(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	(&fakeReactSeams{}).install(d)
	fcr := &fakeCodeRabbit{text: "x", latest: time.Now().UTC()}
	fcr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	d.sessions.Upsert(s)

	data, err := d.handleCodeRabbit(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("must not error when the watch is disabled, got %v", err)
	}
	if data.Ran || data.Skipped == "" {
		t.Errorf("disabled watch must yield a skipped outcome, got %+v", data)
	}
	if fcr.callCount() != 0 {
		t.Errorf("disabled watch must not poll, got %d calls", fcr.callCount())
	}
}

func TestHandleCodeRabbitNoneFound(t *testing.T) {
	d := newTestDaemon(t, coderabbitTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	fcr := &fakeCodeRabbit{text: "", latest: time.Time{}} // PR has no CodeRabbit comments
	fcr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	data, err := d.handleCodeRabbit(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("handleCodeRabbit: %v", err)
	}
	if !data.Ran || data.Found || data.Skipped != "" {
		t.Errorf("want Ran + not Found, got %+v", data)
	}
	if len(seams.sendCalls()) != 0 || seams.noteCount() != 0 {
		t.Error("no comments → nothing routed")
	}
}

func TestHandleCodeRabbitUnknownSession(t *testing.T) {
	d := newTestDaemon(t, coderabbitTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	if _, err := d.handleCodeRabbit(context.Background(), "nope"); err == nil {
		t.Fatal("unknown session must error")
	}
}
