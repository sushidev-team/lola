import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import Button from './components/Button.svelte';
import { gitCommitTooltip } from './gittooltip';

afterEach(() => { cleanup(); vi.useRealTimers(); });

it('attaches a delayed structured tooltip directly to the shared commit button', async () => {
  vi.useFakeTimers();
  const commit = { sha: 'a'.repeat(40), subject: 'Improve <branch> history', author: 'Martin', date: '2026-11-12T14:00:00Z', parents: [], refs: [], email: '' };
  const branch = { name: 'feature/sidebar', fullName: 'refs/heads/feature/sidebar', sha: commit.sha, remote: false, current: true };
  render(Button, {
    ...gitCommitTooltip(commit, [branch], ['Active checkout: /repo', 'Worktree: /repo-feature']),
    children: createRawSnippet(() => ({ render: () => '<span>Commit row</span>' })),
  });
  const button = screen.getByRole('button', { name: 'Commit row' });
  await fireEvent.focusIn(button);
  vi.advanceTimersByTime(499);
  expect(screen.queryByRole('tooltip')).toBeNull();
  vi.advanceTimersByTime(1);
  const tip = screen.getByRole('tooltip');
  expect(tip.textContent).toContain('Improve <branch> history');
  expect(tip.textContent).toContain('Martin');
  expect(tip.querySelector('.git-tip-sha')).toHaveTextContent(commit.sha.slice(0, 8));
  expect(tip.querySelector('.git-tip-sha')).toHaveAttribute('aria-label', `Commit ${commit.sha}`);
  expect(tip.querySelector('time')?.getAttribute('aria-label')).toContain('2026');
  expect(tip.textContent).toContain('feature/sidebar');
  expect(tip.textContent).toContain('Checked out here · 1 worktree');
  expect(tip.querySelector('.git-tip-checkout')).toHaveAttribute('aria-label', 'Active checkout: /repo; Worktree: /repo-feature');
  expect(tip.textContent).not.toContain('/repo-feature');
  expect(tip.querySelector('branch')).toBeNull();
  expect(button.getAttribute('aria-describedby')).toBe(tip.id);
  await fireEvent.keyDown(window, { key: 'Escape' });
  expect(screen.queryByRole('tooltip')).toBeNull();
  expect(button.hasAttribute('aria-describedby')).toBe(false);
});


it('keeps the pending hover timer when the commit row rerenders with equivalent metadata', async () => {
  vi.useFakeTimers();
  const commit = { sha: 'b'.repeat(40), subject: 'Stable row', author: 'Martin', date: '2026-11-12T14:00:00Z', parents: [], refs: [], email: '' };
  const branch = { name: 'main', fullName: 'refs/heads/main', sha: commit.sha, remote: false, current: true };
  const children = createRawSnippet(() => ({ render: () => '<span>Stable commit row</span>' }));
  const view = render(Button, { ...gitCommitTooltip(commit, [branch], ['Active checkout: /repo']), children });
  const button = screen.getByRole('button', { name: 'Stable commit row' });
  await fireEvent.mouseEnter(button, { clientX: 120, clientY: 80 });
  vi.advanceTimersByTime(300);
  await view.rerender({ ...gitCommitTooltip(commit, [{ ...branch }], ['Active checkout: /repo']), class: 'lane-traced', children });
  vi.advanceTimersByTime(200);
  expect(screen.getByRole('tooltip')).toHaveTextContent('Stable row');
  expect(button.getAttribute('aria-describedby')).toBe(screen.getByRole('tooltip').id);
});


it('groups paired refs, prioritizes the current branch, and bounds ancestry chips', async () => {
  vi.useFakeTimers();
  const commit = { sha: 'c'.repeat(40), subject: 'Compact ancestry', author: '<img src=x>', date: '2026-11-12T14:00:00Z', parents: [], refs: [], email: '' };
  const ref = (name: string, remote = false, current = false, sha = commit.sha) => ({ name, fullName: `refs/${remote ? 'remotes' : 'heads'}/${name}`, sha, remote, current });
  const refs = [ref('origin/HEAD', true), ref('origin/main', true), ref('feature/z'), ref('main', false, true), ref('feature/a'), ref('origin/feature/main', true), ref('other/main', true, false, 'different-tip'), ref('feature/a')];
  render(Button, { ...gitCommitTooltip(commit, refs), children: createRawSnippet(() => ({ render: () => '<span>Compact row</span>' })) });
  await fireEvent.focusIn(screen.getByRole('button', { name: 'Compact row' }));
  vi.advanceTimersByTime(500);
  const tip = screen.getByRole('tooltip');
  const chips = [...tip.querySelectorAll('.git-tip-branch')];
  expect(chips.map(chip => chip.textContent)).toEqual(['main', 'feature/a', 'feature/z']);
  expect(chips[0]).toHaveAttribute('aria-label', 'Current branch: main');
  expect(tip).toHaveTextContent('Branch history');
  expect(tip.querySelector('.git-tip-more')).toHaveTextContent('+2');
  expect(tip.querySelector('.git-tip-more')).toHaveAttribute('aria-label', 'More branches: origin/feature/main, other/main');
  expect(tip.textContent).not.toContain('origin/HEAD');
  expect(tip.textContent).not.toContain('origin/main');
  expect(tip.querySelector('.git-tip-checkout')).toBeNull();
  expect(tip.querySelector('img')).toBeNull();
  expect(tip.querySelector('.git-tip-author')).toHaveTextContent('<img src=x>');
});

it('omits symbolic remote HEAD even when no named branch explains it', async () => {
  vi.useFakeTimers();
  const commit = { sha: 'd'.repeat(40), subject: 'Remote head', author: 'Martin', date: '2026-11-12T14:00:00Z', parents: [], refs: [], email: '' };
  render(Button, { ...gitCommitTooltip(commit, [{ name: 'origin/HEAD', fullName: 'refs/remotes/origin/HEAD', sha: commit.sha, remote: true, current: false }]), children: createRawSnippet(() => ({ render: () => '<span>Remote row</span>' })) });
  await fireEvent.focusIn(screen.getByRole('button', { name: 'Remote row' }));
  vi.advanceTimersByTime(500);
  expect(screen.getByRole('tooltip').querySelector('.git-tip-branch')).toBeNull();
});
