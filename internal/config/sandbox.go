package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
)

// The container SANDBOX ([[project]].sandbox = "docker" + the global [sandbox]
// table): the agent runs inside a container that sees only its worktree, so a
// rogue command cannot touch the rest of the machine. Not a [defaults]-
// inheritable key: a sandbox is a property of the code being run (its image
// must carry that project's toolchain). For a STACK (stack.go) the stack's own
// sandbox keys decide; a member's are ignored.

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
//   - Image is the image a sandboxed project runs ([[project]].sandbox_image
//     overrides it). Empty means lola's published default image
//     (DefaultSandboxImage: claude, git, gh, ripgrep), so a sandbox needs no
//     setup. A custom image must carry `sh`, `git` and the coding agent's
//     binary on PATH; it does NOT need lola, because lola mounts its own.
//   - LolaBin is an optional LINUX build of lola (`make build-linux`). The
//     container cannot run the macOS binary the hooks normally call, so a
//     linux lola is bind-mounted read-only at the host lola's path: the
//     generated hook wiring, codex's notify and `lola report` all work
//     unchanged. Empty means the one inside the default image, which the
//     daemon extracts and caches (internal/runtime/sandboxlola.go).
//   - HookPort is the loopback TCP port of the hook bridge (0 = 7718). A
//     host unix socket cannot be bind-mounted into a container on Docker
//     Desktop / OrbStack, so a sandboxed agent's hooks reach the daemon over
//     host.docker.internal:<port>, authenticated per session.
//   - PassEnv names HOST environment variables copied into the sandboxed
//     agent's 0600 env file at spawn — the agent's credentials, since a
//     container cannot read the macOS keychain. Names only: a value never lives
//     in config.toml. Empty means DefaultSandboxPassEnv; the runtime then also
//     falls back to the keychain (claude) and `gh auth token` (GitHub) for a
//     name the daemon's environment does not carry.
type SandboxConfig struct {
	Docker   string   `toml:"docker,omitempty"`
	Image    string   `toml:"image,omitempty"`
	LolaBin  string   `toml:"lola_bin,omitempty"`
	HookPort int      `toml:"hook_port,omitempty"`
	PassEnv  []string `toml:"pass_env,omitempty"`
	// UseAPIKey passes ANTHROPIC_API_KEY from the daemon's environment into
	// sandboxed agents. Off by default: the key bills per token and claude
	// prefers it over the subscription token (`claude setup-token`).
	UseAPIKey bool `toml:"use_api_key,omitempty"`
}

// isZero reports whether the table carries nothing (omitted on Save).
func (s SandboxConfig) isZero() bool {
	return s.Docker == "" && s.Image == "" && s.LolaBin == "" && s.HookPort == 0 && len(s.PassEnv) == 0 && !s.UseAPIKey
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

// Sandboxed reports whether the project's agents run in a container.
func (p *Project) Sandboxed() bool { return p.Sandbox == SandboxDocker }

// SandboxImageFor is the image a sandboxed project runs: its own
// sandbox_image, else [sandbox].image, else lola's default image. Never "".
func (c *Config) SandboxImageFor(p *Project) string {
	if p != nil && p.SandboxImage != "" {
		return p.SandboxImage
	}
	if c.Sandbox.Image != "" {
		return c.Sandbox.Image
	}
	return DefaultSandboxImage()
}

// DefaultSandboxImageRepo is where lola's default sandbox image is published
// (sandbox/Dockerfile, .github/workflows/sandbox-image.yml).
const DefaultSandboxImageRepo = "ghcr.io/sushidev-team/lola-sandbox"

// DefaultSandboxEdgeTag is the default image's tag for a lola that is not a
// release build: the image CI publishes from main.
const DefaultSandboxEdgeTag = "edge"

// DefaultSandboxPassEnv is what an empty [sandbox].pass_env copies: claude's
// subscription token and GitHub's token. Each only when set.
var DefaultSandboxPassEnv = []string{"CLAUDE_CODE_OAUTH_TOKEN", "GH_TOKEN"}

// SandboxAPIKeyEnv is the Anthropic API key. Claude Code prefers it over a
// subscription token and it bills per token, so it reaches a container only
// through the explicit [sandbox].use_api_key switch, never by default and
// never by being listed in pass_env alone.
const SandboxAPIKeyEnv = "ANTHROPIC_API_KEY"

// lolaVersion is the running lola's version, which picks the default image's
// tag. Set once from main (SetLolaVersion); a package-level value because the
// default is read wherever a Config is, and config cannot import main.
var lolaVersion atomic.Value

// SetLolaVersion records the running lola's build version ("dev" for a
// non-release build). main calls it before anything reads a config.
func SetLolaVersion(v string) { lolaVersion.Store(v) }

// releaseVersionRe is a release build's version as goreleaser stamps it.
var releaseVersionRe = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+)$`)

// SandboxImageTag is the default image tag for lola version v: a release's
// own version (the image is published with every release, so lola and the
// lola inside it match), else DefaultSandboxEdgeTag.
func SandboxImageTag(v string) string {
	if m := releaseVersionRe.FindStringSubmatch(strings.TrimSpace(v)); m != nil {
		return m[1]
	}
	return DefaultSandboxEdgeTag
}

// DefaultSandboxImage is the image a sandboxed agent runs when nothing names
// one: DefaultSandboxImageRepo at the running lola's tag.
func DefaultSandboxImage() string {
	v, _ := lolaVersion.Load().(string)
	return DefaultSandboxImageRepo + ":" + SandboxImageTag(v)
}

// EffectivePassEnv is PassEnv with its default applied and the API key
// gated: ANTHROPIC_API_KEY is in the list exactly when UseAPIKey is set.
func (s SandboxConfig) EffectivePassEnv() []string {
	base := s.PassEnv
	if len(base) == 0 {
		base = DefaultSandboxPassEnv
	}
	out := make([]string, 0, len(base)+1)
	for _, name := range base {
		if name != SandboxAPIKeyEnv {
			out = append(out, name)
		}
	}
	if s.UseAPIKey {
		out = append(out, SandboxAPIKeyEnv)
	}
	return out
}

// APIKeyFiltered reports whether pass_env lists ANTHROPIC_API_KEY while
// use_api_key is off, so the listing has no effect (doctor says so).
func (s SandboxConfig) APIKeyFiltered() bool {
	return !s.UseAPIKey && slices.Contains(s.PassEnv, SandboxAPIKeyEnv)
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

// validateSandbox checks the [sandbox] table and every project's sandbox keys. Offline and static like the rest of Validate: whether the
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
			// Nothing more to require: the image defaults to lola's own and the
			// linux lola the hooks need comes out of it (see SandboxConfig).
		default:
			errs = append(errs, fmt.Errorf("%s: sandbox must be one of none|docker (empty = none), got %q", id, p.Sandbox))
		}
		for _, spec := range p.SandboxPorts {
			if _, _, ok := SandboxPortPair(spec); !ok {
				errs = append(errs, fmt.Errorf("%s: sandbox_ports entry %q must be \"<port>\" or \"<host>:<container>\" (1..65535)", id, spec))
			}
		}
	}
	return errs
}
