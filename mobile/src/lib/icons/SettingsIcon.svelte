<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import type { SVGAttributes } from "svelte/elements";
  // The gear. Selected: the same outline filled, with the centre hole kept (even-odd).
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
        d="M9.671 4.136a2.34 2.34 0 0 1 4.659 0 2.34 2.34 0 0 0 3.319 1.915 2.34 2.34 0 0 1 2.33 4.033 2.34 2.34 0 0 0 0 3.831 2.34 2.34 0 0 1-2.33 4.033 2.34 2.34 0 0 0-3.319 1.915 2.34 2.34 0 0 1-4.659 0 2.34 2.34 0 0 0-3.32-1.915 2.34 2.34 0 0 1-2.33-4.033 2.34 2.34 0 0 0 0-3.831A2.34 2.34 0 0 1 6.35 6.051a2.34 2.34 0 0 0 3.319-1.915z M15 12a3 3 0 1 0-6 0 3 3 0 0 0 6 0z"
      />
    </svg>
  {:else}
    <Icon {...rest} name="settings" {size} stroke-width={1.75} fill="none" />
  {/if}
</span>
