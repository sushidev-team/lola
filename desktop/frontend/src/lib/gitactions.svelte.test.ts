import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
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
let callbacks: { viewHistory: ReturnType<typeof vi.fn>; openWorktree: ReturnType<typeof vi.fn>; viewCommit: ReturnType<typeof vi.fn> };
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
    actions.openBranch(feature, event());
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
    await fireEvent.keyDown(view, { key: 'ArrowDown' });
    expect(screen.getByRole('menuitem', { name: 'Copy commit SHA' })).toHaveFocus();
    expect(callbacks.viewCommit).not.toHaveBeenCalled();
    await fireEvent.click(view);
    expect(callbacks.viewCommit).toHaveBeenCalledWith(main.sha);
  });
});
