package main

import (
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
