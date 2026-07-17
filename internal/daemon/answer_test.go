package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
)

// A canned needs_input pane: a claude-code numbered select rendered in a box,
// exactly what capture-pane -e returns. Used to assert handlePane's parse.
const cannedMenuPane = "\x1b[2m⏺ Ready for your call.\x1b[0m\n" +
	"╭────────────────────────────────────────────────────────╮\n" +
	"│ \x1b[1mDo you want to proceed?\x1b[0m                                 │\n" +
	"│ \x1b[36m❯ 1. Yes\x1b[0m                                                │\n" +
	"│   2. No, and tell Claude what to do differently (esc)    │\n" +
	"╰────────────────────────────────────────────────────────╯\n"

// handleAnswer must refuse any session that is not provably parked at its input
// prompt (status != needs_input) and MUST NOT send-keys — typing into a
// mid-turn agent corrupts it. Even an idle session at the prompt (AtPrompt true,
// safe for the reaction engine) is refused: only an explicit needs_input
// authorizes a human answer.
func TestHandleAnswerRefusesUnlessNeedsInput(t *testing.T) {
	for _, status := range []string{"working", "idle", "ci_failed", "session_ended"} {
		t.Run(status, func(t *testing.T) {
			d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
			s := nativeSess("FE-1", status)
			s.AtPrompt = status == "idle" // idle is "at prompt" yet still not answerable
			d.sessions.Upsert(s)

			sends := 0
			d.sendKeys = func(context.Context, string, string) error { sends++; return nil }

			err := d.handleAnswer(context.Background(), s.ID, "2")
			if err == nil || !strings.Contains(err.Error(), "not waiting for input") {
				t.Fatalf("handleAnswer(status %q) = %v, want a 'not waiting for input' refusal", status, err)
			}
			if sends != 0 {
				t.Errorf("refused answer must not send-keys, got %d send(s)", sends)
			}
			if got, _ := d.sessions.Get(s.ID); got.Status != status {
				t.Errorf("refused answer must not change status, got %q want %q", got.Status, status)
			}
		})
	}
}

// A needs_input session accepts the answer: it is send-keyed to the pane, and
// the session flips AtPrompt=false / status "working" so the reaction engine
// won't also type into it and the TUI shows the agent resuming.
func TestHandleAnswerSendsWhenNeedsInputAndFlipsToWorking(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "needs_input")
	s.AtPrompt = false
	d.sessions.Upsert(s)

	var got sendKeysCall
	sends := 0
	d.sendKeys = func(_ context.Context, name, text string) error {
		got = sendKeysCall{name, text}
		sends++
		return nil
	}

	if err := d.handleAnswer(context.Background(), s.ID, "2"); err != nil {
		t.Fatalf("handleAnswer: %v", err)
	}
	if sends != 1 || got != (sendKeysCall{s.TmuxName, "2"}) {
		t.Errorf("send-keys = %+v (n=%d), want one {%q, 2}", got, sends, s.TmuxName)
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
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "needs_input")
	s.AtPrompt = false
	d.sessions.Upsert(s)

	var got string
	d.sendKeys = func(_ context.Context, _, text string) error { got = text; return nil }

	// CR (the submit vector), a bell (C0), and an ANSI SGR sequence, around
	// preserved LF/TAB content.
	raw := "do X\r\nthen\tY\x07\x1b[31mred\x1b[0m"
	if err := d.handleAnswer(context.Background(), s.ID, raw); err != nil {
		t.Fatalf("handleAnswer: %v", err)
	}
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
