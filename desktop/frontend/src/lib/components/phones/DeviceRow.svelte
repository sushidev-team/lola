<script lang="ts">
  import Button from "$lib/components/Button.svelte";
  import TierToggles from "./TierToggles.svelte";
  import { phones } from "$lib/phones.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { agoPhrase } from "$lib/time";
  import type { DeviceInfo } from "@bindings/internal/protocol";

  let { device }: { device: DeviceInfo } = $props();

  const presence = $derived(
    device.connected > 0
      ? `Connected · ${device.connected}`
      : device.lastSeenAt
        ? `Last seen ${agoPhrase(device.lastSeenAt)}`
        : "Never seen",
  );
  const busy = $derived(!!phones.busy[device.id]);

  function revoke() {
    confirm.ask({
      title: "Revoke phone?",
      subject: device.name,
      body: "It loses access now.",
      confirmLabel: "Revoke",
      onConfirm: () => void phones.revoke(device.id),
    });
  }
</script>

<li class="grid gap-2 py-3" aria-label={device.name}>
  <div class="flex min-w-0 items-baseline gap-3">
    <span class="min-w-0 truncate text-ink">{device.name}</span>
    <span class="font-mono text-sm text-faint">{device.fingerprint}</span>
    <span class="ml-auto shrink-0 text-sm text-faint">{presence}</span>
  </div>
  <div class="flex flex-wrap items-center gap-3">
    <TierToggles
      value={device.tiers ?? []}
      disabled={busy}
      onchange={(next) => void phones.setTiers(device.id, next)} />
    <Button variant="danger" size="sm" class="ml-auto" disabled={busy} onclick={revoke}>Revoke</Button>
  </div>
</li>
