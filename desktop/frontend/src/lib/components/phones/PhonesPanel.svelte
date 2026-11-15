<script lang="ts">
  import { onMount } from "svelte";
  import Button from "$lib/components/Button.svelte";
  import Checkbox from "$lib/components/Checkbox.svelte";
  import SettingsGroup from "$lib/components/SettingsGroup.svelte";
  import DeviceRow from "./DeviceRow.svelte";
  import PairDialog from "./PairDialog.svelte";
  import { phones } from "$lib/phones.svelte";
  import { hub } from "$lib/hub.svelte";

  // `relay` is SettingsDTO.remoteRelay: it saves with the rest of the form.
  let { relay = $bindable(false) }: { relay?: boolean } = $props();

  let pairing = $state(false);

  // Mounted only while the Phones tab is on screen, so this is the visibility gate.
  onMount(() => {
    const stopPhones = phones.watch();
    const stopHub = hub.watch();
    return () => {
      stopPhones();
      stopHub();
    };
  });

  const RELAY_STATE: Record<string, string> = {
    off: "Off",
    syncing: "Syncing",
    connecting: "Connecting",
    connected: "Connected",
    backoff: "Reconnecting",
    unsupported: "Hub too old",
    refused: "Hub refused this Mac",
  };
  const relayText = $derived.by(() => {
    const r = hub.status?.relay;
    return r ? (RELAY_STATE[r.state] ?? r.state) : "Not available";
  });
</script>

<SettingsGroup title="Pair a phone">
  <div>
    <Button variant="primary" size="md" onclick={() => (pairing = true)}>Pair a phone</Button>
  </div>
</SettingsGroup>

<SettingsGroup title="Paired phones">
  {#if phones.problem}
    <p class="text-bad">{phones.problem}</p>
  {/if}
  {#if phones.error}
    <p class="text-bad">{phones.error}</p>
  {/if}
  {#if phones.devices.length}
    <ul class="divide-y divide-edge/60">
      {#each phones.devices as d (d.id)}
        <DeviceRow device={d} />
      {/each}
    </ul>
  {:else if phones.loaded && !phones.problem}
    <p class="text-faint">No phones paired.</p>
  {/if}
</SettingsGroup>

<SettingsGroup title="Relay">
  <label class="flex cursor-pointer items-center gap-2">
    <Checkbox bind:checked={relay} />
    <span>Reach this Mac away from home</span>
  </label>
  <p class="text-sm text-faint">Relay status: {relayText}</p>
</SettingsGroup>

{#if pairing}
  <PairDialog onClose={() => (pairing = false)} />
{/if}
