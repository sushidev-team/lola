<script lang="ts">
  import type { SVGAttributes } from "svelte/elements";
  let { ...rest }: SVGAttributes<SVGSVGElement> = $props();
</script>

<!-- btn/back — a chevron. -->
<svg
  width="14"
  height="14"
  viewBox="7 6.5 14 14"
  fill="none"
  class="block shrink-0"
  aria-hidden="true"
  {...rest}
>
  <path
    d="M20.5 13.5L13.5 20L20.5 26.5"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    transform="translate(0 -6.5)"
  />
</svg>
