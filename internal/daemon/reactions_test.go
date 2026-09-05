package daemon

// Tests for the P3 reaction engine (reactions.go): the merged-cleanup loop
// close, ci_failed re-prompt + defer + escalation, changes_requested and
// merge_conflict one-shot sends, approved park, and the send-keys AtPrompt gate.
//
// All seams are hermetic fakes — no gh, tmux, git, or network. fakeReactSeams
// stands in for tmux send-keys, the scm reaction-content fetchers, and the
// notifier so every send / notify is observable.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
	"github.com/sushidev-team/lola/internal/worktree"
)

// sendKeysCall records one send-keys into a pane.
type sendKeysCall struct{ name, text string }

// fakeReactSeams installs counting fakes for every external effect the reaction
// engine drives. It doubles as the notify.Notifier.
type fakeReactSeams struct {
	mu         sync.Mutex
	sends      []sendKeysCall
	sendErr    error
	failing    string
	failingErr error
	review     string
	reviewErr  error
	notes      []notify.Note
}

func (f *fakeReactSeams) install(d *Daemon) {
	d.sendKeys = func(_ context.Context, name, text string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.sends = append(f.sends, sendKeysCall{name, text})
		return f.sendErr
	}
	d.failingChecks = func(_ context.Context, _ string, _ int) (string, error) {
		return f.failing, f.failingErr
	}
	d.reviewComments = func(_ context.Context, _ string, _ int) (string, error) {
		return f.review, f.reviewErr
	}
	// A resting pane by default: handoffPromptProof now captures the pane for
	// EVERY hand-off (no AtPromptVerified short-circuit), so a seam set that left
	// the real tmux capture in place would defer every send. Tests that care about
	// the pane assign d.paneTail AFTER install and override this.
	d.paneTail = func(context.Context, string, int) (string, error) { return paneWaiting, nil }
	d.notifier = f
}

func (f *fakeReactSeams) Notify(_ context.Context, n notify.Note) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notes = append(f.notes, n)
}

func (f *fakeReactSeams) sendCalls() []sendKeysCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sendKeysCall(nil), f.sends...)
}

func (f *fakeReactSeams) notesByPriority(p notify.Priority) []notify.Note {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []notify.Note
	for _, n := range f.notes {
		if n.Priority == p {
			out = append(out, n)
		}
	}
	return out
}

func (f *fakeReactSeams) noteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.notes)
}

// reactTestConfig is a native test config with the default (enabled) reactions
// wired in — testConfig leaves Reactions at its zero value (all auto=false).
func reactTestConfig(polls ...config.Project) *config.Config {
	c := nativeTestConfig(polls...)
	c.Reactions = config.ReactionsConfig{
		CIFailed:         config.Reaction{Auto: true, Retries: config.DefaultCIRetries, Message: config.DefaultCIFailedMessage},
		ChangesRequested: config.Reaction{Auto: true, Message: config.DefaultChangesRequestedMessage},
		MergeConflict:    config.Reaction{Auto: true, Message: config.DefaultMergeConflictMessage},
		ApprovedAndGreen: config.Reaction{Auto: false},
		Merged:           config.Reaction{Auto: true},
	}
	return c
}

// reactSess builds a native session in a given status with an open PR.
func reactSess(ident, status string, pr *scm.PR) session.Session {
	s := nativeSess(ident, status)
	s.IssueUUID = "uuid-" + strings.ToLower(ident)
	s.PR = pr
	return s
}

func openPR(number int, mergeable, review, checks string) *scm.PR {
	return &scm.PR{
		Number:         number,
		URL:            "https://github.com/acme/widgets/pull/" + itoa(number),
		State:          "OPEN",
		Mergeable:      mergeable,
		ReviewDecision: review,
		ChecksState:    checks,
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// --- merged → cleanup + notify Info -------------------------------------------

func TestReactMergedCleansUpAndNotifies(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	seams := &fakeReactSeams{}
	seams.install(d)

	pr := openPR(7, "MERGEABLE", "APPROVED", "pass")
	pr.State = "MERGED"
	s := reactSess("FE-1", "merged", pr)
	d.sessions.Upsert(s)
	d.inflight.Add(s.IssueUUID, s.Issue)

	d.react(context.Background(), s)

	calls := nat.killCalls()
	if len(calls) != 1 || !calls[0].removeWorktree || calls[0].force {
		t.Fatalf("merged cleanup Kill = %+v, want one {removeWorktree:true, force:false}", calls)
	}
	if _, ok := d.sessions.Get(s.ID); ok {
		t.Error("merged session must be dropped from the store")
	}
	if d.inflight.Has(s.IssueUUID) {
		t.Error("merged cleanup must free the in-flight claim")
	}
	if info := seams.notesByPriority(notify.Info); len(info) != 1 {
		t.Fatalf("want exactly one Info notification, got %d (%+v)", len(info), seams.notes)
	}
	if len(seams.sendCalls()) != 0 {
		t.Error("merged cleanup must never send-keys")
	}
	// The Info note names the branch that went with the worktree, so a merged
	// cleanup reads as complete rather than as "something was removed".
	if body := seams.notesByPriority(notify.Info)[0].Body; !strings.Contains(body, s.Branch) {
		t.Errorf("merged notification should name the deleted branch %q, got %q", s.Branch, body)
	}
}

// A pr-kind session's branch is upstream, so the cleanup must not claim to have
// deleted it (runtime.Kill does not, via Session.OwnsBranch).
func TestReactMergedPRKindDoesNotClaimBranchDeletion(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	seams := &fakeReactSeams{}
	seams.install(d)

	pr := openPR(7, "MERGEABLE", "APPROVED", "pass")
	pr.State = "MERGED"
	s := reactSess("FE-1", "merged", pr)
	s.Kind = session.KindPR
	d.sessions.Upsert(s)

	d.react(context.Background(), s)

	info := seams.notesByPriority(notify.Info)
	if len(info) != 1 {
		t.Fatalf("want one Info notification, got %d", len(info))
	}
	if strings.Contains(info[0].Body, "branch") {
		t.Errorf("must not mention branch deletion for a pr session, got %q", info[0].Body)
	}
}

// A merged PR whose worktree is dirty keeps the checkout on disk (Kill refuses
// with ErrDirty) but STILL drops the store entry and frees the in-flight claim,
// so a dirty merge does not linger in the sessions view forever. The operator is
// notified once that the worktree was kept.
func TestReactMergedDirtyDropsEntryKeepsWorktree(t *testing.T) {
	nat := &fakeNative{killErr: worktree.ErrDirty}
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	seams := &fakeReactSeams{}
	seams.install(d)

	pr := openPR(7, "MERGEABLE", "APPROVED", "pass")
	pr.State = "MERGED"
	s := reactSess("FE-1", "merged", pr)
	d.sessions.Upsert(s)
	d.inflight.Add(s.IssueUUID, s.Issue)

	d.react(context.Background(), s)

	calls := nat.killCalls()
	if len(calls) != 1 || !calls[0].removeWorktree || calls[0].force {
		t.Fatalf("dirty merged Kill = %+v, want one {removeWorktree:true, force:false}", calls)
	}
	if _, ok := d.sessions.Get(s.ID); ok {
		t.Error("a dirty merged session must still be dropped from the store")
	}
	if d.inflight.Has(s.IssueUUID) {
		t.Error("a dirty merged cleanup must free the in-flight claim")
	}
	info := seams.notesByPriority(notify.Info)
	if len(info) != 1 {
		t.Fatalf("want exactly one Info notification, got %d (%+v)", len(info), seams.notes)
	}
	if !strings.Contains(info[0].Body, "kept") {
		t.Errorf("dirty-merge notification should say the worktree was kept, got %q", info[0].Body)
	}
}

// A merged cleanup that fails with a NON-dirty error keeps the store entry
// un-dropped and un-notified, so the next cycle retries — reactMerged's
// idempotence is drop-on-success, not a one-shot guard (the old
// LastReactedStatus=="merged" guard could never be stamped: success removes
// the record it would have been stamped on).
func TestReactMergedRetriesOnError(t *testing.T) {
	nat := &fakeNative{killErr: errors.New("tmux exploded")}
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	seams := &fakeReactSeams{}
	seams.install(d)

	pr := openPR(7, "MERGEABLE", "", "pass")
	pr.State = "MERGED"
	s := reactSess("FE-1", "merged", pr)
	d.sessions.Upsert(s)

	d.react(context.Background(), s)
	if len(nat.killCalls()) != 1 {
		t.Fatalf("want one kill attempt, got %d", len(nat.killCalls()))
	}
	if _, ok := d.sessions.Get(s.ID); !ok {
		t.Error("a failed merged cleanup must keep the store entry for the retry")
	}
	if seams.noteCount() != 0 {
		t.Error("a failed merged cleanup must not notify (nothing was cleaned)")
	}

	// A LATER cycle retries the still-merged session. "Later" is now literal:
	// the retry is spaced by cleanupBackoff off the delivery anchor, so the test
	// rewinds that anchor rather than relying on back-to-back cycles (which is
	// exactly the hammering this schedule exists to stop — see
	// TestMergedCleanupBacksOffBetweenAttempts for the other half).
	got := ageMergedAnchor(t, d, s.ID, 24*time.Hour)
	d.react(context.Background(), got)
	if len(nat.killCalls()) != 2 {
		t.Errorf("want the retry's second kill attempt, got %d", len(nat.killCalls()))
	}
}

// --- merged cleanup: bounded, backed-off retry --------------------------------
//
// The unbounded version of this loop was found in a real 20MB daemon.log: 1971
// retries across 19 sessions, 1953 of them for ONE session over ~16 hours, each
// logging the same `git worktree remove … exit status 255` line and notifying
// nobody. These tests pin the three properties that replaced it — the failures
// are counted, the attempts are spaced, and the give-up is loud — plus the one
// property that must NOT change: a dirty worktree is still never forced.

// ageMergedAnchor rewinds the stored session's delivery anchor (DeliverySince —
// the moment its PR became merged), which is what cleanupRetryDue measures the
// backoff schedule from. It returns the updated record, which is what a test
// hands to react (the observer likewise reacts on the CURRENT record, not on a
// caller's stale copy).
func ageMergedAnchor(t *testing.T, d *Daemon, id string, age time.Duration) session.Session {
	t.Helper()
	got, ok := d.sessions.Update(id, func(cur *session.Session) bool {
		cur.DeliverySince = time.Now().Add(-age)
		return true
	})
	if !ok {
		t.Fatalf("session %s is not in the store", id)
	}
	return got
}

// failCleanupCycle runs one merged-cleanup attempt with the schedule rewound far
// enough that the backoff can never be what suppressed it — so a missing Kill
// call in these tests always means the ATTEMPT BUDGET stopped it.
func failCleanupCycle(t *testing.T, d *Daemon, id string) {
	t.Helper()
	d.react(context.Background(), ageMergedAnchor(t, d, id, 24*time.Hour))
}

// mergedFailingCleanup wires a daemon whose merged cleanup always fails with a
// non-dirty error — the shape of the real incident.
func mergedFailingCleanup(t *testing.T) (*Daemon, *fakeNative, *fakeReactSeams, session.Session) {
	t.Helper()
	nat := &fakeNative{killErr: errors.New("git worktree remove: exit status 255: error: failed to delete")}
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	seams := &fakeReactSeams{}
	seams.install(d)

	pr := openPR(7, "MERGEABLE", "APPROVED", "pass")
	pr.State = "MERGED"
	s := reactSess("FE-1", "merged", pr)
	d.sessions.Upsert(s)
	d.inflight.Add(s.IssueUUID, s.Issue)
	return d, nat, seams, s
}

// Consecutive failures accumulate on the record (they are what the backoff and
// the give-up are both keyed off), the entry is kept for the retry, and nothing
// is reported to the operator while the budget still has room.
func TestMergedCleanupFailuresAccumulate(t *testing.T) {
	d, nat, seams, s := mergedFailingCleanup(t)

	for i := 1; i <= 3; i++ {
		failCleanupCycle(t, d, s.ID)
		got, ok := d.sessions.Get(s.ID)
		if !ok {
			t.Fatalf("attempt %d: a failed cleanup must keep the store entry for the retry", i)
		}
		if got.CleanupFailures != i {
			t.Fatalf("after attempt %d, CleanupFailures = %d, want %d", i, got.CleanupFailures, i)
		}
	}
	if n := len(nat.killCalls()); n != 3 {
		t.Errorf("want 3 kill attempts, got %d", n)
	}
	for i, c := range nat.killCalls() {
		if c.force {
			t.Errorf("attempt %d passed force=true; a merged cleanup must never force", i+1)
		}
	}
	if seams.noteCount() != 0 {
		t.Errorf("a retry inside the budget must not notify, got %d note(s)", seams.noteCount())
	}
}

// Back-to-back cycles do NOT re-attempt: after a failure the next attempt is
// owed cleanupBackoff, measured from the delivery anchor. This is the property
// whose absence produced ~1953 attempts for one session.
func TestMergedCleanupBacksOffBetweenAttempts(t *testing.T) {
	d, nat, _, s := mergedFailingCleanup(t)

	// First attempt: no failures yet, so it is always due.
	got, _ := d.sessions.Get(s.ID)
	d.react(context.Background(), got)
	if n := len(nat.killCalls()); n != 1 {
		t.Fatalf("want the first attempt, got %d kill call(s)", n)
	}

	// The observer's next cycle. One failure owes nothing (the free retry), so
	// this one still runs — the contract the merged cleanup has always had.
	got, _ = d.sessions.Get(s.ID)
	d.react(context.Background(), got)
	if n := len(nat.killCalls()); n != 2 {
		t.Fatalf("the first retry must still run on the next cycle, got %d kill call(s)", n)
	}

	// From the SECOND failure the wait bites: the anchor is seconds old and
	// cleanupBackoffTotal(2) is a full observer cadence, so nothing is attempted
	// and — the part that matters — the skipped cycle is not counted as a
	// failure either.
	got, _ = d.sessions.Get(s.ID)
	d.react(context.Background(), got)
	if n := len(nat.killCalls()); n != 2 {
		t.Fatalf("a cycle inside the backoff window must not re-attempt, got %d kill call(s)", n)
	}
	if got, _ := d.sessions.Get(s.ID); got.CleanupFailures != 2 {
		t.Errorf("a skipped cycle must not count as a failure: CleanupFailures = %d, want 2", got.CleanupFailures)
	}

	// Rewind just past that wait: due again.
	d.react(context.Background(), ageMergedAnchor(t, d, s.ID, cleanupBackoffTotal(2)+time.Second))
	if n := len(nat.killCalls()); n != 3 {
		t.Fatalf("want the next attempt once the wait elapsed, got %d kill call(s)", n)
	}

	// Three failures owe cleanupBackoffTotal(3) from the anchor, and the anchor
	// is only just past the two-failure mark — so the next cycle waits again.
	got, _ = d.sessions.Get(s.ID)
	d.react(context.Background(), got)
	if n := len(nat.killCalls()); n != 3 {
		t.Errorf("the wait must grow with the failure count, got %d kill call(s)", n)
	}
}

// After maxCleanupAttempts the engine STOPS and says so exactly once: one Action
// notification naming the session and the last error, and no further attempts —
// but the record stays, because it is the only thing pointing at the worktree
// that still needs a hand.
func TestMergedCleanupGivesUpAndNotifiesOnce(t *testing.T) {
	d, nat, seams, s := mergedFailingCleanup(t)

	for i := 0; i < maxCleanupAttempts; i++ {
		failCleanupCycle(t, d, s.ID)
	}
	if n := len(nat.killCalls()); n != maxCleanupAttempts {
		t.Fatalf("want %d attempts before giving up, got %d", maxCleanupAttempts, n)
	}
	got, ok := d.sessions.Get(s.ID)
	if !ok {
		t.Fatal("a given-up cleanup must KEEP the store entry so the failure stays visible")
	}
	if got.CleanupFailures != maxCleanupAttempts {
		t.Errorf("CleanupFailures = %d, want %d", got.CleanupFailures, maxCleanupAttempts)
	}

	notes := seams.notesByPriority(notify.Action)
	if len(notes) != 1 {
		t.Fatalf("want exactly one Action notification at the give-up moment, got %d (%+v)", len(notes), seams.notes)
	}
	if !strings.Contains(notes[0].Body, s.Issue) {
		t.Errorf("give-up notification must name the session, got %q", notes[0].Body)
	}
	if !strings.Contains(notes[0].Body, "exit status 255") {
		t.Errorf("give-up notification must carry the last error, got %q", notes[0].Body)
	}

	// Every later cycle is inert: no attempt, no second notification, and the
	// record is still there.
	for i := 0; i < 3; i++ {
		failCleanupCycle(t, d, s.ID)
	}
	if n := len(nat.killCalls()); n != maxCleanupAttempts {
		t.Errorf("a given-up cleanup must not attempt again, got %d kill call(s)", n)
	}
	if seams.noteCount() != 1 {
		t.Errorf("the give-up must notify ONCE, got %d note(s)", seams.noteCount())
	}
	if _, ok := d.sessions.Get(s.ID); !ok {
		t.Error("the record must survive every post-give-up cycle")
	}
}

// A given-up session keeps being re-stamped, so the observer's retention prune
// (which runs AFTER react in the same cycle) can never age out the record that
// carries the problem. Without this the pane is already dead, nothing else
// re-writes the record, and 24h later the stranded worktree has no trace left
// in the store at all.
func TestMergedCleanupGiveUpKeepsRecordFresh(t *testing.T) {
	d, nat, _, s := mergedFailingCleanup(t)
	for i := 0; i < maxCleanupAttempts; i++ {
		failCleanupCycle(t, d, s.ID)
	}
	before, ok := d.sessions.Get(s.ID)
	if !ok {
		t.Fatal("record missing after give-up")
	}
	kills := len(nat.killCalls())

	d.react(context.Background(), before)

	after, ok := d.sessions.Get(s.ID)
	if !ok {
		t.Fatal("a post-give-up cycle must keep the record")
	}
	if !after.LastSeen.After(before.LastSeen) {
		t.Errorf("LastSeen must be re-stamped so the retention prune cannot drop the record: %v -> %v",
			before.LastSeen, after.LastSeen)
	}
	if len(nat.killCalls()) != kills {
		t.Error("the keep-alive must not run a cleanup attempt")
	}
}

// The dirty-worktree refusal is untouched by the retry bound: whatever the
// failure count, force is never passed, and a cleanup that reaches ErrDirty
// still keeps the checkout and drops the record.
func TestMergedCleanupNeverForcesAfterFailures(t *testing.T) {
	d, nat, seams, s := mergedFailingCleanup(t)

	failCleanupCycle(t, d, s.ID)
	failCleanupCycle(t, d, s.ID)
	nat.killErr = worktree.ErrDirty
	failCleanupCycle(t, d, s.ID)

	for i, c := range nat.killCalls() {
		if c.force {
			t.Errorf("attempt %d passed force=true; a dirty worktree must never be force-removed", i+1)
		}
	}
	if _, ok := d.sessions.Get(s.ID); ok {
		t.Error("a dirty outcome still drops the store entry, however many attempts preceded it")
	}
	info := seams.notesByPriority(notify.Info)
	if len(info) != 1 || !strings.Contains(info[0].Body, "kept") {
		t.Errorf("want one Info note saying the worktree was kept, got %+v", info)
	}
	if len(seams.notesByPriority(notify.Action)) != 0 {
		t.Error("a dirty outcome inside the budget must not fire the give-up notification")
	}
}

// The schedule itself: one observer cycle after the first failure, doubling,
// capped — and a first attempt that is always due.
func TestCleanupBackoffSchedule(t *testing.T) {
	if got := cleanupBackoff(0); got != 0 {
		t.Errorf("cleanupBackoff(0) = %v, want 0 (the first attempt is immediate)", got)
	}
	if got := cleanupBackoff(1); got != 0 {
		t.Errorf("cleanupBackoff(1) = %v, want 0 — one free retry on the next observer cycle", got)
	}
	if got := cleanupBackoff(2); got != observeInterval {
		t.Errorf("cleanupBackoff(2) = %v, want the observer cadence %v", got, observeInterval)
	}
	if got, want := cleanupBackoff(3), 2*observeInterval; got != want {
		t.Errorf("cleanupBackoff(3) = %v, want %v", got, want)
	}
	if got := cleanupBackoff(maxCleanupAttempts); got != cleanupBackoffCap {
		t.Errorf("cleanupBackoff(%d) = %v, want the cap %v", maxCleanupAttempts, got, cleanupBackoffCap)
	}
	prev := time.Duration(0)
	for i := 1; i <= maxCleanupAttempts; i++ {
		d := cleanupBackoff(i)
		if d < prev || d > cleanupBackoffCap {
			t.Fatalf("cleanupBackoff(%d) = %v breaks monotonic-and-capped (prev %v, cap %v)", i, d, prev, cleanupBackoffCap)
		}
		prev = d
	}
	// The cumulative schedule is clamped, so a corrupted counter cannot push the
	// next attempt arbitrarily far out (or spin the loop that computes it).
	if got, want := cleanupBackoffTotal(1_000_000), cleanupBackoffTotal(maxCleanupAttempts); got != want {
		t.Errorf("cleanupBackoffTotal is not clamped: %v vs %v", got, want)
	}
}

func TestCleanupRetryDue(t *testing.T) {
	now := time.Now()

	// No failures yet: always due, anchor or not.
	if !cleanupRetryDue(session.Session{}, now) {
		t.Error("the first cleanup attempt must always be due")
	}
	// No anchor: fail OPEN — retry as the unbounded loop did; the attempt
	// budget is what stops such a record.
	if !cleanupRetryDue(session.Session{CleanupFailures: 3}, now) {
		t.Error("an anchorless record must fail open and retry")
	}

	// One failure owes nothing: the free retry runs on the next cycle.
	if !cleanupRetryDue(session.Session{CleanupFailures: 1, DeliverySince: now}, now) {
		t.Error("the first retry must be due on the next cycle")
	}

	s := session.Session{CleanupFailures: 2, DeliverySince: now.Add(-cleanupBackoffTotal(2) + time.Second)}
	if cleanupRetryDue(s, now) {
		t.Error("an attempt inside the backoff window must not be due")
	}
	s.DeliverySince = now.Add(-cleanupBackoffTotal(2) - time.Second)
	if !cleanupRetryDue(s, now) {
		t.Error("an attempt past the backoff window must be due")
	}
}

// The streak is per merged EPISODE: a session that stops being merged (a
// reopened PR) starts its next cleanup with a full budget rather than inheriting
// a spent one. This is the only partial progress the engine can observe — a
// successful or dirty cleanup drops the record outright.
func TestResetReactionGuardsClearsCleanupStreak(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)

	s := nativeSess("FE-4", "working")
	s.CleanupFailures = 5
	d.sessions.Upsert(s)

	got, _ := d.sessions.Get(s.ID)
	d.react(context.Background(), got)

	after, ok := d.sessions.Get(s.ID)
	if !ok {
		t.Fatal("session missing")
	}
	if after.CleanupFailures != 0 {
		t.Errorf("CleanupFailures = %d, want 0 once the session is no longer merged", after.CleanupFailures)
	}
}

// --- ci_failed → send-keys with logs, retries increment, notify Action --------

func TestReactCIFailedAtPromptSendsLogs(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{failing: "FAILING-LOG-XYZ"}
	seams.install(d)

	s := reactSess("FE-1", "ci_failed", openPR(9, "MERGEABLE", "", "fail"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.react(context.Background(), s)

	calls := seams.sendCalls()
	if len(calls) != 1 {
		t.Fatalf("want one send-keys, got %d", len(calls))
	}
	if calls[0].name != s.TmuxName {
		t.Errorf("send-keys target = %q, want %q", calls[0].name, s.TmuxName)
	}
	if !strings.Contains(calls[0].text, "FAILING-LOG-XYZ") {
		t.Errorf("send-keys text must include the failing logs, got %q", calls[0].text)
	}
	if !strings.Contains(calls[0].text, "#9") {
		t.Errorf("send-keys text must include the PR ref #9, got %q", calls[0].text)
	}
	got, _ := d.sessions.Get(s.ID)
	if got.CIRetries != 1 {
		t.Errorf("CIRetries = %d, want 1 after one send", got.CIRetries)
	}
	if got.AtPrompt {
		t.Error("AtPrompt must be consumed (false) after a send")
	}
	if got.LastReactedStatus != "ci_failed" {
		t.Errorf("LastReactedStatus = %q, want ci_failed", got.LastReactedStatus)
	}
	if len(seams.notesByPriority(notify.Action)) != 1 {
		t.Errorf("want one Action notification, got %+v", seams.notes)
	}
}

// ci_failed while the agent is mid-turn defers (no send-keys, PendingReaction
// recorded); once AtPrompt flips true the next cycle fires the send.
func TestReactCIFailedDefersUntilAtPrompt(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{failing: "LOGS"}
	seams.install(d)

	s := reactSess("FE-1", "ci_failed", openPR(9, "MERGEABLE", "", "fail"))
	s.AtPrompt = false
	d.sessions.Upsert(s)

	d.react(context.Background(), s)
	if len(seams.sendCalls()) != 0 {
		t.Fatal("a mid-turn agent must not be sent-keys")
	}
	got, _ := d.sessions.Get(s.ID)
	if got.PendingReaction != "ci_failed" {
		t.Errorf("PendingReaction = %q, want ci_failed (deferred)", got.PendingReaction)
	}
	if got.CIRetries != 0 {
		t.Errorf("CIRetries = %d, want 0 while deferred", got.CIRetries)
	}

	// Agent returns to its prompt (Stop hook) → next cycle fires.
	d.sessions.Update(s.ID, func(cur *session.Session) bool { cur.AtPrompt = true; return true })
	got, _ = d.sessions.Get(s.ID)
	d.react(context.Background(), got)

	if len(seams.sendCalls()) != 1 {
		t.Fatalf("deferred reaction must fire once AtPrompt is true, got %d sends", len(seams.sendCalls()))
	}
	got, _ = d.sessions.Get(s.ID)
	if got.PendingReaction != "" {
		t.Errorf("PendingReaction must clear after firing, got %q", got.PendingReaction)
	}
	if got.CIRetries != 1 {
		t.Errorf("CIRetries = %d, want 1", got.CIRetries)
	}
}

// Retries exhausted (CIRetries >= retries) escalates: Urgent notify, Escalated
// set, and NO further send-keys.
func TestReactCIFailedEscalatesWhenExhausted(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{failing: "LOGS"}
	seams.install(d)

	s := reactSess("FE-1", "ci_failed", openPR(9, "MERGEABLE", "", "fail"))
	s.AtPrompt = true
	s.CIRetries = config.DefaultCIRetries // already at the limit
	d.sessions.Upsert(s)

	d.react(context.Background(), s)

	if len(seams.sendCalls()) != 0 {
		t.Error("an exhausted ci_failed streak must not send-keys again")
	}
	if urgent := seams.notesByPriority(notify.Urgent); len(urgent) != 1 {
		t.Fatalf("want one Urgent escalation notification, got %+v", seams.notes)
	}
	got, _ := d.sessions.Get(s.ID)
	if !got.Escalated {
		t.Error("Escalated must be set after retries are exhausted")
	}

	// A second identical cycle does not re-escalate.
	got, _ = d.sessions.Get(s.ID)
	d.react(context.Background(), got)
	if urgent := seams.notesByPriority(notify.Urgent); len(urgent) != 1 {
		t.Errorf("escalation must fire once, got %d Urgent notes", len(urgent))
	}
}

// --- changes_requested → review comments sent once ----------------------------

func TestReactChangesRequestedSendsOnce(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{review: "REVIEW-FEEDBACK-ABC"}
	seams.install(d)

	s := reactSess("FE-1", "changes_requested", openPR(3, "MERGEABLE", "CHANGES_REQUESTED", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.react(context.Background(), s)
	calls := seams.sendCalls()
	if len(calls) != 1 || !strings.Contains(calls[0].text, "REVIEW-FEEDBACK-ABC") {
		t.Fatalf("want one send-keys carrying the review feedback, got %+v", calls)
	}

	// Second identical cycle (agent still marked at prompt in the record does
	// not matter — the one-shot guard is LastReactedStatus) must not re-send.
	got, _ := d.sessions.Get(s.ID)
	d.react(context.Background(), got)
	if len(seams.sendCalls()) != 1 {
		t.Errorf("changes_requested must send once per transition, got %d sends", len(seams.sendCalls()))
	}
}

// --- merge_conflict → rebase message sent once --------------------------------

func TestReactMergeConflictSendsRebaseOnce(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)

	// DeriveStatus surfaces a conflicting PR as "merge_conflict".
	s := reactSess("FE-1", "merge_conflict", openPR(5, "CONFLICTING", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.react(context.Background(), s)
	calls := seams.sendCalls()
	if len(calls) != 1 || !strings.Contains(strings.ToLower(calls[0].text), "rebase") {
		t.Fatalf("want one rebase send-keys, got %+v", calls)
	}
	got, _ := d.sessions.Get(s.ID)
	if got.LastReactedStatus != "merge_conflict" {
		t.Errorf("LastReactedStatus = %q, want merge_conflict", got.LastReactedStatus)
	}

	got, _ = d.sessions.Get(s.ID)
	d.react(context.Background(), got)
	if len(seams.sendCalls()) != 1 {
		t.Errorf("merge_conflict must send once, got %d sends", len(seams.sendCalls()))
	}
}

// --- approved → notify Action once, no send-keys, session parked --------------

func TestReactApprovedNotifiesAndParks(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	seams := &fakeReactSeams{}
	seams.install(d)

	s := reactSess("FE-1", "approved", openPR(11, "MERGEABLE", "APPROVED", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.react(context.Background(), s)

	if len(seams.sendCalls()) != 0 {
		t.Error("approved must never send-keys")
	}
	if len(nat.killCalls()) != 0 {
		t.Error("approved must park the session, never clean it up")
	}
	if _, ok := d.sessions.Get(s.ID); !ok {
		t.Error("approved session must stay in the store (parked)")
	}
	action := seams.notesByPriority(notify.Action)
	if len(action) != 1 {
		t.Fatalf("want one Action notification, got %+v", seams.notes)
	}
	if !strings.Contains(action[0].URL, "/pull/11") {
		t.Errorf("approved notification must carry the PR URL, got %q", action[0].URL)
	}

	// One-shot: a second identical cycle does not re-notify.
	got, _ := d.sessions.Get(s.ID)
	d.react(context.Background(), got)
	if len(seams.notesByPriority(notify.Action)) != 1 {
		t.Errorf("approved must notify once, got %d Action notes", len(seams.notesByPriority(notify.Action)))
	}
}

// --- send-keys payload sanitization (control chars can't submit mid-payload) ---

// A reaction payload carrying carriage returns and ANSI escapes (routine in
// `gh run view --log-failed` output, and injectable via PR/review bodies) must
// reach the pane stripped of any \r / ANSI / other control byte: a bare \r is
// indistinguishable from the transport's own submit Enter and would fragment
// the prompt (defeating the AtPrompt gate in transport). LF and TAB survive —
// reaction templates are intentionally multi-line.
func TestReactSanitizesControlBytesBeforeSend(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{
		// Progress-bar carriage returns, an ANSI color sequence, a lone ESC and
		// a NUL, plus a legitimate newline+tab that must be preserved.
		failing: "step 1\rstep 2\r\n\x1b[31mFAILED\x1b[0m\x1b\x00\n\tdetail-KEEP",
	}
	seams.install(d)

	s := reactSess("FE-1", "ci_failed", openPR(9, "MERGEABLE", "", "fail"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.react(context.Background(), s)

	calls := seams.sendCalls()
	if len(calls) != 1 {
		t.Fatalf("want one send-keys, got %d", len(calls))
	}
	got := calls[0].text
	if strings.ContainsRune(got, '\r') {
		t.Errorf("payload must not contain CR (submit vector): %q", got)
	}
	if strings.ContainsRune(got, '\x1b') || strings.Contains(got, "[31m") || strings.Contains(got, "[0m") {
		t.Errorf("payload must be stripped of ANSI escapes: %q", got)
	}
	if strings.ContainsRune(got, '\x00') {
		t.Errorf("payload must not contain other control bytes: %q", got)
	}
	if !strings.Contains(got, "FAILED") || !strings.Contains(got, "detail-KEEP") {
		t.Errorf("payload must keep visible text: %q", got)
	}
	if !strings.Contains(got, "\n\tdetail-KEEP") {
		t.Errorf("payload must preserve legitimate LF and TAB: %q", got)
	}
}

// --- a waiting agent no longer hides the PR ----------------------------------
//
// react dispatches on the DELIVERY axis, so an agent blocked on a human is
// simply orthogonal to where its PR stands. Two halves are pinned here: the
// guards a red or changes-requested PR already stamped survive the excursion
// (they always had to — the old code bought that with an explicit
// `Status == "needs_input"` bail-out in resetReactionGuards), and the reaction
// itself still REACHES the engine, which under the rollup it could not.

// An escalated ci_failed session whose agent is also waiting on a human must
// keep its CIRetries streak, Escalated backstop, and one-shot LastReactedStatus
// guard — otherwise every excursion re-arms the retry budget and re-escalates
// forever.
func TestReactWaitingAgentDoesNotResetCIStreak(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{failing: "LOGS"}
	seams.install(d)

	// Escalated red PR: retries spent, human paged, one-shot guard stamped.
	s := reactSess("FE-1", "needs_input", openPR(9, "MERGEABLE", "", "fail"))
	s.CIRetries = config.DefaultCIRetries
	s.Escalated = true
	s.LastReactedStatus = "ci_failed"
	d.sessions.Upsert(s)

	d.react(context.Background(), s)

	got, _ := d.sessions.Get(s.ID)
	if got.CIRetries != config.DefaultCIRetries {
		t.Errorf("CIRetries reset while the agent waited: got %d, want %d", got.CIRetries, config.DefaultCIRetries)
	}
	if !got.Escalated {
		t.Error("Escalated cleared while the agent waited — escalation backstop defeated")
	}
	if got.LastReactedStatus != "ci_failed" {
		t.Errorf("LastReactedStatus cleared while the agent waited: got %q", got.LastReactedStatus)
	}
	if len(seams.sendCalls()) != 0 {
		t.Error("an escalated session must not send-keys")
	}
}

// The same excursion must not clear the one-shot guard for a review/rebase
// send, which would re-send the feedback when the agent returns to its prompt.
func TestReactWaitingAgentPreservesChangesRequestedGuard(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{review: "REVIEW"}
	seams.install(d)

	s := reactSess("FE-1", "needs_input", openPR(3, "MERGEABLE", "CHANGES_REQUESTED", "pass"))
	s.LastReactedStatus = "changes_requested"
	d.sessions.Upsert(s)

	d.react(context.Background(), s)

	got, _ := d.sessions.Get(s.ID)
	if got.LastReactedStatus != "changes_requested" {
		t.Errorf("changes_requested guard cleared while the agent waited: got %q", got.LastReactedStatus)
	}
}

// A DEAD pane still suppresses everything but the merged cleanup. The rollup
// used to deliver that for free (rule 2 collapsed every non-merged dead session
// to "dead"); react restates it, because SetAgentState leaves AtPrompt open
// when a pane dies and the send path would otherwise consume that gate and type
// into a tmux session that is not there.
func TestReactDeadPaneSuppressesDeliveryReactions(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{failing: "LOGS", review: "REVIEW"}
	seams.install(d)

	for _, pr := range []*scm.PR{
		openPR(11, "MERGEABLE", "", "fail"),              // ci_failed
		openPR(12, "CONFLICTING", "", "pass"),            // merge_conflict
		openPR(13, "MERGEABLE", "CHANGES_REQUESTED", ""), // changes_requested
		openPR(14, "MERGEABLE", "APPROVED", "pass"),      // approved
		{Number: 15, State: "CLOSED", URL: "u"},          // closed
	} {
		s := reactSess("FE-1", "dead", pr)
		s.AgentState = state.AgentDead
		s.AtPrompt = true // the stale gate a dying pane leaves behind
		s.AtPromptVerified = true
		d.sessions.Upsert(s)

		d.react(context.Background(), s)

		if n := len(seams.sendCalls()); n != 0 {
			t.Fatalf("PR #%d: dead pane must never be typed into, got %d sends", pr.Number, n)
		}
		if n := seams.noteCount(); n != 0 {
			t.Fatalf("PR #%d: dead pane must not notify, got %d notes", pr.Number, n)
		}
		got, _ := d.sessions.Get(s.ID)
		if got.LastReactedStatus != "" || got.PendingReaction != "" {
			t.Fatalf("PR #%d: guards stamped for a dead pane: reacted=%q pending=%q",
				pr.Number, got.LastReactedStatus, got.PendingReaction)
		}
	}
}

// The other half: a waiting agent no longer SUPPRESSES the reaction. Under the
// rolled-up status these two sessions read "needs_input", fell into react's
// default branch, and the engine never saw the PR at all — the approved PR was
// never announced and the red one was never queued. Both now dispatch off the
// delivery axis.
func TestReactWaitingAgentDoesNotSuppressTheDeliveryReaction(t *testing.T) {
	t.Run("approved is still announced", func(t *testing.T) {
		d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
		seams := &fakeReactSeams{}
		seams.install(d)

		s := reactSess("FE-1", "needs_input", openPR(7, "MERGEABLE", "APPROVED", "pass"))
		d.sessions.Upsert(s)
		if got := state.Rollup(state.AgentWaitingInput, state.DeliveryApproved); got != "needs_input" {
			t.Fatalf("precondition: Rollup = %q, want needs_input", got)
		}

		d.react(context.Background(), s)

		if n := len(seams.notesByPriority(notify.Action)); n != 1 {
			t.Fatalf("want one approved notification, got %d", n)
		}
		got, _ := d.sessions.Get(s.ID)
		if got.LastReactedStatus != "approved" {
			t.Errorf("LastReactedStatus = %q, want approved", got.LastReactedStatus)
		}
	})

	t.Run("ci_failed is deferred, not dropped", func(t *testing.T) {
		d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
		seams := &fakeReactSeams{failing: "LOGS"}
		seams.install(d)

		// Waiting on a human, so the send-keys gate is shut: the reaction must
		// be RECORDED as pending for a later cycle rather than silently lost.
		s := reactSess("FE-2", "needs_input", openPR(8, "MERGEABLE", "", "fail"))
		s.AtPrompt = false
		d.sessions.Upsert(s)

		d.react(context.Background(), s)

		got, _ := d.sessions.Get(s.ID)
		if got.PendingReaction != "ci_failed" {
			t.Errorf("PendingReaction = %q, want ci_failed (deferred, not dropped)", got.PendingReaction)
		}
		if len(seams.sendCalls()) != 0 {
			t.Error("a mid-turn agent must never be typed into")
		}
	})
}

// --- one-shot guards reset when the session leaves a reacted state ------------

// After a ci_failed send, a push moves the PR to ci_pending (CIRetries kept),
// and a re-failure re-sends — proving LastReactedStatus resets while CIRetries
// survives the retry loop.
func TestReactCIFailedResetsGuardAcrossRetryLoop(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{failing: "LOGS"}
	seams.install(d)

	s := reactSess("FE-1", "ci_failed", openPR(9, "MERGEABLE", "", "fail"))
	s.AtPrompt = true
	d.sessions.Upsert(s)
	d.react(context.Background(), s) // send #1

	// Agent pushes: CI re-runs → ci_pending. The guard must clear; retries kept.
	// Moved on the DELIVERY axis, which is what react dispatches on — writing
	// Status directly is a bug (the axes and the rollup drift) and the engine
	// would not see it at all.
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		return cur.SetDelivery(state.DeliveryCIPending, time.Now())
	})
	got, _ := d.sessions.Get(s.ID)
	d.react(context.Background(), got)
	got, _ = d.sessions.Get(s.ID)
	if got.LastReactedStatus != "" {
		t.Errorf("LastReactedStatus must clear on leaving ci_failed, got %q", got.LastReactedStatus)
	}
	if got.CIRetries != 1 {
		t.Errorf("CIRetries must survive ci_pending, got %d", got.CIRetries)
	}

	// Re-failure at the prompt → send #2, CIRetries → 2.
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.SetDelivery(state.DeliveryCIFailed, time.Now())
		cur.AtPrompt = true
		return true
	})
	got, _ = d.sessions.Get(s.ID)
	d.react(context.Background(), got)
	if len(seams.sendCalls()) != 2 {
		t.Fatalf("re-failure must re-send, got %d sends", len(seams.sendCalls()))
	}
	got, _ = d.sessions.Get(s.ID)
	if got.CIRetries != 2 {
		t.Errorf("CIRetries = %d, want 2", got.CIRetries)
	}
}

// The feedback changes_requested relays IS a set of review threads (a human's,
// or CodeRabbit's inline comments), so the worker is told to close the ones it
// fixes — after the push, and only those. The other two reactions relay no
// threads and must stay silent about them.
func TestReactChangesRequestedAsksToResolveThreads(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{review: "REVIEW-FEEDBACK-ABC"}
	seams.install(d)

	s := reactSess("FE-1", "changes_requested", openPR(3, "MERGEABLE", "CHANGES_REQUESTED", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)
	d.react(context.Background(), s)

	calls := seams.sendCalls()
	if len(calls) != 1 {
		t.Fatalf("want one send-keys, got %d", len(calls))
	}
	text := calls[0].text
	for _, want := range []string{
		"may also be open as review threads on PR #3 (acme/widgets)",
		"commit and push",
		"resolveReviewThread",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("changes_requested hand-off missing %q:\n%s", want, text)
		}
	}
	// lola's own text sits AFTER the untrusted feedback, never before it.
	if strings.Index(text, "REVIEW-FEEDBACK-ABC") > strings.Index(text, "resolveReviewThread") {
		t.Errorf("the instruction must follow the untrusted feedback:\n%s", text)
	}
}

func TestReactMergeConflictSaysNothingAboutThreads(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)

	s := reactSess("FE-1", "merge_conflict", openPR(4, "CONFLICTING", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)
	d.react(context.Background(), s)

	calls := seams.sendCalls()
	if len(calls) != 1 {
		t.Fatalf("want one send-keys, got %d", len(calls))
	}
	if strings.Contains(calls[0].text, "resolveReviewThread") {
		t.Errorf("a rebase request relays no threads:\n%s", calls[0].text)
	}
}
