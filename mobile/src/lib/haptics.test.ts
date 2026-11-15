import { describe, it, expect, vi, beforeEach } from "vitest";

const haptic = vi.fn(async (_o: unknown) => {});
vi.mock("@mobile/native", () => ({ transport: { haptic: (o: unknown) => haptic(o) } }));

import { HAPTIC_FOR, haptic as fire, resetHaptics } from "./haptics";

beforeEach(() => {
  haptic.mockClear();
  resetHaptics();
});

describe("haptics", () => {
  it("maps each event to its kind (spec §6.5)", () => {
    expect(HAPTIC_FOR).toEqual({
      sent: "light",
      decision: "medium",
      stop: "rigid",
      select: "selection",
      success: "success",
      refusal: "warning",
      notPaired: "error",
    });
  });

  it("throttles a call inside the minimum gap", () => {
    fire("sent", 1000);
    fire("decision", 1010);
    fire("stop", 1050);
    expect(haptic.mock.calls.map((c) => (c[0] as { kind: string }).kind)).toEqual(["light", "rigid"]);
  });

  it("survives a rejecting plugin", () => {
    haptic.mockRejectedValueOnce(new Error("x"));
    expect(() => fire("success", 5000)).not.toThrow();
  });
});
