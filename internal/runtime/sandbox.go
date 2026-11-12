package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/session"
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
//	read-write  the session's worktree (a stack session's FOLDER, with its app
//	            worktrees inside it), the repositories' git object/ref stores
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

// sandboxOpenCodeDir is the XDG_DATA_HOME of a sandboxed opencode: its
// data (sessions, auth.json) lives in the worktree, not the image's home.
const sandboxOpenCodeDir = lolaDir + "/opencode"

// sandboxAuthPath is where a sandboxed agent of kind reads the login lola
// copies in ("" for claude, which logs in through the env file).
func sandboxAuthPath(dir string, kind agent.Kind) string {
	switch kind {
	case agent.Codex:
		return filepath.Join(dir, sandboxCodexDir, "auth.json")
	case agent.OpenCode:
		return filepath.Join(dir, sandboxOpenCodeDir, "opencode", "auth.json")
	}
	return ""
}

// seedAgentLogin copies the host's codex or opencode login into the session's
// private config (0600, under the git-excluded .lola/), so the sandboxed agent
// works with the person's own subscription. The copy is the container's:
// a token the agent refreshes there does not reach the host file. Best
// effort — a missing host login leaves the agent to report it.
func (n *Native) seedAgentLogin(dir string, kind agent.Kind) {
	dst := sandboxAuthPath(dir, kind)
	src := hostAuthFile(kind)
	if dst == "" || src == "" {
		return
	}
	body, err := os.ReadFile(src)
	if err != nil || len(body) == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return
	}
	if err := os.WriteFile(dst, body, 0o600); err != nil && n.Logf != nil {
		n.Logf("sandbox: copying the %s login failed: %v", kind, err)
	}
}

// hostAuthFile is agent.HostAuthFile behind a seam (tests never read the
// person's real login).
var hostAuthFile = agent.HostAuthFile

// sandboxProject returns the named project when its agents run in a
// container, else nil. Always by NAME: a session ID does not identify its
// project unambiguously (see launchLine).
func (n *Native) sandboxProject(project string) *config.Project {
	if n.Cfg == nil || project == "" {
		return nil
	}
	p := n.Cfg.ProjectByName(project)
	if p == nil || !p.Sandboxed() {
		return nil
	}
	return p
}

// pinSandbox captures and registers the trusted git layout of a worktree just
// created for a sandboxed project (internal/gitguard), returning it for the
// session record. It must run before anything else executes in the
// worktree. nil, nil for a host project.
func (n *Native) pinSandbox(project, dir string) (*gitguard.Pin, error) {
	if n.sandboxProject(project) == nil {
		return nil, nil
	}
	pin, err := gitguard.Capture(dir)
	if err != nil {
		return nil, err
	}
	gitguard.Set(dir, pin)
	return &pin, nil
}

// pinnedOverlays are the read-only mounts that keep a pinned worktree's
// admin-dir config out of the agent's reach: host git reads
// <admin>/config.worktree whenever the repository enables worktree config, and
// the admin dir lies inside the read-write git store. The file is created
// empty when missing (git ignores it unless the extension is on), because a
// bind mount needs a source.
func pinnedOverlays(worktrees []string) []string {
	var out []string
	for _, wt := range worktrees {
		at, pin, ok := gitguard.Lookup(wt)
		if !ok || at != filepath.Clean(wt) || !pin.Valid() {
			continue
		}
		cw := filepath.Join(pin.GitDir, "config.worktree")
		if f, err := os.OpenFile(cw, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			f.Close()
			out = append(out, cw)
		}
	}
	return out
}

// SandboxFor is the sandbox kind a NEW session of project would record ("" =
// runs on the host).
func (n *Native) SandboxFor(project string) string {
	if p := n.Cfg.ProjectByName(project); p != nil && p.Sandboxed() {
		return config.SandboxDocker
	}
	return ""
}

// syncSandboxRecord makes a relaunched session's record say what launchLine just
// did. launchLine reads the CURRENT project config, and the production rule for ssh
// trusts this field: a record still saying "docker" over an agent now on the host
// would let that agent past a production host. A container left behind by the
// previous launch goes too, since the host path has no `docker rm -f` of its own.
func (n *Native) syncSandboxRecord(ctx context.Context, s *session.Session) {
	now := n.SandboxFor(s.Project)
	if s.Sandbox != "" && now == "" {
		if err := n.removeContainer(ctx, s.ID); err != nil && n.Logf != nil {
			n.Logf("session %s: removing its old sandbox container failed: %v", s.ID, err)
		}
	}
	s.Sandbox = now
}

// sandboxEnv is the env-file block a sandboxed agent additionally sources
// INSIDE the container: where the hook bridge is, this session's token, the
// agent's private config home, the host's commit identity, and the effective
// pass_env credentials (each from the daemon's environment, else from the
// SandboxCredential fallback). Like
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
	case agent.OpenCode:
		fmt.Fprintf(&b, "XDG_DATA_HOME=%s\n", shQuote(filepath.Join(dir, sandboxOpenCodeDir)))
	}
	n.seedAgentLogin(dir, kind)
	// The container is the sandbox: claude accepts skip-permissions there
	// even when a custom image runs it as root.
	b.WriteString("IS_SANDBOX=1\n")
	if n.GitIdentity != nil {
		name, email := n.GitIdentity(p.Path)
		if name != "" && email != "" {
			for _, who := range []string{"AUTHOR", "COMMITTER"} {
				fmt.Fprintf(&b, "GIT_%s_NAME=%s\nGIT_%s_EMAIL=%s\n", who, shQuote(name), who, shQuote(email))
			}
		}
	}
	b.WriteString(n.sandboxCredentials())
	return b.String()
}

// sandboxCredentials renders the effective pass_env credentials as env-file
// assignments, each from the daemon's environment, else from the
// SandboxCredential fallback. Values only ever land in the 0600 env file.
func (n *Native) sandboxCredentials() string {
	var b strings.Builder
	for _, name := range n.Cfg.Sandbox.EffectivePassEnv() {
		if !envNameRe.MatchString(name) { // defense-in-depth, mirrors envFile
			continue
		}
		if v := n.sandboxCredential(name); v != "" {
			fmt.Fprintf(&b, "%s=%s\n", name, shQuote(v))
		}
	}
	return b.String()
}

// sandboxCredential is one pass_env value as sandboxCredentials resolves it.
func (n *Native) sandboxCredential(name string) string {
	v, ok := os.LookupEnv(name)
	if (!ok || v == "") && n.SandboxCredential != nil {
		v, _ = n.SandboxCredential(name)
	}
	return v
}

// claudeLoginEnv are the variables that log a sandboxed claude in: the
// subscription token, and the API key when [sandbox].use_api_key passes it.
func (n *Native) claudeLoginEnv() []string {
	out := []string{"CLAUDE_CODE_OAUTH_TOKEN"}
	if n.Cfg.Sandbox.UseAPIKey {
		out = append(out, config.SandboxAPIKeyEnv)
	}
	return out
}

// SandboxLoginAvailable reports whether a sandboxed agent of kind would get a
// login if it started now: claude's token or opted-in API key; codex's or
// opencode's host login (copied in at launch) or an API key passed through
// pass_env.
func (n *Native) SandboxLoginAvailable(kind agent.Kind) bool {
	passed := n.Cfg.Sandbox.EffectivePassEnv()
	for _, name := range n.loginEnv(kind) {
		if slices.Contains(passed, name) && n.sandboxCredential(name) != "" {
			return true
		}
	}
	if kind == agent.Claude {
		return false
	}
	if p := hostAuthFile(kind); p != "" {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
			return true
		}
	}
	return false
}

// loginEnv are the variables that log a sandboxed agent of kind in.
func (n *Native) loginEnv(kind agent.Kind) []string {
	switch kind {
	case agent.Claude:
		return n.claudeLoginEnv()
	case agent.Codex:
		return []string{"OPENAI_API_KEY", "CODEX_API_KEY"}
	case agent.OpenCode:
		var out []string
		for _, name := range n.Cfg.Sandbox.EffectivePassEnv() {
			if strings.HasSuffix(name, "_API_KEY") {
				out = append(out, name)
			}
		}
		return out
	}
	return nil
}

// SandboxLoginMissing reports whether s is a sandboxed agent session that was
// started without a login: its env file carries no login variable and, for
// codex/opencode, no login was copied into its private config. Read from the
// files lola wrote, never from the pane, so nothing the agent prints can
// trigger a relaunch.
func (n *Native) SandboxLoginMissing(s session.Session) bool {
	if s.Sandbox != config.SandboxDocker || s.Kind == session.KindLocal || s.GoalManaged() || s.IsAgentless() {
		return false
	}
	kind := agent.Parse(s.Agent)
	dir := n.sessionDir(s)
	if p := sandboxAuthPath(dir, kind); p != "" {
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			return false
		}
	}
	body, err := os.ReadFile(filepath.Join(dir, lolaDir, "env"))
	if err != nil {
		return false // unknown: never relaunch on a guess
	}
	for _, line := range strings.Split(string(body), "\n") {
		for _, name := range n.loginEnv(kind) {
			if strings.HasPrefix(line, name+"=") && line != name+"=''" {
				return false
			}
		}
	}
	return true
}

// sessionDir is the session's worktree (a stack session's folder).
func (n *Native) sessionDir(s session.Session) string {
	if s.Worktree != "" {
		return s.Worktree
	}
	return filepath.Join(n.WT.Root, s.Project, s.ID)
}

// refreshSandboxCredentials appends the current credentials to a sandboxed
// session's env file, so a relaunch picks up a login made after the spawn.
// Appended, not rewritten: project env values may hold quoted newlines that
// look like assignments, and the later assignment wins when sourced.
func (n *Native) refreshSandboxCredentials(dir string, kind agent.Kind) error {
	n.seedAgentLogin(dir, kind)
	block := n.sandboxCredentials()
	if block == "" {
		return nil
	}
	path := filepath.Join(dir, lolaDir, "env")
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.HasSuffix(string(body), block) {
		return nil
	}
	if len(body) > 0 && body[len(body)-1] != '\n' {
		body = append(body, '\n')
	}
	return os.WriteFile(path, append(body, block...), 0o600)
}

// Relogin restarts a sandboxed session's agent with the credentials that
// exist NOW: it refreshes the env file, stops the pane and its container, and
// launches the agent fresh on the same worktree with the standard first
// prompt. Fresh, not resumed: a session that never had a login did no work in
// its conversation, and a resumed one would sit idle without a prompt.
func (n *Native) Relogin(ctx context.Context, s session.Session) (session.Session, error) {
	if s.Sandbox != config.SandboxDocker || n.sandboxProject(s.Project) == nil {
		return s, fmt.Errorf("runtime: relogin %s: not a sandboxed session", s.ID)
	}
	if s.Kind == session.KindLocal || s.GoalManaged() || s.IsAgentless() {
		return s, fmt.Errorf("runtime: relogin %s: not an agent session lola can relaunch", s.ID)
	}
	id := s.TmuxName
	if id == "" {
		id = s.ID
	}
	if n.Tmux.Has(ctx, id) {
		if err := n.Tmux.KillSessionTree(ctx, id); err != nil {
			return s, fmt.Errorf("runtime: relogin %s: stop agent: %w", s.ID, err)
		}
	}
	if err := n.removeContainer(ctx, s.ID); err != nil && n.Logf != nil {
		n.Logf("session %s: removing the sandbox container before relogin failed: %v", s.ID, err)
	}
	return n.revive(ctx, s, true)
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
	if n.sandboxLolaPath() == "" {
		return sandboxHooklessGuard
	}
	dir := filepath.Join(n.WT.Root, p.Name, id)
	docker := shQuote(n.Cfg.Sandbox.DockerBin())
	var b strings.Builder
	fmt.Fprintf(&b, "%s rm -f %s >/dev/null 2>&1; ", docker, shQuote(id))

	// The git stores: the project's repository, or for a stack each app the
	// session checked out (the spawn's label selection — a store the session
	// has no worktree of is not the agent's). Asked of the MAIN checkouts,
	// never of a worktree: the worktree's `.git` pointer is a file the agent
	// can rewrite, and following it would let a tampered pointer pick which
	// repository gets mounted read-write on the next launch.
	repos, required := n.sessionRepos(p, dir)
	for i, r := range repos {
		v := "lola_gcd_" + strconv.Itoa(i)
		fmt.Fprintf(&b, `%s=$(git -C %s rev-parse --path-format=absolute --git-common-dir 2>/dev/null)`, v, shQuote(r.path))
		if i == 0 && required {
			fmt.Fprintf(&b, ` && test -d "$%s" || { echo 'lola: cannot resolve the git directory of this project' >&2; exit 1; }`, v)
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
	for i := range repos {
		v := "$lola_gcd_" + strconv.Itoa(i)
		// ${v:+…} so an unresolvable OPTIONAL store adds nothing rather than an
		// empty -v. The rw store first; the read-only overlays after it, so they
		// win for config + hooks.
		fmt.Fprintf(&b, ` ${lola_gcd_%d:+-v} ${lola_gcd_%d:+"%s:%s"}`, i, i, v, v)
		fmt.Fprintf(&b, ` ${lola_gcd_%d:+-v} ${lola_gcd_%d:+"%s/config:%s/config:ro"}`, i, i, v, v)
		fmt.Fprintf(&b, ` ${lola_gcd_%d:+-v} ${lola_gcd_%d:+"%s/hooks:%s/hooks:ro"}`, i, i, v, v)
	}
	var worktrees []string
	for _, r := range repos {
		worktrees = append(worktrees, r.worktree)
	}
	for _, cw := range pinnedOverlays(worktrees) {
		fmt.Fprintf(&b, " -v %s", shQuote(cw+":"+cw+":ro"))
	}
	// Defense in depth beside the host-side pin (internal/gitguard): each
	// worktree's `.git` pointer is read-only too, so the agent's own git keeps
	// using the real repository. Only existing files — a bind mount of a
	// missing source would create it.
	for _, wt := range worktrees {
		ptr := filepath.Join(wt, ".git")
		if fi, err := os.Lstat(ptr); err == nil && fi.Mode().IsRegular() {
			fmt.Fprintf(&b, " -v %s", shQuote(ptr+":"+ptr+":ro"))
		}
	}
	if transcripts != "" {
		fmt.Fprintf(&b, " -v %s", shQuote(transcripts+":"+transcripts))
	}
	if ctxDir := n.sandboxContextDir(p, dir); ctxDir != "" {
		fmt.Fprintf(&b, " -v %s", shQuote(ctxDir+":"+ctxDir))
	}
	if lb, host := n.sandboxLolaPath(), n.lolaBin(); lb != "" && filepath.IsAbs(host) {
		fmt.Fprintf(&b, " -v %s", shQuote(lb+":"+host+":ro"))
	}
	for _, r := range repos {
		for _, rel := range r.symlinks {
			if !filepath.IsLocal(rel) {
				continue
			}
			target := filepath.Join(r.path, rel)
			if _, err := os.Stat(target); err == nil {
				fmt.Fprintf(&b, " -v %s", shQuote(target+":"+target+":ro"))
			}
		}
	}
	for _, spec := range p.SandboxPorts {
		if h, c, ok := config.SandboxPortPair(spec); ok {
			fmt.Fprintf(&b, " -p %s", shQuote("127.0.0.1:"+strconv.Itoa(h)+":"+strconv.Itoa(c)))
		}
	}
	if vol := n.cacheVolume(p); vol != "" {
		fmt.Fprintf(&b, " -v %s", shQuote(vol+":/home/lola/.cache"))
	}
	image := n.sandboxImageOf(p)
	// --entrypoint: an image's own entrypoint (git for alpine/git, a wrapper
	// script for many toolchain images) would otherwise receive `sh -c …` as
	// ITS arguments. The image contributes a filesystem, not a launcher.
	fmt.Fprintf(&b, " --entrypoint sh %s -c %s", shQuote(image), shQuote(sandboxPrelude(dir, kind, transcripts)+inner))
	return b.String()
}

// sandboxContextDir is the shared context folder to mount for the session at
// dir: the target of its .lola/context link (setupContext), so the notes the
// briefing points at exist inside the container too. The link lives in the
// worktree the AGENT can write, so its target is trusted only when it is a
// folder of this project under ~/.lola/context — an agent that re-pointed the
// link at $HOME must not get $HOME mounted on its next launch.
func (n *Native) sandboxContextDir(p *config.Project, dir string) string {
	if n.Home == "" {
		return ""
	}
	target, err := os.Readlink(filepath.Join(dir, lolaDir, contextLink))
	if err != nil || !filepath.IsAbs(target) {
		return ""
	}
	target = filepath.Clean(target)
	root := filepath.Join(n.Home, "context", p.Name) + string(filepath.Separator)
	rest, ok := strings.CutPrefix(target, root)
	if !ok || rest == "" || strings.Contains(rest, string(filepath.Separator)) || validContextSegment(rest) != nil {
		return ""
	}
	if fi, err := os.Lstat(target); err != nil || !fi.IsDir() {
		return ""
	}
	return target
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
