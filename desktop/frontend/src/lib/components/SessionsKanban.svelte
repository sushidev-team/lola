<script lang="ts">
  import { store, type SessionInfo } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { KANBAN_COLUMNS, statusBadge, statusText } from "$lib/theme";
  import PrBadge from "./PrBadge.svelte";

  let { rows }: { rows: SessionInfo[] } = $props();
  const cols = $derived(
    KANBAN_COLUMNS.map((c) => ({
      title: c.title,
      items: rows.filter((s) => c.statuses.includes(s.status)),
    })),
  );
</script>

<div class="flex h-full min-h-0 gap-2 overflow-x-auto p-1">
  {#each cols as col (col.title)}
    <div class="flex min-w-[13rem] flex-1 flex-col">
      <div class="mb-1 flex items-center gap-1.5 border-b border-edge/60 pb-1 text-xs font-semibold">
        <span>{col.title}</span><span class="text-faint">{col.items.length}</span>
      </div>
      <div class="flex flex-col gap-1 overflow-auto">
        {#each col.items as s (s.id)}
          {@const sel = nav.selectedId === s.id}
          <button
            class="rounded border px-2 py-1 text-left text-xs transition-colors hover:border-accent/60"
            class:border-accent={sel}
            class:border-edge={!sel}
            class:bg-sel={sel}
            onclick={() => nav.select(s.id)}
            ondblclick={() => nav.toggleFocusTerm(s.id)}
          >
            <div class="flex items-center gap-1.5">
              {#if s.status === "needs_input" && !sel}<span class="text-warn">!</span>{/if}
              <span class="font-medium" class:text-accent={sel}>{s.issue || s.id.slice(0, 8)}</span>
              <span class="ml-auto font-mono text-[10px] {statusText(s.status)}">{statusBadge(s.status)}</span>
            </div>
            {#if s.title}<div class="truncate text-[11px] text-faint">{s.title}</div>{/if}
            <div class="mt-0.5"><PrBadge session={s} /></div>
          </button>
        {/each}
      </div>
    </div>
  {/each}
</div>
