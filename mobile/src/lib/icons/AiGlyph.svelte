<script lang="ts">
  import type { SVGAttributes } from "svelte/elements";
  let { ...rest }: SVGAttributes<SVGSVGElement> = $props();
</script>

<!-- icon/ai — the sparkle that leads an agent's activity sentence. -->
<svg
  width="14"
  height="16"
  viewBox="0 0 14 16"
  fill="none"
  class="block shrink-0"
  aria-hidden="true"
  {...rest}
>
  <path
    d="M5.5 3.14939C5.5 6.17439 7.975 8.64939 11 8.64939C7.975 8.64939 5.5 11.1244 5.5 14.1494C5.5 11.1244 3.025 8.64939 0 8.64939C3.025 8.64939 5.5 0.12439 5.5 3.14939Z"
    fill="currentColor"
  />
  <path
    opacity="0.75"
    d="M11.25 0.324695C11.25 1.8372 12.4875 3.0747 14 3.0747C12.4875 3.0747 11.25 4.3122 11.25 5.8247C11.25 4.3122 10.0125 3.0747 8.5 3.0747C10.0125 3.0747 11.25 -1.1878 11.25 0.324695Z"
    fill="currentColor"
  />
</svg>
