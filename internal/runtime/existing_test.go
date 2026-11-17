package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/state"
)

func TestOpenExistingPreservesBranch(t *testing.T) {
	for _, useAgent := range []bool{false, true} {
		name := "shell"
		if useAgent {
			name = "agent"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "", "")
			s, err := f.n.OpenExisting(context.Background(), f.p, "lola-nori-open-feature", "feature", "Fix login", "codex", useAgent)
			if err != nil {
				t.Fatal(err)
			}
			if s.OwnsBranch() || s.HasAgent() != useAgent || s.LinearBound() {
				t.Fatalf("wrong ownership or agent: %+v", s)
			}
			if useAgent && (s.Agent != "codex" || s.AgentState != state.AgentStarting) {
				t.Fatalf("agent override lost: %+v", s)
			}
			if !useAgent && s.AgentState != state.AgentShell {
				t.Fatalf("not a shell: %+v", s)
			}
			if err := f.n.Kill(context.Background(), s, true, false); err != nil {
				t.Fatal(err)
			}
			calls := loggedArgs(t, f.gitLog)
			if !strings.Contains(calls, "worktree add "+s.Worktree+" feature") || strings.Contains(calls, "branch -D") || strings.Contains(calls, "worktree add -b") {
				t.Fatalf("must reuse and preserve the branch:\n%s", calls)
			}
		})
	}
}

func TestOpenExistingRollbackPreservesBranch(t *testing.T) {
	for _, useAgent := range []bool{false, true} {
		f := newFixture(t, "", `*"new-session"*) echo failed >&2; exit 1 ;;`)
		_, err := f.n.OpenExisting(context.Background(), f.p, "lola-nori-open-feature", "feature", "Fix login", "codex", useAgent)
		if err == nil {
			t.Fatal("want launch failure")
		}
		calls := loggedArgs(t, f.gitLog)
		if !strings.Contains(calls, "worktree remove") || strings.Contains(calls, "branch -D") {
			t.Fatalf("rollback must keep branch:\n%s", calls)
		}
	}
}
