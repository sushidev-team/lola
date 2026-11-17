export type SectionFilter = { search: string; status: string; regexes: string[]; match: 'any' | 'all'; exclude: boolean; ignoreCase: boolean };
export type SectionFilters = Record<'local' | 'remote' | 'worktrees' | 'stashes', SectionFilter>;
export const emptySectionFilter = (): SectionFilter => ({search:'',status:'all',regexes:[],match:'any',exclude:false,ignoreCase:false});
export const emptySectionFilters = (): SectionFilters => ({local:emptySectionFilter(),remote:emptySectionFilter(),worktrees:emptySectionFilter(),stashes:emptySectionFilter()});
export function filterActive(filter: SectionFilter) { return !!filter.search.trim() || filter.status !== 'all' || filter.regexes.some(pattern => pattern !== ''); }
export function regexError(pattern: string) { try { new RegExp(pattern); return ''; } catch { return 'Invalid regular expression'; } }
export function compileSectionFilter(filter: SectionFilter) {
  const search = filter.search.trim().toLocaleLowerCase();
  const patterns = filter.regexes.filter(Boolean);
  // While a pattern is incomplete, keep the name/status results visible.
  const regexes = patterns.some(regexError) ? [] : patterns.map(pattern => new RegExp(pattern, filter.ignoreCase ? 'i' : ''));
  const match = filter.match, exclude = filter.exclude;
  return (text: string, statusMatches = true) => {
    if (!statusMatches || (search && !text.toLocaleLowerCase().includes(search))) return false;
    if (!regexes.length) return true;
    const matched = match === 'all' ? regexes.every(regex => regex.test(text)) : regexes.some(regex => regex.test(text));
    return exclude ? !matched : matched;
  };
}
export function readSectionFilters(project: string): SectionFilters {
  const result = emptySectionFilters();
  try {
    const saved = JSON.parse(localStorage.getItem(`lola.git.section-filters.${project}`) || '{}');
    for (const key of Object.keys(result) as (keyof SectionFilters)[]) {
      const value = saved?.[key];
      if (!value || typeof value !== 'object') continue;
      result[key] = {search:typeof value.search === 'string' ? value.search : '', status:typeof value.status === 'string' ? value.status : 'all', regexes:Array.isArray(value.regexes) ? value.regexes.filter((r: unknown) => typeof r === 'string') : [], match:value.match === 'all' ? 'all' : 'any', exclude:value.exclude === true, ignoreCase:value.ignoreCase === true};
    }
  } catch { /* Storage is optional. */ }
  return result;
}
export function saveSectionFilters(project: string, filters: SectionFilters) {
  try { localStorage.setItem(`lola.git.section-filters.${project}`, JSON.stringify(filters)); } catch { /* Keep live filters. */ }
}
// Git's reflog subject includes the original branch for ordinary stashes.
export function stashBranch(subject: string) { return /^(?:WIP on|On) ([^:]+):/.exec(subject)?.[1] ?? ''; }
