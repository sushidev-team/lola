import { describe, expect, it } from "vitest";
import { isChord } from "./keys";

const ev = (o: Partial<KeyboardEvent>) =>
  ({ metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, ...o }) as KeyboardEvent;

describe("isChord", () => {
  it("claims nothing for a bare key", () => {
    expect(isChord(ev({ key: "c" }))).toBe(false);
  });

  it("leaves the platform's Cmd/Ctrl/Alt chords alone", () => {
    // The regression: Cmd-C ran the coderabbit review, Cmd-X asked to kill.
    for (const mod of ["metaKey", "ctrlKey", "altKey"] as const) {
      for (const key of ["c", "x", "v", "s", "o", "n", "g", "p", ","]) {
        expect(isChord(ev({ key, [mod]: true }))).toBe(true);
      }
    }
  });

  it("still fires the shifted shortcuts", () => {
    // 'V', 'G', 'N', 'S', 'R', 'P' and '?' ARE bindings — Shift must not bail.
    for (const key of ["V", "G", "N", "S", "R", "P", "?"]) {
      expect(isChord(ev({ key, shiftKey: true }))).toBe(false);
    }
  });
});
