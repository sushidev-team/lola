<script lang="ts">
  import { store } from "$lib/store.svelte";
  import type { SessionInfo } from "@bindings/internal/protocol";
  import Button from "./Button.svelte";

  // Why a dev tab is dead, in the one case lola can both name and undo: another
  // process already holds the port the command wanted.
  //
  // It is a banner rather than a line in the terminal because the evidence is
  // NOT in the terminal — `wails3 dev` and `vite` clear the screen on their way
  // out, so the tab reads as "dead, no reason given" while the cause is a
  // process somewhere else on the machine. The daemon derives all of it
  // (internal/daemon/devclash.go); everything here is display.
  //
  // The action asks first (store.askFreePort → the shared confirm dialog): this
  // is the one control in the app that kills a process lola did not start.
  //
  // In the conversation it sits last in the tray and `tray` is set. Every other view mounts it under
  // the strip, and only those, so a clash is said once. Neutral in both places, because no colour
  // has "a dev port is taken" as its job.
  let { session, tray = false }: { session: SessionInfo; tray?: boolean } = $props();

  const clash = $derived(session.devClash);
</script>

{#if clash}
  <div class="flex shrink-0 items-center gap-3 border-edge bg-panel px-3 py-2 text-ink {tray ? 'rounded-[10px] border' : 'border-b'}" role="alert" data-card={tray ? "dev-clash" : undefined}>
    <span class="shrink-0 text-faint" aria-hidden="true">⚠</span>
    <span class="min-w-0 flex-1">
      {#if clash.command}<span class="font-mono text-sm">{clash.command}</span> stopped —{:else}A dev process stopped
        —{/if}
      port <span class="font-mono text-sm">{clash.port}</span> is held by
      <span class="font-mono text-sm">{clash.proc || "another process"}</span>
      <span class="text-sm text-faint">(pid {clash.pid})</span>.
      {#if clash.dir}
        <span class="selectable block truncate text-sm text-faint" title={clash.dir}>
          {clash.ours ? "A leftover server in" : "Started outside lola, in"}
          {clash.dir}
        </span>
      {/if}
    </span>
    <Button
      variant="secondary"
      size="xs"
      loading={store.devPending[session.id]}
      title={`stop ${clash.proc || "the process"} (pid ${clash.pid}) and start the dev processes here`}
      onclick={() => store.askFreePort(session.id)}
    >
      Free port {clash.port}
    </Button>
  </div>
{/if}
