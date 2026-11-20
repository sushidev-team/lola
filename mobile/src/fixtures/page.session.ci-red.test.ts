import { beforeAll, describe, expect, it, vi } from "vitest";
import { fireEvent, waitFor } from "@testing-library/svelte";
import { openPreview, PAGE_TIMEOUT } from "./page.helpers";

describe("preview page: session, ci-red", () => {
  let app: HTMLElement;
  beforeAll(async () => {
    app = await openPreview("?fixture=ci-red&route=/session/s6/review");
    await waitFor(() => expect(app.querySelector("[data-ci-failed]")).toHaveTextContent("2 checks failed"), { timeout: PAGE_TIMEOUT });
  }, PAGE_TIMEOUT * 2);

  it("makes See checks primary with the failing count and a Merge anyway… text link, no 'Review PR'", () => {
    const bar = app.querySelector("[data-review-bar]")!;
    const labels = Array.from(bar.querySelectorAll("button")).map((b) => b.textContent?.trim());
    expect(labels).toContain("See checks");
    expect(labels).toContain("Merge anyway…");
    expect(labels).not.toContain("Merge");
    expect(bar.querySelector("[data-ci-failed]")?.textContent).toBe("2 checks failed");
    const all = Array.from(app.querySelector("[data-review-segment]")!.querySelectorAll("button"));
    expect(all.some((b) => /^\s*Review PR\s*$/.test(b.textContent ?? ""))).toBe(false);
  }, PAGE_TIMEOUT * 2);

  it("leaves a vertical drag at the screen edge with the reader and abandons a cancelled back swipe", async () => {
    const reader = app.querySelector<HTMLElement>(".review-scroll")!;
    const layer = app.querySelector("[data-session-screen]")!.parentElement!;
    const capture = vi.fn();
    layer.setPointerCapture = capture;
    const { router } = await import("@mobile/lib/router.svelte");
    const back = vi.spyOn(router, "back");
    const pointer = (type: string, x: number, y: number) => fireEvent(reader, Object.assign(
      new MouseEvent(type, { bubbles: true, clientX: x, clientY: y }),
      { pointerId: 1, isPrimary: true },
    ));
    try {
      await pointer("pointerdown", 8, 100);
      await pointer("pointermove", 12, 128);
      await pointer("pointerup", 32, 160);
      expect(capture).not.toHaveBeenCalled();
      await pointer("pointerdown", 8, 100);
      await pointer("pointermove", 80, 102);
      expect(capture).toHaveBeenCalledWith(1);
      await pointer("pointercancel", 80, 102);
      await pointer("pointerup", 400, 102);
      expect(back).not.toHaveBeenCalled();
    } finally {
      back.mockRestore();
    }
  });
});
