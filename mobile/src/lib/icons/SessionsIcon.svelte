<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import type { SVGAttributes } from "svelte/elements";

  let { filled = false, size = 26, ...rest }: { filled?: boolean; size?: number } & SVGAttributes<SVGSVGElement> = $props();
</script>

<span class="relative inline-flex shrink-0" data-filled={filled ? "" : undefined}>
  {#if filled}
    <svg {...rest} width={size} height={size} viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <rect x="2" y="2" width="20" height="20" rx="3.5" fill="currentColor" />
      <path d="m6.75 8 4 4-4 4M13 16h4" stroke="var(--color-canvas)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
    </svg>
  {:else}
    <Icon {...rest} name="square-terminal" {size} stroke-width={1.75} />
  {/if}
</span>
