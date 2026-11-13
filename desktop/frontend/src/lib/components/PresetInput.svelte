<script lang="ts">
  import Select from "./Select.svelte";
  import PathInput from "./PathInput.svelte";
  let {
    label, value, options, onChange, disabled = false, placeholder = "", onFocus, pathKind,
  }: {
    label: string;
    value: string;
    options: { value: string; label: string }[];
    onChange: (value: string) => void;
    disabled?: boolean;
    placeholder?: string;
    onFocus?: () => void;
    pathKind?: "file" | "folder";
  } = $props();

  // Opening Custom is presentation only. Keep the value until the user edits
  // it, and never coerce an existing value that isn't in today's preset list.
  let custom = $state(false);
  const index = $derived(options.findIndex((option) => option.value === value));
  const showCustom = $derived(custom || index < 0);
</script>

<div class="grid min-w-0 gap-2">
  <Select aria-label={label} {disabled} value={showCustom ? "custom" : String(index)} onfocus={onFocus}
    onchange={(e) => {
      const selected = e.currentTarget.value;
      custom = selected === "custom";
      if (!custom) onChange(options[Number(selected)].value);
    }}>
    {#each options as option, i}
      <option value={String(i)}>{option.label}</option>
    {/each}
    <option value="custom">Custom…</option>
  </Select>
  {#if showCustom}
    {#if pathKind}
    <PathInput label={`Custom ${label}`} kind={pathKind} {disabled} {placeholder} {value}
      onChange={(next) => { custom = true; onChange(next); }} />
    {:else}
    <input aria-label={`Custom ${label}`} {disabled} {placeholder} {value}
      class="w-full rounded border border-edge bg-canvas px-2 py-1.5 font-mono text-ink outline-none focus:border-accent placeholder:text-placeholder disabled:opacity-40"
      oninput={(e) => { custom = true; onChange(e.currentTarget.value); }} />
    {/if}
  {/if}
</div>
