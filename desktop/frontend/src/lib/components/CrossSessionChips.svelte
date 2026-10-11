<script lang="ts">
  import type { SessionInfo } from "@bindings/internal/protocol";
  import { mergeQueueChip, overlapChip } from "$lib/crosssession";
  import { STAGE_CHIP, STAGE_DOT } from "$lib/stage";
  import HoverCard from "./HoverCard.svelte";

  // The two cross-session facts beside a row's lifecycle chip: its place in
  // lola's merge queue, and a warning that another session of the project edits
  // the same files. Static, display-only — the details live in the tooltip.
  let { session }: { session: SessionInfo } = $props();

  const chips = $derived([mergeQueueChip(session.mergeQueue), overlapChip(session.overlaps)].filter((c) => c !== null));
</script>

{#each chips as c (c.label)}
  <HoverCard
    class="inline-flex shrink-0 items-center text-sm whitespace-nowrap {c.overlaps
      ? 'size-5 justify-center rounded text-warn hover:bg-warn/10'
      : `gap-1.5 rounded-full px-2 py-[1px] ${STAGE_CHIP[c.tone]}`}"
  >
    {#snippet card()}
      {#if c.overlaps}
        <dl class="grid gap-2.5 whitespace-normal">
          {#each c.overlaps as overlap, i}
            <div class={i > 0 ? "border-t border-edge pt-2.5" : ""}>
              <dt class="flex items-baseline justify-between gap-4">
                <span class="font-medium text-ink [overflow-wrap:anywhere]">{overlap.session}</span>
                {#if overlap.more > 0}
                  <span class="shrink-0 whitespace-nowrap text-faint">+{overlap.more} {overlap.more === 1 ? "file" : "files"}</span>
                {/if}
              </dt>
              <dd class="mt-1 text-[11px] leading-[14px]">
                <ul class="grid">
                  {#each overlap.files as file}
                    {@const slash = file.lastIndexOf("/")}
                    <li class="[overflow-wrap:anywhere]"><span class="text-faint">{file.slice(0, slash + 1)}</span><span class="text-ink">{file.slice(slash + 1)}</span></li>
                  {/each}
                </ul>
              </dd>
            </div>
          {/each}
        </dl>
      {:else}
        <div class="whitespace-pre-line break-words">{c.hint}</div>
      {/if}
    {/snippet}
    {#if c.overlaps}
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M12 8a5 5 0 0 1 0 8 5 5 0 0 1 0-8Z" fill="currentColor" fill-opacity="0.25" stroke="none" />
        <circle cx="9" cy="12" r="5" />
        <circle cx="15" cy="12" r="5" />
      </svg>
      <span class="sr-only">{c.label}</span>
    {:else}
      <span class="h-1.5 w-1.5 shrink-0 rounded-full {STAGE_DOT[c.tone]}" aria-hidden="true"></span>
      {c.label}
    {/if}
  </HoverCard>
{/each}
