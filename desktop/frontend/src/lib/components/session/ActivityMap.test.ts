import { afterEach, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { freshSession, ssh } from './talk.testkit';
import { feed } from '$lib/feed.svelte';
import ActivityMap from './ActivityMap.svelte';

afterEach(cleanup);
it('shows only timed entries and jumps to real timeline anchors with no playback control', async () => {
  const id = freshSession({ worktree: '/w' });
  feed.view(id).items = [
    { id: 't1', kind: 'tool', seq: 1, order: 1, at: '2026-11-07T12:00:00Z', tool: { name: 'Bash', category: 'run', summary: 'npm test', status: 'ok' } },
    { id: 't2', kind: 'tool', seq: 2, order: 2, at: '2026-11-07T12:00:10Z', tool: { name: 'Edit', category: 'edit', summary: 'app.ts', status: 'ok' } },
  ];
  render(ActivityMap, { sessionId: id });
  expect(screen.queryByRole('button', { name: /play/i })).toBeNull();
  expect(screen.queryByRole('button', { name: 'Tests' })).toBeNull();
  expect(screen.getByRole('button', { name: /app.ts/ })).toBeInTheDocument();
  const last = screen.getByRole('button', { name: /app.ts/ });
  last.focus();
  await fireEvent.keyDown(last, { key: 'ArrowLeft' });
  expect(screen.getByRole('button', { name: /npm test/ })).toHaveFocus();
  await fireEvent.click(screen.getByRole('button', { name: /npm test/ }));
  expect(ssh.view(id).jump?.block).toBe('item:t1');
  expect(ssh.view(id).cursor).toBe('t1');
});
