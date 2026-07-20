package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An absent [ui] table stays ZERO in memory — deliberately unlike [tmux], whose
// resolve materializes a default — while UITheme() still reports the effective
// default. The paired assertion is the point: it pins the
// zero-value-plus-read-time-resolver contract that keeps Save from freezing the
// default into the file (see TestUIAbsentTableNotFrozenOnSave).
func TestUIDefaultsWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[defaults]\nglobal_cap = 4\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.UI != (UIConfig{}) {
		t.Errorf("absent [ui] = %+v, want the zero UIConfig (the default lives in UITheme())", c.UI)
	}
	if got := c.UITheme(); got != DefaultUITheme {
		t.Errorf("UITheme() = %q, want %q", got, DefaultUITheme)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("config without [ui] must validate: %v", err)
	}
}

// An explicitly-set theme survives load, including one that names the default.
func TestUIExplicitValueKept(t *testing.T) {
	for _, theme := range UIThemes {
		t.Run(theme, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			body := "[defaults]\nglobal_cap = 4\n\n[ui]\ntheme = \"" + theme + "\"\n"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			c, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if c.UI.Theme != theme {
				t.Errorf("ui.theme = %q, want %q", c.UI.Theme, theme)
			}
			if got := c.UITheme(); got != theme {
				t.Errorf("UITheme() = %q, want the explicit %q", got, theme)
			}
			if err := c.Validate(); err != nil {
				t.Errorf("explicit [ui].theme %q must validate: %v", theme, err)
			}
		})
	}
}

// An explicit theme round-trips through Save/Load unchanged.
func TestUIRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	orig := &Config{}
	orig.Defaults.GlobalCap = 4
	orig.UI = UIConfig{Theme: "catppuccin-latte"}
	if err := orig.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.UI != orig.UI {
		t.Errorf("ui round trip:\n save: %+v\n load: %+v", orig.UI, got.UI)
	}
}

// A theme explicitly pinned to the DEFAULT value is still written, because the
// operator named it — "" (inherit the default) and "catppuccin-mocha" (pin it)
// are different intents and both must round-trip.
func TestUIExplicitDefaultIsWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	orig := &Config{}
	orig.Defaults.GlobalCap = 4
	orig.UI = UIConfig{Theme: DefaultUITheme}
	if err := orig.Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[ui]") {
		t.Errorf("an explicitly-pinned default theme must be persisted, got:\n%s", data)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.UI.Theme != DefaultUITheme {
		t.Errorf("pinned theme = %q, want %q", got.UI.Theme, DefaultUITheme)
	}
}

// THE regression test for the save/load identity property: a config that never
// mentioned [ui] must not GAIN a frozen [ui] table on its next save. This is
// what the zero-value + UITheme() split buys, and it is asserted through a full
// Load→Save cycle (not a bare &Config{} literal) because a load is exactly the
// step that could materialize a default.
func TestUIAbsentTableNotFrozenOnSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "[defaults]\nglobal_cap = 4\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.toml")
	if err := c.Save(out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "[ui]") {
		t.Errorf("a config with no [ui] must not gain one on save, got:\n%s", data)
	}
}

// A fresh &Config{} likewise persists no [ui] table and reloads to the
// effective default.
func TestUIFreshConfigOmitsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := (&Config{}).Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "[ui]") {
		t.Errorf("fresh config should omit the [ui] table, got:\n%s", data)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.UITheme() != DefaultUITheme {
		t.Errorf("reloaded UITheme() = %q, want %q", got.UITheme(), DefaultUITheme)
	}
}

// UITheme resolves to the configured theme, else the default, and never
// returns "".
func TestUIThemeResolver(t *testing.T) {
	c := &Config{}
	if got := c.UITheme(); got != DefaultUITheme {
		t.Errorf("zero config UITheme() = %q, want %q", got, DefaultUITheme)
	}
	c.UI.Theme = "catppuccin-frappe"
	if got := c.UITheme(); got != "catppuccin-frappe" {
		t.Errorf("UITheme() = %q, want catppuccin-frappe", got)
	}
}

// Validation accepts empty and every known identifier, and rejects anything
// else with an error naming the valid values.
func TestUIValidateTheme(t *testing.T) {
	cases := []struct {
		name  string
		theme string
		valid bool
	}{
		{"empty inherits the default", "", true},
		{"latte", "catppuccin-latte", true},
		{"frappe", "catppuccin-frappe", true},
		{"macchiato", "catppuccin-macchiato", true},
		{"mocha", "catppuccin-mocha", true},
		{"unknown name", "dracula", false},
		{"wrong separator", "catppuccin_mocha", false},
		{"case mismatch", "Catppuccin-Mocha", false},
		{"bare flavor", "mocha", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{}
			c.Defaults.GlobalCap = 4
			c.UI.Theme = tc.theme
			err := c.Validate()
			if tc.valid && err != nil {
				t.Fatalf("theme %q should validate, got: %v", tc.theme, err)
			}
			if !tc.valid {
				if err == nil {
					t.Fatalf("theme %q should be rejected", tc.theme)
				}
				if !strings.Contains(err.Error(), "ui.theme") {
					t.Errorf("error should name the key, got: %v", err)
				}
				for _, want := range UIThemes {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error should list the valid value %q, got: %v", want, err)
					}
				}
			}
		})
	}
}
