<script lang="ts">
  import { tick } from 'svelte';
  import Icon from "$lib/components/Icon.svelte";
  import { makeScope } from '$lib/scope';
  import { bulkProjectActions } from '$lib/projectbulk';
  import { nav } from '$lib/nav.svelte';
  import { store } from '$lib/store.svelte';
  import { talk } from '$lib/talk.svelte';
  import { ssh } from '$lib/ssh.svelte';
  import { waitingQueue } from '$lib/queue';
  import { STATUS_OPTIONS, ATTENTION, matchesTriageFor } from '$lib/filters';
  import { kanbanDotText } from '$lib/theme';
  import { newSessionHere, openShell } from '$lib/projectActions';
  import Button from './Button.svelte';
  import MenuItem from './MenuItem.svelte';
  let { contextTarget }: { contextTarget?: HTMLElement } = $props();
  const scope = $derived(makeScope(nav.scopeNames, store.projects) ?? (() => true));
  const rows = $derived(store.rosterSessions.filter(s => scope(s)));
  const needs = $derived(waitingQueue(rows).length);
  const endedCount = $derived(talk.endedList().filter(e => !store.sessionById(e.id) && scope(e)).length);
  let createOpen = $state(false);
  let createRoot: HTMLDivElement;
  let menu = $state<HTMLDivElement>();
  let trigger: HTMLElement | undefined;
  let searchInput = $state<HTMLInputElement>();
  // The filter lives in a popover: search, then what to show. A live filter marks its button.
  let filterAt = $state<{ x: number; y: number } | null>(null);
  let filterButton: HTMLElement | undefined;
  const filtering = $derived(nav.triage !== "" || nav.sidebarQuery.trim() !== "");
  const counts = $derived.by(() => {
    const waiting = waitingQueue(rows).length;
    const by = (id: string) => rows.filter(s => matchesTriageFor(s, id)).length;
    const attention = rows.filter(s => waitingQueue([s]).length > 0 || matchesTriageFor(s, ATTENTION)).length;
    const m: Record<string, number> = { "": rows.length, [ATTENTION]: attention, "Needs You": waiting, Ended: endedCount };
    for (const id of ["Fixing", "In Review", "Working", "Done"]) m[id] = by(id);
    return m;
  });
  async function toggleFilter(event: MouseEvent) {
    filterButton = event.currentTarget as HTMLElement;
    if (filterAt) { closeFilter(true); return; }
    const r = filterButton.getBoundingClientRect();
    filterAt = { x: Math.max(4, Math.min(r.right - 288, window.innerWidth - 292)), y: r.bottom + 4 };
    await tick();
    searchInput?.focus();
  }
  function closeFilter(refocus = false) {
    filterAt = null;
    if (refocus) filterButton?.focus();
  }
  function clearFilters() { nav.sidebarQuery = ""; nav.setTriage(""); }
  $effect(() => {
    if (!filterAt) return;
    const escape = (e: KeyboardEvent) => { if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); closeFilter(true); } };
    window.addEventListener("keydown", escape, true);
    return () => window.removeEventListener("keydown", escape, true);
  });
  let position = $state({ x: 0, y: 0 });
  // Actions for the selected projects, shown once there are two or more.
  let bulkAt = $state<{ x: number; y: number } | null>(null);
  const bulk = $derived(bulkProjectActions(nav.scopeNames));

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
    trigger = createRoot?.querySelector<HTMLElement>('button') ?? undefined;
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
  <!-- The header carries nothing but the title; the filter and the + appear on hover (or focus),
       and the filter stays lit while it is doing something. -->
  <div class="flex min-w-0 items-center px-1">
    <h2 class="label text-faint">Sessions</h2>
    {#if filtering}<button type="button" class="ml-2 inline-flex h-5 items-center gap-1 rounded-full bg-accent-fill px-2 text-xs text-accent-ink hover:brightness-110" onclick={clearFilters} aria-label="Clear session filters" title="Clear filters">Filtered<Icon name="x" size={11} /></button>{/if}
    <div class="relative ml-auto flex shrink-0 items-center transition-opacity {filtering || filterAt || createOpen ? '' : 'opacity-0 group-hover/side:opacity-100 focus-within:opacity-100'}" bind:this={createRoot}>
      <Button icon size="xs" aria-label="Filter sessions" title="Filter sessions" aria-haspopup="dialog" aria-expanded={filterAt !== null} aria-pressed={filtering} onclick={toggleFilter}><Icon name="list-filter" size={14} /></Button>
      <Button icon size="xs" aria-label="New session" title="New session" aria-haspopup="menu" aria-expanded={createOpen} onclick={toggleCreate}><Icon name="plus" size={14} /></Button>
      {#if createOpen}
        <div bind:this={menu} role="menu" tabindex="-1" aria-label="Create session" style="left:{position.x}px;top:{position.y}px" class="fixed z-50 max-h-[calc(100vh-8px)] w-56 max-w-[calc(100vw-8px)] overflow-y-auto rounded-lg border border-edge bg-panel p-1 shadow-xl" onkeydown={menuKey} oncontextmenu={e => e.preventDefault()}>
          <MenuItem icon="+" trailing="C" onclick={() => choose(newSessionHere)}>New session…</MenuItem>
          <MenuItem icon="⌁" trailing="⌘⇧H" onclick={() => choose(() => ssh.newSession())}>SSH session…</MenuItem>
          <MenuItem icon="$" trailing="⌘⇧N" onclick={() => choose(() => { void openShell(); })}>Temporary shell…</MenuItem>
        </div>
      {/if}
    </div>
  </div>
  {#if nav.scoped}
    <!-- Any of these projects: a session counts when it is part of at least one. -->
    <div class="mt-1.5 flex min-w-0 flex-wrap gap-1" role="group" aria-label="Project filter">
      {#each nav.scopeNames as name (name)}
        <button type="button" class="inline-flex h-6 max-w-full min-w-0 items-center gap-1.5 rounded-full bg-accent-fill py-0 pr-1.5 pl-2 text-sm text-accent-ink hover:brightness-110" onclick={() => nav.toggleScope(name)} aria-label="Remove {store.displayNameFor(name)} from the filter" title="Remove from the filter">
          <Icon name="folder" size={12} /><span class="min-w-0 truncate">{store.displayNameFor(name)}</span><span class="shrink-0 opacity-70" aria-hidden="true"><Icon name="x" size={12} /></span>
        </button>
      {/each}
      {#if nav.scopeNames.length > 1}
        <button type="button" class="inline-flex h-6 items-center gap-1 rounded-full px-2 text-sm text-faint hover:bg-sel hover:text-ink" aria-haspopup="menu" aria-expanded={bulkAt !== null} aria-label="Actions for {nav.scopeNames.length} projects" title="Actions for the selected projects"
          onclick={(e) => { const r = (e.currentTarget as HTMLElement).getBoundingClientRect(); bulkAt = bulkAt ? null : { x: Math.min(r.left, window.innerWidth - 232), y: r.bottom + 4 }; }}>
          <Icon name="ellipsis" size={14} />{nav.scopeNames.length} selected
        </button>
      {/if}
    </div>
  {/if}
</div>

{#if filterAt}
  <div class="fixed inset-0 z-40" role="presentation" onclick={() => closeFilter()}></div>
  <div role="dialog" aria-label="Filter sessions" class="fixed z-50 w-72 overflow-hidden rounded-xl border border-edge bg-panel shadow-2xl" style="left:{filterAt.x}px;top:{filterAt.y}px">
    <div class="relative border-b border-edge/70 px-3 py-2">
      <Icon name="search" size={14} class="absolute top-1/2 left-3 -translate-y-1/2 text-faint" />
      <input bind:this={searchInput} class="w-full bg-transparent pr-6 pl-6 text-sm text-ink outline-none placeholder:text-faint" aria-label="Search sessions" placeholder="Search title, issue, branch, PR…" bind:value={nav.sidebarQuery} />
      {#if nav.sidebarQuery}<button type="button" class="absolute top-1/2 right-2 -translate-y-1/2 text-faint hover:text-ink" aria-label="Clear session search" onclick={() => { nav.sidebarQuery = ''; searchInput?.focus(); }}><Icon name="x" size={14} /></button>{/if}
    </div>
    <div class="p-1.5" role="radiogroup" aria-label="Show">
      {#each STATUS_OPTIONS as o (o.id)}
        {@const n = counts[o.id] ?? 0}
        <button type="button" role="radio" aria-checked={nav.triage === o.id} class="flex min-h-8 w-full items-center gap-2.5 rounded-md px-2 py-1 text-left hover:bg-sel/60 {nav.triage === o.id ? 'bg-sel' : ''} {n === 0 && o.id !== '' && nav.triage !== o.id ? 'opacity-55' : ''}" onclick={() => nav.setTriage(o.id)} title={o.hint}>
          <span class="flex size-3.5 shrink-0 items-center justify-center {o.key ? kanbanDotText(o.key) : 'text-faint'}" aria-hidden="true">{#if o.id === ''}<Icon name="list" size={13} />{:else}<Icon name="circle" size={9} />{/if}</span>
          <span class="min-w-0 flex-1"><span class="block truncate text-sm {nav.triage === o.id ? 'font-medium text-ink' : 'text-ink'}">{o.label}</span>{#if o.hint}<span class="block truncate text-xs text-faint">{o.hint}</span>{/if}</span>
          <span class="num shrink-0 rounded-full px-1.5 text-xs {o.key === 'needs' && n > 0 ? 'bg-orange/20 font-medium text-orange' : 'text-faint'}">{n}</span>
        </button>
      {/each}
    </div>
    {#if filtering}
      <div class="border-t border-edge/70 p-1.5"><button type="button" class="flex h-8 w-full items-center justify-center rounded-md text-sm text-faint hover:bg-sel hover:text-ink" onclick={clearFilters}>Clear filters</button></div>
    {/if}
  </div>
{/if}

{#if bulkAt}
  <div class="fixed inset-0 z-40" role="presentation" onclick={() => (bulkAt = null)}></div>
  <div role="menu" aria-label="Selected projects" class="fixed z-50 w-56 rounded-lg border border-edge bg-panel p-1 shadow-xl" style="left:{bulkAt.x}px;top:{bulkAt.y}px">
    {#each bulk as a (a.id)}
      {#if a.id === "clear"}<div class="my-1 border-t border-edge"></div>{/if}
      <MenuItem variant={a.variant} disabled={!a.enabled} onclick={() => { bulkAt = null; a.run(); }}>{a.label}</MenuItem>
    {/each}
  </div>
{/if}
