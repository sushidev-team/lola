import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AUTO_FETCH_INTERVAL, ProjectGitState } from './projectgit.svelte';
const api = vi.hoisted(() => ({ BranchPullRequests: vi.fn(), Stashes: vi.fn(), CreateBranch: vi.fn(), CreateWorktree: vi.fn(), BranchAction: vi.fn(), StashPush: vi.fn(), StashApply: vi.fn(), StashDrop: vi.fn(), Uncommit: vi.fn(), Snapshot: vi.fn(), CachedIdentities: vi.fn(), Identities: vi.fn(), Diff: vi.fn(), Stage: vi.fn(), Unstage: vi.fn(), Commit: vi.fn(), Amend: vi.fn(), LastCommitMessage: vi.fn(), Fetch: vi.fn(), Pull: vi.fn(), Push: vi.fn() }));
vi.mock('@bindings/desktop', () => ({ ProjectGitService: api }));
function deferred<T>() { let resolve!: (v: T) => void; const promise = new Promise<T>(r => resolve = r); return { promise, resolve }; }
const snapshot = (branch: string) => ({ path: '/repo', branch, head: 'a', upstream: '', commits: [], refs: [], worktrees: [], files: [], signing: { enabled: false, format: '', key: '' }, hasMore: false });
beforeEach(() => { Object.values(api).forEach(fn => fn.mockReset()); api.Stashes.mockResolvedValue([]); api.BranchPullRequests.mockResolvedValue([]); api.Snapshot.mockResolvedValue(snapshot('main')); api.CachedIdentities.mockResolvedValue({ authors: {}, remoteAvatars: {} }); api.Identities.mockResolvedValue({ authors: {} }); });
afterEach(() => { vi.restoreAllMocks(); });

describe('Automatic remote fetching', () => {
  const remote = {name:'origin',host:'github.com',owner:'team',repo:'repo',avatarUrl:''};
  it.each(['automatic','manual','remote'])('keeps %s fetching and its refresh out of the checkout lock', async mode => {
    const state = new ProjectGitState('background-fetch-'+mode,'');
    state.snapshot = {...snapshot('main'),remotes:[remote]};
    const pending = deferred<void>();
    if(mode==='remote') api.BranchAction.mockReturnValueOnce(pending.promise.then(()=>({path:'/repo'})));
    else api.Fetch.mockReturnValueOnce(pending.promise);
    const fetching = mode==='automatic'?state.autoFetch():mode==='manual'?state.act('fetch'):state.branchAction({action:'fetch',source:'origin',sourceSHA:'',target:'',targetSHA:''});
    expect(state.busy).toBe('');
    expect(state.loading).toBe(false);
    expect(state.fetching).toBe(true);
    const refresh = deferred<ReturnType<typeof snapshot>>();
    api.Snapshot.mockReturnValueOnce(refresh.promise);
    pending.resolve();
    await vi.waitFor(()=>expect(api.Snapshot).toHaveBeenCalled());
    expect(state.loading).toBe(false);
    const files=[{path:'work',oldPath:'',index:'A',worktree:' ',untracked:false,conflict:false}];
    api.Stage.mockResolvedValue(files);
    expect(await state.act('stage',['work'])).toBe(true);
    refresh.resolve(snapshot('main')); await fetching;
    expect(state.snapshot!.files).toEqual(files);
    expect(state.fetching).toBe(false);
  });
  it('does not clear an operation lock when a fetch finishes during a commit', async () => {
    const state = new ProjectGitState('background-fetch-commit','');
    state.snapshot = {...snapshot('main'),remotes:[remote]};
    const network = deferred<void>(), commit = deferred<void>();
    api.Fetch.mockReturnValueOnce(network.promise);
    api.Commit.mockReturnValueOnce(commit.promise);
    const fetching = state.fetch();
    const committing = state.act('commit');
    network.resolve(); await fetching;
    expect(state.busy).toBe('commit');
    expect(api.Snapshot).not.toHaveBeenCalled();
    commit.resolve(); await committing;
    expect(state.busy).toBe('');
  });
  it('does not replace a newer action failure when a manual fetch fails', async () => {
    const state = new ProjectGitState('background-fetch-error','');
    let fail!: (error:Error)=>void;
    api.Fetch.mockReturnValueOnce(new Promise((_,reject)=>{fail=reject;}));
    const fetching = state.fetch();
    state.error = 'Push rejected';
    state.failedAction = {action:'push',source:'refs/heads/main',sourceSHA:'a',target:'',targetSHA:''};
    fail(new Error('Offline')); await fetching;
    expect(state.error).toBe('Push rejected');
    expect(state.failedAction.action).toBe('push');
    expect(state.autoFetchError).toContain('Offline');
  });
  it('throttles across worktrees, avoids overlapping requests, and preserves recovery and drafts', async () => {
    let now = 1_000;
    vi.spyOn(Date,'now').mockImplementation(() => now);
    const state = new ProjectGitState('auto-fetch-shared','');
    const linked = new ProjectGitState('auto-fetch-shared','/linked');
    state.snapshot = {...snapshot('main'),remotes:[remote]};
    linked.snapshot = {...snapshot('other'),remotes:[remote]};
    state.message = 'My draft';
    state.error = 'Push rejected';
    state.failedAction = {action:'push',source:'refs/heads/main',sourceSHA:'a',target:'',targetSHA:''};
    const pending = deferred<void>();
    api.Fetch.mockReturnValueOnce(pending.promise);
    const fetching = state.autoFetch();
    await linked.autoFetch();
    expect(api.Fetch).toHaveBeenCalledTimes(1);
    pending.resolve(); await fetching;
    expect(state.error).toBe('Push rejected');
    expect(state.failedAction?.action).toBe('push');
    expect(state.message).toBe('My draft');
    now += AUTO_FETCH_INTERVAL - 1;
    await linked.autoFetch();
    expect(api.Fetch).toHaveBeenCalledTimes(1);
    now++;
    await linked.autoFetch();
    expect(api.Fetch).toHaveBeenCalledTimes(2);
    expect(api.Fetch).toHaveBeenLastCalledWith('auto-fetch-shared','/linked');
  });
  it('backs off failures and resets the interval after success', async () => {
    let now = 10_000;
    vi.spyOn(Date,'now').mockImplementation(() => now);
    const state = new ProjectGitState('auto-fetch-retry','');
    state.snapshot = {...snapshot('main'),remotes:[remote]};
    api.Snapshot.mockResolvedValue(state.snapshot);
    api.Fetch.mockRejectedValueOnce(new Error('Offline')).mockRejectedValueOnce(new Error('Offline'));
    await state.autoFetch();
    expect(state.autoFetchError).toContain('Offline');
    expect(state.error).toBe('');
    now += AUTO_FETCH_INTERVAL;
    await state.autoFetch(); expect(api.Fetch).toHaveBeenCalledTimes(1);
    now += AUTO_FETCH_INTERVAL;
    await state.autoFetch(); expect(api.Fetch).toHaveBeenCalledTimes(2);
    now += AUTO_FETCH_INTERVAL * 2;
    await state.autoFetch(); expect(api.Fetch).toHaveBeenCalledTimes(2);
    now += AUTO_FETCH_INTERVAL * 2;
    await state.autoFetch(); expect(api.Fetch).toHaveBeenCalledTimes(3);
    expect(state.autoFetchError).toBe('');
    now += AUTO_FETCH_INTERVAL;
    await state.autoFetch(); expect(api.Fetch).toHaveBeenCalledTimes(4);
  });
  it.each(['no remote','busy','loading','merge'])('skips auto-fetch while %s', async reason => {
    const state = new ProjectGitState('auto-fetch-skip-'+reason,'');
    state.snapshot = {...snapshot('main'),remotes:reason==='no remote'?[]:[remote]};
    state.busy = reason==='busy'?'commit':'';
    state.loading = reason==='loading';
    state.snapshot.operation = reason==='merge'?'merge':undefined;
    await state.autoFetch();
    expect(api.Fetch).not.toHaveBeenCalled();
  });
});

describe('Project Git request and operation ownership', () => {
  it('keeps both merge branches busy when an overlapping branch fetch finishes, then clears them on failure', async () => {
    const state = new ProjectGitState('merge-and-fetch', '');
    const source = 'refs/remotes/origin/main', target = 'refs/heads/main';
    const fetch = deferred<{path: string}>();
    let rejectMerge!: (error: Error) => void;
    api.BranchAction.mockReturnValueOnce(fetch.promise)
      .mockReturnValueOnce(new Promise((_, reject) => { rejectMerge = reject; }));
    const fetching = state.fetch('origin', false, source);
    const merging = state.branchAction({action:'merge', source, target, sourceSHA:'a', targetSHA:'b'});
    expect(state.fetchingBranchRef).toBe(source);
    expect(state.busyBranchRefs).toEqual([source, target]);
    fetch.resolve({path:'/repo'});
    await fetching;
    expect(state.fetchingBranchRef).toBe('');
    expect(state.busyBranchRefs).toEqual([source, target]);
    rejectMerge(new Error('merge conflict'));
    expect(await merging).toBeNull();
    expect(state.busyBranchRefs).toEqual([]);
  });
  it('tracks the deleting branch and preserves a newer history selection during deletion', async () => {
    const state = new ProjectGitState('lola','');
    await state.load('refs/heads/old',200);
    const pending=deferred<{path:string}>();api.BranchAction.mockReturnValueOnce(pending.promise);
    const removing=state.branchAction({action:'delete-branch',source:'refs/heads/old',sourceSHA:'a',target:'',targetSHA:''},'',200);
    expect(state.busyBranchRefs).toEqual(['refs/heads/old']);
    await state.load('refs/heads/other',400);
    pending.resolve({path:'/repo'});await removing;
    expect(api.Snapshot).toHaveBeenLastCalledWith('lola','','refs/heads/other',400);
    expect(state.busyBranchRefs).toEqual([]);
  });
  it('adds an unloaded branch tip while retaining the full graph', async () => {
    const state = new ProjectGitState('lola','');
    const commit={sha:'a',parents:[],subject:'Current',author:'A',email:'a@test',date:'',refs:[]};
    state.snapshot={...snapshot('main'),commits:[commit]};
    api.Snapshot.mockResolvedValue({...snapshot('old'),commits:[{...commit,sha:'b',subject:'Older tip'}]});
    expect(await state.revealBranchTip({name:'old',fullName:'refs/heads/old',sha:'b',remote:false,current:false})).toBe('b');
    expect(state.snapshot.commits.map(c=>c.sha)).toEqual(['a','b']);
    expect(api.Snapshot).toHaveBeenCalledWith('lola','','refs/heads/old',1);
  });
  it('stages and unstages with authoritative file updates without reloading history or identities',async()=>{
    const state=new ProjectGitState('lola','/tree');await state.load('refs/heads/main',400);
    const commits=state.snapshot!.commits,refs=state.snapshot!.refs;
    const staged=[{path:'a.ts',oldPath:'',index:'A',worktree:' ',untracked:false,conflict:false}];
    api.Stage.mockResolvedValue(staged);api.Unstage.mockResolvedValue([{...staged[0],index:'?',worktree:'?',untracked:true}]);
    api.Snapshot.mockClear();api.Stashes.mockClear();api.CachedIdentities.mockClear();api.Identities.mockClear();
    expect(await state.act('stage',['a.ts'],'refs/heads/main',400)).toBe(true);
    expect(state.snapshot!.files).toEqual(staged);expect(state.snapshot!.commits).toBe(commits);expect(state.snapshot!.refs).toBe(refs);
    expect(await state.act('unstage',['a.ts'],'refs/heads/main',400)).toBe(true);expect(state.snapshot!.files[0].untracked).toBe(true);
    expect(api.Snapshot).not.toHaveBeenCalled();expect(api.Stashes).not.toHaveBeenCalled();expect(api.CachedIdentities).not.toHaveBeenCalled();expect(api.Identities).not.toHaveBeenCalled();expect(state.busy).toBe('');
  });
  it('does not let an overlapping history refresh undo the returned index state',async()=>{
    const state=new ProjectGitState('lola','');await state.load();
    const old=deferred<ReturnType<typeof snapshot>>();api.Snapshot.mockReturnValueOnce(old.promise);
    const loading=state.load();
    const files=[{path:'a.ts',oldPath:'',index:'A',worktree:' ',untracked:false,conflict:false}];api.Stage.mockResolvedValue(files);
    await state.act('stage',['a.ts']);old.resolve({...snapshot('new-branch'),files:[]});await loading;
    expect(state.snapshot!.branch).toBe('new-branch');expect(state.snapshot!.files).toEqual(files);
  });
  it('keeps the index action locked until its status arrives and reconciles partial failures',async()=>{
    const state=new ProjectGitState('lola','');await state.load();state.message='Preserve draft';
    const pending=deferred<[]>();api.Stage.mockReturnValueOnce(pending.promise);
    const operation=state.act('stage',['a']);expect(state.busy).toBe('stage');expect(await state.act('unstage',['a'])).toBe(false);
    pending.resolve([]);await operation;
    api.Stage.mockRejectedValueOnce(new Error('Clean filter failed'));api.Snapshot.mockClear();
    expect(await state.act('stage',['a'],'refs/heads/main',400)).toBe(false);
    expect(api.Snapshot).toHaveBeenCalledWith('lola','','refs/heads/main',400);expect(state.error).toContain('Clean filter failed');expect(state.message).toBe('Preserve draft');
  });
  it('serializes worktree creation, preserves the view, and refreshes failed post-checkout facts', async () => {
    const state=new ProjectGitState('lola','/tree');
    const pending=deferred<{path:string}>();api.CreateWorktree.mockReturnValueOnce(pending.promise);
    const creating=state.createWorktree('refs/heads/main','a'.repeat(40),'/parallel','feature/parallel','refs/heads/main',400);
    expect(state.busy).toBe('create-worktree');
    expect(await state.createWorktree('refs/heads/main','a'.repeat(40),'/other','other')).toBeNull();expect(api.CreateWorktree).toHaveBeenCalledTimes(1);
    pending.resolve({path:'/parallel'});expect(await creating).toBe('/parallel');
    expect(api.Snapshot).toHaveBeenLastCalledWith('lola','/tree','refs/heads/main',400);
    api.CreateWorktree.mockRejectedValue(new Error('post-checkout hook failed'));
    expect(await state.createWorktree('refs/heads/main','a'.repeat(40),'/again','again')).toBeNull();
    expect(state.error).toContain('post-checkout hook failed');expect(state.busy).toBe('');
  });
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
  it('undoes a commit against the exact HEAD and returns its message to an empty commit box', async () => {
    const state = new ProjectGitState('lola', '/tree');
    api.LastCommitMessage.mockResolvedValue('Oops\n\nbody\n'); api.Uncommit.mockResolvedValue(undefined);
    expect(await state.uncommit('a'.repeat(40))).toBe(true);
    expect(api.Uncommit).toHaveBeenCalledWith('lola', '/tree', 'a'.repeat(40));
    expect(state.message).toBe('Oops\n\nbody'); expect(state.notice).toContain('staged'); expect(state.busy).toBe('');
    state.message = 'Keep this draft'; api.Uncommit.mockRejectedValue(new Error('this commit is already on a remote'));
    expect(await state.uncommit('a'.repeat(40))).toBe(false);
    expect(state.message).toBe('Keep this draft'); expect(state.error).toContain('already on a remote');
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

describe('Background branch PR metadata',()=>{
  const remotes=[{name:'origin',repo:'team/repo',host:'github.com',owner:'team',avatarUrl:''}];
  const pr={number:7,title:'Change',url:'https://github.com/team/repo/pull/7',repo:'team/repo',headRepo:'team/repo',head:'topic',base:'main',draft:false};
  it('shares pending lookups across worktrees and throttles without locking the UI',async()=>{
    let now=1000; vi.spyOn(Date,'now').mockImplementation(()=>now);
    const first=new ProjectGitState('pr-shared',''), second=new ProjectGitState('pr-shared','/linked');
    first.snapshot={...snapshot('main'),remotes}; second.snapshot={...snapshot('topic'),remotes};
    const pending=deferred<typeof pr[]>(); api.BranchPullRequests.mockReturnValueOnce(pending.promise);
    const a=first.loadBranchPullRequests(), b=second.loadBranchPullRequests();
    await vi.waitFor(()=>expect(api.BranchPullRequests).toHaveBeenCalledTimes(1));
    expect(first.busy).toBe(''); expect(first.loading).toBe(false); expect(first.fetching).toBe(false);
    pending.resolve([pr]); await Promise.all([a,b]);
    expect(first.branchPullRequests).toEqual([pr]); expect(second.branchPullRequests).toEqual([pr]);
    now+=15000; await first.loadBranchPullRequests(); expect(api.BranchPullRequests).toHaveBeenCalledTimes(1);
    now+=45000; api.BranchPullRequests.mockRejectedValueOnce(new Error('Offline')); await first.loadBranchPullRequests();
    expect(first.branchPullRequests).toEqual([pr]); expect(first.branchPRError).toContain('Offline'); expect(first.error).toBe('');
    api.BranchPullRequests.mockResolvedValueOnce([]); await first.loadBranchPullRequests(true);
    expect(first.branchPullRequests).toEqual([]); expect(first.branchPRError).toBe('');
  });
  it('discards responses for remotes that have changed',async()=>{
    const state=new ProjectGitState('pr-remotes-changed',''); state.snapshot={...snapshot('main'),remotes};
    const pending=deferred<typeof pr[]>(); api.BranchPullRequests.mockReturnValueOnce(pending.promise);
    const request=state.loadBranchPullRequests();
    await vi.waitFor(()=>expect(api.BranchPullRequests).toHaveBeenCalled());
    state.snapshot={...snapshot('main'),remotes:[]}; await state.loadBranchPullRequests();
    pending.resolve([pr]); await request;
    expect(state.branchPullRequests).toEqual([]);
  });
});
