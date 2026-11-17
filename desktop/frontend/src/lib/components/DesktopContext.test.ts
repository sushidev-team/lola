import { beforeEach, expect, it, vi } from 'vitest';
import { cleanup, render, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import { Window } from '@wailsio/runtime';
import { Publish } from '@bindings/desktop/contextservice';
import DesktopContext from './DesktopContext.svelte';
import { nav } from '$lib/nav.svelte';
import { store, type SessionInfo } from '$lib/store.svelte';
import { reviewWorkspace } from '$lib/reviewworkspace.svelte';

vi.mock('@bindings/desktop/contextservice', () => ({ Publish: vi.fn().mockResolvedValue(undefined) }));
vi.mock('@wailsio/runtime', async (original) => ({
  ...await original<typeof import('@wailsio/runtime')>(),
  Window: { SetTitle: vi.fn().mockResolvedValue(undefined) },
}));

beforeEach(() => {
  cleanup(); vi.clearAllMocks();
  nav.goCockpit(); nav.closeOverlay(); nav.select('a');
  store.hasConfig = true; store.configChecked = true;
  store.sessions = [
    { id: 'a', project: 'donso', title: 'Fix login', issue: 'ENG-123' },
    { id: 'b', project: 'donso', title: 'Investigate sync', issue: '' },
  ] as SessionInfo[];
  vi.spyOn(store, 'displayNameFor').mockImplementation(id => id === 'donso' ? 'Donso' : id);
});

it('publishes readable titles and hidden identity, following the open session over sidebar selection', async () => {
  nav.openSession('a'); nav.select('b');
  const { container } = render(DesktopContext);
  await waitFor(() => expect(Window.SetTitle).toHaveBeenLastCalledWith('ENG-123 · Fix login · Donso · Lola'));
  expect(Publish).toHaveBeenLastCalledWith({ view: 'cockpit', sessionId: 'a', title: 'Fix login', projectId: 'donso', projectName: 'Donso', issueKey: 'ENG-123' });
  expect(container.textContent).toBe('');
  expect(document.title).not.toContain('sessionId');
  nav.openSession('b');
  await waitFor(() => expect(Window.SetTitle).toHaveBeenLastCalledWith('Investigate sync · Donso · Lola'));
  store.sessions = store.sessions.map(s => s.id === 'b' ? { ...s, title: 'Fix sync' } : s);
  await waitFor(() => expect(Window.SetTitle).toHaveBeenLastCalledWith('Fix sync · Donso · Lola'));
  const count = vi.mocked(Publish).mock.calls.length;
  store.sessions = store.sessions.map(s => ({ ...s, agentState: 'working' }));
  await tick();
  expect(Publish).toHaveBeenCalledTimes(count);
});

it('clears attribution on covering views and uses the review overlay target', async () => {
  nav.openSession('a'); render(DesktopContext);
  await waitFor(() => expect(Publish).toHaveBeenCalled());
  nav.openOverlay('settings');
  await waitFor(() => expect(Publish).toHaveBeenLastCalledWith({ view: 'settings' }));
  expect(Window.SetTitle).toHaveBeenLastCalledWith('Lola');
  reviewWorkspace.open('b');
  await waitFor(() => expect(Publish).toHaveBeenLastCalledWith(expect.objectContaining({ view: 'pr-review', sessionId: 'b' })));
  nav.closeOverlay(); nav.goHome();
  await waitFor(() => expect(Publish).toHaveBeenLastCalledWith({ view: 'home' }));
  store.hasConfig = false;
  await waitFor(() => expect(Publish).toHaveBeenLastCalledWith({ view: 'setup' }));
});

it('does not retain the previous ticket while a new session is loading or missing', async () => {
  render(DesktopContext);
  await waitFor(() => expect(Publish).toHaveBeenCalled());
  nav.openSession('not-yet-loaded');
  await waitFor(() => expect(Publish).toHaveBeenLastCalledWith({ view: 'cockpit' }));
  expect(Window.SetTitle).toHaveBeenLastCalledWith('Lola');
});

it('serializes rapid switches and continues after a failed context write', async () => {
  let rejectWrite!: (reason: Error) => void;
  vi.mocked(Publish).mockImplementationOnce(() => new Promise<void>((_, reject) => { rejectWrite = reject; }) as ReturnType<typeof Publish>);
  const warning = vi.spyOn(console, 'warn').mockImplementation(() => {});
  render(DesktopContext);
  await waitFor(() => expect(Publish).toHaveBeenCalledTimes(1));
  nav.openSession('b'); await tick();
  nav.openOverlay('settings'); await tick();
  expect(Publish).toHaveBeenCalledTimes(1);
  expect(Window.SetTitle).not.toHaveBeenCalled(); // The title event must follow the snapshot write.
  rejectWrite(new Error('disk unavailable'));
  await waitFor(() => expect(Publish).toHaveBeenLastCalledWith({ view: 'settings' }));
  expect(Window.SetTitle).toHaveBeenLastCalledWith('Lola');
  expect(warning).toHaveBeenCalled();
  warning.mockRestore();
});
