<script lang="ts">
  import type { SVGAttributes } from "svelte/elements";
  let { online = false, ...rest }: { online?: boolean } & SVGAttributes<SVGSVGElement> = $props();
</script>

<!-- icon/mac — the connection button. The corner dot is the LINK state: green
     when the daemon is answering, and absent rather than red when it is not,
     because the banner under the header already says so in words. -->
<svg
  width="23"
  height="20"
  viewBox="0 0 23 20"
  fill="none"
  class="block shrink-0"
  aria-hidden="true"
  {...rest}
>
  <rect
    x="1.8"
    y="2.8"
    width="18.4"
    height="11.4"
    rx="1.2"
    stroke="currentColor"
    stroke-width="1.6"
  />
  <rect x="4" y="17" width="14" height="1.8" rx="0.9" fill="currentColor" />
  {#if online}
    <circle
      cx="20"
      cy="3"
      r="2.25"
      fill="var(--color-good)"
      stroke="var(--color-canvas)"
      stroke-width="1.5"
    />
  {/if}
</svg>
