import { afterEach, expect, it } from 'vitest';
import { render, cleanup, fireEvent, screen } from '@testing-library/svelte';
import SidebarResize from './SidebarResize.svelte';
import { nav } from '$lib/nav.svelte';
afterEach(cleanup);
it('resizes with the keyboard, persists the preference and resets on double click', async () => {
  nav.sidebarOpen = true;
  render(SidebarResize, { width: 320, maxWidth: 480 });
  const handle = screen.getByRole('separator', { name: 'Resize sidebar' });
  await fireEvent.keyDown(handle, { key: 'ArrowRight' });
  expect(nav.sidebarWidth).toBe(330);
  expect(localStorage.getItem('lola.sidebar.width')).toBe('330');
  await fireEvent.keyDown(handle, { key: 'End' });
  expect(nav.sidebarWidth).toBe(480);
  await fireEvent.dblClick(handle);
  expect(nav.sidebarWidth).toBe(320);
});
it('drags the divider within the available width and stops after release', async () => {
  nav.sidebarOpen = true;
  nav.setSidebarWidth(320);
  render(SidebarResize, { width: 320, maxWidth: 400 });
  const handle = screen.getByRole('separator', { name: 'Resize sidebar' });
  handle.setPointerCapture = () => {};
  const pointer = async (type: string, x: number) => {
    const event = new Event(type, { bubbles: true, cancelable: true });
    Object.assign(event, { clientX: x, pointerId: 1, button: 0 });
    await fireEvent(handle, event);
  };
  await pointer('pointerdown', 320);
  await pointer('pointermove', 370);
  expect(nav.sidebarWidth).toBe(370);
  await pointer('pointermove', 600);
  expect(nav.sidebarWidth).toBe(400);
  await pointer('pointerup', 600);
  await pointer('pointermove', 260);
  expect(nav.sidebarWidth).toBe(400);
});
