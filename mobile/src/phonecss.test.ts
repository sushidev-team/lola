import { describe, expect, it } from "vitest";
import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

// The phone stylesheet's contract with the shared desktop components (spec §4.3):
// a 44 pt touch target is grown with a pseudo-element, never by sizing the box.

const css = readFileSync(resolve(process.cwd(), "src/app.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

interface Rule {
  selector: string;
  body: string;
}

/** Every innermost `selector { declarations }` block, at any nesting depth. */
function rules(): Rule[] {
  const out: Rule[] = [];
  for (const m of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    out.push({ selector: m[1].trim(), body: m[2] });
  }
  return out;
}

const desk = rules().filter((r) => r.selector.includes("[data-phone-desk]"));

describe("[data-phone-desk] rules", () => {
  it("are present", () => {
    expect(desk.length).toBeGreaterThan(5);
  });

  it("never size a button's box", () => {
    const sized = desk.filter(
      (r) => /\bbutton\b/.test(r.selector) && /(^|[;\s])(min-|max-)?height\s*:/.test(r.body),
    );
    expect(sized).toEqual([]);
  });

  it("grow the touch target with ::after on Button's own class", () => {
    const grow = desk.find((r) => r.selector === "[data-phone-desk] button.cursor-pointer::after");
    expect(grow?.body).toMatch(/inset:\s*-10px/);
  });

  it("keep dense rows' own targets", () => {
    const excl = desk.find((r) => /content:\s*none/.test(r.body));
    expect(excl).toBeDefined();
    expect(excl!.selector).toContain(".line-gutter");
    expect(excl!.selector).toContain(".diff-body");
    expect(excl!.selector).toContain("[data-queue-row]");
  });

  it("rescale the shared ladder to the 13/15/17 metadata scale", () => {
    const scope = desk.find((r) => r.selector === "[data-phone-desk]");
    expect(scope?.body).toContain("--text-sm: 0.8125rem");
    expect(scope?.body).toContain("--text-base: 0.9375rem");
    expect(scope?.body).toContain("--text-lg: 1.0625rem");
  });
});

describe("phone type scale", () => {
  it.each(["title", "headline", "body", "reading", "callout", "subhead", "footnote", "micro", "mono", "code"])(
    "defines --text-%s with a line height",
    (name) => {
      expect(css).toContain(`--text-${name}:`);
      expect(css).toContain(`--text-${name}--line-height:`);
    },
  );
});

describe("phone globals", () => {
  const all = rules();
  const body = (sel: string): string => all.find((r) => r.selector.replace(/\s+/g, " ") === sel)?.body ?? "";

  it("paints the WebView's own ground with the canvas", () => {
    const r = all.find((x) => /^html,\s*body$/.test(x.selector) && /background:\s*var\(--color-canvas\)/.test(x.body));
    expect(r).toBeDefined();
  });

  it("never lets a textarea be dragged", () => {
    expect(body("textarea")).toMatch(/resize:\s*none/);
  });

  it("hides desktop keyboard chrome inside the session", () => {
    const r = all.find((x) => x.selector.includes("[data-phone-desk] [data-chord]"));
    expect(r?.selector).toContain("[data-phone-desk] kbd");
    expect(r?.selector).toContain("[data-phone-desk] .cv-compose-hint");
    expect(r?.body).toMatch(/display:\s*none\s*!important/);
  });

  it("declares the gutter and its utility", () => {
    expect(css).toMatch(/--gutter:\s*16px/);
    expect(css).toMatch(/@utility px-gutter\s*\{\s*padding-inline:\s*var\(--gutter\)/);
  });

  it("grows a tab's hit area with ::after, not the track", () => {
    expect(body('[role="tab"]::after')).toMatch(/inset:\s*-6px 0/);
  });

  it("uses the full subtext colour for placeholders", () => {
    expect(body("::placeholder")).toMatch(/color:\s*var\(--color-subtext\)/);
    expect(body("::placeholder")).toMatch(/opacity:\s*1/);
  });

  it("separates adjacent list rows with an inset hairline", () => {
    const r = all.find((x) => x.selector === "[data-list-row] + [data-list-row]::before");
    expect(r?.body).toMatch(/left:\s*var\(--row-inset,\s*16px\)/);
    expect(r?.body).toMatch(/height:\s*0\.5px/);
  });

  it("declares the on-accent and idle-tab fallbacks so the utilities exist", () => {
    expect(css).toMatch(/--color-on-accent:/);
    expect(css).toMatch(/--color-tab-idle:/);
  });
});

describe("the confirm dialog as an action sheet", () => {
  const sheet = rules().filter((r) => r.selector.includes("[data-phone-app]"));

  it("is scoped under the phone shell only", () => {
    expect(sheet.length).toBeGreaterThan(5);
    for (const r of sheet) expect(r.selector).toContain("[data-phone-app]");
  });

  it("anchors to the bottom with a 12pt inset and a 14pt radius", () => {
    const r = sheet.find((x) => /\.modal-shell:has\(\[data-confirm\]\)$/.test(x.selector));
    expect(r?.body).toMatch(/inset:\s*auto 12px/);
    expect(r?.body).toMatch(/border-radius:\s*14px/);
  });

  it("draws the destructive row 56pt in --color-bad and Cancel below it via order", () => {
    const ok = sheet.find((x) => x.selector.endsWith("[data-confirm-ok]"));
    const cancel = sheet.find((x) => x.selector.endsWith("[data-confirm-cancel]"));
    expect(ok?.body).toMatch(/color:\s*var\(--color-bad\)/);
    expect(cancel?.body).toMatch(/font-weight:\s*600/);
    expect(Number(/order:\s*(\d+)/.exec(cancel!.body)?.[1])).toBeGreaterThan(Number(/order:\s*(\d+)/.exec(ok!.body)?.[1]));
    const btn = sheet.find((x) => x.selector.endsWith(".dialog-actions button"));
    expect(btn?.body).toMatch(/height:\s*56px/);
  });

  it("hides the close x", () => {
    const r = sheet.find((x) => x.selector.includes(".modal-header button"));
    expect(r?.body).toMatch(/display:\s*none/);
  });

  it("grow the shared Select's trigger to a 44pt target", () => {
    const sel = desk.find((r) => r.selector === "[data-phone-desk] .lola-select .lola-field");
    expect(sel?.body).toMatch(/min-height:\s*44px/);
    expect(sel?.body).toMatch(/padding-block:\s*12px/);
  });

  it("make a field inside a ListGroup borderless, transparent and 44pt", () => {
    const f = rules().find((r) => r.selector.startsWith("[data-list-group] .lola-field"));
    expect(f?.body).toMatch(/border:\s*0/);
    expect(f?.body).toMatch(/background:\s*transparent/);
    expect(f?.body).toMatch(/min-height:\s*44px/);
    expect(f?.body).toMatch(/padding-inline:\s*16px/);
  });
});

describe("gutter utilities", () => {
  it("every m*/p*-gutter class used in a .svelte file has an @utility", () => {
    const raw = readFileSync(resolve(process.cwd(), "src/app.css"), "utf8");
    const defined = new Set([...raw.matchAll(/@utility\s+([\w-]+)/g)].map((m) => m[1]));
    const used = new Set<string>();
    const walk = (dir: string): void => {
      for (const e of readdirSync(dir, { withFileTypes: true })) {
        const p = resolve(dir, e.name);
        if (e.isDirectory()) walk(p);
        else if (e.name.endsWith(".svelte")) {
          for (const m of readFileSync(p, "utf8").matchAll(/(?<![\w-])((?:m|p)[xytrbl]?-gutter)(?![\w-])/g)) used.add(m[1]);
        }
      }
    };
    walk(resolve(process.cwd(), "src"));
    expect([...used].filter((c) => !defined.has(c))).toEqual([]);
  });
});
