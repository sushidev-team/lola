import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";

const { list, diff } = vi.hoisted(() => ({ list: vi.fn(), diff: vi.fn() }));
vi.mock("@bindings/desktop", () => ({
  DaemonService: {
    Checkpoints: (...a: unknown[]) => list(...a),
    CheckpointDiff: (...a: unknown[]) => diff(...a),
  },
}));

import Checkpoints from "./checkpoints.svelte";
import { router } from "@mobile/lib/router.svelte";
import { tiers } from "@mobile/lib/tiers.svelte";

const cp = (seq: number) => ({ seq, label: `turn ${seq}` });

describe("checkpoints sheet", () => {
  let n = 0;
  beforeEach(() => {
    n++;
    tiers.set(["read", "control"]);
    router.stack = [{ name: "tab", tab: "inbox" }, { name: "session", id: `cp${n}`, seg: "conversation" }];
    router.sheet = "checkpoints";
    list.mockReset();
    diff.mockReset().mockResolvedValue({ files: [{ path: "a.go", additions: 1, deletions: 0 }] });
  });
  afterEach(() => cleanup());

  it("lists checkpoints newest first and previews the selected one", async () => {
    list.mockResolvedValue({ checkpoints: [cp(1), cp(2)], restorable: true });
    render(Checkpoints);
    const rows = await screen.findAllByRole("button", { name: /^#\d · turn/ });
    expect(rows.map((r) => r.textContent?.trim())).toEqual(["#2 · turn 2", "#1 · turn 1"]);
    expect(await screen.findByText("1 file")).toBeTruthy();
    expect(screen.getByText("+1 −0")).toBeTruthy();
    const checked = () => rows.filter((r) => r.querySelector("[data-row-check]")).map((r) => r.textContent?.trim());
    expect(checked()).toEqual(["#2 · turn 2"]);
    await fireEvent.click(rows[1]);
    expect(checked()).toEqual(["#1 · turn 1"]);
  });

  it("shows why the list could not be read instead of an empty list", async () => {
    list.mockRejectedValue(new Error("unknown session cp"));
    render(Checkpoints);
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("unknown session cp"));
    expect(screen.getByRole("button", { name: "Restore" })).toBeDisabled();
  });

  it("pins Restore and a quiet 'New session from here' in the footer", async () => {
    tiers.set(["read", "control", "destructive"]);
    list.mockResolvedValue({ checkpoints: [cp(1)], restorable: true });
    render(Checkpoints);
    await screen.findAllByRole("button", { name: /^#1 · turn/ });
    const restore = screen.getByRole("button", { name: "Restore" });
    expect(restore.closest("[data-sheet-footer]")).not.toBeNull();
    expect(screen.getByRole("button", { name: "New session from here" }).closest("[data-sheet-footer]")).not.toBeNull();
  });

  it("windows a long list", async () => {
    list.mockResolvedValue({ checkpoints: Array.from({ length: 300 }, (_, i) => cp(i + 1)), restorable: true });
    const { container } = render(Checkpoints);
    await waitFor(() => expect(container.querySelector("[data-virtual='on']")).not.toBeNull());
    expect(container.querySelectorAll("[role='listitem']").length).toBeLessThan(100);
  });
});
