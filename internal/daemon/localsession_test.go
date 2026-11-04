package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

func TestLocalSessionCreateRenameAndAdopt(t *testing.T) {
	nat := &fakeNative{alive: map[string]bool{}}
	cfg := nativeTestConfig(nativePoll("p1"))
	cfg.Projects[0].Path = t.TempDir()
	d := newTestDaemon(t, cfg, &linear.Fake{}, nat)
	a := protocol.OpenManualArgs{Project: "p1", Location: "main", Name: "Investigate checkout", Agent: true}
	one, err := d.handleOpenManual(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	two, err := d.handleOpenManual(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if one.SessionID == two.SessionID {
		t.Fatal("same-directory sessions collided")
	}
	if len(nat.openManualCalls()) != 0 {
		t.Fatal("created a worktree")
	}
	if err := d.handleRenameSession(one.SessionID, "My session"); err != nil {
		t.Fatal(err)
	}
	nat.alive[one.SessionID] = true
	d.adoptNativeSessions(context.Background())
	s, _ := d.sessions.Get(one.SessionID)
	if s.Kind != session.KindLocal || s.Name != "My session" || s.Location != "main" || s.AgentState != state.AgentWorking {
		t.Fatalf("adoption lost local session: %+v", s)
	}
	dead, _ := d.sessions.Get(two.SessionID)
	if dead.AgentState != state.AgentDead {
		t.Fatalf("dead session: %+v", dead)
	}
	if _, err := d.handleKill(context.Background(), one.SessionID, false, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.sessions.Get(one.SessionID); ok {
		t.Fatal("closed session still listed")
	}
}

func TestTemporarySessionDefaultDirectoryAndValidation(t *testing.T) {
	cfg := nativeTestConfig(nativePoll("p1"))
	cfg.Defaults.TemporaryDirectory = t.TempDir()
	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})
	out, err := d.handleOpenManual(context.Background(), protocol.OpenManualArgs{Location: "temporary"})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := d.sessions.Get(out.SessionID)
	if !s.IsAgentless() || s.Project != "" || !strings.HasSuffix(out.Worktree, strings.TrimPrefix(cfg.Defaults.TemporaryDirectory, "/private")) {
		t.Fatalf("temporary = %+v", s)
	}
	for _, a := range []protocol.OpenManualArgs{
		{Location: "temporary", Agent: true}, {Location: "temporary", Project: "p1"}, {Location: "main", Project: "missing"},
		{Location: "temporary", Path: "relative"}, {Location: "main", Project: "p1", Branch: "wrong"}, {Location: "other"},
		{Location: "temporary", Name: "bad\nname"},
	} {
		if _, err := d.handleOpenManual(context.Background(), a); err == nil {
			t.Fatalf("accepted %+v", a)
		}
	}
}
