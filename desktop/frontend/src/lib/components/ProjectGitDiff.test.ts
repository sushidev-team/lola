import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import ProjectGitDiff from './ProjectGitDiff.svelte';

afterEach(cleanup);
// Patch line indexes: 0 header, 1 ctx, 2 -b, 3 +B, 4 ctx, 5 -d, 6 +D.
const patch = '@@ -1,4 +1,4 @@\n a\n-b\n+B\n c\n-d\n+D\n';
const file = { path: 'doc.txt', status: 'modified', additions: 2, deletions: 2, patch };
const gutter = (text: string) => screen.getByText(text).closest('.unified-row')!.querySelector('.gutter')!;

describe('Project Git diff line actions', () => {
  it('offers a button on a hovered changed line and sends its patch index', async () => {
    const onlines = vi.fn();
    render(ProjectGitDiff, { file, lineAction: 'stage', onlines });
    const row = screen.getByText('B').closest('.unified-row')!;
    await fireEvent.pointerEnter(row);
    await fireEvent.click(await screen.findByRole('button', { name: 'Stage line' }));
    expect(onlines).toHaveBeenCalledWith([3]);
  });

  it('moves a selected range of lines with one button, ignoring context rows', async () => {
    const onlines = vi.fn();
    render(ProjectGitDiff, { file, lineAction: 'unstage', onlines });
    await fireEvent.pointerDown(gutter('B'), { button: 0 });
    await fireEvent.pointerEnter(screen.getByText('D').closest('.unified-row')!);
    await fireEvent.pointerUp(window);
    await fireEvent.click(await screen.findByRole('button', { name: 'Unstage 3 lines' }));
    expect(onlines).toHaveBeenCalledWith([3, 5, 6]);
  });

  it('shows no line actions for a commit diff, in split view or while busy', async () => {
    const { rerender } = render(ProjectGitDiff, { file, lineAction: null, onlines: vi.fn() });
    await fireEvent.pointerEnter(screen.getByText('B').closest('.unified-row')!);
    expect(screen.queryByRole('button', { name: /Stage/ })).toBeNull();
    await rerender({ file, lineAction: 'stage', onlines: vi.fn(), busy: true });
    await fireEvent.pointerEnter(screen.getByText('B').closest('.unified-row')!);
    await waitFor(() => expect((screen.getByRole('button', { name: 'Stage line' }) as HTMLButtonElement).disabled).toBe(true));
  });

  it('opens a context menu with line and hunk actions', async () => {
    const oncontext = vi.fn();
    const onlines = vi.fn();
    render(ProjectGitDiff, { file, lineAction: 'stage', onlines, oncontext });
    await fireEvent.contextMenu(screen.getByText('D').closest('.unified-row')!);
    const [title, items] = oncontext.mock.calls[0];
    expect(title).toBe('');
    expect(items.map((item: { label: string }) => item.label)).toEqual(['Stage line', 'Stage hunk', 'Clear selection']);
    items[1].run();
    expect(onlines).toHaveBeenCalledWith([2, 3, 5, 6]);
  });

  it('moves the selection from the keyboard', async () => {
    const onlines = vi.fn();
    render(ProjectGitDiff, { file, lineAction: 'stage', onlines });
    const region = screen.getByRole('region', { name: 'Code changes' });
    await fireEvent.pointerDown(gutter('B'), { button: 0 });
    await fireEvent.pointerUp(window);
    await fireEvent.keyDown(region, { key: 'Enter' });
    expect(onlines).toHaveBeenCalledWith([3]);
  });
});
