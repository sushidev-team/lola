import { describe, expect, it } from "vitest";
import { FADE_PX, edgeFadeMask, overflowEdges } from "./edgefade";

// The rule under test is "fade only what is actually hiding something". Every
// case below is one of the four states a horizontal strip can be in, plus the
// degenerate measurements a browser hands out before layout settles.

describe("edgeFadeMask", () => {
  it("costs nothing when the strip fits", () => {
    expect(edgeFadeMask(0, 420, 420)).toBe("");
    expect(edgeFadeMask(0, 420, 300)).toBe("");
  });

  it("tolerates the sub-pixel overflow a fitting strip reports", () => {
    // A strip that fits routinely measures a fraction wide. Fading it would put
    // a permanent dimmed edge on a complete row of keys.
    expect(edgeFadeMask(0, 420, 420.5)).toBe("");
  });

  it("fades only the trailing edge at the start of a scroll", () => {
    expect(edgeFadeMask(0, 360, 446)).toBe(
      "linear-gradient(to right, black 0px, black calc(100% - 24px), transparent 100%)",
    );
  });

  it("fades both edges in the middle", () => {
    expect(edgeFadeMask(40, 360, 446)).toBe(
      "linear-gradient(to right, transparent 0px, black 24px, black calc(100% - 24px), transparent 100%)",
    );
  });

  it("fades only the leading edge at the end of a scroll", () => {
    // The whole reason this is code rather than one CSS declaration: a static
    // mask would keep dimming the last key here, which is the same lie as a
    // hard cut pointing the other way.
    expect(edgeFadeMask(86, 360, 446)).toBe(
      "linear-gradient(to right, transparent 0px, black 24px, black 100%)",
    );
  });

  it("treats a scroll a pixel short of the end as the end", () => {
    expect(edgeFadeMask(85.4, 360, 446)).toBe(
      "linear-gradient(to right, transparent 0px, black 24px, black 100%)",
    );
  });

  it("keeps the two ramps from meeting on a narrow strip", () => {
    // At 60px wide a 24px ramp from each side would cross and dim the middle,
    // which reads as a disabled control rather than as a scroll hint.
    const m = edgeFadeMask(10, 60, 400);
    expect(m).toContain("black 20px");
    expect(m).toContain("calc(100% - 20px)");
  });

  it("returns nothing for an unlaid-out node", () => {
    // Before layout every measurement is 0, and NaN arrives from a detached
    // node. Both must produce no mask rather than an all-transparent one.
    expect(edgeFadeMask(0, 0, 0)).toBe("");
    expect(edgeFadeMask(0, Number.NaN, Number.NaN)).toBe("");
  });

  it("defaults to the shared ramp width", () => {
    expect(edgeFadeMask(0, 360, 446)).toBe(edgeFadeMask(0, 360, 446, FADE_PX));
  });
});

describe("overflowEdges", () => {
  it("reports neither edge when the strip fits", () => {
    expect(overflowEdges(0, 300, 300)).toEqual({ left: false, right: false });
  });

  it("reports the trailing edge at rest, which is where a key row lies", () => {
    // The mask alone is not enough here: a key row's clip lands in a key's
    // padding, so the fade has no ink to dim and the row reads as complete.
    expect(overflowEdges(0, 300, 500)).toEqual({ left: false, right: true });
  });

  it("reports both edges mid-scroll and only the leading one at the far end", () => {
    expect(overflowEdges(100, 300, 500)).toEqual({ left: true, right: true });
    expect(overflowEdges(200, 300, 500)).toEqual({ left: true, right: false });
  });

  it("tolerates the sub-pixel slack a scroller that fits reports", () => {
    expect(overflowEdges(0, 300, 300.4)).toEqual({ left: false, right: false });
    expect(overflowEdges(199.6, 300, 500)).toEqual({ left: true, right: false });
  });
});
