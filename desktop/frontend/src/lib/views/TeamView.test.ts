import { describe, it, expect, vi, beforeEach } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/svelte";

const { Status, Board, Org, Claim, Release, Sync, Stats, RevokeDevice, HubProjects, SandboxLoginNeeded } = vi.hoisted(() => ({
  RevokeDevice: vi.fn(),
  HubProjects: vi.fn(),
  SandboxLoginNeeded: vi.fn(),
  Status: vi.fn(),
  Board: vi.fn(),
  Org: vi.fn(),
  Claim: vi.fn(),
  Release: vi.fn(),
  Sync: vi.fn(),
  Stats: vi.fn(),
}));

vi.mock("@bindings/desktop", async (orig) => ({
  ...(await orig<object>()),
  HubService: { Status, Board, Org, Claim, Release, Sync, Stats, RevokeDevice, Join: vi.fn(), Leave: vi.fn(), DefaultDeviceName: vi.fn(), Kinds: vi.fn() },
  ConfigService: { HubProjects: () => HubProjects(), SandboxLoginNeeded: (p: string, a: string) => SandboxLoginNeeded(p, a) },
}));

import TeamView from "./TeamView.svelte";
import { hub } from "$lib/hub.svelte";
import { confirm } from "$lib/confirm.svelte";
import { nav } from "$lib/nav.svelte";
import { sandboxLogin } from "$lib/sandboxlogin.svelte";
import { store } from "$lib/store.svelte";

const hoursAgo = (h: number) => new Date(Date.now() - h * 3_600_000).toISOString();

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
const mine = { ...base, id: "it_mine", state: "claimed", advisory: "GHSA-dddd", package: "ms", claimedAt: hoursAgo(1), lease: { mine: true, deviceName: "studio-mac", expires: new Date(Date.now() + 30 * 60_000).toISOString() }, local: { claimable: false } };
const theirs = { ...base, id: "it_theirs", state: "in_review", advisory: "GHSA-eeee", package: "ws", claimedAt: hoursAgo(2), owner: { mine: false, deviceName: "bobs-mac", member: "Bob" }, outcome: { prUrl: "https://github.com/acme/web/pull/9", delivery: "ci_pending" }, local: { claimable: false } };
const done = { ...base, id: "it_done", state: "done", advisory: "GHSA-ffff", package: "ini", attempts: 2, owner: { mine: false, deviceName: "bobs-mac", member: "Bob" }, outcome: { prUrl: "https://github.com/acme/web/pull/7", delivery: "merged" },
  history: [
    { action: "claimed", deviceName: "old-mac", at: "2026-11-02T00:00:00Z" },
    { action: "failed", reason: "agent_gave_up", deviceName: "old-mac", at: "2026-11-02T01:00:00Z" },
    { action: "completed", deviceName: "bobs-mac", member: "Bob", prUrl: "https://github.com/acme/web/pull/7", at: "2026-11-03T00:00:00Z" },
  ], local: { claimable: false } };
const escalated = { ...base, id: "it_esc", state: "escalated", advisory: "GHSA-gggg", package: "tar", attempts: 3, lastFail: "ci_failed", escalation: { pending: false, identifier: "SEC-12", url: "https://linear.app/acme/issue/SEC-12" }, local: { claimable: false } };

beforeEach(() => {
  cleanup();
  hub.reset();
  confirm.cancel();
  sandboxLogin.finish(false);
  for (const f of [Status, Board, Org, Claim, Release, Sync, Stats, SandboxLoginNeeded]) f.mockReset();
  Status.mockResolvedValue({ ...joined });
  SandboxLoginNeeded.mockResolvedValue("");
  Stats.mockRejectedValue(new Error('unknown cmd "hubStats"'));
  HubProjects.mockReset().mockResolvedValue([{ name: "web", hubChores: true }]);
  Board.mockResolvedValue({ fetchedAt: new Date().toISOString(), items: [open1, openMany, blocked, mine, theirs, done, escalated] });
  Org.mockResolvedValue({ fetchedAt: "", org: "acme", policyVersion: 1, devices: [
    { name: "studio-mac", me: true, freeSlots: 2, agents: ["claude"], active: true },
    { name: "bobs-mac", member: "Bob", me: false, lastSeen: new Date().toISOString(), freeSlots: 0, agents: ["codex"], active: true },
    { name: "old-mac", me: false, lastSeen: "2026-01-01T00:00:00Z", freeSlots: 1, agents: [], active: false },
  ] });
  store.projects = [{ name: "web", label: "Web" }, { name: "api", label: "API" }] as never;
  nav.closeOverlay();
});

const row = (id: string) => document.querySelector(`[data-board-item="${id}"]`) as HTMLElement;
const needsRow = (id: string) => document.querySelector(`[data-needs-item="${id}"]`) as HTMLElement;

/** Available rows sit in collapsed groups: open them all. */
async function expandGroups() {
  await waitFor(() => expect(document.querySelector("[data-board-group]")).not.toBeNull());
  await waitFor(() => expect(document.querySelector("[data-work-group]")).not.toBeNull());
  for (const b of document.querySelectorAll<HTMLElement>('[data-board-group] [aria-expanded="false"], [data-work-group] [aria-expanded="false"]')) await fireEvent.click(b);
}

describe("TeamView", () => {
  it("explains the hub and opens its settings when not joined", async () => {
    Status.mockResolvedValue({ enabled: false, joined: false, sessions: [] });
    render(TeamView);
    await screen.findByText("Share chores with your team");
    await fireEvent.click(screen.getByRole("button", { name: "Set up Team hub" }));
    expect(nav.overlay).toBe("settings");
    expect(nav.overlayTab).toBe("hub");
  });

  it("says when the hub removed this Mac, and offers to join again", async () => {
    Status.mockResolvedValue({ enabled: true, joined: false, membership: "revoked", sessions: [] });
    render(TeamView);
    await screen.findByText("This Mac was removed from the team");
    expect(screen.queryByText("Share chores with your team")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Join again" }));
    expect(nav.overlayTab).toBe("hub");
  });

  it("says when the daemon predates the hub", async () => {
    Status.mockRejectedValue(new Error('unknown cmd "hubStatus"'));
    render(TeamView);
    expect(await screen.findByRole("alert")).toHaveTextContent("Update the lola daemon to use Team hub");
  });

  it("offers to start the daemon when it does not answer", async () => {
    Status.mockRejectedValue(new Error("Error: daemon not running"));
    const start = vi.spyOn(store, "startDaemon").mockImplementation(() => undefined as never);
    render(TeamView);
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The lola daemon is not answering. Daemon not running");
    expect(alert.textContent).not.toMatch(/Error:/);
    await fireEvent.click(within(alert).getByRole("button", { name: "Start daemon" }));
    expect(start).toHaveBeenCalled();
  });

  it("shows an empty board calmly", async () => {
    Board.mockResolvedValue({ fetchedAt: "", items: [] });
    render(TeamView);
    expect(await screen.findByText("Nothing waiting. The team is caught up.")).toBeInTheDocument();
    expect(screen.getByText("No one is working a chore right now.")).toBeInTheDocument();
    expect(document.querySelector("[data-needs]")).toBeNull();
  });

  it("lays out a populated board", async () => {
    render(TeamView);
    await expandGroups();
    expect(screen.getByText("acme")).toBeInTheDocument();
    expect(document.querySelector("[data-connection]")).toHaveTextContent("Connected");
    expect(document.querySelector("[data-online]")).toHaveTextContent("2 of 3 Macs online");
    // Available: worst first, package from → fixed, reporters.
    const order = [...document.querySelectorAll("[data-board-group] [data-board-item]")].map((e) => e.getAttribute("data-board-item"));
    expect(order.slice(0, 3)).toEqual(["it_open", "it_many", "it_blocked"]);
    expect(within(row("it_open")).getByText("lodash 4.17.20 → 4.17.21")).toBeInTheDocument();
    expect(within(row("it_open")).getByText("2 reporters")).toBeInTheDocument();
    expect(row("it_open").querySelector("[data-severity]")).toHaveTextContent("Critical");
    // Not claimable: disabled, and the reason is visible text, not only a tooltip.
    expect(within(row("it_blocked")).getByRole("button", { name: "Claim" })).toBeDisabled();
    expect(row("it_blocked").querySelector("[data-claim-reason]")).toHaveTextContent("Can't claim here: The project is not sandboxed");
    // Summary cards.
    expect(document.querySelector('[data-team-card="open"]')).toHaveTextContent("3 open chores 1 critical 1 high 1 low");
    expect(document.querySelector('[data-team-card="mine"]')).toHaveTextContent("This Mac 1 chore · 1 session");
    expect(document.querySelector('[data-team-card="done"]')).toHaveTextContent("Done 1 · 1 escalated");
    const mineGroup = document.querySelector("[data-work-group][data-mine]") as HTMLElement;
    expect(within(mineGroup).getByText("This Mac")).toBeInTheDocument();
    await fireEvent.click(within(mineGroup).getByRole("button", { name: "Open session" }));
    expect(nav.focusedTerm).toBe("sess-9");
    // Macs: shape and words, never colour alone.
    const dev = document.querySelector('[data-device="studio-mac"]') as HTMLElement;
    expect(within(dev).getByText("This Mac")).toBeInTheDocument();
    const old = document.querySelector('[data-device="old-mac"]') as HTMLElement;
    expect(within(old).getByRole("img", { name: "Offline" })).toBeInTheDocument();
    expect(old.querySelector("[data-device-state]")).toHaveTextContent("Offline");
    expect(document.querySelector('[data-device="bobs-mac"]')).toHaveTextContent(/^\s*Bob/);
  });

  it("lets only a team admin remove another Mac, after a confirm", async () => {
    render(TeamView);
    await waitFor(() => expect(document.querySelector('[data-device="bobs-mac"]')).not.toBeNull());
    // A plain member sees no Remove.
    expect(screen.queryByRole("button", { name: /^Remove .* from the team$/ })).toBeNull();
    cleanup();
    hub.reset();

    Status.mockResolvedValue({ ...joined, admin: true });
    RevokeDevice.mockReset().mockResolvedValue({ revoked: true });
    render(TeamView);
    await waitFor(() => expect(screen.getByRole("button", { name: "Remove Bob from the team" })).toBeInTheDocument());
    // Never this Mac.
    expect(within(document.querySelector('[data-device="studio-mac"]') as HTMLElement).queryByRole("button")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Remove Bob from the team" }));
    expect(RevokeDevice).not.toHaveBeenCalled();
    expect(confirm.request?.title).toMatch(/Remove this Mac/);
    confirm.accept();
    // The hub knows a device by its name, not the member's label.
    await waitFor(() => expect(RevokeDevice).toHaveBeenCalledWith("bobs-mac"));

    RevokeDevice.mockRejectedValueOnce(new Error("hub: HTTP 403 forbidden: refused"));
    await fireEvent.click(screen.getByRole("button", { name: "Remove old-mac from the team" }));
    confirm.accept();
    const row = document.querySelector('[data-device="old-mac"]') as HTMLElement;
    await waitFor(() => expect(within(row).getByRole("alert")).toHaveTextContent(/did not accept this Mac as a team admin/));
  });

  it("shows who works a teammate's chore, for how long, and its pull request", async () => {
    const openURL = vi.spyOn(store, "openURL").mockResolvedValue(undefined as never);
    render(TeamView);
    await waitFor(() => expect(document.querySelector('[data-work-group^="d:bobs-mac"]')).not.toBeNull());
    const g = document.querySelector('[data-work-group^="d:bobs-mac"]') as HTMLElement;
    expect(g.querySelector("[data-work-who]")).toHaveTextContent("Bob · bobs-mac");
    expect(g.querySelector("[data-working-for]")).toHaveTextContent("In review for 2h");
    expect(g.querySelector("[data-delivery]")).toHaveTextContent("CI running");
    await fireEvent.click(within(g).getByRole("button", { name: "Open pull request" }));
    expect(openURL).toHaveBeenCalledWith("https://github.com/acme/web/pull/9");
    // Mine: working for, from the claim time; a teammate's has no release.
    const m = document.querySelector("[data-work-group][data-mine]") as HTMLElement;
    expect(m.querySelector("[data-working-for]")).toHaveTextContent("Working for 1h");
    expect(within(g).queryByRole("button", { name: /Release/ })).toBeNull();
  });

  it("never says lease, heartbeat, offer or device key, and never shows a raw error prefix", async () => {
    Status.mockResolvedValue({ ...joined, lastError: "Error: hub: heartbeat: lease renew refused", lastErrorAt: new Date(Date.now() + 1000).toISOString() });
    render(TeamView);
    await expandGroups();
    const text = document.querySelector("[data-team-view]")!.textContent ?? "";
    expect(text).not.toMatch(/\blease|heartbeat|\boffer|device key/i);
    expect(text).not.toMatch(/Error:/);
  });

  it("puts what needs a person at the top, with what to do about each", async () => {
    const openURL = vi.spyOn(store, "openURL").mockResolvedValue(undefined as never);
    const failedMine = { ...base, id: "it_fail", state: "failed", advisory: "GHSA-hhhh", attempts: 2, lastFail: "agent_gave_up", owner: { mine: true, deviceName: "studio-mac" }, outcome: { prUrl: "https://github.com/acme/web/pull/3", delivery: "closed" }, local: { claimable: true, project: "web" } };
    const lost = { ...base, id: "it_lost", state: "claimed", advisory: "GHSA-iiii", lease: { mine: false, deviceName: "bobs-mac", expires: "" }, local: { claimable: false } };
    const review = { ...base, id: "it_rev", state: "in_review", advisory: "GHSA-jjjj", claimedAt: hoursAgo(24 * 4), owner: { mine: true, deviceName: "studio-mac" }, outcome: { prUrl: "https://github.com/acme/web/pull/5", delivery: "review_pending" }, local: { claimable: false } };
    Status.mockResolvedValue({ ...joined, sessions: [...joined.sessions, { session: "sess-lost", itemId: "it_lost", kind: "dep_advisory", leaseLost: true, leaseTaken: true }] });
    Board.mockResolvedValue({ fetchedAt: "", items: [open1, failedMine, lost, review, escalated] });
    Claim.mockResolvedValue({ session: "s-new", project: "web", itemId: "it_fail" });
    render(TeamView);
    await waitFor(() => expect(document.querySelector("[data-needs]")).not.toBeNull());
    const kinds = [...document.querySelectorAll("[data-needs-item]")].map((e) => [e.getAttribute("data-needs-kind"), e.getAttribute("data-needs-item")]);
    expect(kinds).toEqual([["taken", "it_lost"], ["failed", "it_fail"], ["escalated", "it_esc"], ["review", "it_rev"], ["critical", "it_open"]]);
    // Why, in words: attempts and the last failure.
    expect(needsRow("it_fail").querySelector("[data-needs-why]")).toHaveTextContent("Failed · 2 attempts · last: the agent stopped without a pull request");
    expect(needsRow("it_esc").querySelector("[data-needs-why]")).toHaveTextContent("3 attempts · last: the pull request's checks stayed red");
    expect(needsRow("it_rev").querySelector("[data-needs-why]")).toHaveTextContent("In review for 4d · Review pending");
    // Actions.
    await fireEvent.click(within(needsRow("it_lost")).getByRole("button", { name: "Open session" }));
    expect(nav.focusedTerm).toBe("sess-lost");
    await fireEvent.click(within(needsRow("it_fail")).getByRole("button", { name: "Open PR" }));
    expect(openURL).toHaveBeenCalledWith("https://github.com/acme/web/pull/3");
    await fireEvent.click(within(needsRow("it_esc")).getByRole("button", { name: "Open Linear issue" }));
    expect(openURL).toHaveBeenCalledWith("https://linear.app/acme/issue/SEC-12");
    await fireEvent.click(within(needsRow("it_fail")).getByRole("button", { name: "Claim again" }));
    expect(confirm.request?.title).toBe("Claim this chore again?");
    confirm.accept();
    await waitFor(() => expect(Claim).toHaveBeenCalledWith({ itemId: "it_fail", project: "web" }));
    // Failed and escalated results are not hidden behind a toggle.
    expect(document.querySelector('[data-results="failed"] [data-board-item="it_fail"]')).not.toBeNull();
    expect(document.querySelector('[data-results="escalated"] [data-board-item="it_esc"]')).not.toBeNull();
  });

  it("says who finished a chore, when, after how many attempts, and keeps its history", async () => {
    render(TeamView);
    await waitFor(() => expect(document.querySelector("[data-board-group]")).not.toBeNull());
    await fireEvent.click(screen.getByRole("button", { name: /^Done/ }));
    const r = row("it_done");
    expect(r.querySelector("[data-delivery]")).toHaveTextContent("Merged");
    expect(r.querySelector("[data-finished-meta]")).toHaveTextContent(/Bob.*bobs-mac.*2 attempts/);
    await fireEvent.click(within(r).getByRole("button", { name: /History/ }));
    const hist = r.querySelector('[data-history="it_done"]') as HTMLElement;
    expect(within(hist).getByText("Failed: the agent stopped without a pull request")).toBeInTheDocument();
    expect(within(hist).getByText("Finished it")).toBeInTheDocument();
    expect(within(hist).getByText("· Bob · bobs-mac")).toBeInTheDocument();
  });

  it("claims through a confirm naming the project", async () => {
    Claim.mockResolvedValue({ session: "s-new", project: "web", itemId: "it_open" });
    const flash = vi.spyOn(store, "setFlash");
    render(TeamView);
    await expandGroups();
    await fireEvent.click(within(row("it_open")).getByRole("button", { name: "Claim" }));
    expect(confirm.request?.body).toBe("Claim GHSA-aaaa and start a session in Web?");
    expect(Claim).not.toHaveBeenCalled();
    confirm.accept();
    // The row named its one project, so the claim names it too: the daemon
    // would otherwise count every repo match and refuse.
    await waitFor(() => expect(Claim).toHaveBeenCalledWith({ itemId: "it_open", project: "web" }));
    // The same login check the store's open path runs went first.
    expect(SandboxLoginNeeded).toHaveBeenCalledWith("web", "");
    await waitFor(() => expect(flash).toHaveBeenCalledWith("Claimed GHSA-aaaa in Web"));
  });

  it("does not claim while the sandboxed agent has no login", async () => {
    SandboxLoginNeeded.mockResolvedValue("claude");
    render(TeamView);
    await expandGroups();
    await fireEvent.click(within(row("it_open")).getByRole("button", { name: "Claim" }));
    confirm.accept();
    await waitFor(() => expect(sandboxLogin.request).toEqual({ project: "web", agent: "claude" }));
    sandboxLogin.finish(false);
    expect(await within(row("it_open")).findByRole("alert")).toHaveTextContent("The sandboxed agent is not logged in yet.");
    expect(Claim).not.toHaveBeenCalled();
  });

  it("turns Docker being down into a fixable message", async () => {
    Claim.mockRejectedValue(new Error("hub: not claimed: Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"));
    render(TeamView);
    await expandGroups();
    await fireEvent.click(within(row("it_open")).getByRole("button", { name: "Claim" }));
    confirm.accept();
    const alert = await within(row("it_open")).findByRole("alert");
    expect(alert).toHaveTextContent("Docker is not running. Start Docker, then try again.");
    await fireEvent.click(within(alert).getByRole("button", { name: "Open doctor" }));
    expect(nav.overlay).toBe("doctor");
  });

  it("makes an ambiguous claim name a project, and shows a refusal", async () => {
    Claim.mockRejectedValue(new Error("hub: not claimed: dispatch held: load"));
    render(TeamView);
    await expandGroups();
    await fireEvent.click(within(row("it_many")).getByRole("button", { name: "Claim" }));
    expect(confirm.request).toBeNull();
    expect(within(row("it_many")).getByRole("alert")).toHaveTextContent("Choose the project to run it in.");
    const select = within(row("it_many")).getByLabelText("Project for GHSA-bbbb") as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: "api" } });
    await fireEvent.click(within(row("it_many")).getByRole("button", { name: "Claim" }));
    expect(confirm.request?.body).toBe("Claim GHSA-bbbb and start a session in API?");
    confirm.accept();
    await waitFor(() => expect(Claim).toHaveBeenCalledWith({ itemId: "it_many", project: "api" }));
    expect(await within(row("it_many")).findByText("New sessions are on hold on this Mac: load")).toBeInTheDocument();
  });

  it("keeps the other rows usable while one claim runs", async () => {
    let finish: (v: unknown) => void = () => {};
    Claim.mockReturnValue(new Promise((r) => (finish = r)));
    render(TeamView);
    await expandGroups();
    await fireEvent.click(within(row("it_open")).getByRole("button", { name: "Claim" }));
    confirm.accept();
    await waitFor(() => expect(within(row("it_open")).getByRole("button", { name: "Claim" })).toBeDisabled());
    expect(within(row("it_many")).getByRole("button", { name: "Claim" })).not.toBeDisabled();
    finish({ session: "s", project: "web", itemId: "it_open" });
  });

  it("releases my chore after a confirm", async () => {
    Release.mockResolvedValue({ itemId: "it_mine", session: "sess-9", sessionKept: true, message: "released; the session keeps running" });
    const flash = vi.spyOn(store, "setFlash");
    render(TeamView);
    await waitFor(() => expect(document.querySelector("[data-work-group][data-mine]")).not.toBeNull());
    await fireEvent.click(within(document.querySelector("[data-work-group][data-mine]") as HTMLElement).getByRole("button", { name: "Release…" }));
    expect(confirm.request?.title).toBe("Release this chore?");
    confirm.accept();
    await waitFor(() => expect(Release).toHaveBeenCalledWith("it_mine"));
    await waitFor(() => expect(flash).toHaveBeenCalledWith("Released; the session keeps running", "good"));
  });

  it("keeps every refusal when releasing a group", async () => {
    const two = { ...mine, id: "it_mine2", advisory: "GHSA-kkkk" };
    Status.mockResolvedValue({ ...joined, sessions: [...joined.sessions, { session: "sess-9", itemId: "it_mine2", kind: "dep_advisory" }] });
    Board.mockResolvedValue({ fetchedAt: "", items: [mine, two] });
    Release.mockImplementation((id: string) => Promise.reject(new Error(`hub: release ${id === "it_mine" ? "one" : "two"}: hub not reachable`)));
    render(TeamView);
    await waitFor(() => expect(document.querySelector("[data-work-group][data-mine]")).not.toBeNull());
    const g = document.querySelector("[data-work-group][data-mine]") as HTMLElement;
    await fireEvent.click(within(g).getByRole("button", { name: "Release all…" }));
    confirm.accept();
    const alert = await within(g).findByRole("alert");
    await waitFor(() => expect(alert).toHaveTextContent(/GHSA-dddd: Release one.*GHSA-kkkk: Release two/s));
  });

  it("syncs from the header", async () => {
    Sync.mockResolvedValue({ queued: true });
    render(TeamView);
    await waitFor(() => expect(document.querySelector("[data-board-group]")).not.toBeNull());
    await fireEvent.click(screen.getByRole("button", { name: "Sync now" }));
    await waitFor(() => expect(Sync).toHaveBeenCalled());
    // The next read carries a check-in newer than the press: the pass ran.
    Status.mockResolvedValue({ ...joined, lastHeartbeat: new Date(Date.now() + 60_000).toISOString() });
    expect(await screen.findByRole("button", { name: "Synced" }, { timeout: 3000 })).toBeInTheDocument();
    expect(screen.queryByText("Sync queued.")).not.toBeInTheDocument();
  });

  it("says how old the board is when the hub stops answering", async () => {
    render(TeamView);
    await waitFor(() => expect(document.querySelector("[data-board-group]")).not.toBeNull());
    expect(document.querySelector("[data-stale]")).toBeNull();
    Board.mockRejectedValue(new Error("hub: board: hub not reachable"));
    await hub.refresh({ fresh: true });
    await waitFor(() => expect(document.querySelector("[data-stale]")).not.toBeNull());
    expect(document.querySelector("[data-stale]")).toHaveTextContent(/As of .* · hub unreachable/);
    // The rows read before stay on screen.
    expect(document.querySelector("[data-board-group]")).not.toBeNull();
  });

  it("points to the project list when no project takes chores, and reads again after settings", async () => {
    HubProjects.mockResolvedValue([{ name: "web", hubChores: false }]);
    render(TeamView);
    expect(await screen.findByText("No project on this Mac takes chores yet.")).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: "Choose projects" }));
    expect(nav.overlay).toBe("settings");
    HubProjects.mockResolvedValue([{ name: "web", hubChores: true }]);
    nav.closeOverlay();
    await waitFor(() => expect(screen.queryByText("No project on this Mac takes chores yet.")).toBeNull());
  });

  it("groups the available chores per project and fixes them with one agent", async () => {
    Claim.mockResolvedValue({ session: "s-new", project: "web", itemId: "it_open" });
    render(TeamView);
    await waitFor(() => expect(document.querySelector("[data-board-group]")).not.toBeNull());
    const group = document.querySelector('[data-board-group="acme/web"]') as HTMLElement;
    expect(group.querySelector("[data-group-title]")).toHaveTextContent("Web");
    expect(group.querySelector("[data-group-count]")).toHaveTextContent("3 chores · 1 critical · 1 high · 1 low");
    expect(row("it_open")).toBeNull(); // collapsed until asked
    await fireEvent.click(within(group).getByRole("button", { name: "Fix all 2" }));
    expect(confirm.request?.body).toBe("Start one agent for 2 advisories in Web?");
    confirm.accept();
    await waitFor(() => expect(Claim).toHaveBeenCalledWith({ itemId: "it_open", itemIds: ["it_open", "it_many"], project: "web" }));
  });

  it("counts failing bumps in Fix all and claims each on its own", async () => {
    const bump = { ...base, id: "it_bump", kind: "red_bump_pr", state: "open", prNumber: 12, package: "vite", local: { claimable: true, project: "web" } };
    const adv2 = { ...open1, id: "it_open2", advisory: "GHSA-zzzz" };
    Board.mockResolvedValue({ fetchedAt: "", items: [open1, adv2, bump] });
    Claim.mockImplementation(({ itemId }: { itemId: string }) => Promise.resolve({ session: "s", project: "web", itemId }));
    render(TeamView);
    await waitFor(() => expect(document.querySelector("[data-board-group]")).not.toBeNull());
    const group = document.querySelector('[data-board-group="acme/web"]') as HTMLElement;
    await fireEvent.click(within(group).getByRole("button", { name: "Fix all 3" }));
    expect(confirm.request?.body).toBe("Start 2 agents in Web? One fixes the 2 advisories; each failing dependency bump gets its own.");
    confirm.accept();
    await waitFor(() => expect(Claim).toHaveBeenCalledTimes(2));
    expect(Claim).toHaveBeenCalledWith({ itemId: "it_open", itemIds: ["it_open", "it_open2"], project: "web" });
    expect(Claim).toHaveBeenCalledWith({ itemId: "it_bump", project: "web" });
  });

  it("asks for the project on a one-chore group and shows its refusal on the group", async () => {
    Board.mockResolvedValue({ fetchedAt: "", items: [{ ...openMany, repo: "acme/api" }] });
    Claim.mockRejectedValue(new Error("hub: not claimed: capped"));
    render(TeamView);
    await waitFor(() => expect(document.querySelector("[data-board-group]")).not.toBeNull());
    const group = document.querySelector('[data-board-group="acme/api"]') as HTMLElement;
    const select = within(group).getByLabelText(/^Project for/) as HTMLSelectElement;
    await fireEvent.click(within(group).getByRole("button", { name: "Claim" }));
    expect(within(group).getByRole("alert")).toHaveTextContent("Choose the project to run it in.");
    await fireEvent.change(select, { target: { value: "api" } });
    await fireEvent.click(within(group).getByRole("button", { name: "Claim" }));
    confirm.accept();
    await waitFor(() => expect(Claim).toHaveBeenCalledWith({ itemId: "it_many", project: "api" }));
    // Collapsed rows hide the row's own error line: the group header says it.
    expect(await within(group).findByText("This Mac already runs as many sessions as allowed.")).toBeInTheDocument();
  });

  it("shows one group per advisory on request", async () => {
    render(TeamView);
    await waitFor(() => expect(document.querySelector("[data-board-group]")).not.toBeNull());
    await fireEvent.click(screen.getByRole("button", { name: "By advisory" }));
    const titles = [...document.querySelectorAll("[data-group-title]")].map((e) => e.textContent?.trim());
    expect(titles).toEqual(["GHSA-aaaa", "GHSA-bbbb", "GHSA-cccc"]);
    expect(screen.queryByRole("button", { name: /Fix all/ })).not.toBeInTheDocument();
    // The blocked advisory says why once, on its group.
    expect(document.querySelector('[data-board-group="GHSA-cccc"] [data-group-reason]')).toHaveTextContent(/not sandboxed/);
    await fireEvent.click(screen.getByRole("button", { name: "By project" }));
  });

  it("offers updating all dependencies with chosen levels and a note", async () => {
    Claim.mockResolvedValue({ session: "s-new", project: "web", itemId: "it_open" });
    render(TeamView);
    await waitFor(() => expect(document.querySelector("[data-board-group]")).not.toBeNull());
    const group = document.querySelector('[data-board-group="acme/web"]') as HTMLElement;
    await fireEvent.click(within(group).getByRole("button", { name: "More ways to fix" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Update all dependencies…" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/fixes the 2 open advisories/)).toBeInTheDocument();
    const major = dialog.querySelector('[data-level="major"] input') as HTMLInputElement;
    expect(major.checked).toBe(false);
    expect(dialog.querySelector("[data-major-warning]")).toBeNull();
    await fireEvent.click(major);
    expect(dialog.querySelector("[data-major-warning]")).toHaveTextContent(/review the pull request closely/);
    await fireEvent.input(within(dialog).getByLabelText(/Instructions for the agent/), { target: { value: "  Keep vite on 5.x.  " } });
    await fireEvent.click(within(dialog).getByRole("button", { name: "Start update" }));
    await waitFor(() => expect(Claim).toHaveBeenCalledWith({
      itemId: "it_open", itemIds: ["it_open", "it_many"], project: "web",
      scope: { patch: true, minor: true, major: true, note: "Keep vite on 5.x." },
    }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("refuses to start an update with no level ticked", async () => {
    render(TeamView);
    await waitFor(() => expect(document.querySelector("[data-board-group]")).not.toBeNull());
    const group = document.querySelector('[data-board-group="acme/web"]') as HTMLElement;
    await fireEvent.click(within(group).getByRole("button", { name: "More ways to fix" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Update all dependencies…" }));
    const dialog = await screen.findByRole("dialog");
    for (const l of ["patch", "minor"]) await fireEvent.click(dialog.querySelector(`[data-level="${l}"] input`) as HTMLInputElement);
    expect(within(dialog).getByRole("button", { name: "Start update" })).toBeDisabled();
    expect(dialog.querySelector("[data-no-level]")).toHaveTextContent("Tick at least one level to update.");
    expect(Claim).not.toHaveBeenCalled();
  });

  it("marks the selected grouping with Button's selected state", async () => {
    render(TeamView);
    const byProject = await screen.findByRole("button", { name: "By project" });
    expect(byProject).toHaveAttribute("aria-pressed", "true");
    expect(byProject.className).toContain("bg-accent-fill");
    expect(screen.getByRole("button", { name: "By advisory" })).toHaveAttribute("aria-pressed", "false");
  });

  it("shows repositories the scan could not read and chores left unclaimed, with the fix", async () => {
    Status.mockResolvedValue({ ...joined,
      findingsHealth: [
        { repo: "acme/web", at: "", alerts: "no_access", pullRequests: "ok" },
        { repo: "acme/api", at: "", alerts: "ok", pullRequests: "ok" },
      ],
      offerSkips: [
        { itemId: "a", kind: "dep_advisory", reason: "the sandboxed agent has no login yet", at: "" },
        { itemId: "b", kind: "dep_advisory", reason: "the sandboxed agent has no login yet", at: "" },
      ],
    });
    render(TeamView);
    await waitFor(() => expect(document.querySelector("[data-loop-health]")).not.toBeNull());
    const web = document.querySelector('[data-health-repo="acme/web"]') as HTMLElement;
    expect(web).toHaveTextContent("No access to Dependabot alerts");
    expect(web).toHaveTextContent(/Turn on Dependabot alerts/);
    expect(document.querySelector('[data-health-repo="acme/api"]')).toBeNull();
    const skip = document.querySelector("[data-offer-skip]") as HTMLElement;
    expect(skip).toHaveTextContent("2 chores were not taken: The sandboxed agent is not logged in yet.");
    expect(skip).toHaveTextContent(/Log the agent in/);
  });

  it("shows the week's stats when the daemon has them", async () => {
    Stats.mockResolvedValue({ available: true, fixedThisWeek: 5, failedThisWeek: 1, openedThisWeek: 6, avgFixSeconds: 3 * 3600 });
    render(TeamView);
    await waitFor(() => expect(document.querySelector("[data-team-stats]")).toHaveTextContent("5 fixed this week · 3h to fix on average"));
  });

  it("opens on the chore a chip pointed at", async () => {
    render(TeamView);
    await waitFor(() => expect(document.querySelector("[data-board-group]")).not.toBeNull());
    expect(row("it_many")).toBeNull();
    hub.focus("it_many");
    await waitFor(() => expect(row("it_many")).not.toBeNull());
    expect(row("it_many").className).toContain("outline-accent");
    expect(hub.focusItem).toBe("");
  });
});
