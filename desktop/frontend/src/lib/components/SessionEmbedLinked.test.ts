import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";

const OpenURL = vi.fn().mockResolvedValue(undefined);
vi.mock("@bindings/desktop", () => ({
  DaemonService: {
    Diff: vi.fn().mockResolvedValue({ session: "s1", base: "origin/main", mergeBase: "abc", files: [] }),
    OpenURL: (...a: unknown[]) => OpenURL(...a),
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

// SUSHI-628: a sandboxed session says so, and every linked repository's PR
// gets its own button beside the primary one; a linked repo with no PR yet
// gets none.
describe("SessionEmbed sandbox + linked PRs", () => {
  it("shows the sandbox chip and one button per linked PR", async () => {
    store.sessions = [
      {
        id: "s1",
        tmuxName: "s1",
        worktree: "/w",
        status: "working",
        sandbox: "docker",
        prNumber: 3,
        prUrl: "https://github.com/acme/web/pull/3",
        linkedPrs: [
          { name: "api", repo: "acme/api", branch: "lola/x", number: 9, url: "https://github.com/acme/api/pull/9", state: "OPEN" },
          { name: "docs", branch: "lola/x" },
        ],
      } as never,
    ];
    // The diff tab, not the live terminal: xterm cannot draw in jsdom.
    terms.select("s1", DIFF);
    render(SessionEmbed, { sessionId: "s1", focused: true });
    expect(screen.getByText("docker")).toBeInTheDocument();
    const btn = screen.getByRole("button", { name: /api PR/ });
    expect(screen.queryByRole("button", { name: /docs PR/ })).toBeNull();
    await fireEvent.click(btn);
    expect(OpenURL).toHaveBeenCalledWith("https://github.com/acme/api/pull/9");
  });
});
