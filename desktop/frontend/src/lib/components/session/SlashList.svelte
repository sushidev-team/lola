<script lang="ts">
  // The commands a "/" at column 0 could mean. A row fills the field with the exact name, because
  // a prefix is never sent. A screen says so, since ↩ on one opens the terminal and types nothing.
  import type { SlashRow } from "$lib/slash";

  let { rows, selected, onpick }: { rows: SlashRow[]; selected: number; onpick: (name: string) => void } = $props();
</script>

<div class="grid max-h-[168px] overflow-y-auto px-1 pb-1" role="listbox" aria-label="Commands" data-slash-list>
  {#each rows as r, i (r.name)}
    <button
      type="button"
      role="option"
      aria-selected={i === selected}
      class="grid h-6 cursor-pointer grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded-md px-2 text-left text-sm {i === selected ? 'bg-sel text-ink' : 'text-ink-2 enabled:hover:bg-sel/50'}"
      onmousedown={(e) => e.preventDefault()}
      onclick={() => onpick(r.name)}
    >
      <span class="truncate font-mono">{r.name}</span>
      {#if r.screen}<span class="text-faint">Opens in terminal</span>{/if}
    </button>
  {/each}
</div>
