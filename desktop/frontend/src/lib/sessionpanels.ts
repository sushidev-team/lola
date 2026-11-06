// Reserved dock tabs belong to lola; agent widgets retain their own ids and sandbox.
export const OVERVIEW_PANEL = '@lola/overview';
export const CHANGES_PANEL = '@lola/changes';
export const SESSION_PANELS = [OVERVIEW_PANEL, CHANGES_PANEL];
export const isSessionPanel = (id: string) => SESSION_PANELS.includes(id);
export const sessionPanelTitle = (id: string) => id === OVERVIEW_PANEL ? 'Overview' : 'Changes';

/** Keep tab labels compact; the full agent title remains available in the tooltip and menu. */
export function widgetTabLabel(title: string): string {
  const name = title.split('\n')[0].trim().replace(/\s+/g, ' ') || 'Widget';
  return name.length > 28 ? name.slice(0, 27).trimEnd() + '…' : name;
}
