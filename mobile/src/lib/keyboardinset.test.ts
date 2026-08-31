import { describe, expect, it, vi } from "vitest";

import { installKeyboardInset, type KeyboardHandle, type KeyboardLike, viewportInset } from "./keyboardinset";

// The defect these pin: the earlier version read `Capacitor.Plugins.Keyboard`
// without ever importing `@capacitor/keyboard`, so the global was never
// populated, no listener was ever installed, the inset stayed 0 and the
// accessory bar sat under the raised keyboard on every device. It is invisible
// in a desktop browser, which has no soft keyboard, so nothing short of a device
// would have found it — hence a seam and a test.

/** A fake plugin that records its listeners and can fire them. */
function fakeKeyboard(opts: { async?: boolean; failEvent?: string } = {}) {
  const listeners = new Map<string, (i: { keyboardHeight?: number }) => void>();
  const removed: string[] = [];
  let released: (() => void) | undefined;

  const kb: KeyboardLike = {
    addListener(event, cb) {
      if (opts.failEvent === event) throw new Error("not implemented on web");
      const handle: KeyboardHandle = {
        remove() {
          removed.push(event);
        },
      };
      listeners.set(event, cb);
      if (!opts.async) return handle;
      return new Promise<KeyboardHandle>((resolve) => {
        released = () => resolve(handle);
      });
    },
  };

  return {
    kb,
    listeners,
    removed,
    /** Resolve a pending addListener, for the async shape. */
    release: () => released?.(),
    fire: (event: string, height?: number) => listeners.get(event)?.({ keyboardHeight: height }),
  };
}

describe("the soft-keyboard inset", () => {
  it("installs listeners through the loader and reports the height", async () => {
    const f = fakeKeyboard();
    const seen: number[] = [];
    installKeyboardInset((px) => seen.push(px), async () => ({ keyboard: f.kb }));
    await vi.waitFor(() => expect(f.listeners.size).toBe(2));

    f.fire("keyboardWillShow", 336);
    f.fire("keyboardWillHide");
    expect(seen).toEqual([336, 0]);
  });

  it("treats a missing height as zero rather than NaN", async () => {
    const f = fakeKeyboard();
    const seen: number[] = [];
    installKeyboardInset((px) => seen.push(px), async () => ({ keyboard: f.kb }));
    await vi.waitFor(() => expect(f.listeners.size).toBe(2));

    f.fire("keyboardWillShow");
    expect(seen).toEqual([0]);
  });

  it("does nothing at all when there is no keyboard plugin", async () => {
    const seen: number[] = [];
    const off = installKeyboardInset((px) => seen.push(px), async () => ({}));
    await Promise.resolve();
    off();
    expect(seen).toEqual([]);
  });

  it("survives a loader that throws", async () => {
    const seen: number[] = [];
    const off = installKeyboardInset(
      (px) => seen.push(px),
      async () => {
        throw new Error("no bridge here");
      },
    );
    await Promise.resolve();
    off();
    expect(seen).toEqual([]);
  });

  it("removes its listeners on teardown and stops reporting", async () => {
    const f = fakeKeyboard();
    const seen: number[] = [];
    const off = installKeyboardInset((px) => seen.push(px), async () => ({ keyboard: f.kb }));
    await vi.waitFor(() => expect(f.listeners.size).toBe(2));

    off();
    expect(f.removed.sort()).toEqual(["keyboardWillHide", "keyboardWillShow"]);
    f.fire("keyboardWillShow", 300);
    expect(seen).toEqual([]);
  });

  it("removes a handle that lands after teardown", async () => {
    // A screen can be left inside the frame it opened, so the teardown routinely
    // runs before the dynamic import has resolved. A handle that arrives then
    // must be dropped, not leaked onto a component that is gone.
    const f = fakeKeyboard({ async: true });
    const seen: number[] = [];
    const off = installKeyboardInset((px) => seen.push(px), async () => ({ keyboard: f.kb }));
    await vi.waitFor(() => expect(f.listeners.size).toBe(1));

    off();
    f.release();
    await vi.waitFor(() => expect(f.removed).toEqual(["keyboardWillShow"]));
    f.fire("keyboardWillShow", 300);
    expect(seen).toEqual([]);
  });

  it("keeps the half it could install when one event is unsupported", async () => {
    const f = fakeKeyboard({ failEvent: "keyboardWillHide" });
    const seen: number[] = [];
    installKeyboardInset((px) => seen.push(px), async () => ({ keyboard: f.kb }));
    await vi.waitFor(() => expect(f.listeners.size).toBe(1));

    f.fire("keyboardWillShow", 291);
    expect(seen).toEqual([291]);
  });
});

describe("viewportInset", () => {
  it("is the overlap between the layout viewport and the visual one", () => {
    // KeyboardResize.None keeps the layout viewport at full height while the
    // visual viewport shrinks by exactly the keyboard's overlap.
    expect(viewportInset({ height: 442, offsetTop: 0 }, 874)).toBe(432);
  });

  it("discounts a visual viewport that has been scrolled up over the keyboard", () => {
    expect(viewportInset({ height: 442, offsetTop: 60 }, 874)).toBe(372);
  });

  it("is 0 with no keyboard, and for the rounding slack of a settled viewport", () => {
    expect(viewportInset({ height: 874, offsetTop: 0 }, 874)).toBe(0);
    expect(viewportInset({ height: 862, offsetTop: 0 }, 874)).toBe(0);
  });

  it("is 0 for anything it cannot measure, rather than NaN", () => {
    expect(viewportInset(undefined, 874)).toBe(0);
    expect(viewportInset({}, 874)).toBe(0);
    expect(viewportInset({ height: 442 }, 0)).toBe(0);
  });

  it("rejects a reading taller than the screen, which is a mid-rotation frame", () => {
    expect(viewportInset({ height: -50, offsetTop: 0 }, 874)).toBe(0);
  });
});

describe("installKeyboardInset with a visual viewport", () => {
  function fakeViewport(height: number) {
    const listeners: Record<string, (() => void)[]> = {};
    return {
      height,
      offsetTop: 0,
      addEventListener(t: string, cb: () => void) {
        (listeners[t] ??= []).push(cb);
      },
      removeEventListener(t: string, cb: () => void) {
        listeners[t] = (listeners[t] ?? []).filter((f) => f !== cb);
      },
      emit(t: string) {
        for (const cb of listeners[t] ?? []) cb();
      },
      count: () => Object.values(listeners).reduce((n, l) => n + l.length, 0),
    };
  }

  it("reports the keyboard with NO plugin at all — the device's failure mode", () => {
    // On the device the plugin path shipped silent: the bar never moved, and an
    // inset that is never reported is indistinguishable from an inset of zero.
    const vv = fakeViewport(874);
    (globalThis as Record<string, unknown>).visualViewport = vv;
    (globalThis as Record<string, unknown>).innerHeight = 874;
    const seen: number[] = [];
    const off = installKeyboardInset((px) => seen.push(px), async () => ({}));

    vv.height = 442;
    vv.emit("resize");
    expect(seen.at(-1)).toBe(432);

    vv.height = 874;
    vv.emit("resize");
    expect(seen.at(-1)).toBe(0);

    off();
    expect(vv.count()).toBe(0);
    delete (globalThis as Record<string, unknown>).visualViewport;
  });

  it("takes the LARGER of the two sources, so either one working is enough", async () => {
    const vv = fakeViewport(874);
    (globalThis as Record<string, unknown>).visualViewport = vv;
    (globalThis as Record<string, unknown>).innerHeight = 874;
    const seen: number[] = [];
    let show: ((i: { keyboardHeight: number }) => void) | undefined;
    const off = installKeyboardInset(
      (px) => seen.push(px),
      async () => ({
        keyboard: {
          addListener(event: string, cb: (i: { keyboardHeight?: number }) => void) {
            if (event === "keyboardWillShow") show = cb as typeof show;
            return { remove: () => undefined };
          },
        },
      }),
    );
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();

    // The plugin reports the resting height first; the viewport catches up over
    // the following frames and must not walk the inset back down.
    show?.({ keyboardHeight: 432 });
    expect(seen.at(-1)).toBe(432);
    vv.height = 700;
    vv.emit("resize");
    expect(seen.at(-1)).toBe(432);

    off();
    delete (globalThis as Record<string, unknown>).visualViewport;
  });
});

describe("the loader's box", () => {
  it("must not resolve with the plugin itself — a plugin proxy is a THENABLE", async () => {
    // THE BUG THIS PINS, and it is the reason the accessory bar sat under the
    // keyboard with nothing in any log. `registerPlugin` returns a Proxy whose
    // `get` manufactures a function for every property name, `then` included —
    // so `await` calls `proxy.then(resolve, reject)`, a bridge call to a native
    // method that does not exist and never calls back. The await hangs, forever,
    // silently. Boxing the plugin in a plain object is what removes the `then`
    // from the awaited value.
    const proxy = new Proxy(
      {},
      {
        get: (_t, prop) =>
          prop === "addListener"
            ? (_e: string, _cb: unknown) => ({ remove: () => undefined })
            : // Every other name, `then` included, answers with a function that
              // never calls back — exactly what Capacitor's proxy does.
              () => undefined,
      },
    ) as KeyboardLike;

    // Awaiting the bare proxy hangs; this is the trap, demonstrated.
    const hung = await Promise.race([
      (async () => proxy)().then(() => "settled"),
      new Promise((r) => setTimeout(() => r("hung"), 30)),
    ]);
    expect(hung).toBe("hung");

    // Boxed, it settles — and the listeners actually get installed.
    let bound = 0;
    const boxed = {
      keyboard: {
        addListener: (_e: string, _cb: (i: { keyboardHeight?: number }) => void) => {
          bound++;
          return { remove: () => undefined };
        },
      },
    };
    installKeyboardInset(() => {}, async () => boxed);
    await new Promise((r) => setTimeout(r, 10));
    expect(bound).toBe(2);
  });
});
