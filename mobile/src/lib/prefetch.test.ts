import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PREFETCH_DELAY_MS, prefetchOnce, resetPrefetch } from "./prefetch";

describe("prefetchOnce", () => {
  let visibility: DocumentVisibilityState = "visible";
  beforeEach(() => {
    vi.useFakeTimers();
    resetPrefetch();
    visibility = "visible";
    vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("fetches every chunk once, after the delay", () => {
    const a = vi.fn(async () => {});
    const b = vi.fn(async () => {});
    prefetchOnce([a, b]);
    vi.advanceTimersByTime(PREFETCH_DELAY_MS - 1);
    expect(a).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(a).toHaveBeenCalledOnce();
    expect(b).toHaveBeenCalledOnce();
    prefetchOnce([a]);
    vi.advanceTimersByTime(PREFETCH_DELAY_MS * 2);
    expect(a).toHaveBeenCalledOnce();
  });

  it("waits for the foreground", () => {
    const a = vi.fn(async () => {});
    visibility = "hidden";
    prefetchOnce([a]);
    vi.advanceTimersByTime(PREFETCH_DELAY_MS * 3);
    expect(a).not.toHaveBeenCalled();
    visibility = "visible";
    document.dispatchEvent(new Event("visibilitychange"));
    expect(a).toHaveBeenCalledOnce();
  });

  it("fetches nothing when cancelled first", () => {
    const a = vi.fn(async () => {});
    const cancel = prefetchOnce([a]);
    cancel();
    vi.advanceTimersByTime(PREFETCH_DELAY_MS * 2);
    expect(a).not.toHaveBeenCalled();
  });
});
