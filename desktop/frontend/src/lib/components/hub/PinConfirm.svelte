<script lang="ts">
  // The comparison a person makes before pinning a teammate's Mac: the fingerprint is
  // shown, they check it against the one on that Mac, and only then is it sent. The
  // daemon re-reads their key at that moment and refuses a changed one.
  //
  // A refresh that changes the fingerprint under an open comparison voids it: the box is
  // cleared and the person compares the new one (what is sent is always the compared one).
  import { untrack } from "svelte";
  import Button from "../Button.svelte";
  import Checkbox from "../Checkbox.svelte";
  import { share } from "$lib/share.svelte";
  import type { ShareMemberInfo } from "@bindings/internal/protocol";

  let { member: m, allow, ondone }: { member: ShareMemberInfo; allow: boolean; ondone: () => void } = $props();

  let compared = $state(untrack(() => m.fingerprint));
  let matches = $state(false);
  let changed = $state(false);

  $effect(() => {
    const now = m.fingerprint;
    if (now !== untrack(() => compared))
      untrack(() => {
        compared = now;
        matches = false;
        changed = true;
      });
  });

  async function confirm() {
    if (!matches || compared !== m.fingerprint) return;
    // The fingerprint the person compared, not whatever a refresh has put there since.
    const ok = await share.trust(m.deviceId, compared, { allow, trust: !allow });
    if (ok) ondone();
  }
</script>

<div class="grid gap-2 rounded-md border border-edge px-3 py-2" data-pin-confirm>
  <p class="text-sm text-ink-2">Check that this matches the fingerprint {m.name.split("·")[1]?.trim() || "their Mac"} shows in lola.</p>
  <p class="selectable break-all font-mono text-ink" data-pin-fingerprint>{m.fingerprint}</p>
  {#if changed}<p role="alert" class="text-sm text-warn" data-pin-changed>The fingerprint changed while you were comparing. Compare the new one.</p>{/if}
  <label class="flex items-center gap-2 text-sm text-ink-2"><Checkbox bind:checked={matches} /> It matches</label>
  <span class="flex gap-2">
    <Button size="sm" variant="primary" disabled={!matches} loading={!!share.busy[m.deviceId]} onclick={() => void confirm()} data-pin-confirm-button>{allow ? "Allow" : "Trust"}</Button>
    <Button size="sm" onclick={ondone}>Cancel</Button>
  </span>
</div>
