import { describe, it, expect } from "vitest";
import {
  parsePatch,
  splitRows,
  buildTree,
  flattenTree,
  anchorOf,
  extendSelection,
  inSelection,
  quoteFor,
  langOf,
  tokenize,
} from "./diff";

const PATCH = "@@ -10,3 +10,4 @@ func f() {\n a\n-b\n+B\n+C\n c\n\\ No newline at end of file\n";

describe("parsePatch", () => {
  // Must agree with gitdiff.ParseLines on the Go side (the TUI's numbering).
  it("numbers both sides from the hunk header", () => {
    const rows = parsePatch(PATCH);
    expect(rows.map((r) => [r.kind, r.oldLine, r.newLine, r.text])).toEqual([
      ["hunk", 0, 0, "@@ -10,3 +10,4 @@ func f() {"],
      ["ctx", 10, 10, "a"],
      ["del", 11, 0, "b"],
      ["add", 0, 11, "B"],
      ["add", 0, 12, "C"],
      ["ctx", 12, 13, "c"],
      ["meta", 0, 0, "\\ No newline at end of file"],
    ]);
  });

  it("is empty for a missing patch", () => {
    expect(parsePatch(undefined)).toEqual([]);
    expect(parsePatch("")).toEqual([]);
  });
});

describe("splitRows", () => {
  it("lays a removal run beside the addition run that replaces it", () => {
    const s = splitRows(parsePatch(PATCH));
    expect(s[0].full?.kind).toBe("hunk");
    expect(s[1].left?.text).toBe("a");
    expect(s[1].right?.text).toBe("a");
    expect([s[2].left?.text, s[2].right?.text]).toEqual(["b", "B"]);
    expect([s[3].left, s[3].right?.text]).toEqual([null, "C"]);
    expect(s[5].full?.kind).toBe("meta");
  });
});

describe("selection", () => {
  const rows = parsePatch(PATCH);

  it("anchors removed lines on the old side and the rest on the new", () => {
    expect(anchorOf(rows[2])).toEqual({ side: "old", line: 11 });
    expect(anchorOf(rows[3])).toEqual({ side: "new", line: 11 });
    expect(anchorOf(rows[1])).toEqual({ side: "new", line: 10 });
    expect(anchorOf(rows[0])).toBeNull();
  });

  it("extends a range on the same side and restarts across sides or files", () => {
    let sel = extendSelection(null, "a.go", "new", 12);
    sel = extendSelection(sel, "a.go", "new", 10);
    expect(sel).toEqual({ path: "a.go", side: "new", start: 10, end: 12 });
    expect(extendSelection(sel, "a.go", "old", 11)).toEqual({ path: "a.go", side: "old", start: 11, end: 11 });
    expect(extendSelection(sel, "b.go", "new", 1)).toEqual({ path: "b.go", side: "new", start: 1, end: 1 });
  });

  it("quotes exactly the selected rows", () => {
    const sel = { path: "a.go", side: "new" as const, start: 10, end: 11 };
    expect(quoteFor(rows, sel)).toBe("a\nB");
    expect(inSelection(sel, "a.go", rows[2])).toBe(false); // the removed b is on the old side
    expect(inSelection(sel, "a.go", rows[3])).toBe(true);
    expect(inSelection(sel, "b.go", rows[3])).toBe(false);
  });
});

describe("buildTree", () => {
  it("puts directories first and collapses single-child chains", () => {
    const flat = flattenTree(buildTree(["internal/daemon/feedback.go", "internal/daemon/a.go", "README.md", "x/y.go"]));
    expect(flat.map((n) => [n.depth, n.name, n.path])).toEqual([
      [0, "internal/daemon", ""],
      [1, "a.go", "internal/daemon/a.go"],
      [1, "feedback.go", "internal/daemon/feedback.go"],
      [0, "x", ""],
      [1, "y.go", "x/y.go"],
      [0, "README.md", "README.md"],
    ]);
  });
});

describe("buildTree keys", () => {
  // Same-named directories at the same depth under different parents used to
  // share a render key, and Svelte throws each_key_duplicate on that.
  it("gives every node a unique key", () => {
    const flat = flattenTree(
      buildTree(["client/index.ts", "client/components/Button.ts", "server/index.ts", "server/components/Button.ts"]),
    );
    const keys = flat.map((n) => n.key);
    expect(new Set(keys).size).toBe(keys.length);
    expect(keys).toContain("dir:client/components");
    expect(keys).toContain("dir:server/components");
  });
});

describe("tokenize", () => {
  it("colours keywords, strings, numbers and trailing comments", () => {
    const toks = tokenize('return "a // not a comment", 42 // done', langOf("x.go"));
    expect(toks.filter((t) => t.kind !== "plain").map((t) => [t.kind, t.text])).toEqual([
      ["kw", "return"],
      ["str", '"a // not a comment"'],
      ["num", "42"],
      ["com", "// done"],
    ]);
    expect(toks.map((t) => t.text).join("")).toBe('return "a // not a comment", 42 // done');
  });

  it("leaves prose and unknown types plain", () => {
    expect(tokenize("if you return", langOf("README.md"))).toEqual([{ kind: "plain", text: "if you return" }]);
    expect(langOf("Makefile")).not.toBeNull();
  });

  it("does not read digits inside an identifier as a number", () => {
    expect(tokenize("x2 := v1", langOf("a.go")).some((t) => t.kind === "num")).toBe(false);
  });
});
