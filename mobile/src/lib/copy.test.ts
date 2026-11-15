import { describe, it, expect } from "vitest";
import { copy, fill } from "./copy";

// A word is a word: a `{placeholder}` is data and a bare separator ("·") is punctuation.
const words = (s: string): number =>
  s
    .trim()
    .split(/\s+/)
    .filter((w) => !/^\{\w+\}$/.test(w) && !/^[·•|–—-]$/.test(w)).length;

// Values that are a whole sentence rather than a label, whatever their key is called: the
// refusals the app says, and the two one-line hints. They keep the BODY budget (one sentence, at
// most 12 words).
const SENTENCE_KEYS = new Set(["noDrafts", "needsText", "stillWorking", "unknown", "into"]);
// A `body` that is a tappable label, not a sentence: the key-changed banner row ("Pair {mac} again").
const LABEL_BODIES = new Set(["banner.keyChanged.body"]);
const isLabelBody = (p: string[]): boolean => LABEL_BODIES.has(p.join("."));
const isSentence = (p: string[]): boolean =>
  !isLabelBody(p) && p[0] === "refusals" || (p[0] === "session" && p[1] === "empty" && last(p) !== "title") || SENTENCE_KEYS.has(last(p));

const last = (p: string[]): string => p[p.length - 1];

function leaves(o: unknown, path: string[] = []): { path: string[]; text: string }[] {
  if (typeof o === "string") return [{ path, text: o }];
  return Object.entries(o as object).flatMap(([k, v]) => leaves(v, [...path, k]));
}

const all = leaves(copy);

describe("copy budget", () => {
  it("keeps every title to 3 words", () => {
    const titles = all.filter((l) => last(l.path) === "title");
    expect(titles.length).toBeGreaterThan(10);
    for (const t of titles) expect(words(t.text), t.path.join(".")).toBeLessThanOrEqual(3);
  });

  it("keeps every body to one sentence of at most 12 words", () => {
    const bodies = all.filter((l) => (last(l.path) === "body" && !isLabelBody(l.path)) || isSentence(l.path));
    expect(bodies.length).toBeGreaterThan(20);
    for (const b of bodies) {
      const id = b.path.join(".");
      expect(words(b.text), id).toBeLessThanOrEqual(12);
      expect(b.text.match(/[.…]/g)?.length, id).toBe(1);
      expect(/[.…]$/.test(b.text), id).toBe(true);
      expect(/[!?]/.test(b.text), id).toBe(false);
    }
  });

  it("keeps every other label to 4 words with no sentence end", () => {
    const rest = all.filter((l) => (!["title", "body"].includes(last(l.path)) || isLabelBody(l.path)) && !isSentence(l.path));
    for (const l of rest) {
      const id = l.path.join(".");
      expect(words(l.text), id).toBeLessThanOrEqual(4);
      if (!["status", "mergeAnyway"].includes(last(l.path))) expect(/[.…]$/.test(l.text), id).toBe(false);
    }
  });

  it("fills placeholders and leaves unknown ones", () => {
    expect(fill(copy.session.reviewBar.feedback, { n: 3 })).toBe("Send feedback (3)");
    expect(fill(copy.banner.unreachable.body, { mac: "Studio" })).toBe("Can't reach Studio.");
    expect(fill("{x}", {})).toBe("{x}");
  });
});
