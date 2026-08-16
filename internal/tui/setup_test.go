package tui

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitrepo"
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
	m.inspectPath = func(path string) gitrepo.Info {
		return gitrepo.Info{IsRepo: true, Root: path, Repo: "sushidev-team/nori-app", DefaultBranch: "main"}
	}
	return m
}

func typeStr(m *setupModel, s string) {
	for _, r := range s {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
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

	v := m.viewString()
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
	if strings.Contains(m.viewString(), "401") == false {
		t.Errorf("view should surface the validation error:\n%s", m.viewString())
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

// Remote parsing itself is internal/gitrepo's (see its ParseRemoteURL table);
// what the wizard owns is USING it — the picked folder fills the repo and the
// branch, and neither overwrites something already entered.
func TestSetupAdoptPathFillsRepoAndBranch(t *testing.T) {
	m := newTestSetup(t, nil, nil)
	m.inspectPath = func(path string) gitrepo.Info {
		return gitrepo.Info{IsRepo: true, Root: "/code/web", Repo: "acme/web", DefaultBranch: "develop"}
	}

	m.adoptPath("/code/web/src")
	if m.projectPath != "/code/web" {
		t.Errorf("path = %q, want the checkout root", m.projectPath)
	}
	if m.repo != "acme/web" {
		t.Errorf("repo = %q, want the detected remote", m.repo)
	}
	if m.branch != "develop" {
		t.Errorf("branch = %q, want the checkout's default", m.branch)
	}

	// A value already entered wins over any later inspection.
	m2 := newTestSetup(t, nil, nil)
	m2.repo, m2.branch = "mine/web", "release"
	m2.inspectPath = func(string) gitrepo.Info {
		return gitrepo.Info{IsRepo: true, Root: "/code/web", Repo: "acme/web", DefaultBranch: "develop"}
	}
	m2.adoptPath("/code/web")
	if m2.repo != "mine/web" || m2.branch != "release" {
		t.Errorf("repo/branch = %q/%q, want the entered values kept", m2.repo, m2.branch)
	}
}

// A directory that is not a checkout contributes nothing and is not an error —
// the path is still taken, so `git init`-later stays possible.
func TestSetupAdoptPathOnNonCheckout(t *testing.T) {
	m := newTestSetup(t, nil, nil)
	m.inspectPath = func(string) gitrepo.Info { return gitrepo.Info{} }

	m.adoptPath("/code/plain")
	if m.projectPath != "/code/plain" {
		t.Errorf("path = %q, want it taken as typed", m.projectPath)
	}
	if m.repo != "" {
		t.Errorf("repo = %q, want empty (fail closed)", m.repo)
	}
	if m.branch != config.DefaultBranchName {
		t.Errorf("branch = %q, want the seeded default", m.branch)
	}
}

// ctrl+f on the path step opens the folder browser, and choosing there fills
// the path (the same wiring the project form uses).
func TestSetupFolderBrowser(t *testing.T) {
	m := newTestSetup(t, nil, nil)
	m.step = stepProjectPath

	if _, cmd := m.Update(keyMsg("ctrl+f")); cmd == nil {
		t.Fatal("ctrl+f must open the browser and request a listing")
	}
	if m.dirs == nil {
		t.Fatal("browser did not open")
	}
	// esc closes the browser rather than quitting the wizard.
	if _, cmd := m.Update(keyMsg("esc")); cmd != nil {
		t.Error("esc in the browser must not quit the wizard")
	}
	if m.dirs != nil {
		t.Error("esc must close the browser")
	}
}
