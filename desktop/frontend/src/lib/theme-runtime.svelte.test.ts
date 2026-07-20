import { describe, it, expect, beforeEach, vi } from "vitest";
import { FLAVORS, THEME_IDS, toTokens, TOKEN_NAMES } from "./catppuccin";
import {
  TERM_FONT,
  appearance,
  applyFlavor,
  loadTermFont,
  termFontLoaded,
  termFontReady,
  DEFAULT_THEME_ID,
} from "./theme-runtime.svelte";

function root(): HTMLElement {
  return document.documentElement;
}

describe("applyFlavor", () => {
  beforeEach(() => {
    root().removeAttribute("style");
    root().removeAttribute("data-theme");
  });

  it("writes every token as an inline custom property", () => {
    const f = FLAVORS["catppuccin-macchiato"];
    applyFlavor(f);
    const want = toTokens(f);
    for (const name of TOKEN_NAMES)
      expect(root().style.getPropertyValue(name), name).toBe(want[name]);
  });

  it("stamps data-theme so CSS can branch on the flavor", () => {
    applyFlavor(FLAVORS["catppuccin-frappe"]);
    expect(root().dataset.theme).toBe("catppuccin-frappe");
  });

  it("flips color-scheme to light for latte and back for the dark flavors", () => {
    applyFlavor(FLAVORS["catppuccin-latte"]);
    expect(root().style.colorScheme).toBe("light");
    applyFlavor(FLAVORS["catppuccin-mocha"]);
    expect(root().style.colorScheme).toBe("dark");
  });

  it("leaves no stale token behind when switching flavors", () => {
    applyFlavor(FLAVORS["catppuccin-mocha"]);
    applyFlavor(FLAVORS["catppuccin-latte"]);
    const want = toTokens(FLAVORS["catppuccin-latte"]);
    for (const name of TOKEN_NAMES) expect(root().style.getPropertyValue(name)).toBe(want[name]);
  });

  it("accepts a detached root, so it never has to touch the live document", () => {
    const el = document.createElement("div");
    applyFlavor(FLAVORS["catppuccin-latte"], el);
    expect(el.style.getPropertyValue("--color-canvas")).toBe("#e6e9ef");
    expect(el.dataset.theme).toBe("catppuccin-latte");
  });

  it("is idempotent", () => {
    const el = document.createElement("div");
    applyFlavor(FLAVORS["catppuccin-frappe"], el);
    const once = el.getAttribute("style");
    applyFlavor(FLAVORS["catppuccin-frappe"], el);
    expect(el.getAttribute("style")).toBe(once);
  });
});

describe("appearance", () => {
  beforeEach(() => localStorage.clear());

  it("starts on the default flavor", () => {
    expect(appearance.id).toBe(DEFAULT_THEME_ID);
    expect(appearance.flavor.id).toBe(DEFAULT_THEME_ID);
  });

  it("derives a terminal theme and an ansi palette for the current flavor", () => {
    expect(appearance.term.background).toBe(FLAVORS[DEFAULT_THEME_ID].base);
    expect(appearance.ansi.ansi16).toHaveLength(16);
  });

  it("paints the current flavor onto the document", () => {
    root().removeAttribute("style");
    appearance.paint();
    expect(root().style.getPropertyValue("--color-canvas")).toBe(
      toTokens(FLAVORS[appearance.id])["--color-canvas"],
    );
  });

  it("init resolves without a live bridge and still paints", async () => {
    // The Wails binding is imported lazily and guarded, so a build whose
    // ConfigService predates GetTheme must fall back rather than throw.
    root().removeAttribute("style");
    await appearance.init();
    expect(appearance.id).toBe(DEFAULT_THEME_ID);
    expect(root().dataset.theme).toBe(DEFAULT_THEME_ID);
  });

  it("propagates a failed persist instead of swallowing it", async () => {
    // commit() deliberately does NOT paint or roll back — the settings form
    // owns the preview and its own revert path, and a commit that repainted
    // would fight it. What it must do is let the failure out so the caller can
    // flash it and keep the overlay open. (No Wails runtime under test, so the
    // binding rejects on its own.)
    const before = appearance.id;
    const target = THEME_IDS.find((id) => id !== before)!;
    await expect(appearance.commit(target)).rejects.toThrow();
    expect(appearance.id).toBe(before);
  });
});

describe("TERM_FONT", () => {
  it("reproduces Ghostty's measured 8x17 css cell at dpr 1 and 2", () => {
    // Replays xterm's own derivation (WebglRenderer): the cell is
    //   floor(advance*dpr) + round(letterSpacing)   wide   [device px]
    //   floor(ceil(charH*dpr) * lineHeight)         tall   [device px]
    // with WebKit's measured inputs for Hack@13.
    const advance = 13 * (1233 / 2048); // 7.826660 — Hack is monospace
    const charH = 15; // WebKit rounds fontBoundingBox asc 12 + desc 3
    for (const dpr of [1, 2]) {
      const cellW = Math.floor(advance * dpr) + Math.round(TERM_FONT.letterSpacing);
      const cellH = Math.floor(Math.ceil(charH * dpr) * TERM_FONT.lineHeight);
      expect(cellW % 1, `dpr ${dpr}`).toBe(0); // whole device pixels
      expect(cellW / dpr, `dpr ${dpr}`).toBe(8);
      expect(cellH / dpr, `dpr ${dpr}`).toBe(17);
    }
  });

  it("rejects the tempting 17/15 lineHeight that floors one pixel short", () => {
    // 17/15 is the EXCLUDED lower boundary of the valid interval. Written out
    // as a decimal — which is how anyone would actually type it — it lands
    // below the boundary and floor() silently loses the pixel.
    expect(Math.floor(15 * 1.1333333)).toBe(16); // the trap
    expect(Math.floor(15 * TERM_FONT.lineHeight)).toBe(17);
    expect(TERM_FONT.lineHeight).toBeGreaterThan(1.1333334);
    expect(TERM_FONT.lineHeight).toBeLessThan(1.1666666);
  });

  it("pins size and weights to what Hack actually ships", () => {
    expect(TERM_FONT.fontSize).toBe(13); // == Ghostty font-size
    expect(TERM_FONT.fontWeight).toBe(400);
    expect(TERM_FONT.fontWeightBold).toBe(700);
    expect(TERM_FONT.fontFamily).toContain("Hack");
    expect(TERM_FONT.allowTransparency).toBe(false);
  });
});

describe("first-paint flavor cache", () => {
  // The flash this defends against: index.html is a static file (main.go serves
  // it with a plain AssetFileServerFS, no templating), so nothing can put the
  // configured flavor in the document ahead of JS. The config read is async over
  // the Wails bridge, so without a cache every non-default flavor shows mocha
  // for the length of an IPC round-trip on every launch.
  beforeEach(() => {
    localStorage.clear();
    root().removeAttribute("style");
    root().removeAttribute("data-theme");
  });

  it("paints the cached flavor synchronously, before the bridge is consulted", async () => {
    localStorage.setItem("lola.theme", "catppuccin-latte");
    const pending = appearance.init(); // deliberately not awaited yet
    // Everything below must already be true in the same microtask: this is the
    // frame the user would otherwise see in the wrong colours.
    expect(appearance.id).toBe("catppuccin-latte");
    expect(root().dataset.theme).toBe("catppuccin-latte");
    expect(root().style.colorScheme).toBe("light");
    expect(root().style.getPropertyValue("--color-canvas")).toBe(
      toTokens(FLAVORS["catppuccin-latte"])["--color-canvas"],
    );
    await pending;
  });

  it("keeps the cached paint when the bridge cannot answer", async () => {
    // No live Wails binding under test, so readTheme() resolves undefined —
    // which must NOT be read as "config says mocha".
    localStorage.setItem("lola.theme", "catppuccin-frappe");
    await appearance.init();
    expect(appearance.id).toBe("catppuccin-frappe");
    expect(localStorage.getItem("lola.theme")).toBe("catppuccin-frappe");
  });

  it("falls back to the default for an absent or corrupt cache entry", async () => {
    await appearance.init();
    expect(appearance.id).toBe(DEFAULT_THEME_ID);

    localStorage.setItem("lola.theme", "solarized-hotdog");
    await appearance.init();
    expect(appearance.id).toBe(DEFAULT_THEME_ID);
  });

  it("never lets the cache lead config.toml: a failed write caches nothing", async () => {
    localStorage.setItem("lola.theme", DEFAULT_THEME_ID);
    const target = THEME_IDS.find((id) => id !== appearance.id)!;
    await expect(appearance.commit(target)).rejects.toThrow();
    expect(localStorage.getItem("lola.theme")).toBe(DEFAULT_THEME_ID);
  });
});

describe("terminal font loading", () => {
  // xterm measures the cell exactly once, inside Terminal.open()
  // (Terminal.ts:570), and re-measures only on a fontFamily/fontSize change
  // (CharSizeService.ts:34) or after a real resize (Terminal.ts:1214). So the
  // font has to be there BEFORE open(), or the 8x17 cell above is never
  // reached — LiveTerminal awaits termFontReady() for exactly that reason.
  const fakeFonts = (load: FontFaceSet["load"]) => ({ load }) as unknown as FontFaceSet;

  it("requests the vendored face at the size the cell arithmetic assumes", async () => {
    const load = vi.fn(async () => [{} as FontFace]);
    expect(await loadTermFont(fakeFonts(load))).toBe(true);
    expect(load).toHaveBeenCalledWith(`${TERM_FONT.fontWeight} ${TERM_FONT.fontSize}px "Hack"`);
  });

  it("requests the bold face too, so the atlas cannot cache a fallback bold", async () => {
    // Only the regular face decides the cell (every Hack glyph shares one
    // advance), but the WebGL atlas caches a rasterised glyph under a key that
    // does not record which face answered. A bold glyph drawn before
    // hack-bold.woff2 arrives is therefore stuck as fallback bold for the
    // terminal's lifetime — the same permanent-staleness bug as the regular
    // face, one weight over, and far easier to miss by eye.
    const load = vi.fn(async () => [{} as FontFace]);
    await loadTermFont(fakeFonts(load));
    expect(load).toHaveBeenCalledWith(`${TERM_FONT.fontWeightBold} ${TERM_FONT.fontSize}px "Hack"`);
  });

  it("still fails when the regular face is missing but bold resolves", async () => {
    // The verdict has to track the face that gates the cell, not whichever call
    // happened to return something — otherwise a missing hack-regular.woff2
    // would report success and open the terminal on fallback metrics.
    const load = vi.fn(async (spec: string) =>
      spec.startsWith(`${TERM_FONT.fontWeightBold} `) ? [{} as FontFace] : [],
    );
    expect(await loadTermFont(fakeFonts(load))).toBe(false);
  });

  it("reports failure rather than throwing when the face cannot load", async () => {
    // No FontFaceSet at all (jsdom, an old webview).
    expect(await loadTermFont(undefined)).toBe(false);
    // A FontFaceSet without the API.
    expect(await loadTermFont({} as FontFaceSet)).toBe(false);
    // src 404s: the spec resolves with an empty list rather than rejecting.
    expect(await loadTermFont(fakeFonts(async () => []))).toBe(false);
    // And an outright rejection is still not allowed to escape.
    expect(
      await loadTermFont(
        fakeFonts(() => Promise.reject(new Error("network"))),
      ),
    ).toBe(false);
  });

  it("loads once for the whole app, however many terminals mount", () => {
    // Idempotent by identity, not just by value: N LiveTerminals awaiting this
    // must not trigger N font loads.
    expect(termFontLoaded()).toBe(termFontLoaded());
  });

  it("resolves false instead of hanging when the load never settles", async () => {
    // A terminal that never opens is worse than a terminal with the wrong cell,
    // so the wait is bounded; LiveTerminal recovers via termFontLoaded() if the
    // face lands after the bound.
    vi.resetModules();
    const had = Object.getOwnPropertyDescriptor(document, "fonts");
    Object.defineProperty(document, "fonts", {
      configurable: true,
      value: { load: () => new Promise<FontFace[]>(() => {}) },
    });
    try {
      const fresh = await import("./theme-runtime.svelte");
      await expect(fresh.termFontReady(5)).resolves.toBe(false);
    } finally {
      if (had) Object.defineProperty(document, "fonts", had);
      else delete (document as unknown as Record<string, unknown>).fonts;
    }
  });

  it("resolves true as soon as the face is there, without waiting out the bound", async () => {
    vi.resetModules();
    const had = Object.getOwnPropertyDescriptor(document, "fonts");
    Object.defineProperty(document, "fonts", {
      configurable: true,
      value: { load: async () => [{} as FontFace] },
    });
    try {
      const fresh = await import("./theme-runtime.svelte");
      const started = Date.now();
      await expect(fresh.termFontReady(60_000)).resolves.toBe(true);
      expect(Date.now() - started).toBeLessThan(1_000);
    } finally {
      if (had) Object.defineProperty(document, "fonts", had);
      else delete (document as unknown as Record<string, unknown>).fonts;
    }
  });
});
