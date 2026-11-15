// When the app comes back, and who is allowed to say so.
//
// The plugin closes the socket on the way into the background (an NWConnection
// has no background privileges and would report `.ready` until a write fails),
// so the app must reopen it on the way back in.
//
// Two signals, one authoritative:
//   1. The plugin's `appState` event, emitted by the object that tore the
//      socket down, so the two cannot disagree about order. Trigger of record.
//   2. `document.visibilitychange`, which also fires for a share sheet or a
//      system dialog the socket never noticed. Belt and braces only; safe
//      because the callee is idempotent and phase-gated.
//
// This module decides nothing: it calls back on every return and lets the
// connection apply its own gates.

import { native } from "../native";

/** A listener handle, in either shape Capacitor has used. */
interface Handle {
  remove(): unknown;
}

/**
 * Call `onForeground` whenever the app returns to the foreground. Returns a
 * teardown; never throws.
 *
 * A build whose plugin predates the `appState` event registers a listener that
 * simply never fires, and `visibilitychange` carries the feature on its own.
 * That is the reason the second trigger is wired at all rather than being
 * dropped as redundant.
 */
export function installAppState(onForeground: () => void): () => void {
  let live = true;
  let handle: Handle | undefined;

  void (async () => {
    if (!native) return;
    try {
      const h = await native.addListener("appState", (e) => {
        if (!live) return;
        // Only the way IN. The way out needs nothing from the app: the plugin
        // has already closed the socket and said so on the `state` event.
        if (e?.active === true) onForeground();
      });
      if (!live) {
        try {
          void h.remove();
        } catch {
          /* a handle that will not detach is not worth failing a launch over */
        }
      } else {
        handle = h;
      }
    } catch {
      /* an older plugin has no such event; visibilitychange still covers it */
    }
  })();

  const onVisible = () => {
    if (!live) return;
    if (globalThis.document?.visibilityState === "visible") onForeground();
  };
  globalThis.document?.addEventListener("visibilitychange", onVisible);

  return () => {
    if (!live) return;
    live = false;
    globalThis.document?.removeEventListener("visibilitychange", onVisible);
    try {
      void handle?.remove();
    } catch {
      /* nothing useful to do on teardown */
    }
  };
}

/**
 * Call `onBackground` whenever the app leaves the foreground. Returns a
 * teardown; never throws.
 *
 * THE MIRROR OF THE ABOVE, AND IT EXISTS FOR EXACTLY ONE CALLER: the pane size
 * pin (see panepin.ts), which reshapes a window on somebody else's machine and
 * must hand it back on every way out — including the way out that is a phone
 * going into a pocket.
 *
 * `installAppState` deliberately ignores this direction, and the reason it gives
 * is still right for what it does: the plugin has already closed the socket by
 * then and a reconnect callback there would only race a teardown it cannot undo.
 * A RELEASE is the opposite case. It is not recovering from the teardown, it is
 * trying to get one last frame out ahead of it, and the plugin gives it the only
 * chance it has — `onAppState(false)` is announced synchronously from
 * `didEnterBackgroundNotification`, before the queued close runs.
 *
 * IT WILL SOMETIMES LOSE THAT RACE, and no amount of care here changes that: the
 * signal crosses the bridge into the WebView and the write has to cross back,
 * while the close is already queued. That is precisely why the pin also keeps a
 * persisted breadcrumb and sweeps it on the way back in. This is the cheap half
 * that usually works, not the guarantee.
 *
 * `visibilitychange` is wired for the same belt-and-braces reason as above and
 * with the same caveat: it also fires for a share sheet or a system dialog, so
 * this callback can be invoked for a moment the socket was never touched. It is
 * safe only because releasing a pin twice costs one no-op request, which is the
 * trade this whole feature is built on.
 */
export function installAppBackground(onBackground: () => void): () => void {
  let live = true;
  let handle: Handle | undefined;

  void (async () => {
    if (!native) return;
    try {
      const h = await native.addListener("appState", (e) => {
        if (!live) return;
        if (e?.active === false) onBackground();
      });
      if (!live) {
        try {
          void h.remove();
        } catch {
          /* a handle that will not detach is not worth failing a screen over */
        }
      } else {
        handle = h;
      }
    } catch {
      /* an older plugin has no such event; visibilitychange still covers it */
    }
  })();

  const onHidden = () => {
    if (!live) return;
    if (globalThis.document?.visibilityState !== "visible") onBackground();
  };
  globalThis.document?.addEventListener("visibilitychange", onHidden);

  return () => {
    if (!live) return;
    live = false;
    globalThis.document?.removeEventListener("visibilitychange", onHidden);
    try {
      void handle?.remove();
    } catch {
      /* nothing useful to do on teardown */
    }
  };
}
