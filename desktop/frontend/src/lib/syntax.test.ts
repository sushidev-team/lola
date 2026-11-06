import { describe, expect, it } from "vitest";
import { syntaxTokens } from "./syntax";

describe("conversation syntax", () => {
  it("preserves source text exactly, including markup and multiline commands", () => {
    for (const text of ['git -C /tmp/repo commit -m "<script>alert(1)</script>"', 'const x = 42;\n// note\nreturn "hello";', '$PORT && echo "done"\n']) {
      expect(syntaxTokens(text, "sh").map((t) => t.text).join("")).toBe(text);
    }
  });
  it("keeps commit hashes, paths and identifiers whole", () => {
    for (const text of ["120eced", "00df2a3", "main", "src/123abc.ts"]) {
      expect(syntaxTokens(text)).toEqual([{ kind: "plain", text }]);
    }
    expect(syntaxTokens("git show 120eced", "sh").filter((t) => t.kind === "num")).toHaveLength(0);
  });
  it("colors shell commands, flags, variables and strings separately", () => {
    const tokens = syntaxTokens('git --no-pager show $REV "message"', "sh");
    expect(tokens).toContainEqual({ kind: "cmd", text: "git" });
    expect(tokens).toContainEqual({ kind: "flag", text: "--no-pager" });
    expect(tokens).toContainEqual({ kind: "var", text: "$REV" });
    expect(tokens).toContainEqual({ kind: "str", text: '"message"' });
  });
  it("does not mistake ordinary log prose for source code", () => {
    expect(syntaxTokens("disk is full", "text")).toEqual([{ kind: "plain", text: "disk is full" }]);
    expect(syntaxTokens('return 42', "typescript")).toContainEqual({ kind: "kw", text: "return" });
  });
  it("bounds rendering of large output without truncating it", () => {
    const text = 'x'.repeat(50000);
    expect(syntaxTokens(text)).toEqual([{ kind: "plain", text }]);
  });
});

it('highlights JSX tags and attributes while preserving the source', () => {
  const text = '  <Button size="sm" disabled={disabled || !!b.locked_at}>';
  const tokens = syntaxTokens(text, 'text', 'Board.tsx');
  expect(tokens).toContainEqual({ kind: 'cmd', text: '<Button' });
  expect(tokens).toContainEqual({ kind: 'flag', text: 'size' });
  expect(tokens).toContainEqual({ kind: 'str', text: '"sm"' });
  expect(tokens.map(token => token.text).join('')).toBe(text);
});
