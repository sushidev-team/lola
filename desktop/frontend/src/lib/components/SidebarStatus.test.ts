import { describe, it, expect, vi, beforeEach } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/svelte";

const { Status, Board } = vi.hoisted(() => ({ Status: vi.fn(), Board: vi.fn() }));

vi.mock("@bindings/desktop", async (orig) => ({
  ...(await orig<object>()),
  HubService: { Status, Board, Org: vi.fn(), Stats: vi.fn() },
}));

import SidebarStatus from "./SidebarStatus.svelte";
import { hub } from "$lib/hub.svelte";
import { store } from "$lib/store.svelte";

const joined = { enabled: true, joined: true, org: "acme", deviceName: "mac", claimsToday: 0, maxClaimsPerDay: 4, pendingOutcomes: 0, sessions: [] };
const crit = { id: "c", kind: "dep_advisory", repo: "o/w", state: "open", severity: "critical", attempts: 0, reporters: 1, local: { claimable: true } };
const esc = { id: "e", kind: "dep_advisory", repo: "o/w", state: "escalated", attempts: 3, reporters: 1, local: { claimable: false } };

beforeEach(() => {
  cleanup();
  hub.reset();
  Status.mockReset().mockResolvedValue({ ...joined });
  Board.mockReset().mockResolvedValue({ fetchedAt: "", items: [crit, esc] });
});

describe("SidebarStatus team badge", () => {
  it("counts what needs a person on the Team icon, from the light poll", async () => {
    const { unmount } = render(SidebarStatus);
    expect(hub.lightPolling).toBe(true);
    const team = await screen.findByRole("button", { name: "Team, 2 chores need you" });
    expect(team.querySelector("[data-team-badge]")).toHaveTextContent("2");
    unmount();
    expect(hub.lightPolling).toBe(false);
  });

  it("shows no badge when nothing needs a person, or when not joined", async () => {
    Board.mockResolvedValue({ fetchedAt: "", items: [] });
    render(SidebarStatus);
    await waitFor(() => expect(Status).toHaveBeenCalled());
    expect(await screen.findByRole("button", { name: "Team" })).toBeInTheDocument();
    expect(document.querySelector("[data-team-badge]")).toBeNull();
  });

  it("routes the hub's off-screen notices to the toast", async () => {
    const flash = vi.spyOn(store, "setFlash");
    render(SidebarStatus);
    await waitFor(() => expect(hub.status?.joined).toBe(true));
    Status.mockResolvedValue({ ...joined, sessions: [{ session: "s", itemId: "x", kind: "dep_advisory", summary: "GHSA-x" }] });
    await hub.refresh({ fresh: true });
    expect(flash).toHaveBeenCalledWith("This Mac took a team chore: GHSA-x", "good");
  });
});
