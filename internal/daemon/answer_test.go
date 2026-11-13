package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// restingPane is a claude-code composer at rest — what handoffPromptProof must
// see before handleAnswer types a byte. It mirrors observer_pane_test.go's
// paneWaiting; every accepting test below installs it as the paneTail seam,
// because the pane proof is now part of the gate rather than an afterthought.
const restingPane = "╭──────────────────────────────────────────────╮\n" +
	"│ >                                              │\n" +
	"╰──────────────────────────────────────────────╯\n" +
	"  ? for shortcuts\n"

// answerDaemon seeds one session and wires the two seams every answer touches:
// a paneTail returning `pane` (empty string ⇒ a capture FAILURE, so the proof
// fails closed) and a sendKeys recorder.
func answerDaemon(t *testing.T, s session.Session, pane string) (*Daemon, *[]sendKeysCall) {
	t.Helper()
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	d.sessions.Upsert(s)
	d.paneTail = func(context.Context, string, int) (string, error) {
		if pane == "" {
			return "", errors.New("capture-pane: no server running")
		}
		return pane, nil
	}
	sends := &[]sendKeysCall{}
	d.sendKeys = func(_ context.Context, name, text string) error {
		*sends = append(*sends, sendKeysCall{name, text})
		return nil
	}
	return d, sends
}

// A canned needs_input pane: a claude-code numbered select rendered in a box,
// exactly what capture-pane -e returns. Used to assert handlePane's parse.
const cannedMenuPane = "\x1b[2m⏺ Ready for your call.\x1b[0m\n" +
	"╭────────────────────────────────────────────────────────╮\n" +
	"│ \x1b[1mDo you want to proceed?\x1b[0m                                 │\n" +
	"│ \x1b[36m❯ 1. Yes\x1b[0m                                                │\n" +
	"│   2. No, and tell Claude what to do differently (esc)    │\n" +
	"╰────────────────────────────────────────────────────────╯\n"

// handleAnswer must refuse any session that is not provably parked at its input
// prompt, and MUST NOT send-keys — typing into a mid-turn agent corrupts it.
// The gate is now the AXES, not the rolled-up status string, so the cases split
// three ways.
//
// A working agent and a dead one are refused outright; a session parked on a
// delivery word (ci_failed) with the send-keys gate closed is refused too — the
// PR axis says nothing about whether the composer is at rest.
func TestHandleAnswerRefusesUnlessParkedAtPrompt(t *testing.T) {
	for _, status := range []string{"working", "ci_failed", "session_ended"} {
		t.Run(status, func(t *testing.T) {
			s := nativeSess("FE-1", status)
			d, sends := answerDaemon(t, s, restingPane) // even a resting pane cannot save it

			err := d.handleAnswer(context.Background(), s.ID, "2")
			if err == nil {
				t.Fatalf("handleAnswer(status %q) = nil, want a refusal", status)
			}
			if len(*sends) != 0 {
				t.Errorf("refused answer must not send-keys, got %d send(s)", len(*sends))
			}
			if got, _ := d.sessions.Get(s.ID); got.Status != status {
				t.Errorf("refused answer must not change status, got %q want %q", got.Status, status)
			}
		})
	}
}

// The two reasons that are REFUSED even though the axis says waiting_input, and
// this is the load-bearing half of the new gate: a modal SWALLOWS typed prose
// and reads the submit Enter as an answer to its own widget, and a
// quota-limited agent cannot take another turn until its quota resets, so the
// reply lands in a pane that will never act on it. Both rules are already
// documented in CLAUDE.md; this is the first path to enforce them for a human's
// answer. The refusal must NAME the condition — the caller is a person waiting
// for their reply to appear.
func TestHandleAnswerRefusesDialogAndQuotaBlocks(t *testing.T) {
	for _, c := range []struct {
		reason  state.InputReason
		wantMsg string
	}{
		{state.InputDialog, "modal dialog"},
		{state.InputQuotaLimited, "usage limit"},
	} {
		t.Run(string(c.reason), func(t *testing.T) {
			s := nativeSess("FE-1", "needs_input")
			s.AgentState = state.AgentWaitingInput
			s.InputReason = c.reason
			d, sends := answerDaemon(t, s, restingPane)

			err := d.handleAnswer(context.Background(), s.ID, "yes please")
			if err == nil || !strings.Contains(err.Error(), c.wantMsg) {
				t.Fatalf("handleAnswer(%s) = %v, want a refusal naming %q", c.reason, err, c.wantMsg)
			}
			if len(*sends) != 0 {
				t.Errorf("a %s block must never be typed into, got %d send(s)", c.reason, len(*sends))
			}
		})
	}
}

// THE FLAP FIX's user-visible half: after the notification split and the pane
// rule, a finished turn is correctly AgentIdle with the gate open — which is
// EXACTLY the session a human most wants to reply to. The old
// `Status != "needs_input"` string gate refused it.
func TestHandleAnswerAcceptsIdleAtPrompt(t *testing.T) {
	s := nativeSess("FE-1", "idle")
	s.AgentState = state.AgentIdle
	s.AtPrompt = true
	s.Nudged = true // the idle nudge parked it here
	d, sends := answerDaemon(t, s, restingPane)

	if err := d.handleAnswer(context.Background(), s.ID, "carry on"); err != nil {
		t.Fatalf("handleAnswer on an idle session at its prompt: %v", err)
	}
	if len(*sends) != 1 || (*sends)[0] != (sendKeysCall{s.TmuxName, "carry on"}) {
		t.Fatalf("send-keys = %+v, want one {%q, \"carry on\"}", *sends, s.TmuxName)
	}
	after := getSess(t, d, s.ID)
	if after.AgentState != state.AgentWorking {
		t.Errorf("AgentState = %q, want working — the agent is resuming", after.AgentState)
	}
	if after.AtPrompt {
		t.Error("answer must consume the gate so the reaction engine cannot also send-keys")
	}
	if after.Nudged {
		t.Error("Nudged must be cleared when the axis leaves idle")
	}
}

// An idle session whose gate is CLOSED (the observer parks one there when a
// stale working axis meets an unreadable pane) is refused: nothing has proved
// the composer is at rest.
func TestHandleAnswerRefusesIdleWithClosedGate(t *testing.T) {
	s := nativeSess("FE-1", "idle")
	s.AgentState = state.AgentIdle
	s.AtPrompt = false
	d, sends := answerDaemon(t, s, restingPane)

	err := d.handleAnswer(context.Background(), s.ID, "hi")
	if err == nil || !strings.Contains(err.Error(), "not parked at its prompt") {
		t.Fatalf("handleAnswer = %v, want a refusal naming the closed gate", err)
	}
	if len(*sends) != 0 {
		t.Errorf("refused answer must not send-keys, got %d", len(*sends))
	}
}

// The PANE PROOF, which the record alone can never substitute for: a session may
// look perfectly answerable and still have a modal over its composer, because
// claude-code ends a turn (Stop hook → AtPrompt) and THEN puts the dialog up.
// handleAnswer reuses handoffPromptProof for exactly this, and — like
// cmd=resolveConflict, and unlike the reaction engine — it REFUSES rather than
// defers: a human is watching for the reply to land.
func TestHandleAnswerRequiresLivePaneProof(t *testing.T) {
	for _, c := range []struct{ name, pane string }{
		{"captureFails", ""},           // answerDaemon turns "" into a capture error
		{"paneIsBusy", paneWorking},    // a live working cue: the turn resumed
		{"paneIsAModal", paneModal},    // ActivityBlocked over a composer that reported AtPrompt
		{"paneIsUnknown", paneUnknown}, // no cue either way: fail closed
	} {
		t.Run(c.name, func(t *testing.T) {
			s := nativeSess("FE-1", "needs_input")
			s.AgentState = state.AgentWaitingInput
			s.InputReason = state.InputQuestion
			s.AtPrompt = true
			s.AtPromptVerified = true // a cached hook verdict must NOT short-circuit the proof
			d, sends := answerDaemon(t, s, c.pane)

			err := d.handleAnswer(context.Background(), s.ID, "1")
			if err == nil || !strings.Contains(err.Error(), "not resting at its prompt") {
				t.Fatalf("handleAnswer = %v, want a refusal on the failed pane proof", err)
			}
			if len(*sends) != 0 {
				t.Errorf("a failed pane proof must never send-keys, got %d", len(*sends))
			}
			if got := getSess(t, d, s.ID); got.AgentState != state.AgentWaitingInput {
				t.Errorf("a refused answer must not move the axis, got %q", got.AgentState)
			}
		})
	}
}

// A needs_input session accepts the answer: it is send-keyed to the pane, and
// the session flips AtPrompt=false / status "working" so the reaction engine
// won't also type into it and the TUI shows the agent resuming.
func TestHandleAnswerSendsWhenNeedsInputAndFlipsToWorking(t *testing.T) {
	s := nativeSess("FE-1", "needs_input")
	s.AgentState = state.AgentWaitingInput
	s.InputReason = state.InputQuestion
	s.AtPrompt = false // a pending question closes the gate; the pane proof is what admits it
	d, sends := answerDaemon(t, s, restingPane)

	if err := d.handleAnswer(context.Background(), s.ID, "2"); err != nil {
		t.Fatalf("handleAnswer: %v", err)
	}
	if len(*sends) != 1 || (*sends)[0] != (sendKeysCall{s.TmuxName, "2"}) {
		t.Errorf("send-keys = %+v, want one {%q, 2}", *sends, s.TmuxName)
	}
	after, ok := d.sessions.Get(s.ID)
	if !ok {
		t.Fatal("session vanished after answer")
	}
	if after.Status != "working" {
		t.Errorf("status after answer = %q, want working", after.Status)
	}
	if after.AtPrompt {
		t.Error("answer must clear AtPrompt so the reaction engine cannot also send-keys")
	}
}

// A human's free-form answer is verbatim operator input, so an embedded CR (a
// bracketed CRLF paste, or `lola answer FE-1 $'do X\rthen Y'`) must never reach
// the send-keys transport: the CR is an INDISTINGUISHABLE submit that would
// submit the first fragment and fire the rest into the now-resumed, mid-turn
// agent. handleAnswer must sanitize (drop CR/other C0/C1/DEL/ANSI, keep LF/TAB)
// exactly as the reaction path does, so only the transport's explicit trailing
// Enter submits.
func TestHandleAnswerSanitizesEmbeddedControlBytes(t *testing.T) {
	s := nativeSess("FE-1", "needs_input")
	s.AgentState = state.AgentWaitingInput
	s.InputReason = state.InputQuestion
	d, sends := answerDaemon(t, s, restingPane)

	// CR (the submit vector), a bell (C0), and an ANSI SGR sequence, around
	// preserved LF/TAB content.
	raw := "do X\r\nthen\tY\x07\x1b[31mred\x1b[0m"
	if err := d.handleAnswer(context.Background(), s.ID, raw); err != nil {
		t.Fatalf("handleAnswer: %v", err)
	}
	got := (*sends)[0].text
	if strings.ContainsRune(got, '\r') {
		t.Errorf("sent payload %q still carries a CR — a second submit can reach a mid-turn agent", got)
	}
	if want := "do X\nthen\tYred"; got != want {
		t.Errorf("sent payload = %q, want sanitized %q (CR/bell/ANSI stripped, LF/TAB kept)", got, want)
	}
}

// handlePane captures the pane (default line bound when unbounded), runs the
// attention parser, and returns the flattened PaneData — verbatim text plus the
// extracted menu question.
func TestHandlePaneReturnsParsedPaneData(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "needs_input")
	d.sessions.Upsert(s)

	gotLines := -1
	gotTarget := ""
	d.paneTail = func(_ context.Context, name string, lines int) (string, error) {
		gotTarget, gotLines = name, lines
		return cannedMenuPane, nil
	}

	pd, err := d.handlePane(context.Background(), s.ID, 0)
	if err != nil {
		t.Fatalf("handlePane: %v", err)
	}
	if gotTarget != s.TmuxName {
		t.Errorf("capture target = %q, want the session's tmux name %q", gotTarget, s.TmuxName)
	}
	if gotLines != defaultPaneLines {
		t.Errorf("capture lines = %d, want the default %d when unbounded", gotLines, defaultPaneLines)
	}
	if pd.Text != cannedMenuPane {
		t.Errorf("PaneData.Text = %q, want the raw capture verbatim", pd.Text)
	}
	if !pd.HasQuestion || pd.Prompt != "Do you want to proceed?" {
		t.Errorf("parsed = {has %v, prompt %q}, want the proceed prompt", pd.HasQuestion, pd.Prompt)
	}
	if pd.FreeForm {
		t.Error("a numbered menu must not be FreeForm")
	}
	want := []protocol.PaneChoice{
		{Key: "1", Label: "Yes"},
		{Key: "2", Label: "No, and tell Claude what to do differently (esc)"},
	}
	if len(pd.Choices) != len(want) || pd.Choices[0] != want[0] || pd.Choices[1] != want[1] {
		t.Errorf("choices = %+v, want %+v", pd.Choices, want)
	}
}

// An explicit line count is threaded through to the capture; a plain pane with
// no discernible question yields PaneData with HasQuestion false but still the
// captured text.
func TestHandlePaneHonorsLineCountAndNoQuestion(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "working")
	d.sessions.Upsert(s)

	gotLines := -1
	d.paneTail = func(_ context.Context, _ string, lines int) (string, error) {
		gotLines = lines
		return "Compiling module foo...\nAll tests passed.\n", nil
	}

	pd, err := d.handlePane(context.Background(), s.ID, 12)
	if err != nil {
		t.Fatalf("handlePane: %v", err)
	}
	if gotLines != 12 {
		t.Errorf("capture lines = %d, want the requested 12", gotLines)
	}
	if pd.HasQuestion || pd.Prompt != "" || pd.Choices != nil {
		t.Errorf("plain output must yield no question, got %+v", pd)
	}
	if !strings.Contains(pd.Text, "All tests passed.") {
		t.Errorf("PaneData.Text = %q, want the captured text", pd.Text)
	}
}

// Both read and write paths error on an unknown session.
func TestPaneAndAnswerUnknownSession(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	if _, err := d.handlePane(context.Background(), "lola-p1-ghost", 0); err == nil || !strings.Contains(err.Error(), "unknown session") {
		t.Errorf("handlePane unknown = %v, want an 'unknown session' error", err)
	}
	if err := d.handleAnswer(context.Background(), "lola-p1-ghost", "hi"); err == nil || !strings.Contains(err.Error(), "unknown session") {
		t.Errorf("handleAnswer unknown = %v, want an 'unknown session' error", err)
	}
}

// The socket handler routes cmd=pane and cmd=answer to the handlers: pane
// replies PaneData, answer replies bare OK and performs the send.
func TestServerRoutesPaneAndAnswer(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "needs_input")
	d.sessions.Upsert(s)
	d.paneTail = func(context.Context, string, int) (string, error) { return cannedMenuPane, nil }
	sent := false
	d.sendKeys = func(context.Context, string, string) error { sent = true; return nil }

	resp := d.handle(context.Background(), protocol.Request{Cmd: "pane", Session: s.ID})
	if !resp.OK {
		t.Fatalf("cmd=pane response = %+v", resp)
	}
	var pd protocol.PaneData
	if err := json.Unmarshal(resp.Data, &pd); err != nil {
		t.Fatalf("decode PaneData: %v", err)
	}
	if !pd.HasQuestion || len(pd.Choices) != 2 {
		t.Errorf("cmd=pane data = %+v, want the parsed menu", pd)
	}

	resp = d.handle(context.Background(), protocol.Request{Cmd: "answer", Session: s.ID, Text: "1"})
	if !resp.OK || resp.Data != nil {
		t.Fatalf("cmd=answer response = %+v, want bare OK", resp)
	}
	if !sent {
		t.Error("cmd=answer must send-keys the reply")
	}
}
