package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	stdlog "log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// talkCommands is written out here rather than read from the protocol, so that taking a
// command off the human list fails this test instead of quietly shrinking it.
var talkCommands = []string{
	protocol.CmdAgentFeed, protocol.CmdAgentFeedOutput, protocol.CmdTalkState, protocol.CmdAgentSay,
	protocol.CmdAgentStop, protocol.CmdAgentChoose, protocol.CmdAgentQueueRelease, protocol.CmdEndedSessions,
}

// The cursor proof's fallback for tests is "a nil seam means EmptyComposer". That makes
// a production daemon with a nil seam silently keep the old proof, and nothing else
// would notice. So this drives each seam of a real newDaemon into a stand-in tmux and
// reads what reached it.
func TestDaemonWiresPaneSeams(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args.log")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n" +
		"case \"$3\" in display-message) echo '4 9 0';; capture-pane) echo 'on screen';; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	d := newDaemon(&config.Config{}, &linear.Fake{}, stdlog.New(io.Discard, "", 0), t.TempDir())
	ctx := context.Background()

	c, err := d.paneCursor(ctx, "s1")
	if err != nil || c.X != 4 || c.Y != 9 || c.Visible {
		t.Errorf("paneCursor = %+v, %v; want the pane's 4,9, hidden", c, err)
	}
	if got, err := d.paneVisible(ctx, "s1"); err != nil || !strings.Contains(got, "on screen") {
		t.Errorf("paneVisible = %q, %v", got, err)
	}
	if err := d.sendKey(ctx, "s1", "1"); err != nil {
		t.Errorf("sendKey: %v", err)
	}
	if err := d.sendBackground(ctx, "s1"); err != nil {
		t.Errorf("sendBackground: %v", err)
	}
	raw, _ := os.ReadFile(log)
	calls := string(raw)
	for _, want := range []string{
		"display-message -p -t =s1: #{cursor_x} #{cursor_y} #{cursor_flag}",
		"capture-pane -p -e -t =s1:\n", // no -S: the visible screen and nothing above it
		"send-keys -t =s1: -l 1\n",
		"send-keys -t =s1: C-b\n",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("tmux never saw %q in:\n%s", want, calls)
		}
	}
	if strings.Contains(calls, "Enter") {
		t.Errorf("a seam pressed Enter:\n%s", calls)
	}

	if d.humanLock == nil || d.readPrompt == nil || d.feedAdvance == nil {
		t.Error("humanLock, readPrompt and feedAdvance must default to the real thing")
	}
	if d.hookCounts == nil || d.timeline == nil {
		t.Error("hookCounts and the timeline channel must exist on a new daemon")
	}
}

// Every one of these reads a pane, types into one or reads a transcript, so each is a
// human's. An agent reaching one through the bridge could answer its own permission
// prompt, and a phone reaching one would read a private conversation.
func TestTalkCommandsAreHumanOnly(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	token := r.d.hookToken("s1")
	if token == "" {
		t.Fatal("no token")
	}
	for _, cmd := range talkCommands {
		if !slices.Contains(protocol.SSHHumanCommands, cmd) {
			t.Errorf("%s is not on the human list", cmd)
		}
		if slices.Contains(protocol.SSHAgentCommands, cmd) {
			t.Errorf("%s is on the agent list", cmd)
		}
		if hookBridgeCmds[cmd] {
			t.Errorf("%s is on the bridge allowlist", cmd)
		}
		if err := r.d.bridgeAdmit(protocol.Request{Cmd: cmd, Session: "s1", HookToken: token}); err == nil {
			t.Errorf("%s admitted on the bridge with a valid token", cmd)
		}
		if !remote.CommandDenied(cmd) {
			t.Errorf("%s reachable from a phone", cmd)
		}
		resp := r.d.handle(context.Background(), protocol.Request{Cmd: cmd, Session: "s1", Args: json.RawMessage(`{}`)})
		if strings.HasPrefix(resp.Error, "unknown") {
			t.Errorf("%s not routed: %s", cmd, resp.Error)
		}
	}
}

// Old records must load and new zero values must add nothing, in both directions: a
// snapshot on disk predates these fields, and a client reading a new daemon may too.
func TestTalkTypesAreAdditive(t *testing.T) {
	t.Run("an old session snapshot loads", func(t *testing.T) {
		dir := t.TempDir()
		old := `[{"id":"s1","source":"native","tmux_name":"s1","transcript_path":"/x/1.jsonl","agent_state":"working"}]`
		if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(old), 0o600); err != nil {
			t.Fatal(err)
		}
		got, ok := session.NewStore(dir).Get("s1")
		if !ok {
			t.Fatal("the old snapshot did not load")
		}
		if got.TranscriptPath != "/x/1.jsonl" {
			t.Errorf("TranscriptPath = %q", got.TranscriptPath)
		}
		if len(got.TranscriptSegments) != 0 || !got.StopHoldUntil.IsZero() || got.QueueHeld {
			t.Errorf("new fields should read zero: %+v %v %v", got.TranscriptSegments, got.StopHoldUntil, got.QueueHeld)
		}
	})
	t.Run("zero values write nothing", func(t *testing.T) {
		for name, v := range map[string]any{
			"session": session.Session{ID: "s1"},
			"event":   protocol.SSHSystemEvent{Kind: "go", Text: "x"},
			"human":   protocol.SSHHumanMessage{Text: "x"},
		} {
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{
				"transcript_segments", "stop_hold_until", "queue_held",
				`"seq"`, `"added"`, `"removed"`, `"files"`, "durationMs", "returned",
			} {
				if strings.Contains(string(raw), key) {
					t.Errorf("%s: a zero value wrote %s: %s", name, key, raw)
				}
			}
		}
	})
	t.Run("a new field round-trips", func(t *testing.T) {
		in := session.Session{
			ID: "s1", TranscriptSegments: []string{"a.jsonl"}, QueueHeld: true,
			StopHoldUntil: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		}
		raw, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		var out session.Session
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		if !out.QueueHeld || !out.StopHoldUntil.Equal(in.StopHoldUntil) || !slices.Equal(out.TranscriptSegments, in.TranscriptSegments) {
			t.Errorf("round trip lost a field: %s", raw)
		}
	})
}

// A restart must not release a hold or forget which transcripts a session wrote. These
// three live in the adoption merge because Adopt rebuilds a record from tmux alone.
func TestAdoptKeepsTalkFields(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nil)

	hold := time.Now().Add(20 * time.Second).Round(0)
	prior := nativeSess("FE-3", "working")
	prior.TranscriptSegments = []string{"11111111-1111-1111-1111-111111111111.jsonl"}
	prior.StopHoldUntil = hold
	prior.QueueHeld = true
	d.sessions.Upsert(prior)

	adopted := nativeSess("FE-3", "working")
	d.native = &fakeNative{adopted: []session.Session{adopted}}
	d.adoptNativeSessions(context.Background())

	got := getSess(t, d, prior.ID)
	if !slices.Equal(got.TranscriptSegments, prior.TranscriptSegments) || !got.StopHoldUntil.Equal(hold) || !got.QueueHeld {
		t.Errorf("adoption lost a talk field: %+v %v %v", got.TranscriptSegments, got.StopHoldUntil, got.QueueHeld)
	}
}

// typeAtRestingPrompt serves a human's click and the merge queue, which must not share a
// gate, so the caller's lock has to be the one that decides.
func TestTypeAtRestingPromptUsesTheCallersLock(t *testing.T) {
	r := newSSHRig(t, hostDev)
	r.session("s1", "")
	r.d.sessions.Update("s1", func(cur *session.Session) bool {
		cur.AtPrompt = true
		cur.SetAgentState(state.AgentIdle, "", time.Now())
		return true
	})
	var typed []string
	r.d.sendKeys = func(_ context.Context, _, text string) error {
		typed = append(typed, text)
		return nil
	}
	s, _ := r.d.sessions.Get("s1")

	refuse := func(context.Context, session.Session) (func(), bool) { return nil, false }
	if err := r.d.typeAtRestingPrompt(context.Background(), s, "hi", nil, refuse, noteFor(s, "")); !errors.Is(err, errNotResting) {
		t.Fatalf("a refusing lock: err = %v, want errNotResting", err)
	}
	if len(typed) != 0 {
		t.Fatalf("typed through a refusing lock: %q", typed)
	}

	unlocked := false
	admit := func(context.Context, session.Session) (func(), bool) { return func() { unlocked = true }, true }
	if err := r.d.typeAtRestingPrompt(context.Background(), s, "hi", nil, admit, noteFor(s, "")); err != nil {
		t.Fatalf("an admitting lock: %v", err)
	}
	if !slices.Equal(typed, []string{"hi"}) || !unlocked {
		t.Fatalf("typed %q, unlocked %v", typed, unlocked)
	}
}

func TestTypedNoteLabels(t *testing.T) {
	claude := session.Session{ID: "s1", Agent: "claude"}
	codex := session.Session{ID: "s2", Agent: "codex"}
	for _, c := range []struct{ got, want string }{
		{reactionLabel("ci_failed", claude), "lola told Claude to fix failing CI"},
		{reactionLabel("ci_failed", codex), "lola told Codex to fix failing CI"},
		{reactionLabel("changes_requested", claude), "lola sent the requested changes to Claude"},
		{reactionLabel("merge_conflict", claude), "lola asked Claude to resolve merge conflicts"},
		{reviewHandoffLabel(reviewProvider{Kind: kindCoderabbitCLI}, claude), "lola sent CodeRabbit's findings to Claude"},
		{reviewHandoffLabel(reviewProvider{Kind: kindCoderabbitWatch}, claude), "lola sent CodeRabbit's findings to Claude"},
		{broadcastLabel(claude, "x"), "Your broadcast went to Claude"},
		{resolveConflictLabel(claude), "You asked Claude to resolve conflicts"},
		{mergeQueueSyncLabel(claude), "lola asked Claude to catch up for the merge queue"},
		{answerLabel(claude), "You answered Claude"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	if got := noteFor(session.Session{ID: session.LaneID("s1", "web")}, "x").Session; got != "s1" {
		t.Errorf("a lane's note names %q, want the base session", got)
	}
}

// The count comes from text lola rendered, so a comment whose own body looks like a
// bullet must not inflate it.
func TestFeedbackLabelCountsComments(t *testing.T) {
	render := func(a protocol.FeedbackArgs) string {
		t.Helper()
		out, err := renderFeedback(a)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	s := session.Session{ID: "s1", Agent: "claude"}
	comment := func(body string) protocol.FeedbackComment {
		return protocol.FeedbackComment{Path: "a.go", Line: 3, Body: body, Quote: "- quoted bullet"}
	}
	for _, c := range []struct {
		text, want string
	}{
		{render(protocol.FeedbackArgs{Comments: []protocol.FeedbackComment{comment("- one\n- still one")}}), "Your diff comment went to Claude"},
		{render(protocol.FeedbackArgs{Comments: []protocol.FeedbackComment{comment("a"), comment("b")}, Note: "- also"}), "Your 2 diff comments went to Claude"},
		{render(protocol.FeedbackArgs{Note: "only a note"}), "Your note went to Claude"},
		// two batches queued behind each other are joined by a blank line
		{render(protocol.FeedbackArgs{Comments: []protocol.FeedbackComment{comment("a")}}) + "\n\n" +
			render(protocol.FeedbackArgs{Comments: []protocol.FeedbackComment{comment("b"), comment("c")}}), "Your 3 diff comments went to Claude"},
	} {
		if got := feedbackLabel(s, c.text); got != c.want {
			t.Errorf("feedbackLabel(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestHookTallyCountsOnlyWhatItKnows(t *testing.T) {
	d := &Daemon{}
	for _, e := range []string{"user_prompt", "tool_use", "tool_use", "stop", "notification"} {
		d.bumpHook("s1", e)
	}
	if got := d.hookTallyFor("s1"); got != (hookTally{UserPrompt: 1, ToolUse: 2, Stop: 1}) {
		t.Errorf("tally = %+v", got)
	}
	if got := d.hookTallyFor("nobody"); got != (hookTally{}) {
		t.Errorf("an unknown session has a tally: %+v", got)
	}
}

// emitSystem runs inside store callbacks, so a full channel must never stall it.
func TestEmitSystemNeverBlocks(t *testing.T) {
	d := newDaemon(&config.Config{}, &linear.Fake{}, stdlog.New(io.Discard, "", 0), t.TempDir())
	done := make(chan struct{})
	go func() {
		for range timelineBuffer + 10 {
			d.emitSystem(timelineEvent{Session: "s1", Event: protocol.SSHSystemEvent{Kind: protocol.SSHEventPR}})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("emitSystem blocked on a full channel")
	}
}
