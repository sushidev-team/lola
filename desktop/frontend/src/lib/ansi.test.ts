import { describe, it, expect } from "vitest";
import { ansiToHtml, trimTrailingBlankLines, paneColumns } from "./ansi";
import { FLAVORS, toAnsi } from "./catppuccin";

const mocha = toAnsi(FLAVORS["catppuccin-mocha"]);
const latte = toAnsi(FLAVORS["catppuccin-latte"]);

describe("ansiToHtml", () => {
  it("passes plain text through, HTML-escaped", () => {
    expect(ansiToHtml("hello")).toBe("hello");
    expect(ansiToHtml("a < b & c > d")).toBe("a &lt; b &amp; c &gt; d");
  });

  it("wraps colored runs in styled spans", () => {
    const html = ansiToHtml("\x1b[31mred\x1b[0m");
    expect(html).toContain("color:#f38ba8"); // Catppuccin Mocha red
    expect(html).toContain(">red<");
  });

  it("resets styling on \\x1b[0m", () => {
    const html = ansiToHtml("\x1b[32mgreen\x1b[0mplain");
    expect(html.endsWith("plain")).toBe(true);
  });

  it("applies codes after a reset in the same sequence (\\x1b[0;31m)", () => {
    // Code 0 must reset in place and CONTINUE, so the trailing 31 still paints
    // red. The old early-return dropped everything after the 0.
    const html = ansiToHtml("\x1b[0;31mred");
    expect(html).toContain("color:#f38ba8"); // Catppuccin Mocha red
    expect(html).toContain(">red<");
  });

  it("handles truecolor fg", () => {
    const html = ansiToHtml("\x1b[38;2;18;20;32mx");
    expect(html).toContain("color:#121420");
  });

  it("handles 256-color fg", () => {
    // Index 9 is BRIGHT red, which is a distinct colour from index 1 in
    // Ghostty's Catppuccin (#f37799 vs #f38ba8). The old table duplicated the
    // normal colours into slots 9-14, so this used to be indistinguishable.
    const html = ansiToHtml("\x1b[38;5;9mx");
    expect(html).toContain("color:#f37799");
    expect(html).not.toContain("color:#f38ba8");
  });

  it("distinguishes bright from normal for SGR 90-97", () => {
    expect(ansiToHtml("\x1b[91mx")).toContain("color:#f37799"); // bright red
    expect(ansiToHtml("\x1b[31mx")).toContain("color:#f38ba8"); // normal red
  });

  it("applies bold weight", () => {
    expect(ansiToHtml("\x1b[1mB")).toContain("font-weight:600");
  });

  it("drops non-SGR CSI without leaking escapes", () => {
    const html = ansiToHtml("\x1b[2J\x1b[Hclean");
    expect(html).toBe("clean");
  });

  // --- palette plumbing -----------------------------------------------------

  it("defaults to the default flavor's palette", () => {
    expect(ansiToHtml("\x1b[31mx")).toBe(ansiToHtml("\x1b[31mx", mocha));
  });

  it("emits a different colour when the flavor changes", () => {
    const dark = ansiToHtml("\x1b[31mred\x1b[0m", mocha);
    const light = ansiToHtml("\x1b[31mred\x1b[0m", latte);
    expect(dark).toContain(`color:${FLAVORS["catppuccin-mocha"].ansi16[1]}`);
    expect(light).toContain(`color:${FLAVORS["catppuccin-latte"].ansi16[1]}`);
    expect(dark).not.toBe(light);
  });

  it("takes inverse-video defaults from the palette, not a hardcoded navy", () => {
    // SGR 7 with no explicit colours has to materialise both defaults, so this
    // is the case that used to bake lola's old #0e1420 / #c3cbd6 into every
    // flavor — including Latte, where it inverted to dark-on-dark.
    const html = ansiToHtml("\x1b[7mx", latte);
    expect(html).toContain(`color:${latte.bg}`);
    expect(html).toContain(`background:${latte.fg}`);
    expect(html).not.toContain("#0e1420");
  });

  // --- security property (unchanged by the palette work) ---------------------

  it("escapes markup inside a coloured run and emits only a style attribute", () => {
    const html = ansiToHtml('\x1b[31m<img src=x onerror="alert(1)">\x1b[0m');
    expect(html).not.toContain("<img");
    expect(html).toContain("&lt;img");
    // The only attribute on the only tag is style=.
    expect(html.match(/<span [^>]*>/g)).toEqual(['<span style="color:#f38ba8">']);
  });

  it("cannot be made to emit a quote-broken style attribute", () => {
    // Colour values only ever come from the palette or from numeric SGR
    // parameters, so no attacker-controlled string reaches the style attribute.
    const html = ansiToHtml('\x1b[38;2;1;2;3m"><script>x</' + "script>");
    expect(html).toContain("color:#010203");
    expect(html).not.toContain("<script");
    expect(html).toContain("&lt;script");
  });
});

// The grid anchors a snapshot to the bottom of its tile, so tmux's visible-pane
// padding is no longer harmless: it would be what lands on the floor.
describe("trimTrailingBlankLines", () => {
  it("drops trailing empty and whitespace-only lines", () => {
    expect(trimTrailingBlankLines("a\nb\n\n   \n\n")).toBe("a\nb");
  });

  it("drops trailing lines that carry only SGR escapes", () => {
    expect(trimTrailingBlankLines("a\n\x1b[0m\n\x1b[39m   \x1b[0m")).toBe("a");
  });

  it("keeps blank lines that sit between real output", () => {
    expect(trimTrailingBlankLines("a\n\nb\n\n")).toBe("a\n\nb");
  });

  it("leaves a snapshot with no padding untouched, and survives an empty one", () => {
    expect(trimTrailingBlankLines("only line")).toBe("only line");
    expect(trimTrailingBlankLines("")).toBe("");
    expect(trimTrailingBlankLines("\n\n")).toBe("");
  });
});

// A tile fits its type to the pane's own width, so what counts as a "column" is
// load-bearing: an unstripped hyperlink escape would measure as ~80 phantom
// columns and shrink the whole tile's text.
describe("OSC handling and paneColumns", () => {
  const link = "\x1b]8;id=1;https://github.com/o/r/pull/248\x07PR #248\x1b]8;;\x07";

  it("renders an OSC 8 hyperlink as its label, not as the control string", () => {
    const html = ansiToHtml(link);
    expect(html).toBe("PR #248");
    expect(html).not.toContain("]8;");
    expect(html).not.toContain("github.com");
  });

  it("accepts the ST-terminated form and a capture-truncated one", () => {
    expect(ansiToHtml("\x1b]0;title\x1b\\body")).toBe("body");
    expect(ansiToHtml("a\x1b]8;;https://cut-off-here")).toBe("a");
  });

  it("keeps SGR colouring around a stripped hyperlink", () => {
    const html = ansiToHtml("\x1b[31m" + link + "\x1b[0m");
    expect(html).toContain("color:#f38ba8");
    expect(html).toContain(">PR #248<");
  });

  it("measures the widest visible line, ignoring escapes and trailing pad", () => {
    expect(paneColumns("abc\n\x1b[31mabcdef\x1b[0m   \nxy")).toBe(6);
    expect(paneColumns(link)).toBe("PR #248".length);
  });

  it("caps one pathological line so it can't shrink the whole tile", () => {
    expect(paneColumns("x".repeat(5000))).toBe(220);
    expect(paneColumns("x".repeat(5000), 100)).toBe(100);
  });

  it("returns 0 for an empty snapshot", () => {
    expect(paneColumns("")).toBe(0);
  });
});
