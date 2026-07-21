package daemon

// Tests for the coderabbit-watch shape (reviewer.go's runProviderWatch + the
// coderabbit.go force command), plus multi-provider, self-feedback, and
// Adopt+migration cases that span shapes.
//
// All seams are hermetic: fakeCodeRabbit stands in for the watch fetch seam
// (6-arg, recording the `since`, `author`, and self-login it was called with);
// fakeReactSeams (reactions_test.go) provides send-keys + the notifier.

import (
	"context"
	"io"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/session"
)

// fakeCodeRabbit installs a counting fake for the watch fetch seam, recording the
// watermark, author, and self-login it was called with.
type fakeCodeRabbit struct {
	mu            sync.Mutex
	calls         int
	text          string
	latest        time.Time
	err           error
	lastSince     time.Time
	lastAuthor    string
	lastSelfLogin string
}

func (f *fakeCodeRabbit) install(d *Daemon) {
	d.mu.Lock()
	d.coderabbitComments = func(_ context.Context, _ string, _ int, since time.Time, author, selfLogin string) (string, time.Time, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls++
		f.lastSince, f.lastAuthor, f.lastSelfLogin = since, author, selfLogin
		return f.text, f.latest, f.err
	}
	d.mu.Unlock()
}

func (f *fakeCodeRabbit) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// coderabbitTestConfig is a native test config with the LEGACY [coderabbit] table
// enabled (notify + send_to_agent on, comment off), so setReviewProvidersLocked
// synthesizes a coderabbit-watch provider from it — the back-compat oracle.
func coderabbitTestConfig(polls ...config.Project) *config.Config {
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
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	latest := time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC)
	fcr := &fakeCodeRabbit{text: "New CodeRabbit feedback on PR #7:\n\n[review COMMENTED]\nfix the nil deref", latest: latest}
	fcr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)

	if fcr.lastAuthor != config.DefaultCodeRabbitAuthor {
		t.Errorf("author passed to fetch = %q, want %q", fcr.lastAuthor, config.DefaultCodeRabbitAuthor)
	}
	if !fcr.lastSince.IsZero() {
		t.Errorf("first watch must pass a zero `since`, got %v", fcr.lastSince)
	}
	// No github pass provider is configured, so no self-login filter is applied.
	if fcr.lastSelfLogin != "" {
		t.Errorf("no github provider ⇒ no self-login filter, got %q", fcr.lastSelfLogin)
	}

	got, _ := d.sessions.Get(s.ID)
	if !got.ReviewWatermarks["coderabbit-watch"].Equal(latest) {
		t.Errorf("ReviewWatermarks[watch] = %v, want %v", got.ReviewWatermarks["coderabbit-watch"], latest)
	}

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
	if action := seams.notesByPriority(notify.Action); len(action) != 1 {
		t.Errorf("want one Action notification, got %+v", seams.notes)
	}
}

// --- fire-once: with the watermark advanced, an empty fetch routes nothing -----

func TestCodeRabbitWatchFiresOncePerComment(t *testing.T) {
	d := newTestDaemon(t, coderabbitTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	latest := time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC)
	fcr := &fakeCodeRabbit{text: "feedback: fix it", latest: latest}
	fcr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)
	if len(seams.sendCalls()) != 1 {
		t.Fatalf("first watch should hand off once, got %d", len(seams.sendCalls()))
	}

	fcr.mu.Lock()
	fcr.text, fcr.latest = "", latest
	fcr.mu.Unlock()
	s2, _ := d.sessions.Get(s.ID)
	d.runReviewProviders(context.Background(), s2)

	if got := len(seams.sendCalls()); got != 1 {
		t.Errorf("empty fetch must not re-fire: sends = %d, want 1", got)
	}
	if got := len(seams.notesByPriority(notify.Action)); got != 1 {
		t.Errorf("empty fetch must not re-notify: notes = %d, want 1", got)
	}
	if !fcr.lastSince.Equal(latest) {
		t.Errorf("second fetch `since` = %v, want the advanced %v", fcr.lastSince, latest)
	}
}

// --- mid-turn: hand-off is deferred, then flushed once the worker is idle ------

func TestCodeRabbitWatchDefersWhenBusyThenFlushes(t *testing.T) {
	d := newTestDaemon(t, coderabbitTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	latest := time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC)
	fcr := &fakeCodeRabbit{text: "feedback: address the review", latest: latest}
	fcr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = false // agent mid-turn
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)

	if got := len(seams.sendCalls()); got != 0 {
		t.Fatalf("must not type into a mid-turn agent, got %d sends", got)
	}
	got, _ := d.sessions.Get(s.ID)
	if got.PendingHandoffs["coderabbit-watch"] == "" {
		t.Error("hand-off must be stashed on PendingHandoffs[watch] when deferred")
	}
	if !got.ReviewWatermarks["coderabbit-watch"].Equal(latest) {
		t.Errorf("watermark must advance even when the hand-off is deferred: %v", got.ReviewWatermarks["coderabbit-watch"])
	}
	if len(seams.notesByPriority(notify.Action)) != 1 {
		t.Errorf("human must be notified even when the worker hand-off is deferred")
	}

	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.AtPrompt = true
		return true
	})
	d.flushReviewHandoffs(context.Background(), s.ID)

	sends := seams.sendCalls()
	if len(sends) != 1 || !strings.Contains(sends[0].text, "PR #7") {
		t.Fatalf("flush must deliver the deferred pointer hand-off, got %+v", sends)
	}
	got, _ = d.sessions.Get(s.ID)
	if got.PendingHandoffs["coderabbit-watch"] != "" {
		t.Error("PendingHandoffs[watch] must be cleared once delivered")
	}
	if got.AtPrompt {
		t.Error("AtPrompt must be consumed by the flush")
	}
}

// --- gates: off when disabled, or the PR is not open --------------------------

func TestCodeRabbitWatchGates(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
		syncProviders(d)
		seams := &fakeReactSeams{}
		seams.install(d)
		fcr := &fakeCodeRabbit{text: "x", latest: time.Now().UTC()}
		fcr.install(d)

		s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
		s.AtPrompt = true
		d.sessions.Upsert(s)
		d.runReviewProviders(context.Background(), s)

		if fcr.callCount() != 0 {
			t.Errorf("disabled watch must not fetch, got %d calls", fcr.callCount())
		}
		if seams.noteCount() != 0 || len(seams.sendCalls()) != 0 {
			t.Error("disabled watch must not notify or send")
		}
	})

	t.Run("pr not open", func(t *testing.T) {
		d := newTestDaemon(t, coderabbitTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
		syncProviders(d)
		seams := &fakeReactSeams{}
		seams.install(d)
		fcr := &fakeCodeRabbit{text: "x", latest: time.Now().UTC()}
		fcr.install(d)

		pr := openPR(7, "MERGEABLE", "", "pass")
		pr.State = "MERGED"
		s := reactSess("FE-1", "merged", pr)
		s.AtPrompt = true
		d.sessions.Upsert(s)
		d.runReviewProviders(context.Background(), s)

		if fcr.callCount() != 0 {
			t.Errorf("a non-open PR must not be polled, got %d calls", fcr.callCount())
		}
	})
}

// --- notify=false mutes ONLY the notify sink (the legacy opt-out) --------------

func TestCodeRabbitWatchNotifyFalseMutesNotifyOnly(t *testing.T) {
	cfg := coderabbitTestConfig(nativePoll("p1"))
	cfg.CodeRabbit.Notify = false // the legacy [coderabbit].notify=false opt-out
	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	latest := time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC)
	fcr := &fakeCodeRabbit{text: "feedback: fix it", latest: latest}
	fcr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)

	if seams.noteCount() != 0 {
		t.Errorf("notify=false must mute the notify sink, got %+v", seams.notes)
	}
	if len(seams.sendCalls()) != 1 {
		t.Errorf("notify=false must NOT mute the worker hand-off, got %d sends", len(seams.sendCalls()))
	}
}

// --- multi-provider: cli + claude + watch all run on one PR, independent guards -

func TestMultiProviderIndependentGuards(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	setProviders(d, cliDesc(), claudeDesc(), watchDesc())

	cli := &fakeReview{findings: "CLI-FINDING"}
	claude := &fakeReview{findings: "CLAUDE-FINDING"}
	cli.installKind(d, kindCoderabbitCLI)
	claude.installKind(d, kindClaudeSession)
	latest := time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC)
	fcr := &fakeCodeRabbit{text: "watch feedback", latest: latest}
	fcr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)

	if cli.callCount() != 1 || claude.callCount() != 1 || fcr.callCount() != 1 {
		t.Fatalf("all three providers must run: cli=%d claude=%d watch=%d", cli.callCount(), claude.callCount(), fcr.callCount())
	}
	got, _ := d.sessions.Get(s.ID)
	if got.ReviewedPRs["coderabbit-cli"] != 7 || got.ReviewedPRs["claude-session"] != 7 {
		t.Errorf("each pass kind must stamp its own guard, got %+v", got.ReviewedPRs)
	}
	if !got.ReviewWatermarks["coderabbit-watch"].Equal(latest) {
		t.Errorf("the watch must advance its own watermark, got %v", got.ReviewWatermarks["coderabbit-watch"])
	}
	// One firing never suppresses another: three human notifications fired (one per
	// provider), independent of the single (AtPrompt-consuming) worker hand-off.
	if n := len(seams.notesByPriority(notify.Action)); n != 3 {
		t.Errorf("want three Action notifications (one per provider), got %d", n)
	}
}

// --- self-feedback: login resolved ONCE, threaded into the watch fetch ---------

func TestSelfFeedbackLoginResolvedOnceAndThreaded(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	(&fakeReactSeams{}).install(d)

	// A github-posting cli provider ⇒ the watch must filter lola's own comments.
	cliGithub := cliDesc()
	cliGithub.Transports = config.TransportSet{config.TransportLola, config.TransportGitHub}
	cliGithub.Notify, cliGithub.SendToAgent = false, false
	setProviders(d, cliGithub, watchDesc())

	fl := &fakeLogin{login: "lola-bot"}
	fl.install(d)
	(&fakePostPR{}).install(d)
	latest := time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC)
	fcr := &fakeCodeRabbit{text: "", latest: latest} // nothing new; we only assert the filter args
	fcr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)
	s2, _ := d.sessions.Get(s.ID)
	d.runReviewProviders(context.Background(), s2)

	if fl.count() != 1 {
		t.Errorf("the authed login must be resolved exactly ONCE across cycles, got %d", fl.count())
	}
	if fcr.lastSelfLogin != "lola-bot" {
		t.Errorf("the watch fetch must receive the resolved self-login, got %q", fcr.lastSelfLogin)
	}
}

// --- back-compat: legacy [coderabbit] drives a synthesized watch identically ---

func TestBackCompatLegacyCodeRabbitSynthesized(t *testing.T) {
	d := newTestDaemon(t, coderabbitTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	// The synthesized descriptor is a coderabbit-watch with the legacy author.
	p, ok := d.watchProvider()
	if !ok {
		t.Fatal("legacy [coderabbit] must synthesize a coderabbit-watch provider")
	}
	if p.Author != config.DefaultCodeRabbitAuthor || p.Handoff != handoffPointer || !p.Notify || !p.SendToAgent {
		t.Errorf("synthesized watch = %+v, want author/pointer/notify/send from the legacy table", p)
	}
}

// --- Adopt + migration: an old-scalar snapshot is NOT re-reviewed --------------

func TestAdoptMigratesOldScalarGuardNoReReview(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)

	// Seed an on-disk snapshot in the OLD scalar form (pre-maps): a session that
	// already reviewed PR #7 via the legacy [review] pass.
	seed := session.NewStore(filepath.Join(home, "state"))
	old := nativeSess("FE-1", "idle")
	old.PR = openPR(7, "MERGEABLE", "", "pass")
	old.ReviewedPR = 7 // legacy scalar guard
	seed.Upsert(old)
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}

	// A new daemon loads the snapshot (Store.load runs migrateReviewState, folding
	// the scalar into ReviewedPRs["coderabbit-cli"]).
	d := newDaemon(reviewTestConfig(nativePoll("p1")), &linear.Fake{}, log.New(io.Discard, "", 0), home)
	d.runtimeHealth = func(string) error { return nil }
	d.native = &fakeNative{adopted: []session.Session{
		{ID: old.ID, Source: "native", Project: "p1", Issue: "FE-1", TmuxName: old.ID, Status: "working"},
	}}
	d.adoptNativeSessions(context.Background())

	got := findSession(t, d.sessions.Snapshot(), old.ID)
	if got.ReviewedPRs["coderabbit-cli"] != 7 {
		t.Fatalf("adoption must carry the migrated guard, got ReviewedPRs=%+v", got.ReviewedPRs)
	}

	// With the guard carried, the auto-trigger must NOT re-review PR #7.
	syncProviders(d)
	fr := &fakeReview{findings: "SHOULD-NOT-RUN"}
	fr.install(d)
	d.runReviewProviders(context.Background(), got)
	if fr.callCount() != 0 {
		t.Errorf("an adopted, already-reviewed session must NOT be re-reviewed, got %d execs", fr.callCount())
	}
}

// --- manual `lola coderabbit <session>`: force poll ignoring the watermark -----

func TestHandleCodeRabbitForcesIgnoringWatermark(t *testing.T) {
	d := newTestDaemon(t, coderabbitTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	latest := time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC)
	fcr := &fakeCodeRabbit{text: "feedback: fix the thing", latest: latest}
	fcr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	s.ReviewWatermarks = map[string]time.Time{"coderabbit-watch": latest}
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
	if !fcr.lastSince.IsZero() {
		t.Errorf("forced poll must ignore the watermark (zero `since`), got %v", fcr.lastSince)
	}
	if len(seams.sendCalls()) != 1 {
		t.Errorf("forced poll must route to the worker, got %d sends", len(seams.sendCalls()))
	}
}

func TestHandleCodeRabbitSkippedWhenDisabled(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
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
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	fcr := &fakeCodeRabbit{text: "", latest: time.Time{}}
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
	syncProviders(d)
	if _, err := d.handleCodeRabbit(context.Background(), "nope"); err == nil {
		t.Fatal("unknown session must error")
	}
}
