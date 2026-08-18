package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
)

// writeTestConfig isolates $LOLA_HOME to a temp dir and seeds a minimal VALID
// config.toml there, so the ConfigService methods below exercise the real
// load → mutate → Validate → atomic Save path without touching the operator's
// own ~/.lola. No daemon listens on that home's socket; saveConfig's reload is
// best-effort and its dial failure is expected.
func writeTestConfig(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	path := filepath.Join(home, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const minimalConfig = "[defaults]\nglobal_cap = 4\nconcurrency_cap = 2\n"

func TestEnvRoundTrip(t *testing.T) {
	lines := []string{"A=1", "B=two=with=eq", "C="}
	m, err := linesToEnv(lines)
	if err != nil {
		t.Fatalf("linesToEnv: %v", err)
	}
	if m["A"] != "1" || m["B"] != "two=with=eq" || m["C"] != "" {
		t.Fatalf("env map = %+v", m)
	}
	// envToLines is sorted and stable.
	got := envToLines(m)
	want := []string{"A=1", "B=two=with=eq", "C="}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("envToLines = %v, want %v", got, want)
	}
}

func TestLinesToEnvRejectsBadLine(t *testing.T) {
	if _, err := linesToEnv([]string{"NOEQUALS"}); err == nil {
		t.Fatal("want error for a line without '='")
	}
}

func TestLinesToEnvSkipsBlank(t *testing.T) {
	m, err := linesToEnv([]string{"  ", "", "K=v"})
	if err != nil {
		t.Fatalf("linesToEnv: %v", err)
	}
	if len(m) != 1 || m["K"] != "v" {
		t.Fatalf("map = %+v", m)
	}
}

func TestEnvToLinesEmpty(t *testing.T) {
	if got := envToLines(nil); got != nil {
		t.Fatalf("want nil for empty map, got %v", got)
	}
}

func TestNonEmpty(t *testing.T) {
	got := nonEmpty([]string{"a", "", "  ", "b"})
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("nonEmpty = %v", got)
	}
}

// Themes mirrors config.UIThemes exactly, so the settings form can enumerate
// the accepted identifiers instead of hardcoding a list that could drift and
// start writing configs Validate rejects.
func TestThemesMirrorsConfig(t *testing.T) {
	got := (&ConfigService{}).Themes()
	if !reflect.DeepEqual(got, config.UIThemes) {
		t.Fatalf("Themes() = %v, want %v", got, config.UIThemes)
	}
	// A copy, not the package slice: a frontend-bound method must not hand out
	// mutable access to package state.
	got[0] = "mutated"
	if config.UIThemes[0] == "mutated" {
		t.Fatal("Themes() aliased config.UIThemes")
	}
}

// A config with no [ui] table reports the effective default rather than "".
func TestGetThemeDefaultsWhenUnset(t *testing.T) {
	writeTestConfig(t, minimalConfig)
	if got := (&ConfigService{}).GetTheme(); got != config.DefaultUITheme {
		t.Fatalf("GetTheme() = %q, want %q", got, config.DefaultUITheme)
	}
}

// GetTheme reads an explicitly-configured theme back.
func TestGetThemeReadsExplicit(t *testing.T) {
	writeTestConfig(t, minimalConfig+"\n[ui]\ntheme = \"catppuccin-latte\"\n")
	if got := (&ConfigService{}).GetTheme(); got != "catppuccin-latte" {
		t.Fatalf("GetTheme() = %q, want catppuccin-latte", got)
	}
}

// SetTheme persists through the shared save path and is readable back, for
// every identifier the config package accepts.
func TestSetThemePersists(t *testing.T) {
	for _, theme := range config.UIThemes {
		t.Run(theme, func(t *testing.T) {
			path := writeTestConfig(t, minimalConfig)
			s := &ConfigService{}
			if err := s.SetTheme(theme); err != nil {
				t.Fatalf("SetTheme(%q): %v", theme, err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "theme = \""+theme+"\"") {
				t.Errorf("config should carry the theme, got:\n%s", data)
			}
			if got := s.GetTheme(); got != theme {
				t.Errorf("GetTheme() = %q, want %q", got, theme)
			}
		})
	}
}

// An unknown identifier is rejected by config.Validate before anything is
// written — the file must be left exactly as it was.
func TestSetThemeRejectsUnknown(t *testing.T) {
	path := writeTestConfig(t, minimalConfig)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = (&ConfigService{}).SetTheme("dracula")
	if err == nil {
		t.Fatal("SetTheme should reject an unknown theme")
	}
	if !strings.Contains(err.Error(), "ui.theme") {
		t.Errorf("error should name the key, got: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("a rejected theme must not touch the file:\n before: %s\n after: %s", before, after)
	}
}

// An empty name clears the key, dropping the [ui] table and restoring the
// default — the reset path.
func TestSetThemeEmptyClears(t *testing.T) {
	path := writeTestConfig(t, minimalConfig+"\n[ui]\ntheme = \"catppuccin-latte\"\n")
	s := &ConfigService{}
	if err := s.SetTheme(""); err != nil {
		t.Fatalf("SetTheme(\"\"): %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "[ui]") {
		t.Errorf("clearing the theme should drop the [ui] table, got:\n%s", data)
	}
	if got := s.GetTheme(); got != config.DefaultUITheme {
		t.Errorf("GetTheme() after clear = %q, want %q", got, config.DefaultUITheme)
	}
}

// SaveSettings must not touch [ui]: the theme has a single writer (SetTheme),
// so a settings commit can never clobber it.
func TestSaveSettingsPreservesTheme(t *testing.T) {
	writeTestConfig(t, minimalConfig+"\n[ui]\ntheme = \"catppuccin-frappe\"\n")
	s := &ConfigService{}
	dto, err := s.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	dto.GlobalCap = 7
	if err := s.SaveSettings(dto); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if got := s.GetTheme(); got != "catppuccin-frappe" {
		t.Fatalf("theme after SaveSettings = %q, want catppuccin-frappe", got)
	}
}

func TestGetProjectNewIsBlank(t *testing.T) {
	s := &ConfigService{}
	dto, err := s.GetProject("")
	if err != nil {
		t.Fatalf("GetProject(\"\"): %v", err)
	}
	if !dto.IsNew {
		t.Fatal("expected IsNew for empty name")
	}
}

// A daemon rejecting a config this build just validated means version skew, not
// a bad config — and the desktop used to DISCARD that entirely, so a live
// daemon could sit on stale config while the UI reported a clean save.
func TestReloadRejectionHint(t *testing.T) {
	stale := `config invalid, keeping previous: project "Okane" polling: dedup_mode=label requires on_sent_set_label`
	got := reloadRejectionHint(stale)
	if !strings.Contains(got, "OLDER binary") {
		t.Errorf("an inherited-key complaint must name the stale daemon:\n%s", got)
	}

	// A real config problem is reported as-is, not blamed on the daemon.
	real := `config invalid, keeping previous: project "web": path is required`
	if got := reloadRejectionHint(real); got != real {
		t.Errorf("a real error must pass through, got:\n%s", got)
	}

	// A daemon that is simply down, or too old to know the command, is not a
	// failure worth interrupting a successful save for.
	for _, msg := range []string{"connection refused", `unknown cmd "reload"`} {
		if got := reloadRejectionHint(msg); got != "" {
			t.Errorf("a non-rejection must be silent, got %q", got)
		}
	}
}

// A project has two names: `label` is display-only, `name` is the id baked into
// worktree paths and tmux session names. SaveProject owns the id's final shape.
func TestSaveProjectSlugsIDAndKeepsLabel(t *testing.T) {
	writeTestConfig(t, minimalConfig)
	s := &ConfigService{}

	dto, err := s.GetProject("")
	if err != nil {
		t.Fatalf("GetProject(\"\"): %v", err)
	}
	dto.Name = "Nori App" // a client that skipped the frontend slug
	dto.Label = "Nori App"
	dto.Path = t.TempDir()
	dto.CycleMode = "none"
	dto.AssigneeMode = "anyone"
	if err := s.SaveProject(dto); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}

	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.ProjectByName("nori-app")
	if p == nil {
		t.Fatalf("project not saved under the slugged id; got %+v", cfg.Projects)
	}
	if p.Label != "Nori App" {
		t.Errorf("Label = %q, want the verbatim label", p.Label)
	}
}

// A label identical to the id carries nothing and is dropped, so the file never
// grows a redundant key.
func TestSaveProjectDropsRedundantLabel(t *testing.T) {
	writeTestConfig(t, minimalConfig)
	s := &ConfigService{}

	dto, _ := s.GetProject("")
	dto.Name, dto.Label, dto.Path = "web", "web", t.TempDir()
	dto.CycleMode, dto.AssigneeMode = "none", "anyone"
	if err := s.SaveProject(dto); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if l := cfg.ProjectByName("web").Label; l != "" {
		t.Errorf("Label = %q, want it dropped as redundant", l)
	}
}

// An id that slugs to nothing is refused with a message explaining the
// label -> id relationship, rather than writing an unusable project.
func TestSaveProjectRejectsUnsluggableID(t *testing.T) {
	writeTestConfig(t, minimalConfig)
	s := &ConfigService{}

	dto, _ := s.GetProject("")
	dto.Name, dto.Path = "日本語", t.TempDir()
	err := s.SaveProject(dto)
	if err == nil {
		t.Fatal("SaveProject accepted a name with no usable id")
	}
	if !strings.Contains(err.Error(), "project id is required") {
		t.Errorf("err = %v, want the id requirement", err)
	}
}

// An EXISTING project whose id is not on disk means the rename that should have
// preceded the save did not happen. Appending would fork the project in two, so
// the save must refuse.
func TestSaveProjectRefusesToForkOnUnknownID(t *testing.T) {
	writeTestConfig(t, minimalConfig+"\n[[project]]\nname = \"web\"\npath = \"/tmp/web\"\n")
	s := &ConfigService{}

	dto, err := s.GetProject("web")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	dto.Name = "web-two" // renamed in the form, but no daemon rename ran
	if err := s.SaveProject(dto); err == nil {
		t.Fatal("SaveProject silently created a second project instead of refusing")
	}
	cfg, _, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Projects) != 1 {
		t.Errorf("projects = %d, want the original one untouched", len(cfg.Projects))
	}
}

// A directory that is not a checkout still yields a usable suggestion: the
// derived facts are empty (fail-closed) but the id/label the form prefills come
// from the folder name, so the pick is never wasted.
func TestInspectPathOnPlainDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nori-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	got := (&ConfigService{}).InspectPath(dir)
	if got.IsRepo {
		t.Errorf("isRepo = true for %q, want false", dir)
	}
	if got.Path != dir {
		t.Errorf("path = %q, want the directory as given", got.Path)
	}
	if got.Repo != "" || got.DefaultBranch != "" {
		t.Errorf("repo/branch = %q/%q, want both empty (fail closed)", got.Repo, got.DefaultBranch)
	}
	if got.Branches == nil {
		t.Error("branches must marshal as [] rather than null")
	}
	if got.SuggestedLabel != "Nori App" || got.SuggestedID != "nori-app" {
		t.Errorf("suggestion = %q/%q, want Nori App/nori-app", got.SuggestedLabel, got.SuggestedID)
	}
}

// An empty path is answered, not errored: the form calls this on every blur.
func TestInspectPathEmpty(t *testing.T) {
	got := (&ConfigService{}).InspectPath("")
	if got.IsRepo || got.Path != "" || got.SuggestedID != "" {
		t.Errorf("InspectPath(\"\") = %+v, want the zero answer", got)
	}
}

// --- [linear] API key -------------------------------------------------------
//
// The key was settable only in the first-run wizard: neither this app's settings
// nor the TUI's had a field for it, so a hand-written config could never gain
// one and rotating a key meant editing the Keychain by hand — while a daemon
// without a key fails every poll.

// stubDesktopKeychain replaces the keychain write and records what it received.
func stubDesktopKeychain(t *testing.T, fail bool) *struct{ service, key string } {
	t.Helper()
	got := &struct{ service, key string }{}
	orig := storeLinearKey
	storeLinearKey = func(service, key string) error {
		got.service, got.key = service, key
		if fail {
			return errors.New("keychain unavailable")
		}
		return nil
	}
	t.Cleanup(func() { storeLinearKey = orig })
	return got
}

func TestSetLinearKeyStoresToKeychainAndNamesTheSource(t *testing.T) {
	path := writeTestConfig(t, minimalConfig)
	kc := stubDesktopKeychain(t, false)
	svc := &ConfigService{}

	msg, err := svc.SetLinearKey("  lin_api_secret  ")
	if err != nil {
		t.Fatalf("SetLinearKey: %v", err)
	}
	if kc.service != setupKeychainService || kc.key != "lin_api_secret" {
		t.Errorf("keychain got service=%q key=%q; the key must be trimmed", kc.service, kc.key)
	}
	if !strings.Contains(msg, "Keychain") {
		t.Errorf("message must say where the key went, got %q", msg)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Linear.APIKeyKeychain != setupKeychainService {
		t.Errorf("api_key_keychain = %q, want %q", cfg.Linear.APIKeyKeychain, setupKeychainService)
	}
	// The value must never reach the file — that is what the keychain is for.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "lin_api_secret") {
		t.Fatalf("the key leaked into config.toml:\n%s", raw)
	}
}

// Rotating from an env-var config to a keychain one must not leave BOTH sources
// named: the stale env var would keep winning or confusing the doctor.
func TestSetLinearKeyClearsTheEnvSourceOnRotation(t *testing.T) {
	path := writeTestConfig(t, minimalConfig+"\n[linear]\napi_key_env = \"OLD_VAR\"\n")
	stubDesktopKeychain(t, false)

	if _, err := (&ConfigService{}).SetLinearKey("lin_api_new"); err != nil {
		t.Fatalf("SetLinearKey: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Linear.APIKeyEnv != "" {
		t.Errorf("api_key_env = %q, want it cleared once the keychain holds the key", cfg.Linear.APIKeyEnv)
	}
}

// No keychain must still leave a WORKING configuration, and say what the user
// has left to do.
func TestSetLinearKeyFallsBackToEnvVar(t *testing.T) {
	path := writeTestConfig(t, minimalConfig)
	stubDesktopKeychain(t, true)

	msg, err := (&ConfigService{}).SetLinearKey("lin_api_secret")
	if err != nil {
		t.Fatalf("SetLinearKey: %v", err)
	}
	if !strings.Contains(msg, setupEnvVar) {
		t.Errorf("message must name the env var to export, got %q", msg)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Linear.APIKeyEnv != setupEnvVar {
		t.Errorf("api_key_env = %q, want %q", cfg.Linear.APIKeyEnv, setupEnvVar)
	}
}

func TestSetLinearKeyRejectsEmpty(t *testing.T) {
	writeTestConfig(t, minimalConfig)
	stubDesktopKeychain(t, false)
	if _, err := (&ConfigService{}).SetLinearKey("   "); err == nil {
		t.Fatal("SetLinearKey(\"   \") must fail rather than write a blank key")
	}
}

func TestLinearKeyStatusReportsSourceWithoutTheValue(t *testing.T) {
	writeTestConfig(t, minimalConfig+"\n[linear]\napi_key_env = \"LOLA_TEST_LINEAR_KEY\"\n")
	t.Setenv("LOLA_TEST_LINEAR_KEY", "lin_api_secret")

	st := (&ConfigService{}).LinearKeyStatus()
	if !st.Configured || !st.Resolvable {
		t.Fatalf("status = %+v, want configured and resolvable", st)
	}
	if !strings.Contains(st.Source, "LOLA_TEST_LINEAR_KEY") {
		t.Errorf("source = %q, want the env var named", st.Source)
	}
	// Secret discipline: nothing in the DTO may carry the key itself.
	if strings.Contains(st.Source+st.Detail, "lin_api_secret") {
		t.Fatalf("the key value leaked into the status DTO: %+v", st)
	}
}

// A config naming a source that yields nothing is the exact silent failure the
// app exists to surface: configured, but not resolvable.
func TestLinearKeyStatusReportsAnUnreadableSource(t *testing.T) {
	writeTestConfig(t, minimalConfig+"\n[linear]\napi_key_env = \"LOLA_TEST_MISSING_KEY\"\n")
	t.Setenv("LOLA_TEST_MISSING_KEY", "")

	st := (&ConfigService{}).LinearKeyStatus()
	if !st.Configured {
		t.Errorf("status = %+v, want configured", st)
	}
	if st.Resolvable {
		t.Errorf("status = %+v, want NOT resolvable", st)
	}
	if st.Detail == "" {
		t.Error("an unresolvable key must explain why")
	}
}

func TestLinearKeyStatusWithNoSource(t *testing.T) {
	writeTestConfig(t, minimalConfig)
	st := (&ConfigService{}).LinearKeyStatus()
	if st.Configured || st.Resolvable {
		t.Fatalf("status = %+v, want neither configured nor resolvable", st)
	}
}

// --- groups & layout ---------------------------------------------------------

const twoProjectConfig = `[defaults]
global_cap = 4

[[project]]
name = "okane"
path = "/tmp/okane"

[[project]]
name = "lola"
path = "/tmp/lola"
`

func TestAddGroupSlugsAndDedupes(t *testing.T) {
	path := writeTestConfig(t, minimalConfig)
	s := &ConfigService{}

	name, err := s.AddGroup("Client Work")
	if err != nil {
		t.Fatalf("AddGroup: %v", err)
	}
	if name != "client-work" {
		t.Fatalf("id = %q", name)
	}
	// A second folder a human would also call "Client Work" must coexist, not
	// silently replace the first.
	second, err := s.AddGroup("Client Work")
	if err != nil {
		t.Fatalf("AddGroup 2: %v", err)
	}
	if second != "client-work-2" {
		t.Fatalf("second id = %q", second)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Groups) != 2 || cfg.Groups[0].Label != "Client Work" {
		t.Fatalf("groups = %+v", cfg.Groups)
	}
}

// A label that already IS its id carries nothing, so it is not written — the
// same rule SaveProject applies to a project's label.
func TestAddGroupOmitsRedundantLabel(t *testing.T) {
	path := writeTestConfig(t, minimalConfig)
	s := &ConfigService{}
	if _, err := s.AddGroup("clients"); err != nil {
		t.Fatalf("AddGroup: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Groups[0].Label != "" {
		t.Fatalf("label = %q, want empty", cfg.Groups[0].Label)
	}
}

func TestAddGroupRejectsEmptyName(t *testing.T) {
	writeTestConfig(t, minimalConfig)
	s := &ConfigService{}
	if _, err := s.AddGroup("   /// "); err == nil {
		t.Fatal("want an error for a label that slugs to nothing")
	}
}

func TestRemoveGroupUngroupsItsProjects(t *testing.T) {
	path := writeTestConfig(t, twoProjectConfig)
	s := &ConfigService{}
	if _, err := s.AddGroup("clients"); err != nil {
		t.Fatalf("AddGroup: %v", err)
	}
	if err := s.SetProjectLayout(ProjectLayoutDTO{
		Groups: []GroupDTO{{Name: "clients"}},
		Projects: []ProjectPlacementDTO{
			{Name: "okane", Group: "clients"},
			{Name: "lola"},
		},
	}); err != nil {
		t.Fatalf("SetProjectLayout: %v", err)
	}
	if err := s.RemoveGroup("clients"); err != nil {
		t.Fatalf("RemoveGroup: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Groups) != 0 {
		t.Fatalf("groups = %+v", cfg.Groups)
	}
	// Deleting a folder must never cost a project.
	if len(cfg.Projects) != 2 {
		t.Fatalf("projects = %+v", cfg.Projects)
	}
	if cfg.Projects[0].Group != "" {
		t.Fatalf("project still filed under %q", cfg.Projects[0].Group)
	}
}

func TestRenameGroupChangesLabelOnly(t *testing.T) {
	path := writeTestConfig(t, minimalConfig)
	s := &ConfigService{}
	if _, err := s.AddGroup("clients"); err != nil {
		t.Fatalf("AddGroup: %v", err)
	}
	if err := s.RenameGroup("clients", "Client Work"); err != nil {
		t.Fatalf("RenameGroup: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Groups[0].Name != "clients" || cfg.Groups[0].Label != "Client Work" {
		t.Fatalf("group = %+v", cfg.Groups[0])
	}
	if err := s.RenameGroup("nope", "x"); err == nil {
		t.Fatal("want an error renaming a group that does not exist")
	}
}

func TestSetGroupCollapsedPersists(t *testing.T) {
	path := writeTestConfig(t, minimalConfig)
	s := &ConfigService{}
	if _, err := s.AddGroup("clients"); err != nil {
		t.Fatalf("AddGroup: %v", err)
	}
	if err := s.SetGroupCollapsed("clients", true); err != nil {
		t.Fatalf("SetGroupCollapsed: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Groups[0].Collapsed {
		t.Fatal("collapse did not persist")
	}
	if err := s.SetGroupCollapsed("nope", true); err == nil {
		t.Fatal("want an error for a group that does not exist")
	}
}

func TestSetProjectLayoutReordersAndFiles(t *testing.T) {
	path := writeTestConfig(t, twoProjectConfig)
	s := &ConfigService{}
	if _, err := s.AddGroup("clients"); err != nil {
		t.Fatalf("AddGroup: %v", err)
	}
	if err := s.SetProjectLayout(ProjectLayoutDTO{
		Groups: []GroupDTO{{Name: "clients", Collapsed: true}},
		Projects: []ProjectPlacementDTO{
			{Name: "lola"},
			{Name: "okane", Group: "clients"},
		},
	}); err != nil {
		t.Fatalf("SetProjectLayout: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// The [[project]] array order IS the render order, in this app and the TUI.
	if cfg.Projects[0].Name != "lola" || cfg.Projects[1].Name != "okane" {
		t.Fatalf("order = %q, %q", cfg.Projects[0].Name, cfg.Projects[1].Name)
	}
	if cfg.Projects[1].Group != "clients" {
		t.Fatalf("group = %q", cfg.Projects[1].Group)
	}
	if !cfg.Groups[0].Collapsed {
		t.Fatal("collapse from the layout was dropped")
	}
	// Everything else about the project survives a pure arrangement change.
	if cfg.Projects[1].Path != "/tmp/okane" {
		t.Fatalf("path = %q", cfg.Projects[1].Path)
	}
}

// The layout is computed by a drag handler against a snapshot that may be a
// reload behind. Anything but an exact permutation is refused whole, so a stale
// layout can neither resurrect a removed project nor drop an unknown one.
func TestSetProjectLayoutRefusesStaleLayouts(t *testing.T) {
	path := writeTestConfig(t, twoProjectConfig)
	s := &ConfigService{}

	cases := []struct {
		name string
		dto  ProjectLayoutDTO
	}{
		{"missing a project", ProjectLayoutDTO{Projects: []ProjectPlacementDTO{{Name: "lola"}}}},
		{"names an unknown project", ProjectLayoutDTO{Projects: []ProjectPlacementDTO{
			{Name: "lola"}, {Name: "ghost"},
		}}},
		{"repeats a project", ProjectLayoutDTO{Projects: []ProjectPlacementDTO{
			{Name: "lola"}, {Name: "lola"},
		}}},
		{"names an unknown group", ProjectLayoutDTO{Projects: []ProjectPlacementDTO{
			{Name: "lola", Group: "ghost"}, {Name: "okane"},
		}}},
		{"names an unknown group table", ProjectLayoutDTO{
			Groups:   []GroupDTO{{Name: "ghost"}},
			Projects: []ProjectPlacementDTO{{Name: "lola"}, {Name: "okane"}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.SetProjectLayout(tc.dto); err == nil {
				t.Fatal("want an error")
			}
			cfg, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			// Refused means NOTHING was written.
			if len(cfg.Projects) != 2 || cfg.Projects[0].Name != "okane" {
				t.Fatalf("config was mutated: %+v", cfg.Projects)
			}
		})
	}
}
