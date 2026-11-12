import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import ProjectGit from './ProjectGit.svelte';
import type { GitSnapshot } from '$lib/projectgit';

const api = vi.hoisted(() => ({ Snapshot: vi.fn(), CreateBranch: vi.fn(), CreateWorktree: vi.fn(), Identities: vi.fn(), Diff: vi.fn(), Stage: vi.fn(), Unstage: vi.fn(), Commit: vi.fn(), BranchAction: vi.fn(), Stashes: vi.fn(), StashPush: vi.fn(), StashApply: vi.fn(), StashDrop: vi.fn(), Fetch: vi.fn(), Pull: vi.fn(), Push: vi.fn() }));
vi.mock('@bindings/desktop', () => ({ ProjectGitService: api }));
let serial = 0;
let project: string;
const oldSHA = 'b'.repeat(40), newSHA = 'a'.repeat(40);
function snapshot(overrides: Partial<GitSnapshot> = {}): GitSnapshot {
  return {
    path: '/repo', branch: 'main', head: newSHA, upstream: 'origin/main', hasMore: false,
    commits: [{ sha: newSHA, parents: [oldSHA], subject: 'Add project workspace', author: 'Martin', email: 'm@example.test', date: '2026-11-12T10:00:00Z', refs: ['main'] }, { sha: oldSHA, parents: [], subject: 'Initial project', author: 'Martin', email: 'm@example.test', date: '2026-11-11T10:00:00Z', refs: [] }],
    refs: [{ name: 'main', fullName: 'refs/heads/main', sha: newSHA, remote: false, current: true }, { name: 'origin/main', fullName: 'refs/remotes/origin/main', sha: oldSHA, remote: true, current: false }],
    worktrees: [{ path: '/repo', branch: 'main', head: newSHA, locked: false, prunable: false, blocked: false }, { path: '/repo-feature', branch: 'feature', head: oldSHA, locked: false, prunable: false, blocked: false }],
    files: [{ path: 'src/app.ts', oldPath: '', index: 'M', worktree: ' ', untracked: false, conflict: false }, { path: 'new.ts', oldPath: '', index: '?', worktree: '?', untracked: true, conflict: false }],
    signing: { enabled: true, format: 'ssh', key: '~/.ssh/signing.pub' }, ...overrides,
  };
}
beforeEach(() => {
  localStorage.removeItem("lola.git.columns");
  vi.stubGlobal('PointerEvent', class extends MouseEvent { pointerId = 1; isPrimary = true; });
  if (!document.elementFromPoint) document.elementFromPoint = () => null;
  project = `project-git-${++serial}`;
  Object.values(api).forEach(fn => fn.mockReset());
  api.Snapshot.mockResolvedValue(snapshot());
  api.Identities.mockResolvedValue({ authors: {} });
  api.Stashes.mockResolvedValue([]);
  api.BranchAction.mockResolvedValue({path:'/repo'});
  api.Diff.mockResolvedValue({ files: [{ path: 'src/app.ts', status: 'modified', additions: 1, deletions: 1, patch: '@@ -1 +1 @@\n-old text\n+new text\n' }] });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
async function open() { render(ProjectGit, { project }); await screen.findByRole('button', { name: /Add project workspace,/ }); }
async function pointerDrag(source: Element, target: Element, release = true) {
  vi.spyOn(document, 'elementFromPoint').mockReturnValue(target);
  await fireEvent.pointerDown(source, { button: 0, clientX: 10, clientY: 10 });
  await fireEvent.pointerMove(window, { clientX: 80, clientY: 100 });
  if (release) await fireEvent.pointerUp(window, { clientX: 80, clientY: 100 });
}


describe('Project Git workspace', () => {
  it('opens publish from Push for a local-only branch without pushing prematurely', async () => {
    api.Snapshot.mockResolvedValue(snapshot({upstream:'',remotes:[{name:'origin',host:'',owner:'',repo:'',avatarUrl:''}]}));
    await open(); await fireEvent.click(screen.getByRole('button',{name:'Push'}));
    expect(await screen.findByRole('form',{name:'Publish branch'})).toBeInTheDocument();
    expect(screen.getByLabelText('Remote branch')).toHaveValue('main');
    expect(api.Push).not.toHaveBeenCalled();expect(api.BranchAction).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole('button',{name:'Cancel'}));
    expect(screen.queryByRole('form',{name:'Publish branch'})).toBeNull();
  });
  it('compares a commit to the working copy through an explicit context action', async () => {
    await open(); await fireEvent.contextMenu(screen.getByRole('button',{name:/Initial project,/}));
    await fireEvent.click(await screen.findByRole('menuitem',{name:'Compare against working changes'}));
    await waitFor(()=>expect(api.Diff).toHaveBeenLastCalledWith(project,'',oldSHA,'','working',''));
    expect(await screen.findByRole('heading',{name:'Compare working changes'})).toBeInTheDocument();
    expect(await screen.findByRole('region',{name:'Diff for src/app.ts'})).toBeInTheDocument();
  });
  it('opens worktree creation from a branch menu without a mutation', async () => {
    await open();await fireEvent.contextMenu(screen.getByRole('button',{name:'main'}));
    await fireEvent.click(await screen.findByRole('menuitem',{name:'Create worktree…'}));
    expect(await screen.findByRole('dialog',{name:'Create worktree'})).toBeInTheDocument();expect(api.CreateWorktree).not.toHaveBeenCalled();
  });
  it('compares modifier-selected commits, swaps direction, and renders a text diff', async () => {
    await open();
    await fireEvent.click(screen.getByRole('button', { name: /Initial project,/ }));
    await fireEvent.click(screen.getByRole('button', { name: /Add project workspace,/ }), { metaKey: true });
    await waitFor(() => expect(api.Diff).toHaveBeenLastCalledWith(project, '', oldSHA, newSHA, 'commit', ''));
    expect(await screen.findByRole('heading', { name: 'Compare commits' })).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'Diff for src/app.ts' })).toBeNull();
    await fireEvent.click(await screen.findByRole('button', {name:'View changes'}));
    expect(await screen.findByRole('region', { name: 'Diff for src/app.ts' })).toHaveTextContent('new text');
    await fireEvent.click(screen.getByRole('button', { name: 'Swap comparison direction' }));
    await waitFor(() => expect(api.Diff).toHaveBeenLastCalledWith(project, '', newSHA, oldSHA, 'commit', ''));
    await fireEvent.click(screen.getByRole('button', { name: 'Close diff' }));
    expect(screen.queryByRole('region', { name:'Selected changes' })).toBeNull();
    expect(screen.getByRole('heading', {name:'Compare commits'})).toBeInTheDocument();
  });
  it('stages literal filenames and retains the commit draft on signing failure', async () => {
    await open();
    await fireEvent.click(screen.getByRole('button', { name: 'Stage new.ts' }));
    await waitFor(() => expect(api.Stage).toHaveBeenCalledWith(project, '', ['new.ts']));
    await waitFor(() => expect(screen.getByLabelText('Commit subject')).toBeEnabled());
    await fireEvent.input(screen.getByLabelText('Commit subject'), { target: { value: 'feat: project workspace' } });
    api.Commit.mockRejectedValue(new Error('SSH signing failed'));
    await fireEvent.click(screen.getByRole('button', { name: 'Commit 1 file' }));
    await screen.findByRole('alert');
    expect(screen.getByLabelText('Commit subject')).toHaveValue('feat: project workspace');
    expect(api.Commit).toHaveBeenCalledExactlyOnceWith(project, '', 'feat: project workspace');
    expect(screen.getByRole('button', {name:'SSH signing enabled'})).toBeInTheDocument();
  });
  it('browses refs without checking them out and routes a configured linked checkout correctly', async () => {
    api.Snapshot.mockResolvedValue(snapshot({ path: '/repo-feature', branch: 'feature' }));
    await open();
    await fireEvent.click(screen.getByRole('button', { name: 'origin/main' }));
    await waitFor(() => expect(api.Snapshot).toHaveBeenLastCalledWith(project, '', 'refs/remotes/origin/main', 200));
    await fireEvent.click(screen.getByRole('button', { name: /Main checkout main/ }));
    await waitFor(() => expect(api.Snapshot).toHaveBeenLastCalledWith(project, '/repo', '', 200));
    await fireEvent.click(screen.getByRole('button', { name: 'feature /repo-feature' }));
    await waitFor(() => expect(api.Snapshot).toHaveBeenLastCalledWith(project, '', '', 200));
  });
  it('does not draw graph connections through filtered history and exposes keyboard navigation', async () => {
    await open();
    const first = screen.getByRole('button', { name: /Add project workspace,/ }); first.focus();
    await fireEvent.keyDown(first, { key: 'ArrowDown' });
    expect(screen.getByRole('button', { name: /Initial project,/ })).toHaveFocus();
    await fireEvent.keyDown(window, { key: 'f', metaKey: true });
    await fireEvent.input(screen.getByRole('textbox', { name: 'Search loaded commits' }), { target: { value: 'Initial' } });
    const history = screen.getByRole('group', { name: /Commits;/ });
    expect(within(history).queryByRole('button', { name: /Add project workspace,/ })).not.toBeInTheDocument();
    expect(history.querySelectorAll('.commit-graph .ancestry-edge')).toHaveLength(0);
  });
  it('aligns real ref tips and checkouts with their commit and groups remote owners', async () => {
    api.Snapshot.mockResolvedValue(snapshot({ remotes: [{ name: 'origin', host: 'github.com', owner: 'acme', repo: 'acme/repo', avatarUrl: 'https://github.com/acme.png?size=40' }] }));
    api.Identities.mockResolvedValue({ authors: { [newSHA]: { login: 'martin', avatarUrl: 'https://avatars.githubusercontent.com/u/42?s=40' } } });
    await open();
    const first = screen.getByRole('button', { name: /Add project workspace,/ });
    const old = screen.getByRole('button', { name: /Initial project,/ });
    expect(first.querySelector('.commit-refs')).toHaveTextContent('main');
    expect(old.querySelector('.commit-refs')).toHaveTextContent('origin/main');
    expect(first.querySelector('.checkout-marker')).not.toHaveAttribute('title');
    await fireEvent.mouseEnter(first.querySelector('.commit-copy')!);
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Checked out here');
    expect(screen.getByRole('tooltip').querySelector('.git-tip-checkout')).toHaveAttribute('aria-label', 'Active checkout: /repo');
    await fireEvent.keyDown(window, {key: 'Escape'});
    expect(within(screen.getByRole('navigation', { name: 'Repository navigation' })).getByText('acme')).toBeInTheDocument();
    await waitFor(() => expect(first.querySelector('img')).toHaveAttribute('src', 'https://avatars.githubusercontent.com/u/42?s=40'));
    await fireEvent.error(first.querySelector('img')!);
    expect(first.querySelector('img')).toBeNull();
    expect(first.querySelector('.graph-avatar')).toHaveTextContent('M');
    await fireEvent.pointerEnter(first);
    expect(old).toHaveClass('lane-traced');
    await fireEvent.click(old);
    const membership = screen.getByText('In branch history').closest('details')!;
    expect(membership).toHaveTextContent('main');
    expect(membership).toHaveTextContent('origin/main');
  });
  it('does not pair a similarly suffixed remote branch with the local tip', async () => {
    const base = snapshot();
    base.refs.push({ name: 'origin/feature/main', fullName: 'refs/remotes/origin/feature/main', sha: newSHA, remote: true, current: false });
    base.remotes = [{ name: 'origin', host: 'github.com', owner: 'acme', repo: 'acme/repo', avatarUrl: '' }];
    api.Snapshot.mockResolvedValue(base);
    await open();
    const row = screen.getByRole('button', { name: /Add project workspace,/ });
    expect(row.querySelector('.paired-remote')).toBeNull();
    expect(row.querySelector('.extra-refs')).toHaveTextContent('+1');
    expect(row).toHaveAccessibleName(/Local branch: main.*Remote branch: origin\/feature\/main.*active checkout/);
  });
  it('collapses branch folders independently and preserves them through refresh and selection', async () => {
    const base = snapshot();
    base.refs.push(
      { name: 'feature/api/login', fullName: 'refs/heads/feature/api/login', sha: newSHA, remote: false, current: false },
      { name: 'origin/feature/api/login', fullName: 'refs/remotes/origin/feature/api/login', sha: newSHA, remote: true, current: false },
    );
    api.Snapshot.mockResolvedValue(base);
    await open();
    const local = screen.getByRole('button', { name: 'Local branch folder feature' });
    const remote = screen.getByRole('button', { name: 'Remote branch folder origin/feature' });
    expect(local).toHaveAttribute('aria-expanded', 'true');
    await fireEvent.click(local);
    expect(screen.queryByRole('button', { name: 'feature/api/login' })).toBeNull();
    expect(screen.getByRole('button', { name: 'origin/feature/api/login' })).toHaveTextContent('login');
    await fireEvent.click(remote);
    await fireEvent.click(screen.getByRole('button', { name: 'Refresh repository' }));
    await waitFor(() => expect(api.Snapshot).toHaveBeenCalledTimes(2));
    expect(screen.getByRole('button', { name: 'Remote branch folder origin/feature' })).toHaveAttribute('aria-expanded', 'false');
    await fireEvent.click(screen.getByRole('button', { name: 'Local branch folder feature' }));
    await fireEvent.click(screen.getByRole('button', { name: 'feature/api/login' }));
    await waitFor(() => expect(api.Snapshot).toHaveBeenLastCalledWith(project, '', 'refs/heads/feature/api/login', 200));
    expect(screen.getByRole('button', { name: 'Remote branch folder origin/feature' })).toHaveAttribute('aria-expanded', 'false');
  });
  it('keeps a clear-filter action after removing the All branches navigation button', async () => {
    await open();
    expect(screen.queryByRole('button', { name: /All branches/ })).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: 'origin/main' }));
    await screen.findByRole('button', { name: 'Clear branch filter' });
    await waitFor(() => expect(api.Snapshot).toHaveBeenLastCalledWith(project, '', 'refs/remotes/origin/main', 200));
    await fireEvent.click(screen.getByRole('button', { name: 'Clear branch filter' }));
    await waitFor(() => expect(api.Snapshot).toHaveBeenLastCalledWith(project, '', '', 200));
  });
  it('shows empty and failed states and refuses commits during conflicts', async () => {
    api.Snapshot.mockResolvedValue(snapshot({ commits: [], files: [{ path: 'conflict.ts', oldPath: '', index: 'U', worktree: 'U', untracked: false, conflict: true }] }));
    render(ProjectGit, { project });
    await screen.findByText('No commits yet');
    await fireEvent.input(screen.getByLabelText('Commit subject'), { target: { value: 'Do not commit conflict' } });
    expect(screen.getByRole('button', { name: 'Commit 1 file' })).toBeDisabled();
    expect(screen.getByText('Resolve conflicted files before committing.')).toBeInTheDocument();
    api.Snapshot.mockRejectedValue(new Error('Repository unavailable'));
    await fireEvent.click(screen.getByRole('button', { name: 'Refresh repository' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Repository unavailable');
  });
  it('places working changes in history and identifies the checked-out local branch', async () => {
    await open();
    const history = screen.getByRole('group', { name: /Commits;/ });
    expect(within(history).getAllByRole('button').find(button => button.hasAttribute('data-commit'))).toHaveAccessibleName('Working changes, 2 files');
    expect(screen.getByRole('button', { name: 'main' })).toHaveAttribute('aria-current', 'true');
    expect(document.querySelector('.checkout-title')).toBeNull();
    expect(history.querySelector('.commit-refs [title]')).toBeNull();
    expect(screen.queryByRole('textbox', { name: 'Search loaded commits' })).toBeNull();
    await fireEvent.keyDown(window, { key: 'f', ctrlKey: true });
    expect(await screen.findByRole('textbox', { name: 'Search loaded commits' })).toHaveFocus();
    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByRole('textbox', { name: 'Search loaded commits' })).toBeNull();
  });
  it('hides the working row after a clean repository refresh', async () => {
    await open();
    expect(screen.getByRole('button', { name: 'Working changes, 2 files' })).toBeInTheDocument();
    api.Snapshot.mockResolvedValue(snapshot({files: []}));
    await fireEvent.click(screen.getByRole('button', { name: 'Refresh repository' }));
    await waitFor(() => expect(screen.queryByRole('button', {name: /Working changes,/})).toBeNull());
    expect(screen.getByRole('button', {name: /Add project workspace,/})).toBeInTheDocument();
  });
  it('persists column visibility and supports keyboard resizing without selecting a commit', async () => {
    await open();
    await fireEvent.click(screen.getByRole('button', { name: 'Configure history columns' }));
    await fireEvent.click(screen.getByRole('menuitemcheckbox', { name: 'Author' }));
    expect(document.querySelector('.commit-row .commit-author')).toBeNull();
    const divider = screen.getByRole('separator', { name: 'Resize Branches / checkouts column' });
    await fireEvent.keyDown(divider, { key: 'ArrowRight' });
    expect(divider).toHaveAttribute('aria-valuenow', '190');
    expect(JSON.parse(localStorage.getItem('lola.git.columns')!)).toEqual({ hidden: ['author'], widths: { refs: 190 } });
    cleanup(); await open();
    expect(document.querySelector('.commit-row .commit-author')).toBeNull();
    expect(screen.getByRole('separator', { name: 'Resize Branches / checkouts column' })).toHaveAttribute('aria-valuenow', '190');
  });
  it('opens branch creation from the toolbar and a commit without creating until submitted', async () => {
    await open();
    await fireEvent.click(screen.getByRole('button', {name:'New branch'}));
    expect(await screen.findByRole('dialog', {name:'New branch'})).toBeInTheDocument();
    expect(screen.getByRole('checkbox', {name:'Switch to new branch'})).toBeChecked();
    expect(api.CreateBranch).not.toHaveBeenCalled();
    await fireEvent.keyDown(screen.getByRole('textbox', {name:'Branch name'}), {key:'Escape'});
    expect(screen.queryByRole('dialog', {name:'New branch'})).toBeNull();
    await fireEvent.contextMenu(screen.getByRole('button', {name:/Initial project,/}));
    await fireEvent.click(await screen.findByRole('menuitem', {name:'Create branch here…'}));
    await fireEvent.input(screen.getByRole('textbox', {name:'Branch name'}), {target:{value:'feature/from-commit'}});
    api.CreateBranch.mockResolvedValue({path:'/repo'});
    await fireEvent.submit(screen.getByRole('textbox', {name:'Branch name'}).closest('form')!);
    await waitFor(() => expect(api.CreateBranch).toHaveBeenCalledWith(project,'','feature/from-commit',oldSHA,true));
    await waitFor(() => expect(screen.queryByRole('dialog', {name:'New branch'})).toBeNull());
  });
  it('opens branch context actions without changing selection and checks out on double-click', async () => {
    await open();
    const branch = screen.getByRole('button', {name:'origin/main'});
    await fireEvent.contextMenu(branch, {clientX:40,clientY:100});
    expect(await screen.findByRole('menuitem', {name:'Check out tracking branch'})).toBeInTheDocument();
    expect(api.BranchAction).not.toHaveBeenCalled();
    await fireEvent.keyDown(screen.getByRole('menu', {name:'Git actions'}), {key:'Escape'});
    await fireEvent.doubleClick(branch);
    await waitFor(() => expect(api.BranchAction).toHaveBeenCalledWith(project, '', {action:'checkout',source:'refs/remotes/origin/main',sourceSHA:oldSHA,target:'',targetSHA:''}));
  });
  it('opens a branch drop chooser without mutating Git', async () => {
    await open();
    const source = screen.getByRole('button', {name:'origin/main'}), target = screen.getByRole('button', {name:'main'});
    await pointerDrag(source, target, false);
    expect(target).toHaveClass('branch-drop-target');
    expect(document.querySelector('.branch-drag-ghost')).toHaveTextContent(source.textContent!.replace(/\s+/g, ' ').trim());
    await fireEvent.pointerUp(window, { clientX:80, clientY:100 });
    expect(document.querySelector('.branch-drag-ghost')).toBeNull();
    expect(await screen.findByRole('menuitem', {name:'Merge origin/main into main…'})).toBeInTheDocument();
    expect(api.BranchAction).not.toHaveBeenCalled();
  });
  it('picks up a copy at the original badge size and preserves the pointer grab offset', async () => {
    await open();
    const source = document.querySelector<HTMLElement>('.history-list .ref-label[data-branch-ref="refs/heads/main"]')!;
    vi.spyOn(source, 'getBoundingClientRect').mockReturnValue({left:180,top:90,x:180,y:90,width:130,height:20,right:310,bottom:110,toJSON:()=>({})});
    vi.spyOn(document, 'elementFromPoint').mockReturnValue(source);
    await fireEvent.pointerDown(source, {button:0,clientX:205,clientY:98});
    await fireEvent.pointerMove(window, {clientX:255,clientY:138});
    const copy = document.querySelector<HTMLElement>('.branch-drag-ghost')!;
    expect(copy.style.width).toBe('130px'); expect(copy.style.height).toBe('20px');
    expect(copy.style.left).toBe('230px'); expect(copy.style.top).toBe('130px');
    expect(copy.style.pointerEvents).toBe('none');
    expect(copy.querySelector('.ref-expanded')).toBeNull();
    await fireEvent.pointerCancel(window);
    expect(document.querySelector('.branch-drag-ghost')).toBeNull();
  });
  it.each(['sidebar to graph', 'graph to sidebar', 'sidebar to sidebar'])('opens branch actions for %s drops', async (direction) => {
    await open();
    const sidebarSource = screen.getByRole('button', { name: 'origin/main' });
    const sidebarTarget = screen.getByRole('button', { name: 'main' });
    const graphSource = document.querySelector('.history-list [data-branch-ref="refs/remotes/origin/main"]')!;
    const graphTarget = document.querySelector('.history-list [data-branch-ref="refs/heads/main"]')!;
    const source = direction.startsWith('graph') ? graphSource : sidebarSource;
    const target = direction.endsWith('graph') ? graphTarget : sidebarTarget;
    await pointerDrag(source, target);
    expect(await screen.findByRole('menuitem', { name: 'Merge origin/main into main…' })).toBeInTheDocument();
    expect(api.BranchAction).not.toHaveBeenCalled();
  });
  it('offers normal branch actions when dropped onto its own graph badge', async () => {
    await open();
    const source = screen.getByRole('button', { name: 'main' });
    const badge = document.querySelector('.history-list [data-branch-ref="refs/heads/main"]')!;
    await pointerDrag(source, badge);
    expect(await screen.findByRole('menuitem', { name: 'Pull changes' })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: /Merge main into main/ })).not.toBeInTheDocument();
    expect(api.BranchAction).not.toHaveBeenCalled();
  });
  it.each(['Initial project', 'Add project workspace'])('does not treat the %s commit row as a branch drop target', async (subject) => {
    const base = snapshot();
    base.refs = [base.refs[0], { name: 'feature', fullName: 'refs/heads/feature', sha: newSHA, remote: false, current: false }];
    api.Snapshot.mockResolvedValue(base);
    await open();
    const row = screen.getByRole('button', { name: new RegExp(`^${subject},`) });
    await pointerDrag(screen.getByRole('button', { name: 'main' }), row.querySelector('.graph-cell')!, false);
    expect(row).not.toHaveClass('branch-drop-target');
    expect(document.querySelector('.branch-drop-target')).toBeNull();
    await fireEvent.pointerUp(window, {clientX:80,clientY:100});
    expect(screen.queryByRole('menu', {name:'Git actions'})).toBeNull();
    expect(api.BranchAction).not.toHaveBeenCalled();
  });
  it('cancels pointer drags on Escape and preserves ordinary clicks below the threshold', async () => {
    await open();
    const source = screen.getByRole('button', {name:'origin/main'}), target = screen.getByRole('button', {name:'main'});
    await fireEvent.pointerDown(source, {button:0, clientX:10, clientY:10});
    await fireEvent.pointerMove(window, {clientX:12, clientY:12});
    await fireEvent.pointerUp(window, {clientX:12, clientY:12});
    expect(document.querySelector('.branch-drag-ghost')).toBeNull();
    await fireEvent.click(source);
    await waitFor(() => expect(api.Snapshot).toHaveBeenLastCalledWith(project, '', 'refs/remotes/origin/main', 200));
    await pointerDrag(source, target, false);
    expect(document.querySelector('.branch-drag-ghost')).not.toBeNull();
    await fireEvent.keyDown(window, {key:'Escape'});
    await fireEvent.pointerUp(window, {clientX:80, clientY:100});
    expect(document.querySelector('.branch-drag-ghost')).toBeNull();
    expect(screen.queryByRole('menu', {name:'Git actions'})).toBeNull();
    expect(api.BranchAction).not.toHaveBeenCalled();
  });
  it('suppresses the release click and clears drag listeners on cancellation and unmount', async () => {
    await open();
    const source = screen.getByRole('button', {name:'origin/main'}), target = screen.getByRole('button', {name:'main'});
    await pointerDrag(source, target);
    api.Snapshot.mockClear();
    await fireEvent.click(source);
    expect(api.Snapshot).not.toHaveBeenCalled();
    expect(screen.getByRole('menu', {name:'Git actions'})).toBeInTheDocument();
    await fireEvent.keyDown(window, {key:'Escape'});
    await pointerDrag(source, target, false);
    await fireEvent.pointerCancel(window);
    expect(document.querySelector('.branch-drag-ghost')).toBeNull();
    await pointerDrag(source, target, false);
    cleanup();
    await fireEvent.pointerUp(window, {clientX:80, clientY:100});
    expect(document.querySelector('.branch-drag-ghost')).toBeNull();
    expect(screen.queryByRole('menu', {name:'Git actions'})).toBeNull();
    expect(api.BranchAction).not.toHaveBeenCalled();
  });
  it('selects a commit without displaying a diff until a file is opened', async () => {
    await open();
    await fireEvent.click(screen.getByRole('button', {name:/Initial project,/}));
    expect(screen.queryByRole('region', {name:'Selected changes'})).toBeNull();
    const changed = await screen.findByRole('button', {name:/src\/app.ts.*\+1/});
    await fireEvent.click(changed);
    expect(await screen.findByRole('region', {name:'Selected changes'})).toBeInTheDocument();
  });
  it('distinguishes an unavailable repository from a repository with no commits', async () => {
    api.Snapshot.mockRejectedValue(new Error('Folder does not exist'));
    render(ProjectGit, { project });
    await screen.findByRole('alert');
    expect(screen.getByText('Repository unavailable')).toBeInTheDocument();
    expect(screen.queryByText('No commits yet')).not.toBeInTheDocument();
    expect(screen.queryByText('Stage your files and create the first commit.')).not.toBeInTheDocument();
    api.Snapshot.mockResolvedValue(snapshot());
  api.Identities.mockResolvedValue({ authors: {} });
    await fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await screen.findByRole('button', { name: /Add project workspace,/ });
    expect(screen.queryByText('Repository unavailable')).not.toBeInTheDocument();
  });
});
