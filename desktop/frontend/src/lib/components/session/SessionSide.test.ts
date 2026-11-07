import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import { freshSession, ssh, terms, store } from './talk.testkit';
import { SSH } from '$lib/terms.svelte';
import { feed } from '$lib/feed.svelte';
import { changes } from '$lib/changes.svelte';
import SessionSide from './SessionSide.svelte';

afterEach(cleanup);

describe('conversation overview', () => {
  it('shows progress reported by the agent', async () => {
    const id = freshSession({ worktree: '/w', board: {
      updatedAt: new Date().toISOString(), hasProgress: true, percent: 40,
      progressDerived: true, done: 2, total: 5,
      todos: [{ text: 'Implement', state: 'done' }, { text: 'Test', state: 'active' }],
    } });
    terms.select(id, SSH);
    render(SessionSide, { sessionId: id });
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '40');
    expect(screen.getByText('Test')).toBeInTheDocument();

  });

  it('uses posted plans when there is no report board, without inventing progress', () => {
    const id = freshSession({ worktree: '/w' });
    ssh.view(id).items = [{ id: 'plan', kind: 'element', element: {
      id: 'plan', kind: 'plan', title: 'Improve history', steps: [
        { text: 'Inspect', state: 'done' }, { text: 'Implement', state: 'active' },
      ],
    } }] as never;
    render(SessionSide, { sessionId: id });
    expect(screen.getByRole('progressbar', { name: 'Reported plan' })).toHaveAttribute('aria-valuenow', '1');
    expect(screen.getByText('Improve history')).toBeInTheDocument();
    expect(screen.queryByText('No plan reported yet.')).toBeNull();
  });
});

it('shows measured changes and test command results and navigates recent activity', async () => {
  const id = freshSession({ worktree: '/w' });
  changes.note(id, { files: [{ additions: 12, deletions: 3 }] } as never);
  feed.view(id).items = [{ id: 'test-run', kind: 'tool', order: 1, seq: 1, tool: { name: 'Bash', category: 'run', summary: 'npm test', status: 'error' } }];
  const { container } = render(SessionSide, { sessionId: id });
  expect(screen.getByText('1 file')).toBeInTheDocument();
  expect(screen.getByText('+12')).toBeInTheDocument();
  expect(screen.getByRole('region', { name: 'Local checks' })).toHaveTextContent('Failed');
  expect(screen.queryByText('Recent test commands')).toBeNull();
  expect(screen.queryByRole('button', { name: /older command/ })).toBeNull();
  await fireEvent.click(container.querySelector('[data-side-activity] button')!);
  expect(ssh.view(id).jump?.block).toBe('item:test-run');
});


it('unifies reported checks and command evidence without an empty duplicate', async () => {
  const id = freshSession({ worktree: '/w', prNumber: 12, prUrl: 'https://github.com/acme/app/pull/12', checks: 'fail', prStale: true, board: {
    updatedAt: new Date().toISOString(), phase: 'done',
    checks: [{ name: 'tests', state: 'pass', summary: '340 passed, 7 skipped', evidence: 'ran' }, { name: 'typecheck', state: 'pass' }],
  } });
  const { container } = render(SessionSide, { sessionId: id });
  expect(screen.getAllByRole('heading', { name: 'Local checks' })).toHaveLength(1);
  expect(screen.getByText('340 passed, 7 skipped')).toBeInTheDocument();
  expect(screen.queryByText(/^[· ]*ran$/)).toBeNull();
  expect(container.querySelector('[data-side-tests]')).toBeNull();
  expect(screen.queryByText(/No test commands/)).toBeNull();
  expect(screen.queryByText('Local checks', { selector: 'span' })).toBeNull();
  expect(screen.getByRole('region', { name: 'Before merge' })).toBeInTheDocument();

});

it('keeps local report results separate from actual command outcomes', () => {
  const id = freshSession({ worktree: '/w', board: { updatedAt: new Date().toISOString(), checks: [{ name: 'tests', state: 'pass', evidence: 'contradicted', evidenceNote: 'Latest run failed' }] } });
  feed.view(id).items = [{ id: 'failed-test', kind: 'tool', order: 1, seq: 1, tool: { name: 'Bash', category: 'run', summary: 'npm test', status: 'error' } }];
  render(SessionSide, { sessionId: id });
  const local = screen.getByRole('region', { name: 'Local checks' });
  expect(within(local).getByText('1/1 passed')).toBeInTheDocument();
  expect(within(local).getByText(/last run failed/)).toBeInTheDocument();
  expect(within(local).getByText('Failed')).toBeInTheDocument();
});

it('moves Dev controls into the overview and preserves actions and URLs', async () => {
  const id = freshSession({ devCommands: ['npm run dev'], devActive: true, devUrls: ['http://localhost:3000', 'http://127.0.0.1:5173'] });
  const dev = vi.spyOn(store, 'dev').mockResolvedValue(undefined);
  const open = vi.spyOn(store, 'openURL').mockResolvedValue(undefined);
  const { container } = render(SessionSide, { sessionId: id });
  const panel = container.querySelector('[data-side-dev]')! as HTMLElement;
  await fireEvent.click(within(panel).getByRole('button', { name: 'Stop dev' }));
  expect(dev).toHaveBeenCalledWith(id, false);
  await fireEvent.click(within(panel).getByRole('button', { name: /3000/ }));
  expect(open).toHaveBeenCalledWith('http://localhost:3000');
  await fireEvent.click(within(panel).getByRole('button', { name: /5173/ }));
  expect(open).toHaveBeenCalledWith('http://127.0.0.1:5173');
  dev.mockRestore(); open.mockRestore();
});




it('collapses completed plans and keeps the detail available', async () => {
  const id = freshSession({ board: { phase: 'done', hasProgress: true, percent: 100, todos: [{ text: 'Implement it', state: 'done' }] } });
  render(SessionSide, { sessionId: id });
  expect(screen.queryByText('Implement it')).toBeNull();
  await fireEvent.click(screen.getByRole('button', { name: /Plan complete/ }));
  expect(screen.getByText('Implement it')).toBeInTheDocument();
  expect(screen.queryByRole('heading', { name: 'Session' })).toBeNull();
});

it('opens Changes from the whole card', async () => {
  const id = freshSession({ worktree: '/w' });
  const open = vi.spyOn(ssh, 'openChanges').mockImplementation(() => {});
  render(SessionSide, { sessionId: id });
  await fireEvent.click(screen.getByRole('button', { name: 'View changes' }));
  expect(open).toHaveBeenCalledWith(store.sessionById(id));
  open.mockRestore();
});


it('places the editable task outline in Overview', () => {
  const id = freshSession({ tasks: [{ id: 'work', kind: 'task', text: 'Implement invitations', state: 'doing', author: 'user', updatedAt: new Date().toISOString() }] });
  render(SessionSide, { sessionId: id });
  const overview = screen.getByRole('complementary', { name: 'Session overview' });
  expect(within(overview).getByText('Implement invitations')).toBeInTheDocument();
  expect(within(overview).getByRole('button', { name: 'Add task' })).toBeInTheDocument();
  expect(screen.queryByText('No plan reported yet.')).toBeNull();
});


it('reveals older test commands only when more than three are available', async () => {
  const id = freshSession({ worktree: '/w' });
  feed.view(id).items = Array.from({ length: 5 }, (_, i) => ({ id: `test-${i}`, kind: 'tool', order: i + 1, seq: i + 1, tool: { name: 'Bash', category: 'run', summary: `npm test -- test${i}`, status: 'ok' } }));
  render(SessionSide, { sessionId: id });
  const local = within(screen.getByRole('region', { name: 'Local checks' }));
  expect(local.queryByRole('button', { name: /test0/ })).toBeNull();
  await fireEvent.click(local.getByRole('button', { name: 'Show 2 older commands' }));
  expect(local.getByRole('button', { name: /test0/ })).toBeInTheDocument();
  await fireEvent.click(local.getByRole('button', { name: /test0/ }));
  expect(ssh.view(id).jump?.block).toBe('item:test-0');
  await fireEvent.click(local.getByRole('button', { name: 'Show fewer' }));
  expect(local.queryByRole('button', { name: /test0/ })).toBeNull();
});
