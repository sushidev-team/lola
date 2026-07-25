import { describe, it, expect, vi, beforeEach } from "vitest";
import { confirm } from "./confirm.svelte";

// confirm is a singleton; clear it so test order can't matter.
beforeEach(() => confirm.cancel());

describe("confirm", () => {
  it("holds a request until it is answered", () => {
    expect(confirm.request).toBeNull();
    confirm.ask({ title: "t", body: "b", confirmLabel: "Go", onConfirm: () => {} });
    expect(confirm.request?.title).toBe("t");
  });

  it("cancel drops the request WITHOUT running the action", () => {
    const onConfirm = vi.fn();
    confirm.ask({ title: "t", body: "b", confirmLabel: "Go", onConfirm });
    confirm.cancel();
    expect(confirm.request).toBeNull();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("accept runs the action and closes", () => {
    const onConfirm = vi.fn();
    confirm.ask({ title: "t", body: "b", confirmLabel: "Go", onConfirm });
    confirm.accept();
    expect(onConfirm).toHaveBeenCalledOnce();
    expect(confirm.request).toBeNull();
  });

  // The dialog is cleared BEFORE the action runs, so an action that opens its
  // own confirmation (or navigates) isn't immediately wiped by the accept.
  it("clears the request before invoking the action", () => {
    let seen: unknown = "unset";
    confirm.ask({
      title: "t",
      body: "b",
      confirmLabel: "Go",
      onConfirm: () => (seen = confirm.request),
    });
    confirm.accept();
    expect(seen).toBeNull();
  });

  it("a second ask replaces the pending request", () => {
    const first = vi.fn();
    confirm.ask({ title: "first", body: "b", confirmLabel: "Go", onConfirm: first });
    confirm.ask({ title: "second", body: "b", confirmLabel: "Go", onConfirm: () => {} });
    expect(confirm.request?.title).toBe("second");
    confirm.accept();
    expect(first).not.toHaveBeenCalled();
  });
});
