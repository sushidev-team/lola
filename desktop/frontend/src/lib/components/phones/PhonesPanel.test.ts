import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/svelte";

const { Devices, Begin, Status, Confirm, Reject, Revoke, SetTiers, HubStatus } = vi.hoisted(() => ({
  Devices: vi.fn(),
  Begin: vi.fn(),
  Status: vi.fn(),
  Confirm: vi.fn(),
  Reject: vi.fn(),
  Revoke: vi.fn(),
  SetTiers: vi.fn(),
  HubStatus: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  PhonesService: { Devices, Begin, Status, Confirm, Reject, Revoke, SetTiers },
  HubService: {
    Status: () => HubStatus(),
    Board: vi.fn().mockResolvedValue({ items: [] }),
    Org: vi.fn().mockResolvedValue({ devices: [] }),
  },
}));

import PhonesPanel from "./PhonesPanel.svelte";
import { confirm } from "$lib/confirm.svelte";

const phone = (over: Record<string, unknown> = {}) => ({
  id: "dev1",
  name: "Martin's iPhone",
  tiers: ["read", "answer", "control", "destructive", "shell"],
  pairedAt: "2026-11-01T10:00:00Z",
  connected: 0,
  fingerprint: "ab12 cd34 ef56 7890",
  ...over,
});

beforeEach(() => {
  Devices.mockResolvedValue({ devices: [phone()] });
  HubStatus.mockResolvedValue({ joined: true, relay: { state: "connected", activeConns: 0, rendezvous: 0 } });
  SetTiers.mockImplementation((id: string, tiers: string[]) => Promise.resolve(phone({ id, tiers })));
  Revoke.mockResolvedValue({ id: "dev1", closed: 0 });
});
afterEach(() => {
  cleanup();
  confirm.cancel();
  vi.clearAllMocks();
});

describe("PhonesPanel", () => {
  it("lists paired phones with fingerprint and last seen", async () => {
    Devices.mockResolvedValue({ devices: [phone({ lastSeenAt: new Date(Date.now() - 2 * 3600_000).toISOString() })] });
    render(PhonesPanel);
    const row = await screen.findByRole("listitem", { name: "Martin's iPhone" });
    expect(row).toHaveTextContent("ab12 cd34 ef56 7890");
    expect(row).toHaveTextContent("Last seen 2h ago");
  });

  it("shows the live connection count", async () => {
    Devices.mockResolvedValue({ devices: [phone({ connected: 2 })] });
    render(PhonesPanel);
    expect(await screen.findByText("Connected · 2")).toBeInTheDocument();
  });

  it("says so when no phone is paired", async () => {
    Devices.mockResolvedValue({ devices: [] });
    render(PhonesPanel);
    expect(await screen.findByText("No phones paired.")).toBeInTheDocument();
  });

  it("shows a broken registry", async () => {
    Devices.mockResolvedValue({ devices: [], problem: "devices.json is unreadable" });
    render(PhonesPanel);
    expect(await screen.findByText("devices.json is unreadable")).toBeInTheDocument();
  });

  it("sends the new tier set when a toggle changes", async () => {
    render(PhonesPanel);
    const row = await screen.findByRole("listitem", { name: "Martin's iPhone" });
    await fireEvent.click(within(row).getByRole("checkbox", { name: "Terminal input" }));
    await waitFor(() => expect(SetTiers).toHaveBeenCalledWith("dev1", ["read", "answer", "control", "destructive"]));
  });

  it("keeps read on", async () => {
    render(PhonesPanel);
    const row = await screen.findByRole("listitem", { name: "Martin's iPhone" });
    expect(within(row).getByRole("checkbox", { name: "Read" })).toBeDisabled();
  });

  it("asks before revoking", async () => {
    render(PhonesPanel);
    const row = await screen.findByRole("listitem", { name: "Martin's iPhone" });
    await fireEvent.click(within(row).getByRole("button", { name: "Revoke" }));
    expect(Revoke).not.toHaveBeenCalled();
    expect(confirm.request).toMatchObject({ title: "Revoke phone?", body: "It loses access now.", confirmLabel: "Revoke" });
    confirm.accept();
    await waitFor(() => expect(Revoke).toHaveBeenCalledWith("dev1"));
  });

  it("reads the relay state from the hub status", async () => {
    render(PhonesPanel);
    expect(await screen.findByText("Relay status: Connected")).toBeInTheDocument();
  });

  it("binds the relay checkbox", async () => {
    render(PhonesPanel, { relay: true });
    const box = await screen.findByRole("checkbox", { name: "Reach this Mac away from home" });
    expect(box).toBeChecked();
    await fireEvent.click(box);
    expect(box).not.toBeChecked();
  });

  it("opens the pairing dialog", async () => {
    Begin.mockResolvedValue({ pairing: "A", payload: "lola2.x", expiresAt: new Date(Date.now() + 60_000).toISOString() });
    Status.mockReturnValue(new Promise(() => {}));
    render(PhonesPanel);
    await fireEvent.click(screen.getByRole("button", { name: "Pair a phone" }));
    expect(await screen.findByRole("dialog", { name: "Pair a phone" })).toBeInTheDocument();
  });
});
