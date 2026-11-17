import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";

import TerminalHost from "./TerminalHost.test.svelte";
import { copy } from "@mobile/lib/copy";
import { router } from "@mobile/lib/router.svelte";
import { tiers } from "@mobile/lib/tiers.svelte";
import { terminalView } from "@mobile/lib/terminalgeometry.svelte";
import { paneTabs } from "@mobile/lib/panetabs.svelte";
import { ChannelTransport } from "@mobile/wailsshim/channeltransport";
import { bridge } from "@mobile/wailsshim/bridge";
import { FRAME_RESP, FakeChannel, type Frame } from "@mobile/wire";
import { connection } from "./fakeconnection.test.svelte";
import { PIN_STUCK_MESSAGE } from "@mobile/lib/panepin";

// The Terminal segment: the pane, its tab strip and the size pin, over a real
// ChannelTransport on a FakeChannel so assertions are made on the `paneResize`
// frames the daemon would receive. xterm is mocked; the DOM measurements the
// pin depends on are given sizes below (jsdom does no layout).
//
// The pin's failure mode is a window squashed on the Mac with nothing saying
// why, so every way out of a focused pane has its own test.

// ---------------------------------------------------------------------------
// The DOM the pin measures against.
//
// The pin's whole point is that it sends the PHONE's capacity rather than the
// Mac's grid, and capacity is measured off the layout — which jsdom does not do.
// So the two elements MobileTerminal measures are given sizes: the padded window
// (the frame, 424 wide, less its 12px padding each side, `data-term-view`) is 400 x 340 and the inner box is the rendered grid (200 columns at an
// 8px cell, 50 rows at 17). That makes the capacity 50 x 20, which is what a
// correct pin must carry and what a pin of the grid (200 x 50) would not.
const CELL_W = 8;
const CELL_H = 17;
const GRID_COLS = 200;
const GRID_ROWS = 50;
const VIEW_W = 400;
const VIEW_H = 340;
/** floor(400 / 8) and floor(340 / 17). */
const PIN_COLS = 50;
const PIN_ROWS = 20;

const FRAME_W = VIEW_W + 2 * 12;
const metrics: [string, (el: HTMLElement) => number][] = [
  ["clientWidth", (el) => (el.classList.contains("term-pane") ? FRAME_W : 0)],
  ["clientHeight", (el) => (el.hasAttribute("data-term-view") ? VIEW_H : 0)],
  [
    "offsetWidth",
    (el) => (el.classList.contains("will-change-transform") ? GRID_COLS * CELL_W : 0),
  ],
  [
    "offsetHeight",
    (el) => (el.classList.contains("will-change-transform") ? GRID_ROWS * CELL_H : 0),
  ],
];
for (const [prop, read] of metrics) {
  Object.defineProperty(HTMLElement.prototype, prop, {
    configurable: true,
    get(this: HTMLElement) {
      return read(this);
    },
  });
}

class NoopResizeObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}
(globalThis as { ResizeObserver?: unknown }).ResizeObserver ??= NoopResizeObserver;

// xterm probes a canvas jsdom does not have. The mock keeps the one behaviour
// the measurement depends on: `resize` moves cols/rows, and `write` runs its
// completion callback, which is where MobileTerminal re-measures.
vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    public options: Record<string, unknown> = {};
    public modes = { applicationCursorKeysMode: false, bracketedPasteMode: false };
    constructor() {
      terms.push(this);
    }
    public cols = 0;
    public rows = 0;
    public loadAddon = vi.fn();
    public open = vi.fn();
    public onData = vi.fn();
    public focus = vi.fn();
    public reset = vi.fn();
    public dispose = vi.fn();
    public attachCustomKeyEventHandler = vi.fn();
    public attachCustomWheelEventHandler = vi.fn();
    public resize(cols: number, rows: number): void {
      this.cols = cols;
      this.rows = rows;
    }
    public write(_data: string, cb?: () => void): void {
      cb?.();
    }
  },
}));
vi.mock("@xterm/addon-web-links", () => ({
  WebLinksAddon: class {
    public activate = vi.fn();
    public dispose = vi.fn();
  },
}));

vi.mock("@mobile/lib/connection.svelte", async () => {
  const m = await import("./fakeconnection.test.svelte");
  return { connection: m.connection };
});

// ---------------------------------------------------------------------------

/** The resync a fresh subscription acknowledges with: a 200 x 50 grid. */
const SCREEN = {
  cols: GRID_COLS,
  rows: GRID_ROWS,
  lines: ["hello"],
  cursorX: 0,
  cursorY: 0,
  altScreen: true,
};

interface ResizeArgs {
  session: string;
  pane: string;
  cols: number;
  rows: number;
}

let ch: FakeChannel;
let resizes: ResizeArgs[];
/** When set, every paneResize is refused, as a dead socket would refuse it. */
let refuseResize = false;
/**
 * The inventory the fake daemon is currently serving.
 *
 * Mutable, because `cmd=paneClose` ends a pane's tmux session and `cmd=panes`
 * derives from tmux — so the tab really does stop being listed, which is the
 * mechanism the strip's move-the-user-off behaviour depends on. A frozen list
 * would let a close "succeed" with nothing changing and the pin never asked to
 * let go.
 */
let paneList: { name: string; kind: string; label: string; index?: number }[];

const pins = () => resizes.filter((r) => r.cols > 0);
const releases = () => resizes.filter((r) => r.cols === 0);

/** Wait until the wire has seen `n` paneResize calls. */
const sawResizes = (n: number) => waitFor(() => expect(resizes.length).toBeGreaterThanOrEqual(n));



/** The mocked terminals, newest last, so a test can change what xterm reports. */
const terms: { modes: { applicationCursorKeysMode: boolean; bracketedPasteMode: boolean } }[] = [];

const SESSION = "lola-fe-42";

/** Everything the segment needs from the route: a session over the inbox. */
function openRoute(pane?: string) {
  router.stack = [
    { name: "tab", tab: "inbox" },
    { name: "session", id: SESSION, seg: "terminal", ...(pane ? { pane } : {}) },
  ];
  router.sheet = "";
}

async function mount() {
  const r = render(TerminalHost);
  await screen.findByRole("tab", { name: "Agent" });
  // The terminal is a lazy chunk: wait until it has attached and measured.
  await waitFor(() => expect(terminalView.capacity.cols).toBe(PIN_COLS), { timeout: 5000 });
  return r;
}

/** Turn the pin on through the view-settings sheet, as a person would. */
async function enablePin() {
  await fireEvent.click(screen.getByRole("button", { name: /^View settings/ }));
  await fireEvent.click(await screen.findByRole("switch"));
  await fireEvent.click(screen.getByRole("button", { name: "Done" }));
}

beforeEach(async () => {
  globalThis.localStorage?.clear();
  // The view is a singleton that re-reads its stored toggle on reset.
  terminalView.reset();
  resizes = [];
  refuseResize = false;
  paneList = [
    { name: "lola-fe-42", kind: "agent", label: "agent" },
    { name: "lola-fe-42-shell-1", kind: "shell", label: "shell 1", index: 1 },
  ];
  connection.reset();
  connection.screen = SCREEN;
  tiers.set(["read", "answer", "control", "destructive", "shell"]);
  openRoute();

  ch = new FakeChannel();
  // Frames from a previous test's late teardown must not land in this test's tally.
  const mine = resizes;
  ch.onSend = (f: Frame) => {
    if (f.type !== "req") return;
    const cmd = String(f.cmd);
    const payload = (f.payload ?? {}) as { args?: Partial<ResizeArgs> };
    let data: unknown = {};
    if (cmd === "panes") {
      data = { session: SESSION, panes: paneList, canCreateShell: true };
    }
    if (cmd === "paneClose" && payload.args?.pane) {
      const gone = payload.args.pane;
      paneList = paneList.filter((p) => p.name !== gone);
      data = { session: SESSION, pane: gone, closed: true };
    }
    if (cmd === "paneResize" && payload.args) {
      const a = payload.args as ResizeArgs;
      resizes.push(a);
      // The daemon refuses a resize of a pane that is gone, release included.
      const ours = a.pane === SESSION || a.pane.startsWith(`${SESSION}-`);
      const gonePane = ours && !paneList.some((p) => p.name === a.pane);
      if (refuseResize || gonePane) {
        ch.deliver({ v: 2, type: FRAME_RESP, id: f.id, payload: { ok: false, error: "not connected" } });
        return;
      }
      data = { ...a, pinned: a.cols > 0 };
    }
    ch.deliver({ v: 2, type: FRAME_RESP, id: f.id, payload: { ok: true, data } });
  };
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect({ daemonId: "d" });
  bridge.installTransport(t);
});

afterEach(() => {
  cleanup();
  bridge.installTransport(null);
  router.sheet = "";
  tiers.clear();
  paneTabs.release();
  terms.length = 0;
});

// ---------------------------------------------------------------------------

describe("geometry reports", () => {
  it("does not reassign geom when a push changes nothing about the view", async () => {
    await mount();
    await waitFor(() => expect(terminalView.geom.cols).toBe(GRID_COLS));
    const before = terminalView.geom;
    const modes = terminalView.modes;

    // A terminal-mode change is a state push whose geometry fields are all
    // unchanged; readers of `geom` must not re-run for it.
    terms[terms.length - 1].modes.bracketedPasteMode = true;
    connection.sub(SESSION)?.emit({ kind: "data", data: "x", gap: false, seq: 1 } as never);

    await waitFor(() => expect(terminalView.modes).not.toBe(modes));
    expect(terminalView.geom).toBe(before);
  });
});

describe("the gutter", () => {
  it("pads the terminal 12px each side and measures cols inside it", async () => {
    await mount();
    const win = document.querySelector("[data-term-view]") as HTMLElement;
    expect(win.style.left).toBe("12px");
    expect(win.style.right).toBe("12px");
    // Capacity follows the padded window (see the metrics above), not the frame.
    expect(terminalView.capacity.cols).toBe(PIN_COLS);
  });

  it("draws the view and new-shell controls as 44pt icon buttons", async () => {
    await mount();
    for (const b of [screen.getByRole("button", { name: /^View settings/ }), screen.getByRole("button", { name: /shell/i })]) {
      expect(b.className).toContain("h-11!");
      expect(b.className).toContain("w-11!");
      expect(b.querySelector("svg")).not.toBeNull();
      expect(b.textContent?.trim()).toBe("");
    }
  });
});

describe("the clipping signal", () => {
  // A phone shows ~55 of a developer's 200 columns, and a clipped pane looks
  // like an agent that stopped writing mid-line, so the view button states it.
  it("names the visible range on the view button, unopened", async () => {
    await mount();
    await waitFor(() => {
      const button = screen.getByRole("button", { name: /^View settings/ });
      expect(button.getAttribute("aria-label")).toMatch(/Showing columns \d+–\d+ of \d+/);
    });
  });

  it("draws the plain type icon with no dot, the clipping living in the name", async () => {
    await mount();
    await waitFor(() => {
      const button = screen.getByRole("button", { name: /^View settings/ });
      expect(button.querySelector(".bg-warn")).toBeNull();
      expect(button.querySelector("svg")).not.toBeNull();
      expect(button.getAttribute("aria-label")).toMatch(/Showing columns/);
    });
  });

  it("paints the terminal on the page's canvas, split from the pane bar by a hairline", async () => {
    await mount();
    const pane = document.querySelector(".term-pane") as HTMLElement;
    expect(pane.className).toContain("bg-canvas");
    expect(pane.className).toContain("border-edge-soft");
    expect(pane.className).toContain("border-t-[0.5px]");
    expect(pane.className).toContain("overflow-x-clip");
  });
});

describe("the tier", () => {
  it("shows the pane but offers no typing, new shell or pin without the shell tier", async () => {
    tiers.set(["read", "answer", "control"]);
    await mount();
    expect(screen.getByText("This phone can't do that.", { selector: "div" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "This phone can't do that." })).toBeDisabled();
    await fireEvent.click(screen.getByRole("button", { name: /^View settings/ }));
    await screen.findByRole("button", { name: "Larger text" });
    expect(screen.queryByRole("switch")).toBeNull();
  });

  it("never pins without the shell tier", async () => {
    tiers.set(["read", "answer", "control"]);
    terminalView.setPin(true);
    await mount();
    await new Promise((r) => setTimeout(r, 60));
    expect(resizes).toHaveLength(0);
  });
});

describe("following the route", () => {
  it("attaches the pane the route names", async () => {
    openRoute("lola-fe-42-shell-1");
    await mount();
    await waitFor(() => expect(connection.sub("lola-fe-42-shell-1")).toBeDefined());
    expect(screen.getByRole("tab", { name: "Shell 1" })).toHaveAttribute("aria-selected", "true");
  });

  it("replaces the route when another tab is chosen", async () => {
    await mount();
    await fireEvent.click(screen.getByRole("tab", { name: "Shell 1" }));
    expect(router.top).toEqual({ name: "session", id: SESSION, seg: "terminal", pane: "lola-fe-42-shell-1" });
    expect(router.stack).toHaveLength(2);
  });

  it("says so, in one line, when the pane has gone", async () => {
    await mount();
    connection.sub(SESSION)?.emit({ kind: "exit", screen: { ...SCREEN, exited: true } } as never);
    expect(await screen.findByText(/Session ended/)).toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------

describe("the toggle", () => {
  it("is off by default, and nothing is pinned while it is", async () => {
    await mount();
    await new Promise((r) => setTimeout(r, 50));
    expect(resizes).toHaveLength(0);
  });

  it("reads as a switch, off, named for what it does to the Mac", async () => {
    await mount();
    await fireEvent.click(screen.getByRole("button", { name: /^View settings/ }));
    const sw = await screen.findByRole("switch", { name: copy.viewSettings.fit });
    expect(sw).toHaveAttribute("aria-checked", "false");
  });

  it("carries no header, helper or Off word beside the switch", async () => {
    await mount();
    await fireEvent.click(screen.getByRole("button", { name: /^View settings/ }));
    await screen.findByRole("switch");
    expect(screen.queryByText(/Narrows the Mac's window/i)).toBeNull();
    expect(screen.queryByText(/Pane size on the Mac/i)).toBeNull();
  });

  it("is remembered, so a segment reopened comes back pinned", async () => {
    await mount();
    await enablePin();
    await sawResizes(1);
    cleanup();
    resizes.length = 0;
    connection.reset();
    connection.screen = SCREEN;

    await mount();
    await sawResizes(1);
    expect(pins()[0]).toMatchObject({ pane: SESSION, cols: PIN_COLS, rows: PIN_ROWS });
  });
});

describe("pinning on focus", () => {
  it("sends the phone's own size, not the grid the Mac is sending", async () => {
    await mount();
    await enablePin();
    await sawResizes(1);
    expect(pins()[0]).toEqual({ session: SESSION, pane: SESSION, cols: PIN_COLS, rows: PIN_ROWS });
    expect(pins()[0].cols).not.toBe(GRID_COLS);
  });

  it("pins once, not once per state push", async () => {
    await mount();
    await enablePin();
    await sawResizes(1);
    await new Promise((r) => setTimeout(r, 60));
    expect(pins()).toHaveLength(1);
  });

  it("releases the moment the toggle goes back off", async () => {
    await mount();
    await enablePin();
    await sawResizes(1);
    await fireEvent.click(screen.getByRole("button", { name: /^View settings/ }));
    await fireEvent.click(await screen.findByRole("switch"));
    await waitFor(() => expect(releases()).toHaveLength(1));
    expect(releases()[0]).toMatchObject({ pane: SESSION, cols: 0, rows: 0 });
  });
});

// Every way out. One test each; this is the part that breaks.
describe("release paths", () => {
  it("releases when the segment is left", async () => {
    const r = await mount();
    await enablePin();
    await sawResizes(1);
    r.unmount();
    await waitFor(() => expect(releases()).toHaveLength(1));
    expect(releases()[0]).toMatchObject({ pane: SESSION, cols: 0, rows: 0 });
  });

  it("releases the old pane when another tab is opened, before pinning the new one", async () => {
    await mount();
    await enablePin();
    await sawResizes(1);

    await fireEvent.click(screen.getByRole("tab", { name: "Shell 1" }));
    await waitFor(() => expect(pins()).toHaveLength(2));
    expect(resizes.map((r) => `${r.pane}:${r.cols}`)).toEqual([
      "lola-fe-42:50",
      "lola-fe-42:0",
      "lola-fe-42-shell-1:50",
    ]);
  });

  it("tries to release on the way into the background, and keeps the breadcrumb when the hidden bridge refuses", async () => {
    // The bridge refuses every request while the document is hidden, so this
    // release races a teardown and loses: the pin must stay recorded and say so,
    // and the return to the foreground must finish the job.
    await mount();
    await enablePin();
    await sawResizes(1);

    const hidden = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    try {
      document.dispatchEvent(new Event("visibilitychange"));
      expect(await screen.findByText(PIN_STUCK_MESSAGE)).toBeInTheDocument();
      expect(globalThis.localStorage?.getItem("lola.mobile.pinnedPane")).not.toBeNull();
    } finally {
      hidden.mockRestore();
    }

    // The foreground brings a new socket: the sweep releases what was left (the
    // toggle is still on, so the pane is pinned again afterwards).
    connection.ready = false;
    await new Promise((r) => setTimeout(r, 20));
    connection.ready = true;
    await waitFor(() => expect(releases()).toHaveLength(1));
    await waitFor(() => expect(screen.queryByText(PIN_STUCK_MESSAGE)).toBeNull());
  });

  it("releases when the connection goes away", async () => {
    await mount();
    await enablePin();
    await sawResizes(1);
    connection.ready = false;
    await waitFor(() => expect(releases()).toHaveLength(1));
    expect(releases()[0]).toMatchObject({ pane: SESSION, cols: 0, rows: 0 });
  });

  it("releases when the pane's own process exits", async () => {
    await mount();
    await enablePin();
    await sawResizes(1);
    connection.sub(SESSION)?.emit({ kind: "exit", screen: { ...SCREEN, exited: true } } as never);
    await waitFor(() => expect(releases()).toHaveLength(1));
  });

  it("retires, not releases, the pinned pane when it is closed from its own menu", async () => {
    // Closing a pane ends its tmux window, so a release would be refused and
    // raise a warning about a window that no longer exists. The inventory the
    // strip just re-read is what retires the record.
    await mount();
    await enablePin();
    await sawResizes(1);
    await fireEvent.click(screen.getByRole("tab", { name: "Shell 1" }));
    await waitFor(() => expect(pins()).toHaveLength(2));
    const before = resizes.length;

    paneTabs.menuPane = "lola-fe-42-shell-1";
    router.open("paneMenu");
    await fireEvent.click(await screen.findByRole("button", { name: "Close this pane" }));

    await waitFor(() => expect(pins()).toHaveLength(3));
    expect(pins()[2]).toMatchObject({ pane: SESSION, cols: PIN_COLS, rows: PIN_ROWS });
    expect(resizes.slice(before).filter((r) => r.pane === "lola-fe-42-shell-1")).toEqual([]);
    expect(screen.queryByText(PIN_STUCK_MESSAGE)).toBeNull();
  });

  it("never pins two panes at once, whichever exit ran", async () => {
    const r = await mount();
    await enablePin();
    await sawResizes(1);
    await fireEvent.click(screen.getByRole("tab", { name: "Shell 1" }));
    await waitFor(() => expect(pins()).toHaveLength(2));
    r.unmount();
    await waitFor(() => expect(releases()).toHaveLength(2));

    const held = new Set<string>();
    for (const c of resizes) {
      if (c.cols > 0) held.add(c.pane);
      else held.delete(c.pane);
      expect(held.size, `two panes pinned after ${c.pane}:${c.cols}`).toBeLessThanOrEqual(1);
    }
    expect(held.size).toBe(0);
  });
});

describe("a pin left behind by an earlier run", () => {
  it("is released on the next open, which unsquashes a window after a force quit", async () => {
    globalThis.localStorage?.setItem(
      "lola.mobile.pinnedPane",
      JSON.stringify({ session: "lola-api-7", pane: "lola-api-7-shell-2" }),
    );
    await mount();
    await waitFor(() => expect(releases()).toHaveLength(1));
    expect(releases()[0]).toMatchObject({ pane: "lola-api-7-shell-2", cols: 0, rows: 0 });
    expect(globalThis.localStorage?.getItem("lola.mobile.pinnedPane")).toBeNull();
  });
});

describe("a release that could not be sent", () => {
  it("says so on screen rather than leaving the Mac squashed in silence", async () => {
    await mount();
    await enablePin();
    await sawResizes(1);
    refuseResize = true;
    connection.ready = false;
    expect(await screen.findByText(PIN_STUCK_MESSAGE)).toBeInTheDocument();
  });

  it("takes the warning back down once the release gets through", async () => {
    await mount();
    await enablePin();
    await sawResizes(1);
    refuseResize = true;
    connection.ready = false;
    await screen.findByText(PIN_STUCK_MESSAGE);

    refuseResize = false;
    connection.ready = true;
    await waitFor(() => expect(screen.queryByText(PIN_STUCK_MESSAGE)).toBeNull());
  });
});
