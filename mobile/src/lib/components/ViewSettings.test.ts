import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";
import ViewSettings, {
  viewClippingNotice,
  viewColumnRange,
  viewIsClipped,
} from "./ViewSettings.svelte";
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


beforeEach(() => {
  cleanup();
  globalThis.localStorage?.clear();
});
afterEach(cleanup);

// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// The clipping signal. This is the requirement the popover must not swallow: a
// pane clipped at column 55 looks exactly like an agent that stopped writing
// mid-line, so the fact has to survive OUTSIDE the popover as well as in it.

describe("ViewSettings clipping signal", () => {
  // THE EXPORTED RULE, which is what the header button spends. These sections
  // lost their own trigger when the terminal header collapsed two glyphs into
  // one; the guarantee did not move with them, it was factored OUT so that the
  // button and the readout below read the same rule. Terminal.test.ts asserts
  // the button actually spends it — that is the other half, and neither test is
  // sufficient alone.
  it("states the clipping as a sentence a button can wear", () => {
    expect(viewIsClipped(CLIPPED)).toBe(true);
    expect(viewColumnRange(CLIPPED)).toBe("44–86 of 211");
    expect(viewClippingNotice(CLIPPED)).toBe("Showing columns 44–86 of 211");
  });

  it("says nothing at all when the whole grid is on screen", () => {
    expect(viewIsClipped(WHOLE)).toBe(false);
    expect(viewClippingNotice(WHOLE)).toBe("");
  });

  it("says nothing before the pane has been measured", () => {
    // `cols: 0` is the state between mounting the terminal and the first frame.
    // "Showing columns 1–0 of 0" is worse than silence.
    const unmeasured = geometry({ cols: 0, rows: 0, shown: 0, shownRows: 0, first: 1, panning: true });
    expect(viewIsClipped(unmeasured)).toBe(false);
    expect(viewClippingNotice(unmeasured)).toBe("");
  });

  it("reports no columns at all — the readout section is gone", async () => {
    // The "Visible" section printed the range, the row count and a sentence
    // explaining that a phone pans over a grid it cannot shrink: three lines of
    // readout in a sheet whose other sections are controls. It was removed.
    //
    // Nothing about the CLIPPING moved with it. The fact has two always-visible
    // carriers — the header button's dot and accessible name, from the rule
    // above, and the pane's own position bar — and the readout was neither.
    mount({ geom: CLIPPED });
    expect(screen.queryByText(/Columns 44–86 of 211/)).toBeNull();
    expect(screen.queryByText(/44 rows/)).toBeNull();
    expect(screen.queryByText(/wider than the screen/)).toBeNull();
    expect(screen.queryByText("No grid yet.")).toBeNull();
  });
});

// ---------------------------------------------------------------------------

describe("ViewSettings fit action", () => {
  it("offers the size it would land on", async () => {
    mount({ geom: geometry({ cols: 211, rows: 44, shown: 43, first: 1, panning: true, canFit: true, fitSize: 8 }) });
    expect(screen.getByRole("button", { name: "Fit the width (8 pt)" })).toBeInTheDocument();
  });

  it("runs the fit and asks to be dismissed, so the result is visible", async () => {
    // The ONE control here that spends `ondone`. It changes what is on screen
    // behind the sheet, so leaving the sheet up hides the thing just asked for.
    // Everything else is adjusted and re-adjusted with the sheet open, which is
    // the whole reason A− / A+ are worth having in one.
    const onfit = vi.fn();
    const ondone = vi.fn();
    mount({
      onfit,
      ondone,
      geom: geometry({ cols: 211, rows: 44, shown: 43, first: 1, panning: true, canFit: true, fitSize: 8 }),
    });
    await fireEvent.click(screen.getByRole("button", { name: "Fit the width (8 pt)" }));
    expect(onfit).toHaveBeenCalledTimes(1);
    expect(ondone).toHaveBeenCalledTimes(1);
  });

  it("offers the way back while a fit is in effect", async () => {
    mount({
      font: 8,
      geom: geometry({ cols: 211, rows: 44, shown: 120, first: 1, panning: true, fitActive: true, fitSize: 8 }),
    });
    expect(screen.getByRole("button", { name: "Back to the reading size" })).toBeInTheDocument();
  });

  // At the 8-point floor with a grid still wider than the screen there is no
  // fit to offer. A sentence beats a dead button, which is the same rule the
  // header followed before this moved.
  it("draws no dead button at the floor, and says why", async () => {
    mount({ font: FONT_MIN, geom: CLIPPED });
    expect(screen.queryByRole("button", { name: /Fit the width/ })).toBeNull();
    expect(screen.getByText(/Already at the smallest text/)).toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------

describe("ViewSettings font controls", () => {
  it("emits an absolute size, one point at a time", async () => {
    const onfont = vi.fn();
    mount({ font: 12, onfont });
    await fireEvent.click(screen.getByRole("button", { name: "Larger text" }));
    expect(onfont).toHaveBeenLastCalledWith(13);
    await fireEvent.click(screen.getByRole("button", { name: "Smaller text" }));
    expect(onfont).toHaveBeenLastCalledWith(11);
  });

  it("shows the current size and announces it", async () => {
    mount({ font: 14 });
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
    const smaller = screen.getByRole("button", { name: "Smaller text" });
    expect(smaller).toBeDisabled();
    await fireEvent.click(smaller);
    expect(onfont).toHaveBeenLastCalledWith(FONT_MIN);
  });

  it("keeps the clamp: nothing above the ceiling", async () => {
    const onfont = vi.fn();
    mount({ font: FONT_MAX, onfont });
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
    expect(screen.queryByText(/Already at the smallest text/)).toBeNull();
  });

  it("gives both font controls a 44pt target", async () => {
    mount();
    for (const name of ["Smaller text", "Larger text"]) {
      expect(screen.getByRole("button", { name }).className).toContain("h-11!");
    }
  });

  it("refuses every control when the caller disables it", async () => {
    // There is no trigger left to disable — these sections lost theirs when the
    // terminal header collapsed two glyphs into one — so what `disabled` has to
    // reach is every control inside.
    mount({
      disabled: true,
      geom: geometry({ cols: 211, rows: 44, shown: 43, first: 1, panning: true, canFit: true, fitSize: 8 }),
    });
    for (const b of screen.getAllByRole("button")) expect(b).toBeDisabled();
    expect(screen.getByRole("switch")).toBeDisabled();
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
    await fireEvent.click(screen.getByRole("button", { name: "Larger text" }));
    await settle(FONT_SAVE_WAIT_MS);
    expect(loadFontSize()).toBe(13);
  });

  it("remembers a smaller one too, and the readout follows the terminal", async () => {
    saveFontSize(12);
    render(ViewSettingsHarness);
    await fireEvent.click(screen.getByRole("button", { name: "Smaller text" }));
    expect(screen.getByRole("status")).toHaveTextContent("11 pt");
    await settle(FONT_SAVE_WAIT_MS);
    expect(loadFontSize()).toBe(11);
  });

  it("opens at the remembered size rather than the default", async () => {
    saveFontSize(FONT_MAX);
    render(ViewSettingsHarness);
    expect(screen.getByRole("status")).toHaveTextContent(`${FONT_MAX} pt`);
    // And the ceiling is already in force, with no round trip needed to learn it.
    expect(screen.getByRole("button", { name: "Larger text" })).toBeDisabled();
  });
});

describe("ViewSettings is addressable", () => {
  it("mounts its controls with no open state and no tap", () => {
    // These sections had their own `open` prop bound to `nav.sheet === "view"`,
    // because the Simulator has no gesture API and a control only a tap can
    // reveal is a control no screenshot can show. That sheet is gone — they
    // moved into the terminal's session sheet when the header collapsed its two
    // glyphs into one — so the addressable name is `sheet=menu` and the
    // guarantee is Terminal's. What is left to pin here is that mounting is all
    // it takes.
    render(ViewSettings, {
      props: {
        font: 12,
        geom: geometry({ cols: 211, rows: 44, shown: 58, first: 44, panning: true }),
        onfont: () => {},
        onfit: () => {},
      },
    });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.getByRole("button", { name: "Larger text" })).toBeTruthy();
    expect(screen.getByRole("switch")).toBeTruthy();
  });
});

// ---------------------------------------------------------------------------
// THE SIZE PIN. It is the one control in this app that changes what somebody
// else sees, so the two things worth pinning about it are that it starts off
// and that its cost to the Mac is on the sheet rather than behind a tooltip.

describe("ViewSettings size pin", () => {
  it("draws a switch that is off unless the caller says otherwise", async () => {
    mount();
    const sw = screen.getByRole("switch", { name: "Resize the Mac's pane to fit this phone" });
    expect(sw).toHaveAttribute("aria-checked", "false");
    expect(sw).toHaveTextContent("Off");
  });

  it("reports the state with aria-checked once it is on", async () => {
    mount({ pinned: true });
    const sw = screen.getByRole("switch");
    expect(sw).toHaveAttribute("aria-checked", "true");
    expect(sw).toHaveTextContent("On");
  });

  it("asks for the opposite of what it currently is", async () => {
    const onpin = vi.fn();
    mount({ onpin });
    await fireEvent.click(screen.getByRole("switch"));
    expect(onpin).toHaveBeenLastCalledWith(true);

    cleanup();
    mount({ pinned: true, onpin });
    await fireEvent.click(screen.getByRole("switch"));
    expect(onpin).toHaveBeenLastCalledWith(false);
  });

  // The requirement in one test: the sheet has to say what this does to the
  // MAC, not only what it does here. It is now the ONLY caption left in the
  // sheet — the ones under the fit and the column readout went with the copy
  // trim — which is the point: everything else here is a local zoom and reads as
  // one, and this is the exact opposite and cannot be inferred from its label.
  //
  // Two clauses may not be lost however short it gets: that somebody else's
  // window narrows, and that it lets go on its own. The first is the cost; the
  // second is what stops a reader believing they have broken something.
  it("names the cost to the Mac in plain words, on the sheet", async () => {
    mount({ geom: CLIPPED });
    expect(screen.getByText(/Narrows the window on the Mac/i)).toBeInTheDocument();
    expect(screen.getByText(/Released when you leave/i)).toBeInTheDocument();
    // ...and the size, so the cost is a number rather than an adjective.
    expect(screen.getByText(/about 43 by 22/)).toBeInTheDocument();
  });

  it("claims no size before the pane has been measured", async () => {
    mount({ geom: geometry() });
    expect(screen.queryByText(/about/)).toBeNull();
  });

  it("is a 44pt target, and the caller can disable it with the rest", async () => {
    mount({ disabled: false });
    expect(screen.getByRole("switch").className).toContain("h-11!");
  });
});
