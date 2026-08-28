// The shim's error vocabulary.
//
// One rule governs this file, and it is the whole reason it exists: a call the
// shim cannot perform must REJECT with a named error, never resolve. The shared
// components run every action through `store.act()`, which flashes a rejection
// and swallows nothing — so a rejection surfaces as "daemon control is not
// available on mobile" in the UI, while a silent `Promise.resolve()` would
// flash "daemon started" having started nothing. A silent `undefined` is worse
// still: `await ConfigService.Themes()` returning undefined from a missing
// export reads as an empty theme list, which is a bug report about theming.

/**
 * A Wails capability with no mobile equivalent.
 *
 * The four families, and why each one cannot work here:
 *
 *   - Window and menu management. There is no window; the app is one WebView
 *     filling the device, and the native menu bar does not exist.
 *   - Local process control (`StartDaemon`, `StopDaemon`, `RestartDaemon`,
 *     `InstallCLI`, `CLIInfo`). The daemon runs on a Mac somewhere else on the
 *     network. A phone cannot spawn it, and there is no PATH to install a CLI
 *     onto. Restarting it remotely is a real feature request, not this shim's
 *     job to fake — it needs a daemon-side command that does not exist.
 *   - The updater. A Capacitor app is updated through TestFlight or the App
 *     Store; there is no DMG to mount and no bundle to swap.
 *   - The native folder picker (`ConfigService.PickFolder`). It opens an
 *     NSOpenPanel over the Mac's filesystem. A phone cannot browse a checkout
 *     that is not on it, and answering with a typed path would silently
 *     configure a project pointing at nothing.
 */
export class UnsupportedOnMobileError extends Error {
  /** The service method that was called, e.g. "DaemonService.StartDaemon". */
  readonly method: string;
  /** Why it cannot work, in a sentence a person can act on. */
  readonly reason: string;

  constructor(method: string, reason: string) {
    super(`${method} is not available on mobile: ${reason}`);
    this.name = "UnsupportedOnMobileError";
    this.method = method;
    this.reason = reason;
  }
}

/**
 * A call arrived before a transport was installed, or after the connection was
 * torn down.
 *
 * Distinct from `UnsupportedOnMobileError` on purpose: this one is TEMPORARY.
 * The method is perfectly supported, there is simply nothing to send it over
 * right now — the app has not paired yet, the socket dropped, or the OS
 * suspended the process and the native side tore the connection down. A view
 * showing "not supported" for that is wrong, and it is exactly the state the
 * connect screen exists to explain.
 */
export class ShimNotConnectedError extends Error {
  readonly method: string;

  constructor(method: string) {
    super(`${method}: not connected to a daemon`);
    this.name = "ShimNotConnectedError";
    this.method = method;
  }
}

/** Reject, named. The one-liner every platform method in the shim returns. */
export function unsupported(method: string, reason: string): Promise<never> {
  return Promise.reject(new UnsupportedOnMobileError(method, reason));
}
