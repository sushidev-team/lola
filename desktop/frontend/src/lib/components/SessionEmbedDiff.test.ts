import { describe, it, expect, vi } from "vitest";
import { render, waitFor } from "@testing-library/svelte";
import { flushSync } from "svelte";

const Diff = vi.fn();
vi.mock("@bindings/desktop", () => ({
  DaemonService: {
    Diff: (...a: unknown[]) => Diff(...a),
    Alive: vi.fn().mockResolvedValue(false),
    Sessions: vi.fn(),
    Projects: vi.fn(),
    Status: vi.fn(),
  },
  ConfigService: {},
  TermService: { Shells: vi.fn(async () => []) },
  TalkService: { State: vi.fn(async () => ({ caps: {}, composer: "unknown", feedSeq: 0, health: "ok" })) },
}));

const { default: SessionEmbed } = await import("./SessionEmbed.svelte");
const { store } = await import("$lib/store.svelte");


describe("Session overview diff totals", () => {
  it("does not reload the diff when the session list is pushed again", async () => {
    Diff.mockResolvedValue({ session: "s1", base: "origin/main", mergeBase: "abc", files: [] });
    const s = (age: string) => ({ id: "s1", tmuxName: "s1", worktree: "/w", age }) as never;
    store.sessions = [s("0")];
    store.alive = true;
    render(SessionEmbed, { sessionId: "s1" });
    await waitFor(() => expect(Diff).toHaveBeenCalledTimes(1));
    for (let i = 1; i <= 3; i++) {
      store.sessions = [s(String(i))];
      flushSync();
      await new Promise((r) => setTimeout(r, 0));
    }
    expect(Diff).toHaveBeenCalledTimes(1);
  });
});
