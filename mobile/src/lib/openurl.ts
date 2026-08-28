// Opening a link the terminal printed — on the PHONE, and only if it is http(s).
//
// TWO HALVES, and only one of them carries over from the desktop.
//
// The GUARD carries over unchanged: terminal text is untrusted. A log line can
// print `file://`, `javascript:` or `data:`, an agent can be induced to print
// one, and the string reaches this function with no provenance at all. So the
// scheme is checked against a two-entry allowlist before anything happens, and
// anything else is refused silently rather than "handled".
//
// The EXEC HOST does not carry over, and this is the whole reason the module
// exists rather than reusing `store.openURL`. That desktop call reaches the
// daemon's `cmd=openURL`, which runs `open` on the DAEMON's machine — so a tap
// on the phone would launch Safari on an unattended Mac in another room. That is
// both the wrong behaviour and a small remote-nuisance primitive, so the phone
// opens locally instead. PLAN.md's "Terminals on a phone" section states this
// explicitly and it is the one place the mobile client deliberately diverges
// from the shared component's behaviour.
//
// `window.open` is not the answer either: inside a WKWebView it opens the page
// INSIDE the app, which is not a browser and has no way back. So the opener is,
// in order of preference:
//
//   1. Capacitor's Browser plugin. It is a real dependency of this project, and
//      it is reached through a DYNAMIC IMPORT rather than through
//      `Capacitor.Plugins.Browser`: that global is populated by a
//      `registerPlugin("Browser")` call in JavaScript, so a probe that never
//      imports the package finds nothing on a device as surely as in a browser.
//      On iOS it presents an SFSafariViewController — a real browser chrome with
//      a Done button back to the app, which is exactly the "way back" the plain
//      window opener does not have.
//   2. `window.open(url, "_system")`. Capacitor's WKUIDelegate treats a
//      navigation with no target frame as a hand-off to the system browser.
//   3. `window.open(url, "_blank")`, which is what a plain `npm run dev` in a
//      desktop browser needs, and a second chance at (2) on a shell that
//      recognises only the standard target.
//
// Every step is tried in order and a failure falls through to the next, so a
// missing plugin or a blocked popup costs a link, never an exception.

/** The only two schemes a terminal-printed URL may carry. */
const ALLOWED = ["http:", "https:"];

/**
 * Whether a string is a URL this app is willing to hand to an opener.
 *
 * Pure and exported so the guard itself is testable without a browser, and so
 * the terminal can decide whether to render a link as clickable at all.
 */
export function isOpenable(url: string): boolean {
  if (typeof url !== "string" || url === "") return false;
  try {
    return ALLOWED.includes(new URL(url).protocol);
  } catch {
    return false; // not a URL at all
  }
}

/** The shape this module needs from a Capacitor Browser plugin, if one exists. */
interface Browserish {
  open(options: { url: string }): Promise<unknown>;
}

interface CapacitorGlobal {
  Plugins?: { Browser?: Browserish };
}

/** Exposed for tests: the loader that finds a browser plugin, or does not. */
export type BrowserLoader = () => Promise<Browserish | undefined>;

/**
 * Import `@capacitor/browser`, falling back to the global for a shell that
 * registered the plugin some other way. Never throws.
 *
 * The result is cached, including the negative: this runs on a link tap, and a
 * dynamic import that is going to fail should fail once rather than on every
 * tap.
 */
let cached: Promise<Browserish | undefined> | undefined;

export const loadBrowser: BrowserLoader = () => {
  cached ??= (async () => {
    try {
      const mod = (await import("@capacitor/browser")) as { Browser?: Browserish };
      if (typeof mod?.Browser?.open === "function") return mod.Browser;
    } catch {
      // Not installed, or a bundler that could not resolve it.
    }
    const cap = (globalThis as { Capacitor?: CapacitorGlobal }).Capacitor;
    const b = cap?.Plugins?.Browser;
    return typeof b?.open === "function" ? b : undefined;
  })();
  return cached;
};

/** Test seam: forget a cached loader result. */
export function resetBrowserCache(): void {
  cached = undefined;
}

/**
 * Open a URL outside the app, or do nothing.
 *
 * Never throws and never reports failure to the caller: this is invoked from
 * xterm's link handler, where the alternative to silence is an unhandled
 * rejection from a click on a log line.
 */
export async function openExternal(url: string, load: BrowserLoader = loadBrowser): Promise<void> {
  if (!isOpenable(url)) return;

  let browser: Browserish | undefined;
  try {
    browser = await load();
  } catch {
    browser = undefined;
  }
  if (browser) {
    try {
      await browser.open({ url });
      return;
    } catch {
      // fall through to the window opener
    }
  }

  const w = globalThis as { open?: (u: string, t?: string, f?: string) => unknown };
  if (typeof w.open !== "function") return;
  for (const target of ["_system", "_blank"]) {
    try {
      const opened = w.open(url, target, "noopener");
      // A shell that does not know the target returns null (a blocked or
      // unrecognised popup). `undefined` is what Capacitor's own delegate hands
      // back for a hand-off it took, so only an explicit null is a failure.
      if (opened !== null) return;
    } catch {
      // Try the next target; a dead link must not take the terminal down.
    }
  }
}
