// The Catppuccin palettes and the mapping from Catppuccin's *named* colors onto
// lola's existing design-token names. Pure data + pure functions: no DOM, no
// Svelte, no Wails bindings — so it unit-tests trivially and can be imported
// from anywhere (the applier, the terminal, the snapshot renderer).
//
// WHY named colors and not just the ANSI 16: the app chrome needs the surface
// ramp (crust < mantle < base < surface0 < surface1 < surface2) and the muted
// text ramp (overlay*/subtext*), and neither is recoverable from a 16-entry ANSI
// table. The ANSI 16 is carried alongside, verbatim from Ghostty's own theme
// files, so a pane in lola renders identically to the same pane in the user's
// terminal.
//
// Flavor ids match internal/config's UIThemes exactly — this list and the Go
// one are the same strings, and [ui].theme is validated against the Go copy.

export type ThemeId =
  | "catppuccin-mocha"
  | "catppuccin-macchiato"
  | "catppuccin-frappe"
  | "catppuccin-latte";

/** Same order and spelling as config.UIThemes (internal/config/ui.go). */
export const THEME_IDS: ThemeId[] = [
  "catppuccin-mocha",
  "catppuccin-macchiato",
  "catppuccin-frappe",
  "catppuccin-latte",
];

export const DEFAULT_THEME_ID: ThemeId = "catppuccin-mocha";

/** The 26 Catppuccin named colors, in the spec's own order. */
export interface FlavorColors {
  rosewater: string;
  flamingo: string;
  pink: string;
  mauve: string;
  red: string;
  maroon: string;
  peach: string;
  yellow: string;
  green: string;
  teal: string;
  sky: string;
  sapphire: string;
  blue: string;
  lavender: string;
  text: string;
  subtext1: string;
  subtext0: string;
  overlay2: string;
  overlay1: string;
  overlay0: string;
  surface2: string;
  surface1: string;
  surface0: string;
  base: string;
  mantle: string;
  crust: string;
}

export interface Flavor extends FlavorColors {
  id: ThemeId;
  /** Human label for the picker. */
  label: string;
  /** False for latte — drives `color-scheme` and every light/dark decision. */
  dark: boolean;
  /**
   * ANSI 0-15 exactly as Ghostty ships them. NOTE these are NOT all official
   * named Catppuccin colors: Ghostty substitutes perceptually brightened
   * variants at 9-14 (Mocha 9 = #f37799, not red #f38ba8). We take Ghostty's
   * numbers deliberately — the goal is that a tmux pane in lola looks like the
   * same pane in the user's Ghostty, not that it matches upstream Catppuccin's
   * terminal port.
   */
  ansi16: readonly string[];
  /** Terminal cursor color (Ghostty `cursor-color`). */
  cursor: string;
  /** Text drawn under the block cursor (Ghostty `cursor-text`). */
  cursorText: string;
  selectionBg: string;
  selectionFg: string;
  /**
   * Which name backs --color-faint. Defaults to overlay1 everywhere; latte
   * overrides to overlay2 because latte's overlay1 (#8c8fa1) on base (#eff1f5)
   * is only ~2.8:1 and the token carries real label text. Encoded as data
   * rather than an `if (latte)` branch in the mapper.
   */
  faintKey: keyof FlavorColors;
}

const mocha: Flavor = {
  id: "catppuccin-mocha",
  label: "Mocha",
  dark: true,
  rosewater: "#f5e0dc",
  flamingo: "#f2cdcd",
  pink: "#f5c2e7",
  mauve: "#cba6f7",
  red: "#f38ba8",
  maroon: "#eba0ac",
  peach: "#fab387",
  yellow: "#f9e2af",
  green: "#a6e3a1",
  teal: "#94e2d5",
  sky: "#89dceb",
  sapphire: "#74c7ec",
  blue: "#89b4fa",
  lavender: "#b4befe",
  text: "#cdd6f4",
  subtext1: "#bac2de",
  subtext0: "#a6adc8",
  overlay2: "#9399b2",
  overlay1: "#7f849c",
  overlay0: "#6c7086",
  surface2: "#585b70",
  surface1: "#45475a",
  surface0: "#313244",
  base: "#1e1e2e",
  mantle: "#181825",
  crust: "#11111b",
  ansi16: [
    "#45475a", "#f38ba8", "#a6e3a1", "#f9e2af",
    "#89b4fa", "#f5c2e7", "#94e2d5", "#a6adc8",
    "#585b70", "#f37799", "#89d88b", "#ebd391",
    "#74a8fc", "#f2aede", "#6bd7ca", "#bac2de",
  ],
  cursor: "#f5e0dc",
  cursorText: "#1e1e2e",
  selectionBg: "#585b70",
  selectionFg: "#cdd6f4",
  faintKey: "overlay1",
};

const macchiato: Flavor = {
  id: "catppuccin-macchiato",
  label: "Macchiato",
  dark: true,
  rosewater: "#f4dbd6",
  flamingo: "#f0c6c6",
  pink: "#f5bde6",
  mauve: "#c6a0f6",
  red: "#ed8796",
  maroon: "#ee99a0",
  peach: "#f5a97f",
  yellow: "#eed49f",
  green: "#a6da95",
  teal: "#8bd5ca",
  sky: "#91d7e3",
  sapphire: "#7dc4e4",
  blue: "#8aadf4",
  lavender: "#b7bdf8",
  text: "#cad3f5",
  subtext1: "#b8c0e0",
  subtext0: "#a5adcb",
  overlay2: "#939ab7",
  overlay1: "#8087a2",
  overlay0: "#6e738d",
  surface2: "#5b6078",
  surface1: "#494d64",
  surface0: "#363a4f",
  base: "#24273a",
  mantle: "#1e2030",
  crust: "#181926",
  ansi16: [
    "#494d64", "#ed8796", "#a6da95", "#eed49f",
    "#8aadf4", "#f5bde6", "#8bd5ca", "#a5adcb",
    "#5b6078", "#ec7486", "#8ccf7f", "#e1c682",
    "#78a1f6", "#f2a9dd", "#63cbc0", "#b8c0e0",
  ],
  cursor: "#f4dbd6",
  cursorText: "#24273a",
  selectionBg: "#5b6078",
  selectionFg: "#cad3f5",
  faintKey: "overlay1",
};

const frappe: Flavor = {
  id: "catppuccin-frappe",
  label: "Frappé",
  dark: true,
  rosewater: "#f2d5cf",
  flamingo: "#eebebe",
  pink: "#f4b8e4",
  mauve: "#ca9ee6",
  red: "#e78284",
  maroon: "#ea999c",
  peach: "#ef9f76",
  yellow: "#e5c890",
  green: "#a6d189",
  teal: "#81c8be",
  sky: "#99d1db",
  sapphire: "#85c1dc",
  blue: "#8caaee",
  lavender: "#babbf1",
  text: "#c6d0f5",
  subtext1: "#b5bfe2",
  subtext0: "#a5adce",
  overlay2: "#949cbb",
  overlay1: "#838ba7",
  overlay0: "#737994",
  surface2: "#626880",
  surface1: "#51576d",
  surface0: "#414559",
  base: "#303446",
  mantle: "#292c3c",
  crust: "#232634",
  ansi16: [
    "#51576d", "#e78284", "#a6d189", "#e5c890",
    "#8caaee", "#f4b8e4", "#81c8be", "#a5adce",
    "#626880", "#e67172", "#8ec772", "#d9ba73",
    "#7b9ef0", "#f2a4db", "#5abfb5", "#b5bfe2",
  ],
  cursor: "#f2d5cf",
  cursorText: "#303446",
  selectionBg: "#626880",
  selectionFg: "#c6d0f5",
  faintKey: "overlay1",
};

const latte: Flavor = {
  id: "catppuccin-latte",
  label: "Latte",
  dark: false,
  rosewater: "#dc8a78",
  flamingo: "#dd7878",
  pink: "#ea76cb",
  mauve: "#8839ef",
  red: "#d20f39",
  maroon: "#e64553",
  peach: "#fe640b",
  yellow: "#df8e1d",
  green: "#40a02b",
  teal: "#179299",
  sky: "#04a5e5",
  sapphire: "#209fb5",
  blue: "#1e66f5",
  lavender: "#7287fd",
  text: "#4c4f69",
  subtext1: "#5c5f77",
  subtext0: "#6c6f85",
  overlay2: "#7c7f93",
  overlay1: "#8c8fa1",
  overlay0: "#9ca0b0",
  surface2: "#acb0be",
  surface1: "#bcc0cc",
  surface0: "#ccd0da",
  base: "#eff1f5",
  mantle: "#e6e9ef",
  crust: "#dce0e8",
  // Latte inverts the index→name mapping at 0/7/8/15 by design (0 = subtext1,
  // 7 = surface2, 8 = subtext0, 15 = surface1), which is why this table is
  // stored per flavor instead of being derived from names.
  ansi16: [
    "#5c5f77", "#d20f39", "#40a02b", "#df8e1d",
    "#1e66f5", "#ea76cb", "#179299", "#acb0be",
    "#6c6f85", "#de293e", "#49af3d", "#eea02d",
    "#456eff", "#fe85d8", "#2d9fa8", "#bcc0cc",
  ],
  cursor: "#dc8a78",
  cursorText: "#eff1f5",
  selectionBg: "#acb0be",
  selectionFg: "#4c4f69",
  faintKey: "overlay2",
};

export const FLAVORS: Readonly<Record<ThemeId, Flavor>> = Object.freeze({
  "catppuccin-mocha": Object.freeze(mocha),
  "catppuccin-macchiato": Object.freeze(macchiato),
  "catppuccin-frappe": Object.freeze(frappe),
  "catppuccin-latte": Object.freeze(latte),
});

/** Resolve an id to a flavor, falling back to the default for anything unknown. */
export function flavorFor(id: string | null | undefined): Flavor {
  return FLAVORS[(id ?? "") as ThemeId] ?? FLAVORS[DEFAULT_THEME_ID];
}

// --- small color math -------------------------------------------------------
// Done in TS rather than CSS color-mix() for two reasons: the results have to be
// literal hex for xterm.js (which cannot resolve CSS functions), and a pure
// function is unit-testable and deterministic.

function rgb(hex: string): [number, number, number] {
  const h = hex.replace("#", "");
  return [
    parseInt(h.slice(0, 2), 16),
    parseInt(h.slice(2, 4), 16),
    parseInt(h.slice(4, 6), 16),
  ];
}

function hex(c: [number, number, number]): string {
  return "#" + c.map((v) => Math.round(Math.min(255, Math.max(0, v))).toString(16).padStart(2, "0")).join("");
}

/** Blend `t` parts of `a` into `b` (t = 1 → a, t = 0 → b). Simple sRGB lerp. */
export function mix(a: string, b: string, t: number): string {
  const [ar, ag, ab] = rgb(a);
  const [br, bg, bb] = rgb(b);
  return hex([ar * t + br * (1 - t), ag * t + bg * (1 - t), ab * t + bb * (1 - t)]);
}

/** Relative luminance (WCAG), used only to pick the darker of two candidates. */
export function luminance(color: string): number {
  const f = (v: number) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  const [r, g, b] = rgb(color);
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
}

/** WCAG contrast ratio between two hex colors. */
export function contrast(a: string, b: string): number {
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

/** WCAG AA for body text. Every derived foreground here is measured against it. */
export const AA = 4.5;

/**
 * Text drawn on top of a fully saturated fill (the solid status pills, the
 * accent chip). Chosen by measured contrast against that specific fill rather
 * than hardcoded, which is what makes ONE rule correct in all four flavors: in
 * the dark flavors the near-black end is `crust`, but in latte crust (#dce0e8)
 * is the LIGHT end while `text` (#4c4f69) is dark — and even within latte the
 * right answer differs per fill (light on red, dark on peach).
 *
 * The ramp's own extremes are tried first, so a fill that the palette can label
 * is labelled in the palette and the dark flavors never move. Where it cannot,
 * the winner keeps walking PAST the end of the ramp toward the absolute end it
 * already points at. Two fills need that, both on latte: peach, whose best
 * on-palette label reaches 2.68:1 while backing `needs_input` — the single label
 * in the app that most has to be readable — and the accent chip, whose best
 * reaches 4.14:1 (measured against the 3:1 accent visible() resolves to,
 * #037cac, not against raw sky). Stopping at the first step that clears AA
 * means the label gives up exactly as much palette as the measurement demands
 * and no more.
 */
export function onFill(f: Flavor, fill: string): string {
  const best = [f.crust, f.base, f.text].reduce((a, c) =>
    contrast(c, fill) > contrast(a, fill) ? c : a,
  );
  if (contrast(best, fill) >= AA) return best;
  const end = luminance(best) < luminance(fill) ? "#000000" : "#ffffff";
  // Integer steps, least deviation first — the same bit-reproducible walk
  // readable() uses in the opposite direction.
  for (let step = 1; step <= 20; step++) {
    const c = mix(end, best, step / 20);
    if (contrast(c, fill) >= AA) return c;
  }
  return end;
}

/**
 * The end of the flavor's ramp furthest from `text` — crust on the dark
 * flavors, base on latte. Derived through onFill rather than branched on
 * `dark` so the direction stays measured.
 */
function backdrop(f: Flavor): string {
  return onFill(f, f.text);
}

/**
 * Mirrors `@utility panel` in app.css, which paints
 * `color-mix(--color-panel 82%, --color-canvas)`. Any "is this legible on a
 * panel" arithmetic has to use THIS color, not `f.base` — the 18% pull toward
 * canvas is what components actually sit on.
 */
export const PANEL_MIX = 0.82;

export function panelBg(f: Flavor): string {
  return mix(f.base, f.mantle, PANEL_MIX);
}

/**
 * WCAG 1.4.11 non-text contrast: the floor for a UI component boundary or a
 * focus indicator against what it is drawn on. Lower than AA because a 2 px
 * border is not 11 px type — but it is a floor, not a suggestion, and the
 * accent is the token that has to meet it (`border-accent`, the input
 * `focus:border-accent` ring, `ring-accent`, `accent-accent` on checkboxes).
 */
export const AA_UI = 3;

/**
 * `color` walked toward black or white until it clears `floor` against every
 * surface in `on`, keeping as much of the original color as the ratio allows.
 * Shared by readable() (AA, for text) and visible() (AA_UI, for borders).
 *
 * WHY an achromatic anchor and not the flavor's own `text`: an sRGB lerp
 * toward black or white scales the channels without reordering them, so the
 * result keeps the hue it started with. Blending toward `text` does not —
 * latte's `text` is #4c4f69, a desaturated slate, and walking the semantic
 * colors into it collapsed green/yellow/peach onto #4a5b60/#5b5561/#67525b:
 * three greys nobody can tell apart, which for status colors destroys the
 * payload the color is carrying. Toward black they stay #26601a/#7b4e10/
 * #983c07 — a green, an amber and a rust.
 *
 * WHICH end is measured, not branched on `f.dark`: whichever of black/white
 * scores better against the worst surface in `on`. That is the dark end on
 * latte and the light end on the other three, but a caller passing a saturated
 * fill gets the right answer without knowing which way "up" is. (onFill is the
 * neighbouring rule for text ON a fill, where the answer wants to be a palette
 * name first and only walks past the ramp when it must.)
 *
 * Callers that choose their own background — the tinted pills, the accent fill
 * — are responsible for picking one where the floor is reachable, and the
 * tests pin exactly that.
 */
function walk(color: string, on: string[], floor: number): string {
  const worst = (c: string) => Math.min(...on.map((bg) => contrast(c, bg)));
  if (worst(color) >= floor) return color;
  const end = worst("#000000") > worst("#ffffff") ? "#000000" : "#ffffff";
  // Integer steps so the walk is bit-reproducible; 19/20 first keeps the most
  // color, and the first candidate that clears the floor wins.
  for (let step = 19; step >= 1; step--) {
    const c = mix(color, end, step / 20);
    if (worst(c) >= floor) return c;
  }
  return end;
}

/** `color` as TEXT on every surface in `on`: walked until it clears AA. */
export function readable(color: string, ...on: string[]): string {
  return walk(color, on, AA);
}

/**
 * `color` as a BORDER or focus ring on every surface in `on`: walked until it
 * clears AA_UI. Separate from readable() only in the floor — a border that had
 * to reach 4.5:1 would be a much darker accent than the design wants, and
 * 1.4.11 does not ask for one.
 */
export function visible(color: string, ...on: string[]): string {
  return walk(color, on, AA_UI);
}

/**
 * How much accent to blend into the flavor's BACKGROUND end for the *tinted*
 * surfaces (the work/done pills, the primary-button fill). Low enough that
 * they still read as surfaces, high enough to carry hue.
 *
 * The substrate used to be `surface0`, which is the bug this constant's
 * comment now guards against: surface0 steps AWAY from base, i.e. toward the
 * accent's own luminance in EVERY flavor (lighter on the dark ones where
 * accents are light, darker on latte where they are dark). Tinting into it
 * therefore squeezes the accent and its backdrop together — latte's done pill
 * landed at 1.75:1. `mantle` is the surface Catppuccin's accents are designed
 * to be legible against, so it is the only substrate that behaves the same way
 * in all four flavors.
 */
export const PILL_TINT = 0.28;

/**
 * Alpha of the accent in the primary-button fill (`bg-accent-fill`). Its hover
 * twin keeps the same tint but re-mixes it into `backdrop()` instead of
 * `mantle`, so hovering moves the fill AWAY from its own text. The old recipe
 * — an alpha `bg-accent/20` deepening to `/30` — did the opposite: more accent
 * in the fill means less contrast against accent-colored text, so hover was
 * always the worse of the two states (mocha 6.5 → 5.0, latte 2.0 → 1.9).
 * Opaque tokens also make the ratio knowable at all; an alpha fill composites
 * over whatever happens to be behind the button.
 */
export const ACCENT_FILL = 0.2;

// --- lola token mapping -----------------------------------------------------

/**
 * Every lola design token, mapped onto a Catppuccin named color. The token
 * NAMES are unchanged from the original dark-navy palette, so no component has
 * to change — only the values become flavor-derived.
 *
 * The mapping is structural, not per-flavor: lola's surface ramp
 * (canvas < panel < sel < edge) lines up 1:1 with Catppuccin's
 * (mantle < base < surface0 < surface1), and latte preserves the ordinal
 * meaning of those names even though it inverts their luminance. That is what
 * makes a single table correct for all four flavors.
 *
 *   canvas  → mantle     app background, one step BELOW panels
 *   panel   → base       raised surface, and the terminal background — they
 *                        match by construction, replacing the hand-tuned
 *                        #111927 that used to be pasted into LiveTerminal
 *   sel     → surface0   selected-row band, one step above panel
 *   edge    → surface1   panel border / rule
 *   ink     → text       default foreground
 *   faint   → overlay1   muted secondary text (overlay2 on latte, see faintKey)
 *
 *   accent  → sky        the old accent #57c7d6 is hue ~187°; sky ~190°,
 *                        teal ~170°, sapphire ~199° — sky is the closest match
 *                        and leaves teal free for other use. DECORATIVE only:
 *                        fills, borders, the focus ring. Accent-colored TEXT
 *                        uses --color-accent-ink, see below.
 *   good    → green      direct semantic carry-over
 *   bad     → red        direct
 *   warn    → yellow     direct
 *   info    → blue       direct (the old #6ea8fe is already almost blue)
 *   orange  → peach      Catppuccin's orange is named "peach"
 *   magenta → mauve      the old #c99bf0 is near-identical to mauve #cba6f7
 *
 * Every one of those seven is then run through readable() against the three
 * surfaces the app prints them on — canvas, panel and the selected-row band.
 * They are the STATUS colors (theme.ts statusText/reactingText), and Rail,
 * SessionsTable, SessionsKanban, VitalsBar and ProjectDetail print them bare,
 * with no fill of their own to lean on. On the dark flavors the names already
 * clear AA and come back verbatim, so the default theme does not move. Latte's
 * do not: green 2.92, yellow 2.29 and peach 2.61 on a panel — Catppuccin tunes
 * its accents to be seen against the flavor's background, and on a near-white
 * background a mid-luminance accent simply cannot carry 11px type. Frappé's
 * red/blue/peach/mauve also miss on the selected band (3.57-4.45). Walking
 * toward black rather than toward `text` is what lets them stay telling apart
 * from each other; see walk().
 *
 * accent is the exception that keeps its raw name: it is a FILL and a BORDER,
 * never text, so it takes visible()'s 3:1 (WCAG 1.4.11) instead. Latte's sky
 * was 2.44 on a panel, i.e. every input's focus ring and every selected tab
 * underline sat below the non-text floor.
 *
 * on-accent → text drawn ON the accent fill, and on-bad likewise for the `bad`
 *             fill. Both are onFill() results, i.e. measured against the exact
 *             fill they sit on. They replace two hardcoded inverse pairs that
 *             each assumed one end of the ramp was the dark end: `canvas` used
 *             as a foreground on the selected lens/agent/scope chips (11.29:1
 *             on mocha but 2.30:1 on latte, where canvas is near-white), and
 *             Tailwind's built-in white on the `dead` pill (2.32:1 on mocha —
 *             the only pill that skipped onFill, and the only foreground in the
 *             app no flavor could reach at all).
 *
 * placeholder → input placeholder text: `faint` walked up to AA against canvas,
 *             the fill those inputs paint. It exists because the sites used to
 *             ask for `faint` at 50% alpha, and an alpha of an already-muted
 *             token composites over an unknown backdrop — 2.15:1 on mocha and
 *             1.70:1 on latte in practice, unknowable in general. Opaque and
 *             measured instead. Plain `faint` is not enough on its own either:
 *             4.09:1 on frappe's canvas, 3.25:1 on latte's.
 *
 * accent-ink → the accent as TEXT. Splitting it off is what fixes latte:
 *              Catppuccin tunes accents to be legible on the flavor's own
 *              background, and latte's sky (#04a5e5) simply is not — 2.44:1
 *              bare on a panel and 2.03:1 inside the button fill, against
 *              7.4-10.7:1 on the dark flavors. No latte palette name rescues
 *              it either (blue, the best, is 4.30:1 and still fails on the
 *              fill), so unlike `faintKey` this cannot be a rename: the accent
 *              has to be darkened. readable() does that by measurement, which
 *              is why the three dark flavors come back as plain `sky` and
 *              nothing about the default theme moves. It walks from the
 *              already-3:1 accent, not from raw sky, so ink and border stay
 *              the same hue rather than diverging into two different cyans.
 *
 * Pills follow two rules instead of ten literals: SOLID pills (urgent/broken)
 * are the accent itself with onFill() text — they must stay high-contrast
 * alarm fills in every flavor; TINTED pills (work/done) are the accent blended
 * into `mantle` with readable() text, so the accent survives as the label
 * wherever the ratio allows (mocha, macchiato's done) and yields to ink where
 * the palette cannot carry it (latte).
 *
 * pill-urgent/-broken keep the RAW peach and red even though --color-orange
 * and --color-bad are walked versions of the same two names. That is the rule
 * the whole table follows: a token that is ever printed as text is walked to
 * AA, a token that is only ever a fill is not — a fill has no contrast
 * requirement of its own, it has onFill() to label it, and darkening an alarm
 * fill for a reason that does not apply to it would only make it quieter. The
 * one token that is both is --color-bad, which also paints the `dead` pill;
 * the text requirement wins there and --color-on-bad re-measures against
 * whatever it became, which is why `dead` and `ci_failed` sit on marginally
 * different reds on latte and frappé. They are never adjacent (different
 * kanban columns), and the alternative was an unreadable `text-bad`.
 */
export function toTokens(f: Flavor): Record<string, string> {
  const surface = panelBg(f);
  // The three surfaces bare text lands on, in one list because every semantic
  // token below has to clear all of them: an unselected row sits on the panel,
  // a selected one on `sel`, and the shell around both is canvas.
  const bare = [f.mantle, surface, f.surface0];
  // Borders first: the accent is a border and a fill before it is anything
  // else, and the ink walks on from wherever the border ended up.
  const accent = visible(f.sky, ...bare);
  const bad = readable(f.red, ...bare);
  const faint = f[f.faintKey];
  const fill = mix(accent, f.mantle, ACCENT_FILL);
  const fillHover = mix(accent, backdrop(f), ACCENT_FILL);
  const work = mix(f.blue, f.mantle, PILL_TINT);
  const done = mix(f.green, f.mantle, PILL_TINT);

  return {
    "--color-canvas": f.mantle,
    "--color-panel": f.base,
    "--color-edge": f.surface1,
    "--color-accent": accent,
    "--color-accent-ink": readable(accent, fill, fillHover, ...bare),
    "--color-accent-fill": fill,
    "--color-accent-fill-hover": fillHover,
    "--color-on-accent": onFill(f, accent),
    "--color-ink": f.text,
    "--color-faint": faint,
    "--color-placeholder": readable(faint, f.mantle),
    "--color-sel": f.surface0,

    "--color-good": readable(f.green, ...bare),
    "--color-bad": bad,
    "--color-on-bad": onFill(f, bad),
    "--color-warn": readable(f.yellow, ...bare),
    "--color-info": readable(f.blue, ...bare),
    "--color-orange": readable(f.peach, ...bare),
    "--color-magenta": readable(f.mauve, ...bare),

    "--color-pill-urgent": f.peach,
    "--color-pill-urgent-fg": onFill(f, f.peach),
    "--color-pill-broken": f.red,
    "--color-pill-broken-fg": onFill(f, f.red),
    "--color-pill-work": work,
    "--color-pill-work-fg": readable(f.blue, work),
    "--color-pill-done": done,
    "--color-pill-done-fg": readable(f.green, done),
    "--color-pill-grey": f.surface0,
    "--color-pill-grey-fg": readable(f.subtext0, f.surface0),
  };
}

/** The token names toTokens always produces — the applier's contract. */
export const TOKEN_NAMES: string[] = Object.keys(toTokens(FLAVORS[DEFAULT_THEME_ID]));

// --- terminal palettes ------------------------------------------------------

/** The subset of xterm.js's ITheme we set. Kept structural to avoid importing
 *  @xterm/xterm from a pure-data module. */
export interface TermTheme {
  background: string;
  foreground: string;
  cursor: string;
  cursorAccent: string;
  selectionBackground: string;
  selectionForeground: string;
  black: string;
  red: string;
  green: string;
  yellow: string;
  blue: string;
  magenta: string;
  cyan: string;
  white: string;
  brightBlack: string;
  brightRed: string;
  brightGreen: string;
  brightYellow: string;
  brightBlue: string;
  brightMagenta: string;
  brightCyan: string;
  brightWhite: string;
}

/**
 * xterm.js theme for a flavor. `background` is deliberately `base`, i.e. the
 * exact value of --color-panel, so the terminal sits flush on the panel it
 * lives in and an agent's OSC-11 background probe reads a color that is really
 * on screen. No hand-picked constant is involved any more.
 */
export function toXterm(f: Flavor): TermTheme {
  const a = f.ansi16;
  return {
    background: f.base,
    foreground: f.text,
    cursor: f.cursor,
    cursorAccent: f.cursorText,
    selectionBackground: f.selectionBg,
    selectionForeground: f.selectionFg,
    black: a[0],
    red: a[1],
    green: a[2],
    yellow: a[3],
    blue: a[4],
    magenta: a[5],
    cyan: a[6],
    white: a[7],
    brightBlack: a[8],
    brightRed: a[9],
    brightGreen: a[10],
    brightYellow: a[11],
    brightBlue: a[12],
    brightMagenta: a[13],
    brightCyan: a[14],
    brightWhite: a[15],
  };
}

/** Palette for the DOM snapshot renderer (lib/ansi.ts). `bg`/`fg` back the
 *  inverse-video (SGR 7) fallback, which used to be hardcoded lola navy. */
export interface AnsiPalette {
  ansi16: readonly string[];
  bg: string;
  fg: string;
}

export function toAnsi(f: Flavor): AnsiPalette {
  return { ansi16: f.ansi16, bg: f.base, fg: f.text };
}
