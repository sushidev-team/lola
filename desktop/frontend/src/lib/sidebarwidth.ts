export const SIDEBAR_DEFAULT = 540;
export const SIDEBAR_MIN = 280;
export const SIDEBAR_MAX = 760;
export const WORKSPACE_MIN = 560;
export function sidebarLimit(windowWidth: number): number {
  return Math.max(0, Math.min(SIDEBAR_MAX, windowWidth - WORKSPACE_MIN));
}
/** The saved preference is clamped separately from available window space. */
export function sidebarWidth(preference: number, windowWidth: number): number {
  const wanted = Number.isFinite(preference) ? Math.min(SIDEBAR_MAX, Math.max(SIDEBAR_MIN, preference)) : SIDEBAR_DEFAULT;
  return Math.min(wanted, sidebarLimit(windowWidth));
}
/**
 * The projects column. Beside the sessions it may shrink to PROJECTS_MIN (labels truncate). In Git
 * mode it is the whole sidebar, so there it never goes narrower than the header row
 * (PROJECTS_GIT_MIN): traffic-light inset, logo, the Code/Git switch and its search.
 */
export const PROJECTS_DEFAULT = 300;
export const PROJECTS_MIN = 180;
export const PROJECTS_GIT_MIN = 260;
export const PROJECTS_MAX = 440;
/** The least the sessions column gets beside it before the sidebar switches to tabs. */
export const SESSIONS_MIN = 180;
export function clampProjectsWidth(value: number): number {
  return Number.isFinite(value) ? Math.max(PROJECTS_MIN, Math.min(PROJECTS_MAX, Math.round(value))) : PROJECTS_DEFAULT;
}
