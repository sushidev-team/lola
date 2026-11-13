package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAIProfilesPreserveProvidersAndLegacySettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[brain]\nenabled = true\nmodel = \"custom-summary\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, enabled := cfg.AIProfile("claude", "summary"); enabled {
		t.Fatal("enabled profiles without opt-in")
	}
	cfg.AI = map[string]AIProvider{}
	for _, provider := range []string{"claude", "codex", "opencode"} {
		p := DefaultAIProvider(provider)
		p.Enabled = true
		p.Profiles["thorough"] = AIModel{Model: provider + "/custom", Effort: "high"}
		p.Actions["resolve"] = "thorough"
		cfg.AI[provider] = p
	}
	if err = cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	next, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.AI, next.AI) || next.Brain.Model != "custom-summary" {
		t.Fatal("profile save lost provider settings or legacy model")
	}
	for _, provider := range []string{"claude", "codex", "opencode"} {
		profile, ok := next.AIProfile(provider, "resolve")
		if !ok || profile.Model != provider+"/custom" || profile.Effort != "high" {
			t.Fatalf("wrong profile for %s: %+v", provider, profile)
		}
	}
	p := next.AI["codex"]
	p.Enabled = false
	next.AI["codex"] = p
	if _, ok := next.AIProfile("codex", "resolve"); ok {
		t.Fatal("disabled profiles were applied")
	}
	if next.AI["codex"].Profiles["thorough"].Model != "codex/custom" {
		t.Fatal("disable destroyed configuration")
	}
}

func TestAIProfilesValidation(t *testing.T) {
	for _, change := range []func(*AIProvider){
		func(p *AIProvider) { p.Actions["resolve"] = "missing" },
		func(p *AIProvider) { p.Profiles["fast"] = AIModel{Model: "--bad"} },
		func(p *AIProvider) { p.Profiles["thorough"] = AIModel{Effort: "not-an-effort"} },
	} {
		p := DefaultAIProvider("claude")
		change(&p)
		c := Config{AI: map[string]AIProvider{"claude": p}}
		if len(c.validateAI()) == 0 {
			t.Fatal("accepted invalid profile")
		}
	}
}
