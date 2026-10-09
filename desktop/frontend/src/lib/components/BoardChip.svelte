<script lang="ts">
  import type { SessionInfo } from "$lib/store.svelte";
  import { boardStale, hasChip, phaseChip, progressText } from "$lib/board";

  // The one-cell summary of the agent's self-report: the blocker when there is
  // one (the only part of the board a human must act on), else a thin bar plus
  // its count, else the phase word. Used by the list's Plan column and the
  // kanban cards; the session sidebar draws the whole board (BoardPanel).
  //
  // The agent's CLAIM, not a fact: it fades once the report is stale and never
  // replaces the status pill or the PR badge it sits beside.
  let { session }: { session: SessionInfo } = $props();

  const b = $derived(session.board);
  const stale = $derived(boardStale(b));
</script>

{#if hasChip(b)}
  {#if b.blocked}
    <span
      class="inline-flex items-center gap-1 text-sm whitespace-nowrap text-orange"
      title={`the agent reports it is blocked: ${b.blocked}`}
    >
      <span aria-hidden="true">⏸</span> Blocked
    </span>
  {:else if b.hasProgress}
    <span
      class="inline-flex items-center gap-1.5 text-sm whitespace-nowrap"
      class:opacity-55={stale}
      title={`${b.current ? `now: ${b.current}` : "reported progress"}${b.updatedAgo ? ` · ${b.updatedAgo} ago` : ""}`}
    >
      <!-- The bar is drawn, not typed, so its width is exact at every font size.
           role=progressbar carries the value for a screen reader. -->
      <span
        class="relative h-1.5 w-12 overflow-hidden rounded-full bg-edge/70"
        role="progressbar"
        aria-label="reported progress"
        aria-valuemin="0"
        aria-valuemax="100"
        aria-valuenow={b.percent}
      >
        <span class="absolute inset-y-0 left-0 rounded-full bg-good" style="width:{b.percent}%"></span>
      </span>
      <span class="num text-faint">{progressText(b)}</span>
    </span>
  {:else if b.phase}
    <span class="rounded px-1.5 py-[1px] text-sm whitespace-nowrap {phaseChip(b.phase)}" class:opacity-55={stale}
      >{b.phase}</span
    >
  {/if}
{/if}
