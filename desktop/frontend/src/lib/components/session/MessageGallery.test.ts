import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
const read = vi.hoisted(() => vi.fn());
vi.mock('@bindings/desktop', () => ({ TalkService: { ReadAttachmentImage: read } }));
import MessageGallery from './MessageGallery.svelte';
afterEach(cleanup);
it('opens the selected original, navigates all message images and closes on Escape', async () => {
  read.mockImplementation((_wt, path) => Promise.resolve(`data:image/png;base64,${path}`));
  const close = vi.fn();
  render(MessageGallery, { images: [{ n: 1, path: 'one' }, { n: 2, path: 'two' }], initial: 1, worktree: '/w', thumbs: new Map(), onclose: close });
  await waitFor(() => expect(screen.getByRole('img', { name: 'Image 2' })).toHaveAttribute('src', 'data:image/png;base64,two'));
  await fireEvent.keyDown(screen.getByRole('dialog'), { key: 'ArrowLeft' });
  await waitFor(() => expect(screen.getByRole('img', { name: 'Image 1' })).toHaveAttribute('src', 'data:image/png;base64,one'));
  await fireEvent.click(screen.getByRole('button', { name: 'Show image 2' }));
  expect(screen.getByRole('button', { name: 'Show image 2' })).toHaveAttribute('aria-pressed', 'true');
  await fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
  expect(close).toHaveBeenCalledOnce();
});
it('shows an unavailable original without trapping navigation', async () => {
  read.mockRejectedValue(new Error('gone'));
  render(MessageGallery, { images: [{ n: 1, path: 'gone' }], initial: 0, worktree: '/w', thumbs: new Map([['gone', 'data:image/png;base64,thumb']]), onclose: vi.fn() });
  expect(await screen.findByRole('status')).toHaveTextContent('Original image unavailable');
  expect(screen.getByRole('button', { name: 'Next image' })).toBeDisabled();
});
