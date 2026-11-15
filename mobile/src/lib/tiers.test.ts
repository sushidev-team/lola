import { describe, it, expect, beforeEach, vi } from "vitest";

const listeners = new Map<string, (e: never) => void>();
vi.mock("@mobile/native", () => ({
  transport: {
    addListener: async (name: string, cb: (e: never) => void) => {
      listeners.set(name, cb);
      return { remove: async () => void listeners.delete(name) };
    },
  },
}));

import { TIER } from "@mobile/wire/tiers";
import { PTY_WRITE_TIER, can, canWritePane, installTiers, tiers } from "./tiers.svelte";

beforeEach(() => {
  tiers.clear();
  listeners.clear();
});

describe("tiers store", () => {
  it("holds nothing until set", () => {
    expect(can("agentSay")).toBe(false);
  });

  it("answers can() from the held tiers", () => {
    tiers.set(["read", "answer"]);
    expect(can("diff")).toBe(true);
    expect(can("agentSay")).toBe(true);
    expect(can("mergePR")).toBe(false);
    expect(can("agentReport")).toBe(false);
    expect(can("noSuchCommand")).toBe(false);
  });

  it("types into a pane only with the pty write tier, the tier that also opens a shell", () => {
    expect(PTY_WRITE_TIER).toBe(TIER.shellCreate);
    tiers.set(["read", "answer", "control", "destructive"]);
    expect(canWritePane()).toBe(false);
    tiers.set([PTY_WRITE_TIER]);
    expect(canWritePane()).toBe(true);
  });

  it("never grants a `never` command even if the device claims the tier", () => {
    tiers.set(["read", "answer", "control", "destructive", "shell", "never"]);
    expect(can("devices")).toBe(false);
  });

  it("is replaced by the next epoch's device event and ignores an older one", async () => {
    const off = installTiers();
    await vi.waitFor(() => expect(listeners.has("device")).toBe(true));
    const emit = listeners.get("device")!;
    emit({ epoch: 2, device: { tiers: ["read"] } } as never);
    expect(can("agentSay")).toBe(false);
    emit({ epoch: 3, device: { tiers: ["read", "answer"] } } as never);
    expect(can("agentSay")).toBe(true);
    emit({ epoch: 2, device: { tiers: ["read"] } } as never);
    expect(can("agentSay")).toBe(true);
    off();
    await vi.waitFor(() => expect(listeners.has("device")).toBe(false));
  });
});
