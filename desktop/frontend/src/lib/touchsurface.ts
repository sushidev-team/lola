// Whether this client is a touch surface (the phone). The phone sets it at boot so shared
// components can tune a gesture or fire a haptic without importing anything native; the desktop
// never sets it, so every call here is false / a no-op there. Same shape as widgetsoff.ts.

export type TouchHaptic = "decision" | "light" | "rigid" | "success" | "warning";

export interface TouchSurface {
  haptic?: (k: TouchHaptic) => void;
}

let surface: TouchSurface | null = null;

export function setTouchSurface(v: TouchSurface | null): void {
  surface = v;
}

export function touchSurface(): boolean {
  return surface !== null;
}

export function touchHaptic(k: TouchHaptic): void {
  try {
    surface?.haptic?.(k);
  } catch {
    /* a haptic is never worth surfacing */
  }
}
