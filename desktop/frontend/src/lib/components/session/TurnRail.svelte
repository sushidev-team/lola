<script lang="ts">
  // The top of Changes' left column: everything against the base, then one row per turn, newest
  // first, the one a bad turn most likely wants. Files sit below it, drawn by DiffView, because
  // that list follows the diff's scroll.
  //
  // A restore's safety snapshot and the pre-launch baseline are lola's own markers rather than
  // turns, so they read fainter and say what they are. Rows are hand-rolled nav rows like the
  // roster's: two pieces of text, not a control with a label.
  import type { CheckpointInfo } from "@bindings/internal/protocol";
  import { checkpoints } from "$lib/checkpoints.svelte";
  import { changes } from "$lib/changes.svelte";
  import { terms, CHECKPOINTS, DIFF } from "$lib/terms.svelte";
  import { agoPhrase } from "$lib/time";

  let { sessionId, turn = 0 }: { sessionId: string; turn?: number } = $props();

  const st = $derived(checkpoints.of(sessionId));
  const newestFirst = $derived([...st.list].reverse());
  const stat = $derived(changes.of(sessionId));

  function isMarker(c: CheckpointInfo): boolean {
    return c.label === "start" || c.label.startsWith("before restore");
  }
  function markerWord(c: CheckpointInfo): string {
    return c.label === "start" ? "start" : "before restore";
  }
  /** A label is `turn 3`, or `turn 3 · what it did`, and the number already has its own place. */
  function description(c: CheckpointInfo): string {
    return c.label.replace(/^turn \d+( · )?/, "");
  }
  function pick(c: CheckpointInfo) {
    checkpoints.select(sessionId, c.seq);
    terms.select(sessionId, CHECKPOINTS);
  }
  const ROW = "grid h-7 w-full grid-cols-[minmax(0,1fr)_auto] items-center gap-x-2 rounded px-2 text-left transition-colors";
</script>

<div class="grid min-h-0 grid-rows-[auto_auto_minmax(0,1fr)] gap-y-1 px-1.5 pt-2 pb-1 text-sm" data-turn-rail>
  <button type="button" class="{ROW} {!turn ? 'bg-sel text-ink' : 'text-ink-2 hover:bg-sel/50'}" aria-current={!turn ? "true" : undefined} onclick={() => terms.select(sessionId, DIFF)}>
    <span class="truncate">All vs base</span>
    {#if stat}<span class="num text-faint"><span class="text-good">+{stat.add}</span> <span class="text-bad">−{stat.del}</span></span>{/if}
  </button>

  <h3 class="label flex h-6 items-center justify-between px-2 text-faint">Turns</h3>

  <div class="min-h-0 overflow-y-auto">
    {#if st.error}
      <p class="px-2 py-1 break-words text-bad">{st.error}</p>
    {:else if st.list.length === 0 && !st.loading}
      <p class="px-2 py-1 text-faint">No turns yet. One is recorded each time the agent ends a turn.</p>
    {/if}
    <ol aria-label="Turns">
      {#each newestFirst as c (c.seq)}
        {@const on = c.seq === turn}
        <li>
          <button type="button" class="{ROW} {on ? 'bg-sel text-ink' : isMarker(c) ? 'text-faint hover:bg-sel/50' : 'text-ink-2 hover:bg-sel/50'}" aria-current={on ? "true" : undefined} title={c.label} onclick={() => pick(c)}>
            <span class="num truncate">
              {#if isMarker(c)}<span aria-hidden="true">⟲</span>{" "}{markerWord(c)}{:else}#{c.seq}{#if description(c)}{" · "}{description(c)}{/if}{/if}
            </span>
            <span class="num text-faint">{agoPhrase(c.created as unknown as string).replace(/ ago$/, "")}</span>
          </button>
        </li>
      {/each}
    </ol>
  </div>
</div>
