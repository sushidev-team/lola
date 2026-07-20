// The font race, pinned. xterm measures the cell EXACTLY ONCE, inside
// Terminal.open() (Terminal.ts:570 -> CharSizeService.measure()), and re-measures
// only on a fontFamily/fontSize change (CharSizeService.ts:34) or after a real
// resize (Terminal.ts:1214). A terminal opened while Hack is still loading is
// therefore stuck on the fallback face's cell forever — and the fallback is
// JetBrains Mono, already loaded for the app chrome, so it measures cleanly and
// nothing looks broken. It just never reaches the 8x17 cell TERM_FONT exists to
// produce. These tests assert the ordering that prevents that.
//
// xterm and the Wails bridge are mocked: this is about WHEN open() happens, and
// jsdom has no canvas/WebGL to open into.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render } from "@testing-library/svelte";
import { tick } from "svelte";
import LiveTerminal from "./LiveTerminal.svelte";
import { TERM_FONT } from "$lib/theme-runtime.svelte";

const gate = vi.hoisted(() => {
  type Deferred = { promise: Promise<boolean>; settle: (v: boolean) => void };
  const defer = (): Deferred => {
    let settle!: (v: boolean) => void;
    const promise = new Promise<boolean>((resolve) => (settle = resolve));
    return { promise, settle };
  };
  const state = { ready: defer(), loaded: defer() };
  return {
    state,
    reset() {
      state.ready = defer();
      state.loaded = defer();
    },
    readyFn: () => state.ready.promise,
    loadedFn: () => state.loaded.promise,
  };
});

const spies = vi.hoisted(() => {
  const familyWrites: string[] = [];
  const options = new Proxy({} as Record<string, unknown>, {
    set(target, key, value) {
      if (key === "fontFamily") familyWrites.push(String(value));
      target[key as string] = value;
      return true;
    },
  });
  return {
    familyWrites,
    options,
    ctor: vi.fn(),
    open: vi.fn(),
    fit: vi.fn(),
    refresh: vi.fn(),
    clearTextureAtlas: vi.fn(),
    attach: vi.fn(async () => {}),
    detach: vi.fn(async () => {}),
  };
});

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    public cols = 80;
    public rows = 24;
    public options = spies.options;
    public constructor(opts: unknown) {
      spies.ctor(opts);
    }
    public loadAddon = vi.fn();
    public open = spies.open;
    public onData = vi.fn();
    public onResize = vi.fn();
    public refresh = spies.refresh;
    public write = vi.fn();
    public writeln = vi.fn();
    public dispose = vi.fn();
  },
}));

vi.mock("@xterm/addon-fit", () => ({
  FitAddon: class {
    public fit = spies.fit;
    public activate = vi.fn();
    public dispose = vi.fn();
  },
}));

vi.mock("@xterm/addon-webgl", () => ({
  WebglAddon: class {
    public onContextLoss = vi.fn();
    public clearTextureAtlas = spies.clearTextureAtlas;
    public dispose = vi.fn();
  },
}));

vi.mock("@wailsio/runtime", () => ({
  Events: { On: vi.fn(() => () => {}) },
}));

vi.mock("@bindings/desktop", () => ({
  TermService: {
    Attach: spies.attach,
    Detach: spies.detach,
    Write: vi.fn(),
    Resize: vi.fn(),
  },
}));

vi.mock("$lib/theme-runtime.svelte", async (importActual) => {
  const actual = await importActual<typeof import("$lib/theme-runtime.svelte")>();
  return { ...actual, termFontReady: gate.readyFn, termFontLoaded: gate.loadedFn };
});

// jsdom has no ResizeObserver; the component installs one after open(). Only
// its construction matters here — resize behaviour is exercised in the app.
class StubResizeObserver {
  public observe(): void {}
  public unobserve(): void {}
  public disconnect(): void {}
}
globalThis.ResizeObserver ??= StubResizeObserver as unknown as typeof ResizeObserver;

/** Let the component's async boot() run as far as it can. */
async function settle(): Promise<void> {
  for (let i = 0; i < 6; i++) await tick();
}

describe("LiveTerminal font ordering", () => {
  beforeEach(() => {
    gate.reset();
    spies.familyWrites.length = 0;
    vi.clearAllMocks();
  });

  it("does not open the terminal until the font wait resolves", async () => {
    render(LiveTerminal, { props: { name: "s1", webgl: false, interactive: false } });
    await settle();
    // The whole point: no measurement has been taken yet.
    expect(spies.ctor).not.toHaveBeenCalled();
    expect(spies.open).not.toHaveBeenCalled();

    gate.state.ready.settle(true);
    await vi.waitFor(() => expect(spies.open).toHaveBeenCalledTimes(1));
    expect(spies.ctor).toHaveBeenCalledWith(expect.objectContaining(TERM_FONT));
    expect(spies.attach).toHaveBeenCalledWith("s1", 80, 24);
  });

  it("does not force a re-measure when the font was there before open()", async () => {
    render(LiveTerminal, { props: { name: "s2", webgl: true, interactive: false } });
    gate.state.ready.settle(true);
    await vi.waitFor(() => expect(spies.open).toHaveBeenCalledTimes(1));
    gate.state.loaded.settle(true);
    await settle();
    // One measurement, taken once, with the real font — nothing to invalidate.
    expect(spies.familyWrites).toEqual([]);
    expect(spies.clearTextureAtlas).not.toHaveBeenCalled();
    expect(spies.fit).toHaveBeenCalledTimes(1);
  });

  it("re-measures and re-fits when the font lands after the wait gave up", async () => {
    render(LiveTerminal, { props: { name: "s3", webgl: true, interactive: false } });
    gate.state.ready.settle(false); // bounded wait expired; opened on fallback metrics
    await vi.waitFor(() => expect(spies.open).toHaveBeenCalledTimes(1));
    expect(spies.familyWrites).toEqual([]);

    gate.state.loaded.settle(true); // Hack arrives late
    await vi.waitFor(() => expect(spies.clearTextureAtlas).toHaveBeenCalledTimes(1));
    // Off and straight back: only a real change fires the option event
    // (OptionsService.ts:132), so the second write is what re-measures.
    expect(spies.familyWrites).toEqual(["monospace", TERM_FONT.fontFamily]);
    expect(spies.fit).toHaveBeenCalledTimes(2); // initial + post-measure
    expect(spies.refresh).toHaveBeenCalled();
  });

  it("does not re-measure when the font never loads at all", async () => {
    render(LiveTerminal, { props: { name: "s4", webgl: true, interactive: false } });
    gate.state.ready.settle(false);
    await vi.waitFor(() => expect(spies.open).toHaveBeenCalledTimes(1));

    gate.state.loaded.settle(false);
    await settle();
    // A fallback cell beats a terminal that rebuilds itself for nothing.
    expect(spies.familyWrites).toEqual([]);
    expect(spies.clearTextureAtlas).not.toHaveBeenCalled();
  });

  it("never attaches when unmounted during the font wait", async () => {
    const { unmount } = render(LiveTerminal, {
      props: { name: "s5", webgl: false, interactive: false },
    });
    unmount();
    gate.state.ready.settle(true);
    await settle();
    expect(spies.open).not.toHaveBeenCalled();
    expect(spies.attach).not.toHaveBeenCalled();
  });
});
