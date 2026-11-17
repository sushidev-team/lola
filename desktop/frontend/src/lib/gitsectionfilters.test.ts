import { beforeEach, expect, it } from 'vitest';
import { compileSectionFilter, emptySectionFilter, emptySectionFilters, readSectionFilters, saveSectionFilters, stashBranch } from './gitsectionfilters';
beforeEach(() => localStorage.clear());
it('combines name/status and any/all regexes with exclusion and case sensitivity', () => {
  const filter = {...emptySectionFilter(),regexes:['^feature/', 'sidebar$']};
  expect(compileSectionFilter(filter)('fix/sidebar')).toBe(true);
  filter.match = 'all';
  expect(compileSectionFilter(filter)('fix/sidebar')).toBe(false);
  expect(compileSectionFilter(filter)('feature/sidebar')).toBe(true);
  filter.exclude = true;
  expect(compileSectionFilter(filter)('feature/sidebar')).toBe(false);
  expect(compileSectionFilter(filter)('feature/panel', false)).toBe(false);
  filter.exclude = false;
  filter.ignoreCase = true;
  filter.search = 'SIDEBAR';
  expect(compileSectionFilter(filter)('FEATURE/SIDEBAR')).toBe(true);
});
it('does not throw or hide results while a regex is incomplete', () => {
  const filter = {...emptySectionFilter(),search:'feature',regexes:['['],exclude:true};
  expect(compileSectionFilter(filter)('feature/sidebar')).toBe(true);
  expect(compileSectionFilter(filter)('main')).toBe(false);
});
it('isolates saved filters by project and handles malformed storage', () => {
  const filters = emptySectionFilters();
  filters.local.regexes = ['^feature/'];
  saveSectionFilters('one',filters);
  expect(readSectionFilters('one').local.regexes).toEqual(['^feature/']);
  expect(readSectionFilters('two').local.regexes).toEqual([]);
  localStorage.setItem('lola.git.section-filters.one', 'broken');
  expect(readSectionFilters('one')).toEqual(emptySectionFilters());
});
it('recognizes stash branch metadata without guessing unknown subjects', () => {
  expect(stashBranch('WIP on feature/topic: abc123 change')).toBe('feature/topic');
  expect(stashBranch('On main: my work')).toBe('main');
  expect(stashBranch('imported stash')).toBe('');
});
