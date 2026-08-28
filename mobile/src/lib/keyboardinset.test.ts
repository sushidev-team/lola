import { describe, expect, it, vi } from "vitest";

import { installKeyboardInset, type KeyboardHandle, type KeyboardLike } from "./keyboardinset";

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
    installKeyboardInset((px) => seen.push(px), async () => f.kb);
    await vi.waitFor(() => expect(f.listeners.size).toBe(2));

    f.fire("keyboardWillShow", 336);
    f.fire("keyboardWillHide");
    expect(seen).toEqual([336, 0]);
  });

  it("treats a missing height as zero rather than NaN", async () => {
    const f = fakeKeyboard();
    const seen: number[] = [];
    installKeyboardInset((px) => seen.push(px), async () => f.kb);
    await vi.waitFor(() => expect(f.listeners.size).toBe(2));

    f.fire("keyboardWillShow");
    expect(seen).toEqual([0]);
  });

  it("does nothing at all when there is no keyboard plugin", async () => {
    const seen: number[] = [];
    const off = installKeyboardInset((px) => seen.push(px), async () => undefined);
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
    const off = installKeyboardInset((px) => seen.push(px), async () => f.kb);
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
    const off = installKeyboardInset((px) => seen.push(px), async () => f.kb);
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
    installKeyboardInset((px) => seen.push(px), async () => f.kb);
    await vi.waitFor(() => expect(f.listeners.size).toBe(1));

    f.fire("keyboardWillShow", 291);
    expect(seen).toEqual([291]);
  });
});
