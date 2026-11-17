package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
)

func TestSetupNeedsOnlyLinearKey(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	kc := stubDesktopKeychain(t, false)
	const key = "lin_api_setup_secret"
	res, err := (&ConfigService{}).Setup(SetupDTO{LinearKey: "  " + key + "  "})
	if err != nil {
		t.Fatal(err)
	}
	if !res.KeychainStored || kc.service != setupKeychainService || kc.key != key {
		t.Fatal("key was not stored under the shared service")
	}
	path, _ := config.DefaultPath()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Projects) != 0 {
		t.Fatal("first-run setup created a project")
	}
	if cfg.Defaults.ConcurrencyCap != 2 || cfg.Defaults.GlobalCap != 4 || cfg.Defaults.PollInterval != time.Minute || cfg.AgentForProject("") != config.DefaultAgent || cfg.UITheme() != config.DefaultUITheme {
		t.Fatal("general defaults did not round-trip")
	}
	if cfg.Linear.APIKeyKeychain != setupKeychainService || cfg.Linear.APIKeyEnv != "" {
		t.Fatal("wrong credential source")
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), key) {
		t.Fatal("credential entered config.toml")
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0600 {
		t.Fatal("configuration must remain private")
	}
}

func TestSetupSavesGeneralSettingsAndPreservesProjects(t *testing.T) {
	path := writeTestConfig(t, minimalConfig)
	cfg, _ := config.Load(path)
	cfg.Projects = []config.Project{{Name: "app", Path: "/code/app", Repo: "acme/app", DefaultBranch: "main"}}
	cfg.Defaults.PostCreate = []string{"echo retained"}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	stubDesktopKeychain(t, false)
	_, err := (&ConfigService{}).Setup(SetupDTO{LinearKey: "new-key", Agent: "codex", Theme: "catppuccin-latte", ConcurrencyCap: 3, GlobalCap: 6, PollInterval: "2m"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Defaults.Agent != "codex" || got.UITheme() != "catppuccin-latte" || got.Defaults.ConcurrencyCap != 3 || got.Defaults.GlobalCap != 6 || got.Defaults.PollInterval != 2*time.Minute {
		t.Fatal("chosen general settings were lost")
	}
	if len(got.Projects) != 1 || got.Projects[0].Path != "/code/app" || len(got.Defaults.PostCreate) != 1 {
		t.Fatal("rerunning setup lost existing configuration")
	}
}

func TestSetupRejectsInvalidInputBeforeStoringKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*SetupDTO)
	}{
		{"empty key", func(d *SetupDTO) { d.LinearKey = "  " }},
		{"negative project limit", func(d *SetupDTO) { d.ConcurrencyCap = -1 }},
		{"negative workspace limit", func(d *SetupDTO) { d.GlobalCap = -1 }},
		{"invalid interval", func(d *SetupDTO) { d.PollInterval = "tomorrow" }},
		{"short interval", func(d *SetupDTO) { d.PollInterval = "1s" }},
		{"negative interval", func(d *SetupDTO) { d.PollInterval = "-1m" }},
		{"unknown agent", func(d *SetupDTO) { d.Agent = "unknown" }},
		{"unknown theme", func(d *SetupDTO) { d.Theme = "unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LOLA_HOME", t.TempDir())
			kc := stubDesktopKeychain(t, false)
			d := SetupDTO{LinearKey: "secret"}
			tc.edit(&d)
			if _, err := (&ConfigService{}).Setup(d); err == nil {
				t.Fatal("invalid setup succeeded")
			}
			if kc.key != "" {
				t.Fatal("invalid settings changed the credential")
			}
			path, _ := config.DefaultPath()
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("invalid setup wrote a configuration")
			}
		})
	}
}

func TestSetupKeychainFallbackDoesNotExposeKey(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	stubDesktopKeychain(t, true)
	res, err := (&ConfigService{}).Setup(SetupDTO{LinearKey: "private-key"})
	if err != nil {
		t.Fatal(err)
	}
	if res.KeychainStored || res.EnvVar != setupEnvVar || !strings.Contains(res.Message, setupEnvVar) {
		t.Fatal("fallback did not explain the required environment setup")
	}
	path, _ := config.DefaultPath()
	cfg, _ := config.Load(path)
	raw, _ := os.ReadFile(path)
	if cfg.Linear.APIKeyEnv != setupEnvVar || cfg.Linear.APIKeyKeychain != "" || strings.Contains(string(raw), "private-key") {
		t.Fatal("wrong or unsafe fallback")
	}
}

func TestSetupPreservesUnreadableConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOLA_HOME", dir)
	path := filepath.Join(dir, "config.toml")
	const invalid = "[broken"
	if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
		t.Fatal(err)
	}
	kc := stubDesktopKeychain(t, false)
	if _, err := (&ConfigService{}).Setup(SetupDTO{LinearKey: "secret"}); err == nil {
		t.Fatal("unreadable configuration was replaced")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != invalid || kc.key != "" {
		t.Fatal("failed setup changed existing state")
	}
}
