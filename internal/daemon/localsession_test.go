package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/projectgit"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
	"github.com/sushidev-team/lola/internal/tmux"
)

type profileNative struct {
	*promptNative
	model, effort string
}

func (n *profileNative) OpenDirectoryProfile(ctx context.Context, p config.Project, id, dir, location, kind string, useAgent bool, prompt, model, effort string) (session.Session, error) {
	n.model, n.effort = model, effort
	return n.OpenDirectoryPrompt(ctx, p, id, dir, location, kind, useAgent, prompt)
}
func TestResolverUsesProjectAIProfile(t *testing.T) {
	nat := &profileNative{promptNative: &promptNative{fakeNative: &fakeNative{}}}
	cfg := nativeTestConfig(nativePoll("p1"))
	cfg.Projects[0].Agent = "codex"
	cfg.Projects[0].Path = t.TempDir()
	p := config.DefaultAIProvider("codex")
	p.Enabled = true
	p.Profiles["thorough"] = config.AIModel{Model: "selected-model", Effort: "high"}
	cfg.AI = map[string]config.AIProvider{"codex": p}
	d := newTestDaemon(t, cfg, &linear.Fake{}, nat)
	d.validateLocalCheckout = func(context.Context, string, string, string) error { return nil }
	_, err := d.handleOpenDirectory(context.Background(), protocol.OpenManualArgs{Project: "p1", Location: "checkout", Path: cfg.Projects[0].Path, Agent: true, AIAction: "resolve", Prompt: "Resolve conflicts"})
	if err != nil {
		t.Fatal(err)
	}
	if nat.model != "selected-model" || nat.effort != "high" || nat.got != "Resolve conflicts" {
		t.Fatal("resolver profile or task was lost")
	}
}

func TestBorrowedCheckoutSession(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		t.Run(fmt.Sprint(refuse), func(t *testing.T) {
			nat := &promptNative{fakeNative: &fakeNative{}}
			cfg := nativeTestConfig(nativePoll("p1"))
			cfg.Projects[0].Path = t.TempDir()
			d := newTestDaemon(t, cfg, &linear.Fake{}, nat)
			dir, _ := filepath.EvalSymlinks(t.TempDir())
			checked := false
			d.validateLocalCheckout = func(_ context.Context, root, path, home string) error {
				checked = true
				if root != cfg.Projects[0].Path || path != dir || home != d.home {
					t.Fatal("wrong checkout validation scope")
				}
				if refuse {
					return errors.New("not registered")
				}
				return nil
			}
			out, err := d.handleOpenManual(context.Background(), protocol.OpenManualArgs{Project: "p1", Location: "checkout", Path: dir, Agent: true, Prompt: "Resolve and stage conflicts"})
			if !checked {
				t.Fatal("checkout was not checked")
			}
			if refuse {
				if err == nil || nat.got != "" {
					t.Fatal("started an unverified checkout")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			s, ok := d.sessions.Get(out.SessionID)
			if !ok || s.Kind != session.KindLocal || s.Worktree != dir || s.OwnsBranch() || nat.got != "Resolve and stage conflicts" {
				t.Fatalf("wrong borrowed session: %+v", s)
			}
			for _, info := range d.sessionsData().Sessions {
				if info.ID == out.SessionID && info.Worktree != dir {
					t.Fatalf("wrong checkout on wire: %s", info.Worktree)
				}
			}
		})
	}
}

func TestRegisteredBorrowedCheckout(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	for _, tc := range []struct {
		name    string
		trees   []projectgit.Worktree
		allowed bool
	}{
		{"registered", []projectgit.Worktree{{Path: dir}}, true},
		{"unregistered", nil, false},
		{"locked", []projectgit.Worktree{{Path: dir, Locked: true}}, false},
		{"prunable", []projectgit.Worktree{{Path: dir, Prunable: true}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if (registeredBorrowedCheckout(dir, tc.trees) == nil) != tc.allowed {
				t.Fatal("wrong checkout admission")
			}
		})
	}
	if err := validateBorrowedCheckout(context.Background(), dir, dir, dir); err == nil {
		t.Fatal("admitted a managed directory")
	}
}

func TestCloseExitedTemporary(t *testing.T) {
	for _, location := range []string{"temporary", "main", "checkout"} {
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
			t.Run(location+"/"+tc.name, func(t *testing.T) {
				nat := &fakeNative{}
				d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
				s := session.Session{ID: "lola-local-test", Kind: session.KindLocal, Location: location, Agentless: true, Worktree: t.TempDir()}
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
}

func TestCloseExitedTemporaryRejectsOtherSessions(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	for _, s := range []session.Session{
		{ID: "lola-local-main-agent", Kind: session.KindLocal, Location: "main", Agentless: false},
		{ID: "lola-local-ssh", Kind: session.KindLocal, Location: "ssh", Agentless: true},
		{ID: "lola-local-agent", Kind: session.KindLocal, Location: "temporary", Agentless: false},
		{ID: "worktree", Kind: session.KindManual, Agentless: true},
	} {
		d.sessions.Upsert(s)
		if _, err := d.handleCloseExitedTemporary(context.Background(), s.ID); err == nil {
			t.Fatalf("accepted %+v", s)
		}
	}
}

func TestObserveLocalShellClosesOnlyAfterLastTerminal(t *testing.T) {
	for _, location := range []string{"temporary", "main", "checkout"} {
		t.Run(location, func(t *testing.T) {
			d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
			s := session.Session{ID: "lola-local-observed", Source: "native", Kind: session.KindLocal, Location: location, Agentless: true, AgentState: state.AgentShell, Worktree: t.TempDir()}
			d.sessions.Upsert(s)
			live := []tmux.Session{{Name: s.ID + "-shell-1"}}
			var listErr error
			d.listTmuxSessions = func(context.Context) ([]tmux.Session, error) { return live, listErr }
			d.observeNative(context.Background())
			if got, ok := d.sessions.Get(s.ID); !ok || got.AgentState != state.AgentShell {
				t.Fatalf("remaining shell was hidden: %+v", got)
			}
			listErr = errors.New("tmux unavailable")
			d.observeNative(context.Background())
			if got, ok := d.sessions.Get(s.ID); !ok || got.AgentState != state.AgentShell {
				t.Fatalf("unknown liveness closed session: %+v", got)
			}
			live, listErr = nil, nil
			d.observeNative(context.Background())
			if _, ok := d.sessions.Get(s.ID); ok {
				t.Fatal("last shell exited but session remains")
			}
			if _, err := os.Stat(s.Worktree); err != nil {
				t.Fatalf("borrowed directory changed: %v", err)
			}
		})
	}
}

func TestAdoptLocalShellKeepsLaunchKind(t *testing.T) {
	for _, alive := range []bool{true, false} {
		t.Run(fmt.Sprint(alive), func(t *testing.T) {
			nat := &fakeNative{alive: map[string]bool{"lola-local-shell": alive}}
			d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
			d.sessions.Upsert(session.Session{ID: "lola-local-shell", Source: "native", Kind: session.KindLocal, Location: "main", Agentless: true, Agent: "claude", AgentState: state.AgentShell})
			d.adoptNativeSessions(context.Background())
			got, ok := d.sessions.Get("lola-local-shell")
			if !ok || !got.IsAgentless() || got.Agent != "claude" {
				t.Fatalf("shell reclassified after restart: %+v", got)
			}
			want := state.AgentDead
			if alive {
				want = state.AgentShell
			}
			if got.AgentState != want {
				t.Fatalf("shell state = %s, want %s", got.AgentState, want)
			}
		})
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
