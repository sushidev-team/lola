package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/tmux"
)

// An absent [tmux] table resolves to the defaults (isolated "lola" socket,
// tmux's default detach, no status override, mouse off) and validates cleanly.
func TestTmuxDefaultsWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[defaults]\nglobal_cap = 4\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Tmux != defaultTmux() {
		t.Errorf("absent [tmux] = %+v, want defaults %+v", c.Tmux, defaultTmux())
	}
	if c.Tmux.SocketName != DefaultTmuxSocketName {
		t.Errorf("socket_name = %q, want %q", c.Tmux.SocketName, DefaultTmuxSocketName)
	}
	if c.Tmux.DetachKey != "" || c.Tmux.StatusRight != "" || c.Tmux.Mouse {
		t.Errorf("absent [tmux] must leave detach/status/mouse off, got %+v", c.Tmux)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("config without [tmux] must validate: %v", err)
	}
}

// Explicitly-set [tmux] fields survive load, including explicit disabling
// zeros (mouse = false, status_right = "") alongside overridden values.
func TestTmuxExplicitValuesKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `
[defaults]
global_cap = 4

[tmux]
socket_name = "work"
detach_key = "F12"
status_right = "#[fg=green]#S"
mouse = true
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := TmuxConfig{
		SocketName:  "work",
		DetachKey:   "F12",
		StatusRight: "#[fg=green]#S",
		Mouse:       true,
	}
	if c.Tmux != want {
		t.Errorf("tmux = %+v, want %+v", c.Tmux, want)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("explicit [tmux] must validate: %v", err)
	}
}

// A partly-specified [tmux] keeps the socket default while honoring the one set
// key, and the explicit zero mouse = false is not treated as "unset".
func TestTmuxPartialOverlay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[defaults]\nglobal_cap = 4\n\n[tmux]\ndetach_key = \"F1\"\nmouse = false\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Tmux.SocketName != DefaultTmuxSocketName {
		t.Errorf("socket_name = %q, want default %q (unset key keeps default)", c.Tmux.SocketName, DefaultTmuxSocketName)
	}
	if c.Tmux.DetachKey != "F1" {
		t.Errorf("detach_key = %q, want F1", c.Tmux.DetachKey)
	}
	if c.Tmux.Mouse {
		t.Error("mouse = true, want explicit false kept")
	}
}

// A fully-specified [tmux] round-trips through Save/Load unchanged.
func TestTmuxRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	orig := &Config{}
	orig.Defaults.GlobalCap = 4
	orig.Reactions = defaultReactions()
	orig.Notify = defaultNotify()
	orig.Tmux = TmuxConfig{
		SocketName:  "custom",
		DetachKey:   "F12",
		StatusRight: "#[fg=red]lola",
		Mouse:       true,
	}
	if err := orig.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if orig.Tmux != got.Tmux {
		t.Errorf("tmux round trip:\n save: %+v\n load: %+v", orig.Tmux, got.Tmux)
	}
}

// A fresh &Config{} does NOT persist a [tmux] table (the zero tmux is
// unconfigured), and reloads to the materialized default.
func TestTmuxFreshConfigOmitsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := (&Config{}).Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "[tmux") {
		t.Errorf("fresh config should omit the [tmux] table, got:\n%s", data)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tmux != defaultTmux() {
		t.Errorf("reloaded tmux = %+v, want defaults %+v", got.Tmux, defaultTmux())
	}
}

// TmuxSocketName resolves to the configured socket, else the "lola" default,
// and never returns "".
func TestTmuxSocketNameResolver(t *testing.T) {
	c := &Config{}
	if got := c.TmuxSocketName(); got != DefaultTmuxSocketName {
		t.Errorf("zero config TmuxSocketName() = %q, want %q", got, DefaultTmuxSocketName)
	}
	c.Tmux.SocketName = "team-lola"
	if got := c.TmuxSocketName(); got != "team-lola" {
		t.Errorf("TmuxSocketName() = %q, want team-lola", got)
	}
}

// The detach-hint helper renders tmux's default two-key sequence when no custom
// key is bound, and the bound key itself when one is.
func TestTmuxDetachHint(t *testing.T) {
	if got := (TmuxConfig{}).DetachHint(); got != DefaultDetachHint {
		t.Errorf("default DetachHint() = %q, want %q", got, DefaultDetachHint)
	}
	if got := DefaultDetachHint; got != "Ctrl-b d" {
		t.Errorf("DefaultDetachHint = %q, want %q", got, "Ctrl-b d")
	}
	if got := (TmuxConfig{DetachKey: "F12"}).DetachHint(); got != "F12" {
		t.Errorf("custom DetachHint() = %q, want F12", got)
	}
}

// SessionChrome projects the resolved [tmux] config into a tmux.SessionChrome:
// the fixed brand, the caller's label, and the status/detach/mouse settings the
// tmux layer needs to dress the session.
func TestSessionChrome(t *testing.T) {
	c := &Config{}
	c.Tmux = TmuxConfig{
		SocketName:  "lola",
		DetachKey:   "F12",
		StatusRight: "#[fg=green]#S",
		Mouse:       true,
	}
	got := c.SessionChrome("ENG-123 fix login")
	want := tmux.SessionChrome{
		Brand:       "LOLA",
		Label:       "ENG-123 fix login",
		StatusRight: "#[fg=green]#S",
		DetachKey:   "F12",
		Mouse:       true,
	}
	if got != want {
		t.Errorf("SessionChrome = %+v, want %+v", got, want)
	}
	if got.Brand != TmuxBrand {
		t.Errorf("brand = %q, want %q", got.Brand, TmuxBrand)
	}

	// A zero config still brands the session and passes the label through; the
	// detach key stays empty (tmux renders its default hint).
	bare := (&Config{}).SessionChrome("")
	if bare.Brand != TmuxBrand || bare.Label != "" || bare.DetachKey != "" {
		t.Errorf("zero-config chrome = %+v, want brand-only", bare)
	}
}
