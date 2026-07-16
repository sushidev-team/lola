// The cockpit color theme: one cohesive, dark-canvas TRUECOLOR palette so lola
// renders identically on every terminal instead of inheriting each terminal's
// 16/256-color scheme (the reason the same frame looked muddy on one terminal
// and crisp on another — btop ships its own truecolor theme for exactly this).
// Every style in the TUI resolves back to a name here; nothing hardcodes an
// ANSI index anymore. The palette is tuned for a dark canvas (set on the View),
// so tint pills and faint text keep their contrast even on a light terminal.
package tui

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"
)

const (
	colCanvas = "#0e1420" // app background — deep navy-charcoal
	colBorder = "#2b3646" // muted slate panel border (unfocused)
	colAccent = "#57c7d6" // cyan — focus border, selection marker, links
	colText   = "#c3cbd6" // default foreground
	colFaint  = "#6b7686" // muted secondary text / rules / labels
	colSel    = "#1b2634" // selected-row band (a cool, subtle lift)

	colGood    = "#5fd08a" // green  — ok / approved / pass
	colBad     = "#e0716f" // red    — error / failed
	colWarn    = "#e0b44a" // amber  — pending / retry
	colBlue    = "#6ea8fe" // blue   — working
	colOrange  = "#eaa04a" // orange — needs-you hero
	colMagenta = "#c99bf0" // magenta — PR detail line

	// Status pills. Urgent + broken states get a SOLID fill (dark text) so the
	// human-in-the-loop queue leaps off the table; active/parked states get a
	// dark TINT (bright-enough text) so they read without shouting.
	pillUrgentBg = "#e0a54a" // solid amber — needs_input
	pillUrgentFg = "#17110a"
	pillBrokenBg = "#d1707a" // solid rose  — ci_failed / changes_requested / merge_conflict
	pillBrokenFg = "#180b0d"
	pillWorkBg   = "#22384f" // tint — working / ci_pending / draft
	pillWorkFg   = "#84b6ea"
	pillDoneBg   = "#1f3a2e" // tint — approved / pr_open
	pillDoneFg   = "#74cf97"
	pillGreyBg   = "#2a323d" // tint — review_pending
	pillGreyFg   = "#aab4c0"
)

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
