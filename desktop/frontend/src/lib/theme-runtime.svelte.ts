// The runtime half of theming: which Catppuccin flavor is live, and how it
// reaches the DOM. `catppuccin.ts` stays pure data; everything that touches
// document, runes, or the Wails bridge lives here.
//
// HOW A TOKEN SWAP TAKES EFFECT — the non-obvious bit worth knowing before
// touching this file. Tailwind v4 compiles `@theme` into custom properties on
// :root and compiles every utility into a `var()` reference:
//
//     @layer theme { :root { --color-canvas: #181825; … } }
//     .bg-canvas { background-color: var(--color-canvas) }
//
// so redefining the property re-resolves every utility, every color-mix() in
// app.css, and the `accent-[var(--color-accent)]` arbitrary values in the forms.
// No component, class name, or Tailwind rebuild is involved.
//
// We write those properties as INLINE STYLE on documentElement. Inline styles
// belong to no cascade layer and therefore outrank `@layer theme` and any
// unlayered author rule — the override cannot be beaten by a stylesheet someone
// adds later. A `data-theme` attribute rides along as a hook for anything that
// needs to branch on the flavor rather than read a value.

import {
  DEFAULT_THEME_ID,
  FLAVORS,
  THEME_IDS,
  flavorFor,
  toAnsi,
  toTokens,
  toXterm,
  type AnsiPalette,
  type Flavor,
  type TermTheme,
  type ThemeId,
} from "./catppuccin";

export { DEFAULT_THEME_ID, FLAVORS, THEME_IDS, type Flavor, type ThemeId };

/**
 * xterm.js constructor options that make a lola terminal cell geometrically
 * identical to the user's Ghostty. Exported as a constant rather than inlined
 * so the arithmetic lives in one place; LiveTerminal.svelte spreads it.
 *
 * Ghostty (font-family = Hack, font-size = 13, adjust-cell-height = 10%) was
 * measured via TIOCGWINSZ ws_xpixel/ws_ypixel ÷ cols/rows: its cell is EXACTLY
 * 8.0000 x 17.0000 css px. Reproducing that in xterm needs three numbers,
 * because xterm derives its cell as (WebglRenderer.ts:541-582):
 *
 *   device.char.width  = floor(advance * dpr)                        // :541
 *   device.cell.width  = device.char.width + round(letterSpacing)    // :558
 *   device.char.height = ceil(charH * dpr)                           // :546
 *   device.cell.height = floor(device.char.height * lineHeight)      // :551
 *   css.cell.*         = device.cell.* / dpr                         // :581-582
 *
 * THE TWO INPUTS, as WebKit reports them for Hack@13 (both measured, not
 * assumed — CharSizeService.ts:121-126 measures with an OffscreenCanvas):
 *
 *   advance = 13 * 1233/2048 = 13 * 0.602051 = 7.826660 px
 *             (Hack is monospace; 1233/2048 em is its one advance width)
 *   charH   = fontBoundingBoxAscent + fontBoundingBoxDescent = 12 + 3 = 15 px
 *             (WebKit rounds those two to INTEGERS, which is why this lands on
 *             exactly the round(hhea.ascent) + round(hhea.descent) = 15 that
 *             Ghostty uses for its own base cell height)
 *
 * WIDTH -> 8.0000 css px. floor() at :541 throws the fraction away, so the
 * char is 7 device px at dpr 1 and floor(15.653320) = 15 at dpr 2. letterSpacing
 * is then added at :558 in DEVICE pixels, with no dpr multiply — that is the
 * non-obvious bit, and it is why one single value works on both displays:
 *
 *   dpr 1: floor(7.826660)  + round(1) = 7  + 1 = 8   ->  8 / 1 = 8.0000 css
 *   dpr 2: floor(15.653320) + round(1) = 15 + 1 = 16  -> 16 / 2 = 8.0000 css
 *
 * The 1 px does NOT "restore what floor discarded" — at dpr 1 floor discarded
 * 0.826660 px and we add a whole 1.0 back, overshooting by 0.173340. That
 * overshoot is the point: Ghostty ROUNDS its cell width, round(7.826660) = 8,
 * so overshooting to 8 is what matches it. Without the letterSpacing the cell
 * would sit at 7 px, i.e. (7.826660 - 7) / 7.826660 = 10.562% narrower than the
 * type's own advance, and consecutive glyphs visibly crowd. (5.09% would be the
 * figure at fontSize 14 — that is the size this set replaced, not this one.)
 * Box-drawing glyphs are synthesised at full cell width, so TUI borders stay
 * gapless despite the trailing 1 px of tracking (char.left = floor(1/2) = 0).
 *
 * HEIGHT -> 17.0000 css px, via lineHeight only:
 *
 *   dpr 1: floor(ceil(15 * 1) * 1.15) = floor(15 * 1.15) = floor(17.25) = 17
 *          -> 17 / 1 = 17.0000 css
 *   dpr 2: floor(ceil(15 * 2) * 1.15) = floor(30 * 1.15) = floor(34.50) = 34
 *          -> 34 / 2 = 17.0000 css
 *
 * Both axes land on WHOLE device pixels at dpr 1 and at dpr 2, so the WebGL
 * glyph atlas stays crisp on either kind of display. (This machine is dpr 1 —
 * the panels are 2560x1440 non-Retina — so dpr 1 is the case that ships and
 * dpr 2 is the case that must not regress.)
 *
 * lineHeight must satisfy floor(15*L) = 17 AND floor(30*L) = 34, i.e.
 * L ∈ [1.1333334, 1.1666666]. 1.15 sits mid-interval. Do NOT "simplify" it to
 * 17/15 = 1.1333333… — that is the excluded lower boundary and floating point
 * makes floor(15 * 1.1333333) = 16, one pixel short, silently.
 *
 * fontWeight/fontWeightBold are 400/700 because Hack ships only those two
 * faces: 500 snaps down to regular and 600 snaps up to bold, so weight is not
 * usable as a stroke-thickening knob (see the font-smoothing rule in app.css
 * for the knob that does work).
 */
export const TERM_FONT = {
  fontFamily: '"Hack", "JetBrains Mono", ui-monospace, Menlo, monospace',
  fontSize: 13,
  fontWeight: 400 as const,
  fontWeightBold: 700 as const,
  lineHeight: 1.15,
  letterSpacing: 1,
  allowTransparency: false,
} as const;

/** The one family in TERM_FONT.fontFamily that we ship ourselves (app.css @font-face). */
const TERM_FONT_FACE = "Hack";

/**
 * How long a terminal will wait for Hack before opening anyway. The face is a
 * vendored woff2 served from the embedded asset FS, so this is single-digit ms
 * in practice; the bound exists only so a webview that never settles the load
 * promise gives us a terminal with the wrong cell instead of no terminal.
 */
const TERM_FONT_WAIT_MS = 2000;

/**
 * Resolve true once Hack is loaded and measurable, false if it cannot be.
 *
 * WHY THIS EXISTS AT ALL: xterm measures the font EXACTLY ONCE, inside
 * `Terminal.open()` (Terminal.ts:570 -> CharSizeService.measure()), and after
 * that only re-measures when `fontFamily`/`fontSize` change
 * (CharSizeService.ts:34) or after a real resize (Terminal.ts:1214). If Hack is
 * still loading at open() the cell is frozen on the FALLBACK face's metrics for
 * the lifetime of the terminal — and the fallback is JetBrains Mono, which the
 * app chrome has already loaded, so it measures cleanly and nothing looks
 * broken. It just isn't 8x17.
 *
 * `document.fonts.ready` is NOT the right primitive here: it reports that no
 * font load is *pending*, and a @font-face is only fetched once something asks
 * for it — so it can resolve before Hack has been requested at all.
 * `fonts.load(spec)` both requests the face and resolves when it is usable.
 *
 * BOTH WEIGHTS, not just 400. xterm passes fontWeight 400 and fontWeightBold 700
 * (TERM_FONT), and app.css declares a separate @font-face per weight, so 700 is
 * its own fetch. Only 400 decides the cell — every Hack glyph has the same
 * advance — but the WebGL atlas rasterises a glyph once and caches it under a
 * key that does not include which face answered, so a bold glyph drawn before
 * hack-bold.woff2 lands is stuck as fallback JetBrains Mono Bold for the
 * terminal's lifetime. Same permanent-staleness bug as the regular face, one
 * weight over: it looks like a font that merely renders bold a bit differently.
 */
export async function loadTermFont(fonts: FontFaceSet | undefined): Promise<boolean> {
  if (!fonts || typeof fonts.load !== "function") return false;
  const face = (weight: number) => `${weight} ${TERM_FONT.fontSize}px "${TERM_FONT_FACE}"`;
  try {
    const loaded = await Promise.all([
      fonts.load(face(TERM_FONT.fontWeight)),
      fonts.load(face(TERM_FONT.fontWeightBold)),
    ]);
    // A missing/404 src resolves with an empty list rather than rejecting. The
    // regular face is what gates the cell, so it alone decides the verdict;
    // awaiting bold still keeps it out of the atlas race above.
    return loaded[0].length > 0;
  } catch {
    return false;
  }
}

let termFontPromise: Promise<boolean> | undefined;

/**
 * Memoised `loadTermFont` — one load for the whole app no matter how many
 * terminals mount. Never rejects.
 */
export function termFontLoaded(): Promise<boolean> {
  return (termFontPromise ??= loadTermFont(globalThis.document?.fonts));
}

/**
 * `termFontLoaded()` bounded by `ms`. False means "we are done waiting and the
 * font is not known to be there" — either it failed or it is still in flight.
 * Callers that care can chain `termFontLoaded()` afterwards to recover.
 */
export function termFontReady(ms: number = TERM_FONT_WAIT_MS): Promise<boolean> {
  return Promise.race([
    termFontLoaded(),
    new Promise<boolean>((resolve) => setTimeout(() => resolve(false), ms)),
  ]);
}

/**
 * Push a flavor onto the document. Idempotent and cheap — safe to call on every
 * change. Takes the root element so it can be unit-tested against a detached
 * node instead of the live document.
 */
export function applyFlavor(flavor: Flavor, root: HTMLElement = document.documentElement): void {
  for (const [name, value] of Object.entries(toTokens(flavor))) {
    root.style.setProperty(name, value);
  }
  root.dataset.theme = flavor.id;
  // app.css pins `color-scheme: dark` as the compiled default (mocha, the
  // default flavor, is dark) so a cold start before JS still renders right.
  // This inline write is what flips native form controls, `<select>` popups and
  // UA scrollbars for latte.
  root.style.colorScheme = flavor.dark ? "dark" : "light";
}

/**
 * Where the last painted flavor is remembered so the NEXT launch can paint it
 * on the first frame. config.toml stays the source of truth; this is a cache of
 * it, and a stale or absent entry costs at most the one repaint we already do.
 */
const THEME_CACHE_KEY = "lola.theme";

/**
 * Last flavor this app painted, or the compiled default. localStorage is
 * per-origin; the app serves its assets from a fixed origin, but if that ever
 * changes the cache simply misses and boot degrades to painting the default
 * first — i.e. exactly the old behaviour, never worse.
 */
function cachedTheme(): ThemeId {
  try {
    return flavorFor(globalThis.localStorage?.getItem(THEME_CACHE_KEY)).id;
  } catch {
    return DEFAULT_THEME_ID; // storage disabled or partitioned
  }
}

function cacheTheme(id: ThemeId): void {
  try {
    globalThis.localStorage?.setItem(THEME_CACHE_KEY, id);
  } catch {
    /* a theme is not worth failing a boot over */
  }
}

/**
 * Read the persisted theme over the Wails bridge. The binding is loaded lazily
 * and guarded: ConfigService.GetTheme is added by the Go side of this change,
 * and a desktop binary predating it must still paint rather than throw.
 *
 * Returns undefined for "the bridge could not answer", which is deliberately
 * NOT the same as "the bridge said mocha": an unanswerable bridge must leave
 * the cached flavor standing and must not overwrite the cache with a default it
 * never actually read.
 */
async function readTheme(): Promise<ThemeId | undefined> {
  try {
    const svc = (await import("@bindings/desktop")).ConfigService as unknown as {
      GetTheme?: () => Promise<string>;
    };
    if (typeof svc.GetTheme !== "function") return undefined;
    return flavorFor(await svc.GetTheme()).id;
  } catch {
    return undefined;
  }
}

async function writeTheme(id: ThemeId): Promise<void> {
  const svc = (await import("@bindings/desktop")).ConfigService as unknown as {
    SetTheme?: (name: string) => Promise<void>;
  };
  if (typeof svc.SetTheme !== "function") throw new Error("this build cannot save a theme");
  await svc.SetTheme(id);
}

/**
 * The live appearance. Deliberately separate from `store.svelte.ts`, which
 * mirrors the daemon: presentation state has nothing to do with sessions, and
 * keeping them apart means a daemon that is down cannot take the theme with it.
 */
class Appearance {
  id = $state<ThemeId>(DEFAULT_THEME_ID);

  flavor = $derived(FLAVORS[this.id]);
  /** xterm.js theme for the current flavor (LiveTerminal spreads this). */
  term = $derived<TermTheme>(toXterm(FLAVORS[this.id]));
  /** Palette for the DOM snapshot renderer (SnapshotTile → ansi.ts). */
  ansi = $derived<AnsiPalette>(toAnsi(FLAVORS[this.id]));

  /** Paint the current flavor. Split out so `set` and `init` share one path. */
  paint(): void {
    applyFlavor(FLAVORS[this.id]);
  }

  /**
   * Boot in two steps, and the first one is synchronous on purpose.
   *
   * Step 1 paints the LAST FLAVOR THIS APP PAINTED, read from localStorage.
   * That is what keeps a non-default flavor from flashing mocha on every
   * launch: the tokens, `data-theme` and `color-scheme` are already right
   * before `mount()` runs, with no I/O of any kind.
   *
   * Step 2 asks config.toml over the bridge and repaints only if it disagrees.
   * config.toml remains the source of truth; the cache is only a head start.
   * A bridge that cannot answer (undefined) leaves step 1's paint standing.
   *
   * We do NOT gate mounting on step 2. Holding first paint until an IPC
   * round-trip completes would replace a possible one-repaint colour change
   * with a guaranteed blank window of unbounded length — strictly worse. The
   * residual flash is now only (a) the very first launch on a machine and
   * (b) config.toml edited behind the running app's back.
   */
  async init(): Promise<void> {
    this.id = cachedTheme();
    this.paint();
    const id = await readTheme();
    if (id === undefined) return;
    if (id !== this.id) {
      this.id = id;
      this.paint();
    }
    cacheTheme(id);
  }

  /**
   * Persist the flavor and remember it for the next boot. The ONLY writer —
   * the settings form used to call ConfigService.SetTheme itself, which wrote
   * config.toml but never the cache, so `init` kept painting the previous
   * flavor first and the flash this cache exists to remove came back on the
   * first launch after every theme change. Anything that changes [ui].theme
   * goes through here.
   *
   * Takes an id rather than reading `this.id` because the settings form
   * PREVIEWS: by the time it commits, `this.id` is already the new flavor and
   * a "did it change" guard here would skip the write entirely. Painting is
   * the caller's business (init paints from cache, the form paints on click);
   * this method only makes it durable.
   *
   * cacheTheme runs only after config.toml agrees, so the cache can never lead
   * the source of truth into a flavor that failed to save.
   */
  async commit(id: ThemeId): Promise<void> {
    await writeTheme(id);
    cacheTheme(id);
  }
}

export const appearance = new Appearance();
