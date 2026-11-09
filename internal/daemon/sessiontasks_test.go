package daemon

import (
	"context"
	"encoding/json"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
	"github.com/sushidev-team/lola/internal/taskboard"
	"strings"
	"testing"
)

func taskReq(t *testing.T, id string, args ...string) protocol.Request {
	t.Helper()
	r := reportReq(t, id, args...)
	r.Cmd = "sessionTask"
	return r
}
func TestSharedTaskRoundTrip(t *testing.T) {
	d, f := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentIdle)
	req := taskReq(t, "s1", "add", "key-nav", "task", "Support keyboard navigation")
	r := d.handle(context.Background(), req)
	if !r.OK {
		t.Fatal(r.Error)
	}
	if got := f.sent(); len(got) != 1 || (!strings.Contains(got[0], "key-nav") || !strings.Contains(got[0], strings.TrimSpace(taskboard.Briefing("lola")))) {
		t.Fatalf("not sent: %v", got)
	}
	// A repeated request neither creates another card nor sends twice.
	if r := d.handle(context.Background(), req); !r.OK {
		t.Fatal(r.Error)
	}
	if len(f.sent()) != 1 {
		t.Fatal("duplicate delivery")
	}
	for _, args := range [][]string{{"task", "add", "contrast", "requirement", "--task", "key-nav", "Readable contrast"}, {"task", "state", "contrast", "done"}, {"task", "state", "key-nav", "done"}, {"clear"}} {
		if r := d.handle(context.Background(), reportReq(t, "s1", args...)); !r.OK {
			t.Fatal(r.Error)
		}
	}
	s, _ := d.sessions.Get("s1")
	if len(s.Tasks) != 2 || s.Tasks[0].State != "done" || s.Tasks[0].Author != "user" || s.Tasks[1].Author != "agent" {
		t.Fatalf("tasks: %+v", s.Tasks)
	}
	if len(f.sent()) != 1 {
		t.Fatal("agent reporting sent a message")
	}
	r = d.handle(context.Background(), reportReq(t, "s1", "task", "list"))
	var read []protocol.SessionTask
	if err := json.Unmarshal(r.Data, &read); err != nil || len(read) != 2 {
		t.Fatalf("read %s %v", r.Data, err)
	}
	// Survives the same snapshot save/load used by a daemon restart.
	dir := t.TempDir()
	persisted := session.NewStore(dir)
	persisted.Upsert(s)
	if err := persisted.Save(); err != nil {
		t.Fatal(err)
	}
	restored, ok := session.NewStore(dir).Get("s1")
	if !ok || len(restored.Tasks) != 2 || restored.Tasks[0].State != "done" {
		t.Fatalf("snapshot: %+v", restored.Tasks)
	}
	for _, si := range d.sessionsData().Sessions {
		if si.ID == "s1" && len(si.Tasks) != 2 {
			t.Fatal("tasks missing from wire")
		}
	}
}
func TestSharedTaskFailedSendPreservesCard(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	s := talkSession(d, "g1", state.AgentIdle)
	s.GoalID = "goal-1"
	d.sessions.Upsert(s)
	r := d.handle(context.Background(), taskReq(t, "g1", "add", "a", "task", "Check it"))
	if !r.OK {
		t.Fatal(r.Error)
	}
	s, _ = d.sessions.Get("g1")
	if len(s.Tasks) != 1 || s.Tasks[0].Notification != "failed" || s.Tasks[0].SendError == "" {
		t.Fatalf("failed send: %+v", s.Tasks)
	}
	// An agent cannot smuggle a human send through the report bridge.
	if r := d.handle(context.Background(), reportReq(t, "g1", "task", "retry", "a")); r.OK {
		t.Fatal("agent retry accepted")
	}
}

func TestUserTaskCheckIsSilentAndComposerFeedbackRoutes(t *testing.T) {
	d, f := talkFakeDaemon(t)
	s := talkSession(d, "check", state.AgentIdle)
	s.QueueHeld = true
	s.Tasks = []protocol.SessionTask{{ID: "browser", Kind: "task", Text: "Check shortcut", State: "checking", Author: "agent", Notification: "failed", SendError: "old delivery failure"}}
	d.sessions.Upsert(s)
	req := taskReq(t, "check", "state", "browser", "done")
	if r := d.handle(context.Background(), req); !r.OK {
		t.Fatal(r.Error)
	}
	if sent := f.sent(); len(sent) != 0 {
		t.Fatalf("confirmation started a turn: %v", sent)
	}
	s, _ = d.sessions.Get("check")
	if !s.QueueHeld || s.Tasks[0].State != "done" || s.Tasks[0].Notification != "" || s.Tasks[0].SendError != "" {
		t.Fatalf("confirmation changed the queue hold or retained a delivery error: %+v", s.Tasks)
	}
	// The canonical list is current before another conversation turn, and even
	// an old Retry button cannot turn this silent confirmation into a message.
	listed := d.handle(context.Background(), reportReq(t, "check", "task", "list"))
	var items []protocol.SessionTask
	if err := json.Unmarshal(listed.Data, &items); err != nil || len(items) != 1 || items[0].State != "done" {
		t.Fatalf("confirmation missing from list: %s %v", listed.Data, err)
	}
	if r := d.handle(context.Background(), taskReq(t, "check", "retry", "browser")); !r.OK {
		t.Fatal(r.Error)
	}
	if r := d.handle(context.Background(), req); !r.OK {
		t.Fatal(r.Error)
	}
	if len(f.sent()) != 0 {
		t.Fatal("confirmation retry sent a message")
	}
	// Free-form messages retain the user's exact words while the agent receives routing instructions.
	out := say(t, d, "check", "Actually, the shortcut still scrolls")
	if got := talkHuman(t, d, "check", out.Item).Text; got != "Actually, the shortcut still scrolls" {
		t.Fatalf("user text changed: %s", got)
	}
	if sent := f.sent(); len(sent) != 1 || !strings.Contains(sent[0], taskboard.ConversationHint) {
		t.Fatalf("routing missing: %v", sent)
	}
	if r := d.handle(context.Background(), reportReq(t, "check", "task", "feedback", "browser", "Shortcut still scrolls")); !r.OK {
		t.Fatal(r.Error)
	}
	if len(f.sent()) != 1 {
		t.Fatal("feedback report echoed to agent")
	}
	s, _ = d.sessions.Get("check")
	dir := t.TempDir()
	persisted := session.NewStore(dir)
	persisted.Upsert(s)
	if err := persisted.Save(); err != nil {
		t.Fatal(err)
	}
	restored, ok := session.NewStore(dir).Get("check")
	if !ok || restored.Tasks[0].Feedback != "Shortcut still scrolls" || restored.Tasks[0].State != "todo" {
		t.Fatalf("feedback not persisted: %+v", restored.Tasks)
	}
}

func TestUserTaskScopeChangesStillNotify(t *testing.T) {
	d, f := talkFakeDaemon(t)
	s := talkSession(d, "scope", state.AgentIdle)
	s.Tasks = []protocol.SessionTask{{ID: "a", Kind: "task", Text: "Original scope", State: "done", Author: "user"}}
	d.sessions.Upsert(s)
	for _, args := range [][]string{{"state", "a", "todo"}, {"edit", "a", "Updated scope"}} {
		if r := d.handle(context.Background(), taskReq(t, "scope", args...)); !r.OK {
			t.Fatal(r.Error)
		}
	}
	if sent := f.sent(); len(sent) != 2 || !strings.Contains(sent[1], "Updated scope") {
		t.Fatalf("scope changes not delivered: %v", sent)
	}
}
