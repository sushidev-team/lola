import { describe, it, expect } from "vitest";
import { ansiToHtml } from "./ansi";
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
