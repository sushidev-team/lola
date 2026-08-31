import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";

import Terminal from "./Terminal.svelte";
import { store } from "$lib/store.svelte";
import { nav } from "@mobile/lib/nav.svelte";
import { ChannelTransport } from "@mobile/wailsshim/channeltransport";
import { bridge } from "@mobile/wailsshim/bridge";
import { FRAME_RESP, FakeChannel, type Frame } from "@mobile/wire";
import { connection } from "./fakeconnection.test.svelte";
import { PIN_STUCK_MESSAGE } from "@mobile/lib/panepin";

// THE SIZE PIN, AT THE SCREEN.
//
// panepin.test.ts proves what the controller does once it is asked. This file
// proves the asking: that focus reaches it with the phone's real measurements,
// and — the part that breaks — that EVERY way out of a focused pane reaches it
// with a release. One test per exit, because the failure this feature must not
// have is a pin that outlives the phone looking at it, and every one of those
// exits is a separate line of wiring that can be forgotten on its own.
//
// The assertions are made on the WIRE, not on a stubbed service: a real
// ChannelTransport over a FakeChannel, so what is checked is the `paneResize`
// frame the daemon would receive, envelope and args included.

// ---------------------------------------------------------------------------
// The DOM the pin measures against.
//
// The pin's whole point is that it sends the PHONE's capacity rather than the
// Mac's grid, and capacity is measured off the layout — which jsdom does not do.
// So the two elements MobileTerminal measures are given sizes: the frame is the
// window (400 x 340) and the inner box is the rendered grid (200 columns at an
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

const metrics: [string, (el: HTMLElement) => number][] = [
  ["clientWidth", (el) => (el.classList.contains("term-pane") ? VIEW_W : 0)],
  ["clientHeight", (el) => (el.classList.contains("term-pane") ? VIEW_H : 0)],
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

async function mount() {
  const r = render(Terminal, { props: { onback: () => {} } });
  // The pin only exists once the pane has painted and been measured.
  await screen.findByRole("tab", { name: "agent" });
  return r;
}

/** Turn the toggle on through the popover, exactly as a person would. */
async function enablePin() {
  await fireEvent.click(screen.getByRole("button", { name: /^View settings/ }));
  await fireEvent.click(screen.getByRole("switch"));
  await fireEvent.click(screen.getByRole("button", { name: "Done" }));
}

beforeEach(async () => {
  globalThis.localStorage?.clear();
  resizes = [];
  refuseResize = false;
  paneList = [
    { name: "lola-fe-42", kind: "agent", label: "agent" },
    { name: "lola-fe-42-shell-1", kind: "shell", label: "shell 1", index: 1 },
  ];
  connection.reset();
  connection.screen = SCREEN;

  ch = new FakeChannel();
  ch.onSend = (f: Frame) => {
    if (f.type !== "req") return;
    const cmd = String(f.cmd);
    const payload = (f.payload ?? {}) as { args?: Partial<ResizeArgs> };
    let data: unknown = {};
    if (cmd === "panes") {
      data = { session: "lola-fe-42", panes: paneList, canCreateShell: true };
    }
    if (cmd === "paneClose" && payload.args?.pane) {
      const gone = payload.args.pane;
      paneList = paneList.filter((p) => p.name !== gone);
      data = { session: "lola-fe-42", pane: gone, closed: true };
    }
    if (cmd === "paneResize" && payload.args) {
      const a = payload.args as ResizeArgs;
      resizes.push(a);
      // THE DAEMON REFUSES A RESIZE OF A PANE THAT IS GONE, release included:
      // `handlePaneResize` validates the pane by name convention and then asks
      // tmux to resize a window that no longer exists. Mirrored here so a
      // release the app should never have sent is visible as the refusal — and
      // the stuck-pin warning — a real device would produce, instead of being
      // absorbed by an over-obliging fake.
      // Only about the session `paneList` describes. A pane of another session
      // — the breadcrumb case — is not covered by it and is not "gone".
      const ours = a.pane === "lola-fe-42" || a.pane.startsWith("lola-fe-42-");
      const gonePane = ours && !paneList.some((p) => p.name === a.pane);
      if (refuseResize || gonePane) {
        ch.deliver({
          v: 1,
          type: FRAME_RESP,
          id: f.id,
          payload: { ok: false, error: "not connected" },
        });
        return;
      }
      data = { ...a, pinned: a.cols > 0 };
    }
    ch.deliver({ v: 1, type: FRAME_RESP, id: f.id, payload: { ok: true, data } });
  };
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect({ host: "127.0.0.1", spkiPin: "pin" });
  bridge.installTransport(t);

  nav.paneSession = "lola-fe-42";
  nav.pane = "lola-fe-42";
});

afterEach(() => {
  cleanup();
  bridge.installTransport(null);
  store.sessions = [];
  nav.closeSheet();
});

// ---------------------------------------------------------------------------

describe("the toggle", () => {
  it("is off by default, and nothing is pinned while it is", async () => {
    await mount();
    // Give the effect every chance to fire before claiming it did not.
    await new Promise((r) => setTimeout(r, 50));
    expect(resizes).toHaveLength(0);
  });

  it("reads as a switch, off, with a name that says what it does to the Mac", async () => {
    await mount();
    await fireEvent.click(screen.getByRole("button", { name: /^View settings/ }));
    const sw = screen.getByRole("switch", { name: "Resize the Mac's pane to fit this phone" });
    expect(sw).toHaveAttribute("aria-checked", "false");
    // 44pt, like every other control on this screen.
    expect(sw.className).toContain("h-11!");
  });

  it("states the cost to the Mac in the sheet itself, not behind a tooltip", async () => {
    await mount();
    await fireEvent.click(screen.getByRole("button", { name: /^View settings/ }));
    expect(screen.getByText(/its window on the Mac is resized/i)).toBeInTheDocument();
    expect(screen.getByText(/redraws when it flips back/i)).toBeInTheDocument();
    // ...and names the size the Mac's window would be squeezed to.
    expect(screen.getByText(new RegExp(`about ${PIN_COLS} by ${PIN_ROWS}`))).toBeInTheDocument();
  });

  it("is remembered, so a screen reopened comes back pinned", async () => {
    await mount();
    await enablePin();
    await sawResizes(1);
    cleanup();
    resizes = [];
    connection.reset();
    connection.screen = SCREEN;

    await mount();
    await sawResizes(1);
    expect(pins()[0]).toMatchObject({ pane: "lola-fe-42", cols: PIN_COLS, rows: PIN_ROWS });
  });
});

// ---------------------------------------------------------------------------

describe("pinning on focus", () => {
  it("sends the phone's own size, not the grid the Mac is sending", async () => {
    await mount();
    await enablePin();
    await sawResizes(1);
    expect(pins()[0]).toEqual({
      session: "lola-fe-42",
      pane: "lola-fe-42",
      cols: PIN_COLS,
      rows: PIN_ROWS,
    });
    // The Mac's own grid would have been a request that changes nothing.
    expect(pins()[0].cols).not.toBe(GRID_COLS);
    expect(pins()[0].rows).not.toBe(GRID_ROWS);
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
    await fireEvent.click(screen.getByRole("switch"));
    await waitFor(() => expect(releases()).toHaveLength(1));
    expect(releases()[0]).toMatchObject({ pane: "lola-fe-42", cols: 0, rows: 0 });
  });
});

// ---------------------------------------------------------------------------
// EVERY WAY OUT. One test each; this is the part that breaks.

describe("release paths", () => {
  it("releases when the session view is left", async () => {
    const r = await mount();
    await enablePin();
    await sawResizes(1);

    // App.svelte swaps this screen out on `onback`, which destroys it.
    r.unmount();
    await waitFor(() => expect(releases()).toHaveLength(1));
    expect(releases()[0]).toMatchObject({ pane: "lola-fe-42", cols: 0, rows: 0 });
  });

  it("releases the old pane when another tab is opened, before pinning the new one", async () => {
    await mount();
    await enablePin();
    await sawResizes(1);

    await fireEvent.click(screen.getByRole("tab", { name: "shell 1" }));
    await waitFor(() => expect(pins()).toHaveLength(2));

    const order = resizes.map((r) => `${r.pane}:${r.cols}`);
    expect(order).toEqual([
      "lola-fe-42:50",
      "lola-fe-42:0",
      "lola-fe-42-shell-1:50",
    ]);
  });

  it("releases when the app goes into the background", async () => {
    await mount();
    await enablePin();
    await sawResizes(1);

    const hidden = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    document.dispatchEvent(new Event("visibilitychange"));
    await waitFor(() => expect(releases()).toHaveLength(1));
    hidden.mockRestore();
  });

  it("releases when the connection goes away", async () => {
    await mount();
    await enablePin();
    await sawResizes(1);

    connection.ready = false;
    await waitFor(() => expect(releases()).toHaveLength(1));
    expect(releases()[0]).toMatchObject({ pane: "lola-fe-42", cols: 0, rows: 0 });
  });

  it("releases when the pane's own process exits", async () => {
    await mount();
    await enablePin();
    await sawResizes(1);

    connection.sub("lola-fe-42")?.emit({
      kind: "exit",
      screen: { ...SCREEN, exited: true },
    } as never);
    await waitFor(() => expect(releases()).toHaveLength(1));
  });

  it("lets the pinned pane go when it is closed from its own menu", async () => {
    // THE EXIT NEITHER HALF OF THIS FEATURE OWNS, which is why it is tested
    // here rather than in either builder's own file.
    //
    // Closing a tab belongs to the strip: PaneTabs.closePane sends
    // `cmd=paneClose`, re-reads the inventory, finds the attached pane missing
    // and calls `onselect` for the neighbour. Releasing a pin belongs to this
    // screen. Nothing else joins the two, and the strip's own tests never look
    // at a pin — so without this the pane a user deliberately closed stayed
    // pinned as far as the app was concerned.
    //
    // AND IT IS RETIRED, NOT RELEASED. Closing a pane ends its tmux window, so
    // the release the obvious wiring would send is one the daemon refuses —
    // which used to raise the "still resized on the Mac" warning about a window
    // that no longer exists, permanently and with nothing the reader could do.
    // The inventory the strip just re-read is what says so.
    await mount();
    await enablePin();
    await sawResizes(1);

    await fireEvent.click(screen.getByRole("tab", { name: "shell 1" }));
    await waitFor(() => expect(pins()).toHaveLength(2));
    const before = resizes.length;

    // The menu is reached the way a development link reaches it — `nav` names
    // the pane and opens the sheet — because opening it for real is a
    // half-second hold and the Simulator has no gesture API. The long press
    // itself is covered in PaneTabs.test.ts.
    nav.menuPane = "lola-fe-42-shell-1";
    nav.openSheet("pane");
    await fireEvent.click(await screen.findByRole("button", { name: "Close this pane" }));

    // The tab the strip moved the user onto is pinned in its place, so a close
    // does not quietly leave the toggle on with nothing held.
    await waitFor(() => expect(pins()).toHaveLength(3));
    expect(pins()[2]).toMatchObject({ pane: "lola-fe-42", cols: PIN_COLS, rows: PIN_ROWS });

    // Nothing was sent about the closed pane at all.
    expect(resizes.slice(before).filter((r) => r.pane === "lola-fe-42-shell-1")).toEqual([]);
    // ...and no warning about a window that is not there.
    expect(screen.queryByText(PIN_STUCK_MESSAGE)).toBeNull();
  });

  it("never pins two panes at once, whichever exit ran", async () => {
    // The invariant behind all five, replayed off the wire: a switch, a
    // disconnect and a teardown in one run must never leave two windows held.
    const r = await mount();
    await enablePin();
    await sawResizes(1);
    await fireEvent.click(screen.getByRole("tab", { name: "shell 1" }));
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

// ---------------------------------------------------------------------------

describe("a pin left behind by an earlier run", () => {
  it("is released on the next open, which is what unsquashes a window after a force quit", async () => {
    // Nothing on the daemon releases a pin when a subscription ends, so this
    // breadcrumb is the only thing that ever undoes one. The pane is another
    // session's, so the screen being opened cannot be mistaken for it.
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

// ---------------------------------------------------------------------------

describe("a release that could not be sent", () => {
  it("says so on screen rather than leaving the Mac squashed in silence", async () => {
    await mount();
    await enablePin();
    await sawResizes(1);

    // The socket is going away, so the release cannot land. The app knows the
    // Mac is still holding the pin; the person holding the phone does not.
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
