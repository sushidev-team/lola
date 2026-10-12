package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Two per-project ISOLATION/SCOPE features live here, because both change what
// a session's agent can reach on disk:
//
//   - The container SANDBOX ([[project]].sandbox = "docker" + the global
//     [sandbox] table): the agent runs inside a container that sees only its
//     worktree, so a rogue command cannot touch the rest of the machine.
//   - LINKED REPOSITORIES ([[project.linked_repo]]): one issue spans several
//     repositories (frontend + backend), each checked out on the session's
//     branch beside the primary worktree, each with its own PR.
//
// Neither is a [defaults]-inheritable key. A sandbox is a property of the code
// being run (its image must carry that stack's toolchain), and an inherited
// linked repository would check a second repo out into every project that
// forgot to override it — the same reasoning that keeps dev_commands out.

// SandboxNone and SandboxDocker are the values [[project]].sandbox accepts.
// Empty means none: every pre-sandbox config keeps running on the host.
const (
	SandboxNone   = "none"
	SandboxDocker = "docker"
)

// DefaultSandboxHookPort is the loopback TCP port the daemon's HOOK BRIDGE
// listens on when any project is sandboxed (see SandboxConfig.HookPort). Next
// to the [remote] default (7717) on purpose: nothing standard claims it.
const DefaultSandboxHookPort = 7718

// SandboxConfig is the global [sandbox] table: the machine-side half of the
// container sandbox. A project opts in with sandbox = "docker"; this table
// says how to run a container on this machine.
//
//   - Docker is the docker CLI ("" = "docker" from PATH). Any CLI speaking
//     docker's run flags works (OrbStack, Colima, podman's docker shim).
//   - Image is the default image a sandboxed project runs
//     ([[project]].sandbox_image overrides it). It must carry `sh`, `git` and
//     the coding agent's binary on PATH — lola installs nothing into it.
//   - LolaBin is a LINUX build of lola (`make build-linux`). The container
//     cannot run the macOS binary the hooks normally call, so this one is
//     bind-mounted read-only at the host lola's path: the generated hook
//     wiring, codex's notify and `lola report` all work unchanged.
//   - HookPort is the loopback TCP port of the hook bridge (0 = 7718). A
//     host unix socket cannot be bind-mounted into a container on Docker
//     Desktop / OrbStack, so a sandboxed agent's hooks reach the daemon over
//     host.docker.internal:<port>, authenticated per session.
//   - PassEnv names HOST environment variables copied into the sandboxed
//     agent's 0600 env file at spawn — the agent's credentials
//     (ANTHROPIC_API_KEY, CLAUDE_CODE_OAUTH_TOKEN, GH_TOKEN …), since a
//     container cannot read the macOS keychain. Names only: a value never lives
//     in config.toml.
type SandboxConfig struct {
	Docker   string   `toml:"docker,omitempty"`
	Image    string   `toml:"image,omitempty"`
	LolaBin  string   `toml:"lola_bin,omitempty"`
	HookPort int      `toml:"hook_port,omitempty"`
	PassEnv  []string `toml:"pass_env,omitempty"`
}

// isZero reports whether the table carries nothing (omitted on Save).
func (s SandboxConfig) isZero() bool {
	return s.Docker == "" && s.Image == "" && s.LolaBin == "" && s.HookPort == 0 && len(s.PassEnv) == 0
}

// sandboxTable is nonZero for SandboxConfig, which holds a slice and so is
// not comparable: the empty table is omitted from the file.
func sandboxTable(s SandboxConfig) *SandboxConfig {
	if s.isZero() {
		return nil
	}
	return &s
}

// DockerBin is the docker CLI to exec ("docker" when unset).
func (s SandboxConfig) DockerBin() string {
	if s.Docker == "" {
		return "docker"
	}
	return s.Docker
}

// EffectiveHookPort is HookPort with its default applied.
func (s SandboxConfig) EffectiveHookPort() int {
	if s.HookPort == 0 {
		return DefaultSandboxHookPort
	}
	return s.HookPort
}

// LinkedRepo is one [[project.linked_repo]]: an additional repository a
// session of this project works in beside the primary checkout. Each session
// gets a worktree of it on the session's own branch (at
// <worktree>/.lola/repos/<name>), and the PR the agent opens there is tracked
// on the session next to the primary one.
//
//   - Name is the directory segment and the label shown beside its PR.
//   - Path is the repository's local checkout (the worktree is cut from it).
//   - Repo is its GitHub owner/name, which is what lets lola find the PR.
//   - DefaultBranch is the branch the session's branch is cut from and the PR
//     targets ("" = the project's default_branch).
type LinkedRepo struct {
	Name          string `toml:"name"`
	Path          string `toml:"path"`
	Repo          string `toml:"repo,omitempty"`
	DefaultBranch string `toml:"default_branch,omitempty"`
}

// BaseBranch is the branch a linked worktree is cut from: its own
// default_branch, else the owning project's.
func (l LinkedRepo) BaseBranch(p Project) string {
	if l.DefaultBranch != "" {
		return l.DefaultBranch
	}
	return p.DefaultBranch
}

// Sandboxed reports whether the project's agents run in a container.
func (p *Project) Sandboxed() bool { return p.Sandbox == SandboxDocker }

// SandboxImageFor is the image a sandboxed project runs: its own
// sandbox_image, else [sandbox].image. "" means none is configured (Validate
// rejects that for a sandboxed project).
func (c *Config) SandboxImageFor(p *Project) string {
	if p != nil && p.SandboxImage != "" {
		return p.SandboxImage
	}
	return c.Sandbox.Image
}

// AnySandboxed reports whether some project runs its agents in a container —
// the condition for the daemon to open its hook bridge at all.
func (c *Config) AnySandboxed() bool {
	for i := range c.Projects {
		if c.Projects[i].Sandboxed() {
			return true
		}
	}
	return false
}

// sandboxPortRe is one sandbox_ports entry: "<port>" (same port on both
// sides) or "<host>:<container>". Only numbers — the bind address is always
// 127.0.0.1, chosen by lola, so a config cannot publish a dev server on the
// network by accident.
var sandboxPortRe = regexp.MustCompile(`^([0-9]{1,5})(?::([0-9]{1,5}))?$`)

// SandboxPortPair splits a validated sandbox_ports entry into host and
// container port.
func SandboxPortPair(spec string) (host, container int, ok bool) {
	m := sandboxPortRe.FindStringSubmatch(strings.TrimSpace(spec))
	if m == nil {
		return 0, 0, false
	}
	h, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, false
	}
	c := h
	if m[2] != "" {
		if c, err = strconv.Atoi(m[2]); err != nil {
			return 0, 0, false
		}
	}
	if h < 1 || h > 65535 || c < 1 || c > 65535 {
		return 0, 0, false
	}
	return h, c, true
}

// validateSandbox checks the [sandbox] table and every project's sandbox and
// linked-repo keys. Offline and static like the rest of Validate: whether the
// image exists or the docker daemon answers is a runtime question (the health
// gate's), not a config one.
func (c *Config) validateSandbox() []error {
	var errs []error
	s := c.Sandbox
	if s.HookPort < 0 || s.HookPort > 65535 {
		errs = append(errs, fmt.Errorf("sandbox.hook_port must be in 1..65535 (0 = %d), got %d", DefaultSandboxHookPort, s.HookPort))
	}
	if s.LolaBin != "" && !filepath.IsAbs(s.LolaBin) {
		errs = append(errs, fmt.Errorf("sandbox.lola_bin must be an absolute path, got %q", s.LolaBin))
	}
	for _, name := range s.PassEnv {
		if !envNameRe.MatchString(name) {
			errs = append(errs, fmt.Errorf("sandbox.pass_env: %q is not a valid shell identifier", name))
		}
	}
	for i := range c.Projects {
		p := &c.Projects[i]
		id := fmt.Sprintf("project %q", p.Name)
		switch p.Sandbox {
		case "", SandboxNone:
			if p.SandboxImage != "" || len(p.SandboxPorts) > 0 {
				errs = append(errs, fmt.Errorf("%s: sandbox_image/sandbox_ports need sandbox = %q", id, SandboxDocker))
			}
		case SandboxDocker:
			if c.SandboxImageFor(p) == "" {
				errs = append(errs, fmt.Errorf("%s: sandbox = %q needs an image (sandbox_image, or [sandbox].image)", id, SandboxDocker))
			}
			// Without a Linux lola inside the container every hook would fail
			// with "exec format error": the session would run, but lola would be
			// blind to it — no stop, no notification, no review hand-off.
			if s.LolaBin == "" {
				errs = append(errs, fmt.Errorf("%s: sandbox = %q needs [sandbox].lola_bin (a linux lola build, `make build-linux`) so the agent's hooks can reach the daemon", id, SandboxDocker))
			}
		default:
			errs = append(errs, fmt.Errorf("%s: sandbox must be one of none|docker (empty = none), got %q", id, p.Sandbox))
		}
		for _, spec := range p.SandboxPorts {
			if _, _, ok := SandboxPortPair(spec); !ok {
				errs = append(errs, fmt.Errorf("%s: sandbox_ports entry %q must be \"<port>\" or \"<host>:<container>\" (1..65535)", id, spec))
			}
		}
		errs = append(errs, validateLinkedRepos(id, p)...)
	}
	return errs
}

func validateLinkedRepos(id string, p *Project) []error {
	var errs []error
	seen := map[string]bool{}
	for j, l := range p.LinkedRepos {
		lid := fmt.Sprintf("%s: linked_repo[%d]", id, j)
		if err := linkedNameErr(l.Name); err != nil {
			errs = append(errs, fmt.Errorf("%s: name %w", lid, err))
		} else if seen[l.Name] {
			errs = append(errs, fmt.Errorf("%s: duplicate name %q", lid, l.Name))
		}
		seen[l.Name] = true
		switch {
		case l.Path == "":
			errs = append(errs, fmt.Errorf("%s: path is required", lid))
		case !filepath.IsAbs(l.Path):
			errs = append(errs, fmt.Errorf("%s: path must be absolute, got %q", lid, l.Path))
		case p.Path != "" && filepath.Clean(l.Path) == filepath.Clean(p.Path):
			errs = append(errs, fmt.Errorf("%s: path is the project's own checkout", lid))
		}
		if l.Repo != "" && !repoRe.MatchString(l.Repo) {
			errs = append(errs, fmt.Errorf("%s: repo must be owner/name, got %q", lid, l.Repo))
		}
	}
	return errs
}

// linkedNameRe keeps a linked repo's name one safe path segment: it becomes
// <worktree>/.lola/repos/<name>.
var linkedNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func linkedNameErr(name string) error {
	if name == "" {
		return errors.New("is required")
	}
	if !linkedNameRe.MatchString(name) || name == "." || name == ".." {
		return fmt.Errorf("%q must be lowercase [a-z0-9._-], starting with a letter or digit", name)
	}
	return nil
}
