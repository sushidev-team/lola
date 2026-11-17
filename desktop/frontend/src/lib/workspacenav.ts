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

/** Use the current repository, then the last Git workspace; otherwise show Projects. */
export function toggleGitWorkspace() {
  const visibleProject = store.projectByName(nav.project);
  if (nav.inGit && visibleProject?.path && visibleProject.kind !== 'stack') { nav.returnFromGit(); return; }
  // Stack and unavailable-project screens render Overview even when Git was
  // the last saved tab. Record the visible destination when leaving them.
  if (nav.inGit) nav.projectTab = 'overview';
  const project = gitTargetProject();
  if (!project) {
    nav.goHome();
    return;
  }
  nav.rememberGitProject(project.name);
  nav.goDetail(project.name, 'git');
}
