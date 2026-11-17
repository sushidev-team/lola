import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/svelte';
import { freshSession, setTalk, ssh, talkSvc, sshSvc } from './talk.testkit';
import QueueRows from './QueueRows.svelte';

afterEach(cleanup);
function queued(id: string) {
  const item = { id: 'q1', kind: 'human', human: { text: 'Check this too', delivered: false } };
  ssh.view(id).items = [item] as never;
  sshSvc.Timeline.mockResolvedValue({ session: id, items: [item], connections: [], lastSeq: 1 });
}

describe('queued messages', () => {
  it('pushes the queue on demand and immediately shows loading until the daemon answers', async () => {
    const id = freshSession({ agentState: 'working' });
    await setTalk(id, { composer: 'working' });
    queued(id);
    let answer!: (value: unknown) => void;
    talkSvc.QueueRelease.mockImplementationOnce(() => new Promise(resolve => answer = resolve));
    render(QueueRows, { sessionId: id });
    const send = screen.getByRole('button', { name: 'Send now' });
    await fireEvent.click(send);
    expect(talkSvc.QueueRelease).toHaveBeenLastCalledWith({ session: id });
    expect(send).toBeDisabled();
    expect(send).toHaveAttribute('aria-busy', 'true');
    answer({ delivered: false, why: 'The agent has a draft in its input.' });
    await waitFor(() => expect(send).not.toBeDisabled());
    expect(screen.getByRole('status')).toHaveTextContent('The agent has a draft in its input.');
    expect(screen.queryByText('sends together when it rests')).toBeNull();
  });

  it('removes delivered rows after the acknowledged send and refresh', async () => {
    const id = freshSession();
    queued(id);
    talkSvc.QueueRelease.mockResolvedValueOnce({ delivered: true });
    sshSvc.Timeline.mockResolvedValue({ session: id, items: [{ id: 'q1', kind: 'human', human: { text: 'Check this too', delivered: true } }], connections: [], lastSeq: 2 });
    render(QueueRows, { sessionId: id });
    await fireEvent.click(screen.getByRole('button', { name: 'Send now' }));
    await waitFor(() => expect(screen.queryByText('Check this too')).toBeNull());
  });

  it('keeps queued text and allows retry when sending fails', async () => {
    const id = freshSession();
    queued(id);
    talkSvc.QueueRelease.mockRejectedValueOnce(new Error('Connection lost'));
    render(QueueRows, { sessionId: id });
    const send = screen.getByRole('button', { name: 'Send now' });
    await fireEvent.click(send);
    await waitFor(() => expect(send).not.toBeDisabled());
    expect(screen.getByText('Check this too')).toBeInTheDocument();
  });
});
