import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import ProjectDetail from './ProjectDetail.svelte';
import { store, type ProjectInfo } from '$lib/store.svelte';
import { nav } from '$lib/nav.svelte';
import { projectGitState } from '$lib/projectgit.svelte';
const api = vi.hoisted(() => ({ Snapshot: vi.fn(), Identities: vi.fn(), Stashes: vi.fn(), Diff: vi.fn() }));
vi.mock('@bindings/desktop', () => ({ ProjectGitService: api }));
let serial = 0, name = '';
const sha = 'a'.repeat(40);
const snapshot = () => ({ path:'/repo',branch:'main',head:sha,upstream:'origin/main',hasMore:false,commits:[{sha,parents:[],subject:'Selected commit',author:'Martin',email:'',date:'2026-11-12T10:00:00Z',refs:[]}],refs:[],remotes:[],worktrees:[],files:[],signing:{enabled:false,format:'',key:''} });
function project(): ProjectInfo { return { name, label:'Project',path:'/repo',repo:'acme/project',defaultBranch:'main',kind:'',agent:'claude',agentBin:'claude',agentOk:true,pathOk:true,repoConfigured:true,pollCount:1,pollsEnabled:1,lastRun:'',sessions:0,liveCounted:0,needsYou:0,ciRed:0,openPrs:0 } as ProjectInfo; }
beforeEach(() => {
  name = `refresh-project-${++serial}`;
  api.Snapshot.mockReset().mockResolvedValue(snapshot()); api.Identities.mockResolvedValue({authors:{}}); api.Stashes.mockResolvedValue([]); api.Diff.mockResolvedValue({files:[]});
  store.projects=[project()];store.sessions=[];store.status=null;store.alive=true;store.connected=false;
  nav.goDetail(name,'git');nav.sidebarOpen=true;
});
afterEach(cleanup);
describe('Project Git background updates', () => {
  it('keeps the selected commit when daemon project objects are replaced', async () => {
    render(ProjectDetail);
    const row = await screen.findByRole('button',{name:/Selected commit,/});
    await fireEvent.click(row);
    expect(row).toHaveAttribute('aria-pressed','true');
    const calls = api.Snapshot.mock.calls.length;
    store.projects=[{...project(),lastRun:'new polling timestamp',sessions:1}];
    await tick(); await tick();
    expect(screen.getByRole('button',{name:/Selected commit,/})).toHaveAttribute('aria-pressed','true');
    expect(api.Snapshot).toHaveBeenCalledTimes(calls);
  });
  it('preserves comparison endpoints and the explicitly opened file across repeated daemon updates', async () => {
    const olderSHA = 'b'.repeat(40);
    const history = snapshot();
    history.commits.push({ ...history.commits[0], sha: olderSHA, subject: 'Earlier commit' });
    api.Snapshot.mockResolvedValue(history);
    api.Diff.mockResolvedValue({ files: [
      { path: 'src/first.ts', status: 'modified', additions: 1, deletions: 0, patch: '@@ -1 +1,2 @@\n old first\n+new first\n' },
      { path: 'src/second.ts', status: 'modified', additions: 1, deletions: 0, patch: '@@ -1 +1,2 @@\n old second\n+new second\n' },
    ] });
    render(ProjectDetail);
    await fireEvent.click(await screen.findByRole('button', { name: /Earlier commit,/ }));
    await fireEvent.click(screen.getByRole('button', { name: /Selected commit,/ }), { metaKey: true });
    await waitFor(() => expect(api.Diff).toHaveBeenLastCalledWith(name, '', olderSHA, sha, 'commit', ''));
    expect(screen.queryByRole('region', { name: 'Diff for src/second.ts' })).not.toBeInTheDocument();
    await fireEvent.click(await screen.findByRole('button', { name: /src\/second\.ts/ }));
    expect(await screen.findByRole('region', { name: 'Diff for src/second.ts' })).toHaveTextContent('new second');
    const snapshotCalls = api.Snapshot.mock.calls.length, diffCalls = api.Diff.mock.calls.length;
    for (let update = 1; update <= 3; update++) {
      store.projects = [{ ...project(), lastRun: `poll-${update}`, sessions: update }];
      await tick(); await tick();
      expect(screen.getByRole('button', { name: /Earlier commit,/ })).toHaveAttribute('aria-pressed', 'true');
      expect(screen.getByRole('button', { name: /Selected commit,/ })).toHaveAttribute('aria-pressed', 'true');
      expect(screen.getByRole('heading', { name: 'Compare commits' })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /src\/second\.ts/ })).toHaveAttribute('aria-pressed', 'true');
      expect(screen.getByRole('region', { name: 'Diff for src/second.ts' })).toHaveTextContent('new second');
      expect(api.Snapshot).toHaveBeenCalledTimes(snapshotCalls);
      expect(api.Diff).toHaveBeenCalledTimes(diffCalls);
    }
    // The visible direction still agrees with the original modifier selection.
    expect(screen.getByText(`${olderSHA.slice(0, 8)} → ${sha.slice(0, 8)}`)).toBeInTheDocument();
  });
  it('clears selection when the user actually chooses another project', async () => {
    render(ProjectDetail);
    await fireEvent.click(await screen.findByRole('button',{name:/Selected commit,/}));
    store.projects=[project(), {...project(), name: name + '-next'}];
    nav.goDetail(name + '-next', 'git');
    await tick(); await tick();
    expect(screen.getByRole('button',{name:/Selected commit,/})).toHaveAttribute('aria-pressed','false');
    expect(api.Snapshot).toHaveBeenLastCalledWith(name + '-next', '', '', 200);
  });
  it('keeps selection through repository facts and author identity refreshes', async () => {
    render(ProjectDetail);
    const row = await screen.findByRole('button',{name:/Selected commit,/}); await fireEvent.click(row);
    const state = projectGitState(name);
    await state.load();
    state.authors = {[sha]:{login:'martin',avatarUrl:''}};
    await tick();
    expect(screen.getByRole('button',{name:/Selected commit,/})).toHaveAttribute('aria-pressed','true');
    expect(screen.getByRole('heading',{name:'Commit'})).toBeInTheDocument();
  });
});
