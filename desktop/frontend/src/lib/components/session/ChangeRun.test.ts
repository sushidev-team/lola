import { describe, it, expect, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/svelte';
import { tool } from './story.testkit';
import { collapseWork, type RunRowData } from '$lib/turns';
import ChangeRun from './ChangeRun.svelte';
import ToolOutput from './ToolOutput.svelte';

afterEach(cleanup);
const edit = (id: string, path: string, n: number) => tool(id, 'edit', path, { preview: `@@ -1 +1 @@\n-const old = ${n};\n+const next = ${n + 1};`, added: 1, removed: 1 }, n);
const group = (items: ReturnType<typeof tool>[]) => collapseWork(items as never, true)[0] as RunRowData;

describe('inline changes', () => {
  it('opens changes on request and keeps the selected file open as activity moves on', async () => {
    const first = edit('one', 'src/one.ts', 1);
    const second = edit('two', 'src/two.ts', 2);
    const { container, rerender } = render(ChangeRun, { sessionId: 'change-preview', row: group([first]), active: true });
    expect(container.querySelector('[data-change-preview]')).toBeNull();
    expect(container.querySelector('[data-preview]')).toHaveTextContent('const next = 2;');
    await fireEvent.click(screen.getByRole('button', { name: /Changes · 1 file/ }));
    expect(container.querySelector('[data-diff="add"]')).toHaveTextContent('const next = 2;');
    await rerender({ sessionId: 'change-preview', row: group([first, second]), active: true });
    expect(screen.getByRole('button', { name: /Changes · 2 files/ })).toHaveAttribute('aria-expanded', 'true');
    expect(container.querySelector('[data-diff="add"]')).toHaveTextContent('const next = 3;');
    await fireEvent.click(screen.getByRole('button', { name: 'src/one.ts Edited' }));
    expect(container.querySelector('[data-diff="add"]')).toHaveTextContent('const next = 2;');
    await rerender({ sessionId: 'change-preview', row: group([first, second]), active: false });
    expect(container.querySelector('[data-change-preview]')).not.toBeNull();
    expect(container.querySelector('[data-diff="add"]')).toHaveTextContent('const next = 2;');
    await fireEvent.click(screen.getByRole('button', { name: /Changes · 2 files/ }));
    expect(container.querySelector('[data-change-preview]')).toBeNull();
  });

  it('shows unnumbered PHP source as a short highlighted excerpt with full output on demand', async () => {
    const text = Array.from({ length: 15 }, (_, i) => `    $family['people'][] = ['name' => 'Person ${i}'];`).join('\n');
    const { container } = render(ToolOutput, { text, command: "sed -n '972,986p' tests/Feature/WorkspaceTest.php | cut -c1-170" });
    expect(container.querySelector('[data-source]')).not.toBeNull();
    expect(container.querySelector('[data-syntax="str"]')).not.toBeNull();
    expect(container.querySelectorAll('.cv-output-line')).toHaveLength(10);
    await fireEvent.click(screen.getByRole('button', { name: 'Show all 15 lines' }));
    expect(container.querySelectorAll('.cv-output-line')).toHaveLength(15);
    await fireEvent.click(screen.getByRole('button', { name: 'Show less' }));
    expect(container.querySelectorAll('.cv-output-line')).toHaveLength(10);
  });
});
