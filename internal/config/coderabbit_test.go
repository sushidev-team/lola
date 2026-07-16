package config

// Tests for the [coderabbit] PR-comment WATCH table (coderabbit.go): the
// default-off-when-absent behavior, the enabled-defaults ergonomics, explicit
// values surviving load (incl. a disabling send_to_agent=false and a custom
// author), the Save/Load round-trip, and a fresh config omitting the table.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodeRabbitDefaultOffWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[defaults]\nglobal_cap = 4\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.CodeRabbit != (CodeRabbitConfig{}) {
		t.Errorf("absent [coderabbit] should give the zero CodeRabbitConfig, got %+v", c.CodeRabbit)
	}
	if c.CodeRabbit.Enabled || c.CodeRabbit.Notify || c.CodeRabbit.SendToAgent || c.CodeRabbit.CommentOnLinear {
		t.Errorf("absent [coderabbit] must be fully disabled, got %+v", c.CodeRabbit)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("config without [coderabbit] must validate: %v", err)
	}
	if CodeRabbitAgentPointerFmt == "" || CodeRabbitNotifyTitle == "" || DefaultCodeRabbitAuthor == "" {
		t.Error("coderabbit hand-off consts must not be empty")
	}
}

// Enabling the watch with nothing else set turns on notify + send_to_agent and
// defaults the author to DefaultCodeRabbitAuthor; comment_on_linear stays off.
func TestCodeRabbitEnabledDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[defaults]\nglobal_cap = 4\n\n[coderabbit]\nenabled = true\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := CodeRabbitConfig{
		Enabled:         true,
		Author:          DefaultCodeRabbitAuthor,
		Notify:          true,
		SendToAgent:     true,
		CommentOnLinear: false,
	}
	if c.CodeRabbit != want {
		t.Errorf("coderabbit = %+v, want %+v", c.CodeRabbit, want)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("enabled coderabbit must validate: %v", err)
	}
}

// Explicit fields survive load: a disabling send_to_agent=false while enabled, a
// custom author, and comment_on_linear=true. Unset notify still follows Enabled.
func TestCodeRabbitExplicitValuesKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[defaults]
global_cap = 4

[coderabbit]
enabled = true
author = "sonarcloud"
send_to_agent = false
comment_on_linear = true
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := CodeRabbitConfig{
		Enabled:         true,
		Author:          "sonarcloud",
		Notify:          true,  // unset → follows Enabled
		SendToAgent:     false, // explicit false kept, not reverted to Enabled
		CommentOnLinear: true,  // explicit true kept (default is false)
	}
	if c.CodeRabbit != want {
		t.Errorf("coderabbit = %+v, want %+v", c.CodeRabbit, want)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("explicit coderabbit must validate: %v", err)
	}
}

// A fully-specified watch (with a disabling zero and a custom author)
// round-trips through Save/Load unchanged.
func TestCodeRabbitRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	orig := &Config{}
	orig.Defaults.GlobalCap = 4
	orig.Reactions = defaultReactions()
	orig.Notify = defaultNotify()
	orig.CodeRabbit = CodeRabbitConfig{
		Enabled:         true,
		Author:          "coderabbitai",
		Notify:          false, // disabling zero must survive the round-trip
		SendToAgent:     true,
		CommentOnLinear: true,
	}
	if err := orig.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if orig.CodeRabbit != got.CodeRabbit {
		t.Errorf("coderabbit round trip:\n save: %+v\n load: %+v", orig.CodeRabbit, got.CodeRabbit)
	}
}

// A fresh &Config{} does NOT persist a [coderabbit] table (the zero watch is
// unconfigured), and reloads to the disabled default.
func TestCodeRabbitFreshConfigOmitsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := (&Config{}).Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "[coderabbit") {
		t.Errorf("fresh config should omit the [coderabbit] table, got:\n%s", data)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.CodeRabbit != (CodeRabbitConfig{}) {
		t.Errorf("reloaded coderabbit = %+v, want zero (disabled)", got.CodeRabbit)
	}
}
