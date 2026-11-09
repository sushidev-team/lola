// The runtime half of theming: which Catppuccin flavor is live, and how it
// reaches the DOM. `catppuccin.ts` stays pure data; everything that touches
// document, runes, or the Wails bridge lives here.
//
// HOW A TOKEN SWAP TAKES EFFECT — the non-obvious bit worth knowing before
// touching this file. Tailwind v4 compiles `@theme` into custom properties on
// :root and compiles every utility into a `var()` reference:
//
//     @layer theme { :root { --color-canvas: #121624; … } }
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

import { appearancePreference, resolveAppearance, type AppearancePreference } from "./appearance";
export type { AppearancePreference } from "./appearance";

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
  // app.css pins `color-scheme: dark` as the compiled default (Midnight, the
  // default flavor, is dark) so a cold start before JS still renders right.
  // This inline write is what flips native form controls, `<select>` popups and
  // UA scrollbars for latte.
  root.style.colorScheme = flavor.dark ? "dark" : "light";
}

const THEME_CACHE_KEY = "lola.theme";
const APPEARANCE_CACHE_KEY = "lola.appearance";

function cachedAppearance(): AppearancePreference {
  try {
    const raw = globalThis.localStorage?.getItem(APPEARANCE_CACHE_KEY);
    if (raw) return appearancePreference(JSON.parse(raw));
    return appearancePreference({ theme: globalThis.localStorage?.getItem(THEME_CACHE_KEY) });
  } catch {
    return appearancePreference(undefined);
  }
}

function cacheAppearance(preference: AppearancePreference, id: ThemeId): void {
  try {
    globalThis.localStorage?.setItem(APPEARANCE_CACHE_KEY, JSON.stringify(preference));
    globalThis.localStorage?.setItem(THEME_CACHE_KEY, id);
  } catch { /* Storage is optional; live appearance still works. */ }
}

type AppearanceService = {
  GetAppearance?: () => Promise<unknown>;
  SetAppearance?: (value: AppearancePreference) => Promise<void>;
  GetTheme?: () => Promise<string>;
  SetTheme?: (name: string) => Promise<void>;
  PreviewTheme?: (name: string) => Promise<void>;
};
let servicePromise: Promise<AppearanceService> | undefined;
function appearanceService(): Promise<AppearanceService> {
  // Startup paints and reads preferences together; share their bridge import.
  return servicePromise ??= import("@bindings/desktop").then(module => module.ConfigService as unknown as AppearanceService);
}
async function readAppearance(): Promise<AppearancePreference | undefined> {
  try {
    const svc = await appearanceService();
    if (typeof svc.GetAppearance === "function") return appearancePreference(await svc.GetAppearance());
    if (typeof svc.GetTheme === "function") return appearancePreference({ theme: await svc.GetTheme() });
  } catch { /* An unavailable bridge must leave the cached preferences intact. */ }
  return undefined;
}

/** Presentation state shared by desktop, mobile, xterm and pane snapshots. */
class Appearance {
  id = $state<ThemeId>(DEFAULT_THEME_ID);
  preference = $state<AppearancePreference>(appearancePreference(undefined));
  systemDark = $state(true);
  flavor = $derived(FLAVORS[this.id]);
  term = $derived<TermTheme>(toXterm(FLAVORS[this.id]));
  ansi = $derived<AnsiPalette>(toAnsi(FLAVORS[this.id]));
  private media?: MediaQueryList;
  private revision = 0;
  private paintRevision = 0;

  snapshot(): AppearancePreference {
    // Fixed-theme callers may still drive id directly (setup and old clients).
    return { ...this.preference, theme: this.preference.followSystem ? this.preference.theme : this.id };
  }

  private onSystemChange = (event: MediaQueryListEvent): void => {
    this.systemDark = event.matches;
    if (this.preference.followSystem) {
      this.id = resolveAppearance(this.preference, this.systemDark);
      this.paint();
    }
  };

  private watchSystem(): void {
    this.media?.removeEventListener("change", this.onSystemChange);
    this.media = typeof window.matchMedia === "function" ? window.matchMedia("(prefers-color-scheme: dark)") : undefined;
    this.systemDark = this.media?.matches ?? true;
    this.media?.addEventListener("change", this.onSystemChange);
  }

  paint(): void {
    applyFlavor(this.flavor);
    const revision = ++this.paintRevision;
    // The native title bar follows previews and OS changes without a config write.
    void appearanceService().then(svc => {
      if (revision === this.paintRevision) return svc.PreviewTheme?.(this.id);
    }).catch(() => { /* Browser/mobile/older binaries have no native window. */ });
  }

  preview(preference: AppearancePreference): void {
    this.revision++;
    this.watchSystem();
    this.preference = appearancePreference(preference);
    this.id = resolveAppearance(this.preference, this.systemDark);
    this.paint();
  }

  async init(): Promise<void> {
    this.preview(cachedAppearance());
    const revision = this.revision;
    const preference = await readAppearance();
    // A late startup read must not overwrite an in-progress user preview.
    if (!preference || revision !== this.revision) return;
    this.preview(preference);
    cacheAppearance(preference, this.id);
  }

  /** Persist only after the backend accepts it. Preview/cancel stays caller-owned. */
  async commit(value: ThemeId | AppearancePreference): Promise<void> {
    const svc = await appearanceService();
    const preference = typeof value === "string"
      ? appearancePreference({ ...this.snapshot(), theme: value, followSystem: false })
      : appearancePreference(value);
    if (typeof value === "string") {
      if (typeof svc.SetTheme !== "function") throw new Error("this build cannot save a theme");
      await svc.SetTheme(value);
    } else {
      if (typeof svc.SetAppearance !== "function") throw new Error("this build cannot save system appearance; update the desktop app");
      await svc.SetAppearance(preference);
    }
    cacheAppearance(preference, resolveAppearance(preference, this.systemDark));
  }

  /** The phone owns its appearance locally; it never changes the Mac's config. */
  commitLocal(preference: AppearancePreference): void {
    this.preview(preference);
    cacheAppearance(this.preference, this.id);
  }
}

export const appearance = new Appearance();
