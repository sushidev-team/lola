export const SIDEBAR_DEFAULT = 320;
export const SIDEBAR_MIN = 240;
export const SIDEBAR_MAX = 480;
export const WORKSPACE_MIN = 560;
export function sidebarLimit(windowWidth: number): number {
  return Math.max(0, Math.min(SIDEBAR_MAX, windowWidth - WORKSPACE_MIN));
}
/** The saved preference is clamped separately from available window space. */
export function sidebarWidth(preference: number, windowWidth: number): number {
  const wanted = Number.isFinite(preference) ? Math.min(SIDEBAR_MAX, Math.max(SIDEBAR_MIN, preference)) : SIDEBAR_DEFAULT;
  return Math.min(wanted, sidebarLimit(windowWidth));
}
