// Haptics (spec §6.5). iOS's System Haptics setting governs them, not
// prefers-reduced-motion. A call inside HAPTIC_MIN_GAP_MS of the last one is
// dropped, and a failed call is never worth surfacing.

import type { HapticKind } from "lola-transport";
import { transport } from "@mobile/native";
// The side-effect-free definitions file, not the package entry, which registers the plugin.
import { HAPTIC_MIN_GAP_MS } from "../../plugins/lola-transport/src/definitions";

export type HapticEvent =
  | "sent"
  | "decision"
  | "stop"
  | "select"
  | "success"
  | "refusal"
  | "notPaired";

export const HAPTIC_FOR: Readonly<Record<HapticEvent, HapticKind>> = {
  sent: "light",
  decision: "medium",
  stop: "rigid",
  select: "selection",
  success: "success",
  refusal: "warning",
  notPaired: "error",
};

let last = -Infinity;

export function haptic(event: HapticEvent, now: number = Date.now()): void {
  if (now - last < HAPTIC_MIN_GAP_MS) return;
  last = now;
  try {
    void transport.haptic({ kind: HAPTIC_FOR[event] }).catch(() => {});
  } catch {
    /* no haptic engine */
  }
}

/** Test seam: forgets the throttle. */
export function resetHaptics(): void {
  last = -Infinity;
}
