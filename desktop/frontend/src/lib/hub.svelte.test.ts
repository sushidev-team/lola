import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const { Status, Board, Org, Join, Leave, Claim, Release, Sync, DefaultDeviceName, Stats } = vi.hoisted(() => ({
  Stats: vi.fn(),
  Status: vi.fn(),
  Board: vi.fn(),
  Org: vi.fn(),
  Join: vi.fn(),
  Leave: vi.fn(),
  Claim: vi.fn(),
  Release: vi.fn(),
  Sync: vi.fn(),
  DefaultDeviceName: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  HubService: { Status, Board, Org, Join, Leave, Claim, Release, Sync, DefaultDeviceName, Stats },
}));

import { HubStore, HUB_LIGHT_BOARD_MS, HUB_LIGHT_POLL_MS, HUB_POLL_MS, OUTDATED_MESSAGE, SYNC_DONE_MS, SYNC_POLL_MS, SYNC_WAIT_MS, connectionState } from "./hub.svelte";

const joined = { enabled: true, joined: true, org: "acme", deviceName: "mac", claimsToday: 0, maxClaimsPerDay: 4, pendingOutcomes: 0, sessions: [] };

beforeEach(() => {
  for (const f of [Status, Board, Org, Join, Leave, Claim, Release, Sync, DefaultDeviceName, Stats]) f.mockReset();
  Stats.mockRejectedValue(new Error('unknown cmd "hubStats"'));
  Status.mockResolvedValue({ ...joined });
  Board.mockResolvedValue({ fetchedAt: "", items: [{ id: "it_1", label: "GHSA-x · lodash" }] });
  Org.mockResolvedValue({ org: "acme", devices: [] });
});

afterEach(() => vi.useRealTimers());

describe("HubStore", () => {
  it("polls only while a view watches", async () => {
    vi.useFakeTimers();
    const hub = new HubStore();
    expect(hub.polling).toBe(false);
    const release = hub.watch();
    await vi.waitFor(() => expect(Status).toHaveBeenCalledTimes(1));
    expect(hub.polling).toBe(true);
    // Settings only watches the status: no board or org traffic.
    expect(Board).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(HUB_POLL_MS);
    expect(Status).toHaveBeenCalledTimes(2);
    release();
    release(); // idempotent
    expect(hub.polling).toBe(false);
    await vi.advanceTimersByTimeAsync(HUB_POLL_MS * 3);
    expect(Status).toHaveBeenCalledTimes(2);
  });

  it("reads the board and devices for a board view, and one failure blanks nothing else", async () => {
    Org.mockRejectedValue(new Error("hub: org: hub not reachable"));
    const hub = new HubStore();
    const release = hub.watch({ board: true });
    await vi.waitFor(() => expect(hub.board).not.toBeNull());
    expect(hub.status?.org).toBe("acme");
    expect(hub.items[0].id).toBe("it_1");
    expect(hub.orgError).toMatch(/not reachable/);
    expect(hub.statusError).toBe("");
    release();
  });

  it("turns an older daemon's unknown cmd into one sentence", async () => {
    Status.mockRejectedValue(new Error('unknown cmd "hubStatus"'));
    const hub = new HubStore();
    await hub.refresh();
    expect(hub.outdated).toBe(true);
    expect(hub.statusError).toBe(OUTDATED_MESSAGE);
    expect(hub.loaded).toBe(true);
  });

  it("does not report not-joined as a board fault", async () => {
    Status.mockResolvedValue({ enabled: false, joined: false });
    Board.mockRejectedValue(new Error("hub: not joined (join a hub first)"));
    const hub = new HubStore();
    const release = hub.watch({ board: true });
    await vi.waitFor(() => expect(hub.loaded).toBe(true));
    expect(hub.boardError).toBe("");
    release();
  });

  it("passes the token through, keeps it nowhere and marks the action busy", async () => {
    let finish: (v: unknown) => void = () => {};
    Join.mockReturnValue(new Promise((r) => (finish = r)));
    const hub = new HubStore();
    const p = hub.join("https://hub.example.com", "tok_secret", "mac");
    expect(hub.busy).toBe("join");
    // A second action while one runs is refused, not queued.
    expect((await hub.sync()).ok).toBe(false);
    finish({ org: "acme", deviceName: "mac" });
    const r = await p;
    expect(r.ok).toBe(true);
    expect(Join).toHaveBeenCalledWith({ url: "https://hub.example.com", token: "tok_secret", name: "mac" });
    expect(hub.busy).toBe("");
    expect(JSON.stringify(hub)).not.toContain("tok_secret");
  });

  it("answers a refusal in the daemon's words", async () => {
    Claim.mockRejectedValue(new Error("hub: not claimed: project is not sandboxed (hub.require_sandbox)"));
    const hub = new HubStore();
    const r = await hub.claim("it_1", "web");
    expect(r).toEqual({ ok: false, error: "hub: not claimed: project is not sandboxed (hub.require_sandbox)" });
    expect(Claim).toHaveBeenCalledWith({ itemId: "it_1", project: "web" });
  });

  it("drops a poll answer that lands after a newer fresh read", async () => {
    const stale = { fetchedAt: "", items: [{ id: "it_1", state: "open" }] };
    const fresh = { fetchedAt: "", items: [{ id: "it_1", state: "claimed" }] };
    let finishOld: (v: unknown) => void = () => {};
    Board.mockReturnValueOnce(new Promise((r) => (finishOld = r))).mockResolvedValueOnce(fresh);
    const hub = new HubStore();
    const release = hub.watch({ board: true }); // the poll read: still in flight
    await vi.waitFor(() => expect(Board).toHaveBeenCalledTimes(1));
    await hub.refresh({ fresh: true }); // the read after a claim
    expect(hub.items[0].state).toBe("claimed");
    finishOld(stale);
    await new Promise((r) => setTimeout(r, 0));
    expect(hub.items[0].state).toBe("claimed");
    release();
  });

  it("reads the board right after a join, not on the next poll", async () => {
    vi.useFakeTimers();
    Status.mockResolvedValue({ enabled: false, joined: false });
    const hub = new HubStore();
    const release = hub.watch({ board: true });
    await vi.waitFor(() => expect(hub.loaded).toBe(true));
    // The first read had no status yet, so it asked for the board; clear that.
    await vi.advanceTimersByTimeAsync(0);
    Board.mockClear();
    Org.mockClear();
    await hub.refresh({ fresh: true });
    expect(Board).not.toHaveBeenCalled(); // still not joined: no board read
    Join.mockResolvedValue({ org: "acme", deviceName: "mac" });
    Status.mockResolvedValue({ ...joined });
    expect((await hub.join("https://hub.example.com", "tok", "mac")).ok).toBe(true);
    await vi.waitFor(() => expect(Board).toHaveBeenCalledTimes(1));
    expect(Org).toHaveBeenCalledTimes(1);
    await vi.waitFor(() => expect(hub.board).not.toBeNull());
    release();
  });

  it("releases, leaves and syncs through their own calls", async () => {
    Release.mockResolvedValue({ itemId: "it_1", message: "released" });
    Leave.mockResolvedValue({ left: true });
    Sync.mockResolvedValue({ queued: true });
    const hub = new HubStore();
    expect((await hub.release("it_1")).ok).toBe(true);
    expect((await hub.leave(true)).ok).toBe(true);
    expect(Leave).toHaveBeenCalledWith(true);
    expect((await hub.sync()).ok).toBe(true);
  });
});

describe("connectionState", () => {
  const now = Date.parse("2026-11-11T12:00:00Z");
  it("reads connected, error, waiting and stale", () => {
    expect(connectionState(null).label).toBe("Not joined");
    expect(connectionState({ ...joined, joined: false, membership: "revoked" } as never, now)).toEqual({ tone: "bad", label: "Removed by the hub" });
    expect(connectionState({ ...joined, joined: false, membership: "not joined" } as never, now).label).toBe("Not joined");
    expect(connectionState({ ...joined, lastHeartbeat: "2026-11-11T11:58:00Z" } as never, now)).toEqual({ tone: "good", label: "Connected" });
    expect(connectionState({ ...joined, lastHeartbeat: "2026-11-11T11:00:00Z" } as never, now).label).toBe("Not heard from lately");
    expect(connectionState({ ...joined } as never, now).label).toBe("Connecting…");
    expect(connectionState({ ...joined, lastHeartbeat: "2026-11-11T11:58:00Z", lastError: "boom", lastErrorAt: "2026-11-11T11:59:00Z" } as never, now).tone).toBe("bad");
    // An error older than the last good heartbeat is history, not the state.
    expect(connectionState({ ...joined, lastHeartbeat: "2026-11-11T11:59:00Z", lastError: "boom", lastErrorAt: "2026-11-11T11:58:00Z" } as never, now).tone).toBe("good");
  });
});

describe("HubStore.sync", () => {
  const at = (ms: number) => new Date(ms).toISOString();

  it("stays syncing until a newer heartbeat lands, then reads Synced and settles", async () => {
    vi.useFakeTimers();
    const t0 = Date.now();
    Status.mockResolvedValue({ ...joined, lastHeartbeat: at(t0 - 60_000) });
    Sync.mockResolvedValue({ queued: true });
    const hub = new HubStore();
    await hub.refresh();
    const run = hub.sync();
    expect(hub.syncPhase).toBe("syncing");
    await vi.advanceTimersByTimeAsync(SYNC_POLL_MS * 2);
    expect(hub.syncPhase).toBe("syncing");
    Status.mockResolvedValue({ ...joined, lastHeartbeat: at(Date.now()) });
    await vi.advanceTimersByTimeAsync(SYNC_POLL_MS);
    await run;
    expect(hub.syncPhase).toBe("synced");
    await vi.advanceTimersByTimeAsync(SYNC_DONE_MS);
    expect(hub.syncPhase).toBe("");
  });

  it("reports the pass's error, and a refused request", async () => {
    vi.useFakeTimers();
    Sync.mockResolvedValue({ queued: true });
    const hub = new HubStore();
    const run = hub.sync();
    Status.mockResolvedValue({ ...joined, lastError: "hub: heartbeat: 503", lastErrorAt: at(Date.now() + 1) });
    await vi.advanceTimersByTimeAsync(SYNC_POLL_MS);
    await run;
    expect(hub.syncPhase).toBe("failed");
    expect(hub.syncError).toBe("hub: heartbeat: 503");

    Sync.mockRejectedValue(new Error("daemon not running"));
    await hub.sync();
    expect(hub.syncPhase).toBe("failed");
    expect(hub.syncError).toBe("daemon not running");
  });

  it("says so when no pass shows up in time", async () => {
    vi.useFakeTimers();
    Sync.mockResolvedValue({ queued: true });
    const hub = new HubStore();
    const run = hub.sync();
    await vi.advanceTimersByTimeAsync(SYNC_WAIT_MS + SYNC_POLL_MS);
    await run;
    expect(hub.syncPhase).toBe("slow");
  });
});

describe("HubStore per-row busy", () => {
  it("keeps one claim from greying out another, and refuses a second press of the same", async () => {
    let finish: (v: unknown) => void = () => {};
    Claim.mockImplementation(({ itemId }: { itemId: string }) =>
      itemId === "a" ? new Promise((r) => (finish = r)) : Promise.resolve({ session: "s", project: "web", itemId }),
    );
    const hub = new HubStore();
    const a = hub.claim("a", "web");
    expect(hub.isBusy("claim:a")).toBe(true);
    expect(hub.isBusy("claim:b")).toBe(false);
    expect((await hub.claim("a", "web")).ok).toBe(false);
    expect((await hub.claim("b", "web")).ok).toBe(true);
    // Join waits for nothing to be running.
    expect((await hub.join("https://hub.example.com", "tok", "mac")).ok).toBe(false);
    expect(Join).not.toHaveBeenCalled();
    finish({ session: "s", project: "web", itemId: "a" });
    expect((await a).ok).toBe(true);
    expect(hub.isBusy("claim:a")).toBe(false);
    expect(hub.busy).toBe("");
  });
});

describe("HubStore light watch", () => {
  it("reads status each minute and the board every few minutes, never the org", async () => {
    vi.useFakeTimers();
    const hub = new HubStore();
    const release = hub.watchLight();
    expect(hub.lightPolling).toBe(true);
    expect(hub.polling).toBe(false);
    await vi.waitFor(() => expect(Status).toHaveBeenCalledTimes(1));
    await vi.waitFor(() => expect(Board).toHaveBeenCalledTimes(1));
    expect(Org).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(HUB_LIGHT_POLL_MS);
    expect(Status).toHaveBeenCalledTimes(2);
    expect(Board).toHaveBeenCalledTimes(1); // board still fresh
    await vi.advanceTimersByTimeAsync(HUB_LIGHT_BOARD_MS);
    expect(Board.mock.calls.length).toBeGreaterThanOrEqual(2);
    expect(Org).not.toHaveBeenCalled();
    expect(Stats).not.toHaveBeenCalled();
    release();
    release();
    expect(hub.lightPolling).toBe(false);
  });

  it("does not read the board while not joined", async () => {
    Status.mockResolvedValue({ enabled: false, joined: false, sessions: [] });
    const hub = new HubStore();
    hub.status = { enabled: false, joined: false } as never;
    const release = hub.watchLight();
    await vi.waitFor(() => expect(hub.loaded).toBe(true));
    expect(Board).not.toHaveBeenCalled();
    expect(hub.needsCount).toBe(0);
    release();
  });

  it("counts what needs a person for the badge", async () => {
    Board.mockResolvedValue({ fetchedAt: "", items: [
      { id: "c", kind: "dep_advisory", state: "open", severity: "critical", repo: "o/w", attempts: 0, reporters: 1, local: { claimable: true } },
      { id: "e", kind: "dep_advisory", state: "escalated", repo: "o/w", attempts: 3, reporters: 1, local: { claimable: false } },
      { id: "o", kind: "dep_advisory", state: "open", severity: "low", repo: "o/w", attempts: 0, reporters: 1, local: { claimable: true } },
    ] });
    const hub = new HubStore();
    const release = hub.watchLight();
    await vi.waitFor(() => expect(hub.needsCount).toBe(2));
    release();
  });

  it("survives a build without the hub bindings", async () => {
    Status.mockImplementation(() => {
      throw new Error("HubService.Status is not a function");
    });
    const hub = new HubStore();
    await hub.refresh({ light: true });
    expect(hub.statusError).toMatch(/not a function/);
  });
});

describe("HubStore notices", () => {
  it("tells the receiver about a chore this Mac took on its own, but not one a person claimed here", async () => {
    const seen: string[] = [];
    const hub = new HubStore();
    hub.onNotice = (n) => seen.push(n.text);
    await hub.refresh();
    Claim.mockResolvedValue({ session: "s1", project: "web", itemId: "mine" });
    Status.mockResolvedValue({ ...joined, sessions: [{ session: "s1", itemId: "mine", kind: "dep_advisory", summary: "GHSA-m" }] });
    await hub.claim("mine", "web");
    await hub.refresh({ fresh: true });
    expect(seen).toEqual([]);
    Status.mockResolvedValue({ ...joined, sessions: [
      { session: "s1", itemId: "mine", kind: "dep_advisory", summary: "GHSA-m" },
      { session: "s2", itemId: "auto", kind: "dep_advisory", summary: "GHSA-a" },
    ] });
    await hub.refresh({ fresh: true });
    expect(seen).toEqual(["This Mac took a team chore: GHSA-a"]);
  });
});

describe("HubStore stats and focus", () => {
  it("reads stats with the board, and an older daemon just has none", async () => {
    Stats.mockResolvedValue({ available: true, fixedThisWeek: 3, failedThisWeek: 0, openedThisWeek: 4, avgFixSeconds: 3600 });
    const hub = new HubStore();
    const release = hub.watch({ board: true });
    await vi.waitFor(() => expect(hub.stats?.fixedThisWeek).toBe(3));
    release();
    const old = new HubStore();
    Stats.mockRejectedValue(new Error('unknown cmd "hubStats"'));
    const r2 = old.watch({ board: true });
    await vi.waitFor(() => expect(Stats).toHaveBeenCalledTimes(2));
    expect(old.stats).toBeNull();
    expect(old.boardError).toBe("");
    r2();
  });

  it("keeps the item a chip asked for until the view takes it", () => {
    const hub = new HubStore();
    hub.focus("it_9");
    expect(hub.focusItem).toBe("it_9");
    hub.reset();
    expect(hub.focusItem).toBe("");
  });
});
