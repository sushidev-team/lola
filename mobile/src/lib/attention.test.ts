import { beforeEach, describe, expect, it, vi } from "vitest";

const haptic = vi.hoisted(() => vi.fn());
vi.mock("./haptics", () => ({ haptic }));

import type { SessionInfo } from "$lib/store.svelte";
import { ATTENTION_GAP_MS, AttentionWatcher } from "./attention";

const s = (id: string, over: Partial<SessionInfo> = {}): SessionInfo =>
  ({ id, project: "p", issue: id, title: id, branch: "b", agentState: "working", delivery: "none", age: "1m", prNumber: 0, ...over }) as SessionInfo;
const needing = (id: string): SessionInfo => s(id, { agentState: "waiting_input" });

beforeEach(() => haptic.mockClear());

describe("AttentionWatcher", () => {
  it("stays quiet on the first snapshot, even with sessions that need you", () => {
    const w = new AttentionWatcher();
    expect(w.ingest([needing("a"), s("b")], null, 1000)).toBe(false);
    expect(haptic).not.toHaveBeenCalled();
  });

  it("buzzes once when a session moves into needs you", () => {
    const w = new AttentionWatcher();
    w.ingest([s("a"), s("b")], null, 0);
    expect(w.ingest([needing("a"), s("b")], null, 10_000)).toBe(true);
    expect(haptic).toHaveBeenCalledWith("attention", 10_000);
    // Still needing on the next push is not news.
    expect(w.ingest([needing("a"), s("b")], null, 20_000)).toBe(false);
    expect(haptic).toHaveBeenCalledTimes(1);
  });

  it("counts a new session that arrives already needing you", () => {
    const w = new AttentionWatcher();
    w.ingest([s("a")], null, 0);
    expect(w.ingest([s("a"), needing("n")], null, 10_000)).toBe(true);
  });

  it("skips the session on screen", () => {
    const w = new AttentionWatcher();
    w.ingest([s("a")], "a", 0);
    expect(w.ingest([needing("a")], "a", 10_000)).toBe(false);
    // It stays known as needing, so leaving it is not a fresh alert.
    expect(w.ingest([needing("a")], null, 20_000)).toBe(false);
  });

  it("buzzes once for several sessions in one push, and rate-limits the next", () => {
    const w = new AttentionWatcher();
    w.ingest([s("a"), s("b"), s("c")], null, 0);
    w.ingest([needing("a"), needing("b"), s("c")], null, 10_000);
    expect(haptic).toHaveBeenCalledTimes(1);
    expect(w.ingest([needing("a"), needing("b"), needing("c")], null, 10_000 + ATTENTION_GAP_MS - 1)).toBe(false);
    expect(haptic).toHaveBeenCalledTimes(1);
    // The rate-limited one is not buzzed later: it is known now.
    expect(w.ingest([needing("a"), needing("b"), needing("c")], null, 10_000 + ATTENTION_GAP_MS + 1)).toBe(false);
  });

  it("buzzes again when a session needs you a second time", () => {
    const w = new AttentionWatcher();
    w.ingest([needing("a")], null, 0);
    w.ingest([s("a")], null, 10_000);
    expect(w.ingest([needing("a")], null, 20_000)).toBe(true);
  });

  it("counts a CI failure as needing you", () => {
    const w = new AttentionWatcher();
    w.ingest([s("a", { delivery: "ci_pending" })], null, 0);
    expect(w.ingest([s("a", { delivery: "ci_failed" })], null, 10_000)).toBe(true);
  });
});
