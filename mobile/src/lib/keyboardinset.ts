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

export type KeyboardLoader = () => Promise<KeyboardLike | undefined>;

interface CapacitorGlobal {
  Plugins?: { Keyboard?: KeyboardLike };
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
    if (mod?.Keyboard && typeof mod.Keyboard.addListener === "function") return mod.Keyboard;
  } catch {
    // No package, or a bundler that could not resolve it. Try the global.
  }
  const cap = (globalThis as { Capacitor?: CapacitorGlobal }).Capacitor;
  const kb = cap?.Plugins?.Keyboard;
  return typeof kb?.addListener === "function" ? kb : undefined;
};

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
      kb = await load();
    } catch {
      return; // a loader that throws is the same as no keyboard
    }
    if (!kb || !live) return;

    const bind = async (event: string, height: (i: KeyboardInfo) => number) => {
      if (!live) return; // torn down while an earlier bind was in flight
      try {
        const h = await kb!.addListener(event, (i) => {
          if (live) onInset(height(i));
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
    while (handles.length > 0) drop(handles.pop()!);
  };
}
