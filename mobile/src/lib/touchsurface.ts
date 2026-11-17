// The phone's side of `$lib/touchsurface`: shared components ask for a kind, this maps it to the
// spec 6.5 haptic event. One place, so main.ts and preview.ts cannot drift.
import type { TouchHaptic } from "$lib/touchsurface";
import { haptic, type HapticEvent } from "@mobile/lib/haptics";

export const TOUCH_HAPTIC_EVENT: Readonly<Record<TouchHaptic, HapticEvent>> = {
  decision: "decision", // medium
  light: "sent", // light
  rigid: "stop", // rigid
  success: "success",
  warning: "refusal", // warning
};

export const phoneTouchSurface = { haptic: (k: TouchHaptic): void => haptic(TOUCH_HAPTIC_EVENT[k]) };
