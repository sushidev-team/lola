import { describe, it, expect } from "vitest";
import { ansiToHtml } from "./ansi";

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
    const html = ansiToHtml("\x1b[38;5;9mx"); // bright red index
    expect(html).toContain("color:");
  });

  it("applies bold weight", () => {
    expect(ansiToHtml("\x1b[1mB")).toContain("font-weight:600");
  });

  it("drops non-SGR CSI without leaking escapes", () => {
    const html = ansiToHtml("\x1b[2J\x1b[Hclean");
    expect(html).toBe("clean");
  });
});
