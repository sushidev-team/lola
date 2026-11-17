import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import type { ReviewPackage } from '@bindings/internal/protocol';
import { reviewCommentContext } from '$lib/reviewnote';
import ReviewScreensPhone, { pinchScale } from './ReviewScreensPhone.svelte';

const image = vi.hoisted(() => vi.fn());
vi.mock('@bindings/desktop', () => ({ DaemonService: { ReviewPackageImage: image } }));
const pkg = {
  version: 1, id: 'pkg', session: 's1', repo: 'acme/app', pr: 1, title: 't', body: '', base: 'a', head: 'b', createdAt: '', artifacts: [],
  screens: [
    { title: 'Light', after: 'after-1.png', before: 'before-1.png', viewport: '390x844', caption: 'Header' },
    { title: 'Dark', after: 'after-2.png' },
  ],
} as ReviewPackage;

beforeEach(() => {
  image.mockReset().mockImplementation(async (_s: string, _p: string, a: string) => ({ dataURL: `data:image/png;base64,${btoa(a)}` }));
});
afterEach(() => cleanup());

it('clamps a pinch between 1x and 4x', () => {
  expect(pinchScale(1, 100, 200)).toBe(2);
  expect(pinchScale(2, 100, 400)).toBe(4);
  expect(pinchScale(2, 100, 10)).toBe(1);
  expect(pinchScale(1.5, 0, 100)).toBe(1.5);
});

it('stacks one card per screen and switches before and after', async () => {
  render(ReviewScreensPhone, { pkg, onComment: () => true });
  expect(screen.getAllByRole('article').map(a => a.getAttribute('aria-label'))).toEqual(['Screen 1: Light', 'Screen 2: Dark']);
  await screen.findByRole('img', { name: 'Light, after' });
  await fireEvent.click(screen.getByRole('button', { name: 'Before' }));
  await screen.findByRole('img', { name: 'Light, before' });
  expect(image).toHaveBeenCalledWith('s1', 'pkg', 'before-1.png');
  // The second screen has no baseline, so no switch.
  expect(screen.getAllByRole('group')).toHaveLength(1);
});

it('opens a full-screen viewer with Close, Previous and Next, and double-tap zoom', async () => {
  render(ReviewScreensPhone, { pkg, onComment: () => true });
  await screen.findByRole('img', { name: 'Dark, after' });
  await fireEvent.click(screen.getByRole('button', { name: 'Open Light, after' }));
  const viewer = screen.getByRole('dialog', { name: 'Light, after' });
  expect(viewer).toHaveTextContent('1 of 2');
  await fireEvent.dblClick(viewer.querySelector('[data-viewer-stage]')!);
  expect((viewer.querySelector('[data-viewer-image]') as HTMLElement).style.transform).toContain('scale(2)');
  await fireEvent.click(screen.getByRole('button', { name: 'Next screen' }));
  expect(screen.getByRole('dialog', { name: 'Dark, after' })).toHaveTextContent('2 of 2');
  await fireEvent.click(screen.getByRole('button', { name: 'Previous screen' }));
  await fireEvent.click(screen.getByRole('button', { name: 'Close screenshot' }));
  expect(screen.queryByRole('dialog')).toBeNull();
});

it('queues a comment from the docked field and shows pending comments for its screen', async () => {
  const onComment = vi.fn(() => true);
  const note = reviewCommentContext(pkg, 'Screen 2: Dark', 1) + 'Too dark';
  render(ReviewScreensPhone, { pkg, note, canComment: true, onComment });
  expect(screen.getByText('Too dark').closest('article')).toHaveAttribute('aria-label', 'Screen 2: Dark');
  await fireEvent.click(screen.getAllByRole('button', { name: 'Comment on this screen' })[0]);
  const add = screen.getByRole('button', { name: 'Add to feedback' });
  expect(add).toBeDisabled();
  await fireEvent.input(screen.getByRole('textbox', { name: 'Comment on this screen' }), { target: { value: 'Clip' } });
  await fireEvent.click(add);
  expect(onComment).toHaveBeenCalledWith(0, 'Clip');
  expect(screen.getByRole('status')).toHaveTextContent('Comment added to review feedback.');
  expect(document.querySelector('[data-screen-composer]')).toBeNull();
});

it('draws no comment control without the right to send feedback', async () => {
  render(ReviewScreensPhone, { pkg, onComment: () => true });
  expect(screen.queryByRole('button', { name: 'Comment on this screen' })).toBeNull();
});

it('says when a screenshot could not be loaded and retries', async () => {
  image.mockReset().mockRejectedValueOnce(new Error('frame_too_large')).mockResolvedValue({ dataURL: 'data:image/png;base64,eA==' });
  render(ReviewScreensPhone, { pkg: { ...pkg, screens: [pkg.screens![1]] }, onComment: () => true });
  await screen.findByText('This screenshot could not be loaded.');
  await fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await screen.findByRole('img', { name: 'Dark, after' });
});
