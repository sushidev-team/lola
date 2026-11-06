import { describe, expect, it, vi } from "vitest";
import { answerBlocks, answerShaped, answerStatus, cellTone, fenceFold, firstSentenceEnd, labelTone, parseLabel, plainInline, prHref, splitLead } from "./answerblocks";

const kinds = (text: string) => answerBlocks(text).map((b) => b.kind);

describe("answerShaped", () => {
  it("reads the text alone: a newline, a length or a block marker", () => {
    expect(answerShaped("One line of narration.")).toBe(false);
    expect(answerShaped("x".repeat(280))).toBe(false);
    expect(answerShaped("x".repeat(281))).toBe(true);
    expect(answerShaped("a\nb")).toBe(true);
    expect(answerShaped("- a")).toBe(true);
    expect(answerShaped("| a | b |")).toBe(true);
    expect(answerShaped("```")).toBe(true);
  });
  it("an unshaped beat is one inline paragraph, however it looks", () => {
    expect(answerBlocks("**Done.** Short one.")).toEqual([{ kind: "p", text: "**Done.** Short one." }]);
  });
});

describe("the lead", () => {
  it("is the first sentence, and the rest stays a paragraph", () => {
    expect(answerBlocks("Stays in PR #74. No code change here.\n\nMore.")).toEqual([
      { kind: "lead", text: "Stays in PR #74." },
      { kind: "p", text: "No code change here." },
      { kind: "p", text: "More." },
    ]);
  });
  it("is the whole paragraph when no short sentence ends it", () => {
    const long = "w".repeat(150);
    expect(splitLead(long)).toEqual({ lead: long, rest: "" });
  });
  it("is absent when neither fits", () => {
    expect(splitLead("w".repeat(200) + ". Then more.")).toBeNull();
    expect(kinds("w".repeat(200) + ". Then more.\n\nNext.")).toEqual(["p", "p"]);
  });
  it("does not end on an abbreviation, in either language", () => {
    for (const t of ["Use e.g. this one. Next.", "Nimm z.B. das hier. Weiter.", "Nimm z. B. das hier. Weiter.", "Das heißt d.h. so. Weiter.", "A vs. B wins. Next.", "Und bzw. oder. Weiter."]) {
      expect(t.slice(0, firstSentenceEnd(t))).toMatch(/(this one|das hier|so|wins|oder)\.$/);
    }
  });
  it("never cuts inside code or bold, and an odd marker means no cut", () => {
    expect(firstSentenceEnd("Run `a. B` now. Next.")).toBe("Run `a. B` now.".length);
    expect(firstSentenceEnd("A **b. C** done. Next.")).toBe("A **b. C** done.".length);
    expect(splitLead("Odd ` tick here. Then ` more. And on and on.\nmore")?.lead).not.toBe("Odd ` tick here.");
  });
  it("a heading first means no lead", () => {
    expect(kinds("**Status**\n\nFine.")).toEqual(["heading", "p"]);
  });
});

describe("headings", () => {
  it("takes a bold-only line, alone or opening a paragraph, and drops its colon", () => {
    expect(answerBlocks("Intro.\n\n**Noch offen:**\nDer Rest.")).toEqual([
      { kind: "lead", text: "Intro." },
      { kind: "heading", text: "Noch offen" },
      { kind: "p", text: "Der Rest." },
    ]);
    expect(kinds("A.\n\n**Head**:\n\n- x")).toEqual(["lead", "heading", "ul"]);
    expect(kinds("A.\n\n## Head")).toEqual(["lead", "heading"]);
  });
  it("is not fooled by a bold word in a sentence", () => {
    expect(kinds("A.\n\n**Bold** then words.")).toEqual(["lead", "p"]);
  });
});

describe("labels", () => {
  it.each([
    ["**X:** t", "X"],
    ["**X**: t", "X"],
    ["**X** – t", "X"],
    ["**`X`:** t", "X"],
    ["`X`: t", "X"],
    ["Plain label: t", "Plain label"],
    ["**`#74`:** t", "#74"],
  ])("reads %s", (input, label) => {
    expect(parseLabel(input)).toMatchObject({ label, text: "t" });
  });
  it("wants a text, a capital and a short plain label", () => {
    expect(parseLabel("**X:**")).toBeNull();
    expect(parseLabel("lower case: t")).toBeNull();
    expect(parseLabel("This plain label runs far too long for a column: t")).toBeNull();
    expect(parseLabel("See https://x.dev: t")).toBeNull();
  });
  it("tones by whole words at the start, any case, and never green", () => {
    expect(labelTone("Noch offen")).toBe("warn");
    expect(labelTone("Still open")).toBe("warn");
    expect(labelTone("RISK")).toBe("warn");
    expect(labelTone("Fehler im Build")).toBe("bad");
    expect(labelTone("Blocked")).toBe("bad");
    expect(labelTone("Opening hours")).toBe("");
    expect(labelTone("Riskant")).toBe("");
    expect(labelTone("Passed")).toBe("");
  });
});

describe("ledgers", () => {
  it("takes a list when two of three items are labelled, and keeps the other row", () => {
    const [b] = answerBlocks("- **A:** one\n- **B:** two\n- three");
    expect(b).toMatchObject({ kind: "ledger", rows: [{ label: "A", text: "one" }, { label: "B", text: "two" }, { label: "", text: "three" }] });
  });
  it("leaves a list with too few labels, a lone item and a numbered list alone", () => {
    expect(kinds("- **A:** one\n- two\n- three")).toEqual(["ul"]);
    expect(kinds("- **A:** one")).toEqual(["ul"]);
    expect(kinds("1. **A:** one\n2. **B:** two")).toEqual(["ol"]);
  });
  it("takes consecutive bold-labelled paragraphs, and leaves a lone one as prose", () => {
    expect(kinds("Top.\n\n**A:** one\n\n**B:** two\n\nEnd.")).toEqual(["lead", "ledger", "p"]);
    expect(kinds("Top.\n\n**A:** one\n\nEnd.")).toEqual(["lead", "p", "p"]);
    expect(kinds("Top.\n\nPlain: one\n\nOther: two")).toEqual(["lead", "p", "p"]);
  });
  it("stacks a long label, and carries tone, marks and sublists", () => {
    const [b] = answerBlocks(`- **${"L".repeat(29)}:** one\n- ✓ **Fine:** two\n  - child\n- **Noch offen:** three`);
    expect(b).toMatchObject({
      kind: "ledger",
      rows: [
        { stacked: true },
        { label: "Fine", status: "ok", subs: ["child"], stacked: false },
        { label: "Noch offen", tone: "warn" },
      ],
    });
  });
});

describe("the other elements", () => {
  it("reads a status mark and keeps the words", () => {
    expect(answerStatus("✅ all good")).toEqual({ tone: "ok", text: "all good" });
    expect(answerStatus("❌ broken")).toEqual({ tone: "bad", text: "broken" });
    expect(answerStatus("⚠ careful")).toEqual({ tone: "warn", text: "careful" });
    expect(answerStatus("[warn] disk")).toEqual({ tone: "warn", text: "disk" });
    expect(answerStatus("plain")).toEqual({ text: "plain" });
  });
  it("sets a closing question apart, only when something came before it", () => {
    expect(kinds("Done.\n\nShall I merge?")).toEqual(["lead", "question"]);
    expect(kinds(`Done.\n\n${"w".repeat(290)}?`)).toEqual(["lead", "p"]);
    expect(kinds("Shall I?\nOr not?")).toEqual(["lead", "question"]);
  });
  it("makes a callout of a quote or an opening word, and keeps the word", () => {
    expect(answerBlocks("A.\n\nWarnung: heiß")[1]).toEqual({ kind: "callout", tone: "warn", word: "Warnung", text: "heiß" });
    expect(answerBlocks("A.\n\n**Note:** cool")[1]).toEqual({ kind: "callout", tone: "note", word: "Note", text: "cool" });
    expect(kinds("A.\n\n> quoted")).toEqual(["lead", "callout"]);
  });
  it("folds a long fence, naming its first line", () => {
    expect(fenceFold("a\nb").folds).toBe(false);
    expect(fenceFold("\nfirst\n" + "x\n".repeat(9))).toEqual({ folds: true, label: "first" });
    expect(answerBlocks("A.\n\n```ts\nconst x = 1;\n```")[1]).toEqual({ kind: "code", text: "const x = 1;", lang: "ts" });
  });
  it("keeps tables and shapes what is inside a fold", () => {
    expect(kinds("A.\n\n| a | b |\n|---|---|\n| 1 | 2 |")).toEqual(["lead", "table"]);
    const fold = answerBlocks("A.\n\n+++ More\n- **X:** y\n- **Z:** w\n+++")[1];
    expect(fold).toMatchObject({ kind: "fold", blocks: [{ kind: "ledger" }] });
  });
  it("gives a table cell a hue only when it is exactly a status word, and never good", () => {
    expect(cellTone("failed")).toBe("bad");
    expect(cellTone("`Open`")).toBe("warn");
    expect(cellTone("ok")).toBe("");
    expect(cellTone("failed twice")).toBe("");
  });
  it("makes a pull request's address only from the session's own repository", () => {
    expect(prHref("https://github.com/o/r/pull/9", "#74")).toBe("https://github.com/o/r/pull/74");
    expect(prHref(undefined, "#74")).toBe("");
    expect(prHref("javascript:alert(1)//x/y/pull/1", "#74")).toBe("");
    expect(prHref("https://github.com/o/r/pull/9", "#7x")).toBe("");
  });
  it("flattens a line for a folded turn", () => {
    expect(plainInline("Pushed **`abc`** on #12 and *more*")).toBe("Pushed abc on #12 and more");
    expect(plainInline("- item")).toBe("item");
    expect(plainInline("**open")).toBe("**open");
  });
});

describe("failing open", () => {
  // A list's number and a known callout tag are markers: the renderer draws those for itself.
  const words = (text: string) => text.replace(/^\s*\d+[.)]\s+/gm, "").replace(/\[!(OK|NOTE|WARN|WARNING|BAD|ERROR)\]/g, "").match(/[\p{L}\p{N}]+/gu) ?? [];
  const every = (text: string) => {
    const out = JSON.stringify(answerBlocks(text));
    for (const w of words(text)) expect(out, `${w} in ${JSON.stringify(text)}`).toContain(w);
  };

  it("never loses a word of a hand-made shape", () => {
    for (const t of [
      "Lead here. Rest of it.\n\n**Head:**\ntext\n\n- **A:** one\n  - sub\n- **B:** two\n- three\n\n| x | y |\n|---|---|\n| 1 | 2 |\n\n> quote\n\n```go\ncode here\n```\n\nFinal question?",
      "**unbalanced bold and `tick\n\n- item\n\n1. one\n\n+++ fold title\ninside\n",
      "+++ never closed\nstill here",
      "```\nnever closed fence",
      "| not | a table\n---\nrest",
      "> [!NOPE] kept word\n> [!WARN] other word",
      "Warning: careful now\n\nHinweis: auch das",
      "   \n\n\n   \nword",
    ]) every(t);
  });

  it("never loses a word of a random string", () => {
    const parts = ["**", "`", "*", "- ", "\n", "\n\n", "| ", "---", "# ", "> ", "+++", "```", ": ", ". ", "? ", "alpha", "Beta", "gamma7", "#12", "[x](y)", "  ", "✓ ", "1. ", "Note: "];
    let seed = 12345;
    const rand = (n: number) => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) % n;
    for (let i = 0; i < 500; i++) {
      let t = "";
      for (let n = rand(24) + 1; n > 0; n--) t += parts[rand(parts.length)];
      every(t);
    }
  });

  it("returns the text as a paragraph if the parser throws", () => {
    const spy = vi.spyOn(String.prototype, "split").mockImplementation(() => {
      throw new Error("boom");
    });
    let out;
    try {
      out = answerBlocks("line one\nline two");
    } finally {
      spy.mockRestore();
    }
    expect(out).toEqual([{ kind: "p", text: "line one\nline two" }]);
  });
});
