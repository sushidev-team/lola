import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/svelte";

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

const { default: SessionBar } = await import("./session/SessionBar.svelte");
const { store } = await import("$lib/store.svelte");

// SUSHI-628: a sandboxed session says so, and a STACK session shows one row
// per app — its PR (clickable), or whether it has changes yet — instead of a
// single "Open PR" that would reach only one repository. Both moved out of the
// band when the header went: the sandbox to the title's hover card, the apps to
// the delivery chip's popover.
describe("SessionBar sandbox + stack apps", () => {
  it("names the sandbox on hover and lists one row per app in the chip's popover", async () => {
    store.sessions = [
      {
        id: "s1",
        tmuxName: "s1",
        worktree: "/w",
        status: "working",
        sandbox: "docker",
        stack: true,
        focusApp: "api",
        prNumber: 9,
        prUrl: "https://github.com/acme/api/pull/9",
        apps: [
          { name: "web", label: "Web", branch: "lola/x", touched: true, ports: [4100, 4101] },
          { name: "api", branch: "lola/x", number: 9, url: "https://github.com/acme/api/pull/9", state: "OPEN", delivery: "ci_failed" },
          { name: "docs", branch: "lola/x" },
        ],
      } as never,
    ];
    render(SessionBar, { sessionId: "s1" });
    // No filled chip for it any more, only the card.
    expect(screen.queryByText("docker")).toBeNull();
    await fireEvent.pointerEnter(document.querySelector("[data-session-bar] [tabindex='0']")!);
    expect(await screen.findByText("docker")).toBeInTheDocument();
    await fireEvent.pointerLeave(document.querySelector("[data-session-bar] [tabindex='0']")!);

    await fireEvent.click(screen.getByRole("button", { name: "Delivery" }));
    expect(screen.queryByRole("menuitem", { name: /Open PR/ })).toBeNull();
    const strip = screen.getByRole("list", { name: "Apps" });
    const items = within(strip).getAllByRole("listitem");
    expect(items.map((li) => li.textContent?.replace(/\s+/g, " ").trim())).toEqual([
      expect.stringMatching(/^Web · changes, no PR :4100, :4101$/),
      expect.stringMatching(/^api · #9/),
      expect.stringMatching(/^docs · untouched$/),
    ]);
    await fireEvent.click(within(items[1]).getByRole("button", { name: /#9/ }));
    expect(OpenURL).toHaveBeenCalledWith("https://github.com/acme/api/pull/9");
  });
});
