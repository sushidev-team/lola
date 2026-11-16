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
  function background() {
    const target = document.createElement('div');
    document.body.append(target);
    const mounted = render(SidebarSessionTools, { props: { contextTarget: target }, target });
    return { target, ...mounted };
  }

  it('opens the shared creation menu on the empty sidebar and preserves the project scope', async () => {
    nav.project = 'nori';
    nav.scoped = true;
    const { target } = background();
    expect(await fireEvent.contextMenu(target, { clientX: 120, clientY: 260 })).toBe(false);
    expect(screen.getByRole('menu', { name: 'Create session' })).toHaveStyle({ left: '120px', top: '260px' });
    expect(screen.getAllByRole('menuitem')).toHaveLength(3);
    expect(screen.getByRole('menuitem', { name: /New session/ })).toHaveFocus();
    await fireEvent.click(screen.getByRole('menuitem', { name: /New session/ }));
    expect(actions.newSessionHere).toHaveBeenCalledOnce();
    expect(nav.project).toBe('nori');
    expect(screen.queryByRole('menu')).toBeNull();
  });

  it('keeps the menu inside the viewport and returns focus to the tools row on Escape', async () => {
    const original = HTMLDivElement.prototype.getBoundingClientRect;
    vi.spyOn(HTMLDivElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLDivElement) {
      return this.getAttribute('role') === 'menu' ? { width: 224, height: 110 } as DOMRect : original.call(this);
    });
    const { target } = background();
    await fireEvent.contextMenu(target, { clientX: window.innerWidth - 2, clientY: window.innerHeight - 2 });
    expect(screen.getByRole('menu')).toHaveStyle({ left: `${window.innerWidth - 228}px`, top: `${window.innerHeight - 114}px` });
    const shortcut = vi.fn();
    window.addEventListener('keydown', shortcut);
    try {
      await fireEvent.keyDown(window, { key: 'Escape' });
      expect(shortcut).not.toHaveBeenCalled();
      expect(screen.queryByRole('menu')).toBeNull();
      expect(screen.getByRole('button', { name: 'Filter sessions' })).toHaveFocus();
    } finally { window.removeEventListener('keydown', shortcut); }
  });

  it('leaves editing and session-row context menus alone', async () => {
    const { target } = background();
    await fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
    expect(await fireEvent.contextMenu(screen.getByRole('textbox', { name: 'Search sessions' }))).toBe(true);
    const row = document.createElement('button');
    const rowMenu = vi.fn((event: Event) => event.preventDefault());
    row.addEventListener('contextmenu', rowMenu);
    target.append(row);
    await fireEvent.contextMenu(row);
    expect(rowMenu).toHaveBeenCalledOnce();
    expect(screen.queryByRole('menu')).toBeNull();
  });

  it('dismisses on outside press and removes its background listener on unmount', async () => {
    const { target, unmount } = background();
    const remove = vi.spyOn(target, 'removeEventListener');
    await fireEvent.contextMenu(target);
    await fireEvent.pointerDown(document.body);
    expect(screen.queryByRole('menu')).toBeNull();
    await unmount();
    expect(remove).toHaveBeenCalledWith('contextmenu', expect.any(Function));
  });

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

  it('filters by status from the popover without changing the open session', async () => {
    nav.openSession('keep-open');
    render(SidebarSessionTools);
    await fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
    expect(screen.getByRole('dialog', { name: 'Filter sessions' })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: /^Needs attention/ })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: /^In review/ })).toBeInTheDocument();
    await fireEvent.click(screen.getByRole('radio', { name: /^Ended/ }));
    expect(nav.triage).toBe('Ended');
    expect(screen.getByRole('radio', { name: /^Ended/ })).toHaveAttribute('aria-checked', 'true');
    expect(nav.focusedTerm).toBe('keep-open');
    await fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(nav.triage).toBe('');
  });
});

describe('session tools focus', () => {
  it('returns focus to the filter button when Escape closes an empty search', async () => {
    render(SidebarSessionTools);
    const filter = screen.getByRole('button', { name: 'Filter sessions' });
    await fireEvent.click(filter);
    await fireEvent.keyDown(screen.getByRole('textbox', { name: 'Search sessions' }), { key: 'Escape' });
    expect(screen.queryByRole('textbox', { name: 'Search sessions' })).toBeNull();
    expect(filter).toHaveFocus();
  });

  it('gives the background menu a mounted return target while the filter is closed', async () => {
    const target = document.createElement('div');
    document.body.append(target);
    render(SidebarSessionTools, { props: { contextTarget: target }, target });
    await fireEvent.contextMenu(target, { clientX: 10, clientY: 10 });
    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByRole('menu')).toBeNull();
    expect(document.activeElement).not.toBe(document.body);
  });
});
