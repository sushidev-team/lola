package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
)

type trustRecorder struct {
	trusted, untrusted []string
	err                error
}

func (r *trustRecorder) wire(n *Native) {
	n.TrustWorkspace = func(dir string) error { r.trusted = append(r.trusted, dir); return r.err }
	n.UntrustWorkspace = func(dir string) error { r.untrusted = append(r.untrusted, dir); return r.err }
}

func TestSpawnTrustsWorktreeBeforeLaunchAndKillUntrusts(t *testing.T) {
	f := newFixture(t, "", "")
	var rec trustRecorder
	rec.wire(f.n)
	ctx := context.Background()

	s, err := f.n.Spawn(ctx, f.p, issueENG42(), "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	dir := filepath.Join(f.root, "nori", s.ID)
	if !reflect.DeepEqual(rec.trusted, []string{dir}) {
		t.Fatalf("trusted = %v, want [%s]", rec.trusted, dir)
	}
	if err := f.n.Kill(ctx, s, true, false); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if !reflect.DeepEqual(rec.untrusted, []string{dir}) {
		t.Errorf("untrusted = %v, want [%s]", rec.untrusted, dir)
	}
}

func TestSpawnTrustFailureIsAdvisory(t *testing.T) {
	f := newFixture(t, "", "")
	rec := trustRecorder{err: errors.New("disk full")}
	rec.wire(f.n)
	var logged []string
	f.n.Logf = func(format string, args ...any) { logged = append(logged, format) }

	if _, err := f.n.Spawn(context.Background(), f.p, issueENG42(), ""); err != nil {
		t.Fatalf("a trust failure must not fail the spawn: %v", err)
	}
	if len(rec.trusted) != 1 || len(logged) == 0 {
		t.Errorf("trusted=%v logged=%v, want one attempt and an advisory", rec.trusted, logged)
	}
}

func TestSpawnRollbackUntrusts(t *testing.T) {
	// tmux new-session fails: the worktree is rolled back, and so is its trust.
	f := newFixture(t, "", `*"new-session"*)
  exit 1
  ;;`)
	var rec trustRecorder
	rec.wire(f.n)
	if _, err := f.n.Spawn(context.Background(), f.p, issueENG42(), ""); err == nil {
		t.Fatal("Spawn: want the tmux failure")
	}
	if len(rec.trusted) != 1 || !reflect.DeepEqual(rec.untrusted, rec.trusted) {
		t.Errorf("trusted=%v untrusted=%v, want the same dir trusted then untrusted", rec.trusted, rec.untrusted)
	}
}

func TestTrustAgentDirGates(t *testing.T) {
	off := false
	cases := []struct {
		name  string
		setup func(f *fixture)
		kind  agent.Kind
		dir   func(f *fixture) string
		want  bool
	}{
		{"claude in a worktree", func(*fixture) {}, agent.Claude, inRoot, true},
		{"codex", func(*fixture) {}, agent.Codex, inRoot, false},
		{"opencode", func(*fixture) {}, agent.OpenCode, inRoot, false},
		{"disabled by config", func(f *fixture) { f.n.Cfg.Defaults.TrustWorktrees = &off }, agent.Claude, inRoot, false},
		{"outside the worktree root", func(*fixture) {}, agent.Claude, func(f *fixture) string { return f.repo }, false},
		{"the root itself", func(*fixture) {}, agent.Claude, func(f *fixture) string { return f.root }, false},
		{"a sibling with the root as prefix", func(*fixture) {}, agent.Claude, func(f *fixture) string { return f.root + "-evil/x" }, false},
		{"sandboxed project", func(f *fixture) {
			f.n.Cfg.Projects[0].Sandbox = "docker"
		}, agent.Claude, inRoot, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "", "")
			var rec trustRecorder
			rec.wire(f.n)
			tc.setup(f)
			f.n.trustAgentDir(f.p.Name, "id", tc.dir(f), tc.kind)
			if got := len(rec.trusted) == 1; got != tc.want {
				t.Errorf("trusted = %v, want trust=%v", rec.trusted, tc.want)
			}
		})
	}
}

func inRoot(f *fixture) string { return filepath.Join(f.root, "nori", "lola-nori-eng-42") }

func TestUntrustIgnoresConfigAndKind(t *testing.T) {
	f := newFixture(t, "", "")
	off := false
	f.n.Cfg.Defaults.TrustWorktrees = &off
	var rec trustRecorder
	rec.wire(f.n)
	f.n.untrustAgentDir("id", inRoot(f))
	f.n.untrustAgentDir("id", f.repo) // never outside the root
	if !reflect.DeepEqual(rec.untrusted, []string{inRoot(f)}) {
		t.Errorf("untrusted = %v", rec.untrusted)
	}
}

func TestTrustClaudeWorktreesDefaultsOn(t *testing.T) {
	var c config.Config
	if !c.TrustClaudeWorktrees() {
		t.Error("unset trust_worktrees must default to true")
	}
	off := false
	c.Defaults.TrustWorktrees = &off
	if c.TrustClaudeWorktrees() {
		t.Error("trust_worktrees = false must disable it")
	}
}
