package config

import (
	"fmt"
	"net"
	"time"
)

// The [linear_agent] table registers lola as a native Linear AGENT: an OAuth
// application installed with `actor=app`, which Linear lets a human assign,
// delegate or @mention like a teammate. Each delegation/mention opens an Agent
// Session on the issue, and lola treats a NEW session as a dispatch trigger
// alongside the label/state filters — no label setup needed — then streams the
// session's progress back into it and relays the human's replies to the agent.
//
// Like [remote], it is a property of the machine, not of a project: there is
// nothing for a project to override, so no [defaults] inheritance. The issue's
// TEAM (and Linear project, when a [[project]] filters by one) picks which
// [[project]] the session spawns into.
//
// SECRETS never live here. The app's client secret, the OAuth access/refresh
// tokens and the webhook signing secret all resolve from the macOS Keychain by
// SERVICE NAME (with an env-var fallback named here), exactly as the Linear API
// key does. `lola linear-agent login` runs the OAuth flow and writes the tokens
// to the keychain; nothing it handles ever reaches argv, a log line or an error.
//
// Triggering is POLLING by default — Linear only pushes to a public HTTPS URL,
// and lola runs on a laptop. `webhook_listen` adds an OPTIONAL receiver for
// operators who run their own tunnel (cloudflared, tailscale funnel): a verified
// webhook is only a DOORBELL that wakes the poll early, never a source of truth,
// so a forged or replayed delivery can at worst cost one extra poll.

const (
	// DefaultLinearAgentPollInterval is how often the agent loop asks Linear for
	// new Agent Sessions and human replies when no webhook rings sooner. Linear
	// expects a first activity within ~10s of a session opening; polling is the
	// laptop fallback and the webhook doorbell is what meets that.
	DefaultLinearAgentPollInterval = 15 * time.Second
	// MinLinearAgentPollInterval keeps a typo'd interval from hammering the API
	// (app tokens share the workspace's rate limit with everything else).
	MinLinearAgentPollInterval = 5 * time.Second
	// DefaultLinearAgentTokenKeychain is the keychain service the OAuth tokens
	// are stored under (one JSON item: access + refresh + expiry).
	DefaultLinearAgentTokenKeychain = "lola-linear-agent"
	// DefaultLinearAgentSecretKeychain holds the OAuth app's client secret.
	DefaultLinearAgentSecretKeychain = "lola-linear-agent-client-secret"
	// DefaultLinearAgentWebhookKeychain holds the webhook signing secret.
	DefaultLinearAgentWebhookKeychain = "lola-linear-agent-webhook-secret"
	// DefaultLinearAgentRedirectPort is the loopback port `lola linear-agent
	// login` listens on for the OAuth callback. The OAuth app in Linear must
	// list http://localhost:<port>/callback as a redirect URI.
	DefaultLinearAgentRedirectPort = 8790
)

// LinearAgentConfig is the resolved [linear_agent] table.
type LinearAgentConfig struct {
	// Enabled gates the whole feature: false (the default, and the value for an
	// absent table) means no agent loop, no webhook listener, no Linear calls.
	Enabled bool
	// ClientID is the OAuth application's client id. Public — it is part of the
	// authorize URL a browser opens — so it is the one credential that may live
	// in config.toml.
	ClientID string
	// ClientSecretKeychain / ClientSecretEnv name where the app's client secret
	// resolves from (keychain first, then the env var), like [linear].api_key_*.
	ClientSecretKeychain string
	ClientSecretEnv      string
	// TokenKeychain names the keychain service holding the OAuth tokens.
	TokenKeychain string
	// RedirectPort is the loopback port of the login callback.
	RedirectPort int
	// PollInterval is the agent loop's cadence (clamped to the minimum).
	PollInterval time.Duration
	// WebhookListen is an OPTIONAL host:port the webhook doorbell binds; ""
	// disables it. It must be an IP literal (no lookups at load time).
	WebhookListen string
	// WebhookSecretKeychain / WebhookSecretEnv name the signing secret. A
	// listener without a resolvable secret refuses every delivery.
	WebhookSecretKeychain string
	WebhookSecretEnv      string
}

// RedirectURI is the OAuth redirect URI `lola linear-agent login` uses.
func (a LinearAgentConfig) RedirectURI() string {
	port := a.RedirectPort
	if port == 0 {
		port = DefaultLinearAgentRedirectPort
	}
	return fmt.Sprintf("http://localhost:%d/callback", port)
}

// --- on-disk mirror (pointer-per-field, the [remote] pattern) --------------

type fileLinearAgentConfig struct {
	Enabled               *bool     `toml:"enabled,omitempty"`
	ClientID              *string   `toml:"client_id,omitempty"`
	ClientSecretKeychain  *string   `toml:"client_secret_keychain,omitempty"`
	ClientSecretEnv       *string   `toml:"client_secret_env,omitempty"`
	TokenKeychain         *string   `toml:"token_keychain,omitempty"`
	RedirectPort          *int      `toml:"redirect_port,omitempty"`
	PollInterval          *Duration `toml:"poll_interval,omitempty"`
	WebhookListen         *string   `toml:"webhook_listen,omitempty"`
	WebhookSecretKeychain *string   `toml:"webhook_secret_keychain,omitempty"`
	WebhookSecretEnv      *string   `toml:"webhook_secret_env,omitempty"`
}

// resolveLinearAgent materializes the table. An absent table is the zero value
// (disabled) and is omitted again on Save; a present one starts from the
// defaults and overlays each explicit key.
func resolveLinearAgent(f *fileLinearAgentConfig) LinearAgentConfig {
	if f == nil {
		return LinearAgentConfig{}
	}
	a := LinearAgentConfig{
		ClientSecretKeychain:  DefaultLinearAgentSecretKeychain,
		TokenKeychain:         DefaultLinearAgentTokenKeychain,
		RedirectPort:          DefaultLinearAgentRedirectPort,
		PollInterval:          DefaultLinearAgentPollInterval,
		WebhookSecretKeychain: DefaultLinearAgentWebhookKeychain,
	}
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	if f.Enabled != nil {
		a.Enabled = *f.Enabled
	}
	set(&a.ClientID, f.ClientID)
	set(&a.ClientSecretKeychain, f.ClientSecretKeychain)
	set(&a.ClientSecretEnv, f.ClientSecretEnv)
	set(&a.TokenKeychain, f.TokenKeychain)
	if f.RedirectPort != nil {
		a.RedirectPort = *f.RedirectPort
	}
	if f.PollInterval != nil {
		a.PollInterval = time.Duration(*f.PollInterval)
	}
	if a.PollInterval == 0 {
		a.PollInterval = DefaultLinearAgentPollInterval
	}
	if a.PollInterval < MinLinearAgentPollInterval {
		a.PollInterval = MinLinearAgentPollInterval
	}
	set(&a.WebhookListen, f.WebhookListen)
	set(&a.WebhookSecretKeychain, f.WebhookSecretKeychain)
	set(&a.WebhookSecretEnv, f.WebhookSecretEnv)
	return a
}

// linearAgentFile builds the on-disk mirror. A zero table is omitted; a
// configured one writes every key so the round trip is exact.
func linearAgentFile(a LinearAgentConfig) *fileLinearAgentConfig {
	if a == (LinearAgentConfig{}) {
		return nil
	}
	pi := Duration(a.PollInterval)
	return &fileLinearAgentConfig{
		Enabled:               &a.Enabled,
		ClientID:              &a.ClientID,
		ClientSecretKeychain:  &a.ClientSecretKeychain,
		ClientSecretEnv:       &a.ClientSecretEnv,
		TokenKeychain:         &a.TokenKeychain,
		RedirectPort:          &a.RedirectPort,
		PollInterval:          &pi,
		WebhookListen:         &a.WebhookListen,
		WebhookSecretKeychain: &a.WebhookSecretKeychain,
		WebhookSecretEnv:      &a.WebhookSecretEnv,
	}
}

// validateLinearAgent applies the static rules. A disabled table is not
// checked: keeping half-filled settings while the feature is off is allowed.
func (c *Config) validateLinearAgent() []error {
	a := c.LinearAgent
	if !a.Enabled {
		return nil
	}
	var errs []error
	if a.ClientID == "" {
		errs = append(errs, fmt.Errorf("linear_agent.client_id is required when linear_agent.enabled = true"))
	}
	if a.TokenKeychain == "" {
		errs = append(errs, fmt.Errorf("linear_agent.token_keychain must not be empty"))
	}
	if a.RedirectPort < 0 || a.RedirectPort > 65535 {
		errs = append(errs, fmt.Errorf("linear_agent.redirect_port must be within 1..65535, got %d", a.RedirectPort))
	}
	if a.WebhookListen != "" {
		host, port, err := net.SplitHostPort(a.WebhookListen)
		if err != nil || port == "" || net.ParseIP(host) == nil {
			errs = append(errs, fmt.Errorf("linear_agent.webhook_listen must be an IP:port (e.g. 127.0.0.1:8789), got %q", a.WebhookListen))
		} else if a.WebhookSecretKeychain == "" && a.WebhookSecretEnv == "" {
			errs = append(errs, fmt.Errorf("linear_agent.webhook_listen needs webhook_secret_keychain or webhook_secret_env — an unsigned webhook is never accepted"))
		}
	}
	return errs
}

// Configured reports whether a human has put anything into the table — the
// keys a settings form edits. A table with none of them carries nothing worth
// writing.
func (a LinearAgentConfig) Configured() bool {
	return a.Enabled || a.ClientID != "" || a.WebhookListen != ""
}

// Normalized is the table a settings form should save: an unconfigured table
// collapses to the zero value (so a save never grows [linear_agent] on a
// config that never used it), and a configured one gets the defaults Load
// would have resolved for every source and number a form leaves blank — a
// table built in memory never went through resolveLinearAgent.
func (a LinearAgentConfig) Normalized() LinearAgentConfig {
	if !a.Configured() {
		return LinearAgentConfig{}
	}
	if a.TokenKeychain == "" {
		a.TokenKeychain = DefaultLinearAgentTokenKeychain
	}
	if a.ClientSecretKeychain == "" && a.ClientSecretEnv == "" {
		a.ClientSecretKeychain = DefaultLinearAgentSecretKeychain
	}
	if a.WebhookSecretKeychain == "" && a.WebhookSecretEnv == "" {
		a.WebhookSecretKeychain = DefaultLinearAgentWebhookKeychain
	}
	if a.RedirectPort == 0 {
		a.RedirectPort = DefaultLinearAgentRedirectPort
	}
	if a.PollInterval == 0 {
		a.PollInterval = DefaultLinearAgentPollInterval
	}
	if a.PollInterval < MinLinearAgentPollInterval {
		a.PollInterval = MinLinearAgentPollInterval
	}
	return a
}
