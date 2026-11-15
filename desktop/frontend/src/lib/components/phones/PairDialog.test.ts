import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";

const { Devices, Begin, Status, Confirm, Reject } = vi.hoisted(() => ({
  Devices: vi.fn(),
  Begin: vi.fn(),
  Status: vi.fn(),
  Confirm: vi.fn(),
  Reject: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  PhonesService: { Devices, Begin, Status, Confirm, Reject, Revoke: vi.fn(), SetTiers: vi.fn() },
}));

import PairDialog from "./PairDialog.svelte";
import { phones } from "$lib/phones.svelte";

/** A fake daemon: Status answers from the queue of states, then hangs like a long poll. */
function script(...states: Record<string, unknown>[]) {
  const queue = [...states];
  Status.mockImplementation((a: { pairing: string }) =>
    queue.length ? Promise.resolve({ pairing: a.pairing, ...queue.shift() }) : new Promise(() => {}),
  );
}
const begin = (id: string) => ({ pairing: id, payload: `lola2.${id}`, expiresAt: new Date(Date.now() + 120_000).toISOString() });
const confirming = (over: Record<string, unknown> = {}) => ({ state: "confirming", deviceName: "Pixel", ...over });

let onClose: () => void;
beforeEach(() => {
  onClose = vi.fn() as () => void;
  Devices.mockResolvedValue({ devices: [] });
  Begin.mockResolvedValue(begin("A"));
  Reject.mockResolvedValue({ pairing: "A", state: "cancelled" });
});
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.useRealTimers();
});

async function open() {
  render(PairDialog, { onClose });
  await waitFor(() => expect(Status).toHaveBeenCalled());
}
const typeCode = (v: string) => fireEvent.input(screen.getByLabelText("Type the code shown on the phone"), { target: { value: v } });

describe("PairDialog", () => {
  it("shows the QR while waiting", async () => {
    script();
    await open();
    expect(await screen.findByRole("img", { name: "Pairing code" })).toBeInTheDocument();
    expect(screen.getByText("Scan with Lola on your phone.")).toBeInTheDocument();
    expect(Status).toHaveBeenCalledWith({ pairing: "A", since: "waiting", waitMs: 25000 });
  });

  it("shows a problem and only Close", async () => {
    Begin.mockResolvedValue({ pairing: "", payload: "", problem: "Phone access is off." });
    render(PairDialog, { onClose });
    expect(await screen.findByText("Phone access is off.")).toBeInTheDocument();
    expect(Status).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalled();
    expect(Reject).not.toHaveBeenCalled();
  });

  it("renders the confirming state", async () => {
    script(confirming({ via: "relay", nameClash: true }));
    await open();
    expect(await screen.findByText("Pixel")).toBeInTheDocument();
    expect(screen.getByText("New device")).toBeInTheDocument();
    expect(screen.getByText("via relay")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "Read" })).toBeDisabled();
    expect(screen.getByRole("checkbox", { name: "Read" })).toBeChecked();
    expect(screen.getByRole("button", { name: "Confirm" })).toBeDisabled();
    await typeCode("123456");
    expect(screen.getByRole("button", { name: "Confirm" })).toBeEnabled();
  });

  it("names the phone it replaces", async () => {
    Devices.mockResolvedValue({ devices: [{ id: "old", name: "Old Pixel", tiers: ["read"], pairedAt: "", connected: 0, fingerprint: "" }] });
    await phones.refresh();
    script(confirming({ replaces: "old" }));
    await open();
    await waitFor(() => expect(screen.getByText("Replaces Old Pixel")).toBeInTheDocument());
    expect(screen.getByText("New device")).toBeInTheDocument();
  });

  it("confirms with the typed digits, the dialog's pairing and the chosen tiers", async () => {
    script(confirming());
    Confirm.mockResolvedValue({ pairing: "A", state: "paired", deviceName: "Pixel" });
    await open();
    await screen.findByText("Pixel");
    await typeCode("12a3456789");
    expect(screen.getByLabelText("Type the code shown on the phone")).toHaveValue("123456");
    await fireEvent.click(screen.getByRole("checkbox", { name: "Terminal input" }));
    await fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() =>
      expect(Confirm).toHaveBeenCalledWith({ pairing: "A", code: "123456", tiers: ["read", "answer", "control", "destructive"] }),
    );
    expect(await screen.findByText("Paired Pixel.")).toBeInTheDocument();
  });

  it("closes a second after pairing", async () => {
    script(confirming());
    Confirm.mockResolvedValue({ pairing: "A", state: "paired", deviceName: "Pixel" });
    await open();
    await screen.findByText("Pixel");
    vi.useFakeTimers({ toFake: ["setTimeout"] });
    await typeCode("123456");
    await fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await vi.waitFor(() => expect(screen.getByText("Paired Pixel.")).toBeInTheDocument());
    expect(onClose).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1000);
    expect(onClose).toHaveBeenCalled();
  });

  it("stays in confirming and shows the tries left when the code is wrong", async () => {
    script(confirming());
    Confirm.mockRejectedValue(new Error("the code does not match the phone; 2 tries left"));
    await open();
    await screen.findByText("Pixel");
    // The refusal re-reads the pairing for its tries.
    Status.mockImplementationOnce(() => Promise.resolve({ pairing: "A", ...confirming({ triesLeft: 2 }) }));
    await typeCode("000000");
    await fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    expect(await screen.findByText("the code does not match the phone; 2 tries left")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText("2 tries left")).toBeInTheDocument());
    expect(screen.getByLabelText("Type the code shown on the phone")).toBeInTheDocument();
  });

  it.each([
    ["rejected", {}, "Phone rejected."],
    ["cancelled", {}, "Pairing cancelled."],
    ["failed", { reason: "too_many_attempts" }, "Too many wrong codes."],
  ])("ends on %s with one line and Close", async (state, extra, line) => {
    script({ state, ...extra });
    await open();
    expect(await screen.findByText(line)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Close" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "New code" })).not.toBeInTheDocument();
  });

  it("offers a new code when the code expired", async () => {
    script({ state: "expired" });
    await open();
    expect(await screen.findByText("Code expired.")).toBeInTheDocument();
    Begin.mockResolvedValue(begin("B"));
    script();
    await fireEvent.click(screen.getByRole("button", { name: "New code" }));
    await waitFor(() => expect(Status).toHaveBeenLastCalledWith({ pairing: "B", since: "waiting", waitMs: 25000 }));
    expect(await screen.findByRole("img", { name: "Pairing code" })).toBeInTheDocument();
  });

  it("rejects its pairing when closed while confirming", async () => {
    script(confirming());
    await open();
    await screen.findByText("Pixel");
    await fireEvent.click(screen.getByRole("button", { name: "Reject" }));
    await waitFor(() => expect(Reject).toHaveBeenCalledWith("A"));
    expect(onClose).toHaveBeenCalled();
  });

  it("rejects its pairing when closed while waiting", async () => {
    script();
    await open();
    await screen.findByRole("img", { name: "Pairing code" });
    await fireEvent.click(screen.getByRole("button", { name: "close" }));
    await waitFor(() => expect(Reject).toHaveBeenCalledWith("A"));
  });

  it("never rejects a pairing that already ended", async () => {
    script({ state: "expired" });
    await open();
    await screen.findByText("Code expired.");
    await fireEvent.click(screen.getByRole("button", { name: "Close" }));
    cleanup();
    expect(Reject).not.toHaveBeenCalled();
  });

  it("confirms pairing A, never B, when a second Begin ran and A was cancelled", async () => {
    // Dialog A is confirming. Another Begin (a second window, a CLI) produces B.
    // The daemon cancels A. The human still types the code they read for A:
    // Confirm must carry A, which the daemon refuses, and never B.
    script(confirming());
    await open();
    await screen.findByText("Pixel");
    Begin.mockResolvedValueOnce(begin("B"));
    const { PhonesService } = await import("@bindings/desktop");
    expect((await PhonesService.Begin()).pairing).toBe("B");
    Confirm.mockImplementation((a: { pairing: string }) =>
      Promise.reject(new Error(a.pairing === "A" ? "pairing cancelled" : "wrong pairing")),
    );
    Status.mockImplementationOnce(() => Promise.resolve({ pairing: "A", state: "cancelled" }));
    await typeCode("123456");
    await fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(Confirm).toHaveBeenCalledTimes(1));
    expect(Confirm.mock.calls.every(([a]) => a.pairing === "A")).toBe(true);
    expect(await screen.findByText("Pairing cancelled.")).toBeInTheDocument();
  });
});
