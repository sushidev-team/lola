<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  // A session's token usage: the count, a 4-bar size glyph ranking it against
  // the user's own finished sessions (lib/usage.ts), and a flame while it burns
  // tokens faster than past sessions ever did. The glyph and flame are SVG in
  // currentColor rather than "▅" / "🔥" text: the font decides how a block
  // character sits on the baseline, and an emoji ignores the theme entirely.
  import type { UsageInfo } from "@bindings/internal/protocol";
  import { usageLabel, usageLevel } from "$lib/usage";
  import HoverCard from "./HoverCard.svelte";
  import UsageCard from "./UsageCard.svelte";

  let { usage, class: cls = "" }: { usage: UsageInfo; class?: string } = $props();
  const level = $derived(usageLevel(usage));
</script>

<HoverCard class="num inline-flex items-center gap-1 whitespace-nowrap outline-none {cls}">
  {#snippet card()}<UsageCard {usage} />{/snippet}
  {usageLabel(usage)}
  <svg
    class="shrink-0 {level === 3 ? 'text-orange' : ''}"
    width="11"
    height="10"
    viewBox="0 0 11 10"
    aria-label="size {level + 1} of 4"
    role="img"
  >
    {#each [3, 5, 7, 10] as h, i (i)}
      <rect x={i * 3} y={10 - h} width="2" height={h} rx="0.5" fill="currentColor" opacity={i <= level ? 1 : 0.25} />
    {/each}
  </svg>
  {#if usage.burning}
    <Icon name="flame" class="shrink-0 text-orange" aria-label="burning fast" role="img" size={14} aria-hidden={false} />
  {/if}
</HoverCard>
