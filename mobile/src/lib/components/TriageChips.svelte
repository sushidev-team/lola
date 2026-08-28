<script lang="ts">
  import { TRIAGE_FILTERS } from "$lib/filters";
  import { kanbanColumn } from "$lib/theme";
  import TouchButton from "./TouchButton.svelte";
  import type { SessionInfo } from "$lib/store.svelte";

  // The triage filter, as a scrolling strip of chips.
  //
  // The buckets are NOT invented here: TRIAGE_FILTERS is derived from theme.ts's
  // KANBAN_COLUMNS, which is a verbatim port of Go's state.KanbanColumns(), and
  // desktop/state_parity_test.go fails the build when the two drift. A phone-side
  // partition would be a third mirror of a list the repository deliberately keeps
  // in exactly two — which is also why the kanban BOARD is not ported (five
  // horizontally-swiped columns is worse than five chips) while its buckets are.

  let {
    value = $bindable(""),
    sessions,
  }: {
    /** "" means everything. */
    value?: string;
    /** For the per-chip counts. A chip showing 0 is worth seeing: it says the
     *  bucket is empty rather than that the filter is broken. */
    sessions: SessionInfo[];
  } = $props();

  const counts = $derived.by(() => {
    const m: Record<string, number> = {};
    for (const s of sessions) {
      const c = kanbanColumn(s.status);
      m[c] = (m[c] ?? 0) + 1;
    }
    return m;
  });
</script>

<div
  class="flex shrink-0 gap-1.5 overflow-x-auto border-b border-edge px-3 py-2 [scrollbar-width:none]"
  role="group"
  aria-label="Filter sessions"
>
  <TouchButton selected={value === ""} onclick={() => (value = "")}>
    All <span class="num text-sm opacity-70">{sessions.length}</span>
  </TouchButton>
  {#each TRIAGE_FILTERS as title (title)}
    <TouchButton
      selected={value === title}
      onclick={() => (value = value === title ? "" : title)}
    >
      {title} <span class="num text-sm opacity-70">{counts[title] ?? 0}</span>
    </TouchButton>
  {/each}
</div>
