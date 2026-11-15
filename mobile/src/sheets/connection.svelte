<script lang="ts">
  import { onMount } from "svelte";
  import type { StatusResult } from "lola-transport";
  import Icon from "$lib/components/Icon.svelte";
  import ListGroup from "@mobile/lib/components/ListGroup.svelte";
  import ListRow from "@mobile/lib/components/ListRow.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import { connection } from "@mobile/lib/connection.svelte";
  import { copy } from "@mobile/lib/copy";
  import { router } from "@mobile/lib/router.svelte";
  import { remoteAccess } from "@mobile/lib/remoteaccess.svelte";
  import { diagnosticRows, interfaceLabel, pathWord, readStatus } from "@mobile/lib/settings.svelte";

  // Path details: where the connection runs, how fast, and (collapsed) what it
  // has carried. The relay's own state is never printed here: "Relay: None"
  // beside a Relay path says the opposite of what the row above it says.
  // Whether the relay COULD be used is its own row, "Away from Wi-Fi", which
  // speaks in sentences (lib/remoteaccess.svelte.ts), never in state words.
  let status = $state<StatusResult | null>(null);
  let open = $state(false);
  onMount(() => {
    void readStatus().then((s) => (status = s));
  });
  $effect(() => {
    void connection.epoch;
    void remoteAccess.load();
  });

  const path = $derived(connection.ready ? connection.path : null);
  const title = $derived(pathWord(connection.ready, path));
  const ms = $derived(connection.raceMs === null ? "" : `${connection.raceMs} ms`);
  // "Wi-Fi · 180 ms" on a direct path, "180 ms" over the relay.
  const secondary = $derived(
    [path?.kind === "relay" ? "" : interfaceLabel(path), ms].filter((p) => p !== "").join(" · "),
  );
  const diagnostics = $derived(diagnosticRows(status, connection.epoch, connection.raceMs));
</script>

<Sheet title={copy.connection.title} detent="fit" onclose={() => router.close()}>
  <div class="flex items-center gap-3 px-4 pb-4">
    <span class="flex size-10 shrink-0 items-center justify-center rounded-full bg-edge-soft text-accent" aria-hidden="true">
      <Icon name={path?.kind === "relay" ? "globe" : "wifi"} size={20} />
    </span>
    <div class="flex min-w-0 flex-col">
      <span class="text-title text-ink" data-hero>{title}</span>
      {#if secondary}<span class="text-callout text-subtext">{secondary}</span>{/if}
    </div>
  </div>

  <ListGroup>
    <ListRow label={copy.remoteAccess.label} value={remoteAccess.row.value} detail={remoteAccess.row.detail} />
  </ListGroup>

  <ListGroup>
    <ListRow label={copy.connection.diagnostics} chevron onclick={() => (open = !open)} />
    {#if open}
      {#each diagnostics as r (r.label)}
        <ListRow label={r.label} value={r.value} />
      {/each}
    {/if}
  </ListGroup>
</Sheet>
