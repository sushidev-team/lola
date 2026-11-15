import { describe, expect, it } from "vitest";

import tiers from "./testdata/tiers.json";
import { TIER, isRead } from "./tiers";

describe("TIER", () => {
  it("deep-equals the table the Go test writes", () => {
    expect(TIER).toEqual(tiers);
  });

  it("knows read-tier commands and nothing else as read", () => {
    expect(isRead("sessions")).toBe(true);
    expect(isRead("watch")).toBe(true);
    expect(isRead("agentSay")).toBe(false);
    expect(isRead("stop")).toBe(false);
    expect(isRead("noSuchCommand")).toBe(false);
  });
});
