// The camera, as the connect screen sees it.
//
// THE PLUGIN CONTRACT, WHICH ANOTHER AGENT OWNS. This module reaches
// `LolaTransport` through the Capacitor global rather than importing
// `lola-transport`, for the same two reasons `secretstore.ts` does: the
// plugin's `dist/` does not exist until it is built, so a hard import breaks
// `vite build` for anyone who has not built it, and a browser `npm run dev`
// session has no plugin at all and must still render the UI. The two methods
// it looks for are:
//
//   scanCapability() -> { available, authorization, reason? }
//   scanQR()         -> { cancelled: boolean, value?: string }
//
// A CANCEL RESOLVES rather than rejecting, and that is the plugin's choice, not
// an accident to normalise away: a human changing their mind is the ordinary
// way a scanner ends, and putting it on the error channel is what makes every
// call site tell it apart from a broken camera by reading a code — which is how
// it ends up rendered as a red banner. `scanQR` rejects only when a scan could
// not be ATTEMPTED, with a `LolaScanErrorCode` in `code`. A rejection carrying
// no code at all still falls back to reading the message, so a plugin that
// reports honestly but differently produces something better than "scan failed".
//
// WHY THE PROBE EXISTS, rather than just trying and handling the failure. The
// Simulator has no camera and cannot be given one, so every scan there fails —
// and on the one device this workflow can screenshot, a Scan button would be a
// control that looks live and does nothing. The probe lets the button be absent
// instead of broken. It is asked once, on mount, because the answer is a
// property of the machine.
//
// `notDetermined` COUNTS AS AVAILABLE, and the plugin is right to say so: the
// camera prompt has not been shown yet, and hiding the button would guarantee
// it never is. The scanner asks when it opens, which is the moment a human has
// expressed the intent that makes an iOS permission prompt make sense.

import { parsePairing, type PairNotice, type PairResult } from "./pairpayload";

/** What one attempt to scan produced. */
export type ScanOutcome =
  /** A code was read. The text is UNTRUSTED and is not a payload yet. */
  | { kind: "value"; text: string }
  /** The user backed out. Not a failure, and deliberately shows no message. */
  | { kind: "cancelled" }
  /** The camera permission was refused. Only Settings can undo it. */
  | { kind: "denied" }
  /** Policy forbids the camera. There is no toggle to send anyone to. */
  | { kind: "restricted" }
  /** There is no scanner here: no plugin, no camera, or a Simulator. */
  | { kind: "unavailable"; reason?: string }
  /** Something else broke inside the scanner. */
  | { kind: "failed"; reason?: string };

/** Whether to offer a Scan button at all, and why not when the answer is no. */
export interface ScanCapability {
  available: boolean;
  reason?: string;
}

/** The slice of `LolaTransportPlugin` this module needs. */
interface ScanCapablePlugin {
  scanQR?(o?: { prompt?: string }): Promise<{ cancelled?: boolean; value?: string | null }>;
  scanCapability?(): Promise<{ available?: boolean; reason?: string }>;
}

/** `LolaScanCapabilityResult.reason`, as a sentence. */
function reasonText(reason: string | undefined): string | undefined {
  switch (reason) {
    case "no_camera":
      return "This device has no camera — a Simulator never does.";
    case "denied":
      return "Camera access is switched off for Lola.";
    case "restricted":
      return "Camera access is blocked on this device.";
    case "unsupported":
      return "This build has no scanner.";
    default:
      return undefined;
  }
}

interface CapacitorGlobal {
  Plugins?: { LolaTransport?: ScanCapablePlugin };
}

function plugin(): ScanCapablePlugin | undefined {
  const cap = (globalThis as { Capacitor?: CapacitorGlobal }).Capacitor;
  const p = cap?.Plugins?.LolaTransport;
  return p && typeof p.scanQR === "function" ? p : undefined;
}

/**
 * Can this build scan?
 *
 * Fails CLOSED in both directions that matter: no plugin means no button, and a
 * probe that throws means no button either. An offered control that cannot work
 * is worse than an absent one, because the absent one sends the user straight
 * to the form that does.
 */
export async function scanCapability(): Promise<ScanCapability> {
  const p = plugin();
  if (!p) return { available: false, reason: "This build has no scanner." };
  if (typeof p.scanCapability !== "function") return { available: true };
  try {
    const r = await p.scanCapability();
    return { available: r?.available === true, reason: reasonText(r?.reason) };
  } catch {
    return { available: false, reason: "The camera did not answer." };
  }
}

/**
 * A rejected `scanQR`, as an outcome.
 *
 * Pure and exported so every branch is pinned by a test: the `code` path is
 * what the plugin promises, and the message path is the fallback for a plugin
 * that rejects honestly without one. Reading the message is deliberately
 * narrow — matching a bare "error" or "camera" against arbitrary text is how a
 * confident wrong diagnosis gets shown.
 */
export function scanErrorToOutcome(e: unknown): ScanOutcome {
  const err = e as { code?: unknown; message?: unknown } | null | undefined;
  const code = typeof err?.code === "string" ? err.code : "";
  const message = typeof err?.message === "string" ? err.message : "";

  switch (code) {
    case "camera_denied":
    case "denied":
      return { kind: "denied" };
    case "camera_restricted":
      return { kind: "restricted" };
    case "cancelled":
    case "canceled":
      return { kind: "cancelled" };
    case "no_camera":
      return { kind: "unavailable", reason: reasonText("no_camera") };
    case "unavailable":
    case "unimplemented":
      return { kind: "unavailable", reason: reasonText("unsupported") };
  }

  const m = message.toLowerCase();
  if (m.includes("denied") || m.includes("permission") || m.includes("not authorized")) {
    return { kind: "denied" };
  }
  if (m.includes("cancel")) return { kind: "cancelled" };
  if (m.includes("no camera") || m.includes("not available") || m.includes("not implemented")) {
    return { kind: "unavailable", reason: message };
  }
  return { kind: "failed", reason: message };
}

/**
 * Open the scanner and wait for one code.
 *
 * Never throws: every path the plugin can take comes back as an outcome, so the
 * call site is a switch rather than a try/catch wrapped around a switch.
 */
export async function scanOnce(): Promise<ScanOutcome> {
  const p = plugin();
  if (!p?.scanQR) return { kind: "unavailable", reason: "This build has no scanner." };
  try {
    const r = await p.scanQR();
    if (r?.cancelled === true) return { kind: "cancelled" };
    const text = typeof r?.value === "string" ? r.value : "";
    return text === "" ? { kind: "cancelled" } : { kind: "value", text };
  } catch (e) {
    return scanErrorToOutcome(e);
  }
}

/**
 * A non-value outcome, as a banner — or null when it deserves no message.
 *
 * `cancelled` is the null: the user pressed Cancel one moment ago and knows
 * exactly what happened, and a banner explaining their own decision back to
 * them is the kind of noise that makes an app feel like it is arguing.
 */
export function scanMessage(o: ScanOutcome): PairNotice | null {
  switch (o.kind) {
    case "value":
    case "cancelled":
      return null;

    case "restricted":
      // Deliberately no Settings hint: a restricted camera has no toggle, and
      // sending somebody to a switch that is not there is worse than silence.
      return {
        tone: "warn",
        title: "The camera is blocked on this device",
        detail: "A profile or Screen Time restriction forbids camera access, so no scan can run.",
        hint: "Type the details in below — the form does exactly the same thing.",
      };

    case "denied":
      return {
        tone: "warn",
        title: "The camera is switched off for Lola",
        detail:
          "iOS asks for the camera once, and it is answered now. The switch is the only way " +
          "back, and it is not in this app.",
        hint: "Settings → Privacy & Security → Camera → Lola. Or type the details in below.",
      };

    case "unavailable":
      return {
        tone: "warn",
        title: "No camera to scan with",
        detail: o.reason || "There is no scanner in this build.",
        hint: "Type the details in below instead — the form does exactly the same thing.",
      };

    case "failed":
      return {
        tone: "bad",
        title: "The scanner stopped",
        detail: o.reason || "It closed without reading anything.",
        hint: "Try again, or type the details in below.",
      };
  }
}

/**
 * The whole scan, from button press to something connectable.
 *
 * Returns either a parsed payload or the ONE notice to show. Keeping the join
 * here rather than in the component is what stops the screen from growing two
 * different ideas of what a failed scan looks like.
 */
export async function scanForPairing(): Promise<
  | { ok: true; result: PairResult }
  | { ok: false; outcome: ScanOutcome; notice: PairNotice | null }
> {
  const outcome = await scanOnce();
  if (outcome.kind === "value") return { ok: true, result: parsePairing(outcome.text) };
  // The outcome travels with the notice so the caller can act on the KIND —
  // "there turned out to be no scanner here" has to stop the button being
  // offered, and matching that on the rendered title would be a screen's copy
  // deciding a control's existence.
  return { ok: false, outcome, notice: scanMessage(outcome) };
}
