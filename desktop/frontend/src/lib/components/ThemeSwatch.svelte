<script lang="ts">
  import Icon from './Icon.svelte';
  import { FLAVORS, type ThemeId } from '../catppuccin';

  let { id, selected, compact = false, onclick }: {
    id: ThemeId;
    selected: boolean;
    compact?: boolean;
    onclick: () => void;
  } = $props();
  const f = $derived(FLAVORS[id]);
</script>

<!-- Each preview uses its own palette, independently of the active theme. -->
<button
  type="button"
  aria-label={compact ? f.label : undefined}
  aria-pressed={selected}
  class="grid min-w-0 rounded-lg border text-left {compact ? 'gap-1.5 p-2' : 'gap-2 p-2.5'} {selected ? 'ring-2 ring-accent' : ''}"
  style="background:{f.base};border-color:{selected ? f.sky : f.surface1}"
  {onclick}
>
  <span class="flex min-w-0 items-center gap-1.5">
    <span class="min-w-0 font-medium {compact ? 'text-xs' : ''}" style="color:{f.text}">{f.label}</span>
    {#if compact || selected}
      <span class="ml-auto shrink-0" style="color:{f.sky};visibility:{selected ? 'visible' : 'hidden'}" aria-hidden="true">
        <Icon name="check" size={12} />
      </span>
    {:else}
      <span class="ml-auto shrink-0 text-[10px] leading-none font-medium tracking-[0.03em] uppercase" style="color:{f.overlay1}">
        {f.dark ? 'dark' : 'light'}
      </span>
    {/if}
  </span>
  <span class="grid auto-cols-max grid-flow-col {compact ? 'gap-0.5' : 'gap-1'}" aria-hidden="true">
    {#each [f.surface0, f.surface2, f.subtext0, f.sky, f.green, f.yellow, f.peach, f.red, f.mauve] as c, i (i)}
      <span class="rounded-sm {compact ? 'size-2' : 'size-3'}" style="background:{c}"></span>
    {/each}
  </span>
</button>
