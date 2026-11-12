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
	if err := f.n.refreshSandboxCredentials(s.Worktree); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(filepath.Join(s.Worktree, ".lola", "env")); string(after) != before {
		t.Fatalf("refresh appended twice:\n%s", after)
	}
}

func TestSandboxLoginMissingIgnoresOtherSessions(t *testing.T) {
	f := sandboxFixture(t)
	f.n.SandboxCredential = func(string) (string, bool) { return "", false }
	s, err := f.n.Spawn(context.Background(), f.p, issueENG42(), "")
	if err != nil {
		t.Fatal(err)
	}
	host := s
	host.Sandbox = ""
	codex := s
	codex.Agent = "codex"
	for name, c := range map[string]struct{ ok bool }{"host": {f.n.SandboxLoginMissing(host)}, "codex": {f.n.SandboxLoginMissing(codex)}} {
		if c.ok {
			t.Errorf("%s session reported as missing a Claude login", name)
		}
	}
	if !f.n.SandboxLoginAvailable(agent.Codex) {
		t.Error("codex gated on a Claude login")
	}
}
