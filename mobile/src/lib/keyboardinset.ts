// Paying back the soft keyboard's height, and the one import that has to be
// dynamic.
//
// WHY THE APP DOES THIS AT ALL. capacitor.config.ts sets
// `Keyboard.resize: KeyboardResize.None`. Letting the plugin resize the WebView
// would fight the terminal's own measurement — xterm re-measures on every
// container change, the resize changes the container, and the two chase each
// other into a storm for as long as the keyboard is animating. So the app takes
// the height itself and pays it back as bottom padding on the screen container.
// Without that, the accessory bar — Escape, Ctrl-C, Shift-Tab, the arrows,
// Shift+Enter, which is the entire reason this app exists rather than a web page
// — sits underneath the raised keyboard.
//
// WHY THE IMPORT IS DYNAMIC RATHER THAN A GLOBAL PROBE. `Capacitor.Plugins.X` is
// populated by a `registerPlugin("X")` call in JavaScript, not by the native
// side and not by capacitor.config.ts (which is read by the Capacitor CLI and is
// not part of the app bundle at all). Reading the global without importing the
// package therefore finds nothing, on a device as much as in a browser, and the
// failure is silent: the inset stays 0 and the bar stays hidden. Importing
// `@capacitor/keyboard` is what runs that registration.
//
// It is dynamic rather than a top-level import so that a `npm run dev` session
// in a desktop browser — which has no bridge — pays nothing at module load and
// degrades to "there is no keyboard plugin here", which is the truth.

/** What the plugin reports. Only the height matters to us. */
export interface KeyboardInfo {
  keyboardHeight?: number;
}

/** A listener handle, in either of the shapes Capacitor has used. */
export interface KeyboardHandle {
  remove(): unknown;
}

/** The slice of `@capacitor/keyboard` this module needs. */
export interface KeyboardLike {
  addListener(
    event: string,
    cb: (info: KeyboardInfo) => void,
  ): Promise<KeyboardHandle> | KeyboardHandle;
}

/**
 * A BOX, and the box is the whole fix. Never a bare `KeyboardLike`.
 *
 * `Capacitor.registerPlugin` returns a Proxy whose `get` handler MANUFACTURES A
 * FUNCTION FOR EVERY PROPERTY NAME — the same trap `secretstore.ts` documents
 * for capability probing, arriving here through a different door. `then` is a
 * property name, so the proxy is a THENABLE, and
 *
 *     const kb = await load();      // load() resolves with the proxy
 *
 * makes the runtime call `proxy.then(resolve, reject)` — a bridge call to a
 * native method named "then" that does not exist and never calls back. The
 * await never settles. No error, no rejection, no log: the promise simply hangs
 * and every line after it is dead.
 *
 * That is exactly what shipped. The keyboard listeners were never bound, the
 * inset stayed 0, and the accessory bar — Escape, Ctrl-C, the arrows,
 * Shift+Enter, the entire reason this app is not a web page — sat underneath
 * the raised keyboard, on a code path that logs nothing because nothing failed.
 * Instrumenting it on the device is what found it: "loadKeyboard: imported,
 * Keyboard=true addListener=function" printed, and the line after the await
 * never did.
 *
 * Wrapping the plugin in a plain object removes the `then` from the value being
 * awaited. Anything that resolves a Capacitor plugin through a promise needs
 * this; returning one directly is a hang, not a type error.
 */
export interface KeyboardBox {
  keyboard?: KeyboardLike;
}

export type KeyboardLoader = () => Promise<KeyboardBox>;

interface CapacitorGlobal {
  Plugins?: { Keyboard?: KeyboardLike };
}

/** The slice of `window.visualViewport` this module reads. */
export interface VisualViewportLike {
  height?: number;
  offsetTop?: number;
  addEventListener?(type: string, cb: () => void): void;
  removeEventListener?(type: string, cb: () => void): void;
}

/**
 * The real loader: import the package, and fall back to the global in case some
 * other part of the shell registered the plugin first.
 *
 * Never throws. A shell without the package, or a browser where the import
 * resolves but the plugin has no web implementation, both come back as "no
 * keyboard", which is exactly right.
 */
export const loadKeyboard: KeyboardLoader = async () => {
  try {
    const mod = (await import("@capacitor/keyboard")) as { Keyboard?: KeyboardLike };
    // BOXED, not returned bare. Returning `mod.Keyboard` from an async function
    // resolves the promise WITH a thenable and hangs it forever. See KeyboardBox.
    if (mod?.Keyboard && typeof mod.Keyboard.addListener === "function") {
      return { keyboard: mod.Keyboard };
    }
  } catch {
    // No package, or a bundler that could not resolve it. Try the global.
  }
  const cap = (globalThis as { Capacitor?: CapacitorGlobal }).Capacitor;
  const kb = cap?.Plugins?.Keyboard;
  return { keyboard: typeof kb?.addListener === "function" ? kb : undefined };
};

/**
 * The keyboard's overlap read off the VISUAL VIEWPORT, with no plugin involved.
 *
 * WHY THERE ARE TWO SOURCES. The plugin path above is the documented one and it
 * is the one that reports a height DURING the keyboard's animation. It is also
 * the one that shipped silent: on the device the bar stayed exactly where it
 * was — the terminal content was pixel-identical with the keyboard up and down,
 * so `keyboardWillShow` never delivered — and the failure is invisible, because
 * an inset that is never reported and an inset of zero are the same number.
 *
 * `window.visualViewport` is the platform's own answer to the same question and
 * needs nothing installed: with `KeyboardResize.None` the layout viewport keeps
 * its full height while the visual viewport shrinks by exactly the keyboard's
 * overlap. It is a property of WebKit rather than of a Capacitor plugin
 * version, so the two fail independently, which is the point of having both.
 *
 * `offsetTop` is subtracted because a visual viewport that has been scrolled or
 * pinched is offset from the layout viewport, and only the part of the
 * difference that is BELOW the visible area is keyboard.
 *
 * MEASURED, NOT ASSUMED: on iOS 26 in the Simulator, with `contentInset: never`
 * and `scrollEnabled: false`, the visual viewport does NOT shrink for the
 * keyboard — `visualViewport.height` stayed at 912 with the keyboard up. So the
 * plugin is the source that carries the inset today and this is the backstop
 * rather than the other way round. It is kept because the two fail
 * independently and this one needs nothing installed.
 */
export function viewportInset(vv: {
  height?: number;
  offsetTop?: number;
} | undefined, layoutHeight: number): number {
  if (!vv || typeof vv.height !== "number" || !(layoutHeight > 0)) return 0;
  const overlap = layoutHeight - vv.height - (vv.offsetTop ?? 0);
  // A few pixels of difference is rounding between CSS and device pixels, or a
  // browser chrome inset, not a keyboard. Anything taller than the screen is a
  // reading taken mid-rotation.
  if (!(overlap > 24) || overlap > layoutHeight) return 0;
  return Math.round(overlap);
}

/**
 * Report the soft keyboard's height as it shows and hides. Returns a teardown.
 *
 * `willShow`/`willHide` rather than `didShow`/`didHide`: the padding has to
 * change WITH the keyboard's animation, or the bar visibly jumps into place
 * after it has finished moving.
 *
 * The teardown is safe to call before the import resolves — the commonest case
 * in practice, because a screen can be left within the same frame it opened. A
 * teardown that arrives first cancels the install and removes any handle that
 * lands afterwards, and `onInset` is never called after it.
 */
export function installKeyboardInset(
  onInset: (px: number) => void,
  load: KeyboardLoader = loadKeyboard,
): () => void {
  let live = true;
  const handles: KeyboardHandle[] = [];

  // THE TWO SOURCES ARE COMBINED BY MAX, NOT BY PRECEDENCE.
  //
  // Whichever of them is working reports the real overlap and the other reports
  // 0, so the larger is always the truth. A precedence rule would have to name
  // which source is trusted, and the whole reason there are two is that the
  // answer differs per device and per plugin version. Taking the max also gets
  // the animation right for free: the plugin reports the final height on
  // `willShow` while the visual viewport catches up over the next few frames,
  // and the bar goes to its resting place immediately rather than sliding.
  let fromPlugin = 0;
  let fromViewport = 0;
  let last = -1;
  const publish = () => {
    const px = Math.max(fromPlugin, fromViewport);
    if (px === last) return;
    last = px;
    onInset(px);
  };

  const vv = (globalThis as { visualViewport?: VisualViewportLike }).visualViewport;
  const onViewport = () => {
    if (!live) return;
    fromViewport = viewportInset(vv, (globalThis as { innerHeight?: number }).innerHeight ?? 0);
    publish();
  };
  if (vv && typeof vv.addEventListener === "function") {
    vv.addEventListener("resize", onViewport);
    // `scroll` as well: iOS moves the visual viewport rather than resizing it
    // when a focused field is scrolled into view above the keyboard.
    vv.addEventListener("scroll", onViewport);
    onViewport();
  }

  const drop = (h: KeyboardHandle) => {
    try {
      void h.remove();
    } catch {
      /* teardown is best effort; a handle that cannot be removed is not fatal */
    }
  };

  void (async () => {
    let kb: KeyboardLike | undefined;
    try {
      kb = (await load()).keyboard;
    } catch {
      return; // a loader that throws is the same as no keyboard
    }
    if (!kb || !live) return;

    const bind = async (event: string, height: (i: KeyboardInfo) => number) => {
      if (!live) return; // torn down while an earlier bind was in flight
      try {
        const h = await kb!.addListener(event, (i) => {
          if (!live) return;
          fromPlugin = height(i);
          publish();
        });
        if (!live) drop(h);
        else handles.push(h);
      } catch {
        // A plugin present but not implemented on this platform (a browser dev
        // session is the normal case). Nothing to install, nothing to report.
      }
    };

    await bind("keyboardWillShow", (i) => Math.max(0, i.keyboardHeight ?? 0));
    await bind("keyboardWillHide", () => 0);
  })();

  return () => {
    if (!live) return;
    live = false;
    if (vv && typeof vv.removeEventListener === "function") {
      vv.removeEventListener("resize", onViewport);
      vv.removeEventListener("scroll", onViewport);
    }
    while (handles.length > 0) drop(handles.pop()!);
  };
}
