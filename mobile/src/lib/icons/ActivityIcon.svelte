<script lang="ts">
  import type { SVGAttributes } from "svelte/elements";
  let { ...rest }: SVGAttributes<SVGSVGElement> = $props();
</script>

<!-- tab/Activity — an ECG trace. -->
<svg
  width="20"
  height="22"
  viewBox="0 0 21.8 22"
  fill="none"
  class="block shrink-0"
  aria-hidden="true"
  {...rest}
>
  <path
    d="M0.9 13H5.9L8.9 5L12.9 18L15.9 11H20.9"
    stroke="currentColor"
    stroke-width="1.8"
    stroke-linecap="round"
    stroke-linejoin="round"
  />
</svg>
