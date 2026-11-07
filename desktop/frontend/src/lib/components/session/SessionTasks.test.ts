import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { freshSession, setTalk, ssh, store, talkSvc } from './talk.testkit';
import { DaemonService } from '@bindings/desktop';
import SessionTasks from './SessionTasks.svelte';
import Composer from './Composer.svelte';

afterEach(() => { cleanup(); vi.restoreAllMocks(); });
const task = (id: string, state = 'todo', author = 'agent') => ({ id, state, author, text: `Requirement ${id}`, kind: 'task', updatedAt: new Date().toISOString() });

it('adds through the shared service, preserves failures, and reflects agent updates', async () => {
  const id = freshSession();
  vi.spyOn(store, 'refresh').mockResolvedValue(undefined);
  const request = vi.mocked(DaemonService.SessionTask).mockRejectedValueOnce(new Error('Offline'));
  render(SessionTasks, { sessionId: id });
  await fireEvent.click(screen.getByRole('button', { name: 'Add task' }));
  await fireEvent.input(screen.getByRole('textbox', { name: 'Task or requirement' }), { target: { value: 'Keyboard support' } });
  await fireEvent.click(screen.getByRole('button', { name: 'Add & send' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Offline');
  expect(screen.getByRole('textbox')).toHaveValue('Keyboard support');
  request.mockResolvedValueOnce([{ ...task('user'), text: 'Keyboard support', author: 'user', notification: 'failed', sendError: 'No agent' }] as never);
  await fireEvent.click(screen.getByRole('button', { name: 'Add & send' }));
  expect(await screen.findByText('Saved · message not sent')).toBeInTheDocument();
  expect(request).toHaveBeenLastCalledWith(id, ['add', expect.any(String), 'task', 'Keyboard support']);
  store.sessions = store.sessions.map(s => ({ ...s, tasks: [...s.tasks!, task('agent')] })) as never;
  expect(await screen.findByText('Requirement agent')).toBeInTheDocument();
});

it('changes status from task details and folds completed work', async () => {
  const id = freshSession({ tasks: [task('one')] });
  vi.spyOn(store, 'refresh').mockResolvedValue(undefined);
  const request = vi.mocked(DaemonService.SessionTask).mockResolvedValueOnce([task('one', 'done')] as never);
  render(SessionTasks, { sessionId: id });
  await fireEvent.click(screen.getByRole('button', { name: 'Status of Requirement one: To do' }));
  await fireEvent.click(await screen.findByRole('menuitemradio', { name: 'Done' }));
  expect(request).toHaveBeenCalledWith(id, ['state', 'one', 'done']);
  await waitFor(() => expect(screen.queryByText('Requirement one')).toBeNull());
  await fireEvent.click(screen.getByRole('button', { name: /Completed/ }));
  expect(screen.getByText('Requirement one')).toBeInTheDocument();
});

it('nests criteria in expandable task rows and keeps unassigned additions separate', async () => {
  const id = freshSession({ tasks: [task('work'), { ...task('criterion'), kind: 'requirement', parentId: 'work' }, { ...task('loose'), kind: 'requirement' }] });
  render(SessionTasks, { sessionId: id });
  expect(screen.queryByRole('region', { name: 'To do' })).toBeNull();
  expect(screen.getByText('Requirement work')).toBeInTheDocument();
  expect(screen.queryByText('Requirement criterion')).toBeNull();
  expect(screen.queryByText('Requirement loose')).toBeNull();
  await fireEvent.click(screen.getByRole('button', { name: /^Requirement work/ }));
  expect(screen.getByText('Requirement criterion')).toBeInTheDocument();
  await fireEvent.click(screen.getByRole('tab', { name: /Requirements/ }));
  expect(screen.getByText('Requirement loose')).toBeInTheDocument();
  expect(screen.queryByRole('combobox')).toBeNull();
  await fireEvent.click(screen.getByRole('button', { name: 'Requirement loose' }));
  expect(screen.getByRole('button', { name: 'Organize Requirement loose' })).toBeInTheDocument();
});

it('adds a requirement directly beneath its task and reveals the result', async () => {
  const id = freshSession({ tasks: [task('work')] });
  vi.spyOn(store, 'refresh').mockResolvedValue(undefined);
  const request = vi.mocked(DaemonService.SessionTask).mockResolvedValueOnce([task('work'), { ...task('child'), text: 'Keyboard accessible', kind: 'requirement', parentId: 'work' }] as never);
  render(SessionTasks, { sessionId: id });
  await fireEvent.click(screen.getByRole('button', { name: /^Requirement work/ }));
  await fireEvent.click(screen.getByRole('button', { name: 'Add requirement' }));
  expect(screen.getByRole('button', { name: 'Requirement scope' })).toHaveTextContent('Requirement work');
  await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'Keyboard accessible' } });
  await fireEvent.click(screen.getByRole('button', { name: 'Add & send' }));
  expect(request).toHaveBeenLastCalledWith(id, ['add', expect.any(String), 'requirement', '--task', 'work', 'Keyboard accessible']);
  expect(await screen.findByText('Keyboard accessible')).toBeInTheDocument();
});

it('shows tasks reopened by an agent even when completed work is folded', async () => {
  const id = freshSession({ tasks: [task('work', 'done')] });
  render(SessionTasks, { sessionId: id });
  expect(screen.queryByText('Requirement work')).toBeNull();
  store.sessions = store.sessions.map(s => ({ ...s, tasks: [task('work'), { ...task('new'), kind: 'requirement', parentId: 'work' }] })) as never;
  expect(await screen.findByText('Requirement work')).toBeInTheDocument();
  expect(screen.getAllByText('0/1').length).toBeGreaterThan(0);
});


it('uses a keyboard-accessible three-state picker with fractional icons', async () => {
  const id = freshSession({ tasks: [task('work')] });
  render(SessionTasks, { sessionId: id });
  const trigger = screen.getByRole('button', { name: 'Status of Requirement work: To do' });
  await fireEvent.click(trigger);
  const first = await screen.findByRole('menuitemradio', { name: 'To do' });
  await waitFor(() => expect(first).toHaveFocus());
  const options = screen.getAllByRole('menuitemradio');
  expect(options.map(o => o.querySelector('[data-task-state]')?.getAttribute('data-task-state'))).toEqual(['todo', 'doing', 'checking', 'done']);
  await fireEvent.keyDown(first, { key: 'ArrowDown' });
  expect(screen.getByRole('menuitemradio', { name: 'In progress' })).toHaveFocus();
  await fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' });
  expect(screen.queryByRole('menu')).toBeNull();
  expect(trigger).toHaveFocus();
});

it('keeps global requirements in their tab and organizes without select fields', async () => {
  const global = { ...task('global'), kind: 'requirement', scope: 'global' };
  const id = freshSession({ tasks: [task('work'), global] });
  vi.spyOn(store, 'refresh').mockResolvedValue(undefined);
  const request = vi.mocked(DaemonService.SessionTask).mockResolvedValueOnce([task('work'), { ...global, scope: '', parentId: 'work' }] as never);
  render(SessionTasks, { sessionId: id });
  expect(screen.queryByText('Requirement global')).toBeNull();
  await fireEvent.click(screen.getByRole('tab', { name: /Requirements/ }));
  expect(screen.getByText('Global')).toBeInTheDocument();
  await fireEvent.click(screen.getByRole('button', { name: 'Requirement global' }));
  await fireEvent.click(screen.getByRole('button', { name: 'Organize Requirement global' }));
  await fireEvent.click(await screen.findByRole('menuitemradio', { name: 'Requirement work' }));
  expect(request).toHaveBeenCalledWith(id, ['attach', 'global', 'work']);
  await waitFor(() => expect(screen.queryByText('Requirement global')).toBeNull());
  expect(screen.queryByRole('combobox')).toBeNull();
});

it('adds an explicitly global requirement from the Requirements tab', async () => {
  const id = freshSession();
  vi.spyOn(store, 'refresh').mockResolvedValue(undefined);
  const request = vi.mocked(DaemonService.SessionTask).mockResolvedValueOnce([{ ...task('g'), kind:'requirement', scope:'global', text:'Support keyboard navigation' }] as never);
  render(SessionTasks, { sessionId: id });
  await fireEvent.keyDown(screen.getByRole('tab', { name: 'Tasks' }), { key: 'ArrowRight' });
  await fireEvent.click(screen.getByRole('button', { name: 'Add requirement' }));
  await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'Support keyboard navigation' } });
  await fireEvent.click(screen.getByRole('button', { name: 'Requirement scope' }));
  await fireEvent.click(await screen.findByRole('menuitemradio', { name: 'Global requirement' }));
  await fireEvent.click(screen.getByRole('button', { name: 'Add & send' }));
  expect(request).toHaveBeenCalledWith(id, ['add', expect.any(String), 'requirement', '--global', 'Support keyboard navigation']);
  expect(await screen.findByText('Support keyboard navigation')).toBeInTheDocument();
});


it('keeps task and requirement actions on their own rows with explicit menu labels', async () => {
  const parent = task('work');
  const child = { ...task('child'), kind: 'requirement', parentId: parent.id };
  const id = freshSession({ tasks: [parent, child] });
  render(SessionTasks, { sessionId: id });
  await fireEvent.click(screen.getByRole('button', { name: 'Task actions: Requirement work' }));
  await fireEvent.click(await screen.findByRole('menuitem', { name: 'Edit task' }));
  expect(screen.getByRole('textbox')).toHaveValue(parent.text);
  await fireEvent.click(screen.getByRole('button', { name: 'Cancel task' }));
  await fireEvent.click(screen.getByRole('button', { name: /^Requirement work/ }));
  await fireEvent.click(screen.getByRole('button', { name: 'Requirement actions: Requirement child' }));
  expect(screen.queryByRole('menuitem', { name: 'Remove task' })).toBeNull();
  await fireEvent.click(await screen.findByRole('menuitem', { name: 'Remove requirement' }));
  expect(screen.getByText('Remove requirement?')).toBeInTheDocument();
  expect(screen.queryByText('Remove task? Requirements will be kept.')).toBeNull();
});

it('shows human checks with a direct confirmation and preserves delivery failures', async () => {
  const id = freshSession({ tasks: [task('browser', 'checking')] });
  vi.spyOn(store, 'refresh').mockResolvedValue(undefined);
  const request = vi.mocked(DaemonService.SessionTask).mockRejectedValueOnce(new Error('Offline'));
  render(SessionTasks, { sessionId: id });
  expect(screen.getByText('Needs your check')).toBeInTheDocument();
  const confirm = screen.getByRole('button', { name: 'Confirm checked: Requirement browser' });
  await fireEvent.click(confirm);
  expect(await screen.findByRole('alert')).toHaveTextContent('Offline');
  expect(confirm).toBeInTheDocument();
  request.mockResolvedValueOnce([{ ...task('browser', 'done'), notification: 'failed' }] as never);
  await fireEvent.click(confirm);
  expect(request).toHaveBeenLastCalledWith(id, ['state', 'browser', 'done']);
  await waitFor(() => expect(screen.queryByText('Needs your check')).toBeNull());
  await fireEvent.click(screen.getByRole('button', { name: /Completed/ }));
  expect(screen.getByText('Saved · message not sent')).toBeInTheDocument();
});

it('keeps child checks actionable and prevents confirming a parent with unmet requirements', async () => {
  const id = freshSession({ tasks: [task('work', 'checking'), { ...task('browser', 'checking'), kind: 'requirement', parentId: 'work', feedback: 'Still scrolls the page' }] });
  render(SessionTasks, { sessionId: id });
  expect(screen.getByRole('button', { name: 'Confirm checked: Requirement work' })).toBeDisabled();
  await fireEvent.click(screen.getByRole('button', { name: '1 requirement needs your check' }));
  expect(screen.getByRole('button', { name: 'Confirm checked: Requirement browser' })).toBeEnabled();
  await fireEvent.click(screen.getByRole('button', { name: 'Requirement browser' }));
  expect(screen.getByText('Still scrolls the page')).toBeInTheDocument();
});

it('opens task feedback in the real composer, keeps the draft, and sends it through the normal queue', async () => {
  const id = freshSession({ tasks: [task('browser', 'checking')] });
  const view = ssh.view(id);
  view.agentDraft = 'It still scrolls the page';
  view.runDefault = true;
  render(SessionTasks, { sessionId: id });
  render(Composer, { sessionId: id });
  await setTalk(id);
  await fireEvent.click(screen.getByRole('button', { name: 'Give feedback: Requirement browser' }));
  const message = 'Feedback on task [browser] (Requirement browser):\nIt still scrolls the page';
  const field = screen.getByRole('textbox');
  expect(field).toHaveValue(message);
  expect(view.runDefault).toBe(false);
  await waitFor(() => expect(field).toHaveFocus());
  await fireEvent.click(screen.getByRole('button', { name: 'Give feedback: Requirement browser' }));
  expect(field).toHaveValue(message);
  await fireEvent.keyDown(field, { key: 'Enter' });
  await waitFor(() => expect(talkSvc.Say).toHaveBeenCalledWith({ session: id, text: message, conn: undefined, stopFirst: undefined }));
  await waitFor(() => expect(view.agentDraft).toBe(''));
});
