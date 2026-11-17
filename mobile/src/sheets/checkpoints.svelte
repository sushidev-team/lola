<script lang="ts">
  import { untrack } from "svelte";
  import { DaemonService } from "@bindings/desktop";
  import type { DiffData } from "@bindings/internal/protocol";
  import { checkpoints } from "$lib/checkpoints.svelte";
  import EmptyState from "@mobile/lib/components/EmptyState.svelte";
  import ListGroup from "@mobile/lib/components/ListGroup.svelte";
  import ListRow from "@mobile/lib/components/ListRow.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import VirtualList from "@mobile/lib/components/VirtualList.svelte";
  import { copy, fill } from "@mobile/lib/copy";
  import { router } from "@mobile/lib/router.svelte";
  import { can } from "@mobile/lib/tiers.svelte";

  // A session's turn checkpoints (spec §5.3): pick one, see which files it
  // changed, then Restore (files only) or Fork. Both ask through the shared
  // confirm, whose copy says what does and does not move. A failed load shows
  // the daemon's sentence, never an empty list.

  const id = $derived(router.top.name === "session" ? router.top.id : "");
  const st = $derived(checkpoints.of(id));
  const current = $derived(st.list.find((c) => c.seq === st.selected));
  const newestFirst = $derived([...st.list].reverse());
  const needsUpdate = $derived(st.error === copy.refusals.update);

  let preview = $state<{ seq: number; data: DiffData } | null>(null);
  let previewError = $state("");

  // load reads and writes the store's entry; tracking it would re-run this forever.
  $effect(() => {
    const sid = id;
    if (sid) untrack(() => void checkpoints.load(sid));
  });

  $effect(() => {
    const sid = id;
    const seq = st.selected;
    if (!sid || seq <= 0) return;
    let live = true;
    previewError = "";
    DaemonService.CheckpointDiff(sid, seq)
      .then((data) => live && (preview = { seq, data }))
      .catch((e) => live && (previewError = String(e instanceof Error ? e.message : e)));
    return () => {
      live = false;
    };
  });

  const files = $derived(preview && preview.seq === st.selected ? (preview.data.files ?? []) : []);

  function fork(): void {
    if (!current) return;
    checkpoints.askFork(id, current, (sid) => {
      router.close();
      router.push({ name: "session", id: sid, seg: "conversation" });
    });
  }
</script>

{#snippet footer()}
  <div class="flex min-w-0 flex-col items-stretch gap-1">
    {#if can("restoreCheckpoint")}
      <TouchButton
        variant="primary"
        prominent
        disabled={!current || !st.restorable || st.busy}
        onclick={() => current && checkpoints.askRestore(id, current)}
      >
        {copy.checkpoints.restore}
      </TouchButton>
    {/if}
    {#if can("forkCheckpoint")}
      <button
        type="button"
        class="min-h-11 w-full touch-manipulation text-callout text-accent active:opacity-60 disabled:opacity-40"
        disabled={!current || st.busy}
        onclick={fork}
      >
        {copy.checkpoints.fork}
      </button>
    {/if}
  </div>
{/snippet}

<Sheet label="Checkpoints" title="Checkpoints" detent="fit" {footer} onclose={() => router.close()}>
  <div class="flex min-h-0 min-w-0 flex-col gap-3 pb-2">
    {#if needsUpdate}
      <EmptyState icon="refresh-cw" title="Update needed" body={copy.refusals.update} />
    {:else if st.error}
      <p class="px-4 text-subhead text-subtext" role="alert">{st.error}</p>
    {/if}
    {#if previewError}<p class="px-4 text-subhead text-subtext" role="alert">{previewError}</p>{/if}
    <ListGroup>
      <VirtualList items={newestFirst} key={(c) => String(c.seq)} label="Checkpoints" class="max-h-[40vh]">
        {#snippet row(c)}
          <ListRow
            label={`#${c.seq} · ${c.label}`}
            checked={c.seq === st.selected}
            aria-label={`#${c.seq} · ${c.label}`}
            onclick={() => checkpoints.select(id, c.seq)}
          />
        {/snippet}
      </VirtualList>
    </ListGroup>
    {#if files.length}
      <ListGroup header={files.length === 1 ? copy.checkpoints.oneFile : fill(copy.checkpoints.files, { n: files.length })}>
        <div role="list" aria-label="Changed files">
          {#each files as f (f.path)}
            <ListRow label={f.path.slice(f.path.lastIndexOf("/") + 1)} detail={f.path} detailMono value={`+${f.additions} −${f.deletions}`} />
          {/each}
        </div>
      </ListGroup>
    {/if}
  </div>
</Sheet>
