import { describe, expect, it } from "vitest";

// Guard the single-column contract; actual geometry is checked in the native preview.
// Not a glob with ?raw as in Controls.test.ts, because vitest empties a stylesheet's raw import, and not
// a static node:fs import, because the frontend carries no @types/node.
const fs = await import(/* @vite-ignore */ `node:${"fs"}`);
const css: string = fs.readFileSync(`${(globalThis as unknown as { process: { cwd(): string } }).process.cwd()}/src/app.css`, "utf8");

describe("the Conversation's layout", () => {
  it("keeps the composer below the story at all heights", () => {
    expect(css).not.toContain("--cv-a:");
    expect(css).not.toContain("--cv-r:");
    expect(css).not.toContain("@container (min-width: 1040px)");
    expect(css).toContain(".cv-desk-row {");
  });
  it("aligns timeline and composer in the same bounded reading column", () => {
    expect(css).toContain("padding-inline: clamp(32px, 3cqw, 56px)");
    expect(css).toContain("max-width: 1040px");
    expect(css).toContain("max-width: 70ch");
  });
  it("keeps status visible in the composer rather than hiding it at wide sizes", () => {
    expect(css).not.toContain(".ssh-col [data-footer-glyph]");
    expect(css).not.toContain(".ssh-col [data-footer-text]");
  });
  it("uses compact reading and metadata sizes", () => {
    expect(/\[data-conversation\] \{[^}]*--text-prose: 13px/.test(css)).toBe(true);
    expect(/\[data-conversation\] \{[^}]*--text-sm: 12px/.test(css)).toBe(true);
  });
});
