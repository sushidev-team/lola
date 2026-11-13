import { afterEach, expect, it, vi } from 'vitest';
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
  expect(ssh.view(id).jump?.expand).toBe(false);
  expect(ssh.view(id).cursor).not.toBe('t1');
});

it('scrubs across entries using pointer capture, clamps outside the strip and stops on release', async () => {
  const id = freshSession({ worktree: '/w' });
  feed.view(id).items = ['one', 'two', 'three'].map((id, i) => ({ id, kind: 'tool', seq: i + 1, order: i + 1, at: `2026-11-07T12:00:0${i}Z`, tool: { name: 'Bash', category: 'run', summary: id, status: 'ok' } }));
  render(ActivityMap, { sessionId: id });
  const buttons = screen.getAllByRole('button');
  const track = buttons[0].parentElement!;
  track.setPointerCapture = vi.fn();
  buttons.forEach((button, i) => {
    vi.spyOn(button, 'getBoundingClientRect').mockReturnValue({ left: i * 100, right: (i + 1) * 100 - 5 } as DOMRect);
  });
  const pointer = async (type: string, x: number) => {
    const event = new Event(type, { bubbles: true, cancelable: true });
    Object.assign(event, { pointerId: 1, button: 0, clientX: x, isPrimary: true });
    await fireEvent(track, event);
  };
  await pointer('pointerdown', 20);
  expect(track.setPointerCapture).toHaveBeenCalledWith(1);
  expect(ssh.view(id).jump).toMatchObject({ block: 'item:one', expand: false });
  await pointer('pointermove', 99); // the compressed gap belongs to its nearest entry
  expect(ssh.view(id).jump?.block).toBe('item:two');
  const n = ssh.view(id).jump?.n;
  await pointer('pointermove', 150);
  expect(ssh.view(id).jump?.n).toBe(n);
  await pointer('pointermove', 500);
  expect(ssh.view(id).jump?.block).toBe('item:three');
  await pointer('pointerup', 500);
  expect(buttons[2]).toHaveFocus();
  await pointer('pointermove', 20);
  await fireEvent.click(buttons[0], { detail: 1 });
  expect(ssh.view(id).jump?.block).toBe('item:three');
});
