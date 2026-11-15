<script lang="ts">
  import Checkbox from "$lib/components/Checkbox.svelte";
  import { TIER_ROWS, withTier } from "$lib/phones.svelte";

  let { value, onchange, disabled = false }: {
    value: readonly string[];
    onchange: (next: string[]) => void;
    disabled?: boolean;
  } = $props();
</script>

<div class="flex flex-wrap gap-x-4 gap-y-1.5" role="group" aria-label="Access">
  {#each TIER_ROWS as t (t.id)}
    <label class="flex cursor-pointer items-center gap-2">
      <Checkbox
        checked={value.includes(t.id)}
        disabled={disabled || t.locked}
        onchange={(e) => onchange(withTier(value, t.id, e.currentTarget.checked))} />
      <span>{t.label}</span>
    </label>
  {/each}
</div>
