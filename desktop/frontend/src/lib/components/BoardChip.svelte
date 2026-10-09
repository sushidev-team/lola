<script lang="ts">
  import type { SessionInfo } from "$lib/store.svelte";
  import { boardStale, hasChip, phaseChip, phaseDot, progressText } from "$lib/board";
  import BoardMarker from "./BoardMarker.svelte";

  // The one-cell summary of the agent's self-report: the blocker when there is
  // one (the only part of the board a human must act on), else a thin bar plus
  // its count, else the phase. Used by the list's Plan column and the kanban
  // cards; the session sidebar draws the whole board (BoardPanel). It speaks the
  // sidebar's visual language — the same drawn markers and phase dots — so the
  // two read as one feature.
  //
  // The agent's CLAIM, not a fact: it fades once the report is stale and never
  // replaces the status pill or the PR badge it sits beside. It stays still on
  // purpose: a column of breathing chips would be noise, and the live pulse on
  // the activity line already carries "running".
  let { session }: { session: SessionInfo } = $props();

  const b = $derived(session.board);
  const stale = $derived(boardStale(b));
  const complete = $derived(!!b?.hasProgress && b.percent >= 100);
  const tip = $derived(
    !b
      ? ""
      : [b.phase, b.current ? `now: ${b.current}` : "", b.updatedAgo ? `reported ${b.updatedAgo} ago` : ""]
          .filter(Boolean)
          .join(" · "),
  );
</script>

{#if hasChip(b)}
  {#if b.blocked}
    <span
      class="inline-flex items-center gap-1.5 text-sm whitespace-nowrap text-orange"
      title={`the agent reports it is blocked: ${b.blocked}`}
    >
      <BoardMarker kind="blocked" size={14} /> Blocked
    </span>
  {:else if b.hasProgress}
    <span class="inline-flex items-center gap-1.5 text-sm whitespace-nowrap" class:opacity-55={stale} title={tip}>
      {#if complete}
        <BoardMarker kind="done" size={14} />
      {:else if b.phase}
        <span class="h-1.5 w-1.5 shrink-0 rounded-full {phaseDot(b.phase)}" aria-hidden="true"></span>
      {/if}
      <!-- The bar is drawn, not typed, so its width is exact at every font size.
           role=progressbar carries the value for a screen reader. -->
      <span
        class="relative h-1.5 w-14 overflow-hidden rounded-full bg-edge/60"
        role="progressbar"
        aria-label="reported progress"
        aria-valuemin="0"
        aria-valuemax="100"
        aria-valuenow={b.percent}
      >
        <span
          class="absolute inset-y-0 left-0 rounded-full bg-good transition-[width] duration-700 ease-out"
          style="width:{b.percent}%"
        ></span>
      </span>
      <span class="num text-faint">{progressText(b)}</span>
    </span>
  {:else if b.phase}
    <span
      class="inline-flex items-center gap-1.5 rounded-full px-2 py-[1px] text-sm whitespace-nowrap {phaseChip(b.phase)}"
      class:opacity-55={stale}
      title={tip}
    >
      <span class="h-1.5 w-1.5 rounded-full {phaseDot(b.phase)}" aria-hidden="true"></span>
      {b.phase}
    </span>
  {/if}
{/if}
