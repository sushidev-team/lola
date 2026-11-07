<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import type { SVGAttributes } from "svelte/elements";
  let { ...rest }: SVGAttributes<SVGSVGElement> = $props();
</script>

<span class="relative inline-flex shrink-0"><Icon {...rest} name="activity" size={22} /></span>
