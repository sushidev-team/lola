<script lang="ts">
  import { store } from "$lib/store.svelte";
  import type { Snippet } from "svelte";
  let { hints }: { hints?: Snippet } = $props();
</script>

<footer
  class="flex h-8 shrink-0 items-center gap-3 border-t border-edge/70 bg-canvas px-4 text-[11px] select-none"
>
  {#if store.flash}
    <span
      class:text-good={store.flash.kind === "good"}
      class:text-warn={store.flash.kind === "warn"}
      class:text-bad={store.flash.kind === "bad"}
      class="truncate">{store.flash.text}</span
    >
  {:else if !store.alive && store.connected}
    <span class="text-bad">daemon not running</span>
    <button
      class="rounded border border-edge px-2 py-[1px] text-ink hover:border-accent hover:text-accent"
      onclick={() => store.startDaemon()}>start daemon</button
    >
  {:else if hints}
    <span class="truncate text-faint">{@render hints()}</span>
  {/if}

  <span class="ml-auto flex items-center gap-2 text-faint">
    {#if store.alive}
      <button
        class="rounded px-1.5 py-[1px] hover:text-accent"
        title="restart daemon"
        onclick={() => store.restartDaemon()}>⟳ restart</button
      >
      <button
        class="rounded px-1.5 py-[1px] hover:text-bad"
        title="stop daemon"
        onclick={() => store.stopDaemon()}>■ stop</button
      >
    {/if}
  </span>
</footer>
