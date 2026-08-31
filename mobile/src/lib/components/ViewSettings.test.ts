import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";
import ViewSettings from "./ViewSettings.svelte";
import ViewSettingsHarness from "./ViewSettingsHarness.test.svelte";
import { FONT_MAX, FONT_MIN } from "@mobile/lib/viewport";
import { loadFontSize, saveFontSize } from "@mobile/lib/prefs";

// xterm probes a canvas while it is being constructed and jsdom has none. The
// popover never touches it; the harness mounts a real MobileTerminal purely so
// the persistence path under test is the real one.
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

// No transport in a unit test. A rejected subscribe is the real shape of that
// (see MobileTerminal.attach), and the font size is remembered either way —
// which is itself worth having the test prove.
vi.mock("@mobile/lib/connection.svelte", () => ({
  connection: {
    ready: false,
    subscribe: vi.fn(() => Promise.reject(new Error("not connected"))),
  },
}));

class StubResizeObserver {
  public observe(): void {}
  public unobserve(): void {}
  public disconnect(): void {}
}
globalThis.ResizeObserver ??= StubResizeObserver as unknown as typeof ResizeObserver;

/** MobileTerminal's trailing debounce on the font write, plus a margin. */
const FONT_SAVE_WAIT_MS = 500;
const settle = (ms: number) => new Promise((r) => setTimeout(r, ms));

function geometry(over: Partial<Record<string, number | boolean>> = {}) {
  return {
    cols: 0,
    rows: 0,
    shown: 0,
    shownRows: 0,
    first: 1,
    panning: false,
    canFit: false,
    fitActive: false,
    fitSize: 0,
    ...over,
  } as never;
}

/** A clipped 211-column grid showing columns 44 to 86 — the real case. */
const CLIPPED = geometry({
  cols: 211,
  rows: 44,
  shown: 43,
  shownRows: 22,
  first: 44,
  panning: true,
});
const WHOLE = geometry({ cols: 80, rows: 24, shown: 80, shownRows: 24, first: 1, panning: false });

function mount(props: Record<string, unknown> = {}) {
  return render(ViewSettings, {
    font: 12,
    geom: CLIPPED,
    onfont: () => {},
    onfit: () => {},
    ...props,
  });
}

const trigger = () => screen.getByRole("button", { name: /^View settings/ });
const dialog = () => screen.queryByRole("dialog", { name: "View settings" });

beforeEach(() => {
  cleanup();
  globalThis.localStorage?.clear();
});
afterEach(cleanup);

// ---------------------------------------------------------------------------

describe("ViewSettings opening and closing", () => {
  it("starts closed, behind one button", () => {
    mount();
    expect(dialog()).toBeNull();
    expect(trigger()).toHaveAttribute("aria-expanded", "false");
    expect(trigger()).toHaveAttribute("aria-haspopup", "dialog");
  });

  it("opens on a tap", async () => {
    mount();
    await fireEvent.click(trigger());
    expect(dialog()).toBeInTheDocument();
    expect(trigger()).toHaveAttribute("aria-expanded", "true");
  });

  it("closes from the backdrop, which is a real labelled control", async () => {
    mount();
    await fireEvent.click(trigger());
    await fireEvent.click(screen.getByRole("button", { name: "Close view settings" }));
    expect(dialog()).toBeNull();
  });

  it("closes from Done", async () => {
    mount();
    await fireEvent.click(trigger());
    await fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(dialog()).toBeNull();
  });

  // Only a hardware keyboard can produce one, but a modal that traps such a
  // keyboard is worse than the handler that prevents it.
  it("closes on Escape", async () => {
    mount();
    await fireEvent.click(trigger());
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(dialog()).toBeNull();
  });

  it("is one button, and it is a 44pt target", () => {
    mount();
    // The whole point of the change: four controls and a subtitle became one.
    expect(screen.getAllByRole("button")).toHaveLength(1);
    expect(trigger().className).toContain("h-11!");
    expect(trigger().className).toContain("w-11!");
  });
});

// ---------------------------------------------------------------------------
// The clipping signal. This is the requirement the popover must not swallow: a
// pane clipped at column 55 looks exactly like an agent that stopped writing
// mid-line, so the fact has to survive OUTSIDE the popover as well as in it.

describe("ViewSettings clipping signal", () => {
  it("names the visible range on the trigger itself, unopened", () => {
    mount({ geom: CLIPPED });
    expect(
      screen.getByRole("button", { name: "View settings. Showing columns 44–86 of 211" }),
    ).toBeInTheDocument();
  });

  it("marks the trigger while the grid is clipped", () => {
    const { container } = mount({ geom: CLIPPED });
    expect(container.querySelector(".bg-warn")).not.toBeNull();
  });

  it("says nothing extra when the whole grid is on screen", () => {
    const { container } = mount({ geom: WHOLE, font: 12 });
    expect(screen.getByRole("button", { name: "View settings" })).toBeInTheDocument();
    expect(container.querySelector(".bg-warn")).toBeNull();
  });

  it("reports the range and the reason once open", async () => {
    mount({ geom: CLIPPED });
    await fireEvent.click(trigger());
    expect(screen.getByText(/Columns 44–86 of 211/)).toBeInTheDocument();
    expect(screen.getByText(/44 rows/)).toBeInTheDocument();
    expect(screen.getByText(/wider than the screen/)).toBeInTheDocument();
  });

  it("says so plainly when nothing is clipped", async () => {
    mount({ geom: WHOLE });
    await fireEvent.click(trigger());
    expect(screen.getByText(/All 80 columns/)).toBeInTheDocument();
    expect(screen.queryByText(/wider than the screen/)).toBeNull();
  });

  it("admits there is no grid yet rather than reporting a zero", async () => {
    mount({ geom: geometry() });
    await fireEvent.click(trigger());
    expect(screen.getByText("No grid yet.")).toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------

describe("ViewSettings fit action", () => {
  it("offers the size it would land on", async () => {
    mount({ geom: geometry({ cols: 211, rows: 44, shown: 43, first: 1, panning: true, canFit: true, fitSize: 8 }) });
    await fireEvent.click(trigger());
    expect(screen.getByRole("button", { name: "Fit the width (8 pt)" })).toBeInTheDocument();
  });

  it("runs the fit and closes, so the result is visible", async () => {
    const onfit = vi.fn();
    mount({
      onfit,
      geom: geometry({ cols: 211, rows: 44, shown: 43, first: 1, panning: true, canFit: true, fitSize: 8 }),
    });
    await fireEvent.click(trigger());
    await fireEvent.click(screen.getByRole("button", { name: "Fit the width (8 pt)" }));
    expect(onfit).toHaveBeenCalledTimes(1);
    expect(dialog()).toBeNull();
  });

  it("offers the way back while a fit is in effect", async () => {
    mount({
      font: 8,
      geom: geometry({ cols: 211, rows: 44, shown: 120, first: 1, panning: true, fitActive: true, fitSize: 8 }),
    });
    await fireEvent.click(trigger());
    expect(screen.getByRole("button", { name: "Back to the reading size" })).toBeInTheDocument();
  });

  // At the 8-point floor with a grid still wider than the screen there is no
  // fit to offer. A sentence beats a dead button, which is the same rule the
  // header followed before this moved.
  it("draws no dead button at the floor, and says why", async () => {
    mount({ font: FONT_MIN, geom: CLIPPED });
    await fireEvent.click(trigger());
    expect(screen.queryByRole("button", { name: /Fit the width/ })).toBeNull();
    expect(screen.getByText(/Already at the smallest text/)).toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------

describe("ViewSettings font controls", () => {
  it("emits an absolute size, one point at a time", async () => {
    const onfont = vi.fn();
    mount({ font: 12, onfont });
    await fireEvent.click(trigger());
    await fireEvent.click(screen.getByRole("button", { name: "Larger text" }));
    expect(onfont).toHaveBeenLastCalledWith(13);
    await fireEvent.click(screen.getByRole("button", { name: "Smaller text" }));
    expect(onfont).toHaveBeenLastCalledWith(11);
  });

  it("shows the current size and announces it", async () => {
    mount({ font: 14 });
    await fireEvent.click(trigger());
    const readout = screen.getByRole("status");
    expect(readout).toHaveTextContent("14 pt");
    expect(readout).toHaveAttribute("aria-live", "polite");
  });

  // TWO ASSERTIONS PER LIMIT, and the second is the one that matters. A real
  // browser drops a click on a disabled button; jsdom dispatches it at the
  // target anyway, so the disabled attribute alone proves nothing here. The
  // clamp is the actual guarantee — stepFont is the same function the header
  // called — so the test pins that a click getting through cannot leave the
  // range rather than pinning that it cannot happen.
  it("keeps the clamp: nothing below the floor", async () => {
    const onfont = vi.fn();
    mount({ font: FONT_MIN, onfont });
    await fireEvent.click(trigger());
    const smaller = screen.getByRole("button", { name: "Smaller text" });
    expect(smaller).toBeDisabled();
    await fireEvent.click(smaller);
    expect(onfont).toHaveBeenLastCalledWith(FONT_MIN);
  });

  it("keeps the clamp: nothing above the ceiling", async () => {
    const onfont = vi.fn();
    mount({ font: FONT_MAX, onfont });
    await fireEvent.click(trigger());
    const larger = screen.getByRole("button", { name: "Larger text" });
    expect(larger).toBeDisabled();
    await fireEvent.click(larger);
    expect(onfont).toHaveBeenLastCalledWith(FONT_MAX);
  });

  // The lie this component must not tell: at a readable size with a clipped
  // grid there IS a smaller size, and saying otherwise sends a reader looking
  // for a control that is right there.
  it("does not claim the floor at a readable size", async () => {
    mount({ font: 12, geom: CLIPPED });
    await fireEvent.click(trigger());
    expect(screen.queryByText(/Already at the smallest text/)).toBeNull();
  });

  it("gives both font controls a 44pt target", async () => {
    mount();
    await fireEvent.click(trigger());
    for (const name of ["Smaller text", "Larger text"]) {
      expect(screen.getByRole("button", { name }).className).toContain("h-11!");
    }
  });

  it("refuses every control when the caller disables it", async () => {
    mount({ disabled: true });
    expect(trigger()).toBeDisabled();
  });
});

// ---------------------------------------------------------------------------
// The seam. Everything above proves the popover emits the right size; this
// proves the size still reaches the disk through the real terminal, which is
// the regression that moving these buttons could have caused silently.

describe("font size still persists through the popover", () => {
  it("remembers a size chosen inside the popover", async () => {
    saveFontSize(12);
    render(ViewSettingsHarness);
    await fireEvent.click(trigger());
    await fireEvent.click(screen.getByRole("button", { name: "Larger text" }));
    await settle(FONT_SAVE_WAIT_MS);
    expect(loadFontSize()).toBe(13);
  });

  it("remembers a smaller one too, and the readout follows the terminal", async () => {
    saveFontSize(12);
    render(ViewSettingsHarness);
    await fireEvent.click(trigger());
    await fireEvent.click(screen.getByRole("button", { name: "Smaller text" }));
    expect(screen.getByRole("status")).toHaveTextContent("11 pt");
    await settle(FONT_SAVE_WAIT_MS);
    expect(loadFontSize()).toBe(11);
  });

  it("opens at the remembered size rather than the default", async () => {
    saveFontSize(FONT_MAX);
    render(ViewSettingsHarness);
    await fireEvent.click(trigger());
    expect(screen.getByRole("status")).toHaveTextContent(`${FONT_MAX} pt`);
    // And the ceiling is already in force, with no round trip needed to learn it.
    expect(screen.getByRole("button", { name: "Larger text" })).toBeDisabled();
  });
});

describe("ViewSettings is addressable", () => {
  it("opens from the outside, so a link can land on the column readout", () => {
    // This popover holds the only number that says a pane is CLIPPED rather
    // than an agent having stopped writing mid-line, and until `open` was a
    // prop no screenshot of it could be taken at all: nothing but a tap opens
    // it, and the Simulator has no gesture API. The terminal screen binds this
    // to nav.sheet; everywhere else the component still runs uncontrolled.
    render(ViewSettings, {
      props: {
        font: 12,
        geom: geometry({ cols: 211, rows: 44, shown: 58, first: 44, panning: true }),
        onfont: () => {},
        onfit: () => {},
        open: true,
      },
    });
    expect(screen.getByRole("dialog", { name: "View settings" })).toBeTruthy();
    expect(screen.getByText(/44–101 of 211/)).toBeTruthy();
  });
});

// ---------------------------------------------------------------------------
// THE SIZE PIN. It is the one control in this app that changes what somebody
// else sees, so the two things worth pinning about it are that it starts off
// and that its cost to the Mac is on the sheet rather than behind a tooltip.

describe("ViewSettings size pin", () => {
  it("draws a switch that is off unless the caller says otherwise", async () => {
    mount();
    await fireEvent.click(trigger());
    const sw = screen.getByRole("switch", { name: "Resize the Mac's pane to fit this phone" });
    expect(sw).toHaveAttribute("aria-checked", "false");
    expect(sw).toHaveTextContent("Off");
  });

  it("reports the state with aria-checked once it is on", async () => {
    mount({ pinned: true });
    await fireEvent.click(trigger());
    const sw = screen.getByRole("switch");
    expect(sw).toHaveAttribute("aria-checked", "true");
    expect(sw).toHaveTextContent("On");
  });

  it("asks for the opposite of what it currently is", async () => {
    const onpin = vi.fn();
    mount({ onpin });
    await fireEvent.click(trigger());
    await fireEvent.click(screen.getByRole("switch"));
    expect(onpin).toHaveBeenLastCalledWith(true);

    cleanup();
    mount({ pinned: true, onpin });
    await fireEvent.click(trigger());
    await fireEvent.click(screen.getByRole("switch"));
    expect(onpin).toHaveBeenLastCalledWith(false);
  });

  // The requirement in one test: the sheet has to say what this does to the
  // MAC, not only what it does here. The fit-width caption four lines above it
  // says the opposite thing ("a zoom on this phone only"), so the two must not
  // be confusable.
  it("names the cost to the Mac in plain words, on the sheet", async () => {
    mount({ geom: CLIPPED });
    await fireEvent.click(trigger());
    expect(screen.getByText(/its window on the Mac is resized/i)).toBeInTheDocument();
    expect(screen.getByText(/Your own view of the session there is/i)).toBeInTheDocument();
    expect(screen.getByText(/redraws when it flips back/i)).toBeInTheDocument();
    // ...and the size, so the cost is a number rather than an adjective.
    expect(screen.getByText(/about 43 by 22/)).toBeInTheDocument();
  });

  it("claims no size before the pane has been measured", async () => {
    mount({ geom: geometry() });
    await fireEvent.click(trigger());
    expect(screen.queryByText(/about/)).toBeNull();
  });

  it("is a 44pt target, and the caller can disable it with the rest", async () => {
    mount({ disabled: false });
    await fireEvent.click(trigger());
    expect(screen.getByRole("switch").className).toContain("h-11!");
  });
});
