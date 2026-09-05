package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/state"
)

// The takeover respawn: the old pane goes down, the new kind's artifacts and
// env replace the old, the handoff briefing lands in .lola, and the session
// record flips to the new kind in a fresh "starting" axis.
func TestSwitchAgentHappyPath(t *testing.T) {
	f := newFixture(t, `
*"log --oneline"*)
  echo "abc123 fix login flow"
  exit 0
  ;;
*"diff --stat"*)
  echo " main.go | 2 ++"
  exit 0
  ;;
*"status --porcelain"*)
  echo " M main.go"
  exit 0
  ;;
`, "")
	ctx := context.Background()
	sess, err := f.n.Spawn(ctx, f.p, issueENG42(), "")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if sess.Agent != "claude" {
		t.Fatalf("spawned agent = %q, want claude (default)", sess.Agent)
	}

	s2, err := f.n.SwitchAgent(ctx, sess, agent.Codex, "hit its usage limit", "old pane output")
	if err != nil {
		t.Fatalf("SwitchAgent: %v", err)
	}

	if s2.Agent != "codex" {
		t.Errorf("Agent = %q, want codex", s2.Agent)
	}
	if s2.AgentState != state.AgentStarting {
		t.Errorf("AgentState = %q, want starting", s2.AgentState)
	}
	if s2.AtPrompt || s2.AtPromptVerified {
		t.Errorf("the carried send-keys gate must be closed and unverified, got AtPrompt=%v verified=%v", s2.AtPrompt, s2.AtPromptVerified)
	}
	if s2.Status != "working" {
		t.Errorf("Status = %q, want working (starting rolls up to working)", s2.Status)
	}

	dir := sess.Worktree
	handoff, err := os.ReadFile(filepath.Join(dir, ".lola", "handoff.md"))
	if err != nil {
		t.Fatalf("read handoff.md: %v", err)
	}
	for _, want := range []string{
		"ENG-42", "Fix login flow", "hit its usage limit", "codex",
		"lola/eng-42", "abc123 fix login flow", "M main.go", "old pane output",
	} {
		if !strings.Contains(string(handoff), want) {
			t.Errorf("handoff.md missing %q:\n%s", want, handoff)
		}
	}

	assertAbsent(t, filepath.Join(dir, ".lola", "codex", "config.toml"))
	env, err := os.ReadFile(filepath.Join(dir, ".lola", "env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if !strings.Contains(string(env), "CODEX_HOME=") {
		t.Errorf("env must carry CODEX_HOME for a codex session:\n%s", env)
	}

	tmuxLog := loggedArgs(t, f.tmuxLog)
	if !strings.Contains(tmuxLog, "kill-session") {
		t.Errorf("old pane was not killed:\n%s", tmuxLog)
	}
	// The relaunch line carries the new binary and the takeover prompt.
	if !strings.Contains(tmuxLog, "codex") || !strings.Contains(tmuxLog, "taking over from claude") {
		t.Errorf("relaunch must run codex with the handoff prompt:\n%s", tmuxLog)
	}
}

// A switch to opencode writes the plugin artifact and excludes .opencode/
// from the worktree's git status.
func TestSwitchAgentToOpenCode(t *testing.T) {
	f := newFixture(t, "", "")
	ctx := context.Background()
	sess, err := f.n.Spawn(ctx, f.p, issueENG42(), "")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if _, err := f.n.SwitchAgent(ctx, sess, agent.OpenCode, "switched by the operator", ""); err != nil {
		t.Fatalf("SwitchAgent: %v", err)
	}
	dir := sess.Worktree
	if _, err := os.Stat(filepath.Join(dir, ".opencode", "plugins", "lola-hook.js")); err != nil {
		t.Errorf("opencode plugin not written: %v", err)
	}
	exclude, err := os.ReadFile(filepath.Join(f.commonDir, "info", "exclude"))
	if err != nil {
		t.Fatalf("read info/exclude: %v", err)
	}
	if !strings.Contains(string(exclude), ".opencode/") {
		t.Errorf(".opencode/ must be git-excluded:\n%s", exclude)
	}
}

// Fail-closed guards: no worktree → no switch; switching to the running kind
// is a no-op error.
func TestSwitchAgentRefusals(t *testing.T) {
	f := newFixture(t, "", "")
	ctx := context.Background()
	sess, err := f.n.Spawn(ctx, f.p, issueENG42(), "")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	gone := sess
	gone.Worktree = filepath.Join(f.root, "nori", "no-such-dir")
	if _, err := f.n.SwitchAgent(ctx, gone, agent.Codex, "hit its usage limit", ""); err == nil || !strings.Contains(err.Error(), "worktree") {
		t.Errorf("gone worktree: err = %v, want a worktree refusal", err)
	}

	if _, err := f.n.SwitchAgent(ctx, sess, agent.Claude, "switched by the operator", ""); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("same kind: err = %v, want an already-running refusal", err)
	}
}

// The briefing renderer keeps pane text plain: control chars are stripped and
// an embedded fence cannot break the markdown.
func TestRenderHandoffSanitizesPaneTail(t *testing.T) {
	f := newFixture(t, "", "")
	sess, err := f.n.Spawn(context.Background(), f.p, issueENG42(), "")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	out := renderHandoff(sess, agent.Codex, "hit its usage limit", "", "", "", "line1\x1b[31mred\x1b[0m\n```\nfence")
	if strings.Contains(out, "\x1b") {
		t.Error("pane tail control chars must be stripped")
	}
	if strings.Contains(out, "[31m") || strings.Contains(out, "[0m") {
		t.Errorf("CSI sequences must be stripped whole, not just the ESC byte:\n%s", out)
	}
	if !strings.Contains(out, "line1red") {
		t.Errorf("the visible text survives the strip: %q missing", "line1red")
	}
	if strings.Contains(out, "\n```\nfence\n```") {
		t.Error("an embedded fence must be defused, not close the block early")
	}
}

// Switching an agent re-renders the project's env template against the session
// so per-session placeholders like {{.Session}} never land as literal text in
// .lola/env.
func TestSwitchAgentExpandsProjectEnv(t *testing.T) {
	f := newFixture(t, "", "")
	f.n.Cfg.Projects[0].Env = map[string]string{"LOLA_TEST_QUEUE": "{{.Session}}"}
	f.p.Env = f.n.Cfg.Projects[0].Env
	ctx := context.Background()

	sess, err := f.n.Spawn(ctx, f.p, issueENG42(), "")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	if _, err := f.n.SwitchAgent(ctx, sess, agent.Codex, "hit its usage limit", ""); err != nil {
		t.Fatalf("SwitchAgent: %v", err)
	}

	dir := sess.Worktree
	env, err := os.ReadFile(filepath.Join(dir, ".lola", "env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	want := "LOLA_TEST_QUEUE=" + sess.TmuxName + "\n"
	if !strings.Contains(string(env), want) {
		t.Errorf(".lola/env missing expanded session id:\nwant %q\ngot:\n%s", want, env)
	}
	if strings.Contains(string(env), "{{.Session}}") {
		t.Errorf(".lola/env must not contain literal {{.Session}} placeholder:\n%s", env)
	}
}
