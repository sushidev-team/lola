import { beforeEach, expect, it, vi } from 'vitest';
import { ProjectGitState } from './projectgit.svelte';
import type { GitSnapshot } from './projectgit';
const mocks = vi.hoisted(() => ({ snapshot:vi.fn(), open:vi.fn(), say:vi.fn(), sessions:[] as any[] }));
vi.mock('@bindings/desktop', () => ({ ProjectGitService:{Snapshot:mocks.snapshot,Identities:vi.fn(),Stashes:vi.fn()} }));
vi.mock('./store.svelte', () => ({store:{sessions:mocks.sessions,openManual:mocks.open}}));
vi.mock('./talk.svelte', () => ({talk:{say:mocks.say}}));
let state:ProjectGitState;
beforeEach(() => {
  vi.clearAllMocks(); mocks.sessions.length=0;
  state=new ProjectGitState('project','/linked');
  state.snapshot={path:'/linked',head:'abc',branch:'feature',operation:'merge',files:[{path:'conflict.ts',conflict:true}],commits:[],refs:[],worktrees:[]} as unknown as GitSnapshot;
  mocks.snapshot.mockResolvedValue(state.snapshot);
  mocks.open.mockResolvedValue({sessionId:'resolver'});
  mocks.say.mockResolvedValue({delivered:true});
});
it('starts an agent in the exact borrowed checkout with a resolve-only prompt', async () => {
  expect(await state.resolveConflicts()).toBe('resolver');
  expect(mocks.open).toHaveBeenCalledWith(expect.objectContaining({project:'project',location:'checkout',path:'/linked',agent:true}));
  const prompt=mocks.open.mock.calls[0][0].prompt;
  expect(prompt).toContain('stage only the resolved files');
  expect(prompt).toContain('Do not start another merge or rebase, commit, continue, abort, reset, or push');
  expect(state.busy).toBe('');
  await state.resolveConflicts();
  expect(mocks.open).toHaveBeenCalledTimes(1);
  expect(mocks.say).toHaveBeenCalledWith({session:'resolver',text:prompt});
});
it('uses the existing checkout agent through the normal message gate', async () => {
  mocks.sessions.push({id:'existing',worktree:'/linked',agentState:'idle'});
  expect(await state.resolveConflicts()).toBe('existing');
  expect(mocks.say).toHaveBeenCalledWith(expect.objectContaining({session:'existing'}));
  expect(mocks.open).not.toHaveBeenCalled();
});
it.each(['changed','failed','resolved','ambiguous'])('does not start an agent when checkout verification is %s', async reason => {
  if(reason==='changed') mocks.snapshot.mockResolvedValue({...state.snapshot,head:'other'});
  if(reason==='failed') mocks.snapshot.mockRejectedValue(new Error('offline'));
  if(reason==='resolved') mocks.snapshot.mockResolvedValue({...state.snapshot,files:[]});
  if(reason==='ambiguous') mocks.sessions.push({id:'one',worktree:'/linked'},{id:'two',worktree:'/linked'});
  expect(await state.resolveConflicts()).toBeNull();
  expect(mocks.open).not.toHaveBeenCalled();expect(mocks.say).not.toHaveBeenCalled();
  expect(state.busy).toBe('');
});
