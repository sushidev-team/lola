import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";

const h = vi.hoisted(() => {
  const t = {
    listener: null as null | ((e: unknown) => void),
    pairBegin: vi.fn(),
    pairCancel: vi.fn(async () => {}),
    setIdleTimerDisabled: vi.fn(async () => {}),
    openURL: vi.fn(async () => ({ opened: true })),
    addListener: vi.fn(),
  };
  t.addListener.mockImplementation(async (_n: string, f: (e: unknown) => void) => {
    t.listener = f;
    return { remove: async () => {} };
  });
  return { t, cap: vi.fn(), scanOnce: vi.fn() };
});

vi.mock("@mobile/native", () => ({ transport: h.t, native: null }));
vi.mock("@mobile/lib/scan", () => ({ scanCapability: h.cap, scanOnce: h.scanOnce }));
vi.mock("@mobile/lib/haptics", () => ({ haptic: vi.fn() }));
vi.mock("@mobile/lib/connection.svelte", () => ({
  connection: {
    daemonId: "",
    diagnosis: { retryable: false },
    refresh: async () => [],
    reconnect: async () => false,
    connect: async () => true,
    retryLater: () => {},
  },
}));

import { copy } from "@mobile/lib/copy";
import { pairing } from "@mobile/lib/pairing.svelte";
import { router } from "@mobile/lib/router.svelte";
import Connect from "./Connect.svelte";

const settle = () => new Promise((r) => setTimeout(r, 0));

function open(state: Parameters<typeof router.replace>[0] & { name: "connect" }) {
  router.stack = [state];
  return render(Connect);
}

beforeEach(() => {
  h.cap.mockResolvedValue({ available: true });
  h.scanOnce.mockResolvedValue({ kind: "cancelled" });
  h.t.pairBegin.mockResolvedValue({ code: "482913", path: { kind: "direct" } });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  router.stack = [{ name: "tab", tab: "inbox" }];
});

describe("Connect", () => {
  it("presents the scanner on appear and offers a rescan after a cancel", async () => {
    open({ name: "connect", state: "scan" });
    await settle();
    expect(h.scanOnce).toHaveBeenCalledOnce();
    expect(screen.getByRole("heading", { name: copy.connect.scan.title })).toBeInTheDocument();
    expect(screen.getByText(copy.connect.scan.body)).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: "Scan again" }));
    expect(h.scanOnce).toHaveBeenCalledTimes(2);
  });

  it("offers Scan code before any scan ended", async () => {
    let finish: (v: unknown) => void = () => {};
    h.scanOnce.mockReturnValue(new Promise((r) => (finish = r)));
    open({ name: "connect", state: "scan" });
    await settle();
    expect(screen.getByRole("button", { name: "Scan code" })).toBeInTheDocument();
    finish({ kind: "cancelled" });
    await waitFor(() => expect(screen.getByRole("button", { name: "Scan again" })).toBeInTheDocument());
  });

  it("offers Pair again as the quiet second action on a dropped link", async () => {
    open({ name: "connect", state: "dropped" });
    await settle();
    expect(screen.getByRole("button", { name: "Reconnect" })).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: "Pair again" }));
    expect(h.scanOnce).toHaveBeenCalledOnce();
  });

  it("shows the grouped code, the wait line and a Cancel that returns to the scan", async () => {
    h.scanOnce.mockResolvedValue({ kind: "scanned", pairRef: "r", daemonId: "d", addrs: 1, relay: false });
    open({ name: "connect", state: "scan" });
    await waitFor(() => expect(screen.getByTestId("pair-code")).toHaveTextContent("482 913"));
    expect(screen.getByRole("heading", { name: "Enter this code" })).toBeInTheDocument();
    expect(screen.getByText(copy.connect.code.body)).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Waiting for your Mac…");
    expect(screen.getByText("On your Mac.")).toBeInTheDocument();
    expect(screen.getAllByRole("button")).toHaveLength(1);
    await fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(h.t.pairCancel).toHaveBeenCalledWith({ pairRef: "r" });
    await waitFor(() => expect(screen.getByRole("heading", { name: copy.connect.scan.title })).toBeInTheDocument());
  });

  it.each([
    ["refused", "refused", "Scan new code"],
    ["expired", "expired", "Scan new code"],
    ["failed", "failed", "Scan new code"],
    ["dropped", "dropped", "Reconnect"],
  ] as const)("renders the %s outcome with its copy and one button", async (outcome, key, label) => {
    h.scanOnce.mockResolvedValue({ kind: "scanned", pairRef: "r", daemonId: "d", addrs: 1, relay: false });
    open({ name: "connect", state: "scan" });
    await waitFor(() => expect(screen.getByTestId("pair-code")).toBeInTheDocument());
    h.t.listener?.({ pairRef: "r", outcome });
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: copy.connect[key].title })).toBeInTheDocument(),
    );
    expect(screen.getByText(copy.connect[key].body)).toBeInTheDocument();
    expect(screen.getAllByRole("button")).toHaveLength(label === "Reconnect" ? 2 : 1);
    expect(screen.getByRole("button", { name: label })).toBeInTheDocument();
  });

  it.each([
    ["refused", "shield-x"],
    ["dropped", "wifi-off"],
  ] as const)("draws a %s outcome with the %s glyph", async (outcome, glyph) => {
    h.scanOnce.mockResolvedValue({ kind: "scanned", pairRef: "r", daemonId: "d", addrs: 1, relay: false });
    const { container } = open({ name: "connect", state: "scan" });
    await waitFor(() => expect(screen.getByTestId("pair-code")).toBeInTheDocument());
    h.t.listener?.({ pairRef: "r", outcome });
    await waitFor(() => expect(container.querySelector(`[data-glyph="${glyph}"]`)).not.toBeNull());
  });

  it("says the phone was removed and offers to pair again", async () => {
    open({ name: "connect", state: "not-paired" });
    await settle();
    expect(screen.getByRole("heading", { name: "Phone removed" })).toBeInTheDocument();
    expect(h.scanOnce).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole("button", { name: "Pair again" }));
    expect(h.scanOnce).toHaveBeenCalledOnce();
  });

  it("has no button when there is no camera", async () => {
    h.cap.mockResolvedValue({ available: false, reason: "no_camera" });
    open({ name: "connect", state: "scan" });
    await waitFor(() => expect(screen.getByRole("heading", { name: "Can't scan" })).toBeInTheDocument());
    expect(screen.getByText("No camera on this device.")).toBeInTheDocument();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("sends a denied camera to Settings with one button, and scans on return", async () => {
    h.cap.mockResolvedValue({ available: false, reason: "denied" });
    open({ name: "connect", state: "scan" });
    await waitFor(() => expect(screen.getByRole("heading", { name: "Camera off" })).toBeInTheDocument());
    expect(screen.getAllByRole("button")).toHaveLength(1);
    await fireEvent.click(screen.getByRole("button", { name: "Open Settings" }));
    expect(h.t.openURL).toHaveBeenCalledWith({ url: "app-settings:" });

    h.cap.mockResolvedValue({ available: true });
    document.dispatchEvent(new Event("visibilitychange"));
    await waitFor(() => expect(h.scanOnce).toHaveBeenCalledOnce());
  });

  it("explains an unreadable code and keeps the scan", async () => {
    h.scanOnce.mockResolvedValue({ kind: "invalid", why: "not_lola" });
    open({ name: "connect", state: "scan" });
    await waitFor(() => expect(screen.getByRole("heading", { name: "Unknown code" })).toBeInTheDocument());
    expect(screen.getByText("Scan the code on your Mac.")).toBeInTheDocument();
  });

  it("keeps the scan when pairBegin rejects", async () => {
    h.scanOnce.mockResolvedValue({ kind: "scanned", pairRef: "r", daemonId: "d", addrs: 1, relay: false });
    h.t.pairBegin.mockRejectedValue({ code: "timeout" });
    open({ name: "connect", state: "scan" });
    await waitFor(() => expect(screen.getByText("Can't reach your Mac.")).toBeInTheDocument());
    expect(pairing.view).toBe("scan");
    expect(screen.getByRole("button", { name: "Scan again" })).toBeInTheDocument();
  });

  it("goes to the inbox when paired", async () => {
    h.scanOnce.mockResolvedValue({ kind: "scanned", pairRef: "r", daemonId: "d", addrs: 1, relay: false });
    open({ name: "connect", state: "scan" });
    await waitFor(() => expect(screen.getByTestId("pair-code")).toBeInTheDocument());
    h.t.listener?.({ pairRef: "r", outcome: "paired", daemonId: "d", name: "Mars" });
    await waitFor(() => expect(router.top).toEqual({ name: "tab", tab: "inbox" }));
  });

  it("cancels a waiting pairing when the screen unmounts", async () => {
    h.scanOnce.mockResolvedValue({ kind: "scanned", pairRef: "r", daemonId: "d", addrs: 1, relay: false });
    const { unmount } = open({ name: "connect", state: "scan" });
    await waitFor(() => expect(screen.getByTestId("pair-code")).toBeInTheDocument());
    unmount();
    expect(h.t.pairCancel).toHaveBeenCalledWith({ pairRef: "r" });
  });
});
