package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/tmux"
	"github.com/sushidev-team/lola/internal/worktree"
)

// TestSandboxContainsTheAgent is the acceptance check for the container
// sandbox, against a REAL docker engine and a REAL tmux: the generated launch
// runs a script (standing in for the agent) inside the container exactly as a
// pane would, and the host is inspected afterwards. Opt-in, because it needs a
// docker engine and pulls an image:
//
//	LOLA_SANDBOX_IT=1 go test ./internal/runtime -run TestSandboxContainsTheAgent -v
//
// LOLA_SANDBOX_IT_IMAGE overrides the image (it needs sh + git).
func TestSandboxContainsTheAgent(t *testing.T) {
	if os.Getenv("LOLA_SANDBOX_IT") == "" {
		t.Skip("set LOLA_SANDBOX_IT=1 to run against a real docker engine")
	}
	for _, bin := range []string{"docker", "tmux", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	image := os.Getenv("LOLA_SANDBOX_IT_IMAGE")
	if image == "" {
		image = "alpine/git:latest"
	}
	f := newLinkedFixture(t)
	// The engine VM shares the user's directories, which the per-test temp
	// dirs (under /var/folders) are part of; resolve the symlinked prefix so
	// the host and container agree on every path.
	real := func(p string) string {
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	f.web, f.api, f.root = real(f.web), real(f.api), real(f.root)
	f.p.Path, f.p.LinkedRepos[0].Path = f.web, f.api
	f.p.Sandbox = config.SandboxDocker
	f.p.Symlinks = nil
	f.n.WT = &worktree.Manager{Root: f.root}
	f.n.Cfg.Projects[0] = f.p
	f.n.Cfg.Sandbox = config.SandboxConfig{Image: image}
	sock := fmt.Sprintf("lola-it-%d", os.Getpid())
	f.n.Tmux = &tmux.Client{SocketName: sock}
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", sock, "kill-server").Run() })

	ctx := context.Background()
	id := "lola-web-eng-42"
	dir, err := f.n.WT.Create(ctx, f.p, id, "lola/eng-42")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.n.pinSandbox(f.p.Name, dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gitguard.Clear(dir) })
	if err := excludeLolaDir(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := f.n.createLinked(ctx, f.p, f.p.LinkedRepos, dir, "lola/eng-42"); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	gcd := filepath.Join(f.web, ".git")
	escapes := map[string]string{
		"main checkout": filepath.Join(f.web, "escape.txt"),
		"home dir":      filepath.Join(home, fmt.Sprintf(".lola-sandbox-escape-%d", os.Getpid())),
		"git hooks":     filepath.Join(gcd, "hooks", "post-checkout"),
	}
	t.Cleanup(func() {
		for _, p := range escapes {
			_ = os.Remove(p)
		}
	})
	r := filepath.Join(dir, ".lola", "result")
	hijacked := filepath.Join(t.TempDir(), "host-ran-agent-config")
	script := strings.Join([]string{
		"git config --global --add safe.directory '*'",
		"echo hi > inside.txt && git add inside.txt && git -c user.name=t -c user.email=t@x commit -qm inside && echo commit-ok >> " + r,
		"(cd .lola/repos/api && echo hi > api.txt && git add api.txt && git -c user.name=t -c user.email=t@x commit -qm api) && echo linked-ok >> " + r,
		"git config core.hooksPath /tmp 2>/dev/null && echo config-written >> " + r,
		"echo x > " + escapes["main checkout"] + " 2>/dev/null",
		"echo x > " + escapes["home dir"] + " 2>/dev/null",
		"echo x > " + escapes["git hooks"] + " 2>/dev/null",
		// Re-point .git at a git dir of the agent's own whose config runs a
		// command — on whichever machine next runs git here.
		"git init -q --bare .evil; printf '[core]\\n\\tbare = false\\n\\tfsmonitor = \"touch " + hijacked + "; false\"\\n' > .evil/config && printf 'gitdir: %s\\n' \"$PWD/.evil\" > .git",
		"grep -q evil .git || echo pointer-kept >> " + r,
		"echo done >> " + r,
	}, "; ")
	line := "exec sh -c " + shQuote(f.n.sandboxLine(&f.p, id, agent.Kind("none"), script))
	if err := f.n.Tmux.NewSession(ctx, id, dir, line); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Minute) // first run pulls the image
	for f.n.Tmux.Has(ctx, id) && time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
	}
	out, _ := os.ReadFile(r)
	got := string(out)
	if !strings.Contains(got, "done") {
		t.Fatalf("container script did not finish; result %q", got)
	}
	if !strings.Contains(got, "commit-ok") || realGit(t, f.web, "log", "-1", "--format=%s", "lola/eng-42") != "inside" {
		t.Errorf("agent could not commit in its worktree (result %q)", got)
	}
	if !strings.Contains(got, "linked-ok") || realGit(t, f.api, "log", "-1", "--format=%s", "lola/eng-42") != "api" {
		t.Errorf("agent could not commit in its linked worktree (result %q)", got)
	}
	hooksPath, _ := exec.Command("git", "-C", f.web, "config", "--get", "core.hooksPath").Output()
	if strings.Contains(got, "config-written") || len(hooksPath) != 0 {
		t.Errorf("agent rewrote the repository's git config")
	}
	// The host's next git call on the worktree (a checkpoint, a diff, a dirty
	// check) must not follow the rewritten pointer.
	// The pointer is read-only in the container, so the rewrite fails; to test
	// the host-side pin on its own, do the rewrite here as if it had landed.
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+filepath.Join(dir, ".evil")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, _ = f.n.WT.WorkSummary(ctx, dir, "main") // the worktree manager's host git
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	gitguard.Apply(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("pinned host git failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(hijacked); err == nil {
		t.Error("host git ran a config the sandboxed agent planted")
	}
	if !strings.Contains(got, "pointer-kept") {
		t.Error("the agent rewrote its worktree's .git pointer")
	}
	for what, p := range escapes {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("agent wrote outside its worktree (%s): %s", what, p)
		}
	}
}
