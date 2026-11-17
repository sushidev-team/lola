package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshconn"
)

// SSHHostDTO is one [[ssh_host]] as the settings form edits it: the file's values,
// zero meaning "the default", exactly as config.SSHHost holds them. It carries no
// key contents: entries reference an SSH alias or hostname and optional key path.
type SSHHostDTO struct {
	Name                     string `json:"name"`
	Alias                    string `json:"alias"`
	Label                    string `json:"label"`
	User                     string `json:"user"`
	Port                     int    `json:"port"`
	IdentityFile             string `json:"identityFile"`
	Tier                     string `json:"tier"`
	AllowUnsandboxed         bool   `json:"allowUnsandboxed"`
	Sandbox                  string `json:"sandbox"`
	Agent                    string `json:"agent"`
	DefaultCwd               string `json:"defaultCwd"`
	GrantMaxMinutes          int    `json:"grantMaxMinutes"`
	CommandTimeoutSeconds    int    `json:"commandTimeoutSeconds"`
	MaxCommandTimeoutSeconds int    `json:"maxCommandTimeoutSeconds"`
	OutputCapKB              int    `json:"outputCapKb"`
	ControlPersistSeconds    int    `json:"controlPersistSeconds"`
	ConnectWaitSeconds       int    `json:"connectWaitSeconds"`
	ApprovalTimeoutSeconds   int    `json:"approvalTimeoutSeconds"`
}

// SSHHostPreviewDTO is what a host's settings resolve to right now, read from
// config the same way the daemon reads it, so the form never restates the rule.
type SSHHostPreviewDTO struct {
	// ID is the catalog id this form would save under.
	ID string `json:"id"`
	// Tier has the fail-closed default applied.
	Tier string `json:"tier"`
	// DirectSandbox is docker or none, "" when a direct session is refused.
	DirectSandbox string `json:"directSandbox"`
	// DirectLine is lola's sentence on where a direct session's agent runs and why.
	DirectLine string `json:"directLine"`
	// DirectRefused is why a direct session cannot start, "" when it can.
	DirectRefused string `json:"directRefused"`
	// GrantMaxMinutes is the longest grant, 0 for none.
	GrantMaxMinutes int `json:"grantMaxMinutes"`
	// The effective defaults a Limits field shows as its placeholder.
	CommandTimeoutSeconds    int `json:"commandTimeoutSeconds"`
	MaxCommandTimeoutSeconds int `json:"maxCommandTimeoutSeconds"`
	OutputCapKB              int `json:"outputCapKb"`
	ControlPersistSeconds    int `json:"controlPersistSeconds"`
	ConnectWaitSeconds       int `json:"connectWaitSeconds"`
	ApprovalTimeoutSeconds   int `json:"approvalTimeoutSeconds"`
}

func sshHostDTO(h config.SSHHost) SSHHostDTO {
	return SSHHostDTO{
		Name:  h.Name,
		Alias: h.Alias,
		Label: h.Label,
		User:  h.User,
		Port:  h.Port, IdentityFile: h.IdentityFile,
		Tier:                     h.Tier,
		AllowUnsandboxed:         h.AllowUnsandboxed,
		Sandbox:                  h.Sandbox,
		Agent:                    h.Agent,
		DefaultCwd:               h.DefaultCwd,
		GrantMaxMinutes:          h.GrantMaxMinutes,
		CommandTimeoutSeconds:    h.CommandTimeoutSeconds,
		MaxCommandTimeoutSeconds: h.MaxCommandTimeoutSeconds,
		OutputCapKB:              h.OutputCapKB,
		ControlPersistSeconds:    h.ControlPersistSeconds,
		ConnectWaitSeconds:       h.ConnectWaitSeconds,
		ApprovalTimeoutSeconds:   h.ApprovalTimeoutSeconds,
	}
}

func sshHostFromDTO(d SSHHostDTO) config.SSHHost {
	// A grant setting has no meaning on production, and Validate rejects a positive
	// one. The field is hidden there, so a value left over from before the tier
	// changed must not make the form unsaveable.
	h := config.SSHHost{
		Name:  strings.TrimSpace(d.Name),
		Alias: strings.TrimSpace(d.Alias),
		Label: strings.TrimSpace(d.Label),
		User:  strings.TrimSpace(d.User),
		Port:  d.Port, IdentityFile: strings.TrimSpace(d.IdentityFile),
		Tier:                     strings.TrimSpace(d.Tier),
		AllowUnsandboxed:         d.AllowUnsandboxed,
		Sandbox:                  strings.TrimSpace(d.Sandbox),
		Agent:                    strings.TrimSpace(d.Agent),
		DefaultCwd:               strings.TrimSpace(d.DefaultCwd),
		GrantMaxMinutes:          d.GrantMaxMinutes,
		CommandTimeoutSeconds:    d.CommandTimeoutSeconds,
		MaxCommandTimeoutSeconds: d.MaxCommandTimeoutSeconds,
		OutputCapKB:              d.OutputCapKB,
		ControlPersistSeconds:    d.ControlPersistSeconds,
		ConnectWaitSeconds:       d.ConnectWaitSeconds,
		ApprovalTimeoutSeconds:   d.ApprovalTimeoutSeconds,
	}
	if h.IsProduction() && h.GrantMaxMinutes > 0 {
		h.GrantMaxMinutes = 0
	}
	return h
}

// SSHHostForms lists the catalog exactly as written, in file order. It reads the
// file rather than the daemon, because settings must work with no daemon running.
func (s *ConfigService) SSHHostForms() ([]SSHHostDTO, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	out := make([]SSHHostDTO, 0, len(cfg.SSHHosts))
	for _, h := range cfg.SSHHosts {
		out = append(out, sshHostDTO(h))
	}
	return out, nil
}

// SSHHostPreview says what a host's settings resolve to, saved or not. It decides
// with the config's own SSHDirectSandbox, so it cannot drift from the daemon.
func (s *ConfigService) SSHHostPreview(d SSHHostDTO) (SSHHostPreviewDTO, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return SSHHostPreviewDTO{}, err
	}
	return sshHostPreview(cfg, sshHostFromDTO(d)), nil
}

func sshHostPreview(cfg *config.Config, h config.SSHHost) SSHHostPreviewDTO {
	p := SSHHostPreviewDTO{
		ID:                       h.ID(),
		Tier:                     h.EffectiveTier(),
		GrantMaxMinutes:          h.EffectiveGrantMaxMinutes(),
		CommandTimeoutSeconds:    h.CommandTimeout(0),
		MaxCommandTimeoutSeconds: h.EffectiveMaxCommandTimeout(),
		OutputCapKB:              int(h.OutputCapBytes() / 1024),
		ControlPersistSeconds:    h.EffectiveControlPersist(),
		ConnectWaitSeconds:       h.EffectiveConnectWait(),
		ApprovalTimeoutSeconds:   h.EffectiveApprovalTimeout(),
	}
	sandbox, err := cfg.SSHDirectSandbox(h)
	if err != nil {
		p.DirectRefused = err.Error()
		p.DirectLine = "A direct session cannot start on this host as configured."
		return p
	}
	p.DirectSandbox = sandbox
	switch {
	case sandbox == config.SandboxDocker && h.Sandbox == config.SandboxDocker:
		p.DirectLine = "In a container, because this host asks for one."
	case sandbox == config.SandboxDocker:
		p.DirectLine = "In a container, the default on production."
	case h.Sandbox == config.SandboxNone:
		p.DirectLine = "On this Mac, because this host asks for it."
	default:
		p.DirectLine = "On this Mac, the default below production."
	}
	return p
}

// SaveSSHHost writes one host. original names the entry being edited, "" for a new
// one. Static validation runs in saveConfig, so a bad value comes back as the
// config's own sentence and nothing is written.
func (s *ConfigService) SaveSSHHost(original string, d SSHHostDTO) error {
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	h := sshHostFromDTO(d)
	if h.Alias == "" {
		return errors.New("choose an SSH alias or enter a hostname")
	}
	// Name defaults to the alias. Writing it out would freeze the id, so an alias
	// edit would stop carrying it along.
	if h.Name == h.Alias {
		h.Name = ""
	}
	if other := cfg.SSHHostByName(h.ID()); other != nil && other.ID() != original {
		return fmt.Errorf("a host named %q already exists", h.ID())
	}
	if original != "" {
		for i := range cfg.SSHHosts {
			if cfg.SSHHosts[i].ID() == original {
				cfg.SSHHosts[i] = h
				return saveConfig(cfg, path)
			}
		}
		return fmt.Errorf("host %q is no longer in config", original)
	}
	cfg.SSHHosts = append(cfg.SSHHosts, h)
	return saveConfig(cfg, path)
}

// RemoveSSHHost removes a host from the catalog. The daemon closes its live
// connections on the reload this triggers. An unknown name is not an error: the
// outcome the human asked for already holds.
func (s *ConfigService) RemoveSSHHost(name string) error {
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	out := make([]config.SSHHost, 0, len(cfg.SSHHosts))
	for _, h := range cfg.SSHHosts {
		if h.ID() != name {
			out = append(out, h)
		}
	}
	cfg.SSHHosts = out
	return saveConfig(cfg, path)
}

// SSHConfigAliases reads the local SSH catalog without a running daemon.
func (s *ConfigService) SSHConfigAliases() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return sshconn.ConfigAliases(home)
}

// ResolveSSHHost previews the same overrides as the connection, without a daemon.
func (s *ConfigService) ResolveSSHHost(d SSHHostDTO) (protocol.SSHResolveData, error) {
	h := sshHostFromDTO(d)
	r, err := (sshconn.Client{}).ResolveTarget(context.Background(), sshconn.Target{Alias: h.Alias, User: h.User, Port: h.Port, IdentityFile: h.IdentityFile})
	if err != nil {
		return protocol.SSHResolveData{}, err
	}
	return protocol.SSHResolveData{User: r.User, Hostname: r.Hostname, Port: r.Port, ProxyJump: r.ProxyJump}, nil
}
