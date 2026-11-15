<script lang="ts" module>
  import type { SessionInfo } from "@bindings/internal/protocol";
  import { mergeQueueChip, overlapChip, type Chip } from "$lib/crosssession";
  import type { StageTone } from "$lib/stage";
  import type { PillTone } from "./MetaPill.svelte";

  const TONE: Record<StageTone, PillTone> = {
    bad: "bad",
    orange: "warn",
    warn: "warn",
    info: "grey",
    good: "good",
    done: "grey",
    neutral: "grey",
  };

  /** The pill tone for a chip tone. */
  export const pillTone = (t: StageTone): PillTone => TONE[t];

  /** The merge-queue chip and the overlap chip of a session, in that order; none when it has neither. */
  export function sessionChips(s: Pick<SessionInfo, "mergeQueue" | "overlaps"> | null | undefined): Chip[] {
    return [mergeQueueChip(s?.mergeQueue), overlapChip(s?.overlaps)].filter((c): c is Chip => c !== null);
  }
</script>

<script lang="ts">
  import MetaPill from "./MetaPill.svelte";

  // The cross-session chips of one session: its place in the merge queue and the
  // overlap warning, from the desktop's own helpers so both surfaces say the same
  // thing. Display only. Draws nothing when there are none. `detail` adds each
  // chip's explanation as a line under the pills, for a sheet that has the room.

  let {
    session,
    detail = false,
  }: {
    session: Pick<SessionInfo, "mergeQueue" | "overlaps"> | null | undefined;
    detail?: boolean;
  } = $props();

  const chips = $derived(sessionChips(session));
</script>

{#if chips.length > 0}
  <div class="flex min-w-0 flex-col gap-1" data-session-chips>
    <div class="flex min-w-0 flex-wrap items-center gap-1.5">
      {#each chips as c (c.label)}
        <MetaPill tone={pillTone(c.tone)} ground="grey">{c.label}</MetaPill>
      {/each}
    </div>
    {#if detail}
      {#each chips as c (c.label)}
        <p class="text-footnote whitespace-pre-line text-faint">{c.hint}</p>
      {/each}
    {/if}
  </div>
{/if}
