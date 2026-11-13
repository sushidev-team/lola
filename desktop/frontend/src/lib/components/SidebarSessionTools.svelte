<script lang="ts">
  import { tick } from 'svelte';
  import Icon from "$lib/components/Icon.svelte";
  import { nav } from '$lib/nav.svelte';
  import { store } from '$lib/store.svelte';
  import { talk } from '$lib/talk.svelte';
  import { ssh } from '$lib/ssh.svelte';
  import { waitingQueue } from '$lib/queue';
  import { newSessionHere, openShell } from '$lib/projectActions';
  import Button from './Button.svelte';
  import MenuItem from './MenuItem.svelte';
  import Tabs from './Tabs.svelte';
  let { contextTarget }: { contextTarget?: HTMLElement } = $props();
  const rows = $derived(store.rosterSessions.filter(s => !nav.scoped || s.project === nav.project));
  const needs = $derived(waitingQueue(rows).length);
  const endedCount = $derived(talk.endedList().filter(e => !store.sessionById(e.id) && (!nav.scoped || e.project === nav.project)).length);
  let createOpen = $state(false);
  let createRoot: HTMLDivElement;
  let menu = $state<HTMLDivElement>();
  let trigger: HTMLElement | undefined;
  let searchInput: HTMLInputElement;
  let position = $state({ x: 0, y: 0 });

  async function placeAndFocus() {
    await tick();
    if (!createOpen || !menu) return;
    const { width, height } = menu.getBoundingClientRect();
    position = {
      x: Math.max(4, Math.min(position.x, window.innerWidth - width - 4)),
      y: Math.max(4, Math.min(position.y, window.innerHeight - height - 4)),
    };
    menu.querySelector<HTMLElement>('[role="menuitem"]')?.focus();
  }

  async function toggleCreate(event: MouseEvent) {
    trigger = event.currentTarget as HTMLButtonElement;
    const rect = trigger.getBoundingClientRect();
    position = { x: rect.right - 224, y: rect.bottom + 4 };
    createOpen = !createOpen;
    if (createOpen) await placeAndFocus();
  }
  async function openBackgroundMenu(event: MouseEvent) {
    // Session rows own their menu; editing fields keep native copy/paste actions.
    if (event.defaultPrevented || (event.target instanceof Element && event.target.closest('input, textarea, [contenteditable], button, a, [role="menu"]'))) return;
    event.preventDefault();
    trigger = searchInput;
    position = { x: event.clientX, y: event.clientY };
    createOpen = true;
    await placeAndFocus();
  }
  $effect(() => {
    const target = contextTarget;
    target?.addEventListener('contextmenu', openBackgroundMenu);
    return () => target?.removeEventListener('contextmenu', openBackgroundMenu);
  });
  $effect(() => {
    if (!createOpen) return;
    const escape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.preventDefault(); event.stopPropagation(); closeCreate(true);
    };
    window.addEventListener('keydown', escape, true);
    return () => window.removeEventListener('keydown', escape, true);
  });
  function closeCreate(focus = false) {
    createOpen = false;
    if (focus) trigger?.focus();
  }
  function choose(run: () => void) { closeCreate(); run(); }
  function menuKey(event: KeyboardEvent) {
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); closeCreate(true); return; }
    if (event.key === 'Tab') { closeCreate(true); return; }
    if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault(); event.stopPropagation();
    const items = Array.from(menu!.querySelectorAll<HTMLElement>('[role="menuitem"]'));
    const at = items.indexOf(document.activeElement as HTMLElement);
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (at + (event.key === 'ArrowUp' ? -1 : 1) + items.length) % items.length;
    items[next]?.focus();
  }
</script>

<svelte:window onpointerdown={e => { if (createOpen && !createRoot?.contains(e.target as Node)) closeCreate(); }} />

<div class="sidebar-session-tools">
  <div class="flex min-w-0 items-center gap-2">
    <div class="relative min-w-0 flex-1">
      <input bind:this={searchInput} class="sidebar-search" aria-label="Search sessions" placeholder="Search sessions…" bind:value={nav.sidebarQuery}
        onkeydown={e => { if (e.key === 'Escape') { nav.sidebarQuery = ''; e.stopPropagation(); } }} />
      {#if nav.sidebarQuery}<button type="button" class="sidebar-search-clear" aria-label="Clear session search" onclick={() => nav.sidebarQuery = ''}><Icon name="x" size={14} /></button>{/if}
    </div>
    <div class="relative" bind:this={createRoot}>
      <Button icon size="xs" aria-label="New session" title="New session" aria-haspopup="menu" aria-expanded={createOpen} onclick={toggleCreate}><Icon name="plus" size={14} /></Button>
      {#if createOpen}
        <div bind:this={menu} role="menu" tabindex="-1" aria-label="Create session" style="left:{position.x}px;top:{position.y}px" class="fixed z-50 max-h-[calc(100vh-8px)] w-56 max-w-[calc(100vw-8px)] overflow-y-auto rounded-lg border border-edge bg-panel p-1 shadow-xl" onkeydown={menuKey} oncontextmenu={e => e.preventDefault()}>
          <MenuItem icon="+" trailing="⌘N" onclick={() => choose(newSessionHere)}>New session…</MenuItem>
          <MenuItem icon="⌁" trailing="⌘⇧H" onclick={() => choose(() => ssh.newSession())}>SSH session…</MenuItem>
          <MenuItem icon="$" trailing="⌘⇧N" onclick={() => choose(() => { void openShell(); })}>Temporary shell…</MenuItem>
        </div>
      {/if}
    </div>
  </div>
  <div class="mt-2 -mx-4">
    <Tabs quiet tabs={[{ id: '', label: 'All', count: rows.length }, { id: 'Needs You', label: 'Needs you', count: needs }, { id: 'Ended', label: 'Ended', count: endedCount }]}
      active={nav.triage} onSelect={id => nav.setTriage(id)} />
  </div>
  {#if nav.scoped}
    <button type="button" class="mt-2 flex w-full min-w-0 items-center gap-2 text-left text-sm text-accent-ink" onclick={() => nav.goCockpit('')} aria-label="Clear project filter">
      <span class="min-w-0 truncate">{store.displayNameFor(nav.project)}</span><span class="ml-auto" aria-hidden="true"><Icon name="x" size={14} /></span>
    </button>
  {/if}
</div>
