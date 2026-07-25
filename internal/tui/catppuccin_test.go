package tui

import (
	"math"
	"regexp"
	"slices"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/config"
)

var hexRE = regexp.MustCompile(`^#[0-9a-f]{6}$`)

func allFlavors() []flavor { return []flavor{mocha, macchiato, frappe, latte} }

// --- flavor tables + lookup -------------------------------------------------

func TestThemeIDsMatchConfig(t *testing.T) {
	// A drift here writes a config.toml the daemon validates against a different
	// list — the exact failure the TS side pins too. Order differs by design
	// (config.UIThemes is a sorted membership list), so compare as sets.
	got := append([]string(nil), themeIDs...)
	want := append([]string(nil), config.UIThemes...)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("themeIDs %v != config.UIThemes %v (as sets)", themeIDs, config.UIThemes)
	}
	if defaultThemeID != config.DefaultUITheme {
		t.Fatalf("defaultThemeID %q != config.DefaultUITheme %q", defaultThemeID, config.DefaultUITheme)
	}
}

func TestFlavorKeyedByOwnID(t *testing.T) {
	for id, f := range flavors {
		if f.id != id {
			t.Errorf("flavors[%q].id = %q", id, f.id)
		}
	}
	for _, id := range themeIDs {
		if _, ok := flavors[id]; !ok {
			t.Errorf("themeIDs has %q but flavors does not", id)
		}
	}
}

func TestOnlyLatteIsLight(t *testing.T) {
	for _, f := range allFlavors() {
		if want := f.id != "catppuccin-latte"; f.dark != want {
			t.Errorf("%s.dark = %v, want %v", f.id, f.dark, want)
		}
	}
}

func TestSurfaceRampOrdered(t *testing.T) {
	// crust < mantle < base in every flavor (so canvas always sits below panel),
	// and surface0..2 step AWAY from base — the invariant that keeps the token
	// mapping structural across latte's luminance inversion.
	for _, f := range allFlavors() {
		if !(luminance(f.crust) < luminance(f.mantle) && luminance(f.mantle) < luminance(f.base)) {
			t.Errorf("%s: crust<mantle<base violated", f.id)
		}
		step := 1.0
		if !f.dark {
			step = -1.0
		}
		ramp := []string{f.base, f.surface0, f.surface1, f.surface2}
		for i := 1; i < len(ramp); i++ {
			if math.Copysign(1, luminance(ramp[i])-luminance(ramp[i-1])) != step {
				t.Errorf("%s: surface step %d wrong direction", f.id, i)
			}
		}
	}
}

func TestFlavorFor(t *testing.T) {
	if flavorFor("catppuccin-latte").id != "catppuccin-latte" {
		t.Error("flavorFor did not resolve a known id")
	}
	for _, bad := range []string{"", "dracula", "catppuccin-oat"} {
		if flavorFor(bad).id != defaultThemeID {
			t.Errorf("flavorFor(%q) did not fall back to default", bad)
		}
	}
}

// --- color math -------------------------------------------------------------

func TestMixEndpoints(t *testing.T) {
	if got := mix("#000000", "#ffffff", 1); got != "#000000" {
		t.Errorf("mix t=1 = %q", got)
	}
	if got := mix("#000000", "#ffffff", 0); got != "#ffffff" {
		t.Errorf("mix t=0 = %q", got)
	}
	if got := mix("#000000", "#ffffff", 0.5); got != "#808080" {
		t.Errorf("mix t=0.5 = %q", got)
	}
	// Deterministic.
	if mix("#89b4fa", "#313244", 0.28) != mix("#89b4fa", "#313244", 0.28) {
		t.Error("mix is not deterministic")
	}
}

func TestLuminanceAndContrast(t *testing.T) {
	if math.Abs(luminance("#ffffff")-1) > 1e-5 {
		t.Errorf("luminance white = %v", luminance("#ffffff"))
	}
	if math.Abs(luminance("#000000")) > 1e-5 {
		t.Errorf("luminance black = %v", luminance("#000000"))
	}
	if math.Abs(contrast("#ffffff", "#000000")-21) > 1e-5 {
		t.Errorf("contrast white/black = %v", contrast("#ffffff", "#000000"))
	}
	// Symmetric regardless of argument order.
	if contrast("#89dceb", "#181825") != contrast("#181825", "#89dceb") {
		t.Error("contrast is not symmetric")
	}
}

func TestPanelBg(t *testing.T) {
	for _, f := range allFlavors() {
		if got := panelBg(f); got != mix(f.base, f.mantle, 0.82) {
			t.Errorf("%s panelBg = %q", f.id, got)
		}
	}
}

func TestOnFill(t *testing.T) {
	// Clears AA on every alarm fill, in every flavor (no light-flavor carve-out).
	for _, f := range allFlavors() {
		for _, fill := range []string{f.peach, f.red, f.sky} {
			if c := contrast(onFill(f, fill), fill); c < aaContrast {
				t.Errorf("%s onFill(%s) = %.2f < AA", f.id, fill, c)
			}
		}
	}
	// Dark flavors stay inside the ramp; latte spends palette only where it must.
	for _, f := range []flavor{mocha, macchiato, frappe} {
		for _, fill := range []string{f.peach, f.red, f.sky, f.green, f.blue} {
			got := onFill(f, fill)
			if got != f.crust && got != f.base && got != f.text {
				t.Errorf("%s onFill(%s) = %q left the ramp", f.id, fill, got)
			}
		}
	}
	if got := onFill(mocha, mocha.peach); got != mocha.crust {
		t.Errorf("mocha onFill(peach) = %q, want crust", got)
	}
	if got := onFill(latte, latte.red); got != latte.base {
		t.Errorf("latte onFill(red) = %q, want base", got)
	}
	// The minimum-spend walk: latte's rescued labels are darkened text, not raw
	// black.
	for _, fill := range []string{latte.peach, latte.sky} {
		got := onFill(latte, fill)
		if got == "#000000" {
			t.Errorf("latte onFill(%s) collapsed to black", fill)
		}
		if contrast(got, fill) < aaContrast {
			t.Errorf("latte onFill(%s) below AA", fill)
		}
		if contrast(got, fill) >= contrast("#000000", fill) {
			t.Errorf("latte onFill(%s) spent more than black", fill)
		}
	}
}

// channelOrder captures whether r>=g, g>=b, r>=b — the property an achromatic
// walk preserves and a walk toward slate `text` does not.
func channelOrder(c string) [3]bool {
	r, g, b := hexToRGB(c)
	return [3]bool{r >= g, g >= b, r >= b}
}

func TestReadable(t *testing.T) {
	// Untouched when already clearing AA.
	if got := readable(mocha.sky, mocha.crust); got != mocha.sky {
		t.Errorf("readable(sky, crust) = %q, want unchanged", got)
	}
	// Keeps the most color that still clears the floor (a blend, not a collapse).
	bg := mix(latte.green, latte.mantle, 0.28)
	got := readable(latte.green, bg)
	if contrast(got, bg) < aaContrast {
		t.Errorf("readable(green) below AA on %q", bg)
	}
	if got == latte.green || got == latte.text {
		t.Errorf("readable(green) = %q, expected a blend", got)
	}
	// Never reorders channels, so a walked color keeps its hue.
	for _, f := range allFlavors() {
		bgs := []string{f.mantle, panelBg(f), f.surface0}
		for _, src := range []string{f.green, f.yellow, f.peach, f.red, f.blue, f.mauve, f.sky} {
			if channelOrder(readable(src, bgs...)) != channelOrder(src) {
				t.Errorf("%s readable(%s) reordered channels", f.id, src)
			}
			if channelOrder(visible(src, bgs...)) != channelOrder(src) {
				t.Errorf("%s visible(%s) reordered channels", f.id, src)
			}
		}
	}
	// Satisfies every background it is given, not just the first.
	for _, f := range allFlavors() {
		bgs := []string{f.surface0, panelBg(f), f.mantle}
		g := readable(f.sky, bgs...)
		for _, b := range bgs {
			if contrast(g, b) < aaContrast {
				t.Errorf("%s readable(sky) on %q below AA", f.id, b)
			}
		}
	}
}

func TestVisibleSpendsLessThanReadable(t *testing.T) {
	bgs := []string{latte.mantle, panelBg(latte), latte.surface0}
	border := visible(latte.sky, bgs...)
	text := readable(latte.sky, bgs...)
	if border == text {
		t.Error("visible and readable coincided on latte (they should differ)")
	}
	if contrast(border, panelBg(latte)) >= contrast(text, panelBg(latte)) {
		t.Error("visible did not spend less than readable")
	}
	// Clears the non-text floor everywhere.
	for _, f := range allFlavors() {
		bgs := []string{f.mantle, panelBg(f), f.surface0}
		g := visible(f.sky, bgs...)
		for _, b := range bgs {
			if contrast(g, b) < aaUI {
				t.Errorf("%s visible(sky) on %q below aaUI", f.id, b)
			}
		}
	}
}

// --- token table ------------------------------------------------------------

func TestToTokensMochaValues(t *testing.T) {
	tok := toTokens(mocha)
	want := map[string]string{
		"--color-canvas": "#181825", // mantle
		"--color-panel":  "#1e1e2e", // base
		"--color-edge":   "#45475a", // surface1
		"--color-accent": "#89dceb", // sky — the old cyan's nearest hue
		"--color-ink":    "#cdd6f4", // text
		"--color-good":   "#a6e3a1", // green
		"--color-bad":    "#f38ba8", // red
		"--color-warn":   "#f9e2af", // yellow
	}
	for k, v := range want {
		if tok[k] != v {
			t.Errorf("mocha %s = %q, want %q", k, tok[k], v)
		}
	}
}

func TestToTokensValidHexAndStable(t *testing.T) {
	var names []string
	for k := range toTokens(mocha) {
		names = append(names, k)
	}
	slices.Sort(names)
	for _, f := range allFlavors() {
		tok := toTokens(f)
		var got []string
		for k, v := range tok {
			got = append(got, k)
			if !hexRE.MatchString(v) {
				t.Errorf("%s %s = %q is not lowercase hex", f.id, k, v)
			}
		}
		slices.Sort(got)
		if !slices.Equal(got, names) {
			t.Errorf("%s token set differs from mocha", f.id)
		}
	}
}

func TestFaintFloorAndKey(t *testing.T) {
	// faintKey selects the raw name; --color-faint is that name run through muted.
	if latte.faintKey != "overlay2" {
		t.Errorf("latte.faintKey = %q, want overlay2", latte.faintKey)
	}
	if got := toTokens(latte)["--color-faint"]; got != muted(latte.overlay2, latte.mantle, panelBg(latte), latte.surface0) {
		t.Errorf("latte faint = %q", got)
	}
	// Mocha & macchiato already clear 3:1 raw, so faint is byte-identical there.
	for _, f := range []flavor{mocha, macchiato} {
		if got := toTokens(f)["--color-faint"]; got != f.faint() {
			t.Errorf("%s faint moved from raw %q to %q", f.id, f.faint(), got)
		}
	}
	// The 3:1 floor holds on every surface, and faint stays well below ink.
	for _, f := range allFlavors() {
		tok := toTokens(f)
		for _, bg := range []string{tok["--color-canvas"], panelBg(f), tok["--color-sel"]} {
			if c := contrast(tok["--color-faint"], bg); c < mutedFloor {
				t.Errorf("%s faint on %q = %.2f < 3", f.id, bg, c)
			}
		}
		panel := panelBg(f)
		if contrast(tok["--color-faint"], panel) >= contrast(tok["--color-ink"], panel)*0.75 {
			t.Errorf("%s faint not a clear step below ink", f.id)
		}
	}
}

func TestSemanticTokensAA(t *testing.T) {
	semantic := []string{"good", "bad", "warn", "info", "orange", "magenta"}
	for _, f := range allFlavors() {
		tok := toTokens(f)
		surfaces := []string{tok["--color-canvas"], panelBg(f), tok["--color-sel"]}
		for _, name := range semantic {
			for _, bg := range surfaces {
				if c := contrast(tok["--color-"+name], bg); c < aaContrast {
					t.Errorf("%s --color-%s on %q = %.2f < AA", f.id, name, bg, c)
				}
			}
		}
	}
}

func TestSemanticTokensMovedSets(t *testing.T) {
	// A latte-shaped fix, not a redesign: mocha/macchiato come back verbatim,
	// frappé moves exactly the four that miss on the selected band, latte all six.
	raw := map[string]string{
		"good": "green", "bad": "red", "warn": "yellow",
		"info": "blue", "orange": "peach", "magenta": "mauve",
	}
	named := func(f flavor, key string) string {
		switch key {
		case "green":
			return f.green
		case "red":
			return f.red
		case "yellow":
			return f.yellow
		case "blue":
			return f.blue
		case "peach":
			return f.peach
		case "mauve":
			return f.mauve
		}
		return ""
	}
	moved := func(f flavor) []string {
		tok := toTokens(f)
		var out []string
		for _, name := range []string{"good", "bad", "warn", "info", "orange", "magenta"} {
			if tok["--color-"+name] != named(f, raw[name]) {
				out = append(out, name)
			}
		}
		return out
	}
	check := func(f flavor, want []string) {
		if got := moved(f); !slices.Equal(got, want) {
			t.Errorf("%s moved = %v, want %v", f.id, got, want)
		}
	}
	check(mocha, nil)
	check(macchiato, nil)
	check(frappe, []string{"bad", "info", "orange", "magenta"})
	check(latte, []string{"good", "bad", "warn", "info", "orange", "magenta"})
}

func TestAccentInk(t *testing.T) {
	for _, f := range allFlavors() {
		tok := toTokens(f)
		ink := tok["--color-accent-ink"]
		for _, bg := range []string{
			tok["--color-accent-fill"], tok["--color-accent-fill-hover"],
			panelBg(f), tok["--color-canvas"], tok["--color-sel"],
		} {
			if c := contrast(ink, bg); c < aaContrast {
				t.Errorf("%s accent-ink on %q = %.2f < AA", f.id, bg, c)
			}
		}
		// The accent itself is a border/ring, so it clears the non-text floor.
		if c := contrast(tok["--color-accent"], panelBg(f)); c < aaUI {
			t.Errorf("%s accent on panel = %.2f < aaUI", f.id, c)
		}
		// Dark flavors keep plain sky as accent-ink; only latte darkens it.
		if wantSky := f.dark; (tok["--color-accent-ink"] == f.sky) != wantSky {
			t.Errorf("%s accent-ink==sky is %v, want %v", f.id, tok["--color-accent-ink"] == f.sky, wantSky)
		}
	}
}

func TestPills(t *testing.T) {
	for _, f := range allFlavors() {
		tok := toTokens(f)
		for _, kind := range []string{"urgent", "broken", "work", "done", "grey"} {
			if c := contrast(tok["--color-pill-"+kind+"-fg"], tok["--color-pill-"+kind]); c < aaContrast {
				t.Errorf("%s pill-%s = %.2f < AA", f.id, kind, c)
			}
		}
	}
	// Mocha's tinted pills keep their own hue.
	tok := toTokens(mocha)
	if tok["--color-pill-work-fg"] != mocha.blue {
		t.Errorf("mocha pill-work-fg = %q, want blue", tok["--color-pill-work-fg"])
	}
	if tok["--color-pill-done-fg"] != mocha.green {
		t.Errorf("mocha pill-done-fg = %q, want green", tok["--color-pill-done-fg"])
	}
}

func TestInversePairsAA(t *testing.T) {
	for _, f := range allFlavors() {
		tok := toTokens(f)
		if c := contrast(tok["--color-on-accent"], tok["--color-accent"]); c < aaContrast {
			t.Errorf("%s on-accent = %.2f < AA", f.id, c)
		}
		if c := contrast(tok["--color-on-bad"], tok["--color-bad"]); c < aaContrast {
			t.Errorf("%s on-bad = %.2f < AA", f.id, c)
		}
	}
	// Dark flavors keep the near-black crust for both.
	for _, f := range []flavor{mocha, macchiato, frappe} {
		tok := toTokens(f)
		if tok["--color-on-accent"] != f.crust || tok["--color-on-bad"] != f.crust {
			t.Errorf("%s inverse pairs left crust", f.id)
		}
	}
}

func TestPlaceholder(t *testing.T) {
	for _, f := range allFlavors() {
		tok := toTokens(f)
		if c := contrast(tok["--color-placeholder"], tok["--color-canvas"]); c < aaContrast {
			t.Errorf("%s placeholder = %.2f < AA", f.id, c)
		}
		// Stays plain faint wherever faint already clears AA on canvas.
		alreadyAA := contrast(tok["--color-faint"], tok["--color-canvas"]) >= aaContrast
		if (tok["--color-placeholder"] == tok["--color-faint"]) != alreadyAA {
			t.Errorf("%s placeholder==faint mismatch (alreadyAA=%v)", f.id, alreadyAA)
		}
	}
}

// --- applier ----------------------------------------------------------------

func TestApplyThemeRepaintsAndRebuilds(t *testing.T) {
	// Restore the default after mutating package globals so later tests that read
	// the live palette are unaffected.
	t.Cleanup(func() { applyTheme(config.DefaultUITheme) })

	// Mocha default first: canvas is the dark mantle and a derived style follows.
	applyTheme("")
	if colCanvas != mocha.mantle {
		t.Errorf("applyTheme(\"\") colCanvas = %q, want mocha mantle %q", colCanvas, mocha.mantle)
	}
	mochaFaint := faintText.GetForeground()
	if mochaFaint != lipgloss.Color(colFaint) {
		t.Errorf("faintText fg %v != colFaint %q after mocha", mochaFaint, colFaint)
	}
	if luminance(colCanvas) > 0.2 {
		t.Errorf("mocha canvas %q is not dark (lum %.3f)", colCanvas, luminance(colCanvas))
	}

	// Latte flips canvas to a LIGHT value and the derived style var moves with it.
	applyTheme("catppuccin-latte")
	if colCanvas != latte.mantle {
		t.Errorf("latte colCanvas = %q, want latte mantle %q", colCanvas, latte.mantle)
	}
	if luminance(colCanvas) < 0.5 {
		t.Errorf("latte canvas %q is not light (lum %.3f)", colCanvas, luminance(colCanvas))
	}
	latteFaint := faintText.GetForeground()
	if latteFaint == mochaFaint {
		t.Error("faintText did not rebuild across a theme change")
	}
	if latteFaint != lipgloss.Color(colFaint) {
		t.Errorf("faintText fg %v != colFaint %q after latte", latteFaint, colFaint)
	}

	// Every palette-derived color is a resolved token, so an unknown id is safe
	// and falls back to the default flavor.
	applyTheme("nonsense")
	if colCanvas != mocha.mantle {
		t.Errorf("applyTheme(unknown) colCanvas = %q, want mocha mantle", colCanvas)
	}
}
