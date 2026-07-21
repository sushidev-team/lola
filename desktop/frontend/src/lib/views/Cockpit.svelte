<script lang="ts">
  import { store, sortSessions } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import Panel from "$lib/components/Panel.svelte";
  import Rail from "$lib/components/Rail.svelte";
  import SessionsTable from "$lib/components/SessionsTable.svelte";
  import SessionsKanban from "$lib/components/SessionsKanban.svelte";
  import SessionEmbed from "$lib/components/SessionEmbed.svelte";
  import TerminalGrid from "$lib/views/TerminalGrid.svelte";
  import { reflowGridRows } from "$lib/reflow";

  // Sort straight off store.sessions ($state), NOT via a chained class-$derived:
  // the rail's direct store.sessions reads stay live in the production webview
  // while a read of a cross-module derived-of-a-derived did not re-render on the
  // async push (the list stayed empty until a manual re-render). Sorting is cheap.
  const rows = $derived.by(() => {
    const sorted = sortSessions(store.sessions);
    return nav.scoped ? sorted.filter((s) => s.project === nav.project) : sorted;
  });
  const selected = $derived(store.sessionById(nav.selectedId));
  // When a terminal is focused (the ⛶ focus button or a grid-tile click), it
  // takes over the whole cockpit as one big interactive terminal.
  const focusedSession = $derived(store.sessionById(nav.focusedTerm));

  // Keep a live selection: pick the first row when nothing is selected, and
  // re-pick if the selected session drops out of the list (killed/filtered).
  $effect(() => {
    if (rows.length > 0 && !rows.some((r) => r.id === nav.selectedId)) {
      nav.select(rows[0].id);
    }
  });
  const lensLabel = $derived(nav.lens === "list" ? "list" : nav.lens === "kanban" ? "kanban" : "grid");

  const lenses: { id: "list" | "kanban" | "grid"; icon: string; label: string }[] = [
    { id: "list", icon: "≡", label: "list" },
    { id: "kanban", icon: "▤", label: "board" },
    { id: "grid", icon: "▦", label: "terminals" },
  ];
</script>

{#if focusedSession}
  <!-- Focused terminal: the whole cockpit is one big interactive terminal. Grid
       so the panel stretches to fill (a plain block leaves it content-height). -->
  <div class="grid h-full min-h-0 p-2" style="grid-template-rows:minmax(0,1fr)" {@attach reflowGridRows}>
    <Panel focused pad={false}>
      <SessionEmbed session={focusedSession} focused />
    </Panel>
  </div>
{:else}
<!-- Outer GRID, not a flex row: WebKit sizes a nested grid's fr tracks off its
     container height, and a flex-1 item's height is resolved too late on first
     paint (the panels collapse until a later relayout). A grid *cell* gives the
     main column a definite height from the first frame, so its own fr rows
     resolve immediately. See CLAUDE.md's WebKit gotcha + $lib/reflow. -->
<div
  class="grid h-full min-h-0 gap-2 p-2"
  style="grid-template-columns:300px minmax(0,1fr)"
>
  <!-- left rail -->
  <aside class="min-h-0 overflow-hidden">
    <Rail />
  </aside>

  <!-- main column: a grid so panels stretch to full width AND height in WebKit -->
  <div
    class="grid min-w-0 min-h-0 gap-2"
    style="grid-template-rows:{nav.lens === 'grid' ? 'minmax(0,1fr)' : 'minmax(0,3fr) minmax(0,2fr)'}"
    {@attach reflowGridRows}
  >
    <Panel
      title={nav.scoped ? `Sessions · ${store.displayNameFor(nav.project)}` : "Sessions"}
      note={lensLabel}
      count={rows.length}
      focused
      pad={false}
    >
      {#snippet actions()}
        <span class="flex items-center gap-0.5 rounded border border-edge p-0.5">
          {#each lenses as l (l.id)}
            <button
              class="rounded px-1.5 py-[1px] text-[11px] font-normal"
              class:bg-accent={nav.lens === l.id}
              class:text-on-accent={nav.lens === l.id}
              class:text-faint={nav.lens !== l.id}
              title={l.label}
              onclick={() => (nav.lens = l.id)}>{l.icon}</button
            >
          {/each}
        </span>
      {/snippet}

      {#if nav.lens === "list"}
        <SessionsTable {rows} />
      {:else if nav.lens === "kanban"}
        <SessionsKanban {rows} />
      {:else}
        <TerminalGrid {rows} />
      {/if}
    </Panel>

    {#if nav.lens !== "grid"}
      <Panel pad={false}>
        <SessionEmbed session={selected} />
      </Panel>
    {/if}
  </div>
</div>
{/if}
