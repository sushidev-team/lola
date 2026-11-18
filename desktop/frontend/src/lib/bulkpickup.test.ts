import { describe, expect, it } from "vitest";
import { pickupArgs } from "./bulkpickup.svelte";

const draft = { matchLabels: "", matchMode: "", onSent: "", blocked: "", keepNames: false };

describe("pickupArgs", () => {
  it("returns null when nothing is set", () => {
    expect(pickupArgs(["a"], draft, false)).toBeNull();
  });

  it("sends only the set fields and the name choice", () => {
    const a = pickupArgs(["a", "b"], { ...draft, matchLabels: " */agent-ready \n\nBug", onSent: " agent/picked-up ", keepNames: true }, true);
    expect(a).toEqual({
      projects: ["a", "b"],
      keepNames: true,
      dryRun: true,
      matchLabels: ["*/agent-ready", "Bug"],
      onSentSetLabel: "agent/picked-up",
    });
  });
});
