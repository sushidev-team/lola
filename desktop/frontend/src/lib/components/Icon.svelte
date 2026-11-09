<script lang="ts">
  import type { SVGAttributes } from 'svelte/elements';
  import { icons, type IconName } from '$lib/icons';
  let { name, size = 16, class: cls = '', color, ...rest }: { name: IconName; size?: number | string } & SVGAttributes<SVGSVGElement> = $props();
  const Symbol = $derived(icons[name]);
</script>

<Symbol {size} color={color ?? undefined} strokeWidth={2} class="lola-icon inline-block shrink-0 align-middle {cls}" aria-hidden={rest['aria-label'] ? undefined : true} {...rest}>
  {#if name === 'loader-circle'}
    <!-- Keep the ring's center visually anchored while its open arc rotates. -->
    <circle cx="12" cy="12" r="9" opacity="0.2" />
  {/if}
</Symbol>

<style>
  /* Use the square viewport, never the painted bounds of an incomplete arc. */
  :global(svg.lola-icon.lucide-loader-circle) {
    transform-box: view-box;
    transform-origin: 50% 50%;
  }
</style>
