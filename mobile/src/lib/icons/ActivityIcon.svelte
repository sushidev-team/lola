<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import type { SVGAttributes } from "svelte/elements";
  // The pulse line. It has no area to fill, so selected is a heavier stroke.
  let { filled = false, size = 25, ...rest }: { filled?: boolean; size?: number } & SVGAttributes<SVGSVGElement> = $props();
</script>

<span class="relative inline-flex shrink-0" data-filled={filled ? "" : undefined}><Icon
  {...rest}
  name="activity"
  {size}
  stroke-width={filled ? 2.5 : 1.75}
/></span>
