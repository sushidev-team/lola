package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/doctor"
	"github.com/sushidev-team/lola/internal/gitrepo"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/protocol"
)

// HubProjectDTO is one project as the Team hub settings show it: which repo it
// offers the hub and whether it takes chores. Stacks are left out — a stack
// has no repository of its own; its member projects are listed instead.
type HubProjectDTO struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	// Repo is the configured GitHub "owner/name" ("" = none).
	Repo string `json:"repo"`
	// SuggestedRepo is what the checkout's remote says, offered when Repo is
	// empty or differs. Read from local git only; "" when unknown.
	SuggestedRepo string `json:"suggestedRepo"`
	HubChores     bool   `json:"hubChores"`
	Sandboxed     bool   `json:"sandboxed"`
	// Stack is the stack this project is a member of ("" = standalone).
	Stack string `json:"stack"`
}

// HubProjectPatchDTO changes one project's hub-facing keys. A nil field is
// left as it is.
type HubProjectPatchDTO struct {
	Name      string  `json:"name"`
	Repo      *string `json:"repo"`
	HubChores *bool   `json:"hubChores"`
	Sandboxed *bool   `json:"sandboxed"`
}

// inspectRepo is the seam over gitrepo.Inspect (tests never shell git).
var inspectRepo = func(ctx context.Context, dir string) string {
	return gitrepo.Inspect(ctx, dir).Repo
}

// HubProjects lists the projects that can take team chores, in config order.
func (s *ConfigService) HubProjects() ([]HubProjectDTO, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out := []HubProjectDTO{}
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		if p.IsStack() {
			continue
		}
		d := HubProjectDTO{
			Name:      p.Name,
			Label:     p.DisplayName(),
			Repo:      p.Repo,
			HubChores: p.HubChores,
			Sandboxed: p.Sandboxed(),
			Stack:     p.Stack,
		}
		if p.Path != "" && ctx.Err() == nil {
			if r := inspectRepo(ctx, p.Path); r != "" && r != p.Repo {
				d.SuggestedRepo = r
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// SaveHubProject writes a project's repo, chore opt-in and Docker sandbox
// in one config save. Turning chores on needs an owner/name repo — the same
// rule Validate enforces — and the error says so in the UI's words.
func (s *ConfigService) SaveHubProject(patch HubProjectPatchDTO) error {
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	p := cfg.ProjectByName(patch.Name)
	if p == nil {
		return errors.New("project not found: " + patch.Name)
	}
	if p.IsStack() {
		return errors.New("a stack has no repository of its own; set it on the member projects")
	}
	if patch.Repo != nil {
		repo := strings.TrimSpace(*patch.Repo)
		if repo != "" && hubproto.ValidateRepo(repo) != nil {
			return errors.New("enter the repository as owner/name")
		}
		p.Repo = repo
	}
	if patch.HubChores != nil {
		p.HubChores = *patch.HubChores
	}
	if patch.Sandboxed != nil {
		if *patch.Sandboxed {
			p.Sandbox = config.SandboxDocker
		} else if p.Sandbox == config.SandboxDocker {
			p.Sandbox = ""
		}
	}
	if p.HubChores && hubproto.ValidateRepo(p.Repo) != nil {
		return errors.New("set the GitHub repository first")
	}
	return saveConfig(cfg, path)
}

// --- readiness --------------------------------------------------------------

// HubReadinessRowDTO is one row of the hub readiness list (doctor.HubReadiness):
// a fact in plain words and, when it is not OK, the stable fix id a UI puts a
// button on. It never carries a token or a key's value.
type HubReadinessRowDTO struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	// Fix is a doctor.FixID ("" = no automated fix).
	Fix string `json:"fix"`
}

// hubReadinessProbe is the seam over doctor.HubReadiness (tests never probe
// docker, the keychain or gh).
var hubReadinessProbe = doctor.HubReadiness

// HubReadiness answers "can this Mac take the team's chores right now?" with
// the same rows `lola hub status` prints. It reads config.toml fresh, so a
// change saved a moment ago shows at once. Bounded: docker and gh are probed
// with their own timeouts inside this one.
func (s *ConfigService) HubReadiness() ([]HubReadinessRowDTO, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out := []HubReadinessRowDTO{}
	for _, r := range hubReadinessProbe(ctx, cfg) {
		out = append(out, HubReadinessRowDTO{Name: r.Name, OK: r.OK, Detail: r.Detail, Fix: string(r.Fix)})
	}
	return out, nil
}

// --- policy keys ---------------------------------------------------------------

// PolicyKeyFingerprint is hub.KeyFingerprint: the short fingerprint an admin
// reads to a member over another channel. "" for a malformed key.
func (s *ConfigService) PolicyKeyFingerprint(pub string) string {
	return hub.KeyFingerprint(strings.TrimSpace(pub))
}

// PinPolicyKey pins keyID → pub in [hub].trusted_policy_keys after the person
// ticked "Trust this key" for the key an INVITE carried. The key comes from
// the text the person pasted, never from a hub response, and an existing
// different key under the same id is never replaced (hub.ErrKeyIDTaken).
// Added is false when the same key was already pinned.
func (s *ConfigService) PinPolicyKey(keyID, pub string) (bool, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return false, err
	}
	added, err := hub.PinTrustedKey(path, strings.TrimSpace(keyID), strings.TrimSpace(pub))
	if err != nil || !added {
		return added, err
	}
	// Advisory like saveConfig's: a down daemon reads the file on start.
	_ = call(protocol.Request{Cmd: "reload"}, shortTimeout, nil)
	return true, nil
}

// --- local rules ---------------------------------------------------------------

// HubRulesDTO changes the [hub] local rules AT ONCE, like join, leave and the
// project toggles, so the Team hub tab has one save model. A nil field is
// left as it is; the rules are the same as SettingsDTO's hub fields (and go
// through the same applyHubSettings), so a later overlay Save that carries
// the same values writes nothing new.
type HubRulesDTO struct {
	AcceptKinds     []string           `json:"acceptKinds"`
	MaxClaimsPerDay *int               `json:"maxClaimsPerDay"`
	RequireSandbox  *bool              `json:"requireSandbox"`
	PollSeconds     *int               `json:"pollSeconds"`
	ApplyPolicy     *bool              `json:"applyPolicy"`
	GroupChores     *bool              `json:"groupChores"`
	TrustedKeys     []HubTrustedKeyDTO `json:"trustedKeys"`
}

// SaveHubRules writes the given [hub] rules and reloads the daemon. The
// config's Validate is the gate (ranges, key shapes); its error names the key.
func (s *ConfigService) SaveHubRules(r HubRulesDTO) error {
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	if err := applyHubSettings(&cfg.Hub, SettingsDTO{
		HubAcceptKinds: r.AcceptKinds, HubMaxClaimsPerDay: r.MaxClaimsPerDay, HubRequireSandbox: r.RequireSandbox,
		HubPollSeconds: r.PollSeconds, HubApplyPolicy: r.ApplyPolicy, HubGroupChores: r.GroupChores, HubTrustedKeys: r.TrustedKeys,
	}); err != nil {
		return err
	}
	return saveConfig(cfg, path)
}
