// The cockpit color theme: one cohesive TRUECOLOR palette so lola renders
// identically on every terminal instead of inheriting each terminal's 16/256-
// color scheme (the reason the same frame looked muddy on one terminal and crisp
// on another — btop ships its own truecolor theme for exactly this). Every style
// in the TUI resolves back to a name here; nothing hardcodes an ANSI index.
//
// The palette is DERIVED, not fixed: applyTheme paints it from the [ui].theme
// Catppuccin flavor (catppuccin.go) — the same flavor and the same token math
// the desktop app uses, so the two surfaces agree and picking the light flavor
// (latte) genuinely lightens the TUI. Contrast is not assumed: the token
// derivation walks each semantic color against the surfaces it lands on, which
// is what makes one table work for both a light and a dark canvas.
package tui

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"
)

// The palette is a var block, NOT const, so applyTheme can repaint it from the
// selected [ui].theme flavor at load/reload. It is SEEDED with the historical
// navy values so behaviour is byte-identical until applyTheme runs — which it
// always does on real startup (Run) and on every reloadConfig, so the live TUI
// paints the resolved Catppuccin flavor (mocha by default, matching the desktop
// app). Tests that never call applyTheme see these seeds.
var (
	colCanvas = "#0e1420" // app background — deep navy-charcoal
	colBorder = "#2b3646" // muted slate panel border (unfocused)
	colAccent = "#57c7d6" // cyan — focus border, selection marker, links
	colText   = "#c3cbd6" // default foreground
	colFaint  = "#6b7686" // muted secondary text / rules / labels
	colSel    = "#1b2634" // selected-row band (a cool, subtle lift)

	// colPanel is the raised-surface background (Catppuccin `base`), one step
	// ABOVE colCanvas. Carried so the token mapping is complete; the TUI does not
	// paint a distinct panel background today, so nothing renders it yet.
	colPanel = "#141b28"

	colGood    = "#5fd08a" // green  — ok / approved / pass
	colBad     = "#e0716f" // red    — error / failed
	colWarn    = "#e0b44a" // amber  — pending / retry
	colBlue    = "#6ea8fe" // blue   — working
	colOrange  = "#eaa04a" // orange — needs-you hero
	colMagenta = "#c99bf0" // magenta — PR detail line

	// Status pills. Urgent + broken states get a SOLID fill (dark text) so the
	// human-in-the-loop queue leaps off the table; active/parked states get a
	// dark TINT (bright-enough text) so they read without shouting.
	pillUrgentBg = "#e0a54a" // solid amber — needs_you (the one urgent pill)
	pillUrgentFg = "#17110a"
	pillBrokenBg = "#d1707a" // solid rose  — regressed delivery (kept for the desktop's mirrored tokens)
	pillBrokenFg = "#180b0d"
	pillWorkBg   = "#22384f" // tint — a live agent turn (working)
	pillWorkFg   = "#84b6ea"
	pillDoneBg   = "#1f3a2e" // tint — approved (kept for the desktop's mirrored tokens)
	pillDoneFg   = "#74cf97"
	pillGreyBg   = "#2a323d" // tint — a resting agent (idle)
	pillGreyFg   = "#aab4c0"
)

// init paints the seeded (navy) palette into every package-level derived style
// before any render, so a TUI that never reaches applyTheme (tests) still has
// fully-built styles. applyTheme reruns rebuildStyles after repainting the vars.
func init() { rebuildStyles() }

// applyTheme repaints the whole palette from the flavor named by id — the
// resolved [ui].theme — using the SAME token math the desktop app runs, then
// rebuilds every style derived from the palette. An unknown or empty id falls
// back to the default flavor (flavorFor), so applyTheme(cfg.UITheme()) is always
// safe and applyTheme("") yields the Mocha default. Called wherever the TUI
// loads or reloads config (Run, reloadConfig), so changing the theme in the
// settings form repaints without a restart.
func applyTheme(id string) {
	t := toTokens(flavorFor(id))

	colCanvas = t["--color-canvas"]
	colPanel = t["--color-panel"]
	colBorder = t["--color-edge"]
	colAccent = t["--color-accent"]
	colText = t["--color-ink"]
	colFaint = t["--color-faint"]
	colSel = t["--color-sel"]

	colGood = t["--color-good"]
	colBad = t["--color-bad"]
	colWarn = t["--color-warn"]
	colBlue = t["--color-info"]
	colOrange = t["--color-orange"]
	colMagenta = t["--color-magenta"]

	pillUrgentBg = t["--color-pill-urgent"]
	pillUrgentFg = t["--color-pill-urgent-fg"]
	pillBrokenBg = t["--color-pill-broken"]
	pillBrokenFg = t["--color-pill-broken-fg"]
	pillWorkBg = t["--color-pill-work"]
	pillWorkFg = t["--color-pill-work-fg"]
	pillDoneBg = t["--color-pill-done"]
	pillDoneFg = t["--color-pill-done-fg"]
	pillGreyBg = t["--color-pill-grey"]
	pillGreyFg = t["--color-pill-grey-fg"]

	rebuildStyles()
}

// rebuildStyles reconstructs every package-level lipgloss.Style built from the
// palette. A `var` reassignment in applyTheme does NOT update a style captured
// at init, so each file that owns palette-derived styles exposes a rebuildX
// helper and this is the one place that calls them all. Keep the style var names
// identical to their old initializers so no call site changes.
func rebuildStyles() {
	rebuildClientStyles()
	rebuildSessionStyles()
	rebuildCockpitStyles()
	rebuildPanelStyles()
}

// bgSGR returns the raw truecolor "set background" escape for a #rrggbb string.
// Used by highlightRow, which composites a background behind an already-styled
// row and so must re-emit the exact SGR after every inner reset — a lipgloss
// style can't express "keep this bg across a child's reset".
func bgSGR(hex string) string {
	r, g, b := hexRGB(hex)
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

// hexRGB parses "#rrggbb" into its components. A malformed string yields black
// rather than an error — the palette is a set of compile-time literals, so a
// bad value is a typo caught in review, not a runtime condition to handle.
func hexRGB(hex string) (r, g, b int) {
	if len(hex) == 7 && hex[0] == '#' {
		fmt.Sscanf(hex[1:], "%02x%02x%02x", &r, &g, &b)
	}
	return r, g, b
}

// canvasColor is the View background (bubbletea v2 paints the alt-screen with
// it) so the frame is one opaque, deliberate surface rather than whatever the
// terminal's default background happens to be.
func canvasColor() color.Color { return lipgloss.Color(colCanvas) }
