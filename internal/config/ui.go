package config

import (
	"fmt"
	"slices"
)

// The [ui] table is PRESENTATION-ONLY: no daemon behavior reads it. It lives in
// config.toml rather than a desktop-local preferences file so there is ONE
// source of truth for a lola install, and the TUI can adopt the same key later
// without a second store to keep in sync.
//
// Unlike [tmux]/[reactions]/[notify], the in-memory zero value is MEANINGFUL:
// Theme == "" means "unset — use DefaultUITheme", resolved at READ time by
// (*Config).UITheme(). That is deliberately the [defaults].agent /
// branch_prefix / concurrency_cap pattern (see AgentForProject), not the
// resolve-to-a-materialized-default pattern [tmux] uses, because it is the only
// shape in which Save can never freeze the default into the file: resolveUI
// leaves an absent table as the zero UIConfig, and uiFile then omits it.
//
// The on-disk mirror is still POINTER-based, matching the other optional
// sections, so an absent key stays distinguishable from an explicitly-written
// one and an operator's explicit value round-trips exactly.

// DefaultUITheme is the theme lola-desktop paints when [ui].theme is unset. It
// matches the palette the app's compiled stylesheet ships, so the default
// install has nothing to re-paint at startup.
const DefaultUITheme = "catppuccin-mocha"

// UIThemes are the ONLY theme identifiers Validate accepts, mirroring the
// PrioritySortKeys rule: a typo that silently rendered the default would be a
// worse failure than a startup error naming the valid values. The desktop
// enumerates this list over the bridge (ConfigService.Themes) rather than
// keeping its own copy that could drift and start writing configs the daemon
// rejects.
var UIThemes = []string{
	"catppuccin-latte",
	"catppuccin-frappe",
	"catppuccin-macchiato",
	"catppuccin-mocha",
}

// UIConfig is the [ui] table.
//
//   - Theme names the palette lola-desktop paints (app chrome, terminal, and
//     the ANSI 16 used to render pane snapshots). "" means DefaultUITheme.
type UIConfig struct {
	Theme string `toml:"theme"`
}

// UITheme returns the EFFECTIVE theme identifier: [ui].theme when set, else
// DefaultUITheme. It never returns "", so every consumer has one unambiguous
// value to apply and no caller has to repeat the fallback.
func (c *Config) UITheme() string {
	if c.UI.Theme != "" {
		return c.UI.Theme
	}
	return DefaultUITheme
}

// --- on-disk mirror --------------------------------------------------------

type fileUIConfig struct {
	Theme *string `toml:"theme,omitempty"`
}

// resolveUI materializes the [ui] table. An absent table yields the ZERO
// UIConfig — NOT a default-filled one, unlike resolveTmux — so Save can still
// tell "never configured" from "explicitly set", and a config that never
// mentioned [ui] does not grow the table on its next write. The default lives
// in UITheme() instead.
func resolveUI(fu *fileUIConfig) UIConfig {
	var u UIConfig
	if fu == nil {
		return u
	}
	if fu.Theme != nil {
		u.Theme = *fu.Theme
	}
	return u
}

// uiFile builds the on-disk mirror for Save. A zero (unconfigured) table
// returns nil so [ui] is omitted entirely; an explicitly-set theme is written
// even when it equals DefaultUITheme, because the operator named it and pinning
// it is both honest and exactly round-trippable.
func uiFile(u UIConfig) *fileUIConfig {
	if u == (UIConfig{}) {
		return nil
	}
	return &fileUIConfig{Theme: &u.Theme}
}

// validateUI checks [ui].theme against UIThemes. Empty is valid and means
// DefaultUITheme; anything else unknown is an ERROR rather than a silent
// fallback — same reasoning as priority_sort, where a value that quietly did
// nothing would leave the operator with no signal. Purely static: this is a
// string-set membership test, no exec, no stat, no network.
func (c *Config) validateUI() []error {
	if c.UI.Theme == "" || slices.Contains(UIThemes, c.UI.Theme) {
		return nil
	}
	return []error{fmt.Errorf("ui.theme must be one of %v (empty uses %q), got %q",
		UIThemes, DefaultUITheme, c.UI.Theme)}
}
