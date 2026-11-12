export const gitColumns = [
  { id: 'refs', label: 'Branches / checkouts', width: 180, min: 90, max: 500 },
  { id: 'graph', label: 'Graph', width: 64, min: 40, max: 600 },
  { id: 'subject', label: 'Commit', width: 240, min: 160, max: 1000 },
  { id: 'author', label: 'Author', width: 110, min: 70, max: 400 },
  { id: 'sha', label: 'SHA', width: 70, min: 60, max: 340 },
  { id: 'age', label: 'Age', width: 70, min: 60, max: 200 },
] as const;
export type GitColumnID = typeof gitColumns[number]['id'];
export type GitColumnPreferences = { hidden: GitColumnID[]; widths: Partial<Record<GitColumnID, number>> };
const key = 'lola.git.columns';
export function defaultGitColumns(): GitColumnPreferences { return { hidden: [], widths: {} }; }
export function readGitColumns(): GitColumnPreferences {
  try {
    const value = JSON.parse(localStorage.getItem(key) ?? '{}');
    const result = defaultGitColumns();
    result.hidden = gitColumns.filter(c => c.id !== 'subject' && Array.isArray(value.hidden) && value.hidden.includes(c.id)).map(c => c.id);
    for (const c of gitColumns) {
      const width = value.widths?.[c.id];
      if (typeof width === 'number' && Number.isFinite(width)) result.widths[c.id] = Math.max(c.min, Math.min(c.max, width));
    }
    return result;
  } catch { return defaultGitColumns(); }
}
export function saveGitColumns(value: GitColumnPreferences) {
  try { localStorage.setItem(key, JSON.stringify(value)); } catch { /* Keep the current layout when storage is unavailable. */ }
}
