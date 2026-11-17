package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/hubproto"
)

func policyBase(t *testing.T) (*Config, string, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	src := `[defaults]
poll_interval = "1m"
concurrency_cap = 2
global_cap = 5

[budget]
daily_tokens = 1000

[merge_queue]
enabled = true

[hub]
accept_kinds = ["dep_advisory"]

[[review.provider]]
provider = "claude-session"
enabled = true
fallback = ["coderabbit-cli"]

[[review.provider]]
provider = "coderabbit-cli"
enabled = true

[[review.provider]]
provider = "bot-watch"
enabled = false
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("base config invalid: %v", err)
	}
	return c, path, []byte(src)
}

func appliedMap(keys []AppliedKey) map[string]AppliedKey {
	m := map[string]AppliedKey{}
	for _, k := range keys {
		m[k.Key] = k
	}
	return m
}

func TestApplyTeamPolicyPerKey(t *testing.T) {
	c, _, _ := policyBase(t)
	off := false
	got := ApplyTeamPolicy(c, hubproto.Settings{
		BudgetDailyWeightedTokens: 777,
		MergeQueueEnabled:         &off,
		MaxConcurrency:            3,
		AcceptKinds:               []hubproto.Kind{hubproto.KindRedBumpPR},
		EscalateAfter:             2,
	})
	if c.Budget.DailyTokens != 777 {
		t.Errorf("budget = %d, want 777", c.Budget.DailyTokens)
	}
	if c.MergeQueue.Enabled {
		t.Error("merge_queue_enabled = false must lock the queue off")
	}
	if c.Defaults.GlobalCap != 3 {
		t.Errorf("global_cap = %d, want min(5, 3) = 3", c.Defaults.GlobalCap)
	}
	if !slices.Equal(c.Hub.AcceptKinds, []string{"red_bump_pr"}) {
		t.Errorf("accept_kinds = %v", c.Hub.AcceptKinds)
	}
	m := appliedMap(got)
	want := map[string]AppliedKey{
		hubproto.KeyBudgetDaily:    {hubproto.KeyBudgetDaily, "777", "1000"},
		hubproto.KeyMergeQueue:     {hubproto.KeyMergeQueue, "false", "true"},
		hubproto.KeyMaxConcurrency: {hubproto.KeyMaxConcurrency, "3", "5"},
		hubproto.KeyAcceptKinds:    {hubproto.KeyAcceptKinds, "red_bump_pr", "dep_advisory"},
	}
	for k, w := range want {
		if m[k] != w {
			t.Errorf("applied[%s] = %+v, want %+v", k, m[k], w)
		}
	}
	if _, ok := m[hubproto.KeyEscalateAfter]; ok {
		t.Error("escalate_after is hub-only and must not be reported as applied")
	}
	// Absent key untouched: the review catalog is exactly the file's.
	if len(c.ReviewProviders) != 3 || !c.ReviewProviders[0].Enabled || !c.ReviewProviders[1].Enabled {
		t.Errorf("review catalog changed without the key: %+v", c.ReviewProviders)
	}
}

// A ceiling never raises: a local cap below the team's stays.
func TestApplyTeamPolicyConcurrencyIsACeiling(t *testing.T) {
	c, _, _ := policyBase(t)
	ApplyTeamPolicy(c, hubproto.Settings{MaxConcurrency: 40})
	if c.Defaults.GlobalCap != 5 {
		t.Errorf("global_cap = %d, want the local 5", c.Defaults.GlobalCap)
	}
}

// The daily budget is a ceiling too: a tighter local budget stays, an unset
// one (0 = no budget) takes the team's.
func TestApplyTeamPolicyBudgetIsACeiling(t *testing.T) {
	c, _, _ := policyBase(t)
	ApplyTeamPolicy(c, hubproto.Settings{BudgetDailyWeightedTokens: 5000})
	if c.Budget.DailyTokens != 1000 {
		t.Errorf("budget = %d, want the local 1000", c.Budget.DailyTokens)
	}
	c, _, _ = policyBase(t)
	c.Budget.DailyTokens = 0
	ApplyTeamPolicy(c, hubproto.Settings{BudgetDailyWeightedTokens: 5000})
	if c.Budget.DailyTokens != 5000 {
		t.Errorf("unset budget = %d, want the team's 5000", c.Budget.DailyTokens)
	}
}

func TestApplyTeamPolicyReviewProviders(t *testing.T) {
	c, _, _ := policyBase(t)
	got := ApplyTeamPolicy(c, hubproto.Settings{ReviewProviders: []string{"claude-session", "codex-session", "bot-watch"}})
	byKind := map[string]ReviewProvider{}
	for _, p := range c.ReviewProviders {
		byKind[p.KindString()] = p
	}
	if !byKind["claude-session"].Enabled {
		t.Error("listed claude-session must be on")
	}
	if byKind["coderabbit-cli"].Enabled {
		t.Error("unlisted coderabbit-cli must be off")
	}
	if p, ok := byKind["codex-session"]; !ok || !p.Enabled || p.TimeoutSeconds != DefaultClaudeReviewTimeoutSeconds {
		t.Errorf("listed codex-session must be added with its kind defaults: %+v", p)
	}
	if byKind["bot-watch"].Enabled {
		t.Error("bot-watch with no author cannot run and must stay off")
	}
	if len(byKind["claude-session"].Fallback) != 0 {
		t.Errorf("a fallback to a disabled kind must be dropped: %v", byKind["claude-session"].Fallback)
	}
	if !slices.ContainsFunc(c.Notices(), func(n string) bool { return strings.Contains(n, `"bot-watch"`) }) {
		t.Errorf("the unrunnable kind must be reported: %v", c.Notices())
	}
	a := appliedMap(got)[hubproto.KeyReviewProviders]
	if a.Local != "claude-session, coderabbit-cli" || !strings.Contains(a.Value, "running: claude-session, codex-session") {
		t.Errorf("applied review key = %+v", a)
	}
}

// The legacy [review] table folds into the catalog in memory only.
func TestApplyTeamPolicyReviewLegacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	src := "[defaults]\nglobal_cap = 2\nconcurrency_cap = 1\n\n[review]\nenabled = true\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	ApplyTeamPolicy(c, hubproto.Settings{ReviewProviders: []string{"claude-session"}})
	kinds := c.ReviewCatalogKinds()
	if !slices.Equal(kinds, []string{"claude-session"}) {
		t.Errorf("enabled kinds = %v, want [claude-session]", kinds)
	}
	if c.Review != (ReviewConfig{}) {
		t.Error("legacy table must be folded away in the overlay")
	}
}

// The overlay never reaches config.toml: Save refuses an overlaid Config and
// leaves the file byte-for-byte as it was.
func TestApplyTeamPolicyNeverSaved(t *testing.T) {
	c, path, src := policyBase(t)
	ApplyTeamPolicy(c, hubproto.Settings{BudgetDailyWeightedTokens: 9, MaxConcurrency: 1})
	if !c.TeamPolicyApplied() {
		t.Fatal("overlay not marked")
	}
	if err := c.Save(path); !errors.Is(err, ErrTeamPolicyOverlay) {
		t.Fatalf("Save after apply = %v, want ErrTeamPolicyOverlay", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, src) {
		t.Errorf("config.toml changed:\n%s", after)
	}
	// A fresh Load is the file again, and saves normally.
	fresh, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Budget.DailyTokens != 1000 || fresh.Defaults.GlobalCap != 5 || fresh.TeamPolicyApplied() {
		t.Errorf("re-loaded config carries the overlay: %+v", fresh.Budget)
	}
	if err := fresh.Save(path); err != nil {
		t.Errorf("Save of a fresh Load: %v", err)
	}
}

// An empty policy locks nothing.
func TestApplyTeamPolicyEmpty(t *testing.T) {
	c, _, _ := policyBase(t)
	if got := ApplyTeamPolicy(c, hubproto.Settings{}); len(got) != 0 {
		t.Errorf("applied = %+v", got)
	}
	if c.Budget.DailyTokens != 1000 || !c.MergeQueue.Enabled || c.Defaults.GlobalCap != 5 {
		t.Error("an empty policy changed config")
	}
}

func TestHubApplyPolicyDefaultAndRoundTrip(t *testing.T) {
	if !(HubConfig{}).PolicyApplies() {
		t.Error("apply_policy must default to true")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[defaults]\nglobal_cap = 2\nconcurrency_cap = 1\n\n[hub]\napply_policy = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Hub.PolicyApplies() {
		t.Fatal("apply_policy = false not read")
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "apply_policy = false") {
		t.Errorf("apply_policy not written back:\n%s", data)
	}
}
