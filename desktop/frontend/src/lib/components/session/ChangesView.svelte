<script lang="ts">
  import Select from "../Select.svelte";
  // What the session did to the code: the left column names everything against the base and each
  // turn, and the right is the diff of whichever is selected. One place, so a turn is never a
  // separate pane to find your way back from.
  //
  // Restore and Fork sit on a selected turn, in DiffView's toolbar, and only there.
  import { checkpoints } from "$lib/checkpoints.svelte";
  import { terms, CHECKPOINTS, DIFF } from "$lib/terms.svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { untrack } from "svelte";
  import DiffView from "../DiffView.svelte";
  import TurnRail from "./TurnRail.svelte";
  import Button from "../Button.svelte";
  import { reviewWorkspace } from "$lib/reviewworkspace.svelte";

  let { sessionId, compact = false }: { sessionId: string; compact?: boolean } = $props();

  const tab = $derived(terms.activeTab(sessionId));
  const st = $derived(checkpoints.of(sessionId));
  let selectedTurn = $state<number | null>(null);
  $effect(() => { if (tab === CHECKPOINTS) selectedTurn = st.selected; else if (tab === DIFF) selectedTurn = 0; });
  const turn = $derived(compact && selectedTurn !== null ? selectedTurn : tab === CHECKPOINTS ? st.selected : 0);
  const current = $derived(st.list.find((c) => c.seq === turn));

  // A turn ending is what records a checkpoint, and it shows up as the agent axis moving. A
  // session in a main checkout has no checkpoints to ask about.
  const agentState = $derived(store.sessionById(sessionId)?.agentState ?? "");
  const checkpointed = $derived(!store.sessionById(sessionId)?.location);
  $effect(() => {
    const sid = sessionId;
    agentState;
    if (checkpointed) untrack(() => void checkpoints.load(sid));
  });
</script>

<div class="grid h-full min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] grid-rows-[auto_minmax(0,1fr)]" data-testid="changes-view">
  <div class="flex justify-end border-b border-edge px-3 py-1"><Button size="xs" onclick={() => reviewWorkspace.open(sessionId)}>Review PR</Button></div>
  <DiffView {sessionId} {compact} turn={current?.seq ?? 0}>
    {#snippet comparison()}
      {#if compact && checkpointed && st.list.length}
        <Select class="diff-comparison min-w-0" aria-label="Compare changes" value={turn} onchange={(event) => selectedTurn = Number(event.currentTarget.value)}>
          <option value={0}>All changes</option>
          {#each [...st.list].reverse() as checkpoint (checkpoint.seq)}<option value={checkpoint.seq}>#{checkpoint.seq} · {checkpoint.label}</option>{/each}
        </Select>
      {/if}
    {/snippet}
    {#snippet rail()}
      {#if checkpointed && !compact}<TurnRail {sessionId} turn={current?.seq ?? 0} />{/if}
    {/snippet}
    {#snippet turnBar()}
      {#if current}
        <span class="min-w-0 truncate text-faint" class:sr-only={compact} data-restore-note>Files only. The conversation and git history do not move.</span>
        <Button size="xs" disabled={!st.restorable || st.busy} title={st.restorable ? "Put the worktree's files back to this turn. Undoable." : "The agent is mid-turn. Wait for it to finish before restoring."} onclick={() => checkpoints.askRestore(sessionId, current)}>Restore…</Button>
        <Button size="xs" disabled={st.busy} title="Start a new agent session from this turn, on its own branch" onclick={() => checkpoints.askFork(sessionId, current, (id) => nav.openSession(id))}>Fork…</Button>
      {/if}
    {/snippet}
  </DiffView>
</div>
