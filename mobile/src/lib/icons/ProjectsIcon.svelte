<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import type { SVGAttributes } from "svelte/elements";
  // The folder. Selected: filled.
  let { filled = false, size = 25, ...rest }: { filled?: boolean; size?: number } & SVGAttributes<SVGSVGElement> = $props();
</script>

<span class="relative inline-flex shrink-0" data-filled={filled ? "" : undefined}><Icon
  {...rest}
  name="folder"
  {size}
  stroke-width={1.75}
  fill={filled ? "currentColor" : "none"}
/></span>
