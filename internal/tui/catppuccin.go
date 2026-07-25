// A Go port of the desktop app's catppuccin.ts: the four Catppuccin flavor
// tables plus the pure color math that turns a flavor into lola's semantic
// design tokens. This is the SAME data and the SAME arithmetic the app runs, so
// picking a flavor paints the TUI and the desktop app identically instead of
// leaving the TUI on its old hand-tuned navy palette.
//
// Discipline mirrors the TS leaf: stdlib only, no lipgloss, no config import. It
// is pure data + pure functions so it unit-tests trivially, and theme.go (which
// DOES touch lipgloss and config) is the only place that consumes it. The token
// NAMES here are Catppuccin/CSS names ("--color-*"); theme.go maps them onto the
// TUI's own palette variables.
//
// Only the 26 named colors + dark + faintKey are ported: the app's terminal
// (ansi16/cursor/selection) fields drive xterm.js, which the TUI does not have —
// it attaches real tmux — so they are deliberately omitted here.
//
// Flavor ids match config.UIThemes exactly; catppuccin_test.go pins that so a
// drift here (which would make the TUI ignore a valid [ui].theme) is caught.
package tui

import "math"

// defaultThemeID mirrors config.DefaultUITheme. It lives here as a plain literal
// (not an import) to keep this file free of the config dependency; theme.go
// resolves the real config.DefaultUITheme and passes it in.
const defaultThemeID = "catppuccin-mocha"

// flavor is one Catppuccin flavor: the 26 named colors plus the two facts the
// token math needs beyond them — whether it is light (only latte) and which
// name backs --color-faint.
type flavor struct {
	id    string
	label string
	dark  bool // false only for latte — drives every light/dark decision

	rosewater, flamingo, pink, mauve, red, maroon, peach, yellow string
	green, teal, sky, sapphire, blue, lavender                   string
	text, subtext1, subtext0                                     string
	overlay2, overlay1, overlay0                                 string
	surface2, surface1, surface0                                 string
	base, mantle, crust                                          string

	// faintKey names the color backing --color-faint: overlay1 everywhere, but
	// overlay2 on latte because latte's overlay1 (#8c8fa1) on base (#eff1f5) is
	// only ~2.8:1 and the token carries real label text. Data, not an if-latte.
	faintKey string
}

// faint resolves faintKey to its color. Only overlay1/overlay2 ever appear, so
// this is the whole of the TS `f[f.faintKey]` indexing.
func (f flavor) faint() string {
	if f.faintKey == "overlay2" {
		return f.overlay2
	}
	return f.overlay1
}

var mocha = flavor{
	id: "catppuccin-mocha", label: "Mocha", dark: true,
	rosewater: "#f5e0dc", flamingo: "#f2cdcd", pink: "#f5c2e7", mauve: "#cba6f7",
	red: "#f38ba8", maroon: "#eba0ac", peach: "#fab387", yellow: "#f9e2af",
	green: "#a6e3a1", teal: "#94e2d5", sky: "#89dceb", sapphire: "#74c7ec",
	blue: "#89b4fa", lavender: "#b4befe",
	text: "#cdd6f4", subtext1: "#bac2de", subtext0: "#a6adc8",
	overlay2: "#9399b2", overlay1: "#7f849c", overlay0: "#6c7086",
	surface2: "#585b70", surface1: "#45475a", surface0: "#313244",
	base: "#1e1e2e", mantle: "#181825", crust: "#11111b",
	faintKey: "overlay1",
}

var macchiato = flavor{
	id: "catppuccin-macchiato", label: "Macchiato", dark: true,
	rosewater: "#f4dbd6", flamingo: "#f0c6c6", pink: "#f5bde6", mauve: "#c6a0f6",
	red: "#ed8796", maroon: "#ee99a0", peach: "#f5a97f", yellow: "#eed49f",
	green: "#a6da95", teal: "#8bd5ca", sky: "#91d7e3", sapphire: "#7dc4e4",
	blue: "#8aadf4", lavender: "#b7bdf8",
	text: "#cad3f5", subtext1: "#b8c0e0", subtext0: "#a5adcb",
	overlay2: "#939ab7", overlay1: "#8087a2", overlay0: "#6e738d",
	surface2: "#5b6078", surface1: "#494d64", surface0: "#363a4f",
	base: "#24273a", mantle: "#1e2030", crust: "#181926",
	faintKey: "overlay1",
}

var frappe = flavor{
	id: "catppuccin-frappe", label: "Frappé", dark: true,
	rosewater: "#f2d5cf", flamingo: "#eebebe", pink: "#f4b8e4", mauve: "#ca9ee6",
	red: "#e78284", maroon: "#ea999c", peach: "#ef9f76", yellow: "#e5c890",
	green: "#a6d189", teal: "#81c8be", sky: "#99d1db", sapphire: "#85c1dc",
	blue: "#8caaee", lavender: "#babbf1",
	text: "#c6d0f5", subtext1: "#b5bfe2", subtext0: "#a5adce",
	overlay2: "#949cbb", overlay1: "#838ba7", overlay0: "#737994",
	surface2: "#626880", surface1: "#51576d", surface0: "#414559",
	base: "#303446", mantle: "#292c3c", crust: "#232634",
	faintKey: "overlay1",
}

var latte = flavor{
	id: "catppuccin-latte", label: "Latte", dark: false,
	rosewater: "#dc8a78", flamingo: "#dd7878", pink: "#ea76cb", mauve: "#8839ef",
	red: "#d20f39", maroon: "#e64553", peach: "#fe640b", yellow: "#df8e1d",
	green: "#40a02b", teal: "#179299", sky: "#04a5e5", sapphire: "#209fb5",
	blue: "#1e66f5", lavender: "#7287fd",
	text: "#4c4f69", subtext1: "#5c5f77", subtext0: "#6c6f85",
	overlay2: "#7c7f93", overlay1: "#8c8fa1", overlay0: "#9ca0b0",
	surface2: "#acb0be", surface1: "#bcc0cc", surface0: "#ccd0da",
	base: "#eff1f5", mantle: "#e6e9ef", crust: "#dce0e8",
	faintKey: "overlay2",
}

// themeIDs is the same order and spelling as config.UIThemes. flavors keys each
// by its own id.
var themeIDs = []string{
	"catppuccin-mocha",
	"catppuccin-macchiato",
	"catppuccin-frappe",
	"catppuccin-latte",
}

var flavors = map[string]flavor{
	mocha.id:     mocha,
	macchiato.id: macchiato,
	frappe.id:    frappe,
	latte.id:     latte,
}

// flavorFor resolves an id to a flavor, falling back to the default for anything
// unknown or empty — the same fail-soft as the TS flavorFor and config.UITheme.
func flavorFor(id string) flavor {
	if f, ok := flavors[id]; ok {
		return f
	}
	return flavors[defaultThemeID]
}

// --- small color math --------------------------------------------------------
// Ported verbatim from catppuccin.ts. Done in Go rather than any CSS/terminal
// facility for the same reason it was done in TS rather than CSS color-mix():
// the result has to be a literal hex a lipgloss.Color can take, and a pure
// function is deterministic and unit-testable.

// hexToRGB parses "#rrggbb" into its three channels.
func hexToRGB(h string) (r, g, b int) {
	if len(h) == 7 && h[0] == '#' {
		r = hexByte(h[1], h[2])
		g = hexByte(h[3], h[4])
		b = hexByte(h[5], h[6])
	}
	return r, g, b
}

func hexByte(hi, lo byte) int { return hexNibble(hi)*16 + hexNibble(lo) }

func hexNibble(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return 0
}

// rgbToHex clamps to [0,255], rounds, and formats "#rrggbb". Rounding is
// half-away-from-zero, which matches JS Math.round for the non-negative channel
// values every mix produces — so the walk is byte-reproducible against the app.
func rgbToHex(r, g, b float64) string {
	const digits = "0123456789abcdef"
	clamp := func(v float64) int {
		n := int(math.Round(math.Min(255, math.Max(0, v))))
		return n
	}
	out := []byte("#000000")
	for i, v := range []int{clamp(r), clamp(g), clamp(b)} {
		out[1+i*2] = digits[v>>4]
		out[2+i*2] = digits[v&0x0f]
	}
	return string(out)
}

// mix blends t parts of a into b (t=1 → a, t=0 → b): a simple sRGB lerp.
func mix(a, b string, t float64) string {
	ar, ag, ab := hexToRGB(a)
	br, bg, bb := hexToRGB(b)
	return rgbToHex(
		float64(ar)*t+float64(br)*(1-t),
		float64(ag)*t+float64(bg)*(1-t),
		float64(ab)*t+float64(bb)*(1-t),
	)
}

// luminance is WCAG relative luminance, used only to pick the darker of two
// candidates.
func luminance(color string) float64 {
	f := func(v int) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	r, g, b := hexToRGB(color)
	return 0.2126*f(r) + 0.7152*f(g) + 0.0722*f(b)
}

// contrast is the WCAG contrast ratio between two hex colors.
func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// Contrast floors. aaContrast is WCAG AA for body text; aaUI is 1.4.11 non-text
// contrast for a component boundary or focus ring; mutedFloor is the
// deliberately-below-AA floor for de-emphasized `faint` text (WCAG's large-text
// threshold — see the app's MUTED for why faint must stay a step under ink).
const (
	aaContrast = 4.5
	aaUI       = 3.0
	mutedFloor = 3.0
)

// onFill picks the text drawn on top of a fully saturated fill, by MEASURED
// contrast against that specific fill rather than a hardcoded end of the ramp —
// the one rule that stays correct across all four flavors (latte inverts which
// ramp end is dark, and even within latte the answer differs per fill). The
// ramp's own extremes win first, so a fill the palette can label stays labelled
// in the palette; where it cannot, the winner keeps walking PAST the ramp toward
// the absolute end it already points at, stopping at the first step that clears
// AA.
func onFill(f flavor, fill string) string {
	best := f.crust
	for _, c := range []string{f.base, f.text} {
		if contrast(c, fill) > contrast(best, fill) {
			best = c
		}
	}
	if contrast(best, fill) >= aaContrast {
		return best
	}
	end := "#ffffff"
	if luminance(best) < luminance(fill) {
		end = "#000000"
	}
	for step := 1; step <= 20; step++ {
		c := mix(end, best, float64(step)/20)
		if contrast(c, fill) >= aaContrast {
			return c
		}
	}
	return end
}

// backdrop is the end of the flavor's ramp furthest from `text` — crust on the
// dark flavors, base on latte — derived through onFill so the direction stays
// measured rather than branched on `dark`.
func backdrop(f flavor) string { return onFill(f, f.text) }

// panelMix mirrors @utility panel in app.css: color-mix(--color-panel 82%,
// --color-canvas). Any "is this legible on a panel" math has to use panelBg, the
// surface components actually sit on, not raw base.
const panelMix = 0.82

func panelBg(f flavor) string { return mix(f.base, f.mantle, panelMix) }

// walk pushes `color` toward black or white until it clears `floor` against
// every surface in `on`, keeping as much of the original as the ratio allows.
// The achromatic anchor (not the flavor's own slate `text`) is what preserves
// hue: an sRGB lerp toward black/white scales the channels without reordering
// them. Shared by readable (AA) and visible (aaUI).
func walk(color string, on []string, floor float64) string {
	worst := func(c string) float64 {
		m := math.Inf(1)
		for _, bg := range on {
			if v := contrast(c, bg); v < m {
				m = v
			}
		}
		return m
	}
	if worst(color) >= floor {
		return color
	}
	end := "#ffffff"
	if worst("#000000") > worst("#ffffff") {
		end = "#000000"
	}
	// 19/20 first keeps the most color; the first candidate clearing the floor
	// wins. Integer steps so the walk is bit-reproducible.
	for step := 19; step >= 1; step-- {
		c := mix(color, end, float64(step)/20)
		if worst(c) >= floor {
			return c
		}
	}
	return end
}

// readable returns `color` as TEXT on every surface in `on`, walked until it
// clears AA.
func readable(color string, on ...string) string { return walk(color, on, aaContrast) }

// visible returns `color` as a BORDER or focus ring on every surface in `on`,
// walked until it clears the non-text floor (aaUI). Separate from readable only
// in the floor.
func visible(color string, on ...string) string { return walk(color, on, aaUI) }

// muted returns `color` as DE-EMPHASIZED text on every surface in `on`, walked
// until it clears mutedFloor — the `faint` token's floor.
func muted(color string, on ...string) string { return walk(color, on, mutedFloor) }

// pillTintMix is how much accent to blend into the flavor's BACKGROUND end for
// the tinted pills (work/done): low enough they still read as surfaces, high
// enough to carry hue. mantle is the substrate Catppuccin's accents are designed
// to be legible against in every flavor.
const pillTintMix = 0.28

// accentFill is the alpha of the accent in the primary-button fill; its hover
// twin re-mixes the same tint into backdrop() so hovering moves the fill AWAY
// from its own text.
const accentFill = 0.2

// toTokens maps every lola design token onto a Catppuccin-derived value — the
// same table catppuccin.ts emits, with the SAME contrast walks, so the compiled
// values match the app. theme.go renames these "--color-*" keys onto the TUI's
// palette variables. The structural mapping (canvas→mantle, panel→base,
// sel→surface0, edge→surface1) holds in all four flavors because latte preserves
// the ordinal meaning of the surface ramp even though it inverts its luminance.
func toTokens(f flavor) map[string]string {
	surface := panelBg(f)
	// The three surfaces bare text lands on; every semantic token must clear all
	// three (unselected row on the panel, selected on `sel`, shell around both is
	// canvas).
	bare := []string{f.mantle, surface, f.surface0}
	accent := visible(f.sky, bare...)
	bad := readable(f.red, bare...)
	faint := muted(f.faint(), bare...)
	fill := mix(accent, f.mantle, accentFill)
	fillHover := mix(accent, backdrop(f), accentFill)
	work := mix(f.blue, f.mantle, pillTintMix)
	done := mix(f.green, f.mantle, pillTintMix)

	accentInk := readable(accent, append([]string{fill, fillHover}, bare...)...)

	return map[string]string{
		"--color-canvas":            f.mantle,
		"--color-panel":             f.base,
		"--color-edge":              f.surface1,
		"--color-accent":            accent,
		"--color-accent-ink":        accentInk,
		"--color-accent-fill":       fill,
		"--color-accent-fill-hover": fillHover,
		"--color-on-accent":         onFill(f, accent),
		"--color-ink":               f.text,
		"--color-faint":             faint,
		"--color-placeholder":       readable(faint, f.mantle),
		"--color-sel":               f.surface0,

		"--color-good":    readable(f.green, bare...),
		"--color-bad":     bad,
		"--color-on-bad":  onFill(f, bad),
		"--color-warn":    readable(f.yellow, bare...),
		"--color-info":    readable(f.blue, bare...),
		"--color-orange":  readable(f.peach, bare...),
		"--color-magenta": readable(f.mauve, bare...),

		"--color-pill-urgent":    f.peach,
		"--color-pill-urgent-fg": onFill(f, f.peach),
		"--color-pill-broken":    f.red,
		"--color-pill-broken-fg": onFill(f, f.red),
		"--color-pill-work":      work,
		"--color-pill-work-fg":   readable(f.blue, work),
		"--color-pill-done":      done,
		"--color-pill-done-fg":   readable(f.green, done),
		"--color-pill-grey":      f.surface0,
		"--color-pill-grey-fg":   readable(f.subtext0, f.surface0),
	}
}
