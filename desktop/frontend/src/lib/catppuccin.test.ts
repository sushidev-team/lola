import { describe, it, expect } from "vitest";
// Read app.css straight off disk to diff its compiled @theme literals against
// the mapping they mirror. `?raw` is not an option — the Tailwind Vite plugin
// claims .css and hands back an empty module. @types/node is deliberately not a
// dependency (nothing in the app runs under node), so the builtin is asserted
// in rather than typed.
// @ts-expect-error node builtin, available under vitest, untyped here
import { readFileSync } from "node:fs";
declare const process: { cwd(): string };
import {
  AA,
  AA_UI,
  MUTED,
  DEFAULT_THEME_ID,
  FLAVORS,
  THEME_IDS,
  TOKEN_NAMES,
  contrast,
  flavorFor,
  luminance,
  mix,
  muted,
  onFill,
  panelBg,
  readable,
  toAnsi,
  toTokens,
  toXterm,
  visible,
  type Flavor,
} from "./catppuccin";

const HEX = /^#[0-9a-f]{6}$/;
const flavors = THEME_IDS.map((id) => FLAVORS[id]);

const NAMED_KEYS = [
  "rosewater", "flamingo", "pink", "mauve", "red", "maroon", "peach", "yellow",
  "green", "teal", "sky", "sapphire", "blue", "lavender", "text", "subtext1",
  "subtext0", "overlay2", "overlay1", "overlay0", "surface2", "surface1",
  "surface0", "base", "mantle", "crust",
] as const;

describe("FLAVORS", () => {
  it("carries exactly the four ids the Go side validates", () => {
    // These strings must match config.UIThemes (internal/config/ui.go); a drift
    // here writes a config.toml the daemon rejects.
    expect(THEME_IDS).toEqual([
      "catppuccin-mocha",
      "catppuccin-macchiato",
      "catppuccin-frappe",
      "catppuccin-latte",
    ]);
    expect(Object.keys(FLAVORS)).toEqual(THEME_IDS);
    expect(THEME_IDS).toContain(DEFAULT_THEME_ID);
  });

  it("keys every flavor by its own id", () => {
    for (const id of THEME_IDS) expect(FLAVORS[id].id).toBe(id);
  });

  it("defines all 26 named colors as lowercase 6-digit hex", () => {
    for (const f of flavors)
      for (const k of NAMED_KEYS) expect(f[k], `${f.id}.${k}`).toMatch(HEX);
  });

  it("gives every flavor 16 valid ansi entries", () => {
    for (const f of flavors) {
      expect(f.ansi16, f.id).toHaveLength(16);
      for (const c of f.ansi16) expect(c, f.id).toMatch(HEX);
    }
  });

  it("marks only latte as light", () => {
    for (const f of flavors) expect(f.dark, f.id).toBe(f.id !== "catppuccin-latte");
  });

  it("keeps the surface ramp ordered so the token mapping stays structural", () => {
    // Two separate invariants, and the difference between them is exactly why
    // the mapping can be flavor-agnostic:
    //
    //  * crust < mantle < base by luminance in EVERY flavor, including latte —
    //    so canvas (mantle) always sits below panel (base) and the elevation
    //    direction never inverts.
    //  * surface0..2 step AWAY from base, which means lighter on the dark
    //    flavors and darker on latte. Ordinal meaning is preserved either way,
    //    so sel (surface0) is always one step off panel and edge (surface1) is
    //    always a visible rule.
    for (const f of flavors) {
      expect(luminance(f.crust), `${f.id} crust<mantle`).toBeLessThan(luminance(f.mantle));
      expect(luminance(f.mantle), `${f.id} mantle<base`).toBeLessThan(luminance(f.base));
      const step = f.dark ? 1 : -1;
      const ramp = [f.base, f.surface0, f.surface1, f.surface2];
      for (let i = 1; i < ramp.length; i++)
        expect(
          Math.sign(luminance(ramp[i]) - luminance(ramp[i - 1])),
          `${f.id} surface step ${i}`,
        ).toBe(step);
    }
  });

  it("is frozen so a consumer cannot mutate the shared palette", () => {
    expect(Object.isFrozen(FLAVORS)).toBe(true);
    expect(Object.isFrozen(FLAVORS[DEFAULT_THEME_ID])).toBe(true);
  });
});

describe("flavorFor", () => {
  it("resolves known ids", () => {
    expect(flavorFor("catppuccin-latte").id).toBe("catppuccin-latte");
  });
  it("falls back to the default for unknown/empty/nullish", () => {
    for (const bad of ["", "dracula", null, undefined])
      expect(flavorFor(bad).id).toBe(DEFAULT_THEME_ID);
  });
});

describe("mix / luminance / contrast", () => {
  it("is an endpoint-exact sRGB lerp", () => {
    expect(mix("#000000", "#ffffff", 1)).toBe("#000000");
    expect(mix("#000000", "#ffffff", 0)).toBe("#ffffff");
    expect(mix("#000000", "#ffffff", 0.5)).toBe("#808080");
  });
  it("is deterministic", () => {
    expect(mix("#89b4fa", "#313244", 0.28)).toBe(mix("#89b4fa", "#313244", 0.28));
  });
  it("orders luminance and ratios sanely", () => {
    expect(luminance("#ffffff")).toBeCloseTo(1, 5);
    expect(luminance("#000000")).toBeCloseTo(0, 5);
    expect(contrast("#ffffff", "#000000")).toBeCloseTo(21, 5);
  });
});

describe("onFill", () => {
  it("is never worse than any end of the flavor's own ramp", () => {
    for (const f of flavors)
      for (const fill of [f.peach, f.red, f.green, f.blue, f.sky]) {
        const got = contrast(onFill(f, fill), fill);
        for (const c of [f.crust, f.base, f.text])
          expect(got, `${f.id} on ${fill}`).toBeGreaterThanOrEqual(contrast(c, fill));
      }
  });

  it("clears AA on every fill, in every flavor", () => {
    // This assertion used to be scoped to `flavors.filter(x => x.dark)`, which
    // is what let latte's peach ship at 2.68:1 — under `needs_input`, the pill
    // that most has to be read. There is no carve-out any more: where the ramp
    // cannot label a fill, onFill walks past it until the measurement passes.
    for (const f of flavors)
      for (const fill of [f.peach, f.red, f.sky])
        expect(contrast(onFill(f, fill), fill), `${f.id} on ${fill}`).toBeGreaterThanOrEqual(AA);
  });

  it("stays inside the palette wherever the palette suffices", () => {
    // Proof the walk is a light-flavor rescue and not a redesign: on all three
    // dark flavors every fill still resolves to a ramp end verbatim, so their
    // pills and chips are exactly what they were. Only latte spends palette,
    // and only on the two fills that cannot carry a label — red still lands on
    // plain `base`.
    const l = FLAVORS["catppuccin-latte"];
    for (const f of flavors.filter((x) => x.dark))
      for (const fill of [f.peach, f.red, f.sky, f.green, f.blue])
        expect([f.crust, f.base, f.text], `${f.id} on ${fill}`).toContain(onFill(f, fill));
    expect(onFill(l, l.red)).toBe(l.base);
    for (const fill of [l.peach, l.sky])
      expect([l.crust, l.base, l.text], `latte on ${fill}`).not.toContain(onFill(l, fill));
  });

  it("spends the minimum it can rather than collapsing to the absolute end", () => {
    // The walk stops at the first step that clears the floor, so the rescue
    // keeps as much of the ramp end as AA allows: latte's labels come back as
    // darkened `text`, not as raw black.
    const l = FLAVORS["catppuccin-latte"];
    for (const fill of [l.peach, l.sky]) {
      const got = onFill(l, fill);
      expect(got, `latte on ${fill}`).not.toBe("#000000");
      expect(contrast(got, fill), `latte on ${fill}`).toBeGreaterThanOrEqual(AA);
      expect(contrast(got, fill), `latte on ${fill} vs black`).toBeLessThan(contrast("#000000", fill));
    }
  });

  it("picks the near-black end on a dark flavor's alarm fill", () => {
    const m = FLAVORS["catppuccin-mocha"];
    expect(onFill(m, m.peach)).toBe(m.crust);
  });
  it("picks the light end on latte's red", () => {
    const l = FLAVORS["catppuccin-latte"];
    expect(onFill(l, l.red)).toBe(l.base);
  });
});

describe("readable", () => {
  it("returns the color untouched when it already clears AA", () => {
    const m = FLAVORS["catppuccin-mocha"];
    expect(readable(m.sky, m.crust)).toBe(m.sky);
  });

  it("keeps the most color that still clears the floor", () => {
    // The walk starts at 19/20 of the original, so the answer is never more
    // blended than it has to be.
    const l = FLAVORS["catppuccin-latte"];
    const bg = mix(l.green, l.mantle, 0.28);
    const got = readable(l.green, bg);
    expect(contrast(got, bg)).toBeGreaterThanOrEqual(AA);
    expect(got).not.toBe(l.green);
    expect(got).not.toBe(l.text); // a blend, not a collapse to plain ink
  });

  it("never reorders the channels, so a walked color keeps its hue", () => {
    // This is the property the anchor was changed for, asserted as a property
    // rather than as hex: blending toward black or white scales the channels,
    // blending toward latte's slate `text` does not, and it was reordering
    // that turned green/yellow/peach into three indistinguishable greys.
    const order = (c: string) => {
      const [r, g, b] = [1, 3, 5].map((i) => parseInt(c.slice(i, i + 2), 16));
      return [r >= g, g >= b, r >= b].join();
    };
    for (const f of flavors) {
      const bgs = [f.mantle, panelBg(f), f.surface0];
      for (const src of [f.green, f.yellow, f.peach, f.red, f.blue, f.mauve, f.sky]) {
        expect(order(readable(src, ...bgs)), `${f.id} ${src}`).toBe(order(src));
        expect(order(visible(src, ...bgs)), `${f.id} ${src} ui`).toBe(order(src));
      }
    }
  });

  it("satisfies every background it is given, not just the first", () => {
    for (const f of flavors) {
      const bgs = [f.surface0, panelBg(f), f.mantle];
      const got = readable(f.sky, ...bgs);
      for (const bg of bgs) expect(contrast(got, bg), `${f.id} on ${bg}`).toBeGreaterThanOrEqual(AA);
    }
  });
});

describe("visible", () => {
  it("spends less than readable, because 1.4.11 asks for less", () => {
    // A border walked to AA would be a much darker accent than the design
    // wants. Latte is the flavor where the two actually differ.
    const l = FLAVORS["catppuccin-latte"];
    const bgs = [l.mantle, panelBg(l), l.surface0];
    const border = visible(l.sky, ...bgs);
    const text = readable(l.sky, ...bgs);
    expect(border).not.toBe(text);
    expect(contrast(border, panelBg(l))).toBeLessThan(contrast(text, panelBg(l)));
  });

  it("clears the 3:1 non-text floor on every surface, in every flavor", () => {
    for (const f of flavors) {
      const bgs = [f.mantle, panelBg(f), f.surface0];
      const got = visible(f.sky, ...bgs);
      for (const bg of bgs)
        expect(contrast(got, bg), `${f.id} on ${bg}`).toBeGreaterThanOrEqual(AA_UI);
    }
  });
});

describe("muted / faint", () => {
  // The bug this guards: --color-faint was the raw overlay1/overlay2 with no
  // contrast check, so on frappé and latte it fell to 2.80:1 / 2.56:1 on a
  // selected row — an unreadable smudge exactly where a row is highlighted.
  it("holds --color-faint to the 3:1 MUTED floor on every surface, every flavor", () => {
    for (const f of flavors) {
      const t = toTokens(f);
      const surfaces = [t["--color-canvas"], panelBg(f), t["--color-sel"]];
      for (const bg of surfaces)
        expect(contrast(t["--color-faint"], bg), `${f.id} faint on ${bg}`).toBeGreaterThanOrEqual(MUTED);
    }
  });

  // The point of a 3:1 floor rather than AA: faint must stay DE-EMPHASIZED, a
  // clear step under ink, or it stops being faint. This is the property the
  // hierarchy depends on — enforced, not just asserted in a comment.
  it("keeps faint well below ink so the hierarchy survives", () => {
    for (const f of flavors) {
      const t = toTokens(f);
      const panel = panelBg(f);
      expect(contrast(t["--color-faint"], panel), f.id).toBeLessThan(
        contrast(t["--color-ink"], panel) * 0.75,
      );
    }
  });

  // A muted fix, not a redesign: the default flavor (and macchiato) already
  // clear 3:1 raw, so faint must come back byte-identical to the raw palette
  // name there. Only frappé and latte are allowed to move.
  it("leaves the flavors that already clear 3:1 untouched", () => {
    for (const id of ["catppuccin-mocha", "catppuccin-macchiato"] as const) {
      const f = FLAVORS[id];
      expect(toTokens(f)["--color-faint"], id).toBe(f[f.faintKey]);
    }
  });

  it("muted() spends no more than readable() — a floor below AA", () => {
    const l = FLAVORS["catppuccin-latte"];
    const bgs = [l.mantle, panelBg(l), l.surface0] as const;
    const m = muted(l.overlay2, ...bgs);
    const r = readable(l.overlay2, ...bgs);
    // On the surface where readable has to spend, muted's contrast is no higher.
    expect(contrast(m, panelBg(l))).toBeLessThanOrEqual(contrast(r, panelBg(l)));
  });
});

describe("panelBg", () => {
  it("mirrors the color-mix in app.css's @utility panel", () => {
    // If these drift, every contrast number derived from panelBg is measuring
    // a surface no component actually paints.
    const css: string = readFileSync(process.cwd() + "/src/app.css", "utf8");
    expect(css).toContain(
      "background: color-mix(in srgb, var(--color-panel) 82%, var(--color-canvas));",
    );
    for (const f of flavors) expect(panelBg(f), f.id).toBe(mix(f.base, f.mantle, 0.82));
  });
});

describe("accent legibility", () => {
  // The defect this guards: --color-accent is `sky`, which Catppuccin tunes to
  // sit on the flavor's own background. Latte's sky is mid-luminance, so accent
  // text on a near-white panel was 2.44:1 and 2.03:1 inside the button fill —
  // below even the 3:1 large-text floor, across every panel title, modal title,
  // focused tab and primary button. --color-accent-ink is the measured split.
  it("clears AA on every surface accent text lands on, in every flavor", () => {
    for (const f of flavors) {
      const t = toTokens(f);
      const ink = t["--color-accent-ink"];
      const surfaces = {
        "accent fill": t["--color-accent-fill"],
        "accent fill (hover)": t["--color-accent-fill-hover"],
        panel: panelBg(f),
        canvas: t["--color-canvas"],
        sel: t["--color-sel"],
      };
      for (const [name, bg] of Object.entries(surfaces))
        expect(contrast(ink, bg), `${f.id} accent ink on ${name}`).toBeGreaterThanOrEqual(AA);
    }
  });

  it("clears the non-text floor as a border and a focus ring", () => {
    // --color-accent is never text — it is `border-accent`, the input
    // `focus:border-accent` ring, `ring-accent`, `accent-accent` and the
    // `bg-accent` chip. WCAG 1.4.11 wants 3:1 against what it is drawn on;
    // latte's raw sky was 2.44 on a panel and 1.81 on a selected row, so the
    // focus indicator on every form in the app was under the floor.
    for (const f of flavors) {
      const t = toTokens(f);
      const surfaces = { panel: panelBg(f), canvas: t["--color-canvas"], sel: t["--color-sel"] };
      for (const [name, bg] of Object.entries(surfaces))
        expect(contrast(t["--color-accent"], bg), `${f.id} accent on ${name}`).toBeGreaterThanOrEqual(
          AA_UI,
        );
    }
  });

  it("leaves the dark flavors on plain sky", () => {
    // Proof the split is latte-shaped and not a redesign: readable() finds sky
    // already sufficient everywhere else, so mocha/macchiato/frappe render
    // exactly as before and app.css's compiled default does not move.
    for (const f of flavors)
      expect(toTokens(f)["--color-accent-ink"] === f.sky, f.id).toBe(f.dark);
  });

  it("hovering a primary button gains contrast instead of losing it", () => {
    // The old recipe deepened the tint (bg-accent/20 -> /30), which pulls the
    // fill toward accent-colored text: hover was always the worse state. The
    // hover fill now re-mixes into the ramp end furthest from the text.
    for (const f of flavors) {
      const t = toTokens(f);
      const ink = t["--color-accent-ink"];
      expect(
        contrast(ink, t["--color-accent-fill-hover"]),
        `${f.id} hover`,
      ).toBeGreaterThan(contrast(ink, t["--color-accent-fill"]));
    }
  });
});

describe("semantic text tokens", () => {
  // The six colors theme.ts turns a status into. Rail, SessionsTable,
  // SessionsKanban, PrBadge, VitalsBar, DoctorOverlay and ProjectDetail print
  // them BARE — no fill of their own — so the surface underneath is whatever
  // the row happens to be sitting on.
  const SEMANTIC = ["good", "bad", "warn", "info", "orange", "magenta"];

  it("clears AA on canvas, panel and a selected row, in every flavor", () => {
    // What this guards: the tokens used to be raw Catppuccin names, which
    // Catppuccin tunes to be seen against the flavor's background rather than
    // to carry 11px type on it. On latte that meant green 2.92, yellow 2.29
    // and peach 2.61 on a panel — statusText("approved") and
    // statusText("needs_input") were effectively unreadable — and on frappé
    // red/blue/peach/mauve missed on the selected band at 3.57-4.45.
    for (const f of flavors) {
      const t = toTokens(f);
      const surfaces = { canvas: t["--color-canvas"], panel: panelBg(f), sel: t["--color-sel"] };
      for (const name of SEMANTIC)
        for (const [where, bg] of Object.entries(surfaces))
          expect(
            contrast(t[`--color-${name}`], bg),
            `${f.id} --color-${name} on ${where}`,
          ).toBeGreaterThanOrEqual(AA);
    }
  });

  it("keeps most of each color's chroma, so a status still reads as a color", () => {
    // The reason the walk anchors on black/white and not on the flavor's own
    // `text`: toward latte's slate ink the six landed at 6-19% of their
    // original chroma — green, yellow and peach all became the same grey and
    // the status color stopped carrying any status. Toward black the worst
    // case is latte's yellow at 55%. Asserted as a ratio rather than as hex so
    // a re-mapped token has to re-measure instead of re-baselining.
    const chroma = (c: string) => {
      const v = [1, 3, 5].map((i) => parseInt(c.slice(i, i + 2), 16));
      return Math.max(...v) - Math.min(...v);
    };
    const RAW: Record<string, keyof Flavor> = {
      good: "green", bad: "red", warn: "yellow",
      info: "blue", orange: "peach", magenta: "mauve",
    };
    for (const f of flavors) {
      const t = toTokens(f);
      for (const name of SEMANTIC) {
        const raw = chroma(f[RAW[name]] as string);
        const got = chroma(t[`--color-${name}`]);
        expect(got / raw, `${f.id} --color-${name}`).toBeGreaterThan(0.5);
      }
    }
  });

  it("moves only what misses the floor, and only on the flavors that miss it", () => {
    // Proof this is a latte-shaped fix, not a redesign. Mocha and macchiato
    // carry all six at AA already and come back verbatim, so app.css's
    // compiled Mocha default does not move. Frappé moves exactly the four that
    // miss on the selected band (red 3.57, blue 4.10, mauve 4.30, peach 4.45)
    // and keeps green and yellow. Latte, whose accents are tuned for a
    // near-white background, moves all six.
    const moved = (f: Flavor) =>
      SEMANTIC.filter((n) => toTokens(f)[`--color-${n}`] !== (f[
        ({ good: "green", bad: "red", warn: "yellow", info: "blue", orange: "peach", magenta: "mauve" } as Record<string, keyof Flavor>)[n]
      ] as string));
    expect(moved(FLAVORS["catppuccin-mocha"])).toEqual([]);
    expect(moved(FLAVORS["catppuccin-macchiato"])).toEqual([]);
    expect(moved(FLAVORS["catppuccin-frappe"])).toEqual(["bad", "info", "orange", "magenta"]);
    expect(moved(FLAVORS["catppuccin-latte"])).toEqual(SEMANTIC);
  });
});

describe("inverse pairs", () => {
  // Both tokens here replace a foreground that was hardcoded to one end of the
  // ramp and so could only be right in one direction: `canvas` used as the
  // label on the selected lens/agent/scope chips (canvas is near-white on
  // latte, so the selected chip was the least readable of the row at 2.30:1),
  // and Tailwind's built-in white on the `dead` pill — a color no flavor can
  // override, and the worse of the two at 2.32:1 on the DEFAULT flavor.
  it("clears AA on the fill it names, in every flavor", () => {
    for (const f of flavors) {
      const t = toTokens(f);
      for (const [fg, bg] of [
        ["--color-on-accent", "--color-accent"],
        ["--color-on-bad", "--color-bad"],
      ])
        expect(contrast(t[fg], t[bg]), `${f.id} ${fg} on ${bg}`).toBeGreaterThanOrEqual(AA);
    }
  });

  it("beats both hardcoded foregrounds it replaces, in every flavor", () => {
    // Not just "passes" — strictly better than what shipped, everywhere. The
    // one number that moves down is latte's dead pill (white happened to be
    // legible on latte's dark red), so that pair is checked for AA above and
    // excluded here rather than quietly asserted away.
    for (const f of flavors) {
      const t = toTokens(f);
      expect(
        contrast(t["--color-on-accent"], t["--color-accent"]),
        `${f.id} on-accent vs canvas`,
      ).toBeGreaterThan(contrast(t["--color-canvas"], t["--color-accent"]));
      if (f.dark)
        expect(
          contrast(t["--color-on-bad"], t["--color-bad"]),
          `${f.id} on-bad vs white`,
        ).toBeGreaterThan(contrast("#ffffff", t["--color-bad"]));
    }
  });

  it("keeps the dark flavors on the same near-black end of the ramp", () => {
    // `canvas` as a label meant mantle; on-accent measures its way to crust, one step
    // further down the same ramp. Visually indistinguishable on a bright chip,
    // which is the point: this is a latte fix, not a restyle of the default.
    for (const f of flavors.filter((x) => x.dark)) {
      const t = toTokens(f);
      expect(t["--color-on-accent"], f.id).toBe(f.crust);
      expect(t["--color-on-bad"], f.id).toBe(f.crust);
    }
  });
});

describe("placeholder", () => {
  it("clears AA on the input fill, in every flavor", () => {
    // The sites used to ask for `faint` at 50% alpha. An alpha of an
    // already-muted token composites over whatever is behind the input, so the
    // real ratio is unknowable in general and was 2.12:1 on mocha / 1.73:1 on
    // latte in practice. Opaque and measured against the fill the inputs
    // actually paint, which is canvas.
    for (const f of flavors)
      expect(
        contrast(toTokens(f)["--color-placeholder"], toTokens(f)["--color-canvas"]),
        `${f.id} placeholder`,
      ).toBeGreaterThanOrEqual(AA);
  });

  it("stays plain faint wherever faint already clears the floor", () => {
    // Mocha and macchiato are untouched — their overlay1 is already 4.75:1 and
    // 4.53:1 on canvas. Frappe (4.09:1) and latte (3.25:1) are not, so those
    // two get a measured nudge toward ink rather than a blanket new color.
    for (const f of flavors) {
      const t = toTokens(f);
      const alreadyAA = contrast(t["--color-faint"], t["--color-canvas"]) >= AA;
      expect(t["--color-placeholder"] === t["--color-faint"], f.id).toBe(alreadyAA);
    }
  });

  it("still reads as muted, not as body text", () => {
    for (const f of flavors) {
      const t = toTokens(f);
      expect(
        contrast(t["--color-placeholder"], t["--color-canvas"]),
        f.id,
      ).toBeLessThan(contrast(t["--color-ink"], t["--color-canvas"]));
    }
  });
});

describe("status pills", () => {
  it("clears AA on every tinted pill, in every flavor", () => {
    // Tinted pills used to blend the accent into `surface0` and then print the
    // accent on top. surface0 steps AWAY from base — lighter on the dark
    // flavors, darker on latte — i.e. toward the accent in both directions, so
    // the pill squeezed its own label: latte's done pill was 1.75:1 and work
    // 2.31:1 on 11px text. Tinting into `mantle` is what makes AA reachable;
    // readable() then picks the label. Solid pills keep the onFill rule above.
    for (const f of flavors) {
      const t = toTokens(f);
      for (const kind of ["work", "done", "grey"])
        expect(
          contrast(t[`--color-pill-${kind}-fg`], t[`--color-pill-${kind}`]),
          `${f.id} pill-${kind}`,
        ).toBeGreaterThanOrEqual(AA);
    }
  });

  it("clears AA on every SOLID pill, in every flavor", () => {
    // The alarm pills, bound at the token level rather than through onFill, so
    // a future remap of --color-pill-urgent to some other named color has to
    // re-measure. Latte's urgent was the hole: peach is mid-luminance and its
    // best on-palette label reached 2.68:1 under `needs_input`.
    for (const f of flavors) {
      const t = toTokens(f);
      for (const kind of ["urgent", "broken"])
        expect(
          contrast(t[`--color-pill-${kind}-fg`], t[`--color-pill-${kind}`]),
          `${f.id} pill-${kind}`,
        ).toBeGreaterThanOrEqual(AA);
    }
  });

  it("keeps a tinted pill distinguishable from the panel it sits on", () => {
    // Lifting the label must not cost the pill its shape — a tint that landed
    // on the panel color would be no pill at all.
    for (const f of flavors)
      for (const kind of ["work", "done"])
        expect(
          contrast(toTokens(f)[`--color-pill-${kind}`], panelBg(f)),
          `${f.id} pill-${kind} vs panel`,
        ).toBeGreaterThan(1.25);
  });

  it("still labels mocha's tinted pills in their own hue", () => {
    // The point of readable() over a blanket onFill(): where the palette can
    // carry the hue at AA, it keeps it. The default theme is unchanged.
    const m = FLAVORS["catppuccin-mocha"];
    const t = toTokens(m);
    expect(t["--color-pill-work-fg"]).toBe(m.blue);
    expect(t["--color-pill-done-fg"]).toBe(m.green);
  });
});

describe("toTokens", () => {
  it("produces the same token set for every flavor", () => {
    for (const f of flavors) expect(Object.keys(toTokens(f)), f.id).toEqual(TOKEN_NAMES);
  });

  it("emits only valid hex", () => {
    for (const f of flavors)
      for (const [k, v] of Object.entries(toTokens(f))) expect(v, `${f.id} ${k}`).toMatch(HEX);
  });

  it("maps the tokens the components already use", () => {
    const t = toTokens(FLAVORS["catppuccin-mocha"]);
    expect(t["--color-canvas"]).toBe("#181825"); // mantle
    expect(t["--color-panel"]).toBe("#1e1e2e"); // base
    expect(t["--color-edge"]).toBe("#45475a"); // surface1
    expect(t["--color-accent"]).toBe("#89dceb"); // sky — the old cyan's nearest hue
    expect(t["--color-ink"]).toBe("#cdd6f4"); // text
    expect(t["--color-good"]).toBe("#a6e3a1"); // green
    expect(t["--color-bad"]).toBe("#f38ba8"); // red
    expect(t["--color-warn"]).toBe("#f9e2af"); // yellow
  });

  it("backs faint with overlay2 on latte and overlay1 on mocha, then walks to MUTED", () => {
    // faintKey selects the raw name (overlay2 keeps latte's label legible on a
    // light base); --color-faint is that name run through muted(). Mocha already
    // clears 3:1, so it stays the raw overlay1; latte's overlay2 gets nudged.
    const l = FLAVORS["catppuccin-latte"];
    expect(l.faintKey).toBe("overlay2");
    expect(toTokens(l)["--color-faint"]).toBe(muted(l.overlay2, l.mantle, panelBg(l), l.surface0));

    const m = FLAVORS["catppuccin-mocha"];
    expect(m.faintKey).toBe("overlay1");
    expect(toTokens(m)["--color-faint"]).toBe(m.overlay1); // unchanged: already ≥3:1
  });

  it("keeps the terminal background identical to --color-panel", () => {
    // This is what replaced the hand-picked #111927: the terminal blends into
    // the panel by construction, in every flavor.
    for (const f of flavors) expect(toXterm(f).background).toBe(toTokens(f)["--color-panel"]);
  });
});

describe("app.css compiled defaults", () => {
  it("still equals toTokens(mocha)", () => {
    // app.css ships Mocha as the pre-JS default so a cold start does not flash.
    // If this fails, regenerate the @theme literals — do not weaken the test.
    const css: string = readFileSync(process.cwd() + "/src/app.css", "utf8");
    const block = css.slice(css.indexOf("@theme {"), css.indexOf("--font-sans"));
    const found: Record<string, string> = {};
    for (const m of block.matchAll(/(--color-[a-z-]+):\s*(#[0-9a-f]{6})/g)) found[m[1]] = m[2];
    expect(found).toEqual(toTokens(FLAVORS["catppuccin-mocha"]));
  });
});

describe("toXterm", () => {
  it("fills every slot from the flavor's ansi table", () => {
    for (const f of flavors) {
      const t = toXterm(f);
      expect(Object.values(t).every((v) => HEX.test(v)), f.id).toBe(true);
      expect(t.black).toBe(f.ansi16[0]);
      expect(t.brightWhite).toBe(f.ansi16[15]);
      expect(t.foreground).toBe(f.text);
    }
  });

  it("keeps Ghostty's brightened 9-14, which differ from the normal colors", () => {
    // Not a transcription slip: Ghostty ships perceptually brightened variants
    // at 9-14 rather than duplicating 1-6, and matching the user's terminal is
    // the goal. lola's old table duplicated them.
    const t = toXterm(FLAVORS["catppuccin-mocha"]);
    expect(t.brightRed).toBe("#f37799");
    expect(t.red).toBe("#f38ba8");
    expect(t.brightRed).not.toBe(t.red);
    expect(t.brightCyan).not.toBe(t.cyan);
  });
});

describe("toAnsi", () => {
  it("carries the flavor's own inverse-video fallbacks", () => {
    for (const f of flavors) {
      const p = toAnsi(f);
      expect(p.ansi16).toHaveLength(16);
      expect(p.bg).toBe(f.base);
      expect(p.fg).toBe(f.text);
    }
  });
});

describe("flavor completeness", () => {
  it("declares no undefined field on any flavor", () => {
    for (const f of flavors)
      for (const [k, v] of Object.entries(f as unknown as Record<string, unknown>))
        expect(v, `${(f as Flavor).id}.${k}`).toBeDefined();
  });
});
