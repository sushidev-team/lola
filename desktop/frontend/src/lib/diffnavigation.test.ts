import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { observeDiffNavigation } from "./diffnavigation";

describe("diff scroll navigation", () => {
  let frame: FrameRequestCallback | undefined;
  let resize: () => void;
  const disconnect = vi.fn();
  let navigation: ReturnType<typeof observeDiffNavigation>;
  let root: HTMLElement;
  let positions: number[];
  let height: number;
  const active = vi.fn();

  function flush() {
    const callback = frame;
    frame = undefined;
    callback?.(0);
  }

  function scroll(top: number) {
    root.scrollTop = top;
    root.dispatchEvent(new Event("scroll"));
    flush();
  }

  beforeEach(() => {
    active.mockClear();
    disconnect.mockClear();
    frame = undefined;
    vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => { frame = callback; return 1; });
    vi.stubGlobal("cancelAnimationFrame", () => { frame = undefined; });
    vi.stubGlobal("ResizeObserver", class {
      constructor(callback: () => void) { resize = callback; }
      observe() {}
      disconnect = disconnect;
    });
    root = document.createElement("div");
    root.innerHTML = '<section data-file="a.go"></section><section data-file="b.go"></section><section data-file="c.go"></section>';
    document.body.append(root);
    positions = [0, 1000, 1200];
    height = 1400;
    Object.defineProperties(root, {
      clientHeight: { value: 300 },
      scrollHeight: { get: () => height },
    });
    root.getBoundingClientRect = () => ({ top: 50 }) as DOMRect;
    Array.from(root.children).forEach((element, index) => {
      element.getBoundingClientRect = () => ({ top: 50 + positions[index] - root.scrollTop }) as DOMRect;
    });
    root.scrollTo = vi.fn((options?: ScrollToOptions | number, y?: number) => {
      root.scrollTop = typeof options === "number" ? y ?? 0 : options?.top ?? 0;
    });
    navigation = observeDiffNavigation(root, active);
    flush();
  });

  afterEach(() => {
    navigation.destroy();
    root.remove();
    vi.unstubAllGlobals();
  });

  it("keeps a long file active until the next header reaches the top, in either direction", () => {
    expect(active).toHaveBeenLastCalledWith("a.go");
    scroll(950); // b.go is visible, but a.go still owns the sticky header.
    expect(active).toHaveBeenCalledTimes(1);
    scroll(1000);
    expect(active).toHaveBeenLastCalledWith("b.go");
    scroll(500);
    expect(active).toHaveBeenLastCalledWith("a.go");
    scroll(-40); // WebKit's elastic overscroll
    expect(active).toHaveBeenLastCalledWith("a.go");
  });

  it("selects the short final file at the bottom even when its header cannot reach the top", () => {
    scroll(1100);
    expect(active).toHaveBeenLastCalledWith("c.go");
    scroll(1050);
    expect(active).toHaveBeenLastCalledWith("b.go");
  });

  it("jumps only the diff container and keeps the clicked target selected at a clamped destination", () => {
    positions = [0, 1150, 1250];
    navigation.jump("b.go");
    expect(root.scrollTo).toHaveBeenCalledWith({ top: 1100, behavior: "instant" });
    expect(active).toHaveBeenLastCalledWith("b.go");
    scroll(1100); // Scroll event produced by the programmatic jump.
    expect(active).toHaveBeenLastCalledWith("b.go");
    scroll(1090); // User takes over again.
    expect(active).toHaveBeenLastCalledWith("a.go");
    scroll(1100);
    expect(active).toHaveBeenLastCalledWith("c.go");
  });

  it("remeasures after a comment, split mode or resize moves later sections", () => {
    scroll(1000);
    expect(active).toHaveBeenLastCalledWith("b.go");
    positions = [0, 1500, 1800];
    height = 2000;
    resize();
    flush();
    expect(active).toHaveBeenLastCalledWith("a.go");
    scroll(1500);
    expect(active).toHaveBeenLastCalledWith("b.go");
  });

  it("keeps the first file selected when everything fits without scrolling", () => {
    positions = [0, 80, 160];
    height = 240;
    resize();
    flush();
    expect(active).toHaveBeenLastCalledWith("a.go");
  });

  it("coalesces scroll events and disconnects observers and pending work on teardown", () => {
    root.scrollTop = 1000;
    root.dispatchEvent(new Event("scroll"));
    root.dispatchEvent(new Event("scroll"));
    expect(active).toHaveBeenCalledTimes(1);
    flush();
    expect(active).toHaveBeenCalledTimes(2);
    root.dispatchEvent(new Event("scroll"));
    navigation.destroy();
    expect(disconnect).toHaveBeenCalled();
    scroll(0);
    expect(active).toHaveBeenCalledTimes(2);
    expect(frame).toBeUndefined();
  });
});
