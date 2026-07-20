package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// newRenameDaemon builds a daemon whose config is also ON DISK, which
// handleRenameProject requires: it rebases on config.DefaultPath rather than
// trusting the in-memory copy, and reloads from there afterwards.
func newRenameDaemon(t *testing.T, projects []config.Project) *Daemon {
	t.Helper()
	cfg := testConfig(projects...)
	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})

	path := filepath.Join(d.home, "config.toml")
	if err := cfg.Save(path); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return d
}

func renameArgs(t *testing.T, from, to string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(protocol.RenameProjectArgs{From: from, To: to})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return raw
}

// The happy path: the config entry is renamed in place and the seen guard
// follows it, so nothing the project already dispatched comes back.
func TestRenameProjectCarriesSeenState(t *testing.T) {
	d := newRenameDaemon(t, []config.Project{
		seenPoll("old"),
	})
	if err := d.seen.save("old", map[string]time.Time{"uuid-1": time.Now()}); err != nil {
		t.Fatalf("seed seen: %v", err)
	}

	data, err := d.handleRenameProject(context.Background(), renameArgs(t, "old", "new"))
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if data.To != "new" {
		t.Errorf("data.To = %q, want new", data.To)
	}

	if _, err := os.Stat(seenPath(d, "old")); !os.IsNotExist(err) {
		t.Errorf("old seen file still present (err = %v)", err)
	}
	seen, err := d.seen.load("new")
	if err != nil {
		t.Fatalf("load carried seen: %v", err)
	}
	if _, ok := seen["uuid-1"]; !ok {
		t.Errorf("seen under new name = %v, want the carried-over uuid-1", seen)
	}

	// The reload at the end must have made the new name live, not just on disk.
	if p := d.cfg.ProjectByName("new"); p == nil {
		t.Error("daemon config has no project \"new\" after rename")
	}
	if p := d.cfg.ProjectByName("old"); p != nil {
		t.Error("daemon config still carries the old name after rename")
	}
}

// A live session pins the old name into worktree paths and tmux session names,
// so the rename must refuse and say which sessions are in the way.
func TestRenameProjectRefusedWithLiveSessions(t *testing.T) {
	d := newRenameDaemon(t, []config.Project{
		labelPoll("old"),
	})
	d.sessions.Upsert(session.Session{ID: "lola-old-eng-1", Project: "old", Issue: "ENG-1"})

	data, err := d.handleRenameProject(context.Background(), renameArgs(t, "old", "new"))
	if err == nil {
		t.Fatal("rename succeeded despite a live session")
	}
	if len(data.Blockers) != 1 || data.Blockers[0] != "lola-old-eng-1" {
		t.Errorf("Blockers = %v, want the live session id", data.Blockers)
	}
	// Nothing may have been written on a refusal.
	if p := d.cfg.ProjectByName("old"); p == nil {
		t.Error("config lost the old project on a refused rename")
	}
	raw, err := os.ReadFile(filepath.Join(d.home, "config.toml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(raw), `"new"`) {
		t.Errorf("refused rename still wrote the new name to disk:\n%s", raw)
	}
}

// A worktree left behind by a half-failed kill is state nothing would resolve
// after the rename, so it also blocks.
func TestRenameProjectRefusedWithLeftoverWorktree(t *testing.T) {
	d := newRenameDaemon(t, []config.Project{
		labelPoll("old"),
	})
	stray := filepath.Join(d.home, "worktrees", "old", "lola-old-eng-9")
	if err := os.MkdirAll(stray, 0o700); err != nil {
		t.Fatalf("seed worktree: %v", err)
	}

	if _, err := d.handleRenameProject(context.Background(), renameArgs(t, "old", "new")); err == nil {
		t.Fatal("rename succeeded despite a leftover worktree")
	}
}

// An empty worktrees dir is inert leftover, not a blocker — and is cleaned up.
func TestRenameProjectDropsEmptyWorktreeDir(t *testing.T) {
	d := newRenameDaemon(t, []config.Project{
		labelPoll("old"),
	})
	empty := filepath.Join(d.home, "worktrees", "old")
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatalf("seed worktree dir: %v", err)
	}

	if _, err := d.handleRenameProject(context.Background(), renameArgs(t, "old", "new")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Errorf("empty worktrees/old survived the rename (err = %v)", err)
	}
}

func TestRenameProjectRejectsBadTargets(t *testing.T) {
	tests := []struct {
		name     string
		from, to string
		wantErr  string
	}{
		{"non-slug target", "old", "New Project", "not a valid project id"},
		{"name already taken", "old", "other", "already exists"},
		{"unknown source", "ghost", "new", "not found"},
		{"empty target", "old", "", "to is required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newRenameDaemon(t, []config.Project{
				labelPoll("old"),
				labelPoll("other"),
			})
			_, err := d.handleRenameProject(context.Background(), renameArgs(t, tc.from, tc.to))
			if err == nil {
				t.Fatalf("rename %q -> %q succeeded, want error", tc.from, tc.to)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// Renaming to the same name is a no-op, not an error: a client that saves a
// form without touching the id must not be punished for it.
func TestRenameProjectSameNameIsNoop(t *testing.T) {
	d := newRenameDaemon(t, []config.Project{
		labelPoll("old"),
	})
	data, err := d.handleRenameProject(context.Background(), renameArgs(t, "old", "old"))
	if err != nil {
		t.Fatalf("same-name rename: %v", err)
	}
	if data.Message == "" {
		t.Error("same-name rename returned no message")
	}
}

// seenStore.rename must refuse to overwrite an existing destination: that file
// is another project's dedup guard.
func TestSeenRenameRefusesToClobber(t *testing.T) {
	dir := t.TempDir()
	s := newSeenStore(dir)
	if err := s.save("a", map[string]time.Time{"x": time.Now()}); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := s.save("b", map[string]time.Time{"y": time.Now()}); err != nil {
		t.Fatalf("save b: %v", err)
	}
	if err := s.rename("a", "b"); err == nil {
		t.Fatal("rename clobbered an existing seen file")
	}
	seen, err := s.load("b")
	if err != nil {
		t.Fatalf("load b: %v", err)
	}
	if _, ok := seen["y"]; !ok {
		t.Errorf("b's seen map = %v, want its own entry intact", seen)
	}
}

// A project that never ticked has no seen file; carrying that over is success,
// not a missing-file error.
func TestSeenRenameMissingSourceIsFine(t *testing.T) {
	s := newSeenStore(t.TempDir())
	if err := s.rename("never-ticked", "new"); err != nil {
		t.Errorf("rename of a pollless project = %v, want nil", err)
	}
}
