import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const { Status, Board, Org, Join, Leave, Claim, Release, Sync, DefaultDeviceName } = vi.hoisted(() => ({
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
  HubService: { Status, Board, Org, Join, Leave, Claim, Release, Sync, DefaultDeviceName },
}));

import { HubStore, HUB_POLL_MS, OUTDATED_MESSAGE, connectionState } from "./hub.svelte";

const joined = { enabled: true, joined: true, org: "acme", deviceName: "mac", claimsToday: 0, maxClaimsPerDay: 4, pendingOutcomes: 0, sessions: [] };

beforeEach(() => {
  for (const f of [Status, Board, Org, Join, Leave, Claim, Release, Sync, DefaultDeviceName]) f.mockReset();
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
    expect(connectionState({ ...joined, lastHeartbeat: "2026-11-11T11:58:00Z" } as never, now)).toEqual({ tone: "good", label: "Connected" });
    expect(connectionState({ ...joined, lastHeartbeat: "2026-11-11T11:00:00Z" } as never, now).label).toBe("Stale");
    expect(connectionState({ ...joined } as never, now).label).toBe("Waiting for first heartbeat");
    expect(connectionState({ ...joined, lastHeartbeat: "2026-11-11T11:58:00Z", lastError: "boom", lastErrorAt: "2026-11-11T11:59:00Z" } as never, now).tone).toBe("bad");
    // An error older than the last good heartbeat is history, not the state.
    expect(connectionState({ ...joined, lastHeartbeat: "2026-11-11T11:59:00Z", lastError: "boom", lastErrorAt: "2026-11-11T11:58:00Z" } as never, now).tone).toBe("good");
  });
});
