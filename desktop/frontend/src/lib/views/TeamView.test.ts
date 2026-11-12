import { describe, it, expect, vi, beforeEach } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/svelte";

const { Status, Board, Org, Claim, Release, Sync } = vi.hoisted(() => ({
  Status: vi.fn(),
  Board: vi.fn(),
  Org: vi.fn(),
  Claim: vi.fn(),
  Release: vi.fn(),
  Sync: vi.fn(),
}));

vi.mock("@bindings/desktop", async (orig) => ({
  ...(await orig<object>()),
  HubService: { Status, Board, Org, Claim, Release, Sync, Join: vi.fn(), Leave: vi.fn(), DefaultDeviceName: vi.fn(), Kinds: vi.fn() },
}));

import TeamView from "./TeamView.svelte";
import { hub } from "$lib/hub.svelte";
import { confirm } from "$lib/confirm.svelte";
import { nav } from "$lib/nav.svelte";
import { store } from "$lib/store.svelte";

const joined = {
  enabled: true,
  joined: true,
  org: "acme",
  deviceName: "studio-mac",
  lastHeartbeat: new Date().toISOString(),
  claimsToday: 0,
  maxClaimsPerDay: 4,
  pendingOutcomes: 0,
  sessions: [{ session: "sess-9", itemId: "it_mine", kind: "dep_advisory" }],
};
const base = { kind: "dep_advisory", repo: "acme/web", label: "", attempts: 0, reporters: 2, firstSeen: "2026-11-01T00:00:00Z", lastSeen: "2026-11-10T00:00:00Z" };
const open1 = { ...base, id: "it_open", state: "open", advisory: "GHSA-aaaa", severity: "critical", package: "lodash", vulnerable: "4.17.20", fixed: "4.17.21", local: { claimable: true, project: "web" } };
const openMany = { ...base, id: "it_many", state: "open", advisory: "GHSA-bbbb", severity: "high", package: "axios", local: { claimable: true, projects: ["web", "api"] } };
const blocked = { ...base, id: "it_blocked", state: "open", advisory: "GHSA-cccc", severity: "low", package: "qs", local: { claimable: false, reason: "project is not sandboxed (hub.require_sandbox)" } };
const mine = { ...base, id: "it_mine", state: "claimed", advisory: "GHSA-dddd", package: "ms", lease: { mine: true, deviceName: "studio-mac", expires: new Date(Date.now() + 30 * 60_000).toISOString() }, local: { claimable: false } };
const theirs = { ...base, id: "it_theirs", state: "claimed", advisory: "GHSA-eeee", package: "ws", lease: { mine: false, deviceName: "bobs-mac", expires: new Date(Date.now() + 2 * 3_600_000).toISOString() }, local: { claimable: false } };
const done = { ...base, id: "it_done", state: "done", advisory: "GHSA-ffff", package: "ini", outcome: { prUrl: "https://github.com/acme/web/pull/7", delivery: "merged" }, local: { claimable: false } };
const escalated = { ...base, id: "it_esc", state: "escalated", advisory: "GHSA-gggg", package: "tar", escalation: { pending: false, identifier: "SEC-12", url: "https://linear.app/acme/issue/SEC-12" }, local: { claimable: false } };

beforeEach(() => {
  cleanup();
  hub.reset();
  confirm.cancel();
  for (const f of [Status, Board, Org, Claim, Release, Sync]) f.mockReset();
  Status.mockResolvedValue({ ...joined });
  Board.mockResolvedValue({ fetchedAt: "", items: [open1, openMany, blocked, mine, theirs, done, escalated] });
  Org.mockResolvedValue({ fetchedAt: "", org: "acme", policyVersion: 1, devices: [
    { name: "studio-mac", me: true, freeSlots: 2, agents: ["claude"], active: true },
    { name: "bobs-mac", me: false, lastSeen: new Date().toISOString(), freeSlots: 0, agents: ["codex"], active: true },
    { name: "old-mac", me: false, lastSeen: "2026-01-01T00:00:00Z", freeSlots: 1, agents: [], active: false },
  ] });
  store.projects = [{ name: "web", label: "Web" }, { name: "api", label: "API" }] as never;
  nav.closeOverlay();
});

const row = (id: string) => document.querySelector(`[data-board-item="${id}"]`) as HTMLElement;

describe("TeamView", () => {
  it("explains the hub and opens its settings when not joined", async () => {
    Status.mockResolvedValue({ enabled: false, joined: false, sessions: [] });
    render(TeamView);
    await screen.findByText("Share chores with your team");
    await fireEvent.click(screen.getByRole("button", { name: "Set up Team hub" }));
    expect(nav.overlay).toBe("settings");
    expect(nav.overlayTab).toBe("hub");
  });

  it("says when the daemon predates the hub", async () => {
    Status.mockRejectedValue(new Error('unknown cmd "hubStatus"'));
    render(TeamView);
    expect(await screen.findByRole("alert")).toHaveTextContent("Update the lola daemon to use Team hub");
  });

  it("shows an empty board calmly", async () => {
    Board.mockResolvedValue({ fetchedAt: "", items: [] });
    render(TeamView);
    expect(await screen.findByText("Nothing waiting. The team is caught up.")).toBeInTheDocument();
    expect(screen.getByText("No one is working a chore right now.")).toBeInTheDocument();
  });

  it("lays out a populated board", async () => {
    render(TeamView);
    await waitFor(() => expect(row("it_open")).not.toBeNull());
    expect(screen.getByText("acme")).toBeInTheDocument();
    expect(document.querySelector("[data-connection]")).toHaveTextContent("Connected");
    expect(document.querySelector("[data-online]")).toHaveTextContent("2 of 3 devices online");
    // Available: worst first, package from → fixed, reporters.
    const order = [...document.querySelectorAll("[data-board-item]")].map((e) => e.getAttribute("data-board-item"));
    expect(order.slice(0, 3)).toEqual(["it_open", "it_many", "it_blocked"]);
    expect(within(row("it_open")).getByText("lodash 4.17.20 → 4.17.21")).toBeInTheDocument();
    expect(within(row("it_open")).getByText("2 reporters")).toBeInTheDocument();
    expect(row("it_open").querySelector("[data-severity]")).toHaveTextContent("Critical");
    // Not claimable: disabled, with the daemon's reason.
    expect(within(row("it_blocked")).getByRole("button", { name: "Claim" })).toBeDisabled();
    expect(within(row("it_blocked")).getByText(/not sandboxed/)).toBeInTheDocument();
    // In progress: mine opens its session, a teammate's shows the device and lease.
    await fireEvent.click(within(row("it_mine")).getByRole("button", { name: "Open session" }));
    expect(nav.focusedTerm).toBe("sess-9");
    expect(within(row("it_theirs")).getByText("bobs-mac")).toBeInTheDocument();
    expect(within(row("it_theirs")).getByText("Lease ends in 1h")).toBeInTheDocument();
    // Done: the PR link goes through the daemon's openURL.
    const openURL = vi.spyOn(store, "openURL").mockResolvedValue(undefined as never);
    await fireEvent.click(screen.getByRole("button", { name: /^Done/ }));
    expect(row("it_done").querySelector("[data-delivery]")).toHaveTextContent("Merged");
    await fireEvent.click(within(row("it_done")).getByRole("button", { name: /Pull request/ }));
    expect(openURL).toHaveBeenCalledWith("https://github.com/acme/web/pull/7");
    await fireEvent.click(screen.getByRole("button", { name: /^Escalated/ }));
    await fireEvent.click(within(row("it_esc")).getByRole("button", { name: /SEC-12/ }));
    expect(openURL).toHaveBeenCalledWith("https://linear.app/acme/issue/SEC-12");
    // Devices.
    const dev = document.querySelector('[data-device="studio-mac"]') as HTMLElement;
    expect(within(dev).getByText("This Mac")).toBeInTheDocument();
    expect(within(document.querySelector('[data-device="old-mac"]') as HTMLElement).getByRole("img", { name: "Offline" })).toBeInTheDocument();
  });

  it("claims through a confirm naming the project", async () => {
    Claim.mockResolvedValue({ session: "s-new", project: "web", itemId: "it_open" });
    const flash = vi.spyOn(store, "setFlash");
    render(TeamView);
    await waitFor(() => expect(row("it_open")).not.toBeNull());
    await fireEvent.click(within(row("it_open")).getByRole("button", { name: "Claim" }));
    expect(confirm.request?.body).toBe("Claim GHSA-aaaa and start a session in Web?");
    expect(Claim).not.toHaveBeenCalled();
    confirm.accept();
    // The row named its one project, so the claim names it too: the daemon
    // would otherwise count every repo match and refuse.
    await waitFor(() => expect(Claim).toHaveBeenCalledWith({ itemId: "it_open", project: "web" }));
    await waitFor(() => expect(flash).toHaveBeenCalledWith("Claimed GHSA-aaaa in Web"));
  });

  it("makes an ambiguous claim name a project, and shows a refusal", async () => {
    Claim.mockRejectedValue(new Error("hub: not claimed: dispatch held: load"));
    render(TeamView);
    await waitFor(() => expect(row("it_many")).not.toBeNull());
    await fireEvent.click(within(row("it_many")).getByRole("button", { name: "Claim" }));
    expect(confirm.request).toBeNull();
    expect(within(row("it_many")).getByRole("alert")).toHaveTextContent("Choose the project to run it in.");
    const select = within(row("it_many")).getByLabelText("Project for GHSA-bbbb") as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: "api" } });
    await fireEvent.click(within(row("it_many")).getByRole("button", { name: "Claim" }));
    expect(confirm.request?.body).toBe("Claim GHSA-bbbb and start a session in API?");
    confirm.accept();
    await waitFor(() => expect(Claim).toHaveBeenCalledWith({ itemId: "it_many", project: "api" }));
    expect(await within(row("it_many")).findByText("Not claimed: dispatch held: load")).toBeInTheDocument();
  });

  it("releases my chore after a confirm", async () => {
    Release.mockResolvedValue({ itemId: "it_mine", session: "sess-9", sessionKept: true, message: "released; the session keeps running" });
    const flash = vi.spyOn(store, "setFlash");
    render(TeamView);
    await waitFor(() => expect(row("it_mine")).not.toBeNull());
    await fireEvent.click(within(row("it_mine")).getByRole("button", { name: "Release…" }));
    expect(confirm.request?.title).toBe("Release this chore?");
    confirm.accept();
    await waitFor(() => expect(Release).toHaveBeenCalledWith("it_mine"));
    await waitFor(() => expect(flash).toHaveBeenCalledWith("released; the session keeps running", "good"));
  });

  it("syncs from the header", async () => {
    Sync.mockResolvedValue({ queued: true });
    render(TeamView);
    await waitFor(() => expect(row("it_open")).not.toBeNull());
    await fireEvent.click(screen.getByRole("button", { name: "Sync now" }));
    await waitFor(() => expect(Sync).toHaveBeenCalled());
    // The next read carries a heartbeat newer than the press: the pass ran.
    Status.mockResolvedValue({ ...joined, lastHeartbeat: new Date(Date.now() + 60_000).toISOString() });
    expect(await screen.findByRole("button", { name: "Synced" }, { timeout: 3000 })).toBeInTheDocument();
    expect(screen.queryByText("Sync queued.")).not.toBeInTheDocument();
  });
});
