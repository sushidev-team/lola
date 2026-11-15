import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
import { tick } from "svelte";
import { confirm } from "$lib/confirm.svelte";
import SheetHarness from "./SheetHarness.test.svelte";

// jsdom has no PointerEvent; a MouseEvent carries the coordinates the sheet reads.
if (!("PointerEvent" in window)) {
  class PointerEventShim extends MouseEvent {
    pointerId: number;
    constructor(type: string, init: MouseEventInit & { pointerId?: number } = {}) {
      super(type, init);
      this.pointerId = init.pointerId ?? 0;
    }
  }
  Object.defineProperty(window, "PointerEvent", { configurable: true, value: PointerEventShim });
}

describe("Sheet", () => {
  it("is a named modal dialog", () => {
    render(SheetHarness, { props: { onclose: () => {} } });
    const dlg = screen.getByRole("dialog", { name: "Test sheet" });
    expect(dlg).toHaveAttribute("aria-modal", "true");
    expect(screen.getByText("Body")).toBeInTheDocument();
  });

  it("takes focus when it opens and gives it back when it closes", async () => {
    const opener = document.createElement("button");
    document.body.append(opener);
    opener.focus();
    const { unmount } = render(SheetHarness, { props: { onclose: () => {} } });
    await tick();
    expect(document.activeElement).toBe(screen.getByRole("dialog"));
    unmount();
    expect(document.activeElement).toBe(opener);
    opener.remove();
  });

  it("closes from its backdrop button", async () => {
    const onclose = vi.fn();
    render(SheetHarness, { props: { onclose } });
    await fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onclose).toHaveBeenCalledOnce();
  });

  it("closes on Escape", async () => {
    const onclose = vi.fn();
    render(SheetHarness, { props: { onclose } });
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(onclose).toHaveBeenCalledOnce();
  });

  it("defaults to the fit detent: auto height under a safe-area max-height", () => {
    render(SheetHarness, { props: { onclose: () => {} } });
    const dlg = screen.getByRole("dialog");
    expect(dlg).toHaveAttribute("data-detent", "fit");
    expect(dlg.style.height).toBe("auto");
    expect(dlg.style.maxHeight).toContain("safe-area-inset-top");
    expect(dlg.style.maxHeight).toContain("44px");
  });

  it("renders a titled 56pt header with a Done that closes", async () => {
    const onclose = vi.fn();
    render(SheetHarness, { props: { onclose, title: "Rename", label: "" } });
    const dlg = screen.getByRole("dialog", { name: "Rename" });
    expect(dlg.querySelector("[data-sheet-header]")).toHaveClass("h-14");
    expect(screen.getByRole("heading", { name: "Rename" })).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(onclose).toHaveBeenCalledOnce();
  });

  it("hides Done with done=false and names a Cancel that closes", async () => {
    const onclose = vi.fn();
    render(SheetHarness, { props: { onclose, title: "T", done: false, cancel: "Cancel" } });
    expect(screen.queryByRole("button", { name: "Done" })).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onclose).toHaveBeenCalledOnce();
  });

  it("an action replaces Done, runs, and dims when disabled", async () => {
    const run = vi.fn();
    const a = render(SheetHarness, {
      props: { onclose: () => {}, title: "T", action: { label: "Save", onclick: run } },
    });
    expect(screen.queryByRole("button", { name: "Done" })).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(run).toHaveBeenCalledOnce();
    a.unmount();
    render(SheetHarness, {
      props: { onclose: () => {}, title: "T", action: { label: "Save", onclick: run, disabled: true } },
    });
    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    expect(save.className).toContain("disabled:opacity-40");
  });

  it("is lifted by --kb: pinned to the bottom edge and capped under the lift", () => {
    document.documentElement.style.setProperty("--kb", "336px");
    try {
      render(SheetHarness, { props: { onclose: () => {} } });
      const dlg = screen.getByRole("dialog");
      expect(getComputedStyle(document.documentElement).getPropertyValue("--kb")).toBe("336px");
      expect(dlg.style.bottom).toBe("var(--kb, 0px)");
      expect(dlg.style.maxHeight).toContain("var(--kb, 0px)");
      expect(dlg.className).toContain("absolute");
    } finally {
      document.documentElement.style.removeProperty("--kb");
    }
  });

  it("pads its content past the home indicator", () => {
    render(SheetHarness, { props: { onclose: () => {} } });
    const content = screen.getByRole("dialog").querySelector("[data-sheet-content]") as HTMLElement;
    expect(content.style.paddingBottom).toContain("safe-area-inset-bottom");
    expect(content.style.paddingBottom).toContain("16px");
  });

  it("takes the detent as a height", () => {
    const { rerender } = render(SheetHarness, { props: { onclose: () => {}, detent: "medium" } });
    expect(screen.getByRole("dialog").style.height).toBe("52%");
    void rerender({ onclose: () => {}, detent: "large" });
  });

  it("uses 92% for large and the safe-area calc for full", () => {
    const a = render(SheetHarness, { props: { onclose: () => {}, detent: "large" } });
    expect(screen.getByRole("dialog").style.height).toBe("92%");
    a.unmount();
    render(SheetHarness, { props: { onclose: () => {}, detent: "full" } });
    expect(screen.getByRole("dialog").style.height).toContain("safe-area-inset-top");
  });

  it("dismisses after a drag past 30% of its height", async () => {
    const onclose = vi.fn();
    render(SheetHarness, { props: { onclose } });
    const dlg = screen.getByRole("dialog");
    Object.defineProperty(dlg, "offsetHeight", { configurable: true, value: 400 });
    const handle = dlg.querySelector("[data-sheet-handle]")!;
    await fireEvent.pointerDown(handle, { clientY: 100, pointerId: 1 });
    await fireEvent.pointerMove(handle, { clientY: 260, pointerId: 1 });
    await fireEvent.pointerUp(handle, { clientY: 260, pointerId: 1 });
    await vi.waitFor(() => expect(onclose).toHaveBeenCalledOnce(), { timeout: 3000 });
  });

  it("stays when released before 30% and slowly", async () => {
    const onclose = vi.fn();
    render(SheetHarness, { props: { onclose } });
    const dlg = screen.getByRole("dialog");
    Object.defineProperty(dlg, "offsetHeight", { configurable: true, value: 400 });
    const handle = dlg.querySelector("[data-sheet-handle]")!;
    await fireEvent.pointerDown(handle, { clientY: 100, pointerId: 1 });
    await fireEvent.pointerMove(handle, { clientY: 140, pointerId: 1 });
    await fireEvent.pointerUp(handle, { clientY: 140, pointerId: 1 });
    await new Promise((r) => setTimeout(r, 100));
    expect(onclose).not.toHaveBeenCalled();
  });

  it("draws a leading Cancel with an action when none is named", async () => {
    const onclose = vi.fn();
    render(SheetHarness, { props: { onclose, title: "T", action: { label: "Save", onclick: () => {} } } });
    await fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onclose).toHaveBeenCalledOnce();
  });

  describe("dirty", () => {
    it("asks to discard on backdrop and Escape instead of closing", async () => {
      confirm.cancel();
      const onclose = vi.fn();
      render(SheetHarness, { props: { onclose, title: "T", dirty: true, action: { label: "Save", onclick: () => {} } } });
      await fireEvent.click(screen.getByRole("button", { name: "Close" }));
      expect(onclose).not.toHaveBeenCalled();
      expect(confirm.request?.title).toBe("Discard changes?");
      confirm.cancel();
      await fireEvent.keyDown(window, { key: "Escape" });
      expect(confirm.request).not.toBeNull();
      expect(onclose).not.toHaveBeenCalled();
      await fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
      expect(onclose).not.toHaveBeenCalled();
      confirm.accept();
      expect(onclose).toHaveBeenCalledOnce();
    });

    it("springs a far drag back and asks instead of closing", async () => {
      confirm.cancel();
      const onclose = vi.fn();
      render(SheetHarness, { props: { onclose, dirty: true } });
      const dlg = screen.getByRole("dialog");
      Object.defineProperty(dlg, "offsetHeight", { configurable: true, value: 400 });
      const handle = dlg.querySelector("[data-sheet-handle]")!;
      await fireEvent.pointerDown(handle, { clientY: 100, pointerId: 1 });
      await fireEvent.pointerMove(handle, { clientY: 260, pointerId: 1 });
      await fireEvent.pointerUp(handle, { clientY: 260, pointerId: 1 });
      expect(confirm.request).not.toBeNull();
      await new Promise((r) => setTimeout(r, 100));
      expect(onclose).not.toHaveBeenCalled();
      confirm.cancel();
    });
  });

  it("renders a footer outside the scroller", () => {
    render(SheetHarness, { props: { onclose: () => {}, withFooter: true } });
    const foot = document.querySelector("[data-sheet-footer]")!;
    const scroller = document.querySelector("[data-sheet-content]") as HTMLElement;
    expect(foot).toHaveTextContent("Pick");
    expect(scroller.contains(foot)).toBe(false);
    expect(foot.previousElementSibling).toBe(scroller);
    expect(scroller.style.paddingBottom).toBe("8px");
  });
});
