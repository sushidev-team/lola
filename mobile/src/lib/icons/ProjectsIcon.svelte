<script lang="ts">
  import type { SVGAttributes } from "svelte/elements";
  let { ...rest }: SVGAttributes<SVGSVGElement> = $props();
</script>

<!-- tab/Projects — a pane divider. Transcribed from node 49:23, same reason as
     the Sessions glyph. -->
<svg
  width="20"
  height="22"
  viewBox="0 0 20 22"
  fill="none"
  class="block shrink-0"
  aria-hidden="true"
  {...rest}
>
  <rect
    x="0.8"
    y="3.8"
    width="18.4"
    height="14.4"
    rx="3"
    stroke="currentColor"
    stroke-width="1.6"
  />
  <rect x="0" y="8" width="20" height="1.6" rx="0.8" fill="currentColor" />
  <rect x="7" y="10" width="1.6" height="9" rx="0.8" fill="currentColor" />
</svg>
