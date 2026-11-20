import { nav } from './nav.svelte';
import { store, type ProjectInfo } from './store.svelte';

/** The repository Git would open on: the open session's, the current project, else the last one used. */
export function gitTargetProject(): ProjectInfo | undefined {
  const session = nav.view === 'cockpit' ? store.sessionById(nav.focusedTerm || nav.selectedId) : undefined;
  const member = session?.stack ? session.apps?.find(app => app.name === session.focusApp) : undefined;
  const repositories = store.projects.filter(project => project.path && project.kind !== 'stack');
  // The projects column highlights the session filter, and a filter keeps the open session on
  // screen even when it belongs elsewhere. Git opens on what is highlighted, so the open
  // session only decides while it is inside the filter (or nothing is filtered).
  const scope = nav.view === 'cockpit' ? nav.scopeNames : [];
  const own = [member?.name, session?.project];
  const inScope = !scope.length || scope.includes(session?.project ?? '');
  // With nothing selected and no history, the first repository: Git never bounces back to Code.
  return [...(inScope ? [...own, ...scope] : [...scope, ...own]), nav.project, nav.lastGitProject]
    .map(name => repositories.find(project => project.name === name)).find(Boolean) ?? repositories[0];
}

/** A repository opens in Git. Stacks and projects without a checkout open their sessions. */
export function openProject(name: string) {
  const project = store.projectByName(name);
  if (project?.path && project.kind !== 'stack') {
    nav.goGit(name);
    return;
  }
  nav.pendingMode = null;
  nav.goCockpit(name);
  nav.sidebarTab = 'sessions';
}

export function toggleGitWorkspace() {
  if (nav.inGit) {
    nav.returnFromGit();
    return;
  }
  const project = gitTargetProject();
  if (project) nav.goGit(project.name);
  else nav.goHome();
}
