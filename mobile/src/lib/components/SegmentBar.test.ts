import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
import SegmentBar from "./SegmentBar.svelte";
import { largeType } from "@mobile/lib/inbox.svelte";
import { haptic } from "@mobile/lib/haptics";

vi.mock("@mobile/lib/haptics", () => ({ haptic: vi.fn() }));

const segments = [
  { id: "conversation", label: "Conversation" },
  { id: "changes", label: "Changes" },
  { id: "terminal", label: "Terminal" },
] as const;

describe("SegmentBar", () => {
  it("selects the current segment and no other", () => {
    render(SegmentBar, { props: { segments, value: "changes", onchange: () => {} } });
    const tabs = screen.getAllByRole("tab");
    expect(tabs.map((t) => t.getAttribute("aria-selected"))).toEqual(["false", "true", "false"]);
    expect(tabs.map((t) => t.getAttribute("tabindex"))).toEqual(["-1", "0", "-1"]);
  });

  it("draws only the segments it is given", () => {
    render(SegmentBar, { props: { segments: segments.slice(0, 2), value: "conversation", onchange: () => {} } });
    expect(screen.queryByRole("tab", { name: "Terminal" })).toBeNull();
  });

  it("reports a press", async () => {
    const onchange = vi.fn();
    render(SegmentBar, { props: { segments, value: "conversation", onchange } });
    await fireEvent.click(screen.getByRole("tab", { name: "Terminal" }));
    expect(onchange).toHaveBeenCalledWith("terminal");
  });

  it("moves with the arrow keys and wraps", async () => {
    const onchange = vi.fn();
    render(SegmentBar, { props: { segments, value: "terminal", onchange } });
    await fireEvent.keyDown(screen.getByRole("tab", { name: "Terminal" }), { key: "ArrowRight" });
    expect(onchange).toHaveBeenLastCalledWith("conversation");
    await fireEvent.keyDown(screen.getByRole("tab", { name: "Terminal" }), { key: "ArrowLeft" });
    expect(onchange).toHaveBeenLastCalledWith("changes");
  });

  it("plays the selection haptic on a change and not on the current segment", async () => {
    vi.mocked(haptic).mockClear();
    render(SegmentBar, { props: { segments, value: "conversation", onchange: () => {} } });
    await fireEvent.click(screen.getByRole("tab", { name: "Conversation" }));
    expect(haptic).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole("tab", { name: "Changes" }));
    expect(haptic).toHaveBeenCalledWith("select");
  });

  it("scrolls sideways with content-sized segments under large type", async () => {
    largeType.value = true;
    try {
      render(SegmentBar, { props: { segments, value: "terminal", onchange: () => {} } });
      const track = screen.getByRole("tablist");
      expect(track).toHaveAttribute("data-scrolling");
      for (const tab of screen.getAllByRole("tab")) expect(tab.className).toContain("flex-none");
    } finally {
      largeType.value = false;
    }
  });

  it("stays a fitted track when nothing overflows", () => {
    render(SegmentBar, { props: { segments, value: "changes", onchange: () => {} } });
    expect(screen.getByRole("tablist")).not.toHaveAttribute("data-scrolling");
    for (const tab of screen.getAllByRole("tab")) expect(tab.className).toContain("flex-auto");
  });

  it("reveals the selected tab by moving only the strip", async () => {
    const left = vi.spyOn(HTMLElement.prototype, "offsetLeft", "get").mockImplementation(function (this: HTMLElement) {
      return this.textContent === "Terminal" ? 260 : 0;
    });
    const width = vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockReturnValue(80);
    const viewport = vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(240);
    try {
      const { rerender } = render(SegmentBar, { props: { segments, value: "terminal", onchange: () => {} } });
      const strip = screen.getByRole("tablist");
      await vi.waitFor(() => expect(strip.scrollLeft).toBe(100));
      await rerender({ segments, value: "conversation", onchange: () => {} });
      await vi.waitFor(() => expect(strip.scrollLeft).toBe(0));
    } finally {
      left.mockRestore();
      width.mockRestore();
      viewport.mockRestore();
    }
  });
});

describe("SegmentBar fade", () => {
  it("masks the track when it overflows under large type", async () => {
    largeType.value = true;
    const sw = vi.spyOn(HTMLElement.prototype, "scrollWidth", "get").mockReturnValue(400);
    const cw = vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(300);
    try {
      render(SegmentBar, { props: { segments, value: "conversation", onchange: () => {} } });
      const track = screen.getByRole("tablist");
      await new Promise((r) => setTimeout(r, 0));
      expect(track.style.getPropertyValue("mask-image") || track.style.getPropertyValue("-webkit-mask-image")).toContain("linear-gradient");
    } finally {
      sw.mockRestore();
      cw.mockRestore();
      largeType.value = false;
    }
  });
});
