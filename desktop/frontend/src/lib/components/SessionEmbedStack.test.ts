import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

const OpenURL = vi.fn().mockResolvedValue(undefined);
const PRReadiness = vi.fn().mockResolvedValue({ number: 9, url: 'https://github.com/acme/api/pull/9', state: 'OPEN', checks: 'fail', threads: [] });
vi.mock("@bindings/desktop", () => ({
  DaemonService: {
    Diff: vi.fn().mockResolvedValue({ session: "s1", base: "origin/main", mergeBase: "abc", files: [] }),
    OpenURL: (...a: unknown[]) => OpenURL(...a),
    PRReadiness: (...a: unknown[]) => PRReadiness(...a),
    Alive: vi.fn().mockResolvedValue(false),
    Sessions: vi.fn(),
    Projects: vi.fn(),
    Status: vi.fn(),
  },
  ConfigService: {},
  TermService: { Shells: vi.fn(async () => []) },
}));

const { default: SessionBar } = await import("./session/SessionBar.svelte");
const { default: BeforeMerge } = await import("./session/BeforeMerge.svelte");
const { store } = await import("$lib/store.svelte");

// Sandbox details live on the title's hover card; stack PRs live in PR status.
describe("SessionBar sandbox + stack apps", () => {
  it("names the sandbox on hover and opens the focused app's PR from PR status", async () => {
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

    expect(screen.queryByRole("button", { name: "Delivery" })).toBeNull();
    render(BeforeMerge, { session: store.sessions[0] });
    await waitFor(() => expect(PRReadiness).toHaveBeenCalledWith('s1~api', 9));
    expect(screen.queryByRole("menuitem", { name: /Open PR/ })).toBeNull();
    expect(screen.getByRole("region", { name: "PR status" })).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /#9/ })).toHaveLength(1);
    await fireEvent.click(screen.getByRole("button", { name: /#9/ }));
    expect(OpenURL).toHaveBeenCalledWith("https://github.com/acme/api/pull/9");
  });
});
