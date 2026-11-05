<script lang="ts">
  // Tier is unmissable and never shouts. Outlined, never filled: a permanently red
  // tag becomes wallpaper in a day, and it would compete with the one solid thing
  // that matters on a card, its action. Unset and unknown read PRODUCTION.
  import { tierTagClass, tierWord } from "$lib/ssh";

  let { tier }: { tier: string | undefined } = $props();
</script>

<span class="inline-flex h-4 shrink-0 items-center rounded-sm border px-1 text-[10px] leading-none font-medium tracking-wide {tierTagClass(tier)}">{tierWord(tier)}</span>
