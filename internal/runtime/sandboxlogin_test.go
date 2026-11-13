package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
)

// A sandboxed claude spawned before any Claude login existed is detected from
// the env file lola wrote, and Relogin relaunches it with the login that
// exists now — fresh, with the standard first prompt.
func TestReloginGivesALoginlessSandboxedSessionTheNewLogin(t *testing.T) {
	f := sandboxFixture(t)
	f.n.Cfg.Sandbox.PassEnv = nil // the default: CLAUDE_CODE_OAUTH_TOKEN, GH_TOKEN
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	token := ""
	f.n.SandboxCredential = func(name string) (string, bool) {
		if name == "CLAUDE_CODE_OAUTH_TOKEN" && token != "" {
			return token, true
		}
		return "", false
	}
	f.n.RemoveContainer = func(context.Context, string) error { return nil }

	s, err := f.n.Spawn(context.Background(), f.p, issueENG42(), "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if f.n.SandboxLoginAvailable(agent.Claude) {
		t.Fatal("login available with no credential anywhere")
	}
	if !f.n.SandboxLoginMissing(s) {
		t.Fatal("a session spawned without a login is not reported missing")
	}

	token = "sk-ant-oat01-new"
	if !f.n.SandboxLoginAvailable(agent.Claude) {
		t.Fatal("stored token not seen as a login")
	}
	if _, err := f.n.Relogin(context.Background(), s); err != nil {
		t.Fatalf("Relogin: %v", err)
	}
	env, err := os.ReadFile(filepath.Join(s.Worktree, ".lola", "env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-new\n") {
		t.Fatalf("env lacks the new login:\n%s", env)
	}
	if f.n.SandboxLoginMissing(s) {
		t.Fatal("still missing after Relogin: it would relaunch again")
	}
	log := loggedArgs(t, f.tmuxLog)
	last := log[strings.LastIndex(log, "new-session"):]
	if strings.Contains(last, "--continue") || !strings.Contains(last, "prompt.md") {
		t.Fatalf("relaunch is not a fresh start with the briefing:\n%s", last)
	}
	// A second refresh appends nothing new.
	before := string(env)
	if err := f.n.refreshSandboxCredentials(s.ID, s.Worktree, agent.Claude); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(filepath.Join(s.Worktree, ".lola", "env")); string(after) != before {
		t.Fatalf("refresh appended twice:\n%s", after)
	}
}

func TestSandboxLoginForCodexUsesTheHostLogin(t *testing.T) {
	f := sandboxFixture(t)
	f.n.SandboxCredential = func(string) (string, bool) { return "", false }
	host := filepath.Join(t.TempDir(), "auth.json")
	hostAuthFile = func(k agent.Kind) string {
		if k == agent.Codex {
			return host
		}
		return ""
	}
	t.Cleanup(func() { hostAuthFile = agent.HostAuthFile })

	if f.n.SandboxLoginAvailable(agent.Codex) {
		t.Fatal("codex logged in with no host login")
	}
	s, err := f.n.Spawn(context.Background(), f.p, issueENG42(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if !f.n.SandboxLoginMissing(s) {
		t.Fatal("codex session without a login not reported")
	}
	if err := os.WriteFile(host, []byte(`{"tokens":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !f.n.SandboxLoginAvailable(agent.Codex) {
		t.Fatal("host login not seen")
	}
	if err := f.n.refreshSandboxCredentials(s.ID, s.Worktree, agent.Codex); err != nil {
		t.Fatal(err)
	}
	auth := filepath.Join(f.n.Home, "sandbox", s.ID, "codex", "auth.json")
	got, err := os.ReadFile(auth)
	if err != nil || string(got) != `{"tokens":"x"}` {
		t.Fatalf("login not copied: %q %v", got, err)
	}
	if fi, _ := os.Stat(auth); fi.Mode().Perm() != 0o600 {
		t.Fatalf("copied login mode %v", fi.Mode())
	}
	if f.n.SandboxLoginMissing(s) {
		t.Fatal("still missing after the copy")
	}
	host2 := s
	host2.Sandbox = ""
	if f.n.SandboxLoginMissing(host2) {
		t.Fatal("a host session reported")
	}
}

// The agent's private state lives outside the worktree (repository tools
// must not walk it), is created and mounted for the launch, keeps a session
// that already has it in the worktree, and goes with the worktree.
func TestSandboxAgentStateLivesOutsideTheWorktree(t *testing.T) {
	f := sandboxFixture(t)
	f.n.SandboxCredential = func(string) (string, bool) { return "", false }
	f.n.RemoveContainer = func(context.Context, string) error { return nil }
	s, err := f.n.Spawn(context.Background(), f.p, issueENG42(), "")
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(f.n.Home, "sandbox", s.ID, "claude")
	env, _ := os.ReadFile(filepath.Join(s.Worktree, ".lola", "env"))
	if !strings.Contains(string(env), "CLAUDE_CONFIG_DIR="+shQuote(state)+"\n") {
		t.Fatalf("CLAUDE_CONFIG_DIR not outside the worktree:\n%s", env)
	}
	if !isDir(filepath.Join(f.n.Home, "sandbox", s.ID)) {
		t.Fatal("state folder not created before the launch")
	}
	if _, err := os.Stat(filepath.Join(s.Worktree, ".lola", "claude")); err == nil {
		t.Fatal("state still written into the worktree")
	}

	// A session started before the move keeps its in-worktree state.
	legacy := filepath.Join(s.Worktree, ".lola", "codex")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := f.n.agentStateDir(s.ID, s.Worktree, agent.Codex); got != legacy {
		t.Fatalf("legacy codex home = %q", got)
	}

	// Teardown that removes the worktree removes the state; one that keeps it does not.
	f.n.pruneSandboxState(s)
	if !isDir(filepath.Join(f.n.Home, "sandbox", s.ID)) {
		t.Fatal("state pruned while the worktree remains")
	}
	if err := os.RemoveAll(s.Worktree); err != nil {
		t.Fatal(err)
	}
	f.n.pruneSandboxState(s)
	if isDir(filepath.Join(f.n.Home, "sandbox", s.ID)) {
		t.Fatal("state kept after the worktree went")
	}
}
