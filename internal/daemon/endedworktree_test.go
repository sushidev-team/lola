package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/tmux"
	"github.com/sushidev-team/lola/internal/worktree"
)

type endedCleanupNative struct {
	fakeNative
	removed session.Session
}

func (n *endedCleanupNative) Kill(ctx context.Context, s session.Session, remove, force bool) error {
	n.removed = s
	return n.fakeNative.Kill(ctx, s, remove, force)
}

func endedCleanupFixture(t *testing.T) (*Daemon, *endedCleanupNative, session.Session) {
	t.Helper()
	d, _ := talkFakeDaemon(t)
	n := &endedCleanupNative{}
	d.native = n
	d.listTmuxSessions = func(context.Context) ([]tmux.Session, error) { return nil, nil }
	s := nativeSess("FE-42", "dead")
	s.Worktree = filepath.Join(d.home, "worktrees", s.Project, s.ID)
	if err := os.MkdirAll(s.Worktree, 0700); err != nil {
		t.Fatal(err)
	}
	d.recordEnded(s)
	// Exercise the legacy record shape of the reported checkout.
	if err := talkStore(t, d, s.ID).Update(func(st *sshstore.State) error { st.Ended.Isolation = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gitguard.Clear(s.Worktree) })
	return d, n, s
}

func TestRemoveEndedWorktreeUsesPersistedIsolationForDirtyChecks(t *testing.T) {
	for _, sandboxed := range []bool{false, true} {
		t.Run(map[bool]string{false: "host", true: "sandbox"}[sandboxed], func(t *testing.T) {
			d, n, s := endedCleanupFixture(t)
			if sandboxed {
				s.Sandbox = "docker"
				s.GitPin = &gitguard.Pin{GitDir: "/trusted/admin", CommonDir: "/trusted/common"}
			}
			if err := talkStore(t, d, s.ID).Update(func(st *sshstore.State) error { st.Ended = nil; return nil }); err != nil {
				t.Fatal(err)
			}
			d.recordEnded(s)
			if ended := sshstore.ReadEnded(d.home, s.ID); ended == nil || ended.Isolation == nil || ended.Isolation.Sandbox != s.Sandbox {
				t.Fatalf("isolation lost: %+v", ended)
			}
			n.killErr = worktree.ErrDirty
			_, err := d.handleRemoveEndedWorktree(context.Background(), s.ID, s.Worktree, false)
			if !errors.Is(err, worktree.ErrDirty) || len(n.killCalls()) != 1 || n.killCalls()[0].force {
				t.Fatalf("must check files without force: %v, %v", err, n.killCalls())
			}
			if n.removed.Sandbox != s.Sandbox {
				t.Fatalf("isolation changed: %+v", n.removed)
			}
			if sandboxed {
				if _, pin, ok := gitguard.Lookup(s.Worktree); !ok || pin != *s.GitPin {
					t.Fatal("trusted pin not restored")
				}
			}
		})
	}
}

func TestRemoveEndedWorktreeRequiresExplicitDiscardWithoutTrustedPin(t *testing.T) {
	d, n, s := endedCleanupFixture(t)
	_, err := d.handleRemoveEndedWorktree(context.Background(), s.ID, s.Worktree, false)
	if err == nil || !strings.Contains(err.Error(), "cannot be verified safely") || len(n.killCalls()) != 0 {
		t.Fatalf("unverified removal: err=%v calls=%v", err, n.killCalls())
	}
	before := sshstore.ReadEnded(d.home, s.ID)
	data, err := d.handleRemoveEndedWorktree(context.Background(), s.ID, s.Worktree, true)
	if err != nil || !data.Removed || len(n.killCalls()) != 1 {
		t.Fatalf("confirmed removal: data=%+v err=%v calls=%v", data, err, n.killCalls())
	}
	if n.removed.OwnsBranch() || n.removed.Branch != "" || n.removed.Sandbox == "" || !n.killCalls()[0].force {
		t.Fatalf("unsafe teardown scope: %+v", n.removed)
	}
	if _, pin, ok := gitguard.Lookup(s.Worktree); !ok || pin.Valid() {
		t.Fatal("unknown metadata must not be followed by host git")
	}
	after := sshstore.ReadEnded(d.home, s.ID)
	if after == nil || after.KeptWorktree != "" || after.EndedAt != before.EndedAt || after.Issue != before.Issue {
		t.Fatalf("history not preserved: %+v", after)
	}
	if _, ok := d.sessions.Get(s.ID); ok {
		t.Fatal("cleanup resurrected the session")
	}
}

func TestRemoveEndedWorktreeRetainsHistoryOnDirtyOrFailedRemoval(t *testing.T) {
	for _, failure := range []error{worktree.ErrDirty, errors.New("git failed")} {
		t.Run(failure.Error(), func(t *testing.T) {
			d, n, s := endedCleanupFixture(t)
			gitguard.Set(s.Worktree, gitguard.Pin{GitDir: "/trusted/admin", CommonDir: "/trusted/common"})
			n.killErr = failure
			_, err := d.handleRemoveEndedWorktree(context.Background(), s.ID, s.Worktree, false)
			if !errors.Is(err, failure) || len(n.killCalls()) != 1 || n.killCalls()[0].force {
				t.Fatalf("dirty check bypassed: %v, %v", err, n.killCalls())
			}
			if end := sshstore.ReadEnded(d.home, s.ID); end == nil || end.KeptWorktree != s.Worktree {
				t.Fatalf("failed removal erased retained path: %+v", end)
			}
		})
	}
}

func TestRemoveEndedWorktreeRefusesChangedOwnershipAndUnverifiedLiveness(t *testing.T) {
	for _, scenario := range []string{"current", "borrowed", "stack member", "ancestor", "terminal", "auxiliary", "tmux failure", "wrong path", "missing history", "symlink", "project removed"} {
		t.Run(scenario, func(t *testing.T) {
			d, n, s := endedCleanupFixture(t)
			path, id := s.Worktree, s.ID
			switch scenario {
			case "current":
				d.sessions.Upsert(s)
			case "borrowed":
				d.sessions.Upsert(session.Session{ID: "local", Kind: session.KindLocal, Worktree: path})
			case "stack member":
				d.sessions.Upsert(session.Session{ID: "stack", Stack: true, Apps: []session.App{{Worktree: path}}})
			case "ancestor":
				d.sessions.Upsert(session.Session{ID: "local", Kind: session.KindLocal, Worktree: filepath.Dir(path)})
			case "terminal", "auxiliary":
				name := id
				if scenario == "auxiliary" {
					name += "-dev-1"
				}
				d.listTmuxSessions = func(context.Context) ([]tmux.Session, error) { return []tmux.Session{{Name: name}}, nil }
			case "tmux failure":
				d.listTmuxSessions = func(context.Context) ([]tmux.Session, error) { return nil, errors.New("tmux unavailable") }
			case "wrong path":
				path = t.TempDir()
			case "missing history":
				id = "unknown"
			case "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), path); err != nil {
					t.Fatal(err)
				}
			case "project removed":
				d.cfg.Projects = nil
			}
			if _, err := d.handleRemoveEndedWorktree(context.Background(), id, path, true); err == nil {
				t.Fatal("unsafe removal accepted")
			}
			if len(n.killCalls()) != 0 {
				t.Fatalf("refusal called runtime: %v", n.killCalls())
			}
		})
	}
}
