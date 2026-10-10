<script lang="ts">
  // The sidebar's Checkpoints tab (SUSHI-624): one snapshot of the worktree per
  // agent turn, newest on top — the one a bad turn most likely wants. A row
  // selects that checkpoint and opens what its turn changed in the main pane
  // (CheckpointsView), which is why this is a list and not a tab of its own: a
  // diff needs the pane's width, a list of turns does not, and the tab strip
  // stays for terminals.
  //
  // The selected row carries the two actions. Restore moves FILES only and saves
  // the current state as a new checkpoint first, so it is undoable; the daemon
  // refuses it mid-turn and `restorable` says so before anyone clicks.
  import type { CheckpointInfo } from "@bindings/internal/protocol";
  import { checkpoints, checkpointTime } from "$lib/checkpoints.svelte";
  import { terms, CHECKPOINTS } from "$lib/terms.svelte";
  import { nav } from "$lib/nav.svelte";
  import Button from "./Button.svelte";

  let { sessionId }: { sessionId: string } = $props();
  const st = $derived(checkpoints.of(sessionId));
  const newestFirst = $derived([...st.list].reverse());
  const showingDiff = $derived(terms.activeTab(sessionId) === CHECKPOINTS);

  function open(c: CheckpointInfo) {
    checkpoints.select(sessionId, c.seq);
    terms.select(sessionId, CHECKPOINTS);
  }

  // A restore's safety snapshot and the pre-launch baseline are lola's own
  // markers, not turns — drawn hollow so the turns read as the timeline.
  function isMarker(c: CheckpointInfo): boolean {
    return c.label === "start" || c.label.startsWith("before restore");
  }
</script>

<div class="flex flex-col gap-3 px-3 py-3.5" data-testid="checkpoint-list">
  <div class="flex items-baseline gap-2 text-sm text-faint">
    <span>{st.list.length} checkpoint{st.list.length === 1 ? "" : "s"}</span>
    <Button
      size="xs"
      class="ml-auto"
      loading={st.loading}
      title="re-read the checkpoints"
      onclick={() => checkpoints.load(sessionId)}>Refresh</Button
    >
  </div>

  {#if st.error}
    <div class="text-sm break-words text-bad">{st.error}</div>
  {:else if st.list.length === 0 && !st.loading}
    <div class="text-sm text-faint">No checkpoints yet — one is recorded each time the agent ends a turn.</div>
  {/if}

  <ol class="flex flex-col" aria-label="checkpoints">
    {#each newestFirst as c, i (c.seq)}
      {@const sel = c.seq === st.selected}
      {@const last = i === newestFirst.length - 1}
      <li class="relative">
        <!-- The same hairline rail as the report's plan, joining the turns. -->
        {#if !last}
          <span class="absolute top-[22px] bottom-0 left-[16.5px] w-px bg-edge/60" aria-hidden="true"></span>
        {/if}
        <!-- A card-shaped row (two lines, selectable) — a hand-rolled button like
             the other nav rows, not a <Button>. -->
        <button
          type="button"
          aria-pressed={sel}
          title="show what this turn changed"
          class="grid w-full grid-cols-[auto_1fr_auto] items-start gap-x-2.5 rounded-md px-2 py-1.5 text-left transition-colors {sel
            ? 'bg-sel'
            : 'hover:bg-sel/50'}"
          onclick={() => open(c)}
        >
          <span class="mt-[5px] flex h-[9px] w-[9px] items-center justify-center" aria-hidden="true">
            <span
              class="h-[9px] w-[9px] rounded-full {isMarker(c)
                ? 'border border-faint bg-canvas'
                : sel
                  ? 'bg-accent'
                  : 'bg-faint/70'}"
            ></span>
          </span>
          <span class="min-w-0">
            <span class="block truncate {sel ? 'font-medium text-ink' : isMarker(c) ? 'text-faint' : 'text-ink/85'}"
              >{c.label}</span
            >
            <span class="num block text-sm text-faint">#{c.seq} · <span class="font-mono">{c.sha.slice(0, 7)}</span></span>
          </span>
          <span class="num text-sm text-faint">{checkpointTime(c.created)}</span>
        </button>
        {#if sel}
          <div class="flex flex-wrap items-center gap-1 pt-1 pb-2 pl-7">
            {#if !showingDiff}
              <Button size="xs" title="show what this turn changed" onclick={() => open(c)}>Diff</Button>
            {/if}
            <Button
              size="xs"
              disabled={!st.restorable || st.busy}
              title={st.restorable
                ? "put the worktree's files back to this checkpoint (undoable)"
                : "the agent is mid-turn — wait for it to finish before restoring"}
              onclick={() => checkpoints.askRestore(sessionId, c)}>Restore…</Button
            >
            <Button
              size="xs"
              disabled={st.busy}
              title="start a new agent session from this checkpoint on its own branch"
              onclick={() => checkpoints.askFork(sessionId, c, (id) => nav.select(id))}>Fork…</Button
            >
          </div>
        {/if}
      </li>
    {/each}
  </ol>
</div>
