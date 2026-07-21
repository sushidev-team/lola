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
	"strings"
	"sync"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
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

// A merged session already reacted to (LastReactedStatus=="merged", e.g. a dirty
// worktree kept) is not cleaned or notified again.
func TestReactMergedFiresOnce(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	seams := &fakeReactSeams{}
	seams.install(d)

	pr := openPR(7, "MERGEABLE", "", "pass")
	pr.State = "MERGED"
	s := reactSess("FE-1", "merged", pr)
	s.LastReactedStatus = "merged"
	d.sessions.Upsert(s)

	d.react(context.Background(), s)
	if len(nat.killCalls()) != 0 {
		t.Error("a merged session already cleaned/kept must not be killed again")
	}
	if seams.noteCount() != 0 {
		t.Error("a merged session already reacted to must not re-notify")
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

// --- needs_input masks a red PR: reaction guards must survive the excursion ---

// An escalated ci_failed session that transiently shows needs_input (a
// permission prompt while the PR is still red) must keep its CIRetries streak,
// Escalated backstop, and one-shot LastReactedStatus guard — otherwise every
// needs_input excursion re-arms the retry budget and re-escalates forever.
func TestReactNeedsInputDoesNotResetCIStreak(t *testing.T) {
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
		t.Errorf("CIRetries reset during needs_input mask: got %d, want %d", got.CIRetries, config.DefaultCIRetries)
	}
	if !got.Escalated {
		t.Error("Escalated cleared during needs_input mask — escalation backstop defeated")
	}
	if got.LastReactedStatus != "ci_failed" {
		t.Errorf("LastReactedStatus cleared during needs_input mask: got %q", got.LastReactedStatus)
	}
	if len(seams.sendCalls()) != 0 {
		t.Error("needs_input must not send-keys")
	}
}

// The same mask must not clear the one-shot guard for a review/rebase send,
// which would re-send the feedback when the agent returns to its prompt.
func TestReactNeedsInputPreservesChangesRequestedGuard(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{review: "REVIEW"}
	seams.install(d)

	s := reactSess("FE-1", "needs_input", openPR(3, "MERGEABLE", "CHANGES_REQUESTED", "pass"))
	s.LastReactedStatus = "changes_requested"
	d.sessions.Upsert(s)

	d.react(context.Background(), s)

	got, _ := d.sessions.Get(s.ID)
	if got.LastReactedStatus != "changes_requested" {
		t.Errorf("changes_requested guard cleared during needs_input mask: got %q", got.LastReactedStatus)
	}
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
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.Status = "ci_pending"
		return true
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
		cur.Status = "ci_failed"
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
