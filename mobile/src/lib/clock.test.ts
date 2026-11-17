import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { render, cleanup } from "@testing-library/svelte";
import { flushSync } from "svelte";
import ClockHarness from "./ClockHarness.test.svelte";
import { CLOCK_TICK_MS } from "./clock.svelte";

function setHidden(h: boolean): void {
  Object.defineProperty(document, "hidden", { configurable: true, get: () => h });
  document.dispatchEvent(new Event("visibilitychange"));
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date(2026, 0, 1, 12, 0, 10));
});
afterEach(() => {
  cleanup();
  setHidden(false);
  vi.useRealTimers();
});

describe("minuteClock", () => {
  it("ticks on the next 30 s boundary, then every 30 s", () => {
    const read = vi.fn();
    render(ClockHarness, { onread: read });
    flushSync();
    expect(read).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(CLOCK_TICK_MS - 10_001);
    flushSync();
    expect(read).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(1);
    flushSync();
    expect(read).toHaveBeenCalledTimes(2);
    expect(read.mock.calls[1][0] % CLOCK_TICK_MS).toBe(0);
    vi.advanceTimersByTime(CLOCK_TICK_MS);
    flushSync();
    expect(read).toHaveBeenCalledTimes(3);
  });

  it("stops while hidden and catches up on return", () => {
    const read = vi.fn();
    render(ClockHarness, { onread: read });
    flushSync();
    setHidden(true);
    vi.advanceTimersByTime(CLOCK_TICK_MS * 4);
    flushSync();
    expect(read).toHaveBeenCalledTimes(1);
    setHidden(false);
    flushSync();
    expect(read).toHaveBeenCalledTimes(2);
  });

  it("stops when nobody reads it", async () => {
    const read = vi.fn();
    const { unmount } = render(ClockHarness, { onread: read });
    flushSync();
    unmount();
    await Promise.resolve();
    vi.advanceTimersByTime(CLOCK_TICK_MS * 3);
    flushSync();
    expect(read).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });
});
