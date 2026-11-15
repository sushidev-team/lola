import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import AccessoryKey from "./AccessoryKey.svelte";
import { KEY_COMMIT_MS } from "@mobile/lib/keygesture";
import { REPEAT_DELAY_MS, REPEAT_INTERVAL_MS } from "@mobile/lib/keybytes";

const HOLD = KEY_COMMIT_MS + REPEAT_DELAY_MS + REPEAT_INTERVAL_MS * 3;

describe("AccessoryKey repeat", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  function hold(onfire: () => void) {
    const r = render(AccessoryKey, { props: { label: "↑", aria: "Up", repeats: true, onfire } });
    fireEvent.pointerDown(screen.getByRole("button", { name: "Up" }), { clientX: 5, clientY: 5 });
    vi.advanceTimersByTime(HOLD);
    return r;
  }

  it("repeats while held", () => {
    const onfire = vi.fn();
    hold(onfire);
    expect(onfire.mock.calls.length).toBeGreaterThan(2);
  });

  it("stops repeating when the key turns disabled mid-hold", async () => {
    const onfire = vi.fn();
    const r = hold(onfire);
    await r.rerender({ label: "↑", aria: "Up", repeats: true, onfire, disabled: true });
    const n = onfire.mock.calls.length;
    vi.advanceTimersByTime(REPEAT_INTERVAL_MS * 10);
    expect(onfire).toHaveBeenCalledTimes(n);
  });

  it("stops repeating when the bar unmounts mid-hold", () => {
    const onfire = vi.fn();
    const r = hold(onfire);
    r.unmount();
    const n = onfire.mock.calls.length;
    vi.advanceTimersByTime(REPEAT_INTERVAL_MS * 10);
    expect(onfire).toHaveBeenCalledTimes(n);
  });
});

describe("AccessoryKey box", () => {
  afterEach(cleanup);

  it("is a real 44pt touch box around an equal 44 x 40 keycap on the sel ground", () => {
    render(AccessoryKey, { props: { label: "esc", aria: "Escape", onfire: () => {} } });
    const button = screen.getByRole("button", { name: "Escape" });
    expect(button.className).toContain("h-11");
    expect(button.className).toContain("w-11");
    const cap = button.querySelector("span") as HTMLElement;
    expect(cap.className).toContain("h-10");
    expect(cap.className).toContain("w-11");
    expect(cap.className).toContain("rounded-lg");
    expect(cap.className).toContain("bg-sel");
    expect(cap.className).toContain("text-[15px]");
  });

  it("keeps the latched ground instead of the surface", () => {
    render(AccessoryKey, { props: { label: "ctrl", aria: "Control", latched: true, onfire: () => {} } });
    const cap = screen.getByRole("button", { name: "Control" }).querySelector("span") as HTMLElement;
    expect(cap.className).toContain("bg-accent-fill");
    expect(cap.className).not.toContain("bg-sel");
  });
});
