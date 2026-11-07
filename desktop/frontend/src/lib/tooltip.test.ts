import { afterEach, expect, it, vi } from 'vitest';
import { tooltip } from './tooltip';
afterEach(() => { vi.useRealTimers(); document.body.replaceChildren(); });
it('keeps long command previews intact and renders syntax as safe text', () => {
  vi.useFakeTimers();
  const node = document.createElement('button'); document.body.append(node);
  const text = `git show --stat ${'a'.repeat(900)} <img src=x>`;
  const action = tooltip(node, { text, language: 'sh' });
  node.dispatchEvent(new MouseEvent('mouseenter'));
  expect(document.querySelector('[role=tooltip]')).toBeNull();
  vi.advanceTimersByTime(301);
  const tip = document.querySelector<HTMLElement>('[role=tooltip]')!;
  expect(tip.textContent).toBe(text);
  expect(tip.querySelector('img')).toBeNull();
  expect(tip.querySelector('.text-accent-ink')?.textContent).toBe('git');
  action.destroy();
});
it('allows entering and scrolling the tooltip, and dismisses on Escape', () => {
  vi.useFakeTimers();
  const node = document.createElement('button'); document.body.append(node);
  const action = tooltip(node, 'Long preview');
  node.dispatchEvent(new MouseEvent('mouseenter'));
  expect(document.querySelector('[role=tooltip]')).toBeNull();
  vi.advanceTimersByTime(301);
  const tip = document.querySelector<HTMLElement>('[role=tooltip]')!;
  node.dispatchEvent(new MouseEvent('mouseleave')); vi.advanceTimersByTime(80);
  tip.dispatchEvent(new MouseEvent('mouseenter')); vi.advanceTimersByTime(200);
  tip.dispatchEvent(new Event('scroll'));
  expect(tip.isConnected).toBe(true);
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
  expect(tip.isConnected).toBe(false);
  expect(node.hasAttribute('aria-describedby')).toBe(false);
  action.destroy();
});

it('shows styled metadata on focus and refreshes a visible tooltip without a delay', () => {
  const node = document.createElement('button'); document.body.append(node);
  const action = tooltip(node, { text: 'npm test', instant: true, heading: 'Tests', meta: 'Running', tone: 'tests' });
  node.dispatchEvent(new FocusEvent('focusin'));
  const tip = document.querySelector<HTMLElement>('[role=tooltip]')!;
  expect(tip.classList.contains('cv-tooltip-card')).toBe(true);
  expect(tip.querySelector('.cv-tooltip-heading')?.textContent).toBe('Tests');
  expect(tip.querySelector('.cv-tooltip-meta')?.textContent).toBe('Running');
  action.update({ text: 'npm test', instant: true, heading: 'Tests', meta: 'Completed · 2s', tone: 'tests' });
  expect(document.querySelector('.cv-tooltip-meta')?.textContent).toBe('Completed · 2s');
  action.destroy();
  expect(document.querySelector('[role=tooltip]')).toBeNull();
});

it('replaces the previous tooltip immediately when moving rapidly between targets', () => {
  vi.useFakeTimers();
  const first = document.createElement('button'), second = document.createElement('button');
  document.body.append(first, second);
  const a = tooltip(first, { text: 'First', instant: true }), b = tooltip(second, { text: 'Second', instant: true });
  first.dispatchEvent(new MouseEvent('mouseenter'));
  first.dispatchEvent(new MouseEvent('mouseleave'));
  second.dispatchEvent(new MouseEvent('mouseenter'));
  expect(document.querySelectorAll('[role=tooltip]')).toHaveLength(1);
  expect(document.querySelector('[role=tooltip]')?.textContent).toBe('Second');
  expect(first.hasAttribute('aria-describedby')).toBe(false);
  vi.advanceTimersByTime(200);
  expect(document.querySelector('[role=tooltip]')?.textContent).toBe('Second');
  first.dispatchEvent(new FocusEvent('focusin'));
  expect(document.querySelectorAll('[role=tooltip]')).toHaveLength(1);
  expect(document.querySelector('[role=tooltip]')?.textContent).toBe('First');
  a.destroy(); b.destroy();
});
