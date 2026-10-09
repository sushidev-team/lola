<script lang="ts">
  import { stageFor, STAGE_CHIP, STAGE_DOT } from "$lib/stage";
  import Button from "./Button.svelte";

  // The list's lifecycle chip (see $lib/stage). On a conflicting branch it is
  // also the ACTION the status pill used to carry: hovering or focusing it
  // swaps "Merge conflict" for "Resolve", and a click asks the session's coding
  // agent to merge the default branch (cmd=resolveConflict). Static otherwise —
  // the only motion on a row is the agent glyph's.
  let {
    delivery = "",
    reacting = "",
    onResolve = undefined,
    resolveBranch = "",
  }: {
    delivery?: string;
    reacting?: string;
    onResolve?: (() => void) | undefined;
    resolveBranch?: string;
  } = $props();

  const st = $derived(stageFor(delivery, reacting));
  const actionable = $derived(delivery === "merge_conflict" && !!onResolve);
  const tip = $derived(
    `Resolve the conflicts: merges ${resolveBranch || "the default branch"} (the project's default branch) into ` +
      `this branch — the session's coding agent does the work and pushes the merge.`,
  );

  let busy = $state(false);
  // stopPropagation: the chip sits in a row whose own click selects the session.
  async function resolve(e: MouseEvent) {
    e.stopPropagation();
    if (!onResolve || busy) return;
    busy = true;
    try {
      await onResolve();
    } finally {
      busy = false;
    }
  }
</script>

{#if st}
  {#if actionable}
    <!-- Both labels share one grid cell so the chip keeps the wider width and
         the row does not reflow when the word swaps under the cursor. -->
    <Button
      variant="bare"
      size="xs"
      loading={busy}
      title={tip}
      class="group h-auto! gap-1.5! rounded-full! px-2! py-[1px]! text-sm! {STAGE_CHIP[st.tone]}"
      onclick={resolve}
    >
      <span class="h-1.5 w-1.5 shrink-0 rounded-full {STAGE_DOT[st.tone]}" aria-hidden="true"></span>
      <span class="grid">
        <span class="col-start-1 row-start-1 group-hover:invisible group-focus-visible:invisible">{st.label}</span>
        <span class="invisible col-start-1 row-start-1 group-hover:visible group-focus-visible:visible">Resolve</span>
      </span>
    </Button>
  {:else}
    <span
      class="inline-flex items-center gap-1.5 rounded-full px-2 py-[1px] text-sm whitespace-nowrap {STAGE_CHIP[st.tone]}"
      title={st.hint}
    >
      <span class="h-1.5 w-1.5 shrink-0 rounded-full {STAGE_DOT[st.tone]}" aria-hidden="true"></span>
      {st.label}
    </span>
  {/if}
{/if}
