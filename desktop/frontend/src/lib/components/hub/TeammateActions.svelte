<script lang="ts">
  // The two sharing decisions about one teammate's Mac, as icon buttons on its row:
  // "may attach to my sessions" and "I will attach to theirs", plus remove for both.
  // Pinning itself is confirmed in PinConfirm; the daemon applies every rule.
  import Button from "../Button.svelte";
  import Icon from "../Icon.svelte";
  import { share } from "$lib/share.svelte";
  import type { ShareMemberInfo } from "@bindings/internal/protocol";

  let { member: m, onpin }: { member: ShareMemberInfo; onpin: (dir: "allow" | "trust") => void } = $props();
</script>

<span class="flex items-center gap-0.5" data-teammate={m.deviceId}>
  <Button
    icon
    size="xs"
    selected={m.allowed}
    aria-pressed={m.allowed}
    aria-label={m.allowed ? `${m.name} may attach to your sessions` : `Allow ${m.name} to attach to your sessions`}
    title={m.allowed ? "May attach to your sessions. Press to stop." : "Allow to attach to your sessions"}
    disabled={!!share.busy[m.deviceId]}
    onclick={() => (m.allowed ? void share.untrust(m.deviceId, { allow: true }) : onpin("allow"))}
    data-allow
  ><Icon name="user-check" size={14} /></Button>
  <Button
    icon
    size="xs"
    selected={m.trusted}
    aria-pressed={m.trusted}
    aria-label={m.trusted ? `You trust ${m.name}'s sessions` : `Trust ${m.name}'s sessions`}
    title={m.trusted ? "You can attach to their sessions. Press to stop." : "Trust their sessions so you can attach"}
    disabled={!!share.busy[m.deviceId]}
    onclick={() => (m.trusted ? void share.untrust(m.deviceId, { trust: true }) : onpin("trust"))}
    data-trust
  ><Icon name="plug" size={14} /></Button>
  {#if m.allowed || m.trusted}
    <Button icon size="xs" variant="danger" aria-label={`Remove ${m.name}`} title="Remove in both directions" disabled={!!share.busy[m.deviceId]} onclick={() => void share.untrust(m.deviceId)} data-remove><Icon name="user-minus" size={14} /></Button>
  {/if}
</span>
