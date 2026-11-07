import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { freshSession, store } from './talk.testkit';
import { DaemonService } from '@bindings/desktop';
import BeforeMerge from './BeforeMerge.svelte';
import { confirm } from '$lib/confirm.svelte';

const read = vi.mocked(DaemonService.PRReadiness);
const address = vi.mocked(DaemonService.AddressPRFeedback);
const merge = vi.mocked(DaemonService.MergePR);
const thread = (id: string) => ({ id, path:'src/app.ts', line:12, outdated:false, author:'reviewer', preview:`Finding ${id}`, url:`https://github.com/acme/app/pull/42#discussion_${id}` });
const facts = (over = {}) => ({ repo:'acme/app', number:42, url:'https://github.com/acme/app/pull/42', state:'OPEN', draft:false, mergeable:'MERGEABLE', mergeState:'CLEAN', review:'APPROVED', checks:'pass', head:'abc', threads:[], mergeMethod:'squash', mergeBlocked:'', ...over });
function mount(over = {}) { freshSession({prNumber:42,prUrl:'https://github.com/acme/app/pull/42',...over});return render(BeforeMerge,{session:store.sessions[0]}); }
beforeEach(() => {read.mockReset(); address.mockReset(); merge.mockReset(); read.mockResolvedValue(facts());});
afterEach(() => { cleanup(); confirm.cancel(); vi.restoreAllMocks(); });

it('merges a ready PR directly using the exact displayed head', async () => {
  mount();
  expect(await screen.findByText('No blockers reported.')).toBeInTheDocument();
  expect(screen.getByRole('region', {name:'PR status'})).toBeInTheDocument();
  expect(screen.getByText('All resolved')).toBeInTheDocument();
  expect(screen.queryByRole('button',{name:'Ask agent to address'})).toBeNull();
  read.mockResolvedValue(facts({state:'MERGED'}));
  await fireEvent.click(screen.getByRole('button',{name:'Squash and merge'}));
  expect(confirm.request).toBeNull();
  expect(await screen.findByText('Merged')).toBeInTheDocument();
  expect(merge).toHaveBeenCalledExactlyOnceWith(store.sessions[0].id,42,'acme/app','abc','squash',false);
  expect(screen.queryByRole('button',{name:'Squash and merge'})).toBeNull();
});
it('previews three threads and expands older ones; queues without resolving optimistically', async () => {
  read.mockResolvedValue(facts({threads:['a','b','c','d'].map(thread)}));
  address.mockResolvedValue({item:'msg',delivered:false});
  const open=vi.spyOn(store,'openURL').mockResolvedValue(undefined);
  mount();
  await screen.findByText('4 unresolved');
  expect(screen.queryByText('Finding d')).toBeNull();
  await fireEvent.click(screen.getByRole('button',{name:'Show 1 more'}));
  await fireEvent.click(screen.getByRole('button',{name:'Open review thread: Finding d'}));
  expect(open).toHaveBeenCalledWith('https://github.com/acme/app/pull/42#discussion_d');
  await fireEvent.click(screen.getByRole('button',{name:'Ask agent to address'}));
  expect(await screen.findByRole('button', {name:'Requested'})).toBeDisabled();
  expect(screen.queryByText(/Sent to agent|Queued for agent/)).toBeNull();
  expect(address).toHaveBeenCalledWith(store.sessions[0].id,42);
  expect(screen.getByText('4 unresolved')).toBeInTheDocument();
  expect(screen.getByRole('button',{name:'Requested'})).toBeDisabled();
});
it('does not show failed reads or missing CI as a clean PR', async () => {
  read.mockRejectedValue(new Error('offline'));mount();
  await screen.findByText(/Could not refresh GitHub/);
  expect(screen.queryByText('All resolved')).toBeNull();
  read.mockResolvedValue(facts({checks:'none',review:'',mergeable:'UNKNOWN',mergeState:'UNKNOWN'}));
  await fireEvent.click(screen.getByRole('button',{name:'Retry'}));
  expect(await screen.findByText('No checks')).toBeInTheDocument();
  expect(screen.getByText('No review')).toBeInTheDocument();
  expect(screen.getByText('Confirm remaining merge requirements on GitHub.')).toBeInTheDocument();
});
it('uses app lanes and drops late responses from another PR', async () => {
  let release!: (v:ReturnType<typeof facts>)=>void;
  read.mockImplementationOnce(()=>new Promise(resolve=>release=resolve) as never);
  read.mockResolvedValueOnce(facts({number:9,checks:'fail',threads:[thread('second')]}));
  mount({stack:true,focusApp:'web',apps:[{name:'web',label:'Website',number:42},{name:'api',label:'API',number:9}]});
  await fireEvent.click(screen.getByRole('tab',{name:'API'}));
  await screen.findByText('Fix failing CI checks.');
  release(facts());
  await waitFor(()=>expect(read).toHaveBeenCalledTimes(2));
  expect(screen.getByText('Finding second')).toBeInTheDocument();
  expect(screen.queryByText('No blockers reported.')).toBeNull();
  expect(read).toHaveBeenLastCalledWith(`${store.sessions[0].id}~api`,9);
});
it('allows retry after sending fails and hides actions for merged PRs', async () => {
  read.mockResolvedValue(facts({threads:[thread('a')]}));
  address.mockRejectedValueOnce(new Error('cannot send'));
  mount();await screen.findByText('1 unresolved');
  await fireEvent.click(screen.getByRole('button',{name:'Ask agent to address'}));
  await screen.findByText(/cannot send/);
  expect(screen.getByRole('button',{name:'Ask agent to address'})).toBeEnabled();
  read.mockResolvedValue(facts({state:'MERGED'}));
  await fireEvent.click(screen.getByRole('button',{name:'Refresh PR status'}));
  await screen.findByText('Merged');
  expect(screen.queryByRole('button',{name:'Ask agent to address'})).toBeNull();
});
it('prioritizes conflicts and exposes draft status', async () => {
  read.mockResolvedValue(facts({mergeable:'CONFLICTING',checks:'fail'}));mount();
  await screen.findByText('Resolve branch conflicts before merging.');
  read.mockResolvedValue(facts({draft:true}));
  await fireEvent.click(screen.getByRole('button',{name:'Refresh PR status'}));
  await screen.findByText('Draft · mark ready on GitHub when ready for review.');
});

it('groups feedback with independent reviewer disclosures and no repeated author labels', async () => {
  read.mockResolvedValue(facts({threads:[thread('a'), {...thread('b'), author:'another-reviewer'}, thread('c')]}));
  mount();
  const group = await screen.findByRole('region', {name:'Feedback from reviewer'});
  expect(within(group).getByText('Finding a')).toBeInTheDocument();
  expect(within(group).getByText('Finding c')).toBeInTheDocument();
  expect(within(group).queryByText('Finding b')).toBeNull();
  expect(within(group).getByLabelText('2 unresolved threads')).toBeInTheDocument();
  expect(within(screen.getByRole('region', {name:'Feedback from another-reviewer'})).getByText('Finding b')).toBeInTheDocument();
  expect(screen.getAllByText('reviewer')).toHaveLength(1);
  expect(screen.queryByRole('button',{name:/Review feedback/})).toBeNull();
  expect(screen.queryByText('Validate and address the open feedback.')).toBeNull();
  const toggle = within(group).getByRole('button', {name:/reviewer 2 unresolved threads/});
  await fireEvent.click(toggle);
  expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await waitFor(() => expect(within(group).queryByText('Finding a')).toBeNull());
  expect(within(group).getByLabelText('2 unresolved threads')).toBeInTheDocument();
  expect(screen.getByText('Finding b')).toBeInTheDocument();
  await fireEvent.click(screen.getByRole('button', {name:'Refresh PR status'}));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(2));
  expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await fireEvent.click(toggle);
  expect(toggle).toHaveAttribute('aria-expanded', 'true');
  expect(await within(group).findByText('Finding a')).toBeInTheDocument();
});

it('keeps an icon action for blockers, and disables only stale or unsupported requests', async () => {
  read.mockResolvedValue(facts({mergeBlocked:'Resolve the open review threads before merging.'}));
  mount();
  const button = await screen.findByRole('button',{name:'Merge pull request'});
  expect(button).toBeEnabled();
  expect(button.textContent?.trim()).toBe('');
  await fireEvent.click(button);
  expect(confirm.request?.body).toBe('Resolve the open review threads before merging.');
  expect(merge).not.toHaveBeenCalled();
  confirm.cancel();
  read.mockResolvedValue(facts());
  await fireEvent.click(screen.getByRole('button',{name:'Refresh PR status'}));
  await waitFor(()=>expect(screen.getByRole('button',{name:'Squash and merge'})).toBeEnabled());
  read.mockRejectedValue(new Error('offline'));
  await fireEvent.click(screen.getByRole('button',{name:'Refresh PR status'}));
  await screen.findByText(/Status may be outdated/);
  expect(screen.getByRole('button',{name:'Squash and merge'})).toBeDisabled();
  read.mockResolvedValue(facts({mergeMethod:undefined,mergeBlocked:undefined}));
  await fireEvent.click(screen.getByRole('button',{name:'Retry'}));
  expect(await screen.findByRole('button',{name:'Merge pull request'})).toBeDisabled();
});

it('shows merge errors and refreshes without claiming success', async () => {
  merge.mockRejectedValue(new Error('branch protection refused'));
  mount();
  await fireEvent.click(await screen.findByRole('button',{name:'Squash and merge'}));
  await screen.findByText(/Could not merge:.*branch protection refused/);
  await waitFor(()=>expect(read).toHaveBeenCalledTimes(2));
  expect(screen.queryByText('Merged')).toBeNull();
  expect(screen.getByRole('button',{name:'Squash and merge'})).toBeEnabled();
});

it('prevents duplicate requests and waits for GitHub to report merged', async () => {
  let release!:()=>void;
  merge.mockImplementationOnce(()=>new Promise<void>(resolve=>release=resolve) as never);
  mount();
  await fireEvent.click(await screen.findByRole('button',{name:'Squash and merge'}));
  expect(await screen.findByRole('button',{name:'Merging…'})).toBeDisabled();
  release();
  expect(await screen.findByRole('button',{name:'Merge requested'})).toBeDisabled();
  expect(screen.queryByText('Merged')).toBeNull();
  expect(merge).toHaveBeenCalledTimes(1);
});

it('invalidates a confirmation when its head changes', async () => {
  read.mockResolvedValue(facts({mergeBlocked:'CI is running.'}));
  mount();
  await fireEvent.click(await screen.findByRole('button',{name:'Merge pull request'}));
  read.mockResolvedValue(facts({head:'changed'}));
  await fireEvent.click(screen.getByRole('button',{name:'Refresh PR status'}));
  await waitFor(()=>expect(screen.getByRole('button',{name:'Squash and merge'})).toBeEnabled());
  confirm.accept();
  expect(merge).not.toHaveBeenCalled();
});

it('pins stack lane actions and drops confirmations after switching lanes', async () => {
  read.mockImplementation((_id, number)=>Promise.resolve(facts({number,mergeMethod:'rebase',mergeBlocked:'CI is running.'})) as never);
  mount({stack:true,focusApp:'web',apps:[{name:'web',label:'Website',number:42},{name:'api',label:'API',number:9}]});
  await fireEvent.click(await screen.findByRole('button',{name:'Merge pull request'}));
  await fireEvent.click(screen.getByRole('tab',{name:'API'}));
  await waitFor(()=>expect(screen.getByRole('button',{name:'Merge pull request'})).toBeEnabled());
  confirm.accept();
  expect(merge).not.toHaveBeenCalled();
  await fireEvent.click(screen.getByRole('button',{name:'Merge pull request'}));
  confirm.accept();
  await waitFor(()=>expect(merge).toHaveBeenCalledExactlyOnceWith(`${store.sessions[0].id}~api`,9,'acme/app','abc','rebase',true));
});

it('selects a merge method without merging and keeps it across status refreshes', async () => {
  mount();
  const picker = await screen.findByRole('combobox',{name:'Merge method'});
  await waitFor(()=>expect(picker).toBeEnabled());
  expect(picker).toHaveValue('squash');
  await fireEvent.click(picker);
  await fireEvent.click(screen.getByRole('option',{name:'Create merge commit'}));
  expect(merge).not.toHaveBeenCalled();
  expect(screen.getByRole('button',{name:'Merge pull request'})).toBeEnabled();
  await fireEvent.click(screen.getByRole('button',{name:'Refresh PR status'}));
  await waitFor(()=>expect(picker).toBeEnabled());
  expect(picker).toHaveValue('merge');
  await fireEvent.click(screen.getByRole('button',{name:'Merge pull request'}));
  expect(merge).toHaveBeenCalledExactlyOnceWith(store.sessions[0].id,42,'acme/app','abc','merge',false);
});

it('supports keyboard selection and uses the selected method in unready confirmation', async () => {
  read.mockResolvedValue(facts({mergeBlocked:'CI is running.'}));
  mount();
  const picker = await screen.findByRole('combobox',{name:'Merge method'});
  await waitFor(()=>expect(picker).toBeEnabled());
  await fireEvent.keyDown(picker,{key:'ArrowDown'});
  await fireEvent.keyDown(picker,{key:'End'});
  await fireEvent.keyDown(picker,{key:'Enter'});
  expect(picker).toHaveValue('rebase');
  const button = screen.getByRole('button',{name:'Merge pull request'});
  expect(button.textContent?.trim()).toBe('');
  await fireEvent.click(button);
  expect(confirm.request?.confirmLabel).toBe('Rebase and merge');
  expect(merge).not.toHaveBeenCalled();
  confirm.accept();
  await waitFor(()=>expect(merge).toHaveBeenCalledExactlyOnceWith(store.sessions[0].id,42,'acme/app','abc','rebase',true));
});

it('returns to the workspace default on another PR', async () => {
  mount({stack:true,focusApp:'web',apps:[{name:'web',label:'Website',number:42},{name:'api',label:'API',number:9}]});
  const picker = await screen.findByRole('combobox',{name:'Merge method'});
  await waitFor(()=>expect(picker).toBeEnabled());
  await fireEvent.click(picker);
  await fireEvent.click(screen.getByRole('option',{name:'Rebase and merge'}));
  read.mockResolvedValue(facts({number:9}));
  await fireEvent.click(screen.getByRole('tab',{name:'API'}));
  const nextPicker=await screen.findByRole('combobox',{name:'Merge method'});
  await waitFor(()=>expect(nextPicker).toBeEnabled());
  expect(nextPicker).toHaveValue('squash');
  expect(merge).not.toHaveBeenCalled();
});
