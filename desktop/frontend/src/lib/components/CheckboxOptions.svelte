<script lang="ts">
  import Checkbox from "./Checkbox.svelte";

  let { label, options, selected, onChange }: {
    label: string;
    options: { id: string; label: string }[];
    selected: string[] | null;
    onChange: (selected: string[]) => void;
  } = $props();
  let query = $state("");
  const searchable = $derived(options.length > 8);
  const visible = $derived(options.filter((option) => !searchable || option.label.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())));
</script>

<div class="min-w-0 space-y-1.5">
  {#if searchable}
    <input type="search" aria-label={`Search ${label}`} placeholder="Search…" spellcheck="false" bind:value={query}
      class="w-full min-w-0 rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder" />
  {/if}
  <div role="group" aria-label={label} class="max-h-36 space-y-1 overflow-auto rounded border border-edge p-2">
    {#each visible as option (option.id)}
      <label class="flex cursor-pointer items-start gap-2 text-ink">
        <Checkbox checked={(selected ?? []).includes(option.id)} onchange={() => {
          const current = selected ?? [];
          onChange(current.includes(option.id) ? current.filter((id) => id !== option.id) : [...current, option.id]);
        }} />
        <span class="min-w-0 break-words">{option.label}</span>
      </label>
    {/each}
    {#if visible.length === 0}<span class="text-sm text-faint">{options.length ? "No matches. Try another search." : "No options available."}</span>{/if}
  </div>
  {#if searchable}<span class="block text-sm text-faint" role="status">{(selected ?? []).length} selected · {visible.length} of {options.length} shown</span>{/if}
</div>
