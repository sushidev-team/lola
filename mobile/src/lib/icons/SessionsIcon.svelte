<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import type { SVGAttributes } from "svelte/elements";
  // The tray. Selected: the same silhouette, filled, with the tray's lip cut out
  // (even-odd) so it still reads as a tray and not a slab.
  let { filled = false, size = 26, ...rest }: { filled?: boolean; size?: number } & SVGAttributes<SVGSVGElement> = $props();
</script>

<span class="relative inline-flex shrink-0" data-filled={filled ? "" : undefined}>
  {#if filled}
    <svg
      {...rest}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="currentColor"
      stroke="currentColor"
      stroke-width="1.75"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <path
        fill-rule="evenodd"
        d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z M1 11.25H8.43L10.4 14.25H13.6L15.57 11.25H23V12.75H16.4L14.4 15.75H9.6L7.6 12.75H1z"
      />
    </svg>
  {:else}
    <Icon {...rest} name="inbox" {size} stroke-width={1.75} fill="none" />
  {/if}
</span>
