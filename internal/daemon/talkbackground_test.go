package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

const talkBackground = "⏺ Bash(make test)\n  ⎿  Running… (5s)\n     (ctrl+b to run in background)\n\n" + talkWorking

func TestTalkBackgroundAction(t *testing.T) {
	d, f := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentWorking)
	f.show(talkBackground)
	ctx := context.Background()
	out, err := d.handleTalkState(ctx, protocol.TalkStateArgs{Session: "s1"})
	if err != nil || out.Prompt == nil || out.Prompt.Kind != "background" || out.Composer != protocol.ComposerWorking {
		t.Fatalf("%+v %v", out, err)
	}
	if s, _ := d.sessions.Get("s1"); s.AgentState != state.AgentWorking || s.InputReason != "" {
		t.Fatalf("background action parked the agent: %+v", s)
	}
	presses := 0
	d.sendBackground = func(_ context.Context, target string) error {
		if target != "s1" {
			t.Fatalf("target %q", target)
		}
		presses++
		f.show(talkWorking)
		return nil
	}
	// A ticking elapsed clock does not invalidate the command the human read.
	f.show(strings.ReplaceAll(talkBackground, "(5s)", "(9s)"))
	r, err := d.handleAgentChoose(ctx, protocol.AgentChooseArgs{Session: "s1", Hash: out.Prompt.Hash, Key: "background"})
	if err != nil || !r.Sent || r.Prompt != nil || presses != 1 || len(f.sent()) != 0 || len(f.keys) != 0 {
		t.Fatalf("background %+v, %v, presses %d", r, err, presses)
	}
	// A second click on the old card cannot press a key after the task moved.
	r, err = d.handleAgentChoose(ctx, protocol.AgentChooseArgs{Session: "s1", Hash: out.Prompt.Hash, Key: "background"})
	if err != nil || r.Sent || presses != 1 {
		t.Fatalf("stale: %+v %v", r, err)
	}
}

func TestTalkBackgroundRefusesChangedOrUnsafePane(t *testing.T) {
	for _, mode := range []string{"changed", "resting", "draft", "hidden", "read failure", "send failure", "codex", "gone", "unknown key"} {
		t.Run(mode, func(t *testing.T) {
			d, f := talkFakeDaemon(t)
			talkSession(d, "s1", state.AgentWorking)
			f.show(talkBackground)
			ctx := context.Background()
			out, _ := d.handleTalkState(ctx, protocol.TalkStateArgs{Session: "s1"})
			if out.Prompt == nil {
				t.Fatal("no background card")
			}
			a := protocol.AgentChooseArgs{Session: "s1", Hash: out.Prompt.Hash, Key: "background"}
			presses := 0
			d.sendBackground = func(context.Context, string) error { presses++; return errors.New("unreachable") }
			switch mode {
			case "changed":
				f.show(strings.ReplaceAll(talkBackground, "make test", "make build"))
			case "resting":
				f.show(talkResting)
			case "draft":
				f.show(strings.ReplaceAll(talkBackground, "❯ ", "❯ draft"))
			case "hidden":
				f.hideCursor()
			case "read failure":
				d.paneVisible = func(context.Context, string) (string, error) { return "", errors.New("unreadable") }
			case "codex":
				d.sessions.Update("s1", func(s *session.Session) bool { s.Agent = "codex"; return true })
			case "gone":
				d.sessions.Update("s1", func(s *session.Session) bool { s.SetAgentState(state.AgentExited, "", time.Now()); return true })
			case "unknown key":
				a.Key = "C-b"
			}
			r, err := d.handleAgentChoose(ctx, a)
			if err != nil || r.Sent || r.Why == "" {
				t.Fatalf("%+v %v", r, err)
			}
			want := 0
			if mode == "send failure" {
				want = 1
			}
			if presses != want || len(f.keys) != 0 {
				t.Fatalf("presses %d, keys %v", presses, f.keys)
			}
		})
	}
}
