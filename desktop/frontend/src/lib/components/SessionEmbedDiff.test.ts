import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/svelte";
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
}));

const { default: SessionEmbed } = await import("./SessionEmbed.svelte");
const { store } = await import("$lib/store.svelte");
const { terms, DIFF } = await import("$lib/terms.svelte");

// The daemon pushes a fresh session list about once a second. SessionEmbed
// passes `session.id` down, so a diff tab that tracked the prop rather than its
// value blanked and re-ran git on every push.
describe("SessionEmbed diff tab", () => {
  it("does not reload the diff when the session list is pushed again", async () => {
    Diff.mockResolvedValue({ session: "s1", base: "origin/main", mergeBase: "abc", files: [] });
    const s = (age: string) => ({ id: "s1", tmuxName: "s1", worktree: "/w", age }) as never;
    store.sessions = [s("0")];
    terms.select("s1", DIFF);
    render(SessionEmbed, { sessionId: "s1", focused: true });
    await screen.findByText(/no changes/);
    for (let i = 1; i <= 3; i++) {
      store.sessions = [s(String(i))];
      flushSync();
      await new Promise((r) => setTimeout(r, 0));
    }
    expect(Diff).toHaveBeenCalledTimes(1);
    expect(screen.getByText(/no changes/)).toBeInTheDocument();
  });
});
