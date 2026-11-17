import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import ConfirmDialog from './ConfirmDialog.svelte';
import { confirm } from '$lib/confirm.svelte';

afterEach(() => { cleanup(); confirm.cancel(); });

describe('ConfirmDialog detail', () => {
  it('keeps text that follows a sentence, including paths and repeated endings', () => {
    confirm.ask({ title: 'Stash first?', body: '', detail: 'Your changes will be stashed. Files: src/a.test.ts src/b.test.ts and docs/README', confirmLabel: 'Go', onConfirm: () => {} });
    render(ConfirmDialog);
    expect(screen.getByText('Your changes will be stashed.')).toBeTruthy();
    expect(screen.getByText('Files: src/a.test.ts src/b.test.ts and docs/README')).toBeTruthy();
  });

  it('renders repeated sentences without a duplicate-key failure', () => {
    confirm.ask({ title: 'Twice', body: '', detail: 'Same. Same.', confirmLabel: 'Go', onConfirm: () => {} });
    render(ConfirmDialog);
    expect(screen.getAllByText('Same.')).toHaveLength(2);
  });
});
