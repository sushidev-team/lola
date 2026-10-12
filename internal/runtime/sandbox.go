package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/usage"
)

// The container SANDBOX ([[project]].sandbox = "docker"). The agent's pane is
// still a tmux pane on lola's own server — only the process inside it changes:
// instead of exec'ing the agent, the pane execs `docker run -it` and the agent
// runs in the container. Everything that reads a pane (attention, the observer,
// send-keys, the terminal views) is therefore untouched, and liveness is still
// the pane's: the docker CLI lives exactly as long as the container's agent.
//
// What the container can reach is the whole point, so it is spelled out:
//
//	read-write  the session's worktree (linked-repo worktrees live INSIDE it,
//	            at .lola/repos/<name>), the repositories' git object/ref stores
//	            (a commit writes there — without them the agent cannot commit),
//	            and claude's transcript directory for this worktree (lola's
//	            usage, claim audit and revive read it on the host)
//	read-only   each git store's `config` and `hooks` (a writable config or
//	            hooks dir would let the agent plant core.hooksPath / a hook
//	            that the HOST runs on the next commit in the main checkout —
//	            a sandbox escape), the Linux lola binary, and the targets of
//	            the project's `symlinks`
//	nothing     else: not $HOME, not ~/.lola, not the project's main checkout
//
// Hooks cannot use the daemon's unix socket (a host unix socket does not
// survive a bind mount into Docker Desktop's / OrbStack's VM), so the env file
// points `lola hook` at the daemon's loopback HOOK BRIDGE through
// host.docker.internal, with a per-session token (see internal/hook/token.go
// and internal/daemon/hookbridge.go).

// sandboxLabel tags every container lola starts, so `docker ps --filter
// label=dev.sushi.lola.session` lists them.
const sandboxLabel = "dev.sushi.lola.session"

// sandboxHost is how a container reaches the host's loopback listeners.
// Docker Desktop and OrbStack resolve it natively; on a plain Linux engine the
// --add-host mapping below supplies it.
const sandboxHost = "host.docker.internal"

// sandboxClaudeDir is the claude config dir a sandboxed claude uses, inside
// the worktree's (git-excluded) .lola: the image's own ~/.claude is not the
// user's, and the keychain the host claude logs in through is out of reach,
// so claude gets a private config dir seeded past its first-run dialogs.
const sandboxClaudeDir = lolaDir + "/claude"

// sandboxCodexDir is the CODEX_HOME of a sandboxed codex (the user's
// ~/.codex is not mounted).
const sandboxCodexDir = lolaDir + "/codex"

// sandboxProject returns the project of session id when its agents run in a
// container, else nil.
func (n *Native) sandboxProject(id string) *config.Project {
	if n.Cfg == nil {
		return nil
	}
	p := n.Cfg.ProjectByName(n.projectForSessionName(id))
	if p == nil || !p.Sandboxed() {
		return nil
	}
	return p
}

// SandboxFor is the sandbox kind a NEW session of project would record ("" =
// runs on the host).
func (n *Native) SandboxFor(project string) string {
	if p := n.Cfg.ProjectByName(project); p != nil && p.Sandboxed() {
		return config.SandboxDocker
	}
	return ""
}

// sandboxEnv is the env-file block a sandboxed agent additionally sources
// INSIDE the container: where the hook bridge is, this session's token, the
// agent's private config home, and the operator's pass_env credentials. Like
// the rest of the env file it is 0600 and never logged, and every value is
// shQuote'd.
func (n *Native) sandboxEnv(p config.Project, id, dir string, kind agent.Kind) string {
	var b strings.Builder
	fmt.Fprintf(&b, "LOLA_SANDBOX=%s\n", shQuote(config.SandboxDocker))
	fmt.Fprintf(&b, "LOLA_HOOK_ADDR=%s\n", shQuote(sandboxHost+":"+strconv.Itoa(n.Cfg.Sandbox.EffectiveHookPort())))
	if n.HookToken != nil {
		if tok := n.HookToken(id); tok != "" {
			fmt.Fprintf(&b, "LOLA_HOOK_TOKEN=%s\n", shQuote(tok))
		}
	}
	switch kind {
	case agent.Claude:
		fmt.Fprintf(&b, "CLAUDE_CONFIG_DIR=%s\n", shQuote(filepath.Join(dir, sandboxClaudeDir)))
	case agent.Codex:
		fmt.Fprintf(&b, "CODEX_HOME=%s\n", shQuote(filepath.Join(dir, sandboxCodexDir)))
	}
	for _, name := range n.Cfg.Sandbox.PassEnv {
		if !envNameRe.MatchString(name) { // defense-in-depth, mirrors envFile
			continue
		}
		if v, ok := os.LookupEnv(name); ok {
			fmt.Fprintf(&b, "%s=%s\n", name, shQuote(v))
		}
	}
	return b.String()
}

// sandboxLine wraps an agent's POSIX launch line (inner — what runs INSIDE the
// container, unchanged from a host launch) into the HOST line the pane runs:
//
//	docker rm -f <id>          a revive must not collide with a container the
//	                           dead pane left behind (-it containers outlive a
//	                           killed CLI: docker proxies no signal to a TTY)
//	git rev-parse … per repo   the git stores to mount (asked of git, so a
//	                           moved .git or a separate git dir still resolves)
//	exec docker run …          the container, foreground, attached to the pane
//
// The result is still POSIX sh; launchLine wraps it in the same `sh -c` as a
// host launch.
func (n *Native) sandboxLine(p *config.Project, id string, kind agent.Kind, inner string) string {
	dir := filepath.Join(n.WT.Root, p.Name, id)
	docker := shQuote(n.Cfg.Sandbox.DockerBin())
	var b strings.Builder
	fmt.Fprintf(&b, "%s rm -f %s >/dev/null 2>&1; ", docker, shQuote(id))

	// The git stores: the primary worktree's, then each linked repo's (whose
	// worktree may be missing — a project gained a linked repo after this
	// session spawned — so those are optional).
	repoDirs := []string{dir}
	for _, l := range p.LinkedRepos {
		repoDirs = append(repoDirs, LinkedWorktreeDir(dir, l.Name))
	}
	for i, rd := range repoDirs {
		v := "lola_gcd_" + strconv.Itoa(i)
		fmt.Fprintf(&b, `%s=$(git -C %s rev-parse --path-format=absolute --git-common-dir 2>/dev/null)`, v, shQuote(rd))
		if i == 0 {
			fmt.Fprintf(&b, ` && test -d "$%s" || { echo 'lola: cannot resolve the git directory of this worktree' >&2; exit 1; }`, v)
		}
		b.WriteString("; ")
	}

	transcripts := ""
	if kind == agent.Claude {
		if root := usage.ProjectsRoot(); root != "" {
			transcripts = usage.SlugDir(root, dir)
			fmt.Fprintf(&b, "mkdir -p %s; ", shQuote(transcripts))
		}
	}

	fmt.Fprintf(&b, "exec %s run --rm -it --init --name %s --label %s", docker, shQuote(id), shQuote(sandboxLabel+"="+id))
	fmt.Fprintf(&b, " --add-host %s --security-opt no-new-privileges", shQuote(sandboxHost+":host-gateway"))
	fmt.Fprintf(&b, " -w %s -v %s", shQuote(dir), shQuote(dir+":"+dir))
	for i := range repoDirs {
		v := "$lola_gcd_" + strconv.Itoa(i)
		// ${v:+…} so an unresolvable OPTIONAL store adds nothing rather than an
		// empty -v. The rw store first; the read-only overlays after it, so they
		// win for config + hooks.
		fmt.Fprintf(&b, ` ${lola_gcd_%d:+-v} ${lola_gcd_%d:+"%s:%s"}`, i, i, v, v)
		fmt.Fprintf(&b, ` ${lola_gcd_%d:+-v} ${lola_gcd_%d:+"%s/config:%s/config:ro"}`, i, i, v, v)
		fmt.Fprintf(&b, ` ${lola_gcd_%d:+-v} ${lola_gcd_%d:+"%s/hooks:%s/hooks:ro"}`, i, i, v, v)
	}
	if transcripts != "" {
		fmt.Fprintf(&b, " -v %s", shQuote(transcripts+":"+transcripts))
	}
	if lb, host := n.Cfg.Sandbox.LolaBin, n.lolaBin(); lb != "" && filepath.IsAbs(host) {
		fmt.Fprintf(&b, " -v %s", shQuote(lb+":"+host+":ro"))
	}
	for _, rel := range p.Symlinks {
		if !filepath.IsLocal(rel) {
			continue
		}
		target := filepath.Join(p.Path, rel)
		if _, err := os.Stat(target); err == nil {
			fmt.Fprintf(&b, " -v %s", shQuote(target+":"+target+":ro"))
		}
	}
	for _, spec := range p.SandboxPorts {
		if h, c, ok := config.SandboxPortPair(spec); ok {
			fmt.Fprintf(&b, " -p %s", shQuote("127.0.0.1:"+strconv.Itoa(h)+":"+strconv.Itoa(c)))
		}
	}
	image := n.Cfg.SandboxImageFor(p)
	// --entrypoint: an image's own entrypoint (git for alpine/git, a wrapper
	// script for many toolchain images) would otherwise receive `sh -c …` as
	// ITS arguments. The image contributes a filesystem, not a launcher.
	fmt.Fprintf(&b, " --entrypoint sh %s -c %s", shQuote(image), shQuote(sandboxPrelude(dir, kind, transcripts)+inner))
	return b.String()
}

// sandboxPrelude prepares the agent's private home INSIDE the container,
// idempotently (a revive runs it again): for claude, the config dir with its
// first-run state marked done — onboarding and the workspace trust dialog are
// modals that would park a fresh session forever — and the transcript
// directory linked to the host's, so the transcript lands where lola reads it.
func sandboxPrelude(dir string, kind agent.Kind, transcripts string) string {
	switch kind {
	case agent.Claude:
		cfg := filepath.Join(dir, sandboxClaudeDir)
		state, _ := json.Marshal(map[string]any{
			"hasCompletedOnboarding": true,
			"projects":               map[string]any{dir: map[string]any{"hasTrustDialogAccepted": true}},
		})
		s := fmt.Sprintf("mkdir -p %s; test -f %s || printf '%%s' %s > %s; ",
			shQuote(filepath.Join(cfg, "projects")), shQuote(filepath.Join(cfg, ".claude.json")),
			shQuote(string(state)), shQuote(filepath.Join(cfg, ".claude.json")))
		if transcripts != "" {
			s += fmt.Sprintf("ln -sfn %s %s; ", shQuote(transcripts), shQuote(filepath.Join(cfg, "projects", filepath.Base(transcripts))))
		}
		return s
	case agent.Codex:
		return fmt.Sprintf("mkdir -p %s; ", shQuote(filepath.Join(dir, sandboxCodexDir)))
	}
	return ""
}

// removeContainer stops and deletes a sandboxed session's container. Teardown
// needs it explicitly: killing the pane kills the docker CLI, and a container
// started with a TTY does not stop with its client.
func (n *Native) removeContainer(ctx context.Context, id string) error {
	if n.RemoveContainer != nil {
		return n.RemoveContainer(ctx, id)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, n.Cfg.Sandbox.DockerBin(), "rm", "-f", id).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(strings.ToLower(msg), "no such container") {
			return nil
		}
		return fmt.Errorf("docker rm -f %s: %w: %s", id, err, msg)
	}
	return nil
}
