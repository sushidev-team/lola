import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { tick } from "svelte";
import TimeAgo from "./TimeAgo.svelte";
import { exactTime } from "$lib/time";

const now = new Date("2026-10-11T12:00:00Z");
afterEach(() => { cleanup(); vi.useRealTimers(); });

describe("TimeAgo", () => {
  it("updates a session age between polls and exposes its exact origin on focus", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(now);
    const at = new Date(+now - 59 * 60_000).toISOString();
    render(TimeAgo, { at, ago: false, hint: "First observed", fallback: "59m" });
    expect(screen.getByText("59m")).toBeTruthy();
    await vi.advanceTimersByTimeAsync(60_100);
    await tick();
    const label = screen.getByText("1h");
    await fireEvent.focusIn(label);
    await vi.advanceTimersByTimeAsync(300);
    await tick();
    expect(screen.getByText("First observed")).toBeTruthy();
    expect(screen.getByText(exactTime(at)).getAttribute("datetime")).toBe(at);
  });

  it("prefers the raw timestamp and falls back only when it is unknown", () => {
    vi.useFakeTimers();
    vi.setSystemTime(now);
    const at = new Date(+now - (5 * 60 + 4) * 60_000).toISOString();
    render(TimeAgo, { at, ago: false, fallback: "5h04m" });
    expect(screen.getByText("5h")).toBeTruthy();
    expect(screen.queryByText("5h04m")).toBeNull();
    cleanup();
    render(TimeAgo, { at: undefined, ago: false, fallback: "5h04m" });
    expect(screen.getByText("5h04m")).toBeTruthy();
    expect(screen.queryByRole("tooltip")).toBeNull();
  });
});
