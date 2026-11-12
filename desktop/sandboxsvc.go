package main

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/doctor"
	"github.com/sushidev-team/lola/internal/secrets"
)

// The container sandbox's machine-side settings ([sandbox]): which image a
// sandboxed project runs by default and the Claude Code token its agents log in
// with. Like the Linear key, these are their own reads and writes rather than
// SettingsDTO fields: the token is a secret (write-only, never read back) and
// the status line is a probe, not config.

// Seams over the keychain and the docker/gh probe, so tests never touch them.
var (
	storeSandboxToken  = secrets.Store
	deleteSandboxToken = secrets.Delete
	lookupSandboxToken = secrets.Lookup
	checkSandbox       = doctor.CheckSandbox
)

// SandboxStatusDTO is the sandbox's configuration and readiness. It never
// carries a credential: TokenStored and the *Source fields say only whether
// and where one exists.
type SandboxStatusDTO struct {
	Image        string   `json:"image"`        // [sandbox].image ("" = the default)
	DefaultImage string   `json:"defaultImage"` // lola's image for this build
	Docker       string   `json:"docker"`
	DockerOK     bool     `json:"dockerOk"`
	Missing      []string `json:"missing"` // images not pulled yet
	TokenStored  bool     `json:"tokenStored"`
	ClaudeSource string   `json:"claudeSource"`
	GitHubSource string   `json:"githubSource"`
	UseAPIKey    bool     `json:"useApiKey"` // [sandbox].use_api_key
	APIKeySet    bool     `json:"apiKeySet"` // ANTHROPIC_API_KEY exists (presence only)
}

// SandboxImageDefault is the image a project with no sandbox_image runs:
// [sandbox].image, else lola's default. For the project form's placeholder.
func (s *ConfigService) SandboxImageDefault() string {
	cfg, _, err := loadConfig()
	if err != nil {
		return config.DefaultSandboxImage()
	}
	return cfg.SandboxImageFor(nil)
}

// SandboxStatus reads [sandbox] and probes docker, the keychain and gh. It
// pulls nothing.
func (s *ConfigService) SandboxStatus() (SandboxStatusDTO, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return SandboxStatusDTO{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st := checkSandbox(ctx, cfg)
	_, stored, _ := lookupSandboxToken(secrets.SandboxClaudeService)
	return SandboxStatusDTO{
		Image: cfg.Sandbox.Image, DefaultImage: st.DefaultImage,
		Docker: st.Docker, DockerOK: st.DockerOK, Missing: st.Missing,
		TokenStored: stored, ClaudeSource: st.ClaudeSource, GitHubSource: st.GitHubSource,
		UseAPIKey: st.UseAPIKey, APIKeySet: st.APIKeySet,
	}, nil
}

// SandboxLoginNeeded reports whether a session of agentKind ("" = the
// project's configured agent) started in project would run a sandboxed claude
// with no login: it would print "Not logged in" and wait forever. The app asks
// before such a start and offers the login. Only claude's login is lola's to
// provide; other agents answer false. The daemon refuses the same start, so
// this is the friendly half, not the guard.
func (s *ConfigService) SandboxLoginNeeded(project, agentKind string) (bool, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return false, err
	}
	p := cfg.ProjectByName(project)
	if p == nil || !p.Sandboxed() {
		return false, nil
	}
	kind := agent.Parse(cfg.AgentForProject(project))
	if agent.Valid(strings.TrimSpace(agentKind)) {
		kind = agent.Parse(strings.TrimSpace(agentKind))
	}
	if kind != agent.Claude {
		return false, nil
	}
	if slices.Contains(cfg.Sandbox.EffectivePassEnv(), "CLAUDE_CODE_OAUTH_TOKEN") {
		if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "" {
			return false, nil
		}
		if _, stored, _ := lookupSandboxToken(secrets.SandboxClaudeService); stored {
			return false, nil
		}
	}
	if cfg.Sandbox.UseAPIKey && os.Getenv(config.SandboxAPIKeyEnv) != "" {
		return false, nil
	}
	return true, nil
}

// SetSandboxImage writes [sandbox].image. "" restores lola's default.
func (s *ConfigService) SetSandboxImage(image string) error {
	image = strings.TrimSpace(image)
	if strings.IndexFunc(image, unicode.IsSpace) >= 0 || strings.IndexFunc(image, unicode.IsControl) >= 0 {
		return errors.New("an image reference has no spaces")
	}
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	cfg.Sandbox.Image = image
	return saveConfig(cfg, path)
}

// SetSandboxUseAPIKey writes [sandbox].use_api_key: whether sandboxed agents
// get ANTHROPIC_API_KEY from the daemon's environment. A deliberate switch —
// the key bills per token and claude prefers it over the subscription token —
// so the UI confirms before turning it on.
func (s *ConfigService) SetSandboxUseAPIKey(on bool) error {
	cfg, path, err := loadConfig()
	if err != nil {
		return err
	}
	cfg.Sandbox.UseAPIKey = on
	return saveConfig(cfg, path)
}

// SetSandboxToken stores the Claude Code token sandboxed agents log in with
// (from `claude setup-token`) in the keychain. Write-only: nothing reads it
// back to the frontend, and the error never carries it.
func (s *ConfigService) SetSandboxToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("empty token")
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return errors.New("the token must be a single line with no spaces")
	}
	return storeSandboxToken(secrets.SandboxClaudeService, token)
}

// ClearSandboxToken deletes the stored token.
func (s *ConfigService) ClearSandboxToken() error {
	return deleteSandboxToken(secrets.SandboxClaudeService)
}
