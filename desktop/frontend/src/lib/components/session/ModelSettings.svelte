<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { talk } from "$lib/talk.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { modelsFor } from "$lib/settingPresets";
  import Select from "../Select.svelte";
  import Button from "../Button.svelte";

  let { sessionId }: { sessionId: string } = $props();
  const session = $derived(store.sessionById(sessionId));
  const agent = $derived(session?.agent || "claude");
  const data = $derived(talk.state(sessionId));
  const supported = $derived(!!data?.caps.settings);
  const pending = $derived(!!talk.settingPending[sessionId]);
  const disabled = $derived(!store.alive || !supported || data?.composer !== "resting" || pending || !!talk.error(sessionId));
  const visible = $derived(session && !session.goalId && !["dead", "exited", "shell", "orphaned"].includes(session.agentState ?? ""));
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
  const validCustom = $derived(/^[A-Za-z0-9][A-Za-z0-9._:/@\[\]-]{0,99}$/.test(customModel));
</script>

{#if visible}
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
  /* Shared select chrome sets width outside Tailwind's layers; scope the
     compact trigger here while the portalled options keep their own width. */
  .model-settings :global(.lola-select) { width: 68px; flex-shrink: 0; }
  .model-settings[aria-busy="true"] :global(.lola-select) { width: 92px; }
  .model-settings :global(.lola-field) { min-height: 24px; height: 24px; padding-block: 2px; }
  .model-settings :global(select) { padding-inline: 6px 24px; border-color: transparent; background: transparent; color: var(--color-ink-2); }
  .model-settings :global(.lola-select-caret) { right: 6px; }
</style>
