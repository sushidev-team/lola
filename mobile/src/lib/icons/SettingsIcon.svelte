<script lang="ts">
  import type { SVGAttributes } from "svelte/elements";
  let { ...rest }: SVGAttributes<SVGSVGElement> = $props();
</script>

<!-- tab/Settings — three sliders. The handles are knocked OUT of their rails, so
     their fill is the bar's own ground (the tab bar is `crust`) rather than
     currentColor; a handle in the ink colour would be a solid dot. -->
<svg
  width="20"
  height="22"
  viewBox="0 0 20 22"
  fill="none"
  class="block shrink-0"
  aria-hidden="true"
  {...rest}
>
  <rect y="3.1" width="20" height="1.8" rx="0.9" fill="currentColor" />
  <circle cx="13" cy="4" r="2" fill="var(--color-crust)" stroke="currentColor" stroke-width="1.6" />
  <rect y="9.6" width="20" height="1.8" rx="0.9" fill="currentColor" />
  <circle
    cx="6"
    cy="10.5"
    r="2"
    fill="var(--color-crust)"
    stroke="currentColor"
    stroke-width="1.6"
  />
  <rect y="16.1" width="20" height="1.8" rx="0.9" fill="currentColor" />
  <circle cx="15" cy="17" r="2" fill="var(--color-crust)" stroke="currentColor" stroke-width="1.6" />
</svg>
