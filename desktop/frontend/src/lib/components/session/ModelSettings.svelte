<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { talk } from "$lib/talk.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { modelsFor } from "$lib/settingPresets";
  import Select from "../Select.svelte";
  import Button from "../Button.svelte";
  import Icon from "../Icon.svelte";

  // `layout="list"` is the phone's sheet: every choice a 44pt row with a check on
  // the active model, no inner select and no label. Drawn with desktop classes so
  // this file never imports from mobile/.
  // `empty` reports (list layout only) that there is nothing to draw, so the phone's sheet can say so.
  // `locked` (list layout): the phone may not send settings; every row is disabled and one footnote says where to change them.
  let { sessionId, layout = "select", empty = $bindable(false), locked = false }: { sessionId: string; layout?: "select" | "list"; empty?: boolean; locked?: boolean } = $props();
  const session = $derived(store.sessionById(sessionId));
  const agent = $derived(session?.agent || "claude");
  const data = $derived(talk.state(sessionId));
  const supported = $derived(!!data?.caps.settings);
  const pending = $derived(!!talk.settingPending[sessionId]);
  const disabled = $derived(locked || !store.alive || !supported || data?.composer !== "resting" || pending || !!talk.error(sessionId));
  const visible = $derived(session && !session.goalId && !["dead", "exited", "shell", "orphaned"].includes(session.agentState ?? ""));
  $effect(() => { empty = layout === "list" && !visible; });
  let custom = $state(false);
  let customModel = $state("");
  let choice = $state("");

  function openControls() {
    talk.settingTerminal[sessionId] = true;
    ssh.view(sessionId).dockFocus++;
  }

  // These are actions, not a claim about the current model: a CLI can reject a
  // choice or ask for confirmation, and the launch model can be out of date.
  async function choose(setting: "model" | "effort" | "speed" | "variant", value: string) {
    if (!value || disabled) return;
    const id = sessionId;
    const kind = agent;
    await talk.setting({ session: id, agent: kind, setting, value });
    if (id === sessionId) {
      choice = "";
      if (!talk.settingErrors[id]) custom = false;
      if (kind === "opencode" && agent === kind && !talk.settingErrors[id]) openControls();
    }
  }
  const hint = $derived(pending ? "Sending setting…" : !supported ? "Open model settings in the agent’s terminal" : data?.composer !== "resting" ? "Available when the agent is idle with an empty prompt" : agent === "claude" ? "Changes also update Claude’s defaults, except maximum effort" : "Change model and settings for this session");
  async function select(value: string) {
    custom = value === "custom";
    if (value === "terminal") openControls();
    else if (!custom) {
      const [setting, model] = value.split("=", 2);
      await choose(setting as "model" | "effort" | "speed" | "variant", model);
    }
    choice = "";
  }
  // The phone's list wears shorter labels and Sentence-case captions; the select layout keeps the desktop strings.
  const phone = $derived(layout === "list");
  const PHONE_CAPTION: Record<string, string> = { opus: "Deep reasoning", fable: "Long tasks" };
  const caption = (value: string, detail?: string) => PHONE_CAPTION[value] ?? (detail ? detail[0].toUpperCase() + detail.slice(1) : undefined);
  type Row = { setting: "model" | "effort" | "speed" | "variant"; value: string; label: string; detail?: string };
  const modelRows = $derived.by((): Row[] => {
    if (agent === "opencode") return [{ setting: "model", value: "picker", label: "Choose available model…" }];
    const rows = modelsFor(agent).filter((m) => m.value).map((m): Row => {
      const [label, detail] = m.label.split(" — ");
      return { setting: "model", value: m.value, label, detail: phone ? caption(m.value, detail) : detail };
    });
    return agent === "claude" ? [{ setting: "model", value: "default", label: phone ? "Default" : "Agent default" }, ...rows] : rows;
  });
  const effortRows = $derived.by((): Row[] => {
    if (agent === "opencode") return [{ setting: "variant", value: "picker", label: "Choose model variant…" }];
    const base: Row[] = agent === "claude" ? [{ setting: "effort", value: "auto", label: phone ? "Default" : "Model default" }] : [{ setting: "effort", value: "none", label: "None" }, { setting: "effort", value: "minimal", label: "Minimal" }];
    const rest: Row[] = [["low", "Low"], ["medium", "Medium"], ["high", "High"], ["xhigh", "Extra high"], ["max", phone ? "Max" : agent === "claude" ? "Maximum (this session)" : "Maximum"]].map(([value, label]) => ({ setting: "effort", value, label }));
    return [...base, ...rest, ...(agent === "codex" ? [{ setting: "effort", value: "ultra", label: "Ultra" } as Row] : [])];
  });
  const speedRows: Row[] = [{ setting: "speed", value: "standard", label: "Standard" }, { setting: "speed", value: "fast", label: "Fast" }, { setting: "speed", value: "flex", label: "Flex" }];
  const activeModel = $derived(session?.model || (agent === "claude" ? "default" : ""));
  const validCustom = $derived(/^[A-Za-z0-9][A-Za-z0-9._:/@\[\]-]{0,99}$/.test(customModel));
</script>

{#snippet group(header: string, rows: Row[], label = true)}
  <div class="flex flex-col" role="group" aria-label={header}>
    {#if label}<span class="px-4 pt-4 pb-1.5 text-xs tracking-wide text-faint uppercase">{header}</span>{:else}<span class="pt-2"></span>{/if}
    <div class="model-group">
      {#each rows as r (r.setting + r.value)}
        {@const on = r.setting === "model" && r.value === activeModel}
        <button type="button" class="model-row" role="menuitemradio" aria-checked={r.setting === "model" ? on : undefined} {disabled} onclick={() => void choose(r.setting, r.value)}>
          <span class="min-w-0 flex-1 text-left"><span class="block truncate text-base">{r.label}</span>{#if r.detail}<span class="block truncate text-[13px] text-subtext">{r.detail}</span>{/if}</span>
          {#if on}<span class="model-check"><Icon name="check" size={18} /></span>{/if}
        </button>
      {/each}
    </div>
  </div>
{/snippet}

{#if visible && layout === "list"}
  <div class="flex flex-col" data-model-settings data-layout="list" aria-busy={pending}>
    {@render group("Model", modelRows, false)}
    {@render group("Effort", effortRows)}
    {#if agent === "codex"}{@render group("Speed", speedRows)}{/if}
    {#if locked}<p class="px-4 pt-2 text-sm text-faint">Change on your Mac.</p>
    {:else if !supported}<p class="px-4 pt-2 text-sm text-faint">{hint}</p>{/if}
    {#if talk.settingErrors[sessionId]}<p role="alert" class="px-4 pt-2 text-sm text-bad">{talk.settingErrors[sessionId]}</p>
    {:else if talk.settingNotices[sessionId]}<p role="status" class="px-4 pt-2 text-sm text-faint">{talk.settingNotices[sessionId]}</p>{/if}
  </div>
{:else if visible}
  <div class="model-settings flex shrink-0 items-center gap-1" data-model-settings aria-busy={pending}>
    <Select aria-label="Model settings" bind:value={choice} disabled={supported ? disabled : !store.alive} title={hint} minMenuWidth={240} onchange={(e) => void select(e.currentTarget.value)}>
      <option value="" disabled hidden>{pending ? "Setting…" : "Model"}</option>
      {#if supported}
        <optgroup label="Model">
          {#if agent === "opencode"}
            <option value="model=picker">Choose available model…</option>
          {:else}
            {#if agent === "claude"}<option value="model=default">Agent default</option>{/if}
            {#each modelsFor(agent).filter((m) => m.value) as model}
              {@const [name, description] = model.label.split(" — ")}
              <option value={"model=" + model.value} data-description={description}>{name}</option>
            {/each}
            <option value="custom">Custom model…</option>
          {/if}
        </optgroup>
        {#if agent === "opencode"}
          <optgroup label="Variant"><option value="variant=picker">Choose model variant…</option></optgroup>
        {:else}
          <optgroup label="Reasoning effort">
            {#if agent === "claude"}<option value="effort=auto">Model default</option>
            {:else}<option value="effort=none">None</option><option value="effort=minimal">Minimal</option>{/if}
            <option value="effort=low">Low</option>
            <option value="effort=medium">Medium</option>
            <option value="effort=high">High</option>
            <option value="effort=xhigh">Extra high</option>
            <option value="effort=max">{agent === "claude" ? "Maximum (this session)" : "Maximum"}</option>
            {#if agent === "codex"}<option value="effort=ultra">Ultra</option>{/if}
          </optgroup>
        {/if}
        {#if agent === "codex"}
          <optgroup label="Speed">
            <option value="speed=standard">Standard</option>
            <option value="speed=fast">Fast</option>
            <option value="speed=flex">Flex</option>
          </optgroup>
        {/if}
      {/if}
      <optgroup label="More">
        <option value="terminal">Open in terminal…</option>
      </optgroup>
    </Select>
    {#if custom}
      <input aria-label="Custom model" bind:value={customModel} {disabled} placeholder="Model ID" class="lola-field w-32 min-w-0"
        onkeydown={(e) => { if (e.key === "Enter") { e.preventDefault(); e.stopPropagation(); if (validCustom) void choose("model", customModel); } else if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); custom = false; } }} />
      <Button size="xs" disabled={disabled || !validCustom} onclick={() => choose("model", customModel)}>Set model</Button>
      <Button size="xs" aria-label="Cancel custom model" onclick={() => custom = false}>×</Button>
    {/if}
    {#if talk.settingErrors[sessionId]}
      <span role="alert"><span class="sr-only">{talk.settingErrors[sessionId]}</span><Button size="xs" class="text-bad!" title={talk.settingErrors[sessionId]} aria-label="Model setting failed — open terminal" onclick={openControls}>!</Button></span>
    {:else if talk.settingNotices[sessionId]}
      <span role="status"><span class="sr-only">{talk.settingNotices[sessionId]}</span><Button size="xs" title={talk.settingNotices[sessionId]} aria-label="Check model setting in terminal" onclick={openControls}>↗</Button></span>
    {/if}
  </div>
{/if}

<style>
  /* Phone list layout: an inset grouped list (16px gutter, 10px radius, hairlines between rows). */
  .model-group { margin-inline: 16px; overflow: hidden; border-radius: 10px; background: var(--color-edge-soft, var(--color-sel)); }
  .model-row { display: flex; width: 100%; align-items: center; gap: 12px; min-height: 44px; padding: 6px 16px; color: var(--color-ink); }
  .model-row + .model-row { border-top: 0.5px solid var(--color-edge); }
  .model-row:disabled { opacity: 0.4; }
  .model-check { flex-shrink: 0; color: var(--color-accent); }
  /* Shared select chrome sets width outside Tailwind's layers; scope the
     compact trigger here while the portalled options keep their own width. */
  .model-settings :global(.lola-select) { width: 68px; flex-shrink: 0; }
  .model-settings[aria-busy="true"] :global(.lola-select) { width: 92px; }
  .model-settings :global(.lola-field) { min-height: 24px; height: 24px; padding-block: 2px; }
  .model-settings :global(select) { padding-inline: 6px 24px; border-color: transparent; background: transparent; color: var(--color-ink-2); }
  .model-settings :global(.lola-select-caret) { right: 6px; }
</style>
