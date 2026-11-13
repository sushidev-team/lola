import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { GitActions } from './gitactions.svelte';
import { ProjectGitState } from './projectgit.svelte';
import { confirm } from './confirm.svelte';
import ProjectGitActionMenu from './components/ProjectGitActionMenu.svelte';
import type { GitRef, GitSnapshot } from './projectgit';
vi.mock('@bindings/desktop', () => ({ ProjectGitService: {} }));
const main: GitRef = { name: 'main', fullName: 'refs/heads/main', sha: 'a'.repeat(40), remote: false, current: true };
const feature: GitRef = { name: 'feature/work', fullName: 'refs/heads/feature/work', sha: 'b'.repeat(40), remote: false, current: false };
let git: ProjectGitState;
let actions: GitActions;
let callbacks: { viewHistory: Mock<(ref: string) => void>; openWorktree: Mock<(path: string) => void>; viewCommit: Mock<(sha: string) => void> };
const event = () => new MouseEvent('contextmenu', { clientX: 15, clientY: 20, cancelable: true });
beforeEach(() => {
  git = new ProjectGitState('project', '');
  git.snapshot = { path: '/repo', branch: 'main', head: main.sha, refs: [main, feature], worktrees: [], commits: [], files: [], signing: { enabled: true, format: 'ssh', key: '' }, upstream: 'origin/main', hasMore: false } satisfies GitSnapshot;
  git.stashes = [{ sha: 'c'.repeat(40), selector: 'stash@{0}', subject: 'Saved work', date: '' }];
  vi.spyOn(git, 'branchAction').mockResolvedValue('/repo');
  vi.spyOn(git, 'stashAction').mockResolvedValue(true);
  callbacks = { viewHistory: vi.fn(), openWorktree: vi.fn(), viewCommit: vi.fn() };
  actions = new GitActions(() => ({ git, ref: '', limit: 200 }), callbacks);
  confirm.cancel();
});
afterEach(() => { cleanup(); confirm.cancel(); vi.restoreAllMocks(); });
describe('Git action menus', () => {
  it('opens a PR with the dragged source and drop target without mutating Git', async () => {
    const createPullRequest = vi.fn();
    git.snapshot!.remotes = [{name:'origin',host:'github.com',owner:'acme',repo:'acme/repo',avatarUrl:''}];
    actions = new GitActions(() => ({git,ref:'',limit:200}), {...callbacks,createPullRequest});
    render(ProjectGitActionMenu, {actions});
    actions.dropBranches(feature, main, event());
    await fireEvent.click(await screen.findByRole('menuitem', {name:'Create PR…'}));
    expect(createPullRequest).toHaveBeenCalledWith(feature, main);
    expect(git.branchAction).not.toHaveBeenCalled();
    expect(confirm.request).toBeNull();
  });

  it('offers push and tracking choices when dropping a local branch onto a remote', async () => {
    const publishBranch=vi.fn();git.snapshot!.remotes=[{name:'origin',host:'',owner:'',repo:'',avatarUrl:''}];
    const target={name:'origin/review',fullName:'refs/remotes/origin/review',sha:main.sha,remote:true,current:false};
    actions=new GitActions(()=>({git,ref:'',limit:200}),{...callbacks,publishBranch});render(ProjectGitActionMenu,{actions});
    actions.dropBranches(feature,target,event());
    expect(await screen.findByRole('menuitem',{name:/Merge feature/})).toBeDisabled();
    await fireEvent.click(await screen.findByRole('menuitem',{name:'Push feature/work to origin/review…'}));
    expect(publishBranch).toHaveBeenLastCalledWith(feature,{remote:'origin',name:'review'});expect(git.branchAction).not.toHaveBeenCalled();
    actions.dropBranches(feature,target,event());await fireEvent.click(await screen.findByRole('menuitem',{name:'Set upstream to origin/review…'}));
    expect(publishBranch).toHaveBeenLastCalledWith(feature,{remote:'origin',name:'review',trackingOnly:true});
  });
  it('disables impossible pulls and sandbox writes while allowing history inspection', async () => {
    render(ProjectGitActionMenu,{actions});actions.openBranch(feature,event());
    expect(await screen.findByRole('menuitem',{name:'Pull changes'})).toBeDisabled();
    git.snapshot!.worktrees=[{path:'/sandbox',branch:feature.name,head:feature.sha,blocked:true,locked:false,prunable:false}];
    actions.dropBranches(main,feature,event());expect(await screen.findByRole('menuitem',{name:/^Merge main/})).toBeDisabled();
    git.snapshot!.operation='rebase';actions.openBranch(main,event());expect(await screen.findByRole('menuitem',{name:'Push branch'})).toBeDisabled();expect(screen.getByRole('menuitem',{name:'View branch history'})).toBeEnabled();
  });
  it('lets merge actions explicitly select their mainline before confirmation', async () => {
    render(ProjectGitActionMenu,{actions});
    actions.openCommit({sha:feature.sha,subject:'Merge',author:'A',email:'',date:'',parents:[main.sha,'c'.repeat(40)],refs:[]},event());
    await fireEvent.click(await screen.findByRole('menuitem',{name:'Revert commit…'}));expect(confirm.request).toBeNull();
    await fireEvent.click(await screen.findByRole('menuitem',{name:'Parent 2 · cccccccc'}));expect(confirm.request?.detail).toContain('parent 2 (cccccccc)');confirm.accept();
    await waitFor(()=>expect(git.branchAction).toHaveBeenCalledWith(expect.objectContaining({action:'revert',mainline:2}), '',200));
  });
  it('provides recovery from the repository menu and confirms skipping', async () => {
    git.snapshot!.operation='cherry-pick';render(ProjectGitActionMenu,{actions});actions.openRepository(event());
    expect(await screen.findByRole('menuitem',{name:'Continue cherry-pick'})).toBeEnabled();
    await fireEvent.click(screen.getByRole('menuitem',{name:'Skip current commit…'}));expect(git.branchAction).not.toHaveBeenCalled();confirm.accept();
    await waitFor(()=>expect(git.branchAction).toHaveBeenCalledWith({action:'skip',source:'cherry-pick',sourceSHA:main.sha,target:'',targetSHA:''},'',200));
  });
  it('routes unchecked local-only Push to publishing while tracked Push stays direct', async () => {
    const publishBranch=vi.fn();actions=new GitActions(()=>({git,ref:'',limit:200}),{...callbacks,publishBranch});
    actions.push(feature);expect(publishBranch).toHaveBeenCalledWith(feature);expect(git.branchAction).not.toHaveBeenCalled();
    actions.push(main);await waitFor(()=>expect(git.branchAction).toHaveBeenCalledWith({action:'push',source:main.fullName,sourceSHA:main.sha,target:'',targetSHA:''},'',200));
  });
  it.each(['cherry-pick','revert'] as const)('confirms a pinned %s and enables conflict recovery', async (action) => {
    render(ProjectGitActionMenu,{actions});
    actions.openCommit({sha:feature.sha,subject:'Change',author:'A',email:'',date:'',parents:[main.sha],refs:[]},event());
    await fireEvent.click(await screen.findByRole('menuitem',{name:action==='revert'?'Revert commit…':'Cherry-pick commit…'}));
    expect(git.branchAction).not.toHaveBeenCalled();confirm.accept();
    await waitFor(()=>expect(git.branchAction).toHaveBeenCalledWith({action,source:'',sourceSHA:feature.sha,target:main.fullName,targetSHA:main.sha},'',200));
    git.snapshot!.operation=action;actions.continueOperation();
    await waitFor(()=>expect(git.branchAction).toHaveBeenLastCalledWith({action:'continue',source:action,sourceSHA:main.sha,target:'',targetSHA:''},'',200));
    actions.abortOperation();expect(confirm.request?.title).toBe(`Abort ${action}?`);
  });
  it('opens branch creation from an exact branch or commit without a Git mutation', async () => {
    const createBranch = vi.fn();
    actions = new GitActions(() => ({git, ref:'', limit:200}), {...callbacks, createBranch});
    render(ProjectGitActionMenu, {actions});
    actions.openBranch(feature, event());
    await fireEvent.click(await screen.findByRole('menuitem', {name:'Create branch here…'}));
    expect(createBranch).toHaveBeenLastCalledWith(feature.sha, feature.name);
    actions.openCommit({sha:main.sha,subject:'A specific commit',author:'A',email:'',date:'',parents:[],refs:[]},event());
    await fireEvent.click(await screen.findByRole('menuitem', {name:'Create branch here…'}));
    expect(createBranch).toHaveBeenLastCalledWith(main.sha,'A specific commit');
    expect(git.branchAction).not.toHaveBeenCalled();
  });
  it('pulls an unselected branch without changing the visible checkout', async () => {
    render(ProjectGitActionMenu, { actions });
    actions.openBranch({...feature,upstream:'refs/remotes/origin/feature/work'}, event());
    await fireEvent.click(await screen.findByRole('menuitem', { name: 'Pull changes' }));
    expect(git.branchAction).toHaveBeenCalledWith({ action: 'pull', source: feature.fullName, sourceSHA: feature.sha, target: '', targetSHA: '' }, '', 200);
    await waitFor(() => expect(actions.request).toBeNull());
    expect(callbacks.openWorktree).not.toHaveBeenCalled();
  });
  it('offers explicit drag directions and does not write until merge is confirmed', async () => {
    render(ProjectGitActionMenu, { actions });
    actions.dropBranches(feature, main, event());
    expect(git.branchAction).not.toHaveBeenCalled();
    await fireEvent.click(await screen.findByRole('menuitem', { name: 'Merge feature/work into main…' }));
    expect(confirm.request?.body).toContain('feature/work (bbbbbbbb) into main (aaaaaaaa)');
    expect(git.branchAction).not.toHaveBeenCalled();
    confirm.accept();
    await waitFor(() => expect(git.branchAction).toHaveBeenCalledWith({ action: 'merge', source: feature.fullName, sourceSHA: feature.sha, target: main.fullName, targetSHA: main.sha }, '', 200));
  });
  it('confirms stash and pop against the visible HEAD and exact stash SHA', () => {
    actions.stash();
    expect(git.stashAction).not.toHaveBeenCalled();
    confirm.accept();
    expect(git.stashAction).toHaveBeenCalledWith('stash', '', main.sha, '', 200);
    actions.pop();
    expect(confirm.request?.body).toContain('stash@{0} (cccccccc)');
    confirm.accept();
    expect(git.stashAction).toHaveBeenLastCalledWith('pop', 'c'.repeat(40), main.sha, '', 200);
  });
  it('pins abort to the operation and HEAD visible at confirmation', () => {
    git.snapshot!.operation = 'rebase';
    actions.abortOperation();
    expect(git.branchAction).not.toHaveBeenCalled();
    confirm.accept();
    expect(git.branchAction).toHaveBeenCalledWith({ action: 'abort', source: 'rebase', sourceSHA: main.sha, target: '', targetSHA: '' }, '', 200);
  });
  it('does not act after the displayed checkout changed while confirmation was open', () => {
    actions.stash();
    const previous = git;
    git = new ProjectGitState('other', '');
    confirm.accept();
    expect(previous.stashAction).not.toHaveBeenCalled();
  });
  it('disables comparison when no distinct commit is selected', async () => {
    const compareCommit = vi.fn();
    actions = new GitActions(() => ({ git, ref: '', limit: 200 }), { ...callbacks, compareCommit, canCompareCommit: () => false });
    render(ProjectGitActionMenu, { actions });
    actions.openCommit({ sha: main.sha, subject: 'Some commit', author: 'Author', email: '', date: '', parents: [], refs: [] }, event());
    const item = await screen.findByRole('menuitem', { name: 'Compare with selected commit' });
    expect(item).toBeDisabled(); await fireEvent.click(item); expect(compareCommit).not.toHaveBeenCalled();
  });
  it('supports keyboard navigation and explicit commit diff opening', async () => {
    render(ProjectGitActionMenu, { actions });
    actions.openCommit({ sha: main.sha, subject: 'Some commit', author: 'Author', email: '', date: '', parents: [], refs: [] }, event());
    const view = await screen.findByRole('menuitem', { name: 'View changes' });
    await waitFor(() => expect(view).toHaveFocus());
    await fireEvent.keyDown(view, { key: 'End' });
    expect(screen.getByRole('menuitem', { name: 'Copy commit SHA' })).toHaveFocus();
    expect(callbacks.viewCommit).not.toHaveBeenCalled();
    await fireEvent.click(view);
    expect(callbacks.viewCommit).toHaveBeenCalledWith(main.sha);
  });
});
