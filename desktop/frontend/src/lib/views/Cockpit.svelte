<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import Panel from "$lib/components/Panel.svelte";
  import Rail from "$lib/components/Rail.svelte";
  import SessionsTable from "$lib/components/SessionsTable.svelte";
  import SessionsKanban from "$lib/components/SessionsKanban.svelte";
  import SessionEmbed from "$lib/components/SessionEmbed.svelte";
  import TerminalGrid from "$lib/views/TerminalGrid.svelte";

  const rows = $derived(nav.scoped ? store.sessionsForProject(nav.project) : store.sorted);
  const selected = $derived(store.sessionById(nav.selectedId));
  const lensLabel = $derived(nav.lens === "list" ? "list" : nav.lens === "kanban" ? "kanban" : "grid");

  const lenses: { id: "list" | "kanban" | "grid"; icon: string; label: string }[] = [
    { id: "list", icon: "≡", label: "list" },
    { id: "kanban", icon: "▤", label: "board" },
    { id: "grid", icon: "▦", label: "terminals" },
  ];
</script>

<div class="flex h-full min-h-0 gap-2 p-2">
  <!-- left rail -->
  <aside class="w-[300px] shrink-0 overflow-hidden">
    <Rail />
  </aside>

  <!-- main column -->
  <div class="flex min-w-0 flex-1 flex-col gap-2">
    <div class="flex min-h-0 {nav.lens === 'grid' ? 'flex-1' : 'flex-[3]'}">
      <Panel
        title={nav.scoped ? `Sessions · ${nav.project}` : "Sessions"}
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
                class:text-canvas={nav.lens === l.id}
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
    </div>

    {#if nav.lens !== "grid"}
      <div class="flex min-h-0 flex-[2]">
        <Panel title="Session" pad={false}>
          <SessionEmbed session={selected} />
        </Panel>
      </div>
    {/if}
  </div>
</div>
