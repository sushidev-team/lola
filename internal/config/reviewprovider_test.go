package config

// Tests for the NEW global review provider CATALOG ([[review.provider]],
// reviewprovider.go): resolution defaults, explicit-values-kept, Save/Load
// identity (incl. a catalog-only file that must NOT re-emit a legacy [review]
// scalar block), fresh-config-omits, the legacy -> EffectiveReviewProviders
// synthesis (incl. the notify=false opt-out), the mixed-config validation
// error, MigrateLegacyReview identity + guard-key continuity, and the
// validateReviewProviders rejection rules.
//
// The existing 5 [review] + 5 [coderabbit] config tests are the identity oracle
// for the legacy tables and stay green UNMODIFIED.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A minimal config that validates, so a catalog can be attached and round-tripped.
func catalogBase() *Config {
	c := &Config{}
	c.Defaults.GlobalCap = 4
	c.Reactions = defaultReactions()
	c.Notify = defaultNotify()
	return c
}

// An absent catalog leaves Config.ReviewProviders empty and validates cleanly.
func TestReviewProvidersDefaultOffWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[defaults]\nglobal_cap = 4\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.ReviewProviders) != 0 {
		t.Errorf("absent catalog should give no providers, got %+v", c.ReviewProviders)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("config without a catalog must validate: %v", err)
	}
	// With no catalog and no legacy tables, EffectiveReviewProviders is empty.
	if got := c.EffectiveReviewProviders(); len(got) != 0 {
		t.Errorf("EffectiveReviewProviders on a bare config = %+v, want none", got)
	}
}

// A minimally-specified provider resolves the full set of defaults: transports
// -> [lola] (forced), notify/send_to_agent/on_pr_open -> true, timeout -> 300,
// author -> DefaultCodeRabbitAuthor, fallback -> none.
func TestReviewProvidersEnabledDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[defaults]
global_cap = 4

[[review.provider]]
provider = "coderabbit-cli"
enabled = true
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.ReviewProviders) != 1 {
		t.Fatalf("want 1 provider, got %d: %+v", len(c.ReviewProviders), c.ReviewProviders)
	}
	want := ReviewProvider{
		Provider:       provCoderabbitCLI,
		Enabled:        true,
		OnPROpen:       true,
		Command:        "",
		TimeoutSeconds: DefaultReviewTimeoutSeconds,
		Model:          "",
		Author:         DefaultCodeRabbitAuthor,
		Transports:     TransportSet{TransportLola},
		Notify:         true,
		SendToAgent:    true,
		Fallback:       nil,
	}
	if !reflect.DeepEqual(c.ReviewProviders[0], want) {
		t.Errorf("provider = %+v\n         want %+v", c.ReviewProviders[0], want)
	}
	// A catalog-only file must NOT resolve a legacy [review] block.
	if c.Review != (ReviewConfig{}) {
		t.Errorf("catalog-only file resolved a spurious legacy [review]: %+v", c.Review)
	}
	// EffectiveReviewProviders returns the catalog verbatim when non-empty.
	if got := c.EffectiveReviewProviders(); !reflect.DeepEqual(got, c.ReviewProviders) {
		t.Errorf("EffectiveReviewProviders = %+v, want the catalog %+v", got, c.ReviewProviders)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("a valid catalog must validate: %v", err)
	}
}

// Explicit fields survive load: a two-token transport set (lola forced-present
// but already there), a disabling send_to_agent=false, notify=false, a custom
// timeout, and a fallback chain. lola is force-appended when omitted.
func TestReviewProvidersExplicitValuesKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[defaults]
global_cap = 4

[[review.provider]]
provider = "coderabbit-cli"
enabled = true
command = "coderabbit review --plain --type all"
timeout_seconds = 120
transports = ["lola", "github"]
notify = false
send_to_agent = false
fallback = ["claude-session"]

[[review.provider]]
provider = "claude-session"
enabled = true
model = "sonnet"
transports = ["github"]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cli := ReviewProvider{
		Provider:       provCoderabbitCLI,
		Enabled:        true,
		OnPROpen:       true,
		Command:        "coderabbit review --plain --type all",
		TimeoutSeconds: 120,
		Author:         DefaultCodeRabbitAuthor,
		Transports:     TransportSet{TransportLola, TransportGitHub},
		Notify:         false,
		SendToAgent:    false,
		Fallback:       []provKind{provClaudeSession},
	}
	claude := ReviewProvider{
		Provider:       provClaudeSession,
		Enabled:        true,
		OnPROpen:       true,
		Model:          "sonnet",
		TimeoutSeconds: DefaultReviewTimeoutSeconds,
		Author:         DefaultCodeRabbitAuthor,
		Transports:     TransportSet{TransportGitHub, TransportLola}, // lola force-appended
		Notify:         true,
		SendToAgent:    true,
	}
	if !reflect.DeepEqual(c.ReviewProviders, []ReviewProvider{cli, claude}) {
		t.Errorf("providers = %+v\n          want %+v", c.ReviewProviders, []ReviewProvider{cli, claude})
	}
	if err := c.Validate(); err != nil {
		t.Errorf("catalog must validate: %v", err)
	}
}

// A catalog round-trips through Save/Load unchanged, including a disabling
// enabled=false entry and a fallback chain.
func TestReviewProvidersRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	orig := catalogBase()
	orig.ReviewProviders = []ReviewProvider{
		{
			Provider:       provCoderabbitCLI,
			Enabled:        true,
			OnPROpen:       false, // disabling zero must survive
			Command:        "coderabbit review --plain --type all",
			TimeoutSeconds: 240,
			Author:         DefaultCodeRabbitAuthor,
			Transports:     TransportSet{TransportLola, TransportGitHub, TransportLinear},
			Notify:         false,
			SendToAgent:    true,
			Fallback:       []provKind{provClaudeSession},
		},
		{
			Provider:       provClaudeSession,
			Enabled:        true,
			OnPROpen:       true,
			Model:          "opus",
			TimeoutSeconds: DefaultReviewTimeoutSeconds,
			Author:         DefaultCodeRabbitAuthor,
			Transports:     TransportSet{TransportLola},
			Notify:         true,
			SendToAgent:    true,
		},
		{
			Provider:       provCoderabbitWatch,
			Enabled:        true,
			OnPROpen:       true,
			TimeoutSeconds: DefaultReviewTimeoutSeconds,
			Author:         "sonarcloud",
			Transports:     TransportSet{TransportLola, TransportLinear},
			Notify:         true,
			SendToAgent:    false,
		},
	}
	if err := orig.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(orig.ReviewProviders, got.ReviewProviders) {
		t.Errorf("catalog round trip:\n save: %+v\n load: %+v", orig.ReviewProviders, got.ReviewProviders)
	}
}

// A catalog-only config must NOT persist any legacy [review] scalar keys (the
// Design-2 bug): the saved file carries [[review.provider]] tables and no
// enabled/command/timeout_seconds scalar under [review], and reloads to a zero
// legacy ReviewConfig.
func TestReviewProvidersCatalogOnlySaveLoadIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	orig := catalogBase()
	orig.ReviewProviders = []ReviewProvider{{
		Provider:       provCoderabbitCLI,
		Enabled:        true,
		OnPROpen:       true,
		TimeoutSeconds: DefaultReviewTimeoutSeconds,
		Author:         DefaultCodeRabbitAuthor,
		Transports:     TransportSet{TransportLola},
		Notify:         true,
		SendToAgent:    true,
	}}
	if err := orig.Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[[review.provider]]") {
		t.Errorf("catalog-only save should emit [[review.provider]], got:\n%s", data)
	}
	// No spurious legacy scalar block: a bare [review] header carrying scalar
	// keys must not appear. (The [[review.provider]] array tables are fine.)
	for _, k := range []string{"\nenabled =", "\ncommand =", "\ncomment_on_linear ="} {
		// these keys DO appear inside provider tables; assert none appear at the
		// top level of [review] by checking the section before the first provider.
		head := string(data)
		if i := strings.Index(head, "[[review.provider]]"); i >= 0 {
			head = head[:i]
		}
		if strings.Contains(head, k) {
			t.Errorf("catalog-only save leaked a legacy [review] scalar %q:\n%s", k, data)
		}
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Review != (ReviewConfig{}) {
		t.Errorf("catalog-only reload materialized a legacy [review]: %+v", got.Review)
	}
	if !reflect.DeepEqual(orig.ReviewProviders, got.ReviewProviders) {
		t.Errorf("catalog-only round trip:\n save: %+v\n load: %+v", orig.ReviewProviders, got.ReviewProviders)
	}
}

// A fresh &Config{} persists no [[review.provider]] tables and reloads to an
// empty catalog.
func TestReviewProvidersFreshConfigOmits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := (&Config{}).Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "review.provider") {
		t.Errorf("fresh config should omit [[review.provider]], got:\n%s", data)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ReviewProviders) != 0 {
		t.Errorf("reloaded catalog = %+v, want empty", got.ReviewProviders)
	}
}

// A legacy-only config (both [review] and [coderabbit], with a notify=false
// opt-out on the watch) synthesizes the equivalent effective providers. This is
// the zero-regression guarantee, and it must preserve the notify=false opt-out.
func TestLegacyEffectiveReviewProvidersSynthesis(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[defaults]
global_cap = 4

[review]
enabled = true
command = "coderabbit review --plain --type all"
timeout_seconds = 120
send_to_agent = false
comment_on_linear = true

[coderabbit]
enabled = true
author = "sonarcloud"
notify = false
send_to_agent = true
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// No catalog; the effective set is synthesized.
	if len(c.ReviewProviders) != 0 {
		t.Fatalf("legacy config should carry no catalog, got %+v", c.ReviewProviders)
	}
	got := c.EffectiveReviewProviders()
	want := []ReviewProvider{
		{
			Provider:       provCoderabbitCLI,
			Enabled:        true,
			OnPROpen:       true, // follows Enabled in the resolved legacy table
			Command:        "coderabbit review --plain --type all",
			TimeoutSeconds: 120,
			Author:         DefaultCodeRabbitAuthor,
			Transports:     TransportSet{TransportLola, TransportLinear}, // comment_on_linear
			Notify:         true,                                         // cli always notifies
			SendToAgent:    false,                                        // legacy send_to_agent=false preserved
		},
		{
			Provider:    provCoderabbitWatch,
			Enabled:     true,
			Author:      "sonarcloud",
			Transports:  TransportSet{TransportLola}, // comment_on_linear off
			Notify:      false,                       // the notify=false opt-out is preserved
			SendToAgent: true,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("synthesis = %+v\n          want %+v", got, want)
	}
}

// A file carrying BOTH the catalog and a non-zero legacy table is a hard
// validation error pointing at `lola config migrate-review`.
func TestReviewProvidersMixedConfigRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[defaults]
global_cap = 4

[review]
enabled = true

[[review.provider]]
provider = "claude-session"
enabled = true
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	err = c.Validate()
	if err == nil {
		t.Fatal("mixed legacy+catalog config must be rejected")
	}
	if !strings.Contains(err.Error(), "migrate-review") {
		t.Errorf("mixed-config error should point at `lola config migrate-review`, got: %v", err)
	}
}

// MigrateLegacyReview folds the legacy tables into the catalog, clears them, and
// preserves behavior: the pre-migration EffectiveReviewProviders equals the
// post-migration set, the guard keys (coderabbit-cli / coderabbit-watch) carry
// over, the result validates, and it round-trips through Save/Load.
func TestMigrateLegacyReviewIdentity(t *testing.T) {
	c := catalogBase()
	c.Review = ReviewConfig{
		Enabled:         true,
		Command:         "coderabbit review --plain --type all",
		OnPROpen:        true,
		SendToAgent:     false,
		CommentOnLinear: true,
		TimeoutSeconds:  120,
	}
	c.CodeRabbit = CodeRabbitConfig{
		Enabled:     true,
		Author:      "coderabbitai",
		Notify:      false,
		SendToAgent: true,
	}
	before := c.EffectiveReviewProviders()

	MigrateLegacyReview(c)

	if c.Review != (ReviewConfig{}) || c.CodeRabbit != (CodeRabbitConfig{}) {
		t.Errorf("migration must clear the legacy tables, got review=%+v coderabbit=%+v", c.Review, c.CodeRabbit)
	}
	if !reflect.DeepEqual(before, c.ReviewProviders) {
		t.Errorf("migration changed the effective providers:\n before: %+v\n after:  %+v", before, c.ReviewProviders)
	}
	after := c.EffectiveReviewProviders()
	if !reflect.DeepEqual(before, after) {
		t.Errorf("EffectiveReviewProviders not identity across migration:\n before: %+v\n after:  %+v", before, after)
	}
	// Guard-key continuity: the synthesized kinds match the legacy guard keys.
	kinds := []provKind{}
	for _, p := range c.ReviewProviders {
		kinds = append(kinds, p.Provider)
	}
	if !reflect.DeepEqual(kinds, []provKind{provCoderabbitCLI, provCoderabbitWatch}) {
		t.Errorf("migrated kinds = %v, want [coderabbit-cli coderabbit-watch]", kinds)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("migrated config must validate: %v", err)
	}

	// Round-trips.
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.ReviewProviders, got.ReviewProviders) {
		t.Errorf("migrated catalog round trip:\n save: %+v\n load: %+v", c.ReviewProviders, got.ReviewProviders)
	}
}

// MigrateLegacyReview is a no-op when there is nothing to migrate.
func TestMigrateLegacyReviewNoop(t *testing.T) {
	c := catalogBase()
	MigrateLegacyReview(c)
	if len(c.ReviewProviders) != 0 {
		t.Errorf("no-op migration produced a catalog: %+v", c.ReviewProviders)
	}
}

// validateReviewProviders rejects the structural provider errors.
func TestValidateReviewProviders(t *testing.T) {
	cases := []struct {
		name    string
		provs   []ReviewProvider
		wantErr string // "" = must be valid
	}{
		{
			name:    "valid single",
			provs:   []ReviewProvider{{Provider: provCoderabbitCLI, Enabled: true, Transports: TransportSet{TransportLola}}},
			wantErr: "",
		},
		{
			name:    "unknown kind",
			provs:   []ReviewProvider{{Provider: provKind("bogus"), Enabled: true, Transports: TransportSet{TransportLola}}},
			wantErr: "unknown provider kind",
		},
		{
			name: "duplicate kind",
			provs: []ReviewProvider{
				{Provider: provCoderabbitCLI, Enabled: true, Transports: TransportSet{TransportLola}},
				{Provider: provCoderabbitCLI, Enabled: true, Transports: TransportSet{TransportLola}},
			},
			wantErr: "at most one provider per kind",
		},
		{
			name:    "unknown transport",
			provs:   []ReviewProvider{{Provider: provCoderabbitCLI, Enabled: true, Transports: TransportSet{TransportLola, Transport("slack")}}},
			wantErr: "unknown transport",
		},
		{
			name:    "github on watch",
			provs:   []ReviewProvider{{Provider: provCoderabbitWatch, Enabled: true, Transports: TransportSet{TransportLola, TransportGitHub}}},
			wantErr: "github transport is not allowed on a watch",
		},
		{
			name:    "fallback on watch",
			provs:   []ReviewProvider{{Provider: provCoderabbitWatch, Enabled: true, Transports: TransportSet{TransportLola}, Fallback: []provKind{provCoderabbitCLI}}},
			wantErr: "fallback is not allowed on a watch",
		},
		{
			name:    "fallback unknown kind",
			provs:   []ReviewProvider{{Provider: provCoderabbitCLI, Enabled: true, Transports: TransportSet{TransportLola}, Fallback: []provKind{"bogus"}}},
			wantErr: "fallback references unknown kind",
		},
		{
			name:    "fallback same kind",
			provs:   []ReviewProvider{{Provider: provCoderabbitCLI, Enabled: true, Transports: TransportSet{TransportLola}, Fallback: []provKind{provCoderabbitCLI}}},
			wantErr: "fallback cannot reference its own kind",
		},
		{
			name:    "fallback to watch kind",
			provs:   []ReviewProvider{{Provider: provCoderabbitCLI, Enabled: true, Transports: TransportSet{TransportLola}, Fallback: []provKind{provCoderabbitWatch}}},
			wantErr: "fallback cannot reference the watch kind",
		},
		{
			name:    "fallback missing from catalog",
			provs:   []ReviewProvider{{Provider: provCoderabbitCLI, Enabled: true, Transports: TransportSet{TransportLola}, Fallback: []provKind{provClaudeSession}}},
			wantErr: "is not present in the catalog",
		},
		{
			name: "fallback not enabled",
			provs: []ReviewProvider{
				{Provider: provCoderabbitCLI, Enabled: true, Transports: TransportSet{TransportLola}, Fallback: []provKind{provClaudeSession}},
				{Provider: provClaudeSession, Enabled: false, Transports: TransportSet{TransportLola}},
			},
			wantErr: "must be enabled",
		},
		{
			name: "fallback cycle",
			provs: []ReviewProvider{
				{Provider: provCoderabbitCLI, Enabled: true, Transports: TransportSet{TransportLola}, Fallback: []provKind{provClaudeSession}},
				{Provider: provClaudeSession, Enabled: true, Transports: TransportSet{TransportLola}, Fallback: []provKind{provCoderabbitCLI}},
			},
			wantErr: "forms a cycle",
		},
		{
			name:    "negative timeout",
			provs:   []ReviewProvider{{Provider: provCoderabbitCLI, Enabled: true, Transports: TransportSet{TransportLola}, TimeoutSeconds: -1}},
			wantErr: "timeout_seconds must be >= 0",
		},
		{
			name: "valid fallback chain",
			provs: []ReviewProvider{
				{Provider: provCoderabbitCLI, Enabled: true, Transports: TransportSet{TransportLola}, Fallback: []provKind{provClaudeSession}},
				{Provider: provClaudeSession, Enabled: true, Transports: TransportSet{TransportLola}},
			},
			wantErr: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := catalogBase()
			c.ReviewProviders = tc.provs
			err := c.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want valid, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error missing %q:\n%v", tc.wantErr, err)
			}
		})
	}
}
