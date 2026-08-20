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
    attachKey: vi.fn(),
    attachWheel: vi.fn(),
    clearTextureAtlas: vi.fn(),
    attach: vi.fn(async () => {}),
    write: vi.fn(async () => {}),
    detach: vi.fn(async () => {}),
    scroll: vi.fn(async () => {}),
    openURL: vi.fn(async () => {}),
    webLinksHandler: undefined as undefined | ((e: MouseEvent, uri: string) => void),
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
    public focus = vi.fn();
    // Captured so the Ctrl-Q escape-hatch tests can drive it directly.
    public attachCustomKeyEventHandler = spies.attachKey;
    // Same for the wheel handler the scroll tests drive.
    public attachCustomWheelEventHandler = spies.attachWheel;
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

vi.mock("@xterm/addon-web-links", () => ({
  // Captures the click handler the component passes in, so the test can fire it.
  WebLinksAddon: class {
    public constructor(handler: (e: MouseEvent, uri: string) => void) {
      spies.webLinksHandler = handler;
    }
    public activate = vi.fn();
    public dispose = vi.fn();
  },
}));

vi.mock("$lib/store.svelte", () => ({ store: { openURL: spies.openURL } }));

vi.mock("@wailsio/runtime", () => ({
  Events: { On: vi.fn(() => () => {}) },
}));

vi.mock("@bindings/desktop", () => ({
  TermService: {
    Attach: spies.attach,
    Detach: spies.detach,
    Write: spies.write,
    Resize: vi.fn(),
    Scroll: spies.scroll,
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

  // A focused terminal forwards EVERY key to tmux, Escape included (agents use
  // it), so Ctrl-Q is the only keyboard route back to the cockpit. The handler is
  // registered unconditionally and gated on the prop inside, because `focused`
  // flips without remounting this component.
  describe("Ctrl-Q escape hatch", () => {
    /** Boot a terminal and hand back the key handler it registered. */
    async function bootWith(onEscapeFocus?: () => void) {
      render(LiveTerminal, { props: { name: "s9", webgl: false, interactive: true, onEscapeFocus } });
      gate.state.ready.settle(true);
      await vi.waitFor(() => expect(spies.open).toHaveBeenCalledTimes(1));
      return spies.attachKey.mock.calls[0][0] as (e: KeyboardEvent) => boolean;
    }

    const ctrlQ = () =>
      ({ type: "keydown", ctrlKey: true, metaKey: false, altKey: false, key: "q", preventDefault: vi.fn() }) as unknown as KeyboardEvent;

    it("swallows Ctrl-Q and calls back instead of forwarding it to tmux", async () => {
      const onEscapeFocus = vi.fn();
      const handler = await bootWith(onEscapeFocus);
      expect(handler(ctrlQ())).toBe(false);
      expect(onEscapeFocus).toHaveBeenCalledOnce();
    });

    it("forwards Ctrl-Q normally when there is no focus to leave", async () => {
      const handler = await bootWith(undefined);
      expect(handler(ctrlQ())).toBe(true);
    });

    it("forwards every other key, including a bare Escape the agent needs", async () => {
      const onEscapeFocus = vi.fn();
      const handler = await bootWith(onEscapeFocus);
      const esc = { type: "keydown", ctrlKey: false, metaKey: false, altKey: false, key: "Escape", preventDefault: vi.fn() };
      expect(handler(esc as unknown as KeyboardEvent)).toBe(true);
      // ⌘Q is macOS "quit app" — never our chord.
      const cmdQ = { type: "keydown", ctrlKey: false, metaKey: true, altKey: false, key: "q", preventDefault: vi.fn() };
      expect(handler(cmdQ as unknown as KeyboardEvent)).toBe(true);
      expect(onEscapeFocus).not.toHaveBeenCalled();
    });
  });

  // xterm sends a bare CR for Enter whether or not shift is held (Keyboard.ts
  // consults only alt), so shift+enter reached the agent as "send this message"
  // and cut it off mid-sentence. The byte pair an agent inserts a newline for is
  // ESC CR — meta+enter, which alt+enter already produces.
  describe("shift+enter line break", () => {
    /** Boot a terminal and hand back the key handler it registered. */
    async function bootKeys(name = "s16", interactive = true) {
      render(LiveTerminal, { props: { name, webgl: false, interactive } });
      gate.state.ready.settle(true);
      await vi.waitFor(() => expect(spies.open).toHaveBeenCalledTimes(1));
      return spies.attachKey.mock.calls[0][0] as (e: KeyboardEvent) => boolean;
    }

    const enter = (mods: Partial<KeyboardEvent> = {}) =>
      ({
        type: "keydown",
        key: "Enter",
        shiftKey: false,
        ctrlKey: false,
        metaKey: false,
        altKey: false,
        preventDefault: vi.fn(),
        ...mods,
      }) as unknown as KeyboardEvent;

    it("writes ESC CR and swallows the key so no bare CR follows", async () => {
      const handler = await bootKeys();
      expect(handler(enter({ shiftKey: true }))).toBe(false);
      expect(spies.write).toHaveBeenCalledWith("s16", "\x1b\r");
    });

    it("leaves a plain Enter alone", async () => {
      const handler = await bootKeys("s17");
      expect(handler(enter())).toBe(true);
      expect(spies.write).not.toHaveBeenCalled();
    });

    // ⌥⇧Enter and ⌘⇧Enter are somebody else's chords; xterm already encodes the
    // alt one as ESC CR on its own.
    it("leaves shift+enter with another modifier alone", async () => {
      const handler = await bootKeys("s18");
      expect(handler(enter({ shiftKey: true, altKey: true }))).toBe(true);
      expect(handler(enter({ shiftKey: true, metaKey: true }))).toBe(true);
      expect(spies.write).not.toHaveBeenCalled();
    });

    // A read-only tile has no PTY to write into.
    it("does not write into a non-interactive terminal", async () => {
      const handler = await bootKeys("s19", false);
      expect(handler(enter({ shiftKey: true }))).toBe(true);
      expect(spies.write).not.toHaveBeenCalled();
    });
  });

  // A URL printed by a dev server is the terminal's most-clicked text. Both link
  // kinds must reach the DAEMON's opener, never window.open — this is a
  // WKWebView, so a new window would open inside the app, and the daemon is
  // where the http(s)-only guard lives.
  describe("link opening", () => {
    async function boot() {
      render(LiveTerminal, { props: { name: "s10", webgl: false, interactive: true } });
      gate.state.ready.settle(true);
      await vi.waitFor(() => expect(spies.open).toHaveBeenCalledTimes(1));
    }

    it("opens a plain URL through the daemon", async () => {
      await boot();
      spies.webLinksHandler?.(new MouseEvent("click"), "http://127.0.0.1:8000");
      expect(spies.openURL).toHaveBeenCalledWith("http://127.0.0.1:8000");
    });

    it("opens an OSC 8 hyperlink through the daemon", async () => {
      await boot();
      const activate = (spies.options.linkHandler as { activate: (e: MouseEvent, uri: string) => void }).activate;
      activate(new MouseEvent("click"), "https://github.com/acme/widgets/pull/7");
      expect(spies.openURL).toHaveBeenCalledWith("https://github.com/acme/widgets/pull/7");
    });
  });

  // `tmux attach` runs on the ALTERNATE screen, so xterm has no scrollback of
  // its own here and its built-in fallback turns the wheel into cursor keys —
  // which walks the agent's input history instead of scrolling. The pane's real
  // history lives in tmux, so the wheel is forwarded to TermService.Scroll and
  // xterm is told to keep its hands off.
  describe("wheel scrolling", () => {
    /** Boot a terminal and hand back the wheel handler it registered. */
    async function bootWheel(name = "s11") {
      render(LiveTerminal, { props: { name, webgl: false, interactive: true } });
      gate.state.ready.settle(true);
      await vi.waitFor(() => expect(spies.open).toHaveBeenCalledTimes(1));
      return spies.attachWheel.mock.calls[0][0] as (e: WheelEvent) => boolean;
    }

    // jsdom reports clientHeight 0, so the component falls back to a 17px cell —
    // the same number TERM_FONT lands on. Three cells' worth of pixels.
    const wheel = (deltaY: number, deltaMode = 0) => ({ deltaY, deltaMode }) as WheelEvent;

    it("scrolls the tmux pane back and refuses xterm's arrow-key fallback", async () => {
      const handler = await bootWheel();
      expect(handler(wheel(-51))).toBe(false); // wheel up = back into history
      await vi.waitFor(() => expect(spies.scroll).toHaveBeenCalledWith("s11", 3));
    });

    it("scrolls forward again on a downward wheel", async () => {
      const handler = await bootWheel("s12");
      expect(handler(wheel(51))).toBe(false);
      await vi.waitFor(() => expect(spies.scroll).toHaveBeenCalledWith("s12", -3));
    });

    it("coalesces a burst into one tmux call", async () => {
      const handler = await bootWheel("s13");
      for (let i = 0; i < 4; i++) handler(wheel(-51));
      await vi.waitFor(() => expect(spies.scroll).toHaveBeenCalledTimes(1));
      expect(spies.scroll).toHaveBeenCalledWith("s13", 12);
    });

    // A trackpad delivers a few pixels at a time; rounding each event to zero
    // would make slow scrolling do nothing at all.
    it("accumulates sub-line deltas instead of dropping them", async () => {
      const handler = await bootWheel("s14");
      for (let i = 0; i < 6; i++) handler(wheel(-3));
      await vi.waitFor(() => expect(spies.scroll).toHaveBeenCalledWith("s14", 1));
    });

    it("reads line and page deltas in the cell's units", async () => {
      const handler = await bootWheel("s15");
      handler(wheel(-2, 1)); // DOM_DELTA_LINE
      await vi.waitFor(() => expect(spies.scroll).toHaveBeenCalledWith("s15", 2));
      handler(wheel(-1, 2)); // DOM_DELTA_PAGE — one screen of 24 rows
      await vi.waitFor(() => expect(spies.scroll).toHaveBeenCalledWith("s15", 24));
    });
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
