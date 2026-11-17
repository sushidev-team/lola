package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

func TestAddAppsRecordsTheAppAndQueuesANoteForTheAgent(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	s := stackSess("FE-1")
	s.AgentState = state.AgentWorking // busy: the note stays queued
	d.sessions.Upsert(s)

	data, err := d.handleAddApps(context.Background(), protocol.AddAppsArgs{Session: s.ID, Apps: []string{" api ", "api"}})
	if err != nil {
		t.Fatalf("handleAddApps: %v", err)
	}
	if len(nat.addApps) != 1 || strings.Join(nat.addApps[0].names, ",") != "api" {
		t.Fatalf("runtime calls = %+v", nat.addApps)
	}
	if strings.Join(data.Apps, ",") != "api" || data.Relaunched {
		t.Fatalf("data = %+v", data)
	}
	cur, _ := d.sessions.Get(s.ID)
	if len(cur.Apps) != 3 || cur.Apps[2].Name != "api" {
		t.Fatalf("apps = %+v", cur.Apps)
	}
	if !strings.Contains(cur.PendingBroadcast, "`api/`") {
		t.Errorf("the agent was not told: %q", cur.PendingBroadcast)
	}
	if cur.AgentState != state.AgentWorking {
		t.Errorf("a host session's agent axis moved: %s", cur.AgentState)
	}
}

func TestAddAppsRefusals(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	plain := nativeSess("FE-2", "working")
	d.sessions.Upsert(plain)
	boxed := stackSess("FE-3")
	boxed.Sandbox, boxed.AgentState = config.SandboxDocker, state.AgentWorking
	d.sessions.Upsert(boxed)

	for _, tc := range []struct {
		args protocol.AddAppsArgs
		want string
	}{
		{protocol.AddAppsArgs{Session: plain.ID, Apps: []string{"api"}}, "not a stack session"},
		{protocol.AddAppsArgs{Session: boxed.ID, Apps: []string{"api"}}, "sandbox"},
		{protocol.AddAppsArgs{Session: boxed.ID}, "at least one app"},
		{protocol.AddAppsArgs{Session: "nope", Apps: []string{"api"}}, "unknown session"},
	} {
		if _, err := d.handleAddApps(context.Background(), tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v = %v, want %q", tc.args, err, tc.want)
		}
	}
	if len(nat.addApps) != 0 {
		t.Fatalf("a refused add reached the runtime: %+v", nat.addApps)
	}
}

// A sandboxed session's agent is restarted by the runtime; the record says so
// even when the add itself failed after the pane went down.
func TestAddAppsRecordsASandboxRelaunch(t *testing.T) {
	nat := &fakeNative{addRelaunch: true}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	s := stackSess("FE-4")
	s.Sandbox, s.AtPrompt = config.SandboxDocker, true
	d.sessions.Upsert(s)

	data, err := d.handleAddApps(context.Background(), protocol.AddAppsArgs{Session: s.ID, Apps: []string{"api"}})
	if err != nil || !data.Relaunched {
		t.Fatalf("handleAddApps = %+v, %v", data, err)
	}
	cur, _ := d.sessions.Get(s.ID)
	if cur.AgentState != state.AgentStarting || cur.AtPrompt {
		t.Errorf("relaunch not recorded: state %s atPrompt %v", cur.AgentState, cur.AtPrompt)
	}
	if !strings.Contains(cur.PendingBroadcast, "`api/`") {
		t.Errorf("the relaunched agent has no note queued: %q", cur.PendingBroadcast)
	}

	nat.addAppsErr = errors.New("post_create failed")
	d.sessions.Update(s.ID, func(c *session.Session) bool { c.SetAgentState(state.AgentIdle, "", c.FirstSeen); return true })
	if _, err := d.handleAddApps(context.Background(), protocol.AddAppsArgs{Session: s.ID, Apps: []string{"docs"}}); err == nil {
		t.Fatal("a failed add must be reported")
	}
	cur, _ = d.sessions.Get(s.ID)
	if cur.AgentState != state.AgentStarting || len(cur.Apps) != 3 {
		t.Errorf("after a failed add: state %s, %d apps", cur.AgentState, len(cur.Apps))
	}
}

func TestOpenTicketRefusesExtraAppsForARepository(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	_, err := d.handleOpenTicket(context.Background(), protocol.OpenTicketArgs{Project: "p1", Identifier: "FE-9", UUID: "issue-9", Apps: []string{"api"}})
	if err == nil || !strings.Contains(err.Error(), "not a stack") {
		t.Fatalf("want a refusal, got %v", err)
	}
	if len(nat.spawnCalls()) != 0 {
		t.Fatal("a refused open spawned")
	}
}
