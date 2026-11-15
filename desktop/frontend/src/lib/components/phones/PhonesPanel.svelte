<script lang="ts">
  import { onMount } from "svelte";
  import Button from "$lib/components/Button.svelte";
  import Checkbox from "$lib/components/Checkbox.svelte";
  import SettingsGroup from "$lib/components/SettingsGroup.svelte";
  import DeviceRow from "./DeviceRow.svelte";
  import PairDialog from "./PairDialog.svelte";
  import { phones } from "$lib/phones.svelte";
  import { hub } from "$lib/hub.svelte";
  import { summarizeRelay } from "$lib/phonerelay";

  // `relay` is SettingsDTO.remoteRelay: it saves with the rest of the form.
  // `onJoinHub` opens the Team hub tab; without it the join hint is text only.
  let { relay = $bindable(false), onJoinHub }: { relay?: boolean; onJoinHub?: () => void } = $props();

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

  const summary = $derived(summarizeRelay(hub.status));
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

<SettingsGroup
  title="Away from home"
  description="Phones off your network reach this Mac through your team hub. It needs a joined team hub; the hub only passes encrypted traffic along.">
  <label class="flex cursor-pointer items-center gap-2">
    <Checkbox bind:checked={relay} />
    <span>Reach this Mac away from home</span>
  </label>
  <div class="grid gap-1" role="status" aria-label="Relay status">
    <p class={summary.ok ? "text-sm text-good" : "text-sm text-ink"}>{summary.title}</p>
    <p class="text-sm text-faint">{summary.detail}</p>
  </div>
  {#if summary.action === "join" && onJoinHub}
    <div>
      <Button size="md" onclick={onJoinHub}>Open Team hub settings</Button>
    </div>
  {/if}
</SettingsGroup>

{#if pairing}
  <PairDialog onClose={() => (pairing = false)} />
{/if}
