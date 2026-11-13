import type { SessionInfo } from './store.svelte';
import { rosterGroups, rosterLine } from './sessionview';
import { waitingQueue } from './queue';
import { matchesTriageFor } from './filters';

export type RosterFilter = { query: string; triage: string; scoped: boolean; project: string };
export function rosterMatches(s: SessionInfo, filter: RosterFilter, projectLabel: string): boolean {
  if (filter.triage === 'Ended') return false;
  if (filter.scoped && s.project !== filter.project) return false;
  if (filter.triage === 'Needs You' ? !waitingQueue([s]).length : !matchesTriageFor(s, filter.triage)) return false;
  const words = filter.query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  const haystack = [s.title, s.issue, s.branch, s.id, projectLabel, s.prNumber ? `#${s.prNumber}` : ''].join(' ').toLocaleLowerCase();
  return words.every(word => haystack.includes(word));
}
/** The one ordering/filtering path used by the roster, initial selection and keyboard. */
export function visibleRoster(sessions: SessionInfo[], projects: string[], label: (name: string) => string, filter: RosterFilter) {
  return rosterGroups(sessions, projects, label).flatMap(group => group.rows).filter(s => rosterMatches(s, filter, label(s.project)));
}

export function rosterActivity(s: SessionInfo, now: number, stopped = false) {
  const line = rosterLine(s, now, stopped);
  if (line.tone !== 'faint' || stopped || s.board?.blocked) return { ...line, compact: false, detail: line.text };
  const report = s.headline ? `≈ ${s.headline}` : s.lastNotification || s.board?.current;
  const routine = /^(?:Claude|Codex|OpenCode) is waiting for your input[.!]?$/i.test(report || '');
  if (report && !routine) return { ...line, text: report, compact: false, detail: report };
  return { ...line, compact: true, detail: report || line.text, text: s.prNumber ? line.text.replace(` · #${s.prNumber}`, '').replace(new RegExp(`^#${s.prNumber}$`), '') : line.text };
}
