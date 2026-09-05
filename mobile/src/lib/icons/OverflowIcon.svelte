<script lang="ts">
  import type { SVGAttributes } from "svelte/elements";
  let { ...rest }: SVGAttributes<SVGSVGElement> = $props();
</script>

<!-- btn/overflow — the three-dot session menu. -->
<svg
  width="4"
  height="17"
  viewBox="13.25 11.75 3.5 16.5"
  fill="none"
  class="block shrink-0"
  aria-hidden="true"
  {...rest}
>
  <circle cx="15" cy="13.5" r="1.75" fill="currentColor" />
  <circle cx="15" cy="20" r="1.75" fill="currentColor" />
  <circle cx="15" cy="26.5" r="1.75" fill="currentColor" />
</svg>
