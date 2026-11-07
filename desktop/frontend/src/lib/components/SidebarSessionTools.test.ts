import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';

vi.mock('@bindings/desktop', () => ({ SSHService: {}, DaemonService: {}, ConfigService: {}, TermService: {} }));
const actions = vi.hoisted(() => ({ newSessionHere: vi.fn(), openShell: vi.fn() }));
vi.mock('$lib/projectActions', () => actions);
const { default: SidebarSessionTools } = await import('./SidebarSessionTools.svelte');
const { store } = await import('$lib/store.svelte');
const { nav } = await import('$lib/nav.svelte');
const { ssh } = await import('$lib/ssh.svelte');

beforeEach(() => { cleanup(); vi.clearAllMocks(); nav.triage = ''; nav.scoped = false; nav.sidebarQuery = ''; store.sessions = []; });
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('session tools', () => {
  it('offers session, SSH and temporary creation from the one plus menu', async () => {
    const sshNew = vi.spyOn(ssh, 'newSession').mockImplementation(() => {});
    render(SidebarSessionTools);
    const plus = screen.getByRole('button', { name: 'New session' });
    await fireEvent.click(plus);
    expect(screen.getAllByRole('menuitem')).toHaveLength(3);
    await fireEvent.click(screen.getByRole('menuitem', { name: /SSH session/ }));
    expect(sshNew).toHaveBeenCalledOnce();
    expect(screen.queryByRole('menu')).toBeNull();
    await fireEvent.click(plus);
    await fireEvent.click(screen.getByRole('menuitem', { name: /Temporary shell/ }));
    expect(actions.openShell).toHaveBeenCalledOnce();
    await fireEvent.click(plus);
    await fireEvent.click(screen.getByRole('menuitem', { name: /New session/ }));
    expect(actions.newSessionHere).toHaveBeenCalledOnce();
  });

  it('supports menu keyboard navigation and returns focus on Escape', async () => {
    render(SidebarSessionTools);
    const plus = screen.getByRole('button', { name: 'New session' });
    await fireEvent.click(plus);
    const first = screen.getByRole('menuitem', { name: /New session/ });
    expect(first).toHaveFocus();
    await fireEvent.keyDown(first, { key: 'ArrowDown' });
    const sshItem = screen.getByRole('menuitem', { name: /SSH session/ });
    expect(sshItem).toHaveFocus();
    await fireEvent.keyDown(sshItem, { key: 'Escape' });
    expect(screen.queryByRole('menu')).toBeNull();
    expect(plus).toHaveFocus();
  });

  it('selects Ended with the keyboard without changing the open session', async () => {
    nav.openSession('keep-open');
    render(SidebarSessionTools);
    await fireEvent.keyDown(screen.getByRole('tab', { name: /^All/ }), { key: 'End' });
    expect(nav.triage).toBe('Ended');
    expect(screen.getByRole('tab', { name: /^Ended/ })).toHaveAttribute('aria-selected', 'true');
    expect(nav.focusedTerm).toBe('keep-open');
  });
});
