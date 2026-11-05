package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/session"
)

func sshNative(t *testing.T, cfg *config.Config) (*Native, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	return &Native{Cfg: cfg, Home: home, LolaBin: "/opt/lola"}, home
}

func TestSSHTabIsAuxAndAnchored(t *testing.T) {
	for _, name := range []string{"lola-x-1-ssh-1", "lola-local-abc-ssh-12"} {
		if !IsAuxSession(name) {
			t.Errorf("%s is not aux", name)
		}
	}
	if !isAuxOf("lola-x-1", "lola-x-1-ssh-2") {
		t.Error("own tab not recognized")
	}
	// A sibling sharing a prefix: teardown must not kill its terminal.
	if isAuxOf("lola-x-1", "lola-x-10-ssh-2") || isAuxOf("lola-x-1", "lola-x-1-ssh-2x") || isAuxOf("lola-x-1", "lola-x-1-ssh-") {
		t.Error("the suffix match is loose")
	}
}

func TestWithSSHBriefingOnlyWhenHostsAreConfigured(t *testing.T) {
	n, _ := sshNative(t, &config.Config{})
	if got := n.withSSHBriefing([]byte("x")); string(got) != "x" {
		t.Fatalf("a machine with no hosts reads about ssh: %q", got)
	}
	n, _ = sshNative(t, &config.Config{SSHHosts: []config.SSHHost{{Alias: "web"}, {Alias: "db"}}})
	got := string(n.withSSHBriefing([]byte("x")))
	for _, want := range []string{"db, web", "ssh run", "--why", "--cwd", "241", "243", "untrusted", "post narration", "post proposal", "post question", "--evidence", "model provider", "Do not run ssh yourself"} {
		if !strings.Contains(got, want) {
			t.Errorf("briefing misses %q", want)
		}
	}
	// Parity across agents is that it is a plain shell command: nothing per-agent.
	if strings.Contains(got, "claude") || strings.Contains(got, "codex") {
		t.Error("the briefing should not vary by agent")
	}
}

func TestSSHPromptWaitsWithoutABrief(t *testing.T) {
	n, _ := sshNative(t, &config.Config{SSHHosts: []config.SSHHost{{Alias: "web", Tier: "dev"}}})
	got := string(n.sshPrompt(SSHOpenSpec{ID: "lola-local-a", Host: config.SSHHost{Alias: "web", Tier: "dev"}}))
	if !strings.Contains(got, "Do not run commands on your own initiative") {
		t.Fatalf("no wait instruction: %s", got)
	}
	got = string(n.sshPrompt(SSHOpenSpec{ID: "lola-local-a", Host: config.SSHHost{Alias: "web", Tier: "dev"}, Brief: "read the logs"}))
	if !strings.Contains(got, "read the logs") || strings.Contains(got, "own initiative") {
		t.Fatalf("%s", got)
	}
}

func TestSandboxScratchLineMountsOnlyScratch(t *testing.T) {
	cfg := &config.Config{Sandbox: config.SandboxConfig{Image: "lola-img", LolaBin: "/linux/lola"}}
	n, home := sshNative(t, cfg)
	dir := filepath.Join(home, "local-sessions", "lola-local-a", "work")
	line := n.sandboxScratchLine("lola-local-a", dir, agent.Claude, "exec claude")
	for _, want := range []string{"rm -f", "run --rm -it --init", "--name lola-local-a", "no-new-privileges", "-w " + dir, "-v " + dir + ":" + dir, "/linux/lola:/opt/lola:ro", "--entrypoint sh lola-img"} {
		if !strings.Contains(line, want) {
			t.Errorf("line misses %q:\n%s", want, line)
		}
	}
	userHome, _ := os.UserHomeDir()
	for _, forbidden := range []string{".ssh", "SSH_AUTH_SOCK", "-v " + userHome + ":", "-v " + home + ":"} {
		if strings.Contains(line, forbidden) {
			t.Errorf("line mounts %q:\n%s", forbidden, line)
		}
	}
	mounts := strings.Count(line, " -v ")
	if mounts > 3 { // scratch, transcripts, the Linux lola
		t.Errorf("%d mounts: %s", mounts, line)
	}
}

func TestIsSSHScratch(t *testing.T) {
	n, home := sshNative(t, &config.Config{})
	if !n.isSSHScratch(filepath.Join(home, "local-sessions", "lola-local-a", "work")) {
		t.Error("the scratch folder is not recognized")
	}
	for _, d := range []string{"/tmp/work", filepath.Join(home, "worktrees", "p", "work"), filepath.Join(home, "local-sessions", "other", "work"), filepath.Join(home, "local-sessions", "lola-local-a", "other")} {
		if n.isSSHScratch(d) {
			t.Errorf("%s vouched for", d)
		}
	}
}

func TestSSHPosixHostAndSandbox(t *testing.T) {
	n, home := sshNative(t, &config.Config{Sandbox: config.SandboxConfig{Image: "i", LolaBin: "/linux/lola"}})
	artifacts := filepath.Join(home, "local-sessions", "lola-local-a")
	work := filepath.Join(artifacts, "work")
	host := session.Session{ID: "lola-local-a", Worktree: work, Agent: "claude", Kind: session.KindLocal, Location: LocationSSH}
	line := n.sshPosix(context.Background(), host, artifacts, false)
	if !strings.Contains(line, filepath.Join(artifacts, "settings.json")) || !strings.Contains(line, filepath.Join(artifacts, "env")) || strings.Contains(line, "docker") {
		t.Fatalf("host: %s", line)
	}
	box := host
	box.Sandbox = config.SandboxDocker
	line = n.sshPosix(context.Background(), box, artifacts, false)
	if !strings.Contains(line, "docker") || !strings.Contains(line, ".lola/settings.json") {
		t.Fatalf("sandbox: %s", line)
	}
}

// Issue SSH-42 of project infra is "lola-infra-ssh-42". Only a parent, live or on
// record, makes such a name a tab; a name with its own record is a session.
func TestIsSSHTabNeedsAParent(t *testing.T) {
	recorded := map[string]bool{"lola-x-1": true, "lola-infra-ssh-42": true}
	n := &Native{SessionRecorded: func(id string) bool { return recorded[id] }}
	live := map[string]bool{"lola-y-2": true}
	for name, want := range map[string]bool{
		"lola-x-1-ssh-1":    true,  // parent recorded, agent dead
		"lola-y-2-ssh-1":    true,  // parent live
		"lola-infra-ssh-42": false, // its own record
		"lola-web-ssh-7":    false, // no such parent: a session whose record was lost
		"lola-x-1":          false,
	} {
		if got := n.isSSHTab(name, live); got != want {
			t.Errorf("isSSHTab(%q) = %v, want %v", name, got, want)
		}
	}
}
