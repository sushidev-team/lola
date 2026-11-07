<script lang="ts">
  import { nav } from '$lib/nav.svelte';
  import { store } from '$lib/store.svelte';
  import { waitingQueue } from '$lib/queue';
  import { newSessionHere } from '$lib/projectActions';
  import Button from './Button.svelte';
  const rows = $derived(store.rosterSessions.filter(s => !nav.scoped || s.project === nav.project));
  const needs = $derived(waitingQueue(rows).length);
</script>

<div class="sidebar-session-tools">
  <div class="flex min-w-0 items-center gap-2">
    <div class="relative min-w-0 flex-1">
      <input class="sidebar-search" aria-label="Search sessions" placeholder="Search sessions…" bind:value={nav.sidebarQuery}
        onkeydown={e => { if (e.key === 'Escape') { nav.sidebarQuery = ''; e.stopPropagation(); } }} />
      {#if nav.sidebarQuery}<button type="button" class="sidebar-search-clear" aria-label="Clear session search" onclick={() => nav.sidebarQuery = ''}>×</button>{/if}
    </div>
    <Button icon size="xs" aria-label="New session" title="New session (⌘N)" onclick={newSessionHere}>+</Button>
  </div>
  <div class="mt-2 flex items-center gap-1" aria-label="Session filters">
    <button type="button" class="sidebar-filter" aria-pressed={!nav.triage} onclick={() => nav.setTriage('')}>All <span class="num">{rows.length}</span></button>
    <button type="button" class="sidebar-filter" aria-pressed={nav.triage === 'Needs You'} onclick={() => nav.setTriage('Needs You')}>Needs you <span class="num">{needs}</span></button>
  </div>
  {#if nav.scoped}
    <button type="button" class="mt-2 flex w-full min-w-0 items-center gap-2 text-left text-sm text-accent-ink" onclick={() => nav.goCockpit('')} aria-label="Clear project filter">
      <span class="min-w-0 truncate">{store.displayNameFor(nav.project)}</span><span class="ml-auto" aria-hidden="true">×</span>
    </button>
  {/if}
</div>
