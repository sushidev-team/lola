package config

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/sushidev-team/lola/internal/agent"
)

// The [[ssh_host]] table: the only hosts an agent-assisted ssh session can reach.
// Not [ssh] or [remote]: [remote] already means phone pairing, and a host is an
// entry in a catalog, not a machine-wide setting. An absent table means the
// feature is off, with zero behaviour change.
//
// An entry names an SSH alias or hostname, with optional port and key-file path
// overrides. Key contents stay with OpenSSH. Connection details come from HERE and never from
// agent text: an agent names a host by its catalog name and nothing it writes can
// reach the ssh argv except as the one quoted remote command.
//
// Validation is offline and static, like the rest of Validate. Whether the alias
// resolves is ssh's question at connect time. See docs/ssh-sessions.md.

// SSH tiers. The tier decides how much friction a host gets; it is the operator's
// statement about the host, not something lola can verify.
const (
	SSHTierProduction = "production"
	SSHTierStaging    = "staging"
	SSHTierDev        = "dev"
)

// SSHTiers lists the vocabulary, most to least restricted.
var SSHTiers = []string{SSHTierProduction, SSHTierStaging, SSHTierDev}

// Defaults and bounds for the per-host knobs. Zero in the file means the default.
const (
	DefaultSSHCommandTimeoutSeconds    = 120
	DefaultSSHMaxCommandTimeoutSeconds = 900
	MaxSSHCommandTimeoutSeconds        = 6 * 3600
	DefaultSSHOutputCapKB              = 1024
	MaxSSHOutputCapKB                  = 16 * 1024
	// DefaultSSHControlPersistSeconds is how long an idle master lingers once its
	// last client has gone. A time rather than "yes" on purpose: ssh_config(5) says
	// a master set to yes stays until killed, and one whose `ssh -O exit` could not
	// run (a crash, a lost socket) would then hold an authenticated connection open
	// forever. A time bounds the worst case.
	DefaultSSHControlPersistSeconds = 900
	MaxSSHControlPersistSeconds     = 24 * 3600
	// DefaultSSHConnectWaitSeconds bounds how long a tab may sit on a passphrase or
	// an MFA prompt before the connection reads as failed.
	DefaultSSHConnectWaitSeconds = 300
	MaxSSHConnectWaitSeconds     = 3600
	// DefaultSSHApprovalTimeoutSeconds is how long a command waits for a human
	// before it expires, unrun.
	DefaultSSHApprovalTimeoutSeconds = 1800
	MaxSSHApprovalTimeoutSeconds     = 24 * 3600
	// Tier defaults for the longest grant a human may open. Production has none and
	// cannot be given one.
	DefaultSSHGrantMinutesStaging = 30
	DefaultSSHGrantMinutesDev     = 120
	MaxSSHGrantMinutes            = 24 * 60
)

// SSHHost is one [[ssh_host]] entry. It is written and read as-is, with no
// pointer mirror: zero means "the default" for every knob.
type SSHHost struct {
	// Name is the id agents, the CLI and the UIs use. Empty means Alias.
	Name string `toml:"name,omitempty"`
	// Alias is what ssh is told to connect to, resolved by ~/.ssh/config. Required.
	Alias string `toml:"alias"`
	// Label is free display text.
	Label string `toml:"label,omitempty"`
	// User is an optional RESTRICTED remote user. This is the real limit on what a
	// command can do: lola does not filter commands. Empty leaves it to ssh config.
	User         string `toml:"user,omitempty"`
	Port         int    `toml:"port,omitempty"`
	IdentityFile string `toml:"identity_file,omitempty"`
	// Tier is production | staging | dev. Empty means production: a host nobody
	// classified gets the strictest handling.
	Tier string `toml:"tier,omitempty"`
	// AllowUnsandboxed lets an unsandboxed agent work on a production host. The
	// default refuses, because an unsandboxed agent runs as the user and could read
	// keys or reach the socket directly. Meaningless below production, where
	// unsandboxed agents are already allowed.
	AllowUnsandboxed bool `toml:"allow_unsandboxed,omitempty"`
	// Sandbox decides how the agent of a DIRECT session on this host runs: docker,
	// none, or empty for the tier's default (docker on production when [sandbox] is
	// configured, otherwise on the host). Agents of ordinary sessions keep whatever
	// their project set; the production rule applies to those through SSHAdmit.
	Sandbox string `toml:"sandbox,omitempty"`
	// Agent is the coding agent of a direct session. Empty means [defaults].agent.
	Agent string `toml:"agent,omitempty"`
	// DefaultCwd is the remote directory a command runs in when it names none.
	DefaultCwd string `toml:"default_cwd,omitempty"`
	// GrantMaxMinutes is the longest time-limited grant a human may open: 0 means
	// the tier's default, -1 means no grants. Must not be positive on production.
	GrantMaxMinutes int `toml:"grant_max_minutes,omitempty"`
	// CommandTimeoutSeconds is a command's timeout when it names none.
	CommandTimeoutSeconds int `toml:"command_timeout_seconds,omitempty"`
	// MaxCommandTimeoutSeconds is the most a command may ask for.
	MaxCommandTimeoutSeconds int `toml:"max_command_timeout_seconds,omitempty"`
	// OutputCapKB bounds the output stored per stream of one command.
	OutputCapKB int `toml:"output_cap_kb,omitempty"`
	// TerminalCapture starts the human's terminal with lola's shell hooks, which is what
	// makes its commands timeline blocks and lets the composer share that shell. Nil
	// means on. Off gives a plain login shell, for a host where a remote command at login
	// is unwelcome or the login shell is csh.
	TerminalCapture *bool `toml:"terminal_capture,omitempty"`
	// ControlPersistSeconds is how long an idle master lingers.
	ControlPersistSeconds int `toml:"control_persist_seconds,omitempty"`
	// ConnectWaitSeconds bounds the wait for a master to come up.
	ConnectWaitSeconds int `toml:"connect_wait_seconds,omitempty"`
	// ApprovalTimeoutSeconds bounds how long a command waits for a human.
	ApprovalTimeoutSeconds int `toml:"approval_timeout_seconds,omitempty"`
}

// ID is the host's catalog id.
func (h SSHHost) ID() string {
	if h.Name != "" {
		return h.Name
	}
	return h.Alias
}

// DisplayName is what a UI shows.
func (h SSHHost) DisplayName() string {
	if h.Label != "" {
		return h.Label
	}
	return h.ID()
}

// EffectiveTier is the tier with the fail-closed default applied.
func (h SSHHost) EffectiveTier() string {
	switch h.Tier {
	case SSHTierStaging, SSHTierDev:
		return h.Tier
	}
	return SSHTierProduction
}

// IsProduction reports whether the host gets production handling.
func (h SSHHost) IsProduction() bool { return h.EffectiveTier() == SSHTierProduction }

// EffectiveGrantMaxMinutes is the longest grant a human may open, 0 for none.
// Production is always 0, whatever the file says.
func (h SSHHost) EffectiveGrantMaxMinutes() int {
	if h.IsProduction() || h.GrantMaxMinutes < 0 {
		return 0
	}
	if h.GrantMaxMinutes > 0 {
		return h.GrantMaxMinutes
	}
	if h.EffectiveTier() == SSHTierStaging {
		return DefaultSSHGrantMinutesStaging
	}
	return DefaultSSHGrantMinutesDev
}

func orDefault(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

// EffectiveMaxCommandTimeout is the most a command may ask for, in seconds.
func (h SSHHost) EffectiveMaxCommandTimeout() int {
	return orDefault(h.MaxCommandTimeoutSeconds, DefaultSSHMaxCommandTimeoutSeconds)
}

// CommandTimeout resolves a command's timeout in seconds: what it asked for, else
// the host's default, never past the maximum.
func (h SSHHost) CommandTimeout(requested int) int {
	t := requested
	if t <= 0 {
		t = orDefault(h.CommandTimeoutSeconds, DefaultSSHCommandTimeoutSeconds)
	}
	return min(t, h.EffectiveMaxCommandTimeout())
}

// OutputCapBytes bounds the output stored per stream of one command.
func (h SSHHost) OutputCapBytes() int64 {
	return int64(orDefault(h.OutputCapKB, DefaultSSHOutputCapKB)) * 1024
}

// CapturesTerminal reports whether the terminal starts with lola's shell hooks.
func (h SSHHost) CapturesTerminal() bool { return h.TerminalCapture == nil || *h.TerminalCapture }

// EffectiveControlPersist is the ControlPersist value in seconds.
func (h SSHHost) EffectiveControlPersist() int {
	return orDefault(h.ControlPersistSeconds, DefaultSSHControlPersistSeconds)
}

// EffectiveConnectWait bounds the wait for a master, in seconds.
func (h SSHHost) EffectiveConnectWait() int {
	return orDefault(h.ConnectWaitSeconds, DefaultSSHConnectWaitSeconds)
}

// EffectiveApprovalTimeout bounds the wait for a human, in seconds.
func (h SSHHost) EffectiveApprovalTimeout() int {
	return orDefault(h.ApprovalTimeoutSeconds, DefaultSSHApprovalTimeoutSeconds)
}

// SSHHostByName finds a host by catalog id, nil when there is none.
func (c *Config) SSHHostByName(name string) *SSHHost {
	if name == "" {
		return nil
	}
	for i := range c.SSHHosts {
		if c.SSHHosts[i].ID() == name {
			return &c.SSHHosts[i]
		}
	}
	return nil
}

// ErrSSHUnsandboxedProduction is why an unsandboxed agent is refused on a
// production host. Wrapped with the host's name and the two ways out.
var ErrSSHUnsandboxedProduction = errors.New("production hosts refuse unsandboxed agents")

// SSHAdmit decides whether an agent may use h at all. sandboxed is how the
// session was RECORDED at spawn (Session.Sandbox), never what the agent says. This
// one function is both entry points' production rule.
func (h SSHHost) SSHAdmit(sandboxed bool) error {
	if sandboxed || !h.IsProduction() || h.AllowUnsandboxed {
		return nil
	}
	return fmt.Errorf("%w: %s is tier production and this agent runs unsandboxed. "+
		"Run it from a project with sandbox = \"docker\", or set allow_unsandboxed = true on this [[ssh_host]] to accept the risk",
		ErrSSHUnsandboxedProduction, h.ID())
}

// SSHDirectSandbox decides how the agent of a direct session on h runs. A direct
// session has no checkout, and it CAN be sandboxed: the runtime mounts only its
// scratch folder (no repository, no $HOME). The container sandbox needs no
// configuration (lola's default image carries the agent and the linux lola), so
// a production host is ALWAYS sandboxed unless the host itself says
// sandbox = "none" and accepts the risk with allow_unsandboxed. Whether docker
// actually runs on this machine is a runtime question: the spawn fails then, it
// never falls back to the host.
//
// It returns SandboxDocker or SandboxNone, or an error for the human to read.
func (c *Config) SSHDirectSandbox(h SSHHost) (string, error) {
	switch h.Sandbox {
	case SandboxDocker:
		return SandboxDocker, nil
	case SandboxNone:
		if err := h.SSHAdmit(false); err != nil {
			return "", err
		}
		return SandboxNone, nil
	}
	if !h.IsProduction() {
		return SandboxNone, nil
	}
	return SandboxDocker, nil
}

// AnySSHSandboxed reports whether a direct ssh session could start in a container,
// which is one more reason for the daemon to open its hook bridge.
func (c *Config) AnySSHSandboxed() bool {
	for _, h := range c.SSHHosts {
		if s, err := c.SSHDirectSandbox(h); err == nil && s == SandboxDocker {
			return true
		}
	}
	return false
}

var (
	// sshNameRe covers both an alias and a catalog id. No leading dash, or ssh would
	// read it as an option; no @, or it would carry a user past the catalog; no
	// whitespace or %, which ssh would split or expand as a token.
	sshDestinationRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	sshNameRe        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	sshUserRe        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)
)

// ValidSSHAlias reports whether s may be handed to ssh as a host. Used for an alias
// a host form has not saved yet, which has no catalog entry to validate.
func ValidSSHAlias(s string) bool {
	return (len(s) <= 253 && sshDestinationRe.MatchString(s)) || net.ParseIP(s) != nil
}

// ValidSSHUser reports whether s may be handed to ssh as a user.
func ValidSSHUser(s string) bool { return sshUserRe.MatchString(s) }

// SSHHostError is the one host's validation result. Validation errors do not stop
// a daemon, so the runtime refuses a host that fails this by name.
func (c *Config) SSHHostError(h SSHHost) error {
	one := Config{SSHHosts: []SSHHost{h}, Sandbox: c.Sandbox}
	return errors.Join(one.validateSSHHosts()...)
}

// validateSSHHosts checks the catalog.
func (c *Config) validateSSHHosts() []error {
	var errs []error
	seen := map[string]bool{}
	bound := func(id, key string, v, maxV int) {
		if v < 0 || v > maxV {
			errs = append(errs, fmt.Errorf("%s: %s must be in 0..%d, got %d", id, key, maxV, v))
		}
	}
	for i, h := range c.SSHHosts {
		id := fmt.Sprintf("ssh_host %q", h.ID())
		if h.ID() == "" {
			id = fmt.Sprintf("ssh_host[%d]", i)
		}
		if h.Alias == "" {
			errs = append(errs, fmt.Errorf("%s: alias is required", id))
		} else if !ValidSSHAlias(h.Alias) {
			errs = append(errs, fmt.Errorf("%s: alias must be letters, digits, dots, dashes and underscores, not starting with a dash (no user@ or spaces)", id))
		}
		bound(id, "port", h.Port, 65535)
		if h.IdentityFile != "" && (!(filepath.IsAbs(h.IdentityFile) || strings.HasPrefix(h.IdentityFile, "~/")) || strings.IndexFunc(h.IdentityFile, unicode.IsControl) >= 0) {
			errs = append(errs, fmt.Errorf("%s: identity_file must be an absolute or ~/ path", id))
		}
		if h.Name != "" && !sshNameRe.MatchString(h.Name) {
			errs = append(errs, fmt.Errorf("%s: name must be letters, digits, dots, dashes and underscores", id))
		}
		if h.ID() != "" {
			if seen[h.ID()] {
				errs = append(errs, fmt.Errorf("%s: duplicate host name", id))
			}
			seen[h.ID()] = true
		}
		if strings.IndexFunc(h.Label, unicode.IsControl) >= 0 {
			errs = append(errs, fmt.Errorf("%s: label has control characters", id))
		}
		if h.User != "" && !sshUserRe.MatchString(h.User) {
			errs = append(errs, fmt.Errorf("%s: user %q is not a valid user name", id, h.User))
		}
		switch h.Tier {
		case "", SSHTierProduction, SSHTierStaging, SSHTierDev:
		default:
			errs = append(errs, fmt.Errorf("%s: tier must be one of %s (empty = production), got %q", id, strings.Join(SSHTiers, "|"), h.Tier))
		}
		switch h.Sandbox {
		case "":
		case SandboxNone:
			if err := h.SSHAdmit(false); err != nil {
				errs = append(errs, fmt.Errorf("%s: sandbox = %q on a production host needs allow_unsandboxed = true", id, SandboxNone))
			}
		case SandboxDocker:
		default:
			errs = append(errs, fmt.Errorf("%s: sandbox must be one of none|docker (empty = the tier's default), got %q", id, h.Sandbox))
		}
		if h.Agent != "" && !agent.Valid(h.Agent) {
			errs = append(errs, fmt.Errorf("%s: agent must be one of claude|codex|opencode, got %q", id, h.Agent))
		}
		if h.DefaultCwd != "" && (!strings.HasPrefix(h.DefaultCwd, "/") || len(h.DefaultCwd) > 1024 || strings.IndexFunc(h.DefaultCwd, unicode.IsControl) >= 0) {
			errs = append(errs, fmt.Errorf("%s: default_cwd must be an absolute path on the remote host", id))
		}
		switch {
		case h.GrantMaxMinutes < -1 || h.GrantMaxMinutes > MaxSSHGrantMinutes:
			errs = append(errs, fmt.Errorf("%s: grant_max_minutes must be in -1..%d (0 = the tier's default, -1 = no grants), got %d", id, MaxSSHGrantMinutes, h.GrantMaxMinutes))
		case h.GrantMaxMinutes > 0 && h.IsProduction():
			errs = append(errs, fmt.Errorf("%s: production hosts approve every command, so grant_max_minutes cannot be set. Time-limited grants are for tier staging or dev", id))
		}
		bound(id, "command_timeout_seconds", h.CommandTimeoutSeconds, MaxSSHCommandTimeoutSeconds)
		bound(id, "max_command_timeout_seconds", h.MaxCommandTimeoutSeconds, MaxSSHCommandTimeoutSeconds)
		bound(id, "output_cap_kb", h.OutputCapKB, MaxSSHOutputCapKB)
		bound(id, "control_persist_seconds", h.ControlPersistSeconds, MaxSSHControlPersistSeconds)
		bound(id, "connect_wait_seconds", h.ConnectWaitSeconds, MaxSSHConnectWaitSeconds)
		bound(id, "approval_timeout_seconds", h.ApprovalTimeoutSeconds, MaxSSHApprovalTimeoutSeconds)
		if h.CommandTimeoutSeconds > h.EffectiveMaxCommandTimeout() {
			errs = append(errs, fmt.Errorf("%s: command_timeout_seconds (%d) is above max_command_timeout_seconds (%d)", id, h.CommandTimeoutSeconds, h.EffectiveMaxCommandTimeout()))
		}
	}
	return errs
}
