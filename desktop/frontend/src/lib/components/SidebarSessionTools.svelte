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
  const rows = $derived(store.rosterSessions.filter(s => !nav.scoped || s.project === nav.project));
  const needs = $derived(waitingQueue(rows).length);
  const endedCount = $derived(talk.endedList().filter(e => !store.sessionById(e.id) && (!nav.scoped || e.project === nav.project)).length);
  let createOpen = $state(false);
  let createRoot: HTMLDivElement;
  let menu = $state<HTMLDivElement>();
  let trigger: HTMLButtonElement | undefined;

  async function toggleCreate(event: MouseEvent) {
    trigger = event.currentTarget as HTMLButtonElement;
    createOpen = !createOpen;
    if (createOpen) { await tick(); menu?.querySelector<HTMLElement>('[role="menuitem"]')?.focus(); }
  }
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
      <input class="sidebar-search" aria-label="Search sessions" placeholder="Search sessions…" bind:value={nav.sidebarQuery}
        onkeydown={e => { if (e.key === 'Escape') { nav.sidebarQuery = ''; e.stopPropagation(); } }} />
      {#if nav.sidebarQuery}<button type="button" class="sidebar-search-clear" aria-label="Clear session search" onclick={() => nav.sidebarQuery = ''}><Icon name="x" size={14} /></button>{/if}
    </div>
    <div class="relative" bind:this={createRoot}>
      <Button icon size="xs" aria-label="New session" title="New session" aria-haspopup="menu" aria-expanded={createOpen} onclick={toggleCreate}><Icon name="plus" size={14} /></Button>
      {#if createOpen}
        <div bind:this={menu} role="menu" tabindex="-1" aria-label="Create session" class="absolute top-full right-0 z-50 mt-1 w-56 rounded-lg border border-edge bg-panel p-1 shadow-xl" onkeydown={menuKey}>
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
