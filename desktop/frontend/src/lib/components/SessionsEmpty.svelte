<script lang="ts">
  import { store } from "$lib/store.svelte";
  import type { Snippet } from "svelte";

  // The sessions panel is blank in three very different situations: the first
  // push hasn't landed (startup), the daemon is down, or it is simply idle.
  // Collapsing them into one "no sessions" line (the old behaviour) hid a dead
  // daemon behind what looked like an empty queue — and unlike the TUI the
  // desktop never auto-spawns it, so the recovery affordance has to be here.
  //
  // Reads the store directly (leaf component) — a view container can't thread
  // live connectivity flags in the production WKWebView. See WKWEBVIEW_REACTIVITY
  // in Cockpit.svelte. `idle` renders only in the genuinely-idle case.
  let { idle }: { idle?: Snippet } = $props();
</script>

{#if !store.connected}
  <!-- No push yet: stay neutral so a cold start never flashes "offline". -->
  <div class="flex h-full items-center justify-center px-4 py-8 text-center text-faint">
    connecting…
  </div>
{:else if !store.alive}
  <!-- Daemon is down. Say what happened and give the one recovery action. -->
  <div class="flex h-full flex-col items-center justify-center gap-2 px-6 py-8 text-center">
    <!-- text-xl is the ceiling and carries its own 600 — a hero empty state is
         the one place the app goes above 15px. -->
    <span class="text-xl text-bad">The lola daemon isn't running</span>
    <span class="copy text-sm text-faint">
      Nothing can be observed or spawned until it starts. It watches Linear and runs your coding
      agents.
    </span>
    <button
      class="mt-1 rounded bg-accent-fill px-3 py-1.5 font-medium text-accent-ink hover:bg-accent-fill-hover"
      onclick={() => store.startDaemon()}>Start the daemon</button
    >
  </div>
{:else if idle}
  {@render idle()}
{/if}
