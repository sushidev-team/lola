package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/tmux"
)

// Exercise the real runtime's launch receipt across both post-launch persistence
// windows. Only tmux is a script: Git stores, launch artifacts and both daemon
// journals are real temporary files. No container or coding agent is started.
func TestGoalLaunchRecoversAfterPostLaunchPersistenceFailure(t *testing.T) {
	for _, failedStore := range []string{"sessions", "goals"} {
		t.Run(failedStore, func(t *testing.T) {
			d, _, g, policy, _ := goalExecutionFixture(t)
			ctx := context.Background()
			sessionDir := t.TempDir()
			d.sessions = session.NewStore(sessionDir)
			binDir := t.TempDir()
			bin := filepath.Join(binDir, "tmux")
			// Log only launch invocations, never the command or its environment.
			script := "#!/bin/sh\nfor arg in \"$@\"; do\n  if [ \"$arg\" = new-session ]; then printf 'launch\\n' >> launches; fi\ndone\nexit 0\n"
			if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			linuxHook := filepath.Join(binDir, "linux-lola")
			if err := os.WriteFile(linuxHook, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			d.cfg.Sandbox.LolaBin = linuxHook
			d.cfg.Sandbox.Image = "test-agent"
			newRuntime := func() *runtime.Native {
				return &runtime.Native{Home: d.home, Cfg: d.cfg,
					Tmux:      &tmux.Client{Bin: bin, Dir: binDir},
					HookToken: func(string) string { return "fixture-token" }}
			}
			native := newRuntime()
			failurePath := filepath.Join(sessionDir, "sessions.json")
			if failedStore == "goals" {
				failurePath = filepath.Join(d.home, "state", "goals.json")
			}
			var launched session.Session
			d.goalSpawn = func(ctx context.Context, current goal.Goal, attempt string) (session.Session, error) {
				s, err := native.SpawnGoal(ctx, current, attempt)
				if err != nil {
					t.Fatal(err)
				}
				launched = s
				// Preserve the pre-launch snapshot, then force the atomic rename
				// to fail after the real runtime has successfully launched.
				if err := os.Rename(failurePath, failurePath+".before"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(failurePath, 0700); err != nil {
					t.Fatal(err)
				}
				return s, nil
			}
			g, err := d.authorizeGoal(ctx, goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: policy})
			if err != nil {
				t.Fatal(err)
			}
			if err := d.runGoal(ctx, g.ID); err == nil || launched.ID == "" {
				t.Fatalf("expected a persistence failure after successful launch: %v", err)
			}
			reserved, _ := d.goals.Get(g.ID)
			attempt := reserved.Execution.Attempts[launched.AttemptID]
			if attempt.State != "reserved" || len(reserved.Execution.Attempts) != 1 {
				t.Fatal("failed outcome write advanced or lost the durable reservation")
			}
			dirtyPath := filepath.Join(launched.Worktree, "unfinished.txt")
			if err := os.WriteFile(dirtyPath, []byte("preserve worker output"), 0600); err != nil {
				t.Fatal(err)
			}
			// Repair only this test's injected storage failure and discard all
			// in-memory session/goal/runtime state, as a restarted daemon would.
			if err := os.Remove(failurePath); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(failurePath+".before", failurePath); err != nil {
				t.Fatal(err)
			}
			d.goals, err = goal.Open(filepath.Join(d.home, "state", "goals.json"))
			if err != nil {
				t.Fatal(err)
			}
			d.sessions = session.NewStore(sessionDir)
			d.goalSpawn = newRuntime().SpawnGoal
			d.listTmuxSessions = func(context.Context) ([]tmux.Session, error) {
				return []tmux.Session{{Name: launched.TmuxName}}, nil
			}
			for range 2 {
				if err := d.runGoal(ctx, g.ID); err != nil {
					t.Fatal(err)
				}
			}
			recovered, _ := d.goals.Get(g.ID)
			if len(recovered.Execution.Attempts) != 1 || len(recovered.Execution.Runs) != 1 || recovered.Execution.Attempts[attempt.ID].State != "running" {
				t.Fatal("recovery created another attempt/run or failed to adopt the reservation")
			}
			if recovered.Execution.Tasks[g.Tasks[1].ID].State != "pending" {
				t.Fatal("launch recovery released an unverified dependency")
			}
			stored, ok := session.NewStore(sessionDir).Get(launched.ID)
			if !ok || stored.Worktree != launched.Worktree || stored.AttemptID != attempt.ID {
				t.Fatal("recovery did not persist the original launch receipt")
			}
			log, err := os.ReadFile(filepath.Join(binDir, "launches"))
			if err != nil || strings.TrimSpace(string(log)) != "launch" {
				t.Fatalf("expected exactly one real launch invocation: %q, %v", log, err)
			}
			if data, err := os.ReadFile(dirtyPath); err != nil || string(data) != "preserve worker output" {
				t.Fatal("recovery changed private worker output")
			}
		})
	}
}
