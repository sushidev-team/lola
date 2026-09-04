<script lang="ts">
  import type { SVGAttributes } from "svelte/elements";
  let { ...rest }: SVGAttributes<SVGSVGElement> = $props();
</script>

<!-- tab/Sessions — a list. Drawn as rectangles in the Figma file rather than
     exported as a vector, so the geometry is transcribed from node 49:14. -->
<svg
  width="20"
  height="22"
  viewBox="0 0 20 22"
  fill="none"
  class="block shrink-0"
  aria-hidden="true"
  {...rest}
>
  <rect x="0" y="3" width="4" height="2.4" rx="1.2" fill="currentColor" />
  <rect x="6" y="3" width="14" height="2.4" rx="1.2" fill="currentColor" />
  <rect x="0" y="9.5" width="4" height="2.4" rx="1.2" fill="currentColor" />
  <rect x="6" y="9.5" width="11" height="2.4" rx="1.2" fill="currentColor" />
  <rect x="0" y="16" width="4" height="2.4" rx="1.2" fill="currentColor" />
  <rect x="6" y="16" width="14" height="2.4" rx="1.2" fill="currentColor" />
</svg>
