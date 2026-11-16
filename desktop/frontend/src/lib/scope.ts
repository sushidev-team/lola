import type { ProjectInfo, SessionInfo } from './store.svelte';

type Scoped = { project?: string; apps?: { name: string }[] | null };
export type ScopeMatcher = (s: Scoped) => boolean;

/**
 * "Part of": a session belongs to a project when it runs in it or, for a stack session, when it
 * has the project among its apps. A stack also reaches its member projects, and a member reaches
 * the stack sessions it is part of. Several names match when ANY of them does.
 */
export function makeScope(names: string[], projects: Pick<ProjectInfo, 'name' | 'stack'>[]): ScopeMatcher | null {
  if (names.length === 0) return null;
  const reach = new Set<string>(names);
  for (const p of projects) if (p.stack && names.includes(p.stack)) reach.add(p.name);
  return (s) => (!!s.project && reach.has(s.project)) || !!s.apps?.some((app) => reach.has(app.name));
}
