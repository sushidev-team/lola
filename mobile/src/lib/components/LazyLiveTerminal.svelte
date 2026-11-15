<script lang="ts">
  // Stands in for the desktop LiveTerminal in the phone bundle (vite.config.ts
  // `lazyLiveTerminal`). Shared desktop components import LiveTerminal statically,
  // which put xterm (157 KB gzip) in the Conversation segment's static graph
  // although the phone's own terminal is MobileTerminal. The real component still
  // loads, on first render, so no shared path loses its terminal.
  import type { Component } from "svelte";

  const props: Record<string, unknown> = $props();
  const real = import("$lib/components/LiveTerminal.svelte").then((m) => m.default as Component<Record<string, unknown>>);
</script>

{#await real then Real}
  <Real {...props} />
{/await}
