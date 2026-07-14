package tui

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sushidev-team/lola/internal/config"
)

// newTestSetup builds a wizard with hermetic seams: no real Linear call, no
// real keychain, canned git defaults. LOLA_HOME points at a temp dir.
func newTestSetup(t *testing.T, validateErr, storeErr error) *setupModel {
	t.Helper()
	t.Setenv("LOLA_HOME", t.TempDir())
	m := newSetupModel()
	m.validateKey = func(ctx context.Context, endpoint, key string) error { return validateErr }
	m.storeKey = func(service, key string) error { return storeErr }
	m.gitToplevel = func() string { return "/tmp/nori-app" }
	m.gitRemote = func(path string) string { return "sushidev-team/nori-app" }
	return m
}

func typeStr(m *setupModel, s string) {
	for _, r := range s {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

// enterKey types the key and drives the async validation cmd to completion.
func enterKey(t *testing.T, m *setupModel, key string) {
	t.Helper()
	typeStr(m, key)
	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("key enter must return the validation command")
	}
	m.Update(cmd()) // run the fake validateKey and feed back keyValidatedMsg
}

func TestSetupKeyMaskingShowsOnlyLast4(t *testing.T) {
	m := newTestSetup(t, nil, nil)
	typeStr(m, "lin_api_secret_WXYZ")

	v := m.View()
	if !strings.Contains(v, "WXYZ") {
		t.Errorf("view must reveal the last 4 chars:\n%s", v)
	}
	if strings.Contains(v, "secret") || strings.Contains(v, "lin_api") {
		t.Errorf("view must not reveal the key body:\n%s", v)
	}
	if !strings.Contains(v, "•") {
		t.Errorf("view must mask the key body with bullets:\n%s", v)
	}
	// maskKey directly: only the last 4 runes survive.
	if got := maskKey("abcdefgh"); got != "••••efgh" {
		t.Errorf("maskKey(abcdefgh) = %q, want ••••efgh", got)
	}
}

// An invalid key keeps the wizard on the key step with an error and never
// advances; the key value must not appear in the error.
func TestSetupInvalidKeyStays(t *testing.T) {
	m := newTestSetup(t, errors.New("linear auth failed: http 401"), nil)
	const key = "lin_api_BADKEY_1234"
	enterKey(t, m, key)

	if m.step != stepKey {
		t.Fatalf("step = %d, want stepKey after invalid key", m.step)
	}
	if m.keyErr == "" {
		t.Fatal("keyErr must be set after a failed validation")
	}
	if strings.Contains(m.keyErr, key) {
		t.Errorf("keyErr leaked the key: %q", m.keyErr)
	}
	if strings.Contains(m.View(), "401") == false {
		t.Errorf("view should surface the validation error:\n%s", m.View())
	}
}

// Keychain-store failure falls back to the env var: api_key_env is set,
// api_key_keychain is not, and the key is nowhere in the written file.
func TestSetupKeychainFailureUsesEnv(t *testing.T) {
	m := newTestSetup(t, nil, errors.New("exit status 1")) // store fails
	const key = "lin_api_REALSECRET_9999"
	enterKey(t, m, key)
	if m.keySource != "env" {
		t.Fatalf("keySource = %q, want env after store failure", m.keySource)
	}
	driveToWrite(t, m)

	path, _ := config.DefaultPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), key) {
		t.Fatalf("config.toml leaked the API key:\n%s", raw)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Linear.APIKeyEnv != setupEnvVar {
		t.Errorf("api_key_env = %q, want %q", cfg.Linear.APIKeyEnv, setupEnvVar)
	}
	if cfg.Linear.APIKeyKeychain != "" {
		t.Errorf("api_key_keychain = %q, want empty on env fallback", cfg.Linear.APIKeyKeychain)
	}
}

// A full happy-path run writes a 0600 config with the project and caps, and
// records the keychain service (never the key).
func TestSetupWritesConfig(t *testing.T) {
	m := newTestSetup(t, nil, nil)
	enterKey(t, m, "lin_api_GOODKEY_5678")
	if m.keySource != "keychain" {
		t.Fatalf("keySource = %q, want keychain", m.keySource)
	}
	// path pre-filled from gitToplevel; repo pre-filled from gitRemote.
	if m.projectPath != "/tmp/nori-app" {
		t.Fatalf("project path default = %q, want the git toplevel", m.projectPath)
	}
	wrote := driveToWrite(t, m)
	if !wrote {
		t.Fatal("wizard must report a written config")
	}

	path, _ := config.DefaultPath()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v, want 0600", fi.Mode().Perm())
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Linear.APIKeyKeychain != setupKeychainService {
		t.Errorf("api_key_keychain = %q, want %q", cfg.Linear.APIKeyKeychain, setupKeychainService)
	}
	if cfg.Defaults.ConcurrencyCap != 2 || cfg.Defaults.GlobalCap != 4 {
		t.Errorf("caps = %d/%d, want 2/4", cfg.Defaults.ConcurrencyCap, cfg.Defaults.GlobalCap)
	}
	if len(cfg.Projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(cfg.Projects))
	}
	p := cfg.Projects[0]
	if p.Path != "/tmp/nori-app" || p.Repo != "sushidev-team/nori-app" || p.Name != "nori-app" {
		t.Errorf("project = %+v, want path/repo/name from git defaults", p)
	}
}

// Esc before the write quits without leaving a config behind.
func TestSetupEscBeforeWriteNoFile(t *testing.T) {
	m := newTestSetup(t, nil, nil)
	enterKey(t, m, "lin_api_KEY_0000")
	// On the project-path step, esc out.
	_, cmd := m.Update(keyMsg("esc"))
	if cmd == nil {
		t.Fatal("esc must return the quit command")
	}
	if m.wrote {
		t.Error("wrote = true after esc, want false")
	}
	path, _ := config.DefaultPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("config.toml exists after esc-before-write (err=%v)", err)
	}
}

// driveToWrite accepts every remaining default field and writes the config,
// returning the wrote flag. Assumes the model is on stepProjectPath.
func driveToWrite(t *testing.T, m *setupModel) bool {
	t.Helper()
	// path, repo, branch, concurrency, global_cap, interval, then confirm.
	for i := 0; i < 7; i++ {
		m.Update(keyMsg("enter"))
	}
	if m.step != stepConfirm {
		t.Fatalf("after accepting defaults, step = %d, want stepConfirm", m.step)
	}
	_, cmd := m.Update(keyMsg("enter")) // write
	if cmd == nil {
		t.Fatalf("confirm enter must return quit; errs: %q", m.fieldErr)
	}
	return m.wrote
}

func TestParseGitRemoteTable(t *testing.T) {
	cases := []struct{ in, want string }{
		{"git@github.com:sushidev-team/nori-app.git", "sushidev-team/nori-app"},
		{"git@github.com:sushidev-team/nori-app", "sushidev-team/nori-app"},
		{"https://github.com/sushidev-team/nori-app.git", "sushidev-team/nori-app"},
		{"https://github.com/sushidev-team/nori-app", "sushidev-team/nori-app"},
		{"ssh://git@github.com/sushidev-team/nori-app.git", "sushidev-team/nori-app"},
		{"", ""},
	}
	for _, c := range cases {
		if got := parseGitRemote(c.in); got != c.want {
			t.Errorf("parseGitRemote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
