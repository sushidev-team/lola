import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render } from '@testing-library/svelte';
import { tick } from 'svelte';
import ToolPreview from './ToolPreview.svelte';

afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); });

describe('tool excerpts', () => {
  const output = (count: number) => Array.from({ length: count }, (_, i) => `Line ${i + 1}`).join('\n');
  const visible = (container: HTMLElement) => [...container.querySelectorAll('.cv-output-code')].map(line => line.textContent);

  it('advances a four-line window from the beginning to the end, then stops', async () => {
    vi.useFakeTimers();
    const { container, unmount } = render(ToolPreview, { text: output(10) });
    expect(visible(container)).toEqual(['Line 1', 'Line 2', 'Line 3', 'Line 4']);
    const second = container.querySelectorAll('.cv-output-line')[1];
    await vi.advanceTimersByTimeAsync(500);
    expect(visible(container)).toEqual(['Line 2', 'Line 3', 'Line 4', 'Line 5']);
    expect(container.querySelector('.cv-output-line')).toBe(second);
    await vi.advanceTimersByTimeAsync(140);
    expect(visible(container)).toEqual(['Line 3', 'Line 4', 'Line 5', 'Line 6']);
    await vi.advanceTimersByTimeAsync(5000);
    expect(visible(container)).toEqual(['Line 7', 'Line 8', 'Line 9', 'Line 10']);
    await unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('continues through appended output and resets when the output is replaced', async () => {
    vi.useFakeTimers();
    const { container, rerender } = render(ToolPreview, { text: output(6) });
    await vi.advanceTimersByTimeAsync(1000);
    expect(visible(container)[0]).toBe('Line 3');
    await rerender({ text: output(9) });
    expect(visible(container)[0]).toBe('Line 3');
    await vi.advanceTimersByTimeAsync(500);
    expect(visible(container)).toEqual(['Line 6', 'Line 7', 'Line 8', 'Line 9']);
    await rerender({ text: 'Replacement\nSecond\nThird\nFourth\nFifth' });
    expect(visible(container)).toEqual(['Replacement', 'Second', 'Third', 'Fourth']);
  });

  it('pauses for reading and expands the same output element in place', async () => {
    vi.useFakeTimers();
    const { container, rerender } = render(ToolPreview, { text: output(8) });
    const surface = container.querySelector('[data-preview-surface]')!;
    const pre = container.querySelector('[data-tool-output]')!;
    await fireEvent.pointerEnter(surface);
    await vi.advanceTimersByTimeAsync(2000);
    expect(visible(container)[0]).toBe('Line 1');
    await fireEvent.pointerLeave(surface);
    await vi.advanceTimersByTimeAsync(500);
    expect(visible(container)[0]).toBe('Line 2');
    await rerender({ expanded: true });
    expect(container.querySelector('[data-tool-output]')).toBe(pre);
    expect(visible(container)).toHaveLength(8);
    await vi.advanceTimersByTimeAsync(2000);
    await rerender({ expanded: false });
    expect(container.querySelector('[data-tool-output]')).toBe(pre);
    expect(visible(container)[0]).toBe('Line 2');
    await fireEvent.focusIn(pre);
    await vi.advanceTimersByTimeAsync(2000);
    expect(visible(container)[0]).toBe('Line 2');
  });

  it('does not auto-scroll with reduced motion', async () => {
    vi.useFakeTimers();
    const preference = window.matchMedia('(prefers-reduced-motion: reduce)');
    vi.spyOn(window, 'matchMedia').mockReturnValue({ ...preference, matches: true });
    const { container } = render(ToolPreview, { text: output(10) });
    await vi.advanceTimersByTimeAsync(5000);
    expect(visible(container)).toEqual(['Line 1', 'Line 2', 'Line 3', 'Line 4']);
  });

  it('shows four consecutive source lines with gutters, highlighting and relative indentation', () => {
    const text = Array.from({ length: 12 }, (_, i) => `${1150 + i}\t${' '.repeat(i === 1 ? 24 : 20)}const value${i} = "example";`).join('\n');
    const { container } = render(ToolPreview, { text, sourcePath: 'src/Editor.tsx' });
    const lines = container.querySelectorAll('.cv-output-line');
    expect(lines).toHaveLength(4);
    expect(lines[0].querySelector('.cv-output-number')).toHaveTextContent('1150');
    expect(lines[3].querySelector('.cv-output-number')).toHaveTextContent('1153');
    expect(lines[0].querySelector('.cv-output-code')?.textContent).toBe('const value0 = "example";');
    expect(lines[1].querySelector('.cv-output-code')?.textContent).toBe('    const value1 = "example";');
    expect(container.querySelector('[data-syntax="str"]')).not.toBeNull();
    expect(container).not.toHaveTextContent('value4');
    expect(container.querySelector('button')).toBeNull();
  });

  it('paces incoming fragments and shows the final result immediately in the same preview', async () => {
    vi.useFakeTimers();
    const { container, rerender } = render(ToolPreview, { text: 'Starting checks', running: true });
    const preview = container.querySelector('[data-preview]');
    await rerender({ text: 'Checking imports', running: true });
    await vi.advanceTimersByTimeAsync(80);
    await rerender({ text: 'Checking types', running: true });
    expect(preview).toHaveTextContent('Starting checks');
    await vi.advanceTimersByTimeAsync(80);
    await tick();
    expect(preview).toHaveTextContent('Checking types');
    await rerender({ text: 'All checks passed', running: false });
    expect(preview).toHaveTextContent('All checks passed');
    expect(container.querySelector('[data-preview]')).toBe(preview);
    await vi.advanceTimersByTimeAsync(1000);
    expect(preview).toHaveTextContent('All checks passed');
  });

  it('keeps existing lines mounted when more output arrives so they do not replay', async () => {
    vi.useFakeTimers();
    const { container, rerender } = render(ToolPreview, { text: 'First result', running: true });
    const first = container.querySelector('.cv-output-line');
    await rerender({ text: 'First result\nSecond result\nThird result', running: true });
    await vi.advanceTimersByTimeAsync(160);
    await tick();
    const lines = container.querySelectorAll('.cv-output-line');
    expect(lines).toHaveLength(3);
    expect(lines[0]).toBe(first);
    expect(lines[1]).toHaveTextContent('Second result');
    expect(lines[2]).toHaveTextContent('Third result');
  });

  it('reserves no space for an empty result or a no-output marker', async () => {
    const { container, rerender } = render(ToolPreview, {});
    expect(container.querySelector('[data-preview]')).toBeNull();
    await rerender({ text: '(Bash completed with no output)' });
    expect(container.querySelector('[data-preview]')).toBeNull();
    await rerender({ text: 'No matches found' });
    expect(container.querySelectorAll('.cv-output-line')).toHaveLength(1);
    expect(container).toHaveTextContent('No matches found');
  });

  it('waits for actual output without a placeholder and cancels pending work on unmount', async () => {
    vi.useFakeTimers();
    const { container, rerender, unmount } = render(ToolPreview, { running: true });
    expect(container.querySelector('[data-preview]')).toBeNull();
    await rerender({ text: 'Pending fragment', running: true });
    await unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});
