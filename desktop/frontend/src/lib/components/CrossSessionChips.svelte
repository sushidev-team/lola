<script lang="ts">
  import type { SessionInfo } from "@bindings/internal/protocol";
  import { mergeQueueChip, overlapChip } from "$lib/crosssession";
  import { STAGE_CHIP, STAGE_DOT } from "$lib/stage";

  // The two cross-session facts beside a row's lifecycle chip: its place in
  // lola's merge queue, and a warning that another session of the project edits
  // the same files. Static, display-only — the details live in the tooltip.
  let { session }: { session: SessionInfo } = $props();

  const chips = $derived([mergeQueueChip(session.mergeQueue), overlapChip(session.overlaps)].filter((c) => c !== null));
</script>

{#each chips as c (c.label)}
  <span
    class="inline-flex items-center gap-1.5 rounded-full px-2 py-[1px] text-sm whitespace-nowrap {STAGE_CHIP[c.tone]}"
    title={c.hint}
  >
    <span class="h-1.5 w-1.5 shrink-0 rounded-full {STAGE_DOT[c.tone]}" aria-hidden="true"></span>
    {c.label}
  </span>
{/each}
