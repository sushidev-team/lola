import { afterEach, describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/svelte";
import { tick } from "svelte";
import VirtualListHarness from "./VirtualListHarness.test.svelte";
import { ESTIMATE, indexAt, OVERSCAN, VIRTUALIZE_AT } from "./VirtualList.svelte";

// A windowed list mounts the rows near the viewport and nothing else; a short
// list is a plain keyed list.

const rows = (n: number, from = 0) =>
  Array.from({ length: n }, (_, i) => ({ id: `r${from + i}` }));

const VIEWPORT = 560;
const visibleRows = () => document.querySelectorAll("[data-row]").length;

afterEach(() => {
  delete (HTMLElement.prototype as { clientHeight?: number }).clientHeight;
});

function fixViewport(h = VIEWPORT): void {
  Object.defineProperty(HTMLElement.prototype, "clientHeight", { configurable: true, get: () => h });
}

describe("indexAt", () => {
  it("finds the row under an offset", () => {
    const offsets = [0, 56, 112, 168];
    expect(indexAt(offsets, 0)).toBe(0);
    expect(indexAt(offsets, 56)).toBe(1);
    expect(indexAt(offsets, 167)).toBe(2);
  });
});

describe("VirtualList", () => {
  it("renders every row below the threshold", () => {
    render(VirtualListHarness, { props: { items: rows(VIRTUALIZE_AT - 1) } });
    expect(visibleRows()).toBe(VIRTUALIZE_AT - 1);
    expect(screen.getByRole("list")).toHaveAttribute("data-virtual", "off");
  });

  it("mounts at most the visible rows plus the overscan on each side for 300 rows", async () => {
    fixViewport();
    render(VirtualListHarness, { props: { items: rows(300) } });
    await tick();
    const visible = Math.ceil(VIEWPORT / ESTIMATE.row);
    expect(screen.getByRole("list")).toHaveAttribute("data-virtual", "on");
    expect(visibleRows()).toBeLessThanOrEqual(visible + 2 * OVERSCAN + 1);
    expect(visibleRows()).toBeGreaterThanOrEqual(visible);
    expect(screen.getByText("r0")).toBeInTheDocument();
    expect(screen.queryByText("r299")).toBeNull();
  });

  it("moves the window with the scroll offset", async () => {
    fixViewport();
    render(VirtualListHarness, { props: { items: rows(300) } });
    const list = screen.getByRole("list");
    list.scrollTop = 100 * ESTIMATE.row;
    list.dispatchEvent(new Event("scroll"));
    await tick();
    expect(screen.getByText("r100")).toBeInTheDocument();
    expect(screen.queryByText("r0")).toBeNull();
    expect(visibleRows()).toBeLessThanOrEqual(Math.ceil(VIEWPORT / ESTIMATE.row) + 2 * OVERSCAN + 1);
  });

  it("keeps the reader's row in place when rows arrive above it", async () => {
    fixViewport();
    const { rerender } = render(VirtualListHarness, { props: { items: rows(300, 3) } });
    const list = screen.getByRole("list");
    list.scrollTop = 100 * ESTIMATE.row;
    list.dispatchEvent(new Event("scroll"));
    await tick();
    await rerender({ items: rows(303) });
    await tick();
    // Three rows of the estimated height were inserted above the viewport.
    expect(list.scrollTop).toBe(103 * ESTIMATE.row);
  });

  it("uses a kind's estimate for the window arithmetic", async () => {
    fixViewport();
    const items = Array.from({ length: 80 }, (_, i) => ({ id: `c${i}`, kind: "card" as const }));
    render(VirtualListHarness, { props: { items } });
    await tick();
    expect(visibleRows()).toBeLessThanOrEqual(Math.ceil(VIEWPORT / ESTIMATE.card) + 1 + OVERSCAN);
    expect(visibleRows()).toBeLessThan(Math.ceil(VIEWPORT / ESTIMATE.row) + 1 + OVERSCAN);
  });
});
