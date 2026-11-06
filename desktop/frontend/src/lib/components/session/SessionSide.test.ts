import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { freshSession, ssh, terms } from './talk.testkit';
import { SSH } from '$lib/terms.svelte';
import SessionSide from './SessionSide.svelte';

afterEach(cleanup);

describe('conversation overview', () => {
  it('shows progress reported by the agent', async () => {
    const id = freshSession({ worktree: '/w', board: {
      updatedAt: new Date().toISOString(), hasProgress: true, percent: 40,
      progressDerived: true, done: 2, total: 5,
      todos: [{ text: 'Implement', state: 'done' }, { text: 'Test', state: 'active' }],
    } });
    terms.select(id, SSH);
    render(SessionSide, { sessionId: id });
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '40');
    expect(screen.getByText('Test')).toBeInTheDocument();

  });

  it('uses posted plans when there is no report board, without inventing progress', () => {
    const id = freshSession({ worktree: '/w' });
    ssh.view(id).items = [{ id: 'plan', kind: 'element', element: {
      id: 'plan', kind: 'plan', title: 'Improve history', steps: [
        { text: 'Inspect', state: 'done' }, { text: 'Implement', state: 'active' },
      ],
    } }] as never;
    render(SessionSide, { sessionId: id });
    expect(screen.getByRole('progressbar', { name: 'Reported plan' })).toHaveAttribute('aria-valuenow', '1');
    expect(screen.getByText('Improve history')).toBeInTheDocument();
    expect(screen.queryByText('No plan reported yet.')).toBeNull();
  });
});
