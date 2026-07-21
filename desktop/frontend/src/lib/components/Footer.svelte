<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { updates } from "$lib/update.svelte";
  import { nav } from "$lib/nav.svelte";
  import type { Snippet } from "svelte";
  let { hints }: { hints?: Snippet } = $props();
</script>

<footer
  class="flex h-8 shrink-0 items-center gap-3 border-t border-edge/70 bg-canvas px-4 text-[11px] select-none"
>
  <!-- Daemon liveness lives here now (moved out of the top bar): a quiet pill,
       plus an inline "start" when it is down. -->
  {#if store.alive}
    <span class="flex items-center gap-1.5 text-good"><span>●</span>running</span>
  {:else if store.connected}
    <span class="flex items-center gap-1.5 text-bad"><span>○</span>down</span>
    <button
      class="rounded border border-edge px-2 py-[1px] text-ink hover:border-accent hover:text-accent-ink"
      onclick={() => store.startDaemon()}>start</button
    >
  {:else}
    <span class="flex items-center gap-1.5 text-faint"><span>○</span>connecting…</span>
  {/if}

  {#if store.flash}
    <span class="text-edge">·</span>
    <span
      class:text-good={store.flash.kind === "good"}
      class:text-warn={store.flash.kind === "warn"}
      class:text-bad={store.flash.kind === "bad"}
      class="truncate">{store.flash.text}</span
    >
  {:else if hints}
    <span class="text-edge">·</span>
    <span class="truncate text-faint">{@render hints()}</span>
  {/if}

  <span class="ml-auto flex items-center gap-2 text-faint">
    {#if store.alive}
      <button
        class="rounded px-1.5 py-[1px] hover:text-accent-ink"
        title="restart daemon"
        onclick={() => store.restartDaemon()}>⟳ restart</button
      >
      <button
        class="rounded px-1.5 py-[1px] hover:text-bad"
        title="stop daemon"
        onclick={() => store.stopDaemon()}>■ stop</button
      >
      <span class="text-edge">·</span>
    {/if}
    <!-- Version, and an update badge when a newer release is out. Clicking
         either opens the software-update overlay. -->
    {#if updates.available}
      <button
        class="rounded border border-accent px-1.5 py-[1px] text-accent-ink hover:opacity-90"
        title="update available: v{updates.info?.latestVersion}"
        onclick={() => nav.openOverlay("update")}>↑ update</button
      >
    {:else}
      <button
        class="rounded px-1.5 py-[1px] hover:text-ink"
        title="check for updates"
        onclick={() => nav.openOverlay("update")}>v{updates.version}</button
      >
    {/if}
  </span>
</footer>
