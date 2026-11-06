<script lang="ts">
  // One line for a run of calls, and it names them: a bare count is the row this design exists to
  // avoid. Opening it keeps the line where it is and grows downward, one indent in. Inside an opened
  // line it is a child row, where the mono comes back because someone asked to dig.
  import { feed } from "$lib/feed.svelte";
  import { formatMs } from "$lib/ssh";
  import { clipEnd, clipMiddle, diffStat, fitNames, workSegments, type RunRowData } from "$lib/turns";
  import Chevron from "../Chevron.svelte";
  import DigLine from "./DigLine.svelte";
  import ToolRow from "./ToolRow.svelte";

  let {
    sessionId,
    row,
    chars = 72,
    readonly = false,
    nested = false,
  }: { sessionId: string; row: RunRowData; chars?: number; readonly?: boolean; nested?: boolean } = $props();

  const f = $derived(feed.view(sessionId));
  const open = $derived(!readonly && f.expanded.has(row.id));
  // The status column and the verb take a share of the width whatever the names do.
  const fit = $derived(fitNames(row.names, Math.max(16, chars - 16), row.category === "read" || row.category === "edit" ? clipMiddle : clipEnd));
  const tail = $derived(
    row.category === "edit"
      ? diffStat(row.added, row.removed)
      : row.category === "read"
        ? ""
        : formatMs(row.durationMs),
  );
  const full = $derived(row.names.join(", "));

  function toggle() {
    if (f.expanded.has(row.id)) f.expanded.delete(row.id);
    else f.expanded.add(row.id);
  }
</script>

{#snippet cells()}
  <span class="text-faint">{row.verb}</span>
  <span class="flex min-w-0 items-baseline gap-[1ch] font-mono text-ink-2">
    <span class="min-w-0 truncate">{fit.shown.join(", ")}</span>
    {#if fit.rest > 0}<span class="num shrink-0 text-faint">+{fit.rest}</span>{/if}
  </span>
  <span class="num flex items-baseline gap-2 whitespace-nowrap text-faint">
    {#if row.failed > 0}<span class={row.settled ? "text-ink-2" : "text-bad"}>{row.failed} failed</span>{/if}
    {#if tail}<span>{tail}</span>{/if}
  </span>
  <span class="grid place-items-center self-center text-faint group-hover:text-ink">{#if !readonly}<Chevron {open} />{/if}</span>
{/snippet}

<div data-run={row.category}>
  {#if nested && readonly}
    <div class="grid h-5 grid-cols-[44px_minmax(0,1fr)_auto_16px] items-baseline gap-x-2 text-sm leading-5" title={full}>{@render cells()}</div>
  {:else if nested}
    <!-- The row is the control, as in ToolRow. -->
    <button
      type="button"
      class="group -mx-2 grid h-5 w-[calc(100%+16px)] grid-cols-[44px_minmax(0,1fr)_auto_16px] items-baseline gap-x-2 rounded-sm px-2 text-left text-sm leading-5 hover:bg-sel/30 focus-visible:-outline-offset-1"
      title={full}
      aria-expanded={open}
      onclick={toggle}
    >
      {@render cells()}
    </button>
  {:else}
    <DigLine
      segments={workSegments([row])}
      control={!readonly}
      chevron={!readonly}
      {open}
      word={row.failed > 0 ? `${row.failed} failed` : ""}
      bad={row.failed > 0 && !row.settled}
      tail={row.category === "edit" || row.category === "read" ? "" : tail}
      title={full}
      onclick={toggle}
    />
  {/if}
  {#if open}
    <div class="pl-4">
      {#each row.items as m (m.id)}
        <div data-row={m.id}><ToolRow {sessionId} row={m} chars={chars - 2} nested /></div>
      {/each}
    </div>
  {/if}
</div>
