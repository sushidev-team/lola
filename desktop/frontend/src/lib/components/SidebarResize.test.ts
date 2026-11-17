import { afterEach, expect, it } from 'vitest';
import { render, cleanup, fireEvent, screen } from '@testing-library/svelte';
import SidebarResize from './SidebarResize.svelte';
import { nav } from '$lib/nav.svelte';
afterEach(() => { cleanup(); nav.closeOverlay(); });
it('resizes with the keyboard, persists the preference and resets on double click', async () => {
  nav.sidebarOpen = true;
  render(SidebarResize, { width: 540, maxWidth: 760 });
  const handle = screen.getByRole('separator', { name: 'Resize sidebar' });
  await fireEvent.keyDown(handle, { key: 'ArrowRight' });
  expect(nav.sidebarWidth).toBe(550);
  expect(localStorage.getItem('lola.sidebar.width')).toBe('550');
  await fireEvent.keyDown(handle, { key: 'End' });
  expect(nav.sidebarWidth).toBe(760);
  await fireEvent.dblClick(handle);
  expect(nav.sidebarWidth).toBe(540);
});
it('drags the divider within the available width and stops after release', async () => {
  nav.sidebarOpen = true;
  nav.setSidebarWidth(540);
  render(SidebarResize, { width: 540, maxWidth: 640 });
  const handle = screen.getByRole('separator', { name: 'Resize sidebar' });
  handle.setPointerCapture = () => {};
  const pointer = async (type: string, x: number) => {
    const event = new Event(type, { bubbles: true, cancelable: true });
    Object.assign(event, { clientX: x, pointerId: 1, button: 0 });
    await fireEvent(handle, event);
  };
  await pointer('pointerdown', 540);
  await pointer('pointermove', 590);
  expect(nav.sidebarWidth).toBe(590);
  await pointer('pointermove', 900);
  expect(nav.sidebarWidth).toBe(640);
  await pointer('pointerup', 900);
  await pointer('pointermove', 260);
  expect(nav.sidebarWidth).toBe(640);
});

it('removes the splitter and cancels a drag while an overlay is open',async()=>{
  nav.sidebarOpen=true; nav.setSidebarWidth(540);
  render(SidebarResize,{width:540,maxWidth:760});
  const handle=screen.getByRole('separator',{name:'Resize sidebar'});
  handle.setPointerCapture=()=>{};
  const event=new Event('pointerdown',{bubbles:true});
  Object.assign(event,{button:0,pointerId:1,clientX:540});
  await fireEvent(handle,event);
  nav.openOverlay('pr-review');
  await import('svelte').then(s=>s.tick());
  expect(screen.queryByRole('separator')).toBeNull();
  await fireEvent.keyDown(handle,{key:'End'});
  expect(nav.sidebarWidth).toBe(540);
  nav.closeOverlay();
  await import('svelte').then(s=>s.tick());
  const restored=screen.getByRole('separator',{name:'Resize sidebar'});
  const move=new Event('pointermove',{bubbles:true});
  Object.assign(move,{pointerId:1,clientX:450});
  await fireEvent(restored,move);
  expect(nav.sidebarWidth).toBe(540);
});
