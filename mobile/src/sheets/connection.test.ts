import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@mobile/native", () => ({
  transport: {
    status: vi.fn(async () => ({ epoch: 4, phase: "ready", relay: "ok", framesIn: 10, framesOut: 20, bytesIn: 30, bytesOut: 40, launchedAt: 0 })),
    daemons: vi.fn(async () => ({ daemons: [] })),
    addListener: async () => ({ remove: async () => {} }),
  },
}));

const { HubStatus } = vi.hoisted(() => ({ HubStatus: vi.fn() }));
vi.mock("@mobile/wailsshim", () => ({ HubService: { Status: HubStatus } }));

import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { tiers } from "@mobile/lib/tiers.svelte";
import { connection } from "@mobile/lib/connection.svelte";
import { router } from "@mobile/lib/router.svelte";
import Connection from "./connection.svelte";

beforeEach(() => {
  router.sheet = "connection";
  connection.phase = "ready";
  connection.epoch = 4;
  connection.path = { kind: "relay" };
});

describe("connection sheet", () => {
  it("leads with a hero tile, the path word and one secondary line; no Via or Latency rows", () => {
    connection.raceMs = 180;
    render(Connection);
    expect(screen.getByRole("dialog", { name: "Connection" })).toBeTruthy();
    expect(screen.getByText("Relay")).toBeTruthy();
    expect(screen.getByText("180 ms")).toBeTruthy();
    expect(screen.queryByText("Via")).toBeNull();
    expect(screen.queryByText("Latency")).toBeNull();
  });

  it("says Wi-Fi and the latency on a direct path", () => {
    connection.raceMs = 180;
    connection.path = { kind: "direct", interface: "wifi" };
    render(Connection);
    expect(screen.getByText("Direct")).toBeTruthy();
    expect(screen.getByText("Wi-Fi · 180 ms")).toBeTruthy();
  });

  it("keeps Traffic inside Diagnostics, which opens Traffic, Epoch and Last race, never Relay: None", async () => {
    connection.raceMs = 180;
    render(Connection);
    expect(screen.queryByText("Epoch")).toBeNull();
    expect(screen.queryByText("Traffic")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: /Diagnostics/ }));
    expect(await screen.findByText("Traffic")).toBeTruthy();
    expect(screen.getByText("30 B, 10 frames in · 40 B, 20 frames out")).toBeTruthy();
    expect(screen.getByText("Epoch")).toBeTruthy();
    expect(screen.getByText("Last race")).toBeTruthy();
    expect(screen.queryByText("Frames in")).toBeNull();
    expect(screen.queryByText("None")).toBeNull();
  });

  it("says whether this phone can reach the Mac away from Wi-Fi, from the Mac's own relay state", async () => {
    tiers.set(["read"]);
    HubStatus.mockResolvedValue({ enabled: true, joined: true, relay: { state: "unsupported", reason: "no_relay", activeConns: 0, rendezvous: 1 } });
    render(Connection);
    expect(screen.getByText("Away from Wi-Fi")).toBeTruthy();
    await waitFor(() => expect(screen.getByText("Team hub needs an update.")).toBeTruthy());
    expect(screen.getByText("Update needed")).toBeTruthy();
  });

  it("says Available once the Mac's relay is connected", async () => {
    tiers.set(["read"]);
    HubStatus.mockResolvedValue({ enabled: true, joined: true, relay: { state: "connected", activeConns: 1, rendezvous: 1 } });
    render(Connection);
    await waitFor(() => expect(screen.getByText("Available")).toBeTruthy());
  });

  it("closes through the router", async () => {
    render(Connection);
    await fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(router.sheet).toBe("");
  });
});
