// When the app comes back, and who is allowed to say so.
//
// The plugin closes the socket on the way into the background — deliberately;
// an NWConnection has no background privileges, iOS suspends the queue, and on
// resume the connection still reports `.ready` until a write fails, so a socket
// that lies about being usable is worse than one that is honestly gone. The
// other half of that bargain is that the app reopens it on the way back in, and
// for a long time nothing did: the phone returned from standby with a closed
// connection, a banner blaming the network, and — once iOS had reclaimed the
// process — the pairing screen.
//
// TWO SIGNALS, AND ONLY ONE OF THEM IS AUTHORITATIVE.
//
//   1. The plugin's `appState` event. Emitted by the very object that tore the
//      socket down, from `UIApplication.willEnterForegroundNotification`, so
//      the two can never disagree about the order. This is the trigger of
//      record.
//   2. `document.visibilitychange`. Already proven in this WebView
//      (`bridge.ts`, `dynamictype.ts`) and free, but it ALSO fires for a share
//      sheet or a system dialog — moments the socket was never touched. It is
//      a belt-and-braces trigger only, and it is safe purely because the thing
//      it calls is idempotent and phase-gated.
//
// `@capacitor/app` would be the ordinary source for (1). It is not installed in
// this app and cannot be added here, which is the immediate reason the event
// lives on the transport plugin — but not the only one: the plugin is the
// component with the opinion, and routing the signal through a second package
// would put a third party between the teardown and the recovery.
//
// THIS MODULE DECIDES NOTHING. It calls back on every return to the foreground
// and lets the connection apply its own gates: an explicit disconnect, a
// connect already in flight, a missing stored key. That split is what keeps the
// policy in one testable place instead of spread across a lifecycle callback.

/** A listener handle, in either shape Capacitor has used. */
interface Handle {
  remove(): unknown;
}

interface AppStateCapablePlugin {
  addListener?(
    event: string,
    cb: (e: { active?: boolean }) => void,
  ): Promise<Handle> | Handle;
}

interface CapacitorGlobal {
  Plugins?: { LolaTransport?: AppStateCapablePlugin };
}

function plugin(): AppStateCapablePlugin | undefined {
  const cap = (globalThis as { Capacitor?: CapacitorGlobal }).Capacitor;
  const p = cap?.Plugins?.LolaTransport;
  return p && typeof p.addListener === "function" ? p : undefined;
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
    const p = plugin();
    if (!p?.addListener) return;
    try {
      const h = await p.addListener("appState", (e) => {
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
    const p = plugin();
    if (!p?.addListener) return;
    try {
      const h = await p.addListener("appState", (e) => {
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
