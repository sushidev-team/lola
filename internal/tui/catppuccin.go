// A Go port of the desktop app's catppuccin.ts: the coordinated flavor
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
const defaultThemeID = "lola-midnight"

// flavor is one Catppuccin flavor: the 26 named colors plus the two facts the
// token math needs beyond them — whether it is light (only latte) and which
// name backs --color-faint.
type flavor struct {
	id    string
	label string
	dark  bool // false for light palettes — drives every light/dark decision

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
	rosewater: "#f5e0dc", flamingo: "#f2cdcd", pink: "#efaee0", mauve: "#c39af5",
	red: "#f183a0", maroon: "#ec94ac", peach: "#f5ac78", yellow: "#efd28f",
	green: "#9bd991", teal: "#87d9ca", sky: "#83d6e8", sapphire: "#75bee6",
	blue: "#88aff5", lavender: "#b6bafa",
	text: "#e4e5f3", subtext1: "#cbd0e5", subtext0: "#b2b6cf",
	overlay2: "#a1a6c0", overlay1: "#9095af", overlay0: "#6c7086",
	surface2: "#585b70", surface1: "#45475a", surface0: "#313244",
	base: "#1e1e2e", mantle: "#181825", crust: "#11111b",
	faintKey: "overlay1",
}

var macchiato = flavor{
	id: "catppuccin-macchiato", label: "Macchiato", dark: true,
	rosewater: "#f4dbd6", flamingo: "#f0c6c6", pink: "#efa8df", mauve: "#bd96f4",
	red: "#f08095", maroon: "#f197aa", peach: "#f4a373", yellow: "#edcc86",
	green: "#a0d88a", teal: "#7fd5c5", sky: "#85d7e5", sapphire: "#71c2e5",
	blue: "#84adfa", lavender: "#b0bafa",
	text: "#e3e8f7", subtext1: "#cbd3eb", subtext0: "#afb9d5",
	overlay2: "#a3adca", overlay1: "#959fbd", overlay0: "#6e738d",
	surface2: "#5b6078", surface1: "#494d64", surface0: "#363a4f",
	base: "#24273a", mantle: "#1e2030", crust: "#181926",
	faintKey: "overlay1",
}

var frappe = flavor{
	id: "catppuccin-frappe", label: "Frappé", dark: true,
	rosewater: "#f2d5cf", flamingo: "#eebebe", pink: "#edafe0", mauve: "#c5a0ef",
	red: "#ef919b", maroon: "#eea0b0", peach: "#f1ad82", yellow: "#e9ce91",
	green: "#a8d99a", teal: "#8dd6ca", sky: "#95d9e5", sapphire: "#88c9e8",
	blue: "#9abafa", lavender: "#bfc5fb",
	text: "#e6eaf7", subtext1: "#d0d7ec", subtext0: "#bcc5df",
	overlay2: "#aeb9d2", overlay1: "#a0abc7", overlay0: "#737994",
	surface2: "#5e6883", surface1: "#4b526b", surface0: "#3b4055",
	base: "#303446", mantle: "#292c3c", crust: "#232634",
	faintKey: "overlay1",
}

var latte = flavor{
	id: "catppuccin-latte", label: "Latte", dark: false,
	rosewater: "#bf7064", flamingo: "#cb6578", pink: "#b747a0", mauve: "#8554cd",
	red: "#bd3158", maroon: "#c8506a", peach: "#bd551f", yellow: "#a66b12",
	green: "#177e4d", teal: "#168477", sky: "#008ca8", sapphire: "#167fba",
	blue: "#326bd6", lavender: "#7264d4",
	text: "#30374f", subtext1: "#48546e", subtext0: "#586580",
	overlay2: "#66738e", overlay1: "#7d89a2", overlay0: "#94a0b8",
	surface2: "#aebad0", surface1: "#c9d1e2", surface0: "#e0e5f0",
	base: "#f7f8fc", mantle: "#edf0f7", crust: "#e2e7f0",
	faintKey: "overlay2",
}

// themeIDs is the same order and spelling as config.UIThemes. flavors keys each
// by its own id.
var midnight = flavor{
	id: "lola-midnight", label: "Midnight", dark: true,
	rosewater: "#f5ddd8",
	flamingo:  "#f3b9b4",
	pink:      "#efa0e2",
	mauve:     "#b986ff",
	red:       "#ff738f",
	maroon:    "#f991aa",
	peach:     "#ffa45c",
	yellow:    "#efcf7b",
	green:     "#8ce477",
	teal:      "#79dccc",
	sky:       "#7fddea",
	sapphire:  "#67bfe8",
	blue:      "#82abff",
	lavender:  "#b2baff",
	text:      "#e6e9f5",
	subtext1:  "#c7cde3",
	subtext0:  "#adb7d1",
	overlay2:  "#a1abc5",
	overlay1:  "#929db9",
	overlay0:  "#707b96",
	surface2:  "#48516b",
	surface1:  "#333c55",
	surface0:  "#252c43",
	base:      "#191d2f",
	mantle:    "#121624",
	crust:     "#0c0f1a",
	faintKey:  "overlay1",
}

var githubLight = flavor{
	id: "github-light", label: "GitHub Light", dark: false,
	rosewater: "#bf7064",
	flamingo:  "#d1577a",
	pink:      "#bf3989",
	mauve:     "#8250df",
	red:       "#cf222e",
	maroon:    "#a40e26",
	peach:     "#bc4c00",
	yellow:    "#9a6700",
	green:     "#1a7f37",
	teal:      "#1b7c83",
	sky:       "#0969da",
	sapphire:  "#0550ae",
	blue:      "#0969da",
	lavender:  "#6639ba",
	text:      "#24292f",
	subtext1:  "#424a53",
	subtext0:  "#57606a",
	overlay2:  "#68717c",
	overlay1:  "#818b98",
	overlay0:  "#9aa4b0",
	surface2:  "#afb8c1",
	surface1:  "#d0d7de",
	surface0:  "#eaeef2",
	base:      "#ffffff",
	mantle:    "#f6f8fa",
	crust:     "#eaeef2",
	faintKey:  "overlay2",
}

var solarizedLight = flavor{
	id: "solarized-light", label: "Solarized Light", dark: false,
	rosewater: "#b77b64",
	flamingo:  "#cb786a",
	pink:      "#d33682",
	mauve:     "#6c71c4",
	red:       "#dc322f",
	maroon:    "#c75a54",
	peach:     "#cb4b16",
	yellow:    "#b58900",
	green:     "#718600",
	teal:      "#2aa198",
	sky:       "#268bd2",
	sapphire:  "#2176a9",
	blue:      "#268bd2",
	lavender:  "#7778b4",
	text:      "#3c5158",
	subtext1:  "#52666d",
	subtext0:  "#657b83",
	overlay2:  "#70848a",
	overlay1:  "#839496",
	overlay0:  "#93a1a1",
	surface2:  "#b5bcb4",
	surface1:  "#d6d5c4",
	surface0:  "#e5e1cf",
	base:      "#fdf6e3",
	mantle:    "#eee8d5",
	crust:     "#e4ddc7",
	faintKey:  "overlay2",
}

var rosePineDawn = flavor{
	id: "rose-pine-dawn", label: "Rosé Pine Dawn", dark: false,
	rosewater: "#b78280",
	flamingo:  "#d1958b",
	pink:      "#b4637a",
	mauve:     "#907aa9",
	red:       "#b4637a",
	maroon:    "#c47585",
	peach:     "#d7827e",
	yellow:    "#a87319",
	green:     "#56836b",
	teal:      "#56949f",
	sky:       "#286983",
	sapphire:  "#437d91",
	blue:      "#286983",
	lavender:  "#8b78a0",
	text:      "#4b485f",
	subtext1:  "#615d76",
	subtext0:  "#706b84",
	overlay2:  "#79738c",
	overlay1:  "#918b9f",
	overlay0:  "#a69eaa",
	surface2:  "#c8bdc4",
	surface1:  "#dfcfd1",
	surface0:  "#f2e6e2",
	base:      "#fffaf3",
	mantle:    "#faf4ed",
	crust:     "#f2e9e1",
	faintKey:  "overlay2",
}

var gruvboxLight = flavor{
	id: "gruvbox-light", label: "Gruvbox Light", dark: false,
	rosewater: "#b57654",
	flamingo:  "#af6754",
	pink:      "#b16286",
	mauve:     "#8f3f71",
	red:       "#9d0006",
	maroon:    "#cc241d",
	peach:     "#af3a03",
	yellow:    "#b57614",
	green:     "#79740e",
	teal:      "#427b58",
	sky:       "#076678",
	sapphire:  "#458588",
	blue:      "#076678",
	lavender:  "#8f6a88",
	text:      "#3c3836",
	subtext1:  "#504945",
	subtext0:  "#665c54",
	overlay2:  "#75695e",
	overlay1:  "#928374",
	overlay0:  "#a89984",
	surface2:  "#bdae93",
	surface1:  "#d5c4a1",
	surface0:  "#ebdbb2",
	base:      "#fbf1c7",
	mantle:    "#f2e5bc",
	crust:     "#ebd9ac",
	faintKey:  "overlay2",
}

var oneLight = flavor{
	id: "one-light", label: "One Light", dark: false,
	rosewater: "#ca7860",
	flamingo:  "#e45649",
	pink:      "#d52b93",
	mauve:     "#a626a4",
	red:       "#e02f44",
	maroon:    "#c93569",
	peach:     "#d75f00",
	yellow:    "#b87500",
	green:     "#268b3a",
	teal:      "#008c86",
	sky:       "#2864f0",
	sapphire:  "#087bb5",
	blue:      "#2864f0",
	lavender:  "#7c42dc",
	text:      "#282c34",
	subtext1:  "#434a59",
	subtext0:  "#586174",
	overlay2:  "#687287",
	overlay1:  "#818ba0",
	overlay0:  "#9aa4b8",
	surface2:  "#b5bfd4",
	surface1:  "#d1d9eb",
	surface0:  "#e5eafb",
	base:      "#fafbff",
	mantle:    "#f1f4fb",
	crust:     "#e5eaf5",
	faintKey:  "overlay2",
}

var themeIDs = []string{
	"lola-midnight",
	"catppuccin-mocha",
	"catppuccin-macchiato",
	"catppuccin-frappe",
	"catppuccin-latte",
	"github-light",
	"solarized-light",
	"rose-pine-dawn",
	"gruvbox-light",
	"one-light",
}

var flavors = map[string]flavor{
	midnight.id:       midnight,
	mocha.id:          mocha,
	macchiato.id:      macchiato,
	frappe.id:         frappe,
	latte.id:          latte,
	githubLight.id:    githubLight,
	solarizedLight.id: solarizedLight,
	rosePineDawn.id:   rosePineDawn,
	gruvboxLight.id:   gruvboxLight,
	oneLight.id:       oneLight,
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

// onFill uses the most legible palette end first. When it misses AA, walk
// toward whichever absolute endpoint has higher contrast against the fill.
// Choosing that endpoint independently avoids a mid-luminance dead end.
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
	if contrast("#000000", fill) > contrast("#ffffff", fill) {
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
// sel→surface0, edge→surface1) holds in all flavors because latte preserves
// the ordinal meaning of the surface ramp even though it inverts its luminance.
func toTokens(f flavor) map[string]string {
	surface := panelBg(f)
	// The three surfaces bare text lands on; every semantic token must clear all
	// three (unselected row on the panel, selected on `sel`, shell around both is
	// canvas).
	bare := []string{f.mantle, surface, f.surface0}
	accent := visible(f.sky, bare...)
	bad := readable(f.red, bare...)
	// Latte alone also walks to AA on canvas and panel, because 3:1 grey on its
	// near-white reads as washed out rather than as quiet. Same as the app.
	faintBase := f.faint()
	if !f.dark {
		faintBase = readable(faintBase, f.mantle, f.base)
	}
	faint := muted(faintBase, bare...)
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
		"--color-ink-2":             readable(f.subtext0, bare...),
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
