import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { freshSession, store } from './talk.testkit';
import { DaemonService } from '@bindings/desktop';
import BeforeMerge from './BeforeMerge.svelte';

const read = vi.mocked(DaemonService.PRReadiness);
const address = vi.mocked(DaemonService.AddressPRFeedback);
const thread = (id: string) => ({ id, path:'src/app.ts', line:12, outdated:false, author:'reviewer', preview:`Finding ${id}`, url:`https://github.com/acme/app/pull/42#discussion_${id}` });
const facts = (over = {}) => ({ repo:'acme/app', number:42, url:'https://github.com/acme/app/pull/42', state:'OPEN', draft:false, mergeable:'MERGEABLE', mergeState:'CLEAN', review:'APPROVED', checks:'pass', head:'abc', threads:[], ...over });
function mount(over = {}) { freshSession({prNumber:42,prUrl:'https://github.com/acme/app/pull/42',...over});return render(BeforeMerge,{session:store.sessions[0]}); }
beforeEach(() => {read.mockReset(); address.mockReset(); read.mockResolvedValue(facts());});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it('shows verified status without implying permission to merge', async () => {
  mount();
  expect(await screen.findByText('No blockers reported · final review on GitHub.')).toBeInTheDocument();
  expect(screen.getByText('All resolved')).toBeInTheDocument();
  expect(screen.queryByRole('button',{name:'Ask agent to address'})).toBeNull();
  expect(screen.queryByRole('button',{name:'Merge'})).toBeNull();
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
  expect(screen.queryByText('No blockers reported · final review on GitHub.')).toBeNull();
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
