import { describe, it, expect } from "vitest";
import {
  KEY_SLOP_PX,
  beginKey,
  beyondSlop,
  cancelKey,
  commitKey,
  moveKey,
  releaseKey,
} from "./keygesture";

describe("keygesture", () => {
  it("fires when a still finger outlasts the commit window", () => {
    const g = beginKey(100, 200);
    expect(commitKey(g).fire).toBe(true);
  });

  it("fires on release for a tap shorter than the commit window", () => {
    const g = beginKey(100, 200);
    expect(releaseKey(g).fire).toBe(true);
  });

  it("NEVER fires when the finger travelled first — this is the ^Z bug", () => {
    // A swipe of the key row begins on a key, because the row is wall-to-wall
    // keys. Firing on pointerdown sent that key into a live agent; a drag begun
    // on ^Z suspended a real Claude Code session.
    let g = beginKey(100, 200);
    g = moveKey(g, 140, 202);
    expect(g.cancelled).toBe(true);
    expect(commitKey(g).fire).toBe(false);
    expect(releaseKey(g).fire).toBe(false);
  });

  it("tolerates the jitter of a still finger", () => {
    let g = beginKey(100, 200);
    g = moveKey(g, 103, 202);
    expect(g.cancelled).toBe(false);
    expect(commitKey(g).fire).toBe(true);
  });

  it("treats movement AFTER the press as a hold, so a repeat survives a slide", () => {
    const { gesture } = commitKey(beginKey(100, 200));
    const held = moveKey(gesture, 300, 400);
    expect(held.cancelled).toBe(false);
    expect(held.fired).toBe(true);
  });

  it("fires exactly once when the timer and the release both land", () => {
    const first = commitKey(beginKey(0, 0));
    expect(first.fire).toBe(true);
    expect(releaseKey(first.gesture).fire).toBe(false);
  });

  it("refuses after a browser-driven scroll takeover", () => {
    const g = cancelKey(beginKey(0, 0));
    expect(releaseKey(g).fire).toBe(false);
  });

  it("measures slop as a distance, not per axis", () => {
    const g = beginKey(0, 0);
    // 6,6 is 8.49 away: beyond an 8px radius although neither axis is.
    expect(beyondSlop(g, 6, 6)).toBe(true);
    expect(beyondSlop(g, KEY_SLOP_PX, 0)).toBe(false);
    expect(beyondSlop(g, KEY_SLOP_PX + 1, 0)).toBe(true);
  });
});
