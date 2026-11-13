import { nav } from './nav.svelte';
import { store } from './store.svelte';

/** Use the current repository, then the last Git workspace; otherwise show Projects. */
export function toggleGitWorkspace() {
  const visibleProject = store.projectByName(nav.project);
  if (nav.inGit && visibleProject?.path && visibleProject.kind !== 'stack') { nav.returnFromGit(); return; }
  // Stack and unavailable-project screens render Overview even when Git was
  // the last saved tab. Record the visible destination when leaving them.
  if (nav.inGit) nav.projectTab = 'overview';
  const session = nav.view === 'cockpit' ? store.sessionById(nav.focusedTerm || nav.selectedId) : undefined;
  const member = session?.stack ? session.apps?.find(app => app.name === session.focusApp) : undefined;
  const repositories = store.projects.filter(project => project.path && project.kind !== 'stack');
  const project = [member?.name, session?.project, nav.project, nav.lastGitProject]
    .map(name => repositories.find(project => project.name === name)).find(Boolean);
  if (!project) {
    nav.goHome();
    nav.setSidebarTab('projects');
    return;
  }
  nav.rememberGitProject(project.name);
  nav.goDetail(project.name, 'git');
}
