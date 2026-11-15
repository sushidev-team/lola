// Opening a link the terminal printed, on the PHONE, and only if it is http(s).
//
// Invariants:
//  - Terminal text is untrusted. A log line can print `file://`, `javascript:`
//    or `data:`, so the scheme is checked against a two-entry allowlist before
//    anything happens and anything else is refused silently.
//  - The exec host is the phone, never the daemon. The desktop's `cmd=openURL`
//    runs `open` on the Mac, which from a phone would launch Safari on an
//    unattended machine. A link whose host is loopback is also refused here:
//    it names the Mac, not the phone.
//  - Every step falls through to the next, so a missing plugin or a blocked
//    popup costs a link, never an exception. The ladder, in order:
//      0. `native.openURL` (UIApplication.open: real Safari, and the system's
//         own answer; the in-app browser resolved without presenting anything
//         for a private-network http address, so it goes second).
//      1. The Capacitor Browser plugin (SFSafariViewController, with a Done
//         button; good for a PR link).
//      2. `window.open(url, "_system")`: Capacitor's WKUIDelegate hands a
//         navigation with no target frame to the system browser.
//      3. `window.open(url, "_blank")`: what a plain desktop browser needs.

import { browser, native } from "../native";

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

/** A loopback host names the daemon's machine, not this phone. */
export function isLoopbackUrl(url: string): boolean {
  let host: string;
  try {
    host = new URL(url).hostname.toLowerCase();
  } catch {
    return false;
  }
  if (host === "localhost" || host.endsWith(".localhost")) return true;
  if (host === "[::1]" || host === "::1") return true;
  return /^127(\.\d{1,3}){3}$/.test(host);
}

/**
 * Open a URL outside the app. Never throws; ANSWERS whether it opened, because a
 * control whose whole purpose is to open something must be able to say it could
 * not. Callers that do not care may ignore the result.
 */
export async function openExternal(url: string): Promise<boolean> {
  if (!isOpenable(url)) return false;

  if (native) {
    try {
      await native.openURL({ url });
      return true;
    } catch {
      // A plugin that refused is not the end of the ladder.
    }
  }

  if (browser) {
    try {
      await browser.open({ url });
      return true;
    } catch {
      // fall through to the window opener
    }
  }

  const w = globalThis as { open?: (u: string, t?: string, f?: string) => unknown };
  if (typeof w.open !== "function") return false;
  for (const target of ["_system", "_blank"]) {
    try {
      const opened = w.open(url, target, "noopener");
      // An unrecognised target returns null; `undefined` is what Capacitor's
      // delegate hands back for a hand-off it took, so only null is a failure.
      if (opened !== null) return true;
    } catch {
      // Try the next target; a dead link must not take the terminal down.
    }
  }
  return false;
}

/** Why `openOnPhone` refused. */
export type OpenRefusal = "not_http" | "loopback" | "failed";

/** Thrown by `openOnPhone`; `reason` is the only thing a caller needs to word. */
export class OpenOnPhoneError extends Error {
  constructor(readonly reason: OpenRefusal) {
    super(`open on phone: ${reason}`);
    this.name = "OpenOnPhoneError";
  }
}

/**
 * The shim's `OpenURL`: open on this phone or reject with an
 * `OpenOnPhoneError`. Loopback is refused before anything is opened.
 */
export async function openOnPhone(url: string): Promise<void> {
  if (!isOpenable(url)) throw new OpenOnPhoneError("not_http");
  if (isLoopbackUrl(url)) throw new OpenOnPhoneError("loopback");
  if (!(await openExternal(url))) throw new OpenOnPhoneError("failed");
}
