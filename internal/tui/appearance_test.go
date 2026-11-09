package tui

import (
	"image/color"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/config"
)

func TestSystemAppearanceFollowsTerminalBackground(t *testing.T) {
	t.Cleanup(func() { applyTheme(defaultThemeID) })
	m := &rootModel{cfg: &config.Config{UI: config.UIConfig{FollowSystem: true, LightTheme: "one-light", DarkTheme: "catppuccin-frappe"}}}
	for _, test := range []struct {
		background color.Color
		want       string
	}{
		{color.White, "one-light"}, {color.Black, "catppuccin-frappe"},
	} {
		m.Update(tea.BackgroundColorMsg{Color: test.background})
		if want := toTokens(flavorFor(test.want))["--color-canvas"]; colCanvas != want {
			t.Fatalf("canvas = %s, want %s for %s", colCanvas, want, test.want)
		}
	}
	m.cfg.UI.FollowSystem = false
	applyTheme("gruvbox-light")
	m.Update(tea.BackgroundColorMsg{Color: color.Black})
	if want := toTokens(flavorFor("gruvbox-light"))["--color-canvas"]; colCanvas != want {
		t.Fatalf("fixed theme changed: %s", colCanvas)
	}
}
