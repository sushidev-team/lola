package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/agentfeed"
	"github.com/sushidev-team/lola/internal/attention"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/state"
)

// The rule that a screen command is never typed holds at the daemon, for every client and
// through every spelling the normalizer would turn back into the command.
func TestSayRefusesAScreenCommand(t *testing.T) {
	d, f := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentIdle)
	for _, c := range []protocol.AgentSayArgs{
		{Session: "s1", Text: "/config"},
		{Session: "s1", Text: " /config"},
		{Session: "s1", Text: "\n  /usage"},
		{Session: "s1", Text: "/usage", StopFirst: true},
	} {
		if out, err := d.handleAgentSay(context.Background(), c); err == nil {
			t.Errorf("%+v: said %+v, want a refusal", c, out)
		}
	}
	if got := f.sent(); len(got) != 0 || f.escCount() != 0 {
		t.Fatalf("typed %q and sent %d Escs for a screen command", got, f.escCount())
	}
	if out := say(t, d, "s1", "/compact"); !out.Delivered {
		t.Errorf("an inline command should still go: %+v", out)
	}
	store := talkStore(t, d, "s1")
	store.View(func(st *sshstore.State) {
		if len(st.Queue) != 0 {
			t.Errorf("a refused command left the queue %+v", st.Queue)
		}
	})
}

// An indented inline command is still a command, and it goes without the indent.
func TestSayAnIndentedCommandIsACommand(t *testing.T) {
	d, f := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentIdle)
	say(t, d, "s1", "  /compact")
	if got := f.sent(); len(got) != 1 || got[0] != "/compact" {
		t.Fatalf("typed %q", got)
	}
}

func TestHumanTextKeepsTheShapeOfText(t *testing.T) {
	for in, want := range map[string]string{
		"one\n\n\n\ntwo":            "one\n\n\n\ntwo",
		"  indented\n    more\nend": "  indented\n    more\nend",
		"\n\n  first\n":             "  first",
		"one  \ntwo":                "one  \ntwo",
		"one\r\ntwo\rthree":         "one\ntwo\nthree",
		"a\x1b[31mb\x07c":           "abc",
		"foo\\\\":                   "foo",
		"foo\\ \\":                  "foo",
		"foo \\\n":                  "foo",
		"check C:\\\\":              "check C:",
		"\\":                        "",
	} {
		if got := humanText(in); got != want {
			t.Errorf("humanText(%q) = %q, want %q", in, got, want)
		}
	}
}

// A batch's last entry decides how it ends, so the whole send is cleaned too.
func TestABatchEndingInABackslashIsNotAContinuation(t *testing.T) {
	if got := sanitizeAgentText("first\n\nsecond \\\\ \n"); got != "first\n\nsecond" {
		t.Errorf("sanitizeAgentText = %q", got)
	}
	if got := sanitizeAgentText("keeps a slash\\inside\n"); got != "keeps a slash\\inside\n" {
		t.Errorf("sanitizeAgentText changed text with no trailing backslash: %q", got)
	}
}

// The words exist nowhere else, so a message past the cap is refused whole.
func TestSayOverTheCapIsRefused(t *testing.T) {
	d, f := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentIdle)
	long := strings.Repeat("x", protocol.SSHMaxSayBytes+1)
	if out, err := d.handleAgentSay(context.Background(), protocol.AgentSayArgs{Session: "s1", Text: long}); err == nil {
		t.Fatalf("over the cap said %+v", out)
	}
	if len(f.sent()) != 0 {
		t.Fatalf("typed %d messages", len(f.sent()))
	}
	if out := say(t, d, "s1", strings.Repeat("x", protocol.SSHMaxSayBytes)); !out.Delivered {
		t.Fatalf("at the cap: %+v", out)
	}
}

func TestARespawnedSessionDoesNotInheritTheQueue(t *testing.T) {
	d, f := talkFakeDaemon(t)
	s := talkSession(d, "lola-p1-42", state.AgentWorking)
	s.TranscriptSegments = []string{"11111111-1111-1111-1111-111111111111.jsonl"}
	d.sessions.Upsert(s)
	f.show(talkDraft) // An occupied composer still queues human messages.
	old := say(t, d, s.ID, "also drop the users table")
	d.dropSession(s)
	d.drainTimeline()

	s2 := talkSession(d, "lola-p1-42", state.AgentIdle)
	d.noteSessionStarted(s2)
	d.drainTimeline()

	if cur, _ := d.sessions.Get(s.ID); cur.PendingSSH != "" {
		t.Errorf("PendingSSH = %q", cur.PendingSSH)
	}
	if h := talkHuman(t, d, s.ID, old.Item); !h.Recalled || h.Delivered {
		t.Errorf("the old message should read as recalled: %+v", h)
	}
	f.show(talkResting)
	say(t, d, s.ID, "start with the tests")
	if got := f.sent(); len(got) != 1 || got[0] != "start with the tests" {
		t.Fatalf("typed %q", got)
	}
	// The old transcript stays part of the story, restart or not.
	if cur, _ := d.sessions.Get(s.ID); len(cur.TranscriptSegments) != 1 || cur.TranscriptSegments[0] != s.TranscriptSegments[0] {
		t.Errorf("segments = %q", cur.TranscriptSegments)
	}
}

// A new session's first words can reach the store before its started row does.
func TestABirthKeepsItsOwnFirstWords(t *testing.T) {
	d, f := talkFakeDaemon(t)
	s := talkSession(d, "s1", state.AgentWorking)
	f.show(talkWorking)
	out := say(t, d, s.ID, "first words")
	d.noteSessionStarted(s)
	d.drainTimeline()
	if h := talkHuman(t, d, s.ID, out.Item); h.Recalled {
		t.Errorf("recalled a message from a session that had no earlier life: %+v", h)
	}
}

func TestAWheelInterruptAndStopShareTheDebounce(t *testing.T) {
	ctx := context.Background()
	t.Run("stop then interrupt", func(t *testing.T) {
		d, f := talkFakeDaemon(t)
		talkSession(d, "s1", state.AgentWorking)
		f.show(talkWorking)
		f.onEsc = func(p *talkPaneFake) { p.show(talkResting) }
		if out, err := d.handleAgentStop(ctx, protocol.AgentStopArgs{Session: "s1"}); err != nil || !out.Stopped {
			t.Fatalf("stop: %+v %v", out, err)
		}
		d.sshInterruptAgent(ctx, "s1")
		if n := f.escCount(); n != 1 {
			t.Errorf("%d Escs: the second lands on a resting empty composer and opens Rewind", n)
		}
		d.connWg.Wait() // the stop's settle read, before the test's fast settle is restored
	})
	t.Run("interrupt then stop", func(t *testing.T) {
		d, f := talkFakeDaemon(t)
		talkSession(d, "s1", state.AgentWorking)
		f.show(talkWorking)
		d.sshInterruptAgent(ctx, "s1")
		if out, err := d.handleAgentStop(ctx, protocol.AgentStopArgs{Session: "s1"}); err != nil || out.Stopped {
			t.Errorf("stop after an interrupt: %+v %v", out, err)
		}
		if n := f.escCount(); n != 1 {
			t.Errorf("%d Escs inside the debounce", n)
		}
		// The agent the human just took the wheel from is not restarted by automation.
		if cur, _ := d.sessions.Get("s1"); !time.Now().Before(cur.StopHoldUntil) {
			t.Error("a wheel interrupt left no hold on automation")
		}
	})
	t.Run("a resting pane gets no key", func(t *testing.T) {
		d, f := talkFakeDaemon(t)
		talkSession(d, "s1", state.AgentWorking)
		f.show(talkResting)
		d.sshInterruptAgent(ctx, "s1")
		if n := f.escCount(); n != 0 {
			t.Errorf("%d Escs at rest", n)
		}
	})
}

// Stop & send that could not deliver leaves the agent as stopped as plain Stop does.
func TestStopAndSendKeepsAHoldWhenItCouldNotSend(t *testing.T) {
	old := stopSendWait
	stopSendWait = 20 * time.Millisecond
	defer func() { stopSendWait = old }()
	d, f := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentWorking)
	f.show(talkWorking) // the pane keeps working past the wait
	out, err := d.handleAgentSay(context.Background(), protocol.AgentSayArgs{Session: "s1", Text: "do this instead", StopFirst: true})
	if err != nil || out.Delivered {
		t.Fatalf("say: %+v %v", out, err)
	}
	if cur, _ := d.sessions.Get("s1"); !time.Now().Before(cur.StopHoldUntil) {
		t.Error("nothing holds automation back, so a CI reaction can restart the stopped work first")
	}
}

func TestStopAndSendThatDeliversEndsTheHold(t *testing.T) {
	d, f := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentWorking)
	f.show(talkWorking)
	f.onEsc = func(p *talkPaneFake) { p.show(talkResting) }
	out, err := d.handleAgentSay(context.Background(), protocol.AgentSayArgs{Session: "s1", Text: "do this instead", StopFirst: true})
	if err != nil || !out.Delivered {
		t.Fatalf("say: %+v %v", out, err)
	}
	if cur, _ := d.sessions.Get("s1"); !cur.StopHoldUntil.IsZero() {
		t.Errorf("hold left after delivery: %v", cur.StopHoldUntil)
	}
}

// Esc before the first output hands the message back. The row of the message says so, and
// the next one explains itself by that.
func TestStopAndSendMarksAMessageHandedBack(t *testing.T) {
	old := stopSendWait
	stopSendWait = 20 * time.Millisecond
	defer func() { stopSendWait = old }()
	d, f := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentIdle)
	a := say(t, d, "s1", "first thing")
	if !a.Delivered {
		t.Fatalf("a: %+v", a)
	}
	f.show(talkWorking)
	f.onEsc = func(p *talkPaneFake) { p.show(talkDraft) }
	b, err := d.handleAgentSay(context.Background(), protocol.AgentSayArgs{Session: "s1", Text: "second thing", StopFirst: true})
	if err != nil || b.Delivered {
		t.Fatalf("b: %+v %v", b, err)
	}
	if h := talkHuman(t, d, "s1", a.Item); !h.Returned {
		t.Errorf("the handed back message shows a plain delivered row: %+v", h)
	}
	if h := talkHuman(t, d, "s1", b.Item); h.Delivered || h.Returned {
		t.Errorf("b: %+v", h)
	}
}

// A vim composer that drops out of INSERT on the Esc would read the message as commands. Its
// mode can only be compared across the Esc, so that is what decides.
func TestStopAndSendLeavesAMessageQueuedWhenVimLeftInsert(t *testing.T) {
	old := stopSendWait
	stopSendWait = 20 * time.Millisecond
	defer func() { stopSendWait = old }()
	d, f := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentWorking)
	f.show(talkWorking + "  -- INSERT --\n")
	f.onEsc = func(p *talkPaneFake) { p.show(talkResting) }
	out, err := d.handleAgentSay(context.Background(), protocol.AgentSayArgs{Session: "s1", Text: "do this instead", StopFirst: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Delivered || len(f.sent()) != 0 {
		t.Fatalf("typed into a composer that left INSERT: %+v %q", out, f.sent())
	}
	if out.Why == "" {
		t.Error("the reply should say why")
	}
	cur, _ := d.sessions.Get("s1")
	if !cur.QueueHeld || cur.PendingSSH != "" {
		t.Errorf("held=%v pending=%q: the prompt path must not type it either", cur.QueueHeld, cur.PendingSSH)
	}
}

func TestStopAndSendTypesWhenVimStaysInInsert(t *testing.T) {
	d, f := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentWorking)
	const restingInsert = talkRule + "\n❯ \n" + talkRule + "\n  -- INSERT --\n"
	f.show(talkWorking + "  -- INSERT --\n")
	f.onEsc = func(p *talkPaneFake) {
		p.show(restingInsert)
		p.mu.Lock()
		p.cursor.Y = 1
		p.mu.Unlock()
	}
	out, err := d.handleAgentSay(context.Background(), protocol.AgentSayArgs{Session: "s1", Text: "do this instead", StopFirst: true})
	if err != nil || !out.Delivered {
		t.Fatalf("say: %+v %v", out, err)
	}
}

// Every path that types leaves a row. lola's own lines have no human item to show them, and
// the record of them in the log is hidden as an echo.
func TestLolasOwnQueuedLineLeavesARow(t *testing.T) {
	d, f := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentIdle)
	store, err := d.sshStore("s1", true)
	if err != nil || store == nil {
		t.Fatal(err)
	}
	d.sshEnqueueLola(store, "s1", "[lola] Your command b12 expired.")
	if !d.deliverSSHQueue(context.Background(), "s1") {
		t.Fatal("not delivered")
	}
	if got := f.sent(); len(got) != 1 {
		t.Fatalf("typed %q", got)
	}
	rows := talkEvents(t, d, "s1", protocol.SSHEventHandoff)
	if len(rows) != 1 || strings.Contains(rows[0].Text, "b12") {
		t.Fatalf("handoff rows = %+v", rows)
	}
}

func TestAHumansQueuedWordsLeaveNoHandoffRow(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentIdle)
	say(t, d, "s1", "hello")
	if rows := talkEvents(t, d, "s1", protocol.SSHEventHandoff); len(rows) != 0 {
		t.Fatalf("handoff rows = %+v", rows)
	}
}

// The user_prompt hook fires for what lola types too. Only a person's own prompt is them
// speaking again.
func TestLolasOwnSendDoesNotReleaseAHeldQueue(t *testing.T) {
	d, f := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentWorking)
	f.show(talkDraft) // An occupied composer still queues human messages.
	say(t, d, "s1", "held one")
	d.sessions.Update("s1", func(cur *session.Session) bool {
		cur.QueueHeld = true
		cur.SetAgentState(state.AgentIdle, "", time.Now())
		cur.AtPrompt = true
		return true
	})
	d.sshSyncPending("s1", talkStore(t, d, "s1"))
	f.show(talkResting)
	f.onType = func(*talkPaneFake) {
		d.handleHookEvent(protocol.Request{Cmd: "hookEvent", Session: "s1", Event: "user_prompt"})
	}
	if err := d.typeToAgent(context.Background(), typedNote{Session: "s1", Label: "lola told Claude to fix failing CI"}, "s1", "s1", "fix CI", time.Second); err != nil {
		t.Fatal(err)
	}
	cur, _ := d.sessions.Get("s1")
	if !cur.QueueHeld || cur.PendingSSH != "" {
		t.Fatalf("held=%v pending=%q: lola's own send released the human's hold", cur.QueueHeld, cur.PendingSSH)
	}

	// A prompt typed in the terminal is a person speaking.
	f.onType = nil
	d.handleHookEvent(protocol.Request{Cmd: "hookEvent", Session: "s1", Event: "user_prompt"})
	cur, _ = d.sessions.Get("s1")
	if cur.QueueHeld || cur.PendingSSH == "" {
		t.Fatalf("held=%v pending=%q: the human spoke", cur.QueueHeld, cur.PendingSSH)
	}
}

// A failed send never fired a hook, so it leaves nothing behind to swallow the next one.
func TestAFailedSendLeavesNoMarkForTheHook(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	s := talkSession(d, "s1", state.AgentIdle)
	d.sendKeys = func(context.Context, string, string) error { return os.ErrClosed }
	if err := d.typeToAgent(context.Background(), noteFor(s, ""), "s1", "s1", "x", time.Second); err == nil {
		t.Fatal("no error")
	}
	d.sessions.Update("s1", func(cur *session.Session) bool { cur.QueueHeld = true; return true })
	d.handleHookEvent(protocol.Request{Cmd: "hookEvent", Session: "s1", Event: "user_prompt"})
	if cur, _ := d.sessions.Get("s1"); cur.QueueHeld {
		t.Fatal("a person's prompt did not release the hold after a failed send")
	}
}

// A receipt is written after a snapshot that can outlast the next send, and buildStory orders
// by time, so it carries the moment the Stop hook arrived.
func TestAReceiptKeepsTheTimeOfTheStopHook(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentIdle)
	if _, err := d.sshStore("s1", true); err != nil {
		t.Fatal(err)
	}
	stopAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	d.applyTimeline(timelineEvent{Session: "s1", At: stopAt, Event: protocol.SSHSystemEvent{Kind: protocol.SSHEventCheckpoint, Text: "Done"}})
	talkStore(t, d, "s1").View(func(st *sshstore.State) {
		for _, it := range st.Items {
			if it.System != nil && it.System.Kind == protocol.SSHEventCheckpoint && !it.At.Equal(stopAt) {
				t.Errorf("receipt at %v, want %v", it.At, stopAt)
			}
		}
	})
}

func TestRecordCheckpointStampsTheReceiptWithTheHookTime(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	s := talkSession(d, "s1", state.AgentIdle)
	s.Kind = session.KindLocal
	d.sessions.Upsert(s)
	if _, err := d.sshStore("s1", true); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Hour)
	d.recordCheckpointAt(context.Background(), "s1", "stop", before)
	d.drainTimeline()
	found := false
	talkStore(t, d, "s1").View(func(st *sshstore.State) {
		for _, it := range st.Items {
			if it.System != nil && it.System.Kind == protocol.SSHEventCheckpoint {
				found = true
				if !it.At.Equal(before) {
					t.Errorf("receipt at %v, want %v", it.At, before)
				}
			}
		}
	})
	if !found {
		t.Fatal("no receipt")
	}
}

func TestDeliverySendHistoryHidesWhatItCannotCover(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentIdle)
	store, err := d.sshStore("s1", true)
	if err != nil || store == nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-48 * time.Hour)
	_ = store.Update(func(st *sshstore.State) error {
		for i := range sshstore.MaxSent {
			st.NoteSent(sshstore.SentBatch{Hash: agentfeed.Hash("sent " + string(rune('a'+i%26))), At: base.Add(time.Duration(i) * time.Hour)})
		}
		return nil
	})
	sends, since := d.feedSends("s1")
	if len(sends) != sshstore.MaxSent || !since.Equal(base) {
		t.Fatalf("%d sends, covering from %v", len(sends), since)
	}
	items := []protocol.AgentFeedItem{
		{ID: "old", Kind: protocol.FeedItemUser, At: base.Add(-time.Hour), Text: "a CI reaction from long ago"},
		{ID: "new", Kind: protocol.FeedItemUser, At: base.Add(30 * time.Minute), Text: "typed by hand"},
	}
	e := &feedEntry{id: "s1", feed: agentfeed.NewFeed(func() agentfeed.Source { return agentfeed.NewClaude(t.TempDir(), "slug", nil) })}
	e.markHistory(d, items)
	if !items[0].Echo || items[0].Origin != "" {
		t.Errorf("no history covers this record, so it must not claim the terminal: %+v", items[0])
	}
	if items[1].Echo || items[1].Origin != "terminal" {
		t.Errorf("covered and unmatched is a person at the terminal: %+v", items[1])
	}
}

// A permission park has to be able to end by itself: nothing else gets it out.
func TestAPermissionParkIsReleasedByTheCursorProof(t *testing.T) {
	ctx := context.Background()
	park := func(t *testing.T) (*Daemon, *talkPaneFake) {
		d, f := talkFakeDaemon(t)
		d.readPrompt = attention.ReadPrompt
		s := talkSession(d, "s1", state.AgentIdle)
		d.sessions.Update(s.ID, func(cur *session.Session) bool {
			cur.SetAgentState(state.AgentWaitingInput, "", time.Now())
			cur.InputReason = state.InputPermission
			cur.AtPrompt = false
			return true
		})
		return d, f
	}
	axis := func(d *Daemon) state.AgentState { s, _ := d.sessions.Get("s1"); return s.AgentState }

	t.Run("denied while no view was polling", func(t *testing.T) {
		d, f := park(t)
		f.show(talkResting)
		s, _ := d.sessions.Get("s1")
		if word, _ := d.talkPaneState(ctx, s, false); word != protocol.ComposerResting {
			t.Errorf("composer = %s", word)
		}
		if axis(d) != state.AgentIdle {
			t.Errorf("axis = %s, still parked on a question that is gone", axis(d))
		}
	})
	t.Run("a draft right after denying, then cleared", func(t *testing.T) {
		d, f := park(t)
		wire := protocol.AgentPrompt{Kind: "permission", Hash: "h"}
		d.setTalkPrompt("s1", &wire)
		f.show(talkDraft)
		s, _ := d.sessions.Get("s1")
		d.talkPaneState(ctx, s, false)
		if axis(d) != state.AgentWaitingInput {
			t.Fatalf("a draft proves nothing: axis = %s", axis(d))
		}
		if d.talkPromptOf("s1") == nil {
			t.Fatal("a read that proves nothing cleared the memory of the question")
		}
		f.show(talkResting)
		s, _ = d.sessions.Get("s1")
		d.talkPaneState(ctx, s, false)
		if axis(d) != state.AgentIdle {
			t.Errorf("axis = %s", axis(d))
		}
		if d.talkPromptOf("s1") != nil {
			t.Error("the memory outlived the proof")
		}
	})
	t.Run("a late notification hook", func(t *testing.T) {
		d, f := talkFakeDaemon(t)
		d.readPrompt = attention.ReadPrompt
		talkSession(d, "s1", state.AgentIdle)
		f.show(talkResting)
		d.handleHookEvent(protocol.Request{Cmd: "hookEvent", Session: "s1", Event: "notification", Hook: &protocol.HookPayload{Message: "Claude needs your permission to use Bash"}})
		deadline := time.Now().Add(2 * time.Second)
		for axis(d) != state.AgentIdle && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if axis(d) != state.AgentIdle {
			t.Errorf("axis = %s after a hook that landed behind its answer", axis(d))
		}
	})
	t.Run("a say gets out by itself", func(t *testing.T) {
		d, f := park(t)
		f.show(talkResting)
		if out := say(t, d, "s1", "carry on"); !out.Delivered {
			t.Errorf("say: %+v", out)
		}
	})
	t.Run("a dialog on screen keeps it", func(t *testing.T) {
		d, f := park(t)
		f.show(strings.Repeat("─", 60) + "\n Do you want to proceed?\n ❯ 1. Yes\n   2. No\n\n Esc to cancel · Tab to amend\n")
		f.hideCursor()
		s, _ := d.sessions.Get("s1")
		if word, p := d.talkPaneState(ctx, s, false); word != protocol.ComposerPrompt || p == nil {
			t.Errorf("composer = %s, prompt = %+v", word, p)
		}
		if axis(d) != state.AgentWaitingInput {
			t.Errorf("axis = %s", axis(d))
		}
	})
	t.Run("the other parks stay", func(t *testing.T) {
		for _, reason := range []state.InputReason{state.InputDialog, state.InputQuotaLimited} {
			d, f := park(t)
			d.sessions.Update("s1", func(cur *session.Session) bool { cur.InputReason = reason; return true })
			f.show(talkResting)
			s, _ := d.sessions.Get("s1")
			d.talkPaneState(ctx, s, false)
			if axis(d) != state.AgentWaitingInput {
				t.Errorf("%s released by a bare resting pane", reason)
			}
		}
	})
}

// A press that drained the queue leaves nothing to be deferred.
func TestADeliveryClearsTheDeferredReason(t *testing.T) {
	d, f := talkFakeDaemon(t)
	s := talkSession(d, "s1", state.AgentIdle)
	f.mu.Lock()
	f.attached = true
	f.mu.Unlock()
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.AutomationDeferredReason = "Close or detach this session's live terminals to let lola send queued messages."
		return true
	})
	out := say(t, d, "s1", "a press goes, attached or not")
	if !out.Delivered {
		t.Fatalf("say: %+v", out)
	}
	if cur, _ := d.sessions.Get(s.ID); cur.AutomationDeferredReason != "" {
		t.Errorf("deferred = %q after a delivery", cur.AutomationDeferredReason)
	}
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.AutomationDeferredReason = "stale"
		return true
	})
	d.sshSyncPending(s.ID, talkStore(t, d, s.ID))
	if cur, _ := d.sessions.Get(s.ID); cur.AutomationDeferredReason != "" {
		t.Errorf("deferred = %q with an empty queue", cur.AutomationDeferredReason)
	}
}

func TestResumeLeavesAQuietStoreUntouched(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentIdle)
	store, err := d.sshStore("s1", true)
	if err != nil || store == nil {
		t.Fatal(err)
	}
	if err := store.Update(func(st *sshstore.State) error { st.Session = "s1"; return nil }); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sshstore.Root(d.home), "s1", "state.json")
	old := time.Now().Add(-13 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	d.sshResumeStore(store)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(old) {
		t.Errorf("a restart reset the retention clock to %v", fi.ModTime())
	}
}

func TestAnEndedStoreIsAgedByItsEnd(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	s := talkSession(d, "old-one", state.AgentIdle)
	young := talkSession(d, "young-one", state.AgentIdle)
	if _, err := d.sshStore(s.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.sshStore(young.ID, true); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	d.ssh.now = func() time.Time { return now.Add(-(sshstore.RetainAfterGone + time.Hour)) }
	d.recordEnded(s)
	d.ssh.now = func() time.Time { return now }
	d.recordEnded(young)
	d.sessions.Delete(s.ID)
	d.sessions.Delete(young.ID)
	// The file is fresh, as it is after any write. Its end is what counts.
	d.sshPruneStores(now)
	if _, err := os.Stat(filepath.Join(sshstore.Root(d.home), s.ID)); !os.IsNotExist(err) {
		t.Errorf("a session ended past the retention is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sshstore.Root(d.home), young.ID)); err != nil {
		t.Errorf("a recent end was pruned: %v", err)
	}
}

func TestStartupDoesNotLoadEndedStores(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	s := talkSession(d, "gone-one", state.AgentIdle)
	if _, err := d.sshStore(s.ID, true); err != nil {
		t.Fatal(err)
	}
	d.recordEnded(s)
	d.sessions.Delete(s.ID)
	d.ssh.mu.Lock()
	delete(d.ssh.stores, s.ID)
	d.ssh.mu.Unlock()
	d.sshResume()
	d.ssh.mu.Lock()
	_, loaded := d.ssh.stores[s.ID]
	d.ssh.mu.Unlock()
	if loaded {
		t.Error("an ended store came back into memory at startup")
	}
}

// A crash mid-send leaves an entry claimed. It must come back to the queue, or it reads as
// Queued forever, cannot be recalled and never goes.
func TestResumeUnclaimsWhatACrashLeftClaimed(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentWorking)
	store, err := d.sshStore("s1", true)
	if err != nil || store == nil {
		t.Fatal(err)
	}
	var item string
	_ = store.Update(func(st *sshstore.State) error {
		st.Session = "s1"
		it := st.AddHuman(protocol.SSHHumanMessage{Origin: protocol.SSHHumanSay, Text: "stuck"}, time.Now())
		st.EnqueueEntry(sshstore.QueueEntry{Item: it.ID, Text: "stuck", Human: true, Claimed: true})
		item = it.ID
		return nil
	})
	d.sshResumeStore(store)
	store.View(func(st *sshstore.State) {
		if len(st.Queue) != 1 || st.Queue[0].Claimed {
			t.Errorf("queue = %+v", st.Queue)
		}
	})
	if cur, _ := d.sessions.Get("s1"); cur.PendingSSH != "stuck" {
		t.Errorf("PendingSSH = %q", cur.PendingSSH)
	}
	if out, err := d.handleSSHRecall(protocol.SSHRecallArgs{Session: "s1", Item: item}); err != nil || !out.Recalled {
		t.Errorf("recall: %+v %v", out, err)
	}
}

func TestABestOfLoserLeavesAnEndedRecord(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	s := talkSession(d, "lola-p1-7-b2", state.AgentIdle)
	if _, err := d.discardAttempt(context.Background(), &fakeNative{}, s); err != nil {
		t.Fatal(err)
	}
	info := sshstore.ReadEnded(d.home, s.ID)
	if info == nil {
		t.Fatal("a loser has no ended record, so its story cannot be opened")
	}
	if info.Reason != "Not picked" {
		t.Errorf("reason = %q", info.Reason)
	}
}

func TestCIRowsFollowTheChecksNotTheStates(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	s := talkSession(d, "s1", state.AgentIdle)
	if _, err := d.sshStore(s.ID, true); err != nil {
		t.Fatal(err)
	}
	cycle := func(prev, next state.DeliveryState, checks string) {
		t.Helper()
		s.PR = &scm.PR{Number: 412, ChecksState: checks}
		s.Delivery = next
		if prev != next {
			d.noteDelivery(s, prev)
		}
		d.noteCI(s)
	}
	texts := func() []string {
		var out []string
		for _, ev := range talkEvents(t, d, s.ID, protocol.SSHEventCI) {
			out = append(out, ev.Text)
		}
		return out
	}
	cycle(state.DeliveryReviewPending, state.DeliveryCIFailed, "fail")
	// The push that follows has not registered its checks yet.
	cycle(state.DeliveryCIFailed, state.DeliveryReviewPending, "none")
	if got := texts(); len(got) != 1 || got[0] != "CI failed on PR #412" {
		t.Fatalf("rows = %q: nothing has passed", got)
	}
	cycle(state.DeliveryReviewPending, state.DeliveryCIPending, "pending")
	cycle(state.DeliveryCIPending, state.DeliveryReviewPending, "pass")
	if got := texts(); len(got) != 2 || got[1] != "CI passed on PR #412" {
		t.Fatalf("rows = %q: the recovery came through ci_pending", got)
	}
	cycle(state.DeliveryReviewPending, state.DeliveryReviewPending, "pass")
	if got := texts(); len(got) != 2 {
		t.Fatalf("rows = %q: one recovery is one row", got)
	}
	// A green PR that was never red says nothing.
	s2 := talkSession(d, "s2", state.AgentIdle)
	if _, err := d.sshStore(s2.ID, true); err != nil {
		t.Fatal(err)
	}
	s2.PR = &scm.PR{Number: 9, ChecksState: "pass"}
	s2.Delivery = state.DeliveryReviewPending
	d.noteCI(s2)
	if got := talkEvents(t, d, s2.ID, protocol.SSHEventCI); len(got) != 0 {
		t.Fatalf("rows = %+v", got)
	}
}

// Only the kind whose paste was measured gets the paste path.
func TestPasteOnlyGoesToAMeasuredAgent(t *testing.T) {
	d, f := talkFakeDaemon(t)
	var typed []string
	d.sendKeysTyped = func(_ context.Context, _, text string) error {
		typed = append(typed, text)
		return nil
	}
	claude := talkSession(d, "c1", state.AgentIdle)
	codex := talkSession(d, "x1", state.AgentIdle)
	codex.Agent = "codex"
	d.sessions.Upsert(codex)
	if err := d.typeToAgent(context.Background(), noteFor(claude, ""), claude.ID, claude.ID, "to claude", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := d.typeToAgent(context.Background(), noteFor(codex, ""), codex.ID, codex.ID, "to codex", time.Second); err != nil {
		t.Fatal(err)
	}
	if got := f.sent(); len(got) != 1 || got[0] != "to claude" {
		t.Errorf("paste capable path got %q", got)
	}
	if len(typed) != 1 || typed[0] != "to codex" {
		t.Errorf("typed path got %q", typed)
	}
}

func TestSayToAnAgentWithNoPasteKeepsTheTypedLimit(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentIdle)
	d.sessions.Update("s1", func(s *session.Session) bool { s.Agent = "codex"; return true })
	if _, err := d.handleAgentSay(context.Background(), protocol.AgentSayArgs{Session: "s1", Text: strings.Repeat("x", typedSayBytes+1)}); err == nil {
		t.Fatal("a send too large to type was taken")
	}
}
