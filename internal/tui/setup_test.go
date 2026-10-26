package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/config"
)

// All credential operations use fakes; setup never touches the real keychain.
func newTestSetup(t *testing.T, validateErr, storeErr error) *setupModel {
	t.Helper()
	t.Setenv("LOLA_HOME", t.TempDir())
	m := newSetupModel()
	m.validateKey = func(context.Context, string, string) error { return validateErr }
	m.storeKey = func(string, string) error { return storeErr }
	return m
}

func typeStr(m *setupModel, s string) {
	for _, r := range s {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func enterKey(t *testing.T, m *setupModel, key string) {
	t.Helper()
	typeStr(m, key)
	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("key enter must validate")
	}
	m.Update(cmd())
}

func driveToWrite(t *testing.T, m *setupModel) bool {
	t.Helper()
	if m.step != stepConfirm {
		t.Fatal("a connected key must focus Finish directly")
	}
	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil || !m.wrote {
		t.Fatalf("setup did not finish: %s", m.fieldErr)
	}
	return m.wrote
}

func TestSetupKeyMaskingShowsOnlyLast4(t *testing.T) {
	m := newTestSetup(t, nil, nil)
	typeStr(m, "lin_api_secret_WXYZ")
	v := m.viewString()
	if !strings.Contains(v, "WXYZ") || strings.Contains(v, "lin_api") || strings.Contains(v, "secret") || !strings.Contains(v, "•") {
		t.Fatal("key masking failed")
	}
	if maskKey("abcdefgh") != "••••efgh" {
		t.Fatal("key mask changed")
	}
}

func TestSetupInvalidKeyStays(t *testing.T) {
	m := newTestSetup(t, errors.New("linear auth failed: http 401"), nil)
	stored := false
	m.storeKey = func(string, string) error { stored = true; return nil }
	enterKey(t, m, "lin_api_BADKEY_1234")
	if m.step != stepKey || m.keyErr == "" || stored || !strings.Contains(m.viewString(), "401") {
		t.Fatal("invalid key advanced or was stored")
	}
}

func TestSetupRequiresKeyAndLocksDuringValidation(t *testing.T) {
	m := newTestSetup(t, nil, nil)
	if _, cmd := m.Update(keyMsg("enter")); cmd != nil || m.keyErr == "" {
		t.Fatal("empty key was accepted")
	}
	m.Update(tea.PasteMsg{Content: "  secret-key\n"})
	_, cmd := m.Update(keyMsg("enter"))
	m.Update(tea.PasteMsg{Content: "changed"})
	typeStr(m, "x")
	if m.key != "secret-key" {
		t.Fatal("validation did not freeze the trimmed key")
	}
	m.Update(cmd())
	if m.key != "" || m.keySource != "keychain" {
		t.Fatal("connected key was not cleared")
	}
}

func TestSetupWritesConfigWithoutProjects(t *testing.T) {
	m := newTestSetup(t, nil, nil)
	enterKey(t, m, "lin_api_GOODKEY_5678")
	driveToWrite(t, m)
	path, _ := config.DefaultPath()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Projects) != 0 {
		t.Fatal("setup created a project")
	}
	if cfg.Defaults.ConcurrencyCap != 2 || cfg.Defaults.GlobalCap != 4 || cfg.Defaults.PollInterval != time.Minute || cfg.Defaults.Agent != config.DefaultAgent || cfg.UITheme() != config.DefaultUITheme {
		t.Fatal("general defaults were lost")
	}
	if cfg.Linear.APIKeyKeychain != setupKeychainService {
		t.Fatal("wrong credential source")
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "lin_api_GOODKEY_5678") {
		t.Fatal("configuration leaked the key")
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0600 {
		t.Fatal("configuration is not private")
	}
}

func TestSetupGeneralSettingsAreOptionalAndEditable(t *testing.T) {
	m := newTestSetup(t, nil, nil)
	enterKey(t, m, "key")
	for i := 0; i < 5; i++ {
		m.Update(keyMsg("up"))
	}
	if m.step != stepAgent {
		t.Fatal("cannot reach general settings")
	}
	m.Update(keyMsg("right"))
	m.Update(keyMsg("enter"))
	m.Update(keyMsg("right"))
	if m.agent != "codex" || m.theme != "catppuccin-latte" {
		t.Fatal("agent/theme choices did not change")
	}
	m.Update(keyMsg("enter"))
	m.concurrency = "3"
	m.Update(keyMsg("enter"))
	m.globalCap = "6"
	m.Update(keyMsg("enter"))
	m.interval = "2m"
	m.Update(keyMsg("enter"))
	driveToWrite(t, m)
	path, _ := config.DefaultPath()
	cfg, _ := config.Load(path)
	if cfg.Defaults.Agent != "codex" || cfg.UITheme() != "catppuccin-latte" || cfg.Defaults.ConcurrencyCap != 3 || cfg.Defaults.GlobalCap != 6 || cfg.Defaults.PollInterval != 2*time.Minute {
		t.Fatal("general settings did not persist")
	}
}

func TestSetupBlankGeneralFieldsUseDefaults(t *testing.T) {
	m := newTestSetup(t, nil, nil)
	enterKey(t, m, "key")
	m.concurrency, m.globalCap, m.interval = "", "", ""
	driveToWrite(t, m)
	path, _ := config.DefaultPath()
	cfg, _ := config.Load(path)
	if cfg.Defaults.ConcurrencyCap != 2 || cfg.Defaults.GlobalCap != 4 || cfg.Defaults.PollInterval != time.Minute {
		t.Fatal("optional empty fields blocked or changed defaults")
	}
}

func TestSetupInvalidGeneralSettingsNeverWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*setupModel)
	}{
		{"project limit", func(m *setupModel) { m.concurrency = "0" }},
		{"workspace limit", func(m *setupModel) { m.globalCap = "-1" }},
		{"fractional limit", func(m *setupModel) { m.globalCap = "1.5" }},
		{"interval", func(m *setupModel) { m.interval = "tomorrow" }},
		{"short interval", func(m *setupModel) { m.interval = "1s" }},
		{"agent", func(m *setupModel) { m.agent = "unknown" }},
		{"theme", func(m *setupModel) { m.theme = "unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestSetup(t, nil, nil)
			enterKey(t, m, "key")
			tc.edit(m)
			m.Update(keyMsg("enter"))
			if m.wrote || m.fieldErr == "" {
				t.Fatal("invalid settings were accepted")
			}
			path, _ := config.DefaultPath()
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("invalid settings wrote a config")
			}
		})
	}
}

func TestSetupKeychainFailureUsesEnv(t *testing.T) {
	m := newTestSetup(t, nil, errors.New("keychain unavailable"))
	enterKey(t, m, "private-secret")
	driveToWrite(t, m)
	path, _ := config.DefaultPath()
	cfg, _ := config.Load(path)
	raw, _ := os.ReadFile(path)
	if cfg.Linear.APIKeyEnv != setupEnvVar || cfg.Linear.APIKeyKeychain != "" || strings.Contains(string(raw), "private-secret") || !strings.Contains(m.viewString(), setupEnvVar) {
		t.Fatal("unsafe or unexplained fallback")
	}
}

func TestSetupEscBeforeWriteNoFile(t *testing.T) {
	m := newTestSetup(t, nil, nil)
	enterKey(t, m, "key")
	if _, cmd := m.Update(keyMsg("esc")); cmd == nil || m.wrote {
		t.Fatal("escape did not cancel")
	}
	path, _ := config.DefaultPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("cancel wrote a configuration")
	}
}

func TestSetupRerunPreservesProjects(t *testing.T) {
	m := newTestSetup(t, nil, nil)
	path, _ := config.DefaultPath()
	cfg, _ := config.Load(path)
	cfg.Defaults.GlobalCap = 4
	cfg.Projects = []config.Project{{Name: "app", Path: "/code/app", DefaultBranch: "main"}}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	enterKey(t, m, "key")
	driveToWrite(t, m)
	cfg, _ = config.Load(path)
	if len(cfg.Projects) != 1 || cfg.Projects[0].Path != "/code/app" {
		t.Fatal("rerun erased an existing project")
	}
}

func TestSetupFitsCompactTerminals(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		m := newTestSetup(t, nil, nil)
		m.width, m.height = size[0], size[1]
		enterKey(t, m, "key")
		for _, step := range []int{stepConfirm, stepAgent, stepInterval} {
			m.step = step
			out := m.viewString()
			if len(strings.Split(out, "\n")) > m.height {
				t.Fatal("setup exceeds terminal height")
			}
			for _, line := range strings.Split(out, "\n") {
				if lipgloss.Width(line) > m.width {
					t.Fatal("setup exceeds terminal width")
				}
			}
			if !strings.Contains(out, "enter") {
				t.Fatal("navigation is hidden")
			}
			if !strings.Contains(out, "Linear connected") {
				t.Fatal("credential success is hidden")
			}
			if m.width == 80 && (!strings.Contains(out, "Poll interval") || !strings.Contains(out, "Start Lola")) {
				t.Fatal("wide setup hid optional settings or its finish action")
			}
		}
	}
}

func TestCaptureSetup(t *testing.T) {
	dir := os.Getenv("LOLA_TUI_CAPTURE_DIR")
	if dir == "" {
		t.Skip("set LOLA_TUI_CAPTURE_DIR for offline captures")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, size := range []struct {
		name string
		w, h int
	}{{"wide", 80, 24}, {"compact", 40, 12}} {
		m := newTestSetup(t, nil, nil)
		m.width, m.height = size.w, size.h
		for _, state := range []string{"key", "ready", "settings"} {
			if state == "ready" {
				enterKey(t, m, "fixture-key")
			}
			if state == "settings" {
				m.step = stepAgent
			}
			if err := os.WriteFile(filepath.Join(dir, "setup-"+size.name+"-"+state+".ansi"), []byte(m.viewString()), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
