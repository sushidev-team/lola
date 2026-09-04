<script lang="ts">
  import type { SVGAttributes } from "svelte/elements";
  let { active = false, ...rest }: { active?: boolean } & SVGAttributes<SVGSVGElement> = $props();
</script>

<!-- icon/filter — three descending bars. The dot in the corner is the ACTIVE
     mark: it is part of the exported glyph rather than a badge bolted beside it,
     which is why it takes a prop rather than being drawn by the call site. Its
     ring is the ground showing through, so it stays a token. -->
<svg
  width="22"
  height="22"
  viewBox="0 0 22 22"
  fill="none"
  class="block shrink-0"
  aria-hidden="true"
  {...rest}
>
  <rect x="1" y="7" width="18" height="1.8" rx="0.9" fill="currentColor" />
  <rect x="4" y="11.5" width="12" height="1.8" rx="0.9" fill="currentColor" />
  <rect x="7" y="16" width="6" height="1.8" rx="0.9" fill="currentColor" />
  {#if active}
    <circle
      cx="18"
      cy="4"
      r="3.25"
      fill="var(--color-accent)"
      stroke="var(--color-canvas)"
      stroke-width="1.5"
    />
  {/if}
</svg>
