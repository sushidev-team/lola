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

  it('folds unchanged lines on request and opens them with above and below rows', async () => {
    const body = Array.from({ length: 60 }, (_, i) => ' l' + i).join('\n');
    const long = { ...file, patch: '@@ -1,61 +1,61 @@\n' + body.split('\n').slice(0, 30).join('\n') + '\n-old\n+new\n' + body.split('\n').slice(30).join('\n') + '\n' };
    render(ProjectGitDiff, { file: long });
    expect(screen.getByText('l0')).toBeTruthy();
    expect(screen.queryByRole('button', { name: /lines above/ })).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: 'Changes only' }));
    expect(screen.queryByText('l0')).toBeNull();
    expect(screen.getByText('l27')).toBeTruthy();
    await fireEvent.click(screen.getByRole('button', { name: /Show 20 lines above/ }));
    expect(screen.getByText('l7')).toBeTruthy();
    expect(screen.queryByText('l6')).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: /Show 20 lines below/ }));
    expect(screen.getByText('l50')).toBeTruthy();
  });

  it('selects the lines a text drag covers', async () => {
    const onlines = vi.fn();
    render(ProjectGitDiff, { file, lineAction: 'stage', onlines });
    const from = screen.getByText('B'), to = screen.getByText('D');
    const range = document.createRange();
    range.setStart(from.firstChild!, 0);
    range.setEnd(to.firstChild!, 1);
    const selection = window.getSelection()!;
    selection.removeAllRanges();
    selection.addRange(range);
    document.dispatchEvent(new Event('selectionchange')); // live, before the pointer is released
    await fireEvent.click(await screen.findByRole('button', { name: 'Stage 3 lines' }));
    expect(onlines).toHaveBeenCalledWith([3, 5, 6]);
  });

  it('steps through the changes of a file', async () => {
    render(ProjectGitDiff, { file, lineAction: null });
    const next = screen.getByRole('button', { name: 'Next change' }) as HTMLButtonElement;
    const prev = screen.getByRole('button', { name: 'Previous change' }) as HTMLButtonElement;
    await waitFor(() => expect(screen.getByText('1 / 2')).toBeTruthy());
    expect(prev.disabled).toBe(true);
    await fireEvent.click(next);
    expect(screen.getByText('2 / 2')).toBeTruthy();
    expect(next.disabled).toBe(true);
    await fireEvent.click(prev);
    expect(screen.getByText('1 / 2')).toBeTruthy();
  });

  it('reveals a change in split view', async () => {
    render(ProjectGitDiff, { file, lineAction: null });
    await fireEvent.click(screen.getByRole('button', { name: 'Split' }));
    const scroll = vi.fn();
    const target = document.querySelector('[data-ats~="5"]') as HTMLElement;
    target.scrollIntoView = scroll;
    await fireEvent.click(screen.getByRole('button', { name: 'Next change' }));
    expect(scroll).toHaveBeenCalled();
  });

  it('does not stage the old lines from a menu opened on an older patch', async () => {
    const onlines = vi.fn();
    const oncontext = vi.fn();
    const { rerender } = render(ProjectGitDiff, { file, lineAction: 'stage', onlines, oncontext });
    await fireEvent.contextMenu(screen.getByText('B').closest('.unified-row')!);
    const items = oncontext.mock.calls[0][1];
    await rerender({ file: { ...file, patch: patch.replace('+D', '+E') }, lineAction: 'stage', onlines, oncontext });
    items[0].run();
    expect(onlines).not.toHaveBeenCalled();
  });

  it('stages only the hunk under the row when a header separates two changes', async () => {
    const two = { ...file, patch: '@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n@@ -90,3 +90,3 @@\n x\n-y\n+Y\n z\n' };
    const onlines = vi.fn();
    const oncontext = vi.fn();
    render(ProjectGitDiff, { file: two, lineAction: 'stage', onlines, oncontext });
    await fireEvent.contextMenu(screen.getByText('B').closest('.unified-row')!);
    oncontext.mock.calls[0][1][1].run();
    expect(onlines).toHaveBeenCalledWith([2, 3]);
  });
});
