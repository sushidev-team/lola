<script lang="ts">
  import Select from "./Select.svelte";
  import PresetInput from "./PresetInput.svelte";
  import { modelsFor } from "$lib/settingPresets";

  let {
    provider, model, onProviderChange, onModelChange,
    defaultProvider, providerLabel = "Provider", disabled = false,
    rowClass = "grid min-w-0 gap-1.5", labelClass = "text-faint",
  }: {
    provider: string;
    model?: string;
    onProviderChange?: (provider: string) => void;
    onModelChange?: (model: string) => void;
    defaultProvider?: string;
    providerLabel?: string;
    disabled?: boolean;
    rowClass?: string;
    labelClass?: string;
  } = $props();

  const providers = ["claude", "codex", "opencode"];
  const modelDrafts = new Map<string, string>();
  const effectiveProvider = $derived(provider || defaultProvider || "claude");

  function chooseProvider(next: string) {
    if (next === provider) return;
    if (model !== undefined) modelDrafts.set(provider, model);
    onProviderChange?.(next);
    // Models are provider-specific. Never send a Claude alias to Codex, but
    // keep each draft when the user switches back to compare providers.
    if (onModelChange) onModelChange(modelDrafts.get(next) ?? "");
  }
</script>

{#if onProviderChange}
  <div class={rowClass}>
    <span class={labelClass}>{providerLabel}</span>
    <Select aria-label={providerLabel} value={provider} {disabled} onchange={(e) => chooseProvider(e.currentTarget.value)}>
      {#if defaultProvider !== undefined}<option value="">Use default ({defaultProvider || "claude"})</option>{/if}
      {#each providers as value}<option {value}>{value}</option>{/each}
    </Select>
  </div>
{/if}
{#if model !== undefined && onModelChange}
  <div class={rowClass}>
    <span class={labelClass}>Model</span>
    {#key effectiveProvider}
      <PresetInput label="Model" value={model} options={modelsFor(effectiveProvider)} {disabled}
        onChange={onModelChange} placeholder={effectiveProvider === "opencode" ? "provider/model" : "Model ID or alias"} />
    {/key}
  </div>
{/if}
