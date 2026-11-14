import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { GitActions } from './gitactions.svelte';
import { ProjectGitState, projectGitState } from './projectgit.svelte';
import { confirm } from './confirm.svelte';
import { store } from './store.svelte';
import ProjectGitActionMenu from './components/ProjectGitActionMenu.svelte';
import type { GitRef, GitSnapshot } from './projectgit';
const api = vi.hoisted(() => ({CanFastForward:vi.fn()}));
vi.mock('@bindings/desktop', () => ({ ProjectGitService: api }));
const main: GitRef = { name: 'main', fullName: 'refs/heads/main', sha: 'a'.repeat(40), remote: false, current: true };
const feature: GitRef = { name: 'feature/work', fullName: 'refs/heads/feature/work', sha: 'b'.repeat(40), remote: false, current: false };
let git: ProjectGitState;
let actions: GitActions;
let callbacks: { viewHistory: Mock<(ref: string) => void>; openWorktree: Mock<(path: string) => void>; viewCommit: Mock<(sha: string) => void> };
const event = () => new MouseEvent('contextmenu', { clientX: 15, clientY: 20, cancelable: true });
beforeEach(() => {
  api.CanFastForward.mockReset().mockResolvedValue(false);
  git = new ProjectGitState('project', '');
  git.snapshot = { path: '/repo', branch: 'main', head: main.sha, refs: [main, feature], worktrees: [], commits: [], files: [], signing: { enabled: true, format: 'ssh', key: '' }, upstream: 'origin/main', hasMore: false } satisfies GitSnapshot;
  git.stashes = [{ sha: 'c'.repeat(40), selector: 'stash@{0}', subject: 'Saved work', date: '' }];
  vi.spyOn(git, 'branchAction').mockResolvedValue('/repo');
  vi.spyOn(git, 'stashAction').mockResolvedValue(true);
  callbacks = { viewHistory: vi.fn<(ref: string) => void>(), openWorktree: vi.fn<(path: string) => void>(), viewCommit: vi.fn<(sha: string) => void>() };
  actions = new GitActions(() => ({ git, ref: '', limit: 200 }), callbacks);
  confirm.cancel();
});
afterEach(() => { cleanup(); confirm.cancel(); store.dismissFlash(); vi.restoreAllMocks(); });
describe('Git action menus', () => {
  function unmergedRemoval() {
    const tree = {path:'/linked',branch:feature.name,head:feature.sha,blocked:false,locked:false,prunable:false};
    git.snapshot!.worktrees = [{...tree,path:'/repo',branch:main.name,head:main.sha},tree];
    git.failedAction = {action:'remove-worktree-branch',source:feature.fullName,sourceSHA:feature.sha,target:tree.path,targetSHA:''};
    git.error = 'branch is not fully merged; remove only the worktree to keep its branch';
    return git.failedAction;
  }
  it.each(['remove-worktree','remove-worktree-branch'])('confirms discarded files before forcing %s', async action => {
    const request = unmergedRemoval();
    request.action = action;
    git.error = 'git worktree: failed to delete checkout: Directory not empty';
    actions.recovery()!.run();
    expect(confirm.request?.body).toContain('uncommitted changes and untracked files');
    expect(git.branchAction).not.toHaveBeenCalled();
    confirm.accept();
    await waitFor(() => expect(git.branchAction).toHaveBeenCalledWith({...request,forceRemoveWorktree:true,forceDeleteBranch:action==='remove-worktree-branch'},'',200));
  });
  it('can force a detached worktree without requiring a branch', async () => {
    const request = unmergedRemoval();
    request.action = 'remove-worktree'; request.source = '';
    git.snapshot!.worktrees[1].branch = '';
    git.error = 'worktree contains modified or untracked files';
    actions.recovery()!.run(); confirm.accept();
    await waitFor(() => expect(git.branchAction).toHaveBeenCalledWith({...request,forceRemoveWorktree:true,forceDeleteBranch:false},'',200));
  });
  it('keeps the branch when choosing the alternative after a directory error', async () => {
    const request = unmergedRemoval();
    git.error = 'Directory not empty';
    actions.recovery()!.run(); confirm.acceptAlternative();
    await waitFor(() => expect(git.branchAction).toHaveBeenCalledWith({...request,action:'remove-worktree',forceRemoveWorktree:true,forceDeleteBranch:false},'',200));
  });
  it('retries only the branch after Git unregistered the failed checkout', async () => {
    unmergedRemoval();
    git.snapshot!.worktrees.splice(1);
    git.error = 'git worktree: failed to delete checkout: Directory not empty';
    const recovery = actions.recovery()!;
    expect(recovery.label).toBe('Delete remaining branch…');
    recovery.run();
    expect(confirm.request?.detail).toContain('Any remaining files at /linked are kept');
    expect(git.branchAction).not.toHaveBeenCalled();
    confirm.accept();
    await waitFor(() => expect(git.branchAction).toHaveBeenCalledWith({action:'delete-branch',source:feature.fullName,sourceSHA:feature.sha,target:'',targetSHA:'',forceDeleteBranch:true},'',200));
  });
  it.each([false,true])('refuses branch-only recovery if another worktree checks out that branch (managed: %s)', managed => {
    unmergedRemoval();
    git.snapshot!.worktrees[1].path = '/new-owner';
    if (managed) actions = new GitActions(() => ({git,ref:'',limit:200}), {...callbacks,isManagedWorktree:()=>true,removeManagedWorktree:vi.fn()});
    git.error = 'Directory not empty';
    expect(actions.recovery()).toBeUndefined();
  });
  it('offers confirmed force deletion for an unmerged branch without a worktree', () => {
    unmergedRemoval();
    git.failedAction!.action = 'delete-branch';
    git.snapshot!.worktrees.splice(1);
    const recovery = actions.recovery()!;
    expect(recovery.label).toBe('Force delete branch…');
    recovery.run();
    git.snapshot!.refs = [main,{...feature,sha:'changed'}];
    confirm.accept();
    expect(git.branchAction).not.toHaveBeenCalled();
  });
  it.each(['force','keep','cancel'])('confirms recovery of an unmerged worktree (%s)', async choice => {
    const request = unmergedRemoval();
    const recovery = actions.recovery()!;
    expect(recovery.label).toBe('Force remove…');
    recovery.run();
    expect(git.branchAction).not.toHaveBeenCalled();
    expect(confirm.request?.body).toContain('may lose commits');
    expect(confirm.request?.subject).toContain('/linked');
    if (choice === 'cancel') { confirm.cancel(); expect(git.branchAction).not.toHaveBeenCalled(); return; }
    if (choice === 'force') confirm.accept(); else confirm.acceptAlternative();
    await waitFor(() => expect(git.branchAction).toHaveBeenCalledWith(choice === 'force' ? {...request,forceDeleteBranch:true} : {...request,action:'remove-worktree'}, '', 200));
  });
  it.each(['head','branch','locked','managed','missing','dismissed','context','disposed'])('refuses a stale force confirmation after %s changes', reason => {
    unmergedRemoval();
    actions.recovery()!.run();
    const original = git;
    if (reason === 'head') git.snapshot!.worktrees[1].head = 'new';
    if (reason === 'branch') git.snapshot!.refs = [main,{...feature,sha:'new'}];
    if (reason === 'locked') git.snapshot!.worktrees[1].locked = true;
    if (reason === 'managed') git.snapshot!.worktrees[1].blocked = true;
    if (reason === 'missing') git.snapshot!.worktrees = [];
    if (reason === 'dismissed') git.error = '';
    if (reason === 'context') git = new ProjectGitState('other','');
    if (reason === 'disposed') actions.dispose();
    confirm.accept();
    expect(original.branchAction).not.toHaveBeenCalled();
  });
  it('labels a proven fast-forward with its destination and keeps the pinned merge action', async () => {
    api.CanFastForward.mockResolvedValue(true);
    render(ProjectGitActionMenu, {actions});
    actions.dropBranches(feature, main, event());
    await fireEvent.click(await screen.findByRole('menuitem', {name:'Fast-forward main to feature/work'}));
    expect(api.CanFastForward).toHaveBeenCalledWith('project', '', {action:'merge',source:feature.fullName,sourceSHA:feature.sha,target:main.fullName,targetSHA:main.sha});
    expect(git.branchAction).toHaveBeenCalledWith({action:'merge',source:feature.fullName,sourceSHA:feature.sha,target:main.fullName,targetSHA:main.sha}, '', 200);
  });
  it.each(['dismissed','replaced','changed','disposed'])('ignores a late merge preview when the menu is %s', async reason => {
    let resolve!: (value:boolean) => void;
    api.CanFastForward.mockReturnValueOnce(new Promise<boolean>(r => {resolve=r;}));
    actions.dropBranches(feature, main, event());
    const original = actions.request!;
    if (reason === 'dismissed') actions.close();
    if (reason === 'replaced') actions.dropBranches(main, feature, event());
    if (reason === 'changed') git.snapshot!.refs = [{...main,sha:'new'},feature];
    if (reason === 'disposed') actions.dispose();
    resolve(true);
    await Promise.resolve();
    expect(original.items[0].label).toBe('Merge feature/work into main');
  });
  it('keeps Merge when the preview fails', async () => {
    api.CanFastForward.mockRejectedValue(new Error('Git unavailable'));
    actions.dropBranches(feature, main, event());
    await Promise.resolve();
    expect(actions.request!.items[0].label).toBe('Merge feature/work into main');
  });
  it('offers pull after a rejected push and an explicit merge after a diverged pull', async () => {
    const remote={...main,name:'origin/main',fullName:'refs/remotes/origin/main',remote:true,current:false,sha:'c'.repeat(40)};
    git.snapshot!.refs.push(remote);
    vi.mocked(git.branchAction).mockImplementationOnce(async request=>{git.failedAction=request;git.error='push rejected (non-fast-forward)';return null;});
    actions.push();
    await waitFor(()=>expect(store.flash?.action?.label).toBe('Pull changes'));
    vi.mocked(git.branchAction).mockImplementationOnce(async request=>{git.failedAction=request;git.error='branch has diverged; check it out and merge or rebase the upstream explicitly';return null;});
    store.flash!.action!.run();
    await waitFor(()=>expect(store.flash?.action?.label).toBe('Merge remote changes'));
    expect(git.branchAction).toHaveBeenLastCalledWith(expect.objectContaining({action:'pull',source:main.fullName,sourceSHA:main.sha}),'',200);
    store.flash!.action!.run();
    await waitFor(()=>expect(git.branchAction).toHaveBeenLastCalledWith({action:'merge',source:remote.fullName,sourceSHA:remote.sha,target:main.fullName,targetSHA:main.sha},'',200));
  });
  it.each(['changed','dismissed','disposed'])('does not run a stale toast action after the context is %s', async reason=>{
    git.failedAction={action:'push',source:main.fullName,sourceSHA:main.sha,target:'',targetSHA:''};
    git.error='push rejected (non-fast-forward)';
    const recovery=actions.recovery()!;
    if(reason==='changed') git.snapshot!.refs=[{...main,sha:'new-tip'}];
    if(reason==='dismissed') git.failedAction=null;
    if(reason==='disposed') actions.dispose();
    recovery.run();expect(git.branchAction).not.toHaveBeenCalled();
  });
  it.each([true, false])('deletes a borrowed branch after session cleanup only if it still exists (%s)', async (exists) => {
    const tree = {path:'/managed',branch:feature.name,head:feature.sha,blocked:true,locked:false,prunable:false};
    git.snapshot!.worktrees = [tree];
    const root = projectGitState(git.project, '');
    root.snapshot = {...git.snapshot!,refs:exists ? [feature] : []};
    vi.spyOn(root, 'load').mockResolvedValue();
    const deleteBranch = vi.spyOn(root, 'branchAction').mockResolvedValue('/repo');
    let removed: (() => Promise<void>) | undefined;
    actions = new GitActions(() => ({git,ref:'',limit:200}), {...callbacks,removeManagedWorktree:(_tree,_deleteBranch,onRemoved)=>{removed=onRemoved;}});
    actions.openBranch(feature,event());
    actions.request!.items.find(item=>item.label==='Remove worktree and branch…')!.run();
    expect(deleteBranch).not.toHaveBeenCalled();
    await removed!();
    if (exists) expect(deleteBranch).toHaveBeenCalledWith({action:'delete-branch',source:feature.fullName,sourceSHA:feature.sha,target:'',targetSHA:''},'',200);
    else expect(deleteBranch).not.toHaveBeenCalled();
  });
  it.each([true, false])('routes managed branch and worktree removal to a session dialog (sandbox: %s)', async (blocked) => {
    const tree = {path:'/managed',branch:feature.name,head:feature.sha,blocked,locked:false,prunable:false};
    git.snapshot!.worktrees = [tree];
    const removeManagedWorktree = vi.fn();
    actions = new GitActions(() => ({git,ref:'',limit:200}), {...callbacks,isManagedWorktree:()=>true,removeManagedWorktree});
    render(ProjectGitActionMenu, {actions});
    actions.openBranch(feature, event());
    await fireEvent.click(await screen.findByRole('menuitem', {name:'Delete branch…'}));
    expect(removeManagedWorktree).toHaveBeenCalledWith(tree, true, expect.any(Function));
    expect(actions.request).toBeNull();
    actions.openBranch(feature, event());
    await fireEvent.click(await screen.findByRole('menuitem', {name:'Remove worktree and branch…'}));
    actions.openWorktree(tree, event());
    await fireEvent.click(await screen.findByRole('menuitem', {name:'Remove worktree…'}));
    expect(removeManagedWorktree).toHaveBeenCalledTimes(3);
    expect(git.branchAction).not.toHaveBeenCalled();
  });
  it.each([false, true])('confirms worktree removal (delete branch: %s)', async (deleteBranch) => {
    const tree = {path:'/linked',branch:feature.name,head:feature.sha,blocked:false,locked:false,prunable:false};
    git.snapshot!.worktrees = [{...tree,path:'/repo',branch:'main',head:main.sha}, tree];
    render(ProjectGitActionMenu, {actions});
    actions.openWorktree(tree, event());
    await fireEvent.click(await screen.findByRole('menuitem', {name:deleteBranch ? 'Remove worktree and branch…' : 'Remove worktree…'}));
    expect(confirm.request?.subject).toContain('/linked');
    expect(git.branchAction).not.toHaveBeenCalled();
    confirm.accept();
    await waitFor(() => expect(git.branchAction).toHaveBeenCalledWith({action:deleteBranch ? 'remove-worktree-branch' : 'remove-worktree',source:feature.fullName,sourceSHA:feature.sha,target:'/linked',targetSHA:''}, '', 200));
  });
  it('leaves a displayed worktree before removal and protects main and locked worktrees', async () => {
    const tree = {path:'/linked',branch:feature.name,head:feature.sha,blocked:false,locked:false,prunable:false};
    const mainTree = {...tree,path:'/repo',branch:'main',head:main.sha};
    git.snapshot!.worktrees = [mainTree, tree];
    const root = git;
    render(ProjectGitActionMenu, {actions});
    actions.openWorktree(mainTree, event());
    expect(await screen.findByRole('menuitem', {name:'Remove worktree…'})).toBeDisabled();
    actions.openWorktree({...tree,locked:true}, event());
    await waitFor(() => expect(screen.getByRole('menuitem', {name:'Remove worktree and branch…'})).toBeDisabled());
    git = new ProjectGitState('project', '/linked');
    git.snapshot = {...root.snapshot!,path:'/linked',branch:feature.name};
    vi.spyOn(git, 'branchAction');
    const linked = git;
    callbacks.openWorktree.mockImplementation(() => { git = root; });
    actions.openWorktree(tree, event());
    await waitFor(() => expect(screen.getByRole('menuitem', {name:'Remove worktree…'})).toBeEnabled());
    await fireEvent.click(screen.getByRole('menuitem', {name:'Remove worktree…'}));
    confirm.accept();
    await waitFor(() => expect(root.branchAction).toHaveBeenCalled());
    expect(callbacks.openWorktree).toHaveBeenCalledWith('');
    expect(linked.branchAction).not.toHaveBeenCalled();
  });
  it('confirms deletion of the exact branch and clears its selected history only on success', async () => {
    actions = new GitActions(() => ({git, ref:feature.fullName, limit:200}), callbacks);
    render(ProjectGitActionMenu, {actions});
    actions.openBranch(feature, event());
    await fireEvent.click(await screen.findByRole('menuitem', {name:'Delete branch…'}));
    expect(git.branchAction).not.toHaveBeenCalled();
    expect(confirm.request?.subject).toBe('feature/work');
    confirm.cancel();
    expect(git.branchAction).not.toHaveBeenCalled();
    actions.openBranch(feature, event());
    await fireEvent.click(await screen.findByRole('menuitem', {name:'Delete branch…'}));
    confirm.accept();
    await waitFor(() => expect(callbacks.viewHistory).toHaveBeenCalledWith(''));
    expect(git.branchAction).toHaveBeenCalledWith({action:'delete-branch',source:feature.fullName,sourceSHA:feature.sha,target:'',targetSHA:''}, '', 200);
  });
  it('protects active branches and branches owned by any worktree', async () => {
    render(ProjectGitActionMenu, {actions});
    actions.openBranch(main, event());
    expect(await screen.findByRole('menuitem', {name:'Delete branch…'})).toBeDisabled();
    git.snapshot!.worktrees = [{path:'/other',branch:feature.name,head:feature.sha,blocked:false,locked:false,prunable:false}];
    actions.openBranch(feature, event());
    await waitFor(() => expect(screen.getByRole('menuitem', {name:'Delete branch…'})).toBeDisabled());
  });
  it('names remote deletion explicitly and refuses a confirmation after changing repositories', async () => {
    git.snapshot!.remotes = [{name:'origin',host:'',owner:'',repo:'',avatarUrl:''}];
    render(ProjectGitActionMenu, {actions});
    const remote = {...feature, remote:true, name:'origin/feature/work', fullName:'refs/remotes/origin/feature/work'};
    actions.openBranch(remote, event());
    await fireEvent.click(await screen.findByRole('menuitem', {name:'Delete remote branch…'}));
    expect(confirm.request?.body).toContain('for everyone');
    const previous = git;
    git = new ProjectGitState('other', '');
    confirm.accept();
    expect(previous.branchAction).not.toHaveBeenCalled();
  });
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
    git.snapshot!.operation='rebase';actions.openBranch(main,event());expect(await screen.findByRole('menuitem',{name:'Push branch'})).toBeDisabled();expect(screen.getByRole('menuitem',{name:'Solo branch'})).toBeEnabled();
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
  it('opens a pulled branch’s existing worktree so merge conflicts stay visible', async () => {
    const tracked = {...feature,upstream:'origin/feature/work'};
    const root = git;
    root.snapshot!.worktrees = [{path:'/linked',branch:feature.name,head:feature.sha,blocked:false,locked:false,prunable:false}];
    const linked = new ProjectGitState(git.project, '/linked');
    linked.snapshot = {...root.snapshot!,path:'/linked',branch:feature.name,head:feature.sha,refs:[{...main,current:false},{...tracked,current:true}]};
    vi.spyOn(linked, 'branchAction').mockImplementation(async request => {
      linked.failedAction = request;
      linked.error = 'Automatic merge failed';
      linked.snapshot!.operation = 'merge';
      return null;
    });
    callbacks.openWorktree.mockImplementation(() => {git = linked;});
    actions.pull(tracked);
    await waitFor(() => expect(linked.branchAction).toHaveBeenCalledWith(expect.objectContaining({action:'pull',source:feature.fullName,sourceSHA:feature.sha}),'',200));
    expect(callbacks.openWorktree).toHaveBeenCalledWith('/linked');
    expect(root.branchAction).not.toHaveBeenCalled();
    expect(git.snapshot!.operation).toBe('merge');
  });
  it('pulls an unselected branch without changing the visible checkout', async () => {
    render(ProjectGitActionMenu, { actions });
    actions.openBranch({...feature,upstream:'refs/remotes/origin/feature/work'}, event());
    await fireEvent.click(await screen.findByRole('menuitem', { name: 'Pull changes' }));
    expect(git.branchAction).toHaveBeenCalledWith({ action: 'pull', source: feature.fullName, sourceSHA: feature.sha, target: '', targetSHA: '' }, '', 200);
    await waitFor(() => expect(actions.request).toBeNull());
    expect(callbacks.openWorktree).not.toHaveBeenCalled();
  });
  it.each(['merge', 'rebase'] as const)('runs a pinned %s directly after choosing the drag action', async (action) => {
    render(ProjectGitActionMenu, { actions });
    actions.dropBranches(feature, main, event());
    expect(git.branchAction).not.toHaveBeenCalled();
    await fireEvent.click(await screen.findByRole('menuitem', { name: action === 'merge' ? 'Merge feature/work into main' : 'Rebase feature/work onto main' }));
    expect(confirm.request).toBeNull();
    await waitFor(() => expect(git.branchAction).toHaveBeenCalledWith({ action, source: feature.fullName, sourceSHA: feature.sha, target: main.fullName, targetSHA: main.sha }, '', 200));
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
