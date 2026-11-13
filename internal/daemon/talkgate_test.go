package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/attention"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// These rows are the live shape: rules around the caret, and a footer the old allowlist
// never heard of. The cursor says which row is the composer.
const (
	gateRest   = "✻ Cogitated for 5s\n────────────\n❯\u00a0\n────────────\n  my-status-line\n"
	gateDraft  = "✻ Cogitated for 5s\n────────────\n❯\u00a0half a thought\n────────────\n  my-status-line\n"
	gateWork   = "✻ Harmonizing… (5s · ↓ 10 tokens)\n────────────\n❯\u00a0\n────────────\n  my-status-line\n"
	gateDialog = "──────────────────────────────\n Do you want to proceed?\n ❯ 1. Yes\n   2. No\n\n Esc to cancel · Tab to amend\n"
	gateModal  = "▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔ ◐ medium · /effort ▔\n   Settings  Status   Config\n\n Esc to cancel\n"
	gateScreen = "   Some full-screen view\n   with no rule at all\n"
)

var (
	atComposer = attention.Cursor{X: 2, Y: 2, Visible: true}
	cursorOff  = attention.Cursor{X: 1, Y: 3}
)

// gateSeams points the proof at one pane and cursor, and records every attached check.
type gateSeams struct {
	pane     string
	cursor   attention.Cursor
	paneErr  error
	attached bool
	asked    int
}

func (g *gateSeams) install(d *Daemon) {
	d.paneVisible = func(context.Context, string) (string, error) { return g.pane, g.paneErr }
	d.paneCursor = func(context.Context, string) (attention.Cursor, error) { return g.cursor, nil }
	d.paneTail = func(context.Context, string, int) (string, error) {
		panic("the cursor proof must not fall back while its seams are wired")
	}
	d.promptAttached = func(context.Context, string) (bool, error) {
		g.asked++
		return g.attached, nil
	}
}

func gateDaemon(t *testing.T) (*Daemon, *fakeReactSeams, *gateSeams, session.Session) {
	t.Helper()
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	g := &gateSeams{pane: gateRest, cursor: atComposer}
	g.install(d)
	s := nativeSess("FE-1", "working")
	s.Agent = "claude"
	s.AtPrompt = true
	s.SetAgentState(state.AgentIdle, "", time.Now())
	d.sessions.Upsert(s)
	cur, _ := d.sessions.Get(s.ID)
	return d, seams, g, cur
}

func TestLockHumanPromptIgnoresAttached(t *testing.T) {
	d, _, g, s := gateDaemon(t)
	g.attached = true
	unlock, why := d.lockHumanPrompt(context.Background(), s)
	if unlock == nil {
		t.Fatalf("refused with a terminal attached: %q", why)
	}
	unlock()
	if g.asked != 0 {
		t.Errorf("the human gate asked about attached clients %d times", g.asked)
	}
}

// It holds sendGate through the proof and until the caller lets go, so a send and a
// restore cannot interleave.
func TestLockHumanPromptHoldsTheGate(t *testing.T) {
	d, _, _, s := gateDaemon(t)
	unlock, _ := d.lockHumanPrompt(context.Background(), s)
	if unlock == nil {
		t.Fatal("refused")
	}
	g := d.sendGate(s.ID)
	if g.TryLock() {
		g.Unlock()
		t.Fatal("the gate is free while the send is meant to be running")
	}
	unlock()
	if !g.TryLock() {
		t.Fatal("unlock did not release the gate")
	}
	g.Unlock()
}

func TestLockHumanPromptRefusesDraft(t *testing.T) {
	d, _, g, s := gateDaemon(t)
	g.pane = gateDraft
	unlock, why := d.lockHumanPrompt(context.Background(), s)
	if unlock != nil || why != humanWhyDraft {
		t.Fatalf("got %v, %q; want a refusal for the draft", unlock != nil, why)
	}
	g2 := d.sendGate(s.ID)
	if !g2.TryLock() {
		t.Fatal("a refusal left the gate held")
	}
	g2.Unlock()
}

func TestLockHumanPromptReasons(t *testing.T) {
	for name, tc := range map[string]struct {
		pane   string
		cursor attention.Cursor
		paneE  error
		edit   func(*session.Session)
		want   string
	}{
		"mid-turn pane":     {pane: gateWork, cursor: atComposer, want: humanWhyWorking},
		"permission dialog": {pane: gateDialog, cursor: cursorOff, want: humanWhyPrompt},
		"modal":             {pane: gateModal, cursor: cursorOff, want: humanWhyModal},
		"unreadable screen": {pane: gateScreen, cursor: cursorOff, want: humanWhyModal},
		"unknown, cursor":   {pane: gateScreen, cursor: atComposer, want: humanWhyWorking},
		"pane gone":         {paneE: errors.New("no pane"), want: humanWhyGone},
		"record working": {pane: gateRest, cursor: atComposer, want: humanWhyWorking, edit: func(s *session.Session) {
			s.AtPrompt = false
			s.SetAgentState(state.AgentWorking, "", time.Now())
		}},
		"record permission": {pane: gateRest, cursor: atComposer, want: humanWhyPrompt, edit: func(s *session.Session) {
			s.AtPrompt = false
			s.SetAgentState(state.AgentWaitingInput, "", time.Now())
			s.InputReason = state.InputPermission
		}},
		"record dialog": {pane: gateRest, cursor: atComposer, want: humanWhyModal, edit: func(s *session.Session) {
			s.AtPrompt = false
			s.SetAgentState(state.AgentWaitingInput, "", time.Now())
			s.InputReason = state.InputDialog
		}},
		// SetAgentState does not close AtPrompt when a pane dies, so the record's own gate
		// still says yes. The pane here even looks fine.
		"dead with a stale gate": {pane: gateRest, cursor: atComposer, want: humanWhyGone, edit: func(s *session.Session) {
			s.AtPrompt = true
			s.SetAgentState(state.AgentDead, "", time.Now())
		}},
	} {
		t.Run(name, func(t *testing.T) {
			d, _, g, s := gateDaemon(t)
			g.pane, g.cursor, g.paneErr = tc.pane, tc.cursor, tc.paneE
			if tc.edit != nil {
				d.sessions.Update(s.ID, func(cur *session.Session) bool { tc.edit(cur); return true })
			}
			unlock, why := d.lockHumanPrompt(context.Background(), s)
			if unlock != nil {
				unlock()
			}
			if unlock != nil || why != tc.want {
				t.Errorf("why = %q (locked %v), want %q", why, unlock != nil, tc.want)
			}
		})
	}
	t.Run("unknown session", func(t *testing.T) {
		d, _, _, s := gateDaemon(t)
		s.ID = "nobody"
		if unlock, why := d.lockHumanPrompt(context.Background(), s); unlock != nil || why != humanWhyGone {
			t.Errorf("why = %q", why)
		}
	})
}

// A session that has not reported in may be spoken to once its pane proves it can hear.
func TestLockHumanPromptAdmitsStartingOnProof(t *testing.T) {
	d, _, g, s := gateDaemon(t)
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.AtPrompt = false
		cur.SetAgentState(state.AgentStarting, "", time.Now())
		return true
	})
	unlock, why := d.lockHumanPrompt(context.Background(), s)
	if unlock == nil {
		t.Fatalf("refused: %q", why)
	}
	unlock()
	g.pane = gateWork
	if unlock, why := d.lockHumanPrompt(context.Background(), s); unlock != nil || why != humanWhyWorking {
		t.Errorf("a starting agent was not held to the proof: %q", why)
	}
}

func TestLockAutomationKeepsAttachedCheck(t *testing.T) {
	d, _, g, s := gateDaemon(t)
	g.attached = true
	if unlock, ok := d.lockAutomationPrompt(context.Background(), s); ok {
		unlock()
		t.Fatal("automation typed at an attached terminal")
	}
	if g.asked == 0 {
		t.Error("the automation gate never asked")
	}
	g.attached = false
	unlock, ok := d.lockAutomationPrompt(context.Background(), s)
	if !ok {
		t.Fatal("refused once detached")
	}
	unlock()
	if g.asked < 3 {
		t.Errorf("attached check ran %d times; it must run before and after the capture", g.asked)
	}
}

func TestLockAutomationRespectsStopHold(t *testing.T) {
	d, _, _, s := gateDaemon(t)
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.StopHoldUntil = time.Now().Add(30 * time.Second)
		return true
	})
	if unlock, ok := d.lockAutomationPrompt(context.Background(), s); ok {
		unlock()
		t.Fatal("automation typed through a stop hold")
	}
	// The hold is on automation only: a person pressing Send is speaking again.
	unlock, why := d.lockHumanPrompt(context.Background(), s)
	if unlock == nil {
		t.Fatalf("the human gate read the hold: %q", why)
	}
	unlock()

	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.StopHoldUntil = time.Now().Add(-time.Second)
		return true
	})
	unlock, ok := d.lockAutomationPrompt(context.Background(), s)
	if !ok {
		t.Fatal("an expired hold still blocks")
	}
	unlock()
}

// A status line used to defer every automatic send forever. The cursor proof is the fix,
// and a draft under that same status line must still hold.
func TestAutomationUsesTheCursorProof(t *testing.T) {
	d, _, g, s := gateDaemon(t)
	if !d.automationPromptReady(context.Background(), s) {
		t.Fatal("a status line deferred an empty composer")
	}
	g.pane = gateDraft
	if d.automationPromptReady(context.Background(), s) {
		t.Fatal("a draft passed")
	}
	got, _ := d.sessions.Get(s.ID)
	if !strings.Contains(got.AutomationDeferredReason, "empty composer") {
		t.Errorf("reason = %q", got.AutomationDeferredReason)
	}
}

// Most of the daemon's tests inject only paneTail. With no cursor seams the old, stricter
// proof answers, in both gates.
func TestAutomationFallsBackWithoutCursorSeams(t *testing.T) {
	d, _, _, s := gateDaemon(t)
	d.paneCursor, d.paneVisible = nil, nil
	pane := paneWaiting
	d.paneTail = func(context.Context, string, int) (string, error) { return pane, nil }
	if !d.automationPromptReady(context.Background(), s) {
		t.Fatal("resting pane refused")
	}
	unlock, why := d.lockHumanPrompt(context.Background(), s)
	if unlock == nil {
		t.Fatalf("human gate refused: %q", why)
	}
	unlock()

	pane = draftPane
	if d.automationPromptReady(context.Background(), s) {
		t.Fatal("draft passed the fallback")
	}
	if unlock, why := d.lockHumanPrompt(context.Background(), s); unlock != nil || why != humanWhyDraft {
		t.Fatalf("why = %q", why)
	}

	// The fallback's own limit: EmptyComposer does not know a status line, and it fails
	// closed on one. That is why production must have the seams.
	pane = gateRest
	if d.automationPromptReady(context.Background(), s) {
		t.Fatal("the fallback is meant to be the stricter proof")
	}

	d.paneTail = nil
	if d.automationPromptReady(context.Background(), s) {
		t.Fatal("no seams at all must fail closed")
	}
	if unlock, why := d.lockHumanPrompt(context.Background(), s); unlock != nil || why != humanWhyGone {
		t.Fatalf("why = %q", why)
	}
}

func TestSanitizeNeutralizesBang(t *testing.T) {
	for in, want := range map[string]string{
		"!rm -rf x":              " !rm -rf x",
		" !rm -rf x":             " !rm -rf x",
		"\n\n  !rm":              " !rm",
		"\t!rm":                  " !rm",
		"\u200b!rm":              " !rm",
		"\ufeff !rm":             " !rm",
		"\x1b[31m!rm\x1b[0m":     " !rm",
		"\x07!rm":                " !rm",
		"\r\n!rm":                " !rm",
		"say !loud":              "say !loud",
		"line one\n!not leading": "line one\n!not leading",
		"":                       "",
		"!":                      " !",
	} {
		got := sanitizeAgentText(in)
		if got != want {
			t.Errorf("sanitizeAgentText(%q) = %q, want %q", in, got, want)
		}
		// Several send paths sanitize twice, and a second pass must not add a space.
		if again := sanitizeAgentText(got); again != got {
			t.Errorf("not idempotent: %q then %q", got, again)
		}
	}
}

// A reply to a resting agent has no question to answer, so a draft would just be sent
// along with it. The first capture is what the existing activity proof reads.
func TestAnswerIdleRequiresEmptyComposer(t *testing.T) {
	d, seams, g, s := gateDaemon(t)
	d.paneTail = func(context.Context, string, int) (string, error) { return g.pane, nil }
	g.pane = gateDraft
	err := d.handleAnswer(context.Background(), s.ID, "go on")
	if err == nil || !strings.Contains(err.Error(), "text in its own prompt") {
		t.Fatalf("err = %v", err)
	}
	if len(seams.sendCalls()) != 0 {
		t.Fatalf("typed over a draft: %+v", seams.sendCalls())
	}
	g.pane = gateRest
	if err := d.handleAnswer(context.Background(), s.ID, "go on"); err != nil {
		t.Fatal(err)
	}
	if len(seams.sendCalls()) != 1 {
		t.Fatalf("sends = %+v", seams.sendCalls())
	}
}

// A waiting session keeps today's proof: its dialog has no empty composer, and a human
// typing "1" at it answers the right question.
func TestAnswerWaitingKeepsItsProof(t *testing.T) {
	d, seams, g, s := gateDaemon(t)
	d.paneTail = func(context.Context, string, int) (string, error) { return g.pane, nil }
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.AtPrompt = false
		cur.SetAgentState(state.AgentWaitingInput, "", time.Now())
		cur.InputReason = state.InputPermission
		return true
	})
	g.pane, g.cursor = gateDialog, cursorOff
	if err := d.handleAnswer(context.Background(), s.ID, "1"); err != nil {
		t.Fatal(err)
	}
	if len(seams.sendCalls()) != 1 {
		t.Fatalf("sends = %+v", seams.sendCalls())
	}
}

// An answer that begins with "!" must not run on this machine.
func TestAnswerSendsBangAsText(t *testing.T) {
	d, seams, g, s := gateDaemon(t)
	d.paneTail = func(context.Context, string, int) (string, error) { return g.pane, nil }
	if err := d.handleAnswer(context.Background(), s.ID, "!whoami"); err != nil {
		t.Fatal(err)
	}
	if calls := seams.sendCalls(); len(calls) != 1 || calls[0].text != " !whoami" {
		t.Fatalf("sends = %+v", calls)
	}
}

func TestResolveConflictIgnoresAttached(t *testing.T) {
	d, seams, g, _ := gateDaemon(t)
	id := conflictSess(t, d, "FE-2")
	g.attached = true
	if _, err := d.handleResolveConflict(context.Background(), id); err != nil {
		t.Fatalf("a click refused for an attached terminal: %v", err)
	}
	if g.asked != 0 {
		t.Errorf("attached check ran %d times", g.asked)
	}
	if len(seams.sendCalls()) != 1 {
		t.Fatalf("sends = %+v", seams.sendCalls())
	}
	got, _ := d.sessions.Get(id)
	if got.LastReactedStatus != "merge_conflict" {
		t.Errorf("guard not stamped: %q", got.LastReactedStatus)
	}
}

func TestResolveConflictSaysWhy(t *testing.T) {
	for name, tc := range map[string]struct {
		pane   string
		cursor attention.Cursor
		want   string
	}{
		"draft": {gateDraft, atComposer, "text in its own prompt"},
		"modal": {gateModal, cursorOff, "screen open"},
		"work":  {gateWork, atComposer, "needs an idle agent"},
	} {
		t.Run(name, func(t *testing.T) {
			d, seams, g, _ := gateDaemon(t)
			id := conflictSess(t, d, "FE-2")
			g.pane, g.cursor = tc.pane, tc.cursor
			_, err := d.handleResolveConflict(context.Background(), id)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "detach") {
				t.Errorf("still asks to detach: %v", err)
			}
			if len(seams.sendCalls()) != 0 {
				t.Errorf("sends = %+v", seams.sendCalls())
			}
		})
	}
}

// The queue is not a person. It shares the helper with the click above, and it must keep
// the attached check and the stop hold.
func TestMergeQueueKeepsAutomationLock(t *testing.T) {
	d, gh, seams := queueDaemon(t)
	id := queueSess(d, gh, "FE-1", greenPR(5, sha(5)))
	gh.behind[sha(5)] = 1
	d.promptAttached = func(context.Context, string) (bool, error) { return true, nil }
	d.runMergeQueue(context.Background())
	if len(seams.sendCalls()) != 0 {
		t.Fatal("the queue typed at an attached terminal")
	}
	d.promptAttached = func(context.Context, string) (bool, error) { return false, nil }
	d.sessions.Update(id, func(cur *session.Session) bool {
		cur.StopHoldUntil = time.Now().Add(30 * time.Second)
		return true
	})
	d.runMergeQueue(context.Background())
	if len(seams.sendCalls()) != 0 {
		t.Fatal("the queue typed through a stop hold")
	}
	s, _ := d.sessions.Get(id)
	if s.MergeQueueGuard != nil || s.MergeQueueStep != mqWaiting {
		t.Fatalf("guard stamped while waiting: %+v, %q", s.MergeQueueGuard, s.MergeQueueStep)
	}
	d.sessions.Update(id, func(cur *session.Session) bool {
		cur.StopHoldUntil = time.Time{}
		return true
	})
	d.runMergeQueue(context.Background())
	if len(seams.sendCalls()) != 1 {
		t.Fatal("the queue did not resume")
	}
}

func TestHumanMessageDuringTurnKeepsComposerSafeguards(t *testing.T) {
	for name, tc := range map[string]struct {
		pane   string
		cursor attention.Cursor
		want   bool
	}{
		"working empty":    {gateWork, atComposer, true},
		"working draft":    {strings.Replace(gateWork, "❯\u00a0", "❯\u00a0my draft", 1), atComposer, false},
		"permission":       {gateDialog, cursorOff, false},
		"screen":           {gateModal, cursorOff, false},
		"hidden cursor":    {gateWork, cursorOff, false},
		"cursor elsewhere": {gateWork, attention.Cursor{X: 2, Y: 0, Visible: true}, false},
	} {
		t.Run(name, func(t *testing.T) {
			d, _, g, s := gateDaemon(t)
			d.sessions.Update(s.ID, func(cur *session.Session) bool {
				cur.AtPrompt = false
				cur.SetAgentState(state.AgentWorking, "", time.Now())
				return true
			})
			g.pane, g.cursor = tc.pane, tc.cursor
			unlock, why := d.lockHumanMessage(context.Background(), s)
			if unlock != nil {
				unlock()
			}
			if (unlock != nil) != tc.want {
				t.Fatalf("locked %v, reason %q", unlock != nil, why)
			}
			if unlock, ok := d.lockAutomationPrompt(context.Background(), s); ok {
				unlock()
				t.Fatal("automation bypassed the idle gate")
			}
		})
	}
}
