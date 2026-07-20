package main

import (
	"os"
	"regexp"
	"sort"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// The Catppuccin `mantle` of each flavor — the color catppuccin.ts's toTokens
// assigns to --color-canvas, and therefore the color the page paints under the
// native title bar. Spelled out here as literals so this test is a real third
// party to the Go map and the TypeScript table rather than a restatement of
// either: a drive-by edit to one of them fails against this list.
var wantCanvasHex = map[string]string{
	"catppuccin-latte":     "#e6e9ef",
	"catppuccin-frappe":    "#292c3c",
	"catppuccin-macchiato": "#1e2030",
	"catppuccin-mocha":     "#181825",
}

const catppuccinTS = "frontend/src/lib/catppuccin.ts"

// flavorMantles parses catppuccin.ts for each flavor literal's id and mantle.
// Parsing the real file (rather than trusting a copied constant) is the whole
// point: the Go window color and the CSS token have no compiler in common, so
// a text read is the only thing that can notice them diverging.
//
// The scan pairs every `id: "catppuccin-*"` with the next `mantle: "#rrggbb"`
// after it, which holds because both appear once per flavor object literal and
// FlavorColors declares mantle after id. A shape change that breaks that fails
// the count assertions below rather than silently matching nothing.
func flavorMantles(t *testing.T) map[string]string {
	t.Helper()
	src, err := os.ReadFile(catppuccinTS)
	if err != nil {
		t.Fatalf("read %s: %v", catppuccinTS, err)
	}
	pair := regexp.MustCompile(`id:\s*"(catppuccin-[a-z]+)"[\s\S]*?\n\s*mantle:\s*"(#[0-9a-f]{6})"`)
	out := map[string]string{}
	for _, m := range pair.FindAllStringSubmatch(string(src), -1) {
		if prev, dup := out[m[1]]; dup {
			t.Fatalf("%s: flavor %q parsed twice (%s, %s)", catppuccinTS, m[1], prev, m[2])
		}
		out[m[1]] = m[2]
	}
	if len(out) == 0 {
		t.Fatalf("%s: parsed no flavors — the file's shape changed and this test "+
			"stopped guarding anything", catppuccinTS)
	}
	return out
}

func hexOf(c application.RGBA) string {
	const digits = "0123456789abcdef"
	b := []byte{'#', 0, 0, 0, 0, 0, 0}
	for i, v := range []uint8{c.Red, c.Green, c.Blue} {
		b[1+i*2] = digits[v>>4]
		b[2+i*2] = digits[v&0xf]
	}
	return string(b)
}

// TestCanvasMatchesFrontendPalette is the anti-drift pin: the Go table, the
// literals above, and catppuccin.ts must all agree, for exactly the flavors
// config.UIThemes allows.
func TestCanvasMatchesFrontendPalette(t *testing.T) {
	ts := flavorMantles(t)

	ids := append([]string(nil), config.UIThemes...)
	sort.Strings(ids)
	goIDs := make([]string, 0, len(canvasByTheme))
	for id := range canvasByTheme {
		goIDs = append(goIDs, id)
	}
	sort.Strings(goIDs)
	tsIDs := make([]string, 0, len(ts))
	for id := range ts {
		tsIDs = append(tsIDs, id)
	}
	sort.Strings(tsIDs)

	if len(goIDs) != len(ids) {
		t.Fatalf("canvasByTheme covers %v, config.UIThemes is %v", goIDs, ids)
	}
	for i, id := range ids {
		if goIDs[i] != id {
			t.Fatalf("canvasByTheme covers %v, config.UIThemes is %v", goIDs, ids)
		}
		if tsIDs[i] != id {
			t.Fatalf("%s declares %v, config.UIThemes is %v", catppuccinTS, tsIDs, ids)
		}
	}

	for _, id := range ids {
		want, ok := wantCanvasHex[id]
		if !ok {
			t.Fatalf("no expected canvas hex recorded for %q", id)
		}
		if got := hexOf(canvasByTheme[id]); got != want {
			t.Errorf("canvasByTheme[%q] = %s, want %s", id, got, want)
		}
		if ts[id] != want {
			t.Errorf("%s: %q mantle = %s, want %s (update canvasByTheme and "+
				"wantCanvasHex together)", catppuccinTS, id, ts[id], want)
		}
	}
}

// TestWindowCanvasFollowsConfig covers the reason this stopped being a literal:
// a Latte install must not get a near-black window frame.
func TestWindowCanvasFollowsConfig(t *testing.T) {
	writeTestConfig(t, minimalConfig+"\n[ui]\ntheme = \"catppuccin-latte\"\n")
	if got, want := hexOf(windowCanvas()), wantCanvasHex["catppuccin-latte"]; got != want {
		t.Fatalf("windowCanvas() = %s, want latte canvas %s", got, want)
	}
}

// TestWindowCanvasDefaultsWhenUnset pins the load-order fallback: the window is
// built before anything validates or reads config, so an absent [ui], an
// unreadable config and an id Validate would reject must all paint the default
// flavor's canvas — never the zero RGBA (transparent black).
func TestWindowCanvasDefaultsWhenUnset(t *testing.T) {
	want := wantCanvasHex[config.DefaultUITheme]

	cases := []struct {
		name string
		body string
	}{
		{"no [ui] table", minimalConfig},
		{"unparseable config", "[defaults\nglobal_cap = "},
		{"unknown theme id", minimalConfig + "\n[ui]\ntheme = \"solarized\"\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeTestConfig(t, tc.body)
			got := windowCanvas()
			if got == (application.RGBA{}) {
				t.Fatal("windowCanvas() returned the zero RGBA")
			}
			if hexOf(got) != want {
				t.Fatalf("windowCanvas() = %s, want default canvas %s", hexOf(got), want)
			}
		})
	}
}
