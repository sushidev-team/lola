<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import type { SVGAttributes } from "svelte/elements";
  let { online = false, ...rest }: { online?: boolean } & SVGAttributes<SVGSVGElement> = $props();
</script>

<span class="relative inline-flex shrink-0"><Icon {...rest} name="monitor" size={22} />{#if online}<span class="absolute -top-0.5 -right-0.5 size-1.5 rounded-full bg-good ring-2 ring-canvas" aria-hidden="true"></span>{/if}</span>
