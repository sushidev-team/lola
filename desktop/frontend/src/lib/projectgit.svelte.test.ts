import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ProjectGitState } from './projectgit.svelte';
const api = vi.hoisted(() => ({ Stashes: vi.fn(), CreateBranch: vi.fn(), BranchAction: vi.fn(), StashPush: vi.fn(), StashApply: vi.fn(), StashDrop: vi.fn(), Snapshot: vi.fn(), CachedIdentities: vi.fn(), Identities: vi.fn(), Diff: vi.fn(), Stage: vi.fn(), Unstage: vi.fn(), Commit: vi.fn(), Amend: vi.fn(), LastCommitMessage: vi.fn(), Fetch: vi.fn(), Pull: vi.fn(), Push: vi.fn() }));
vi.mock('@bindings/desktop', () => ({ ProjectGitService: api }));
function deferred<T>() { let resolve!: (v: T) => void; const promise = new Promise<T>(r => resolve = r); return { promise, resolve }; }
const snapshot = (branch: string) => ({ path: '/repo', branch, head: 'a', upstream: '', commits: [], refs: [], worktrees: [], files: [], signing: { enabled: false, format: '', key: '' }, hasMore: false });
beforeEach(() => { Object.values(api).forEach(fn => fn.mockReset()); api.Stashes.mockResolvedValue([]); api.Snapshot.mockResolvedValue(snapshot('main')); api.CachedIdentities.mockResolvedValue({ authors: {}, remoteAvatars: {} }); api.Identities.mockResolvedValue({ authors: {} }); });

describe('Project Git request and operation ownership', () => {
  it('creates at an exact starting commit, holds the action lock, and follows checkout history', async () => {
    const state = new ProjectGitState('lola', '/tree');
    const pending = deferred<{path:string}>(); api.CreateBranch.mockReturnValue(pending.promise);
    const create = state.createBranch('feature/new', 'a'.repeat(40), true, 'refs/heads/old', 400);
    expect(state.busy).toBe('create-branch');
    expect(await state.createBranch('feature/duplicate', 'b'.repeat(40), false)).toBeNull();
    expect(api.CreateBranch).toHaveBeenCalledTimes(1);
    expect(api.CreateBranch).toHaveBeenCalledWith('lola', '/tree', 'feature/new', 'a'.repeat(40), true);
    pending.resolve({path:'/tree'}); expect(await create).toBe('/tree');
    expect(api.Snapshot).toHaveBeenLastCalledWith('lola', '/tree', '', 400);
    expect(state.busy).toBe(''); expect(state.notice).toContain('checked out feature/new');
  });
  it('preserves the history filter without switching and refreshes failures without losing the Git error', async () => {
    const state = new ProjectGitState('lola', '/tree');
    api.CreateBranch.mockResolvedValueOnce({path:'/tree'}).mockRejectedValueOnce(new Error('Branch already exists'));
    expect(await state.createBranch('feature/new', 'a'.repeat(40), false, 'refs/heads/main')).toBe('/tree');
    expect(api.Snapshot).toHaveBeenLastCalledWith('lola', '/tree', 'refs/heads/main', 200);
    expect(await state.createBranch('feature/new', 'a'.repeat(40), true)).toBeNull();
    expect(state.snapshot?.branch).toBe('main');
    expect(state.error).toContain('Branch already exists'); expect(state.busy).toBe('');
  });
  it('ignores an older history response after another ref was selected', async () => {
    const pending = deferred<ReturnType<typeof snapshot>>();
    api.Snapshot.mockReturnValueOnce(pending.promise);
    const state = new ProjectGitState('lola', '');
    const first = state.load('refs/heads/old');
    await state.load('refs/heads/main');
    pending.resolve(snapshot('old')); await first;
    expect(state.snapshot?.branch).toBe('main'); expect(state.loading).toBe(false);
  });
  it('renders local history before optional identities and ignores stale identity replies', async () => {
    const pending = deferred<{ authors: Record<string, { login: string; avatarUrl: string }> }>();
    api.Identities.mockReturnValueOnce(pending.promise).mockRejectedValue(new Error('Offline'));
    api.Snapshot.mockResolvedValue({ ...snapshot('main'), commits: [{ sha: 'a'.repeat(40), parents: [], refs: [] }] });
    const state = new ProjectGitState('lola', '');
    await state.load();
    expect(state.loading).toBe(false);
    expect(state.snapshot?.commits).toHaveLength(1);
    await state.load('refs/heads/other');
    pending.resolve({ authors: { ['a'.repeat(40)]: { login: 'stale', avatarUrl: '' } } });
    await Promise.resolve();
    expect(state.authors).toEqual({});
    expect(state.error).toBe('');
  });
  it('keeps branch rows mounted while another ref loads for double-click checkout', async () => {
    const state = new ProjectGitState('lola', ''); await state.load();
    const pending = deferred<ReturnType<typeof snapshot>>(); api.Snapshot.mockReturnValueOnce(pending.promise);
    const loading = state.load('refs/heads/feature');
    expect(state.snapshot?.branch).toBe('main'); expect(state.loading).toBe(true);
    pending.resolve(snapshot('feature')); await loading;
    expect(state.snapshot?.branch).toBe('feature');
  });
  it('refreshes conflict facts after a failed branch operation while preserving its error', async () => {
    const state = new ProjectGitState('lola', '/tree');
    api.BranchAction.mockRejectedValue(new Error('Merge conflict: resolve the listed files'));
    api.Snapshot.mockResolvedValue({ ...snapshot('main'), operation: 'merge' });
    expect(await state.branchAction({ action: 'merge', source: 'refs/heads/feature', target: 'refs/heads/main', sourceSHA: 'b', targetSHA: 'a' })).toBeNull();
    expect(state.snapshot?.operation).toBe('merge'); expect(state.error).toContain('Merge conflict'); expect(state.busy).toBe('');
  });
  it('passes exact stash identity and retains conflict errors after refreshing', async () => {
    const state = new ProjectGitState('lola', '/tree');
    api.StashApply.mockRejectedValue(new Error('Conflict applying stash; stash kept'));
    expect(await state.stashAction('pop', 'c'.repeat(40), 'a'.repeat(40))).toBe(false);
    expect(api.StashApply).toHaveBeenCalledWith('lola', '/tree', 'c'.repeat(40), 'a'.repeat(40), true);
    expect(state.error).toContain('stash kept'); expect(state.busy).toBe('');
  });
  it('ignores old diff replies, including after closing the diff', async () => {
    const pending = deferred<{ files: { path: string }[] }>(); api.Diff.mockReturnValueOnce(pending.promise).mockResolvedValue({ files: [] });
    const state = new ProjectGitState('lola', '');
    const first = state.loadDiff('commit', '', 'a'); await state.loadDiff('commit', '', 'b');
    pending.resolve({ files: [{ path: 'stale' }] }); await first;
    expect(state.diff?.files).toEqual([]);
    const next = deferred<{ files: [] }>(); api.Diff.mockReturnValueOnce(next.promise);
    const loading = state.loadDiff('staged'); state.clearDiff(); next.resolve({ files: [] }); await loading;
    expect(state.diff).toBeNull(); expect(state.diffLoading).toBe(false);
  });
  it('holds duplicate actions until completion and keeps message on signing failure', async () => {
    const pending = deferred<void>(); api.Stage.mockReturnValue(pending.promise);
    const state = new ProjectGitState('lola', '/repo/tree');
    const stage = state.act('stage', ['a']);
    expect(await state.act('stage', ['a'])).toBe(false); expect(api.Stage).toHaveBeenCalledTimes(1);
    pending.resolve(); await stage; expect(state.busy).toBe('');
    state.message = 'Keep this draft'; api.Commit.mockRejectedValue(new Error('signing failed'));
    expect(await state.act('commit')).toBe(false); expect(state.message).toBe('Keep this draft'); expect(state.error).toContain('signing failed');
  });
  it('isolates in-flight operations from another project and refreshes only their checkout', async () => {
    const pending = deferred<void>(); api.Commit.mockReturnValueOnce(pending.promise);
    const old = new ProjectGitState('old', '/old/tree'), next = new ProjectGitState('next', '');
    old.message = 'Old project commit'; next.message = 'Next draft';
    const commit = old.act('commit'); await next.load(); pending.resolve(); await commit;
    expect(api.Commit).toHaveBeenCalledWith('old', '/old/tree', 'Old project commit');
    expect(next.message).toBe('Next draft'); expect(next.notice).toBe('');
    expect(api.Snapshot).toHaveBeenLastCalledWith('old', '/old/tree', '', 200);
  });
  it('does not overwrite a typed draft or disabled amend choice with a late message response', async () => {
    const pending = deferred<string>(); api.LastCommitMessage.mockReturnValueOnce(pending.promise);
    const state = new ProjectGitState('lola', ''); state.snapshot = snapshot('main');
    const loading = state.setAmend(true); state.subject = 'Human draft';
    pending.resolve('Old commit\n\nOld body'); await loading;
    expect(state.subject).toBe('Human draft'); expect(state.body).toBe('');
    state.message = ''; const late = deferred<string>(); api.LastCommitMessage.mockReturnValueOnce(late.promise);
    const second = state.setAmend(true); await state.setAmend(false); late.resolve('Old commit'); await second;
    expect(state.message).toBe(''); expect(state.amend).toBe(false); expect(state.amendLoading).toBe(false);
  });
  it('retains subject, body and amend choice when amending fails', async () => {
    const state = new ProjectGitState('lola', '/tree'); state.subject = 'fix: amend'; state.body = 'More context'; state.amend = true;
    api.Amend.mockRejectedValue(new Error('HEAD changed'));
    expect(await state.act('amend', [], '', 200, 'a'.repeat(40))).toBe(false);
    expect(api.Amend).toHaveBeenCalledWith('lola', '/tree', 'fix: amend\n\nMore context', 'a'.repeat(40));
    expect(state.subject).toBe('fix: amend'); expect(state.body).toBe('More context'); expect(state.amend).toBe(true);
  });

  it('paints cached authors and remotes before refresh and preserves them if refresh has no image', async () => {
    const sha = 'a'.repeat(40), pending = deferred<{authors: Record<string, {login: string; avatarUrl: string}>; remoteAvatars: Record<string, string>}>();
    api.Snapshot.mockResolvedValue({...snapshot('main'), commits:[{sha,parents:[],refs:[]}], remotes:[{name:'origin',owner:'team',host:'github.com',repo:'team/repo',avatarUrl:''}]});
    api.CachedIdentities.mockResolvedValue({authors:{[sha]:{login:'martin',avatarUrl:'cached-author'}},remoteAvatars:{origin:'cached-owner'}});
    api.Identities.mockReturnValueOnce(pending.promise);
    const state = new ProjectGitState('lola',''); await state.load();
    await vi.waitFor(()=>expect(state.authors[sha]?.avatarUrl).toBe('cached-author'));
    expect(state.remoteAvatars.origin).toBe('cached-owner');expect(state.identityLoading[sha]).toBe(true);expect(state.loading).toBe(false);
    pending.resolve({authors:{[sha]:{login:'martin',avatarUrl:''}},remoteAvatars:{}});
    await vi.waitFor(()=>expect(state.identityLoading).toEqual({}));
    expect(state.authors[sha].avatarUrl).toBe('cached-author');expect(state.remoteAvatars.origin).toBe('cached-owner');
    await state.load();expect(api.CachedIdentities).toHaveBeenCalledTimes(1);expect(api.Identities).toHaveBeenCalledTimes(1);
  });
  it('loads remote-only avatars and stops pending decoration on offline refresh', async () => {
    api.Snapshot.mockResolvedValue({...snapshot('main'), remotes:[{name:'origin',owner:'team',host:'github.com',repo:'team/repo',avatarUrl:''}]});
    api.CachedIdentities.mockResolvedValue({authors:{},remoteAvatars:{origin:'cached-owner'}});api.Identities.mockRejectedValue(new Error('Offline'));
    const state = new ProjectGitState('lola','');await state.load();
    await vi.waitFor(()=>expect(state.remoteIdentityLoading).toBe(false));
    expect(api.CachedIdentities).toHaveBeenCalledWith('lola',[]);expect(api.Identities).toHaveBeenCalledWith('lola',[]);
    expect(state.remoteAvatars.origin).toBe('cached-owner');expect(state.error).toBe('');
  });
  it('ignores stale disk cache replies and does not repeat resolved unknown identities on refresh', async () => {
    const sha='a'.repeat(40),pending=deferred<{authors:Record<string,{login:string;avatarUrl:string}>}>();
    api.Snapshot.mockResolvedValue({...snapshot('main'),commits:[{sha,parents:[],refs:[]}]});
    api.CachedIdentities.mockReturnValueOnce(pending.promise);
    const state=new ProjectGitState('lola','');await state.load();await state.load('refs/heads/other');
    await vi.waitFor(()=>expect(state.identityLoading).toEqual({}));
    pending.resolve({authors:{[sha]:{login:'stale',avatarUrl:'old'}}});await Promise.resolve();
    expect(state.authors).toEqual({});await state.load();
    expect(api.Identities).toHaveBeenCalledTimes(1);expect(api.CachedIdentities).toHaveBeenCalledTimes(2);
  });
  it('clears pending identity flags when a newer snapshot fails', async () => {
    const sha='a'.repeat(40),pending=deferred<{authors:Record<string,{login:string;avatarUrl:string}>}>();
    api.Snapshot.mockResolvedValueOnce({...snapshot('main'),commits:[{sha,parents:[],refs:[]}]}).mockRejectedValue(new Error('Repository unavailable'));
    api.CachedIdentities.mockReturnValueOnce(pending.promise);
    const state=new ProjectGitState('lola','');await state.load();expect(state.identityLoading[sha]).toBe(true);await state.load();
    expect(state.identityLoading).toEqual({});expect(state.remoteIdentityLoading).toBe(false);
    pending.resolve({authors:{}});await Promise.resolve();expect(api.Identities).not.toHaveBeenCalled();
  });

});
