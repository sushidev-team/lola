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
  // Without a lock the children render bare, so the wrapper costs a form
  // nothing on a machine that has not joined a hub.
  import type { Snippet } from "svelte";
  import Icon from "./Icon.svelte";

  let { lock = null, version = 0, children }: {
    /** The locked key, or null when the policy does not carry it. */
    lock?: { key: string; label: string; value: string; local: string } | null;
    /** The applied policy's version. */
    version?: number;
    children: Snippet;
  } = $props();
  const id = $props.id();
</script>

{#if lock}
  <div class="grid min-w-0 gap-1.5" data-team-lock={lock.key}>
    <fieldset disabled aria-describedby={id} class="m-0 min-w-0 border-0 p-0 opacity-70">
      {@render children()}
    </fieldset>
    <p id={id} class="flex min-w-0 flex-wrap items-center gap-x-1.5 text-sm text-faint">
      <Icon name="lock" size={12} />
      <span>Set by team policy v{version}</span>
      {#if lock.value}<span class="min-w-0 break-words">· Team value: <span class="text-ink">{lock.value}</span></span>{/if}
    </p>
  </div>
{:else}
  {@render children()}
{/if}
