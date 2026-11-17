<script lang="ts">
  // A settings control the verified team policy LOCKS. The daemon overlays the
  // team's value onto its runtime config (internal/daemon/hubpolicy.go), so a
  // value typed here would have no effect while the policy holds — the control
  // is drawn disabled, with the team's value and the policy version beside it.
  //
  // The disable is a <fieldset disabled>: it reaches every native control the
  // children draw (inputs, <Checkbox>, <Select>, <Button>) without each one
  // needing a prop, and CSS `:disabled` styles them as it does any dead control.
  // config.toml keeps its own value; nothing here writes.
  //
  // A CEILING (budget, concurrency: the daemon runs min(local, team)) is not a
  // lock: the control stays editable, because a lower local value still
  // counts, and the note reads "Team ceiling N".
  //
  // Without a lock the children render bare, so the wrapper costs a form
  // nothing on a machine that has not joined a hub.
  import type { Snippet } from "svelte";
  import Icon from "./Icon.svelte";

  let { lock = null, version = 0, disable = true, children }: {
    /** The locked key, or null when the policy does not carry it. */
    lock?: { key: string; label: string; value: string; local: string; ceiling?: boolean } | null;
    /** The applied policy's version. */
    version?: number;
    /**
     * Disable every control inside. False when the policy locks only part of
     * the block (the review providers: which kinds are on, not their details);
     * the caller then disables just the locked controls itself.
     */
    disable?: boolean;
    children: Snippet;
  } = $props();
  const id = $props.id();
</script>

{#snippet note()}
  <p id={id} class="flex min-w-0 flex-wrap items-center gap-x-1.5 text-sm text-faint">
    <Icon name="lock" size={12} />
    {#if lock?.ceiling}
      <span class="text-ink">Team ceiling {lock.value} (policy v{version})</span>
      <span class="min-w-0 break-words">· The lower of yours and the team’s applies.</span>
    {:else}
      <span>Set by team policy v{version}</span>
      {#if lock?.value}<span class="min-w-0 break-words">· Team value: <span class="text-ink">{lock.value}</span></span>{/if}
    {/if}
  </p>
{/snippet}

{#if lock}
  <div class="grid min-w-0 gap-1.5" data-team-lock={lock.key}>
    {#if lock.ceiling}
      <div aria-describedby={id} class="min-w-0">{@render children()}</div>
      {@render note()}
    {:else if disable}
      <fieldset disabled aria-describedby={id} class="m-0 min-w-0 border-0 p-0 opacity-70">
        {@render children()}
      </fieldset>
      {@render note()}
    {:else}
      <!-- A partial lock: the note leads, so it is read before the controls. -->
      {@render note()}
      {@render children()}
    {/if}
  </div>
{:else}
  {@render children()}
{/if}
