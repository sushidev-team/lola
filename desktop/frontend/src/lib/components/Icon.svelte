<script lang="ts">
  import type { SVGAttributes } from 'svelte/elements';
  import { icons, type IconName } from '$lib/icons';
  let { name, size = 16, class: cls = '', color, ...rest }: { name: IconName; size?: number | string } & SVGAttributes<SVGSVGElement> = $props();
  const Symbol = $derived(icons[name]);
</script>

<Symbol {size} color={color ?? undefined} strokeWidth={2} class="lola-icon inline-block shrink-0 align-middle {cls}" aria-hidden={rest['aria-label'] ? undefined : true} {...rest} />
