export const SIDEBAR_DEFAULT = 540;
export const SIDEBAR_MIN = 280;
export const SIDEBAR_MAX = 760;
/** Below this the two columns do not fit; the sidebar offers Projects and Sessions as tabs instead. */
export const SIDEBAR_SPLIT = 480;
export const WORKSPACE_MIN = 560;
export function sidebarLimit(windowWidth: number): number {
  return Math.max(0, Math.min(SIDEBAR_MAX, windowWidth - WORKSPACE_MIN));
}
/** The saved preference is clamped separately from available window space. */
export function sidebarWidth(preference: number, windowWidth: number): number {
  const wanted = Number.isFinite(preference) ? Math.min(SIDEBAR_MAX, Math.max(SIDEBAR_MIN, preference)) : SIDEBAR_DEFAULT;
  return Math.min(wanted, sidebarLimit(windowWidth));
}
/** Git mode keeps the projects panel only, but never narrower than the brand row: traffic-light inset, logo and the Code/Git switch. */
export const GIT_SIDEBAR_WIDTH = 332;
