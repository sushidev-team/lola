import { describe, it, expect } from "vitest";
import { atomicRange, attachmentTone, attachmentToken, expandPastes, fieldSegments, foldsPaste, isImagePath, padAttachments, textSize, tokenAtCaret, tokenStep, tokensIn, youAttachments, type Attachment, type Pastes } from "./composerpaste";

const text = (value: string) => ({ kind: "text", value }) as const;

describe("attachments", () => {
  it("folds a long or many-line paste, and leaves a short one", () => {
    expect(foldsPaste("one line")).toBe(false);
    expect(foldsPaste("a\nb\nc\nd\ne")).toBe(true);
    expect(foldsPaste("x".repeat(800))).toBe(true);
  });
  it("names lines, or size for one long line", () => {
    expect(textSize("a\nb")).toBe("2 lines");
    expect(textSize("x".repeat(2048))).toBe("2 KB");
    expect(textSize("abc")).toBe("3 chars");
  });
  it("reserves horizontal padding inside the atomic number", () => {
    expect(attachmentToken(3).replace(/⁠/g, "")).toBe("\u00a0(3)\u00a0");
  });
  it("fills in a reference, and a typed (1) is the person's own words", () => {
    const pastes: Pastes = new Map([[1, text("THE LOG")]]);
    expect(expandPastes(`see ${attachmentToken(1)} and (1)`, pastes)).toBe("see THE LOG and (1)");
  });
  it("a reference with nothing kept keeps its visible words and no marks", () => {
    expect(expandPastes(`see ${attachmentToken(9)}`, new Map([[1, text("x")]]))).toBe("see (9)");
  });
  it("does not read dollars in a paste as a replacement pattern", () => {
    const pastes: Pastes = new Map([[1, text("cost $& $1")]]);
    expect(expandPastes(attachmentToken(1), pastes)).toBe("cost $& $1");
  });
  it("an image goes as its path", () => {
    const pastes: Pastes = new Map([[3, { kind: "image", value: "/w/.lola/images/a.png" }]]);
    expect(expandPastes(`look ${attachmentToken(3)}`, pastes)).toBe("look /w/.lola/images/a.png");
  });
  it("finds a reference against the caret, on the side the key deletes", () => {
    const pastes: Pastes = new Map([[1, text("x")]]);
    const field = `a ${attachmentToken(1)} b`;
    const [t] = tokensIn(field, pastes);
    expect(tokenAtCaret(field, t.to, false, pastes)?.n).toBe(1);
    expect(tokenAtCaret(field, t.from, true, pastes)?.n).toBe(1);
    expect(tokenAtCaret(field, t.from, false, pastes)).toBeNull();
  });
  it("only a bare image path counts as one", () => {
    expect(isImagePath("/Users/me/CleanShot 2026-11-06 at 09.11.17.png\n")).toBe(true);
    expect(isImagePath("see /a/b.png please")).toBe(false);
    expect(isImagePath("/a/b.txt")).toBe(false);
  });
  it("moves across an entire chip in either direction, including multi-digit counts", () => {
    const pastes: Pastes = new Map([[12, text('log')]]);
    const value = `a ${attachmentToken(12)} z`;
    const [chip] = tokensIn(value, pastes);
    expect(tokenStep(value, chip.from, true, pastes)).toBe(chip.to);
    expect(tokenStep(value, chip.to, false, pastes)).toBe(chip.from);
    expect(tokenStep(value, 0, true, pastes)).toBeNull();
    expect(tokenStep('(12)', 0, true, pastes)).toBeNull();
    expect(atomicRange(value, chip.from + 1, chip.to - 1, pastes)).toEqual({ start: chip.from, end: chip.to });
    expect(atomicRange(value, chip.from + 1, chip.from + 1, pastes)).toEqual({ start: chip.from, end: chip.from });
  });
  it("keeps padding inside chips and separates their expanded contents only when sending", () => {
    const pastes: Pastes = new Map<number, Attachment>([[1, text('log')], [2, { kind: 'image', value: '/image.png' }]]);
    const one = attachmentToken(1), two = attachmentToken(2);
    const value = `a${one}${two}z`;
    expect(padAttachments(value, value.length, value.length, pastes)).toEqual({ text: value, start: value.length, end: value.length });
    expect(expandPastes(value, pastes)).toBe('a log /image.png z');
    expect(expandPastes(one, pastes)).toBe('log');
    expect(expandPastes(`\n${one}\n`, pastes)).toBe('\nlog\n');
    for (const unchanged of [one, `${one}\n${two}`, `\n${one}\n`, `a ${one}\t z`, '(1)text']) {
      expect(padAttachments(unchanged, 0, 0, pastes).text).toBe(unchanged);
    }
  });
  it("pads legacy drafts without moving the cursor from the text before a chip", () => {
    const pastes: Pastes = new Map([[1, text('log')]]);
    const legacy = '\u2060(1)\u2060';
    expect(padAttachments(`word${legacy}`, 4, 4, pastes)).toEqual({ text: `word${attachmentToken(1)}`, start: 4, end: 4 });
    expect(padAttachments(legacy, legacy.length, legacy.length, pastes)).toEqual({ text: attachmentToken(1), start: attachmentToken(1).length, end: attachmentToken(1).length });
    expect(expandPastes(legacy, pastes)).toBe('log');
  });
  it("cuts the field at its references, and leaves a typed (1) in the words", () => {
    const pastes: Pastes = new Map([[1, text("x")]]);
    const token = attachmentToken(1);
    expect(fieldSegments(`a ${token} and (1)`, pastes)).toEqual([{ text: "a " }, { text: token, n: 1 }, { text: " and (1)" }]);
  });
  it("tells attachments apart, and comes round again", () => {
    expect(attachmentTone(1)).not.toBe(attachmentTone(2));
    expect(attachmentTone(5)).toBe(attachmentTone(1));
  });
});

describe("youAttachments", () => {
  const a = "/Users/m/.lola/worktrees/p/s/.lola/images/a.png";
  const b = "/Users/m/.lola/worktrees/p/s/.lola/images/b.JPG";

  it("reads image copies back as numbers in reading order", () => {
    const got = youAttachments(`first ${a} then (${b}), and ${a}?`);
    expect(got.text).toBe("first (1) then ((2)), and (1)?");
    expect(got.images).toEqual([{ n: 1, path: a }, { n: 2, path: b }]);
  });
  it("leaves every other path alone", () => {
    for (const t of ["see /tmp/shot.png", "src/.lola/images/a.png", "/w/.lola/images/notes.txt", "/w/.lola/images/sub/a.png", "x/w/.lola/images/a.png"]) {
      expect(youAttachments(t)).toEqual({ text: t, images: [], parts: [{ text: t }] });
    }
  });
  it("a typed (1) stays the person's words, and only the image becomes a number part", () => {
    const got = youAttachments(`(1) is ${a}`);
    expect(got.parts).toEqual([{ text: "(1) is " }, { n: 1 }]);
  });
});
