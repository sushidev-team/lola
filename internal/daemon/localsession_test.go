package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
	"github.com/sushidev-team/lola/internal/tmux"
)

func TestCloseExitedTemporary(t *testing.T) {
	for _, tc := range []struct {
		name, live string
		listErr    bool
		removed    bool
	}{
		{name: "all gone", removed: true},
		{name: "primary alive", live: "lola-local-test"},
		{name: "another shell alive", live: "lola-local-test-shell-1"},
		{name: "different session", live: "lola-local-test2", removed: true},
		{name: "unknown liveness", listErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nat := &fakeNative{}
			d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
			s := session.Session{ID: "lola-local-test", Kind: session.KindLocal, Location: "temporary", Agentless: true, Worktree: t.TempDir()}
			d.sessions.Upsert(s)
			artifacts := filepath.Join(d.home, "local-sessions", s.ID)
			if err := os.MkdirAll(artifacts, 0700); err != nil {
				t.Fatal(err)
			}
			d.listTmuxSessions = func(context.Context) ([]tmux.Session, error) {
				if tc.listErr {
					return nil, errors.New("tmux unavailable")
				}
				if tc.live != "" {
					return []tmux.Session{{Name: tc.live}}, nil
				}
				return nil, nil
			}
			out, err := d.handleCloseExitedTemporary(context.Background(), s.ID)
			if (err != nil) != tc.listErr || out.Removed != tc.removed {
				t.Fatalf("result %+v, %v", out, err)
			}
			if _, exists := d.sessions.Get(s.ID); exists == tc.removed {
				t.Fatalf("record exists = %v", exists)
			}
			if _, err := os.Stat(s.Worktree); err != nil {
				t.Fatalf("borrowed directory changed: %v", err)
			}
			if _, err := os.Stat(artifacts); os.IsNotExist(err) != tc.removed {
				t.Fatalf("artifacts: %v", err)
			}
			if len(nat.killCalls()) != 0 {
				t.Fatal("exit notification killed a process")
			}
		})
	}
}

func TestCloseExitedTemporaryRejectsOtherSessions(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	for _, s := range []session.Session{
		{ID: "lola-local-main", Kind: session.KindLocal, Location: "main", Agentless: true},
		{ID: "lola-local-agent", Kind: session.KindLocal, Location: "temporary", Agentless: false},
		{ID: "worktree", Kind: session.KindManual, Agentless: true},
	} {
		d.sessions.Upsert(s)
		if _, err := d.handleCloseExitedTemporary(context.Background(), s.ID); err == nil {
			t.Fatalf("accepted %+v", s)
		}
	}
}

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
