import { describe, expect, it, vi } from "vitest";
import { waitFor, within } from "@testing-library/svelte";
import { openPreview, PAGE_TIMEOUT } from "./page.helpers";

class NoopResizeObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}
(globalThis as { ResizeObserver?: unknown }).ResizeObserver ??= NoopResizeObserver;

// xterm probes a canvas while it is built and jsdom has none.
vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    public options: Record<string, unknown> = {};
    public modes = { applicationCursorKeysMode: false, bracketedPasteMode: false };
    public cols = 0;
    public rows = 0;
    public loadAddon = vi.fn();
    public open = vi.fn();
    public onData = vi.fn();
    public focus = vi.fn();
    public reset = vi.fn();
    public resize = vi.fn();
    public write = vi.fn();
    public dispose = vi.fn();
    public attachCustomKeyEventHandler = vi.fn();
    public attachCustomWheelEventHandler = vi.fn();
  },
}));
vi.mock("@xterm/addon-web-links", () => ({
  WebLinksAddon: class {
    public activate = vi.fn();
    public dispose = vi.fn();
  },
}));

describe("preview page: session", () => {
  it("pushes a session over the inbox and lists the files of its diff", async () => {
    const app = await openPreview("?fixture=diff&route=/session/s4/changes");
    await waitFor(() => expect(app.querySelector('[data-fixture="diff"]')).not.toBeNull(), { timeout: PAGE_TIMEOUT });
    await waitFor(() => expect(app.textContent).toContain("Split the settings form into tabs"), { timeout: PAGE_TIMEOUT });
    await waitFor(() => expect(app.textContent).toContain("module00.ts"), { timeout: PAGE_TIMEOUT });
  }, PAGE_TIMEOUT * 2);

  // Layout overflow: the Session root is one column that may shrink (`minmax(0,1fr)`), and every
  // wrapper may shrink with it. jsdom does no layout, so the scrollWidth check below is only
  // meaningful in a real browser run of this page; the class contract is what holds everywhere.
  it("keeps every segment inside a 390 px wide root", async () => {
    const { router } = await import("@mobile/lib/router.svelte");
    const app = document.getElementById("app")!;
    for (const seg of ["conversation", "changes", "review"] as const) {
      router.replace({ name: "session", id: "s4", seg });
      await waitFor(() => expect(app.querySelector("[data-session-screen]")).not.toBeNull(), { timeout: PAGE_TIMEOUT });
      const root = app.querySelector<HTMLElement>("[data-session-screen]")!;
      expect(root.className).toContain("grid-cols-[minmax(0,1fr)]");
      for (const child of Array.from(root.children)) expect(child.className).toContain("min-w-0");
      root.style.width = "390px";
      expect(root.scrollWidth).toBeLessThanOrEqual(root.clientWidth);
    }
    // Let the last lazy segment mount before the next describe resets the module graph: a segment
    // that resolves after `vi.resetModules()` belongs to a second Svelte runtime and throws effect_orphan.
    await waitFor(() => expect(app.querySelector("[data-review-segment]")).not.toBeNull(), { timeout: PAGE_TIMEOUT });
  }, PAGE_TIMEOUT * 3);
});

describe("preview page: terminal sheets", () => {
  it("never scrolls the page sideways on the terminal segment", async () => {
    vi.resetModules();
    const app = await openPreview("?fixture=terminal&route=/session/s1/terminal");
    await waitFor(() => expect(app.querySelector(".term-pane")).not.toBeNull(), { timeout: PAGE_TIMEOUT });
    const pane = app.querySelector<HTMLElement>(".term-pane")!;
    // jsdom does no layout: the class contract is what holds here, the number is for a real browser run.
    expect(pane.className).toContain("min-w-0");
    expect(pane.className).toContain("max-w-full");
    expect(pane.className).toContain("overflow-x-clip");
    const scroller = (document.scrollingElement ?? document.documentElement) as HTMLElement;
    expect(scroller.scrollWidth).toBeLessThanOrEqual(window.innerWidth);
  }, PAGE_TIMEOUT * 2);

  // The agent pane has no menu (it is the session itself), so the link names a shell tab.
  it("opens the pane menu by link", async () => {
    vi.resetModules();
    const app = await openPreview("?fixture=terminal&route=/session/s1/terminal/lola-s1-shell-1&sheet=paneMenu");
    await waitFor(() => expect(within(app).getByRole("dialog", { name: "Options for Shell 1" })).toBeTruthy(), {
      timeout: PAGE_TIMEOUT,
    });
  }, PAGE_TIMEOUT * 2);

  it("opens view settings by link", async () => {
    vi.resetModules();
    const app = await openPreview("?fixture=terminal&route=/session/s1/terminal&sheet=viewSettings");
    await waitFor(() => expect(within(app).getByRole("dialog", { name: "View" })).toBeTruthy(), {
      timeout: PAGE_TIMEOUT,
    });
  }, PAGE_TIMEOUT * 2);
});
