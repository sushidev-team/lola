import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { freshSession, ssh, store } from './talk.testkit';
import { IssueBrief } from '@bindings/desktop/linearservice';
import SessionBrief from './SessionBrief.svelte';

vi.mock('@bindings/desktop/linearservice', () => ({ IssueBrief: vi.fn() }));
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.mocked(IssueBrief).mockReset(); });

it('shows the Linear task description and opens its issue', async () => {
  vi.mocked(IssueBrief).mockResolvedValue({ title: 'Board settings', description: 'Add **Beratung** and **Mediation** to the board type picker.' });
  const sessionId = freshSession({ issue: 'CE-161', title: 'Board settings' });
  const open = vi.spyOn(store, 'openURL').mockResolvedValue(undefined);
  render(SessionBrief, { sessionId });
  expect(await screen.findByText('Add Beratung and Mediation to the board type picker.')).toBeInTheDocument();
  await fireEvent.click(screen.getByRole('button', { name: 'Open CE-161 in Linear' }));
  expect(open).toHaveBeenCalledWith('https://linear.app/issue/CE-161');
});

it('uses the opening request for a general session, with expandable detail', async () => {
  const sessionId = freshSession({ title: 'Improve search' });
  const text = 'Make search match issue titles and identifiers. '.repeat(12);
  ssh.view(sessionId).items = [{ id: 'initial', kind: 'human', human: { origin: 'initial', text, delivered: true } }] as never;
  render(SessionBrief, { sessionId });
  expect(screen.queryByRole('button', { name: /in Linear/ })).toBeNull();
  expect(IssueBrief).not.toHaveBeenCalled();
  await fireEvent.click(screen.getByRole('button', { name: 'Show more' }));
  expect(screen.getByText(text.trim())).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Show less' })).toHaveAttribute('aria-expanded', 'true');
});

it('ignores a late description after switching sessions and allows retry', async () => {
  let finish!: (value: { title: string; description: string }) => void;
  vi.mocked(IssueBrief).mockReturnValueOnce(new Promise(resolve => { finish = resolve; }) as ReturnType<typeof IssueBrief>);
  const first = freshSession({ issue: 'CE-161', title: 'First task' });
  const firstSession = store.sessions[0];
  const second = freshSession({ issue: 'CE-162', title: 'Second task' });
  store.sessions = [firstSession, ...store.sessions];
  const { rerender } = render(SessionBrief, { sessionId: first });
  vi.mocked(IssueBrief).mockRejectedValueOnce(new Error('offline'));
  await rerender({ sessionId: second });
  await screen.findByText('Could not load the Linear description.');
  finish({ title: 'Wrong task', description: 'Old description' });
  vi.mocked(IssueBrief).mockResolvedValueOnce({ title: 'Second task', description: 'Current description' });
  await fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  expect(await screen.findByText('Current description')).toBeInTheDocument();
  expect(screen.queryByText('Old description')).toBeNull();
});
