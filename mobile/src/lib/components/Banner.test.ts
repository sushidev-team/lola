import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
import Banner, { connectionBanner } from "./Banner.svelte";
import type { Diagnosis, DiagnosisKind } from "@mobile/lib/diagnose";

const d = (kind: DiagnosisKind, detail = ""): Diagnosis => ({ kind, title: "", detail, retryable: false });

describe("connectionBanner", () => {
  it("offers pairing again for a changed key", () => {
    expect(connectionBanner(d("key_changed"), "Mars")).toMatchObject({
      id: "key_changed",
      tone: "bad",
      glyph: "shield-x",
      body: "Pair Mars again",
      intent: "pair",
    });
    expect(connectionBanner(d("key_changed"), "Mars")).not.toHaveProperty("action");
  });

  it("names the Mac it cannot reach and offers the details", () => {
    expect(connectionBanner(d("unreachable"), "Mars")).toMatchObject({
      glyph: "wifi-off",
      body: "Can't reach Mars.",
      action: "Details",
      intent: "details",
    });
  });

  it("says the short thing for racing and offline", () => {
    expect(connectionBanner(d("racing"), "Mars")?.body).toBe("Reconnecting…");
    expect(connectionBanner(d("racing"), "Mars")?.glyph).toBe("refresh-cw");
    expect(connectionBanner(d("offline"), "Mars")?.body).toBe("No network.");
  });

  it("passes a version or failure sentence through", () => {
    expect(connectionBanner(d("version", "Update this app."), "Mars")?.body).toBe("Update this app.");
  });

  it("says nothing when connected, idle, or not paired (Connect says it)", () => {
    for (const k of ["ok", "idle", "not_paired"] as const) expect(connectionBanner(d(k), "Mars")).toBeNull();
  });
});

describe("Banner", () => {
  it("shows its sentence as a status", () => {
    render(Banner, { props: { body: "No network." } });
    expect(screen.getByRole("status")).toHaveTextContent("No network.");
  });

  it("renders no button without an action", () => {
    render(Banner, { props: { body: "No network." } });
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("is one button whose label also reads the action", async () => {
    const onaction = vi.fn();
    render(Banner, { props: { body: "Can't reach Mars.", action: "Details", onaction } });
    await fireEvent.click(screen.getByRole("button", { name: /Can't reach Mars\..*Details/ }));
    expect(onaction).toHaveBeenCalledOnce();
  });

  it("is a 44pt row with a 10px radius, broken-soft for a changed key, with no separate action", () => {
    const { container } = render(Banner, { props: { tone: "bad", glyph: "shield-x", body: "Pair Mars again", onaction: () => {} } });
    const btn = container.querySelector("button")!;
    expect(btn.className).toContain("h-11");
    expect(btn.className).toContain("rounded-[10px]");
    expect(btn.className).toContain("bg-pill-broken-soft");
    expect(btn.querySelector(".font-bold")).toBeNull();
    expect(btn).toHaveTextContent("Pair Mars again");
  });

  it("is edge-soft and keeps its action for assistive technology only", () => {
    const { container } = render(Banner, { props: { body: "Can't reach Mars.", action: "Details", onaction: () => {} } });
    const btn = container.querySelector("button")!;
    expect(btn.className).toContain("bg-edge-soft");
    expect(btn.querySelector(".sr-only")?.textContent).toBe("Details");
  });

  it("pays the status-bar inset only when asked", () => {
    const { container, rerender } = render(Banner, { props: { body: "x" } });
    expect(container.firstElementChild?.className).not.toContain("safe-area-inset-top");
    void rerender({ body: "x", inset: true });
  });
});
