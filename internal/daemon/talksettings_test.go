package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

func TestAgentSettingDelivery(t *testing.T) {
	for _, tt := range []struct{ kind, setting, value, command string }{
		{"claude", "model", "opus", "/model opus"},
		{"codex", "effort", "high", "/model --reasoning=high"},
		{"opencode", "model", "picker", "/models"},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			d, f := talkFakeDaemon(t)
			s := talkSession(d, "s1", state.AgentIdle)
			s.Agent = tt.kind
			s.Model = "original"
			d.sessions.Upsert(s)
			if tt.kind != "claude" {
				f.show("╭──────────╮\n│ >        │\n╰──────────╯\n? for shortcuts\n")
			}
			out, err := d.handleAgentSetting(context.Background(), protocol.AgentSettingArgs{Session: "s1", Agent: tt.kind, Setting: tt.setting, Value: tt.value})
			if err != nil || !out.Sent {
				t.Fatalf("%+v, %v", out, err)
			}
			if sent := f.sent(); len(sent) != 1 || sent[0] != tt.command {
				t.Fatalf("sent %q", sent)
			}
			cur, _ := d.sessions.Get("s1")
			if cur.Model != "original" || cur.PendingSSH != "" {
				t.Fatal("setting delivery must not claim a model change or queue text")
			}
		})
	}
}

func TestAgentSettingRefusesUnsafePane(t *testing.T) {
	for _, tt := range []struct {
		name, pane string
		st         state.AgentState
	}{
		{"working record", talkResting, state.AgentWorking},
		{"fresh working pane", talkWorking, state.AgentIdle},
		{"draft", talkDraft, state.AgentIdle},
		{"unknown screen", "Settings\nSelect an option", state.AgentIdle},
		{"gone", talkResting, state.AgentDead},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d, f := talkFakeDaemon(t)
			talkSession(d, "s1", tt.st)
			f.show(tt.pane)
			_, err := d.handleAgentSetting(context.Background(), protocol.AgentSettingArgs{Session: "s1", Agent: "claude", Setting: "model", Value: "opus"})
			if err == nil || len(f.sent()) != 0 {
				t.Fatalf("unsafe send: %v %q", err, f.sent())
			}
		})
	}
}

func TestAgentSettingRejectsChangedAgentAndInvalidValues(t *testing.T) {
	d, f := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentIdle)
	for _, a := range []protocol.AgentSettingArgs{
		{Session: "s1", Agent: "codex", Setting: "model", Value: "opus"},
		{Session: "s1", Agent: "claude", Setting: "model", Value: "opus\n/clear"},
		{Session: "missing", Agent: "claude", Setting: "model", Value: "opus"},
	} {
		if _, err := d.handleAgentSetting(context.Background(), a); err == nil {
			t.Fatalf("accepted %+v", a)
		}
	}
	d.sessions.Update("s1", func(s *session.Session) bool { s.GoalID = "goal"; return true })
	if _, err := d.handleAgentSetting(context.Background(), protocol.AgentSettingArgs{Session: "s1", Agent: "claude", Setting: "model", Value: "opus"}); err == nil {
		t.Fatal("accepted goal")
	}
	if len(f.sent()) != 0 {
		t.Fatalf("sent %q", f.sent())
	}
}

func TestAgentSettingReportsTransportFailure(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentIdle)
	d.sendKeys = func(context.Context, string, string) error { return errors.New("offline") }
	out, err := d.handleAgentSetting(context.Background(), protocol.AgentSettingArgs{Session: "s1", Agent: "claude", Setting: "model", Value: "opus"})
	if err == nil || out.Sent {
		t.Fatalf("%+v, %v", out, err)
	}
}
