<script lang="ts">
  import type { SVGAttributes } from "svelte/elements";
  let { ...rest }: SVGAttributes<SVGSVGElement> = $props();
</script>

<!-- icon/branch — the mark on a PR badge. -->
<svg
  width="11"
  height="12"
  viewBox="0 0 11 12"
  fill="none"
  class="block shrink-0"
  aria-hidden="true"
  {...rest}
>
  <path
    d="M2.5 2.5V9.5M2.5 6L8.5 3.5"
    stroke="currentColor"
    stroke-width="1.5"
    stroke-linecap="round"
    stroke-linejoin="round"
  />
  <circle cx="2.5" cy="2.5" r="1.6" fill="currentColor" />
  <circle cx="2.5" cy="9.5" r="1.6" fill="currentColor" />
  <circle cx="8.5" cy="3.5" r="1.6" fill="currentColor" />
</svg>
