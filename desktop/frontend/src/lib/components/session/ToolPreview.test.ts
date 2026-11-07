import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render } from '@testing-library/svelte';
import { tick } from 'svelte';
import ToolPreview from './ToolPreview.svelte';

afterEach(() => { cleanup(); vi.useRealTimers(); });

describe('tool excerpts', () => {
  it('paces incoming fragments and shows the final result immediately in the same preview', async () => {
    vi.useFakeTimers();
    const { container, rerender } = render(ToolPreview, { text: 'Starting checks', running: true });
    const preview = container.querySelector('[data-preview]');
    await rerender({ text: 'Checking imports', running: true });
    await vi.advanceTimersByTimeAsync(400);
    await rerender({ text: 'Checking types', running: true });
    expect(preview).toHaveTextContent('Starting checks');
    await vi.advanceTimersByTimeAsync(400);
    await tick();
    expect(preview).toHaveTextContent('Checking types');
    await rerender({ text: 'All checks passed', running: false });
    expect(preview).toHaveTextContent('All checks passed');
    expect(container.querySelector('[data-preview]')).toBe(preview);
    await vi.advanceTimersByTimeAsync(1000);
    expect(preview).toHaveTextContent('All checks passed');
  });

  it('shows an honest waiting state without inventing output and cancels pending work on unmount', async () => {
    vi.useFakeTimers();
    const { container, rerender, unmount } = render(ToolPreview, { running: true });
    expect(container).toHaveTextContent('Waiting for output…');
    await rerender({ text: 'Pending fragment', running: true });
    await unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});
